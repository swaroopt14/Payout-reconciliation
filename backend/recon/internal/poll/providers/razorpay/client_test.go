package razorpay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		BaseURL:     "http://localhost:0", // overridden by test server
		KeyID:       "rzp_test_FAKEKEYID",
		KeySecret:   "fake_key_secret_not_real",
		Mode:        ModeTest,
		Timeout:     10 * time.Second,
		MaxRetries:  3,
		BaseDelay:   10 * time.Millisecond, // fast for tests
		MaxPageSize: 100,
	}
}

// --- Basic Auth Tests ---

func TestBasicAuthHeader(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":0,"items":[]}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	// Verify Basic Auth
	expectedCred := cfg.KeyID + ":" + cfg.KeySecret
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(expectedCred))
	if capturedAuth != expectedAuth {
		t.Errorf("Basic Auth header mismatch.\nExpected: %s\nGot:      %s", expectedAuth, capturedAuth)
	}
}

func TestHeadersPresent(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":0,"items":[]}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if capturedHeaders.Get("Accept") != "application/json" {
		t.Error("Accept header missing or wrong")
	}
	if capturedHeaders.Get("User-Agent") != "zord-connector/1.0" {
		t.Error("User-Agent header missing or wrong")
	}
}

func TestCorrectMethodAndPath(t *testing.T) {
	var capturedMethod, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":0,"items":[]}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if capturedMethod != "GET" {
		t.Errorf("expected GET, got %s", capturedMethod)
	}
	if capturedPath != "/payments" {
		t.Errorf("expected /payments, got %s", capturedPath)
	}
}

// --- Response Decode Tests ---

func Test200DecodesPaymentResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"id":"pay_xxx","entity":"payment","amount":50000,"currency":"INR","status":"captured","captured":true,"created_at":1700000000}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	var payment PaymentResponse
	err := client.do(ctx, http.MethodGet, "/payments/pay_xxx", nil, &payment)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payment.ID != "pay_xxx" {
		t.Errorf("expected pay_xxx, got %s", payment.ID)
	}
	if payment.Amount != 50000 {
		t.Errorf("expected 50000, got %d", payment.Amount)
	}
	if payment.Currency != "INR" {
		t.Errorf("expected INR, got %s", payment.Currency)
	}
}

// --- Error Classification Tests ---

func Test400ReturnsBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprintf(w, `{"error":{"code":"BAD_REQUEST","description":"Invalid params"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	err := client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if err == nil {
		t.Fatal("expected error for 400")
	}
	pErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if pErr.Kind != ErrBadRequest {
		t.Errorf("expected bad_request, got %s", pErr.Kind)
	}
	if pErr.Retryable {
		t.Error("400 should not be retryable")
	}
}

func Test401ReturnsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"code":"UNAUTHORIZED","description":"Invalid key"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	err := client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if err == nil {
		t.Fatal("expected error for 401")
	}
	pErr := err.(*ProviderError)
	if pErr.Kind != ErrUnauthorized {
		t.Errorf("expected unauthorized, got %s", pErr.Kind)
	}
	if pErr.Retryable {
		t.Error("401 should not be retryable")
	}
}

func Test403ReturnsForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprintf(w, `{"error":{"code":"FORBIDDEN","description":"No access"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	err := client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if err == nil {
		t.Fatal("expected error for 403")
	}
	pErr := err.(*ProviderError)
	if pErr.Kind != ErrForbidden {
		t.Errorf("expected forbidden, got %s", pErr.Kind)
	}
	if pErr.Retryable {
		t.Error("403 should not be retryable")
	}
}

func Test404ReturnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprintf(w, `{"error":{"code":"NOT_FOUND","description":"Not found"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	err := client.do(ctx, http.MethodGet, "/payments/pay_xxx", nil, nil)

	if err == nil {
		t.Fatal("expected error for 404")
	}
	pErr := err.(*ProviderError)
	if pErr.Kind != ErrNotFound {
		t.Errorf("expected not_found, got %s", pErr.Kind)
	}
	if pErr.Retryable {
		t.Error("404 should not be retryable")
	}
}

// --- Retry Tests ---

func Test429RetriesWithBackoff(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			fmt.Fprintf(w, `{"error":{"code":"RATE_LIMITED"}}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":0,"items":[]}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	err := client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func Test500RetriesUpToLimit(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"error":{"code":"INTERNAL_ERROR"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	err := client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// 1 initial + 3 retries = 4 total
	if attempts.Load() != 4 {
		t.Errorf("expected 4 attempts (1 + 3 retries), got %d", attempts.Load())
	}
}

func TestNoRetryOn400(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(400)
		fmt.Fprintf(w, `{"error":{"code":"BAD_REQUEST"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if attempts.Load() != 1 {
		t.Errorf("400 should not be retried, got %d attempts", attempts.Load())
	}
}

func TestNoRetryOn401(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"code":"UNAUTHORIZED"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if attempts.Load() != 1 {
		t.Errorf("401 should not be retried, got %d attempts", attempts.Load())
	}
}

func TestNoRetryOn403(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(403)
		fmt.Fprintf(w, `{"error":{"code":"FORBIDDEN"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	if attempts.Load() != 1 {
		t.Errorf("403 should not be retried, got %d attempts", attempts.Load())
	}
}

// --- Context Cancellation ---

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"error":{}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := client.do(ctx, http.MethodGet, "/payments", nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestContextDeadlinePreventsRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"error":{}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	cfg.BaseDelay = 200 * time.Millisecond // slower retries
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = client.do(ctx, http.MethodGet, "/payments", nil, nil)

	// Should stop early due to deadline (1 initial + maybe 1 retry)
	total := attempts.Load()
	if total > 2 {
		t.Errorf("expected at most 2 attempts before 50ms deadline, got %d", total)
	}
}

// --- Invalid JSON ---

func TestInvalidJSONReturnsDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `not json at all`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	var out PaymentResponse
	err := client.do(ctx, http.MethodGet, "/payments", nil, &out)

	if err == nil {
		t.Fatal("expected decode error for invalid JSON")
	}
	pErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Kind != ErrDecode {
		t.Errorf("expected decode_error, got %s", pErr.Kind)
	}
}

// --- Health Check Tests ---

func TestHealthCheckSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":0,"items":[]}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	result, err := client.HealthCheck(ctx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "healthy" {
		t.Errorf("expected healthy, got %s", result.Status)
	}
	if result.Provider != "razorpay" {
		t.Error("provider should be razorpay")
	}
	if result.Mode != "test" {
		t.Error("mode should be test")
	}
}

func TestHealthCheckUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"code":"UNAUTHORIZED"}}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	result, err := client.HealthCheck(ctx)

	if err != nil {
		t.Fatalf("health check should not return Go error: %v", err)
	}
	if result.Status != "unauthorized" {
		t.Errorf("expected unauthorized, got %s", result.Status)
	}
	if result.ErrorCode != "RAZORPAY_AUTH_FAILED" {
		t.Errorf("expected RAZORPAY_AUTH_FAILED, got %s", result.ErrorCode)
	}
}

// --- List Payments ---

func TestListPaymentsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		resp := ListResponse[PaymentResponse]{
			Entity: "collection",
			Count:  2,
			Items: []PaymentResponse{
				{ID: "pay_1", Amount: 10000, Currency: "INR", Status: "captured"},
				{ID: "pay_2", Amount: 20000, Currency: "INR", Status: "authorized"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	window := TimeWindow{
		From: time.Now().Add(-24 * time.Hour),
		To:   time.Now(),
	}
	payments, err := client.ListPayments(ctx, window, SkipCount{Skip: 0, Count: 100})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payments) != 2 {
		t.Fatalf("expected 2 payments, got %d", len(payments))
	}
	if payments[0].ID != "pay_1" {
		t.Errorf("expected pay_1, got %s", payments[0].ID)
	}
}

// --- FetchPayment ---

func TestFetchPaymentSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payments/pay_123" {
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":{"code":"NOT_FOUND"}}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"id":"pay_123","entity":"payment","amount":75000,"currency":"INR","status":"captured","captured":true}`)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, _ := NewClient(cfg, nil, nil, nil)

	ctx := context.Background()
	payment, err := client.FetchPayment(ctx, "pay_123")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payment.ID != "pay_123" {
		t.Errorf("expected pay_123, got %s", payment.ID)
	}
	if payment.Amount != 75000 {
		t.Errorf("expected 75000, got %d", payment.Amount)
	}
}

// --- Client Validation ---

func TestNewClientInvalidConfig(t *testing.T) {
	_, err := NewClient(Config{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestNewClientNilLogger(t *testing.T) {
	cfg := validTestConfig()
	client, err := NewClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Error("client should not be nil")
	}
}
