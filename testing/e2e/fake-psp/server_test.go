package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestServer(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	if cfg.TimeoutHold == 0 {
		cfg.TimeoutHold = 300 * time.Millisecond
	}
	s := NewServer(cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func body(ref string, amount string) string {
	return `{"reference_id":"` + ref + `","narration":"ZRD:c-1","amount":` + amount +
		`,"mode":"IMPS","beneficiary":{"name":"E2E Payee","account_number":"000000000001","ifsc":"HDFC0000001"}}`
}

func post(t *testing.T, c *http.Client, url, b string, hdr map[string]string) (int, map[string]any, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, nil
}

func code(m map[string]any) string {
	s, _ := m["code"].(string)
	return s
}

func TestParseIntegerAmount(t *testing.T) {
	cases := []struct {
		tok  string
		want int64
		code string
	}{
		{`10050`, 10050, ""},
		{`1`, 1, ""},
		{`10000010`, 10000010, ""},
		{`9007199254740993`, 9007199254740993, ""}, // > 2^53: must not go through float64
		{`100.50`, 0, "amount_not_integer"},
		{`100.0`, 0, "amount_not_integer"},
		{`1e4`, 0, "amount_not_integer"},
		{`1E4`, 0, "amount_not_integer"},
		{`"10050"`, 0, "amount_not_integer"},
		{`true`, 0, "amount_not_integer"},
		{`99999999999999999999`, 0, "amount_not_integer"},
		{`0`, 0, "amount_not_positive"},
		{`-5`, 0, "amount_not_positive"},
		{``, 0, "amount_required"},
		{`null`, 0, "amount_required"},
	}
	for _, c := range cases {
		got, code := ParseIntegerAmount(json.RawMessage(c.tok))
		if got != c.want || code != c.code {
			t.Errorf("ParseIntegerAmount(%q) = %d,%q want %d,%q", c.tok, got, code, c.want, c.code)
		}
	}
}

func TestCreatePayout_IntegerValidationOnTheWire(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	for _, amt := range []string{`100.50`, `"10050"`, `1.005e4`, `10050.0`} {
		st, m, err := post(t, ts.Client(), ts.URL+"/v1/payouts", body("ref-float", amt), nil)
		if err != nil || st != http.StatusBadRequest || code(m) != "amount_not_integer" {
			t.Fatalf("amount %s: status=%d code=%q err=%v", amt, st, code(m), err)
		}
	}
	st, m, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("ref-int", `10050`), nil)
	if st != http.StatusOK || m["payout_id"] != "pout_fake_ref-int" || m["status"] != "pending" || m["reference_id"] != "ref-int" {
		t.Fatalf("integer amount: status=%d body=%v", st, m)
	}
}

func TestCreatePayout_RequiredFields(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	st, m, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", `{"amount":100,"mode":"IMPS","beneficiary":{"name":"a","account_number":"1"}}`, nil)
	if st != 400 || code(m) != "reference_id_required" {
		t.Fatalf("missing reference_id: %d %v", st, m)
	}
	st, m, _ = post(t, ts.Client(), ts.URL+"/v1/payouts", `{"reference_id":"r1","amount":100,"mode":"IMPS"}`, nil)
	if st != 422 || code(m) != "beneficiary_required" {
		t.Fatalf("missing beneficiary: %d %v", st, m)
	}
	st, m, _ = post(t, ts.Client(), ts.URL+"/v1/payouts", `not json`, nil)
	if st != 400 || code(m) != "invalid_json" {
		t.Fatalf("bad json: %d %v", st, m)
	}
}

func TestIdempotency_SameBodySamePayout_DifferentBody409(t *testing.T) {
	s, ts := newTestServer(t, Config{})
	h := map[string]string{"X-Payout-Idempotency": "disp-1"}
	st1, m1, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("disp-1", "10050"), h)
	// Same body with different whitespace is still the same body.
	st2, m2, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", strings.ReplaceAll(body("disp-1", "10050"), ",", ", "), h)
	if st1 != 200 || st2 != 200 || m1["payout_id"] != m2["payout_id"] {
		t.Fatalf("replay: %d %v / %d %v", st1, m1, st2, m2)
	}
	st3, m3, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("disp-1", "10051"), h)
	if st3 != http.StatusConflict || code(m3) != "idempotency_conflict" {
		t.Fatalf("conflict: %d %v", st3, m3)
	}
	s.mu.Lock()
	n := len(s.payouts)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("payouts stored = %d, want 1", n)
	}
	// Fallback key is reference_id when the header is absent.
	st4, m4, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("ref-nohdr", "200"), nil)
	st5, m5, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("ref-nohdr", "300"), nil)
	if st4 != 200 || st5 != 409 || code(m5) != "idempotency_conflict" {
		t.Fatalf("fallback key: %d %v / %d %v", st4, m4, st5, m5)
	}
}

func TestIdempotency_ConcurrentDuplicates(t *testing.T) {
	s, ts := newTestServer(t, Config{})
	var wg sync.WaitGroup
	ids := make(chan any, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, m, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("conc-1", "500"), map[string]string{"X-Payout-Idempotency": "conc-1"})
			ids <- m["payout_id"]
		}()
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		if id != "pout_fake_conc-1" {
			t.Fatalf("got payout_id %v", id)
		}
	}
	if len(s.payouts) != 1 {
		t.Fatalf("payouts = %d", len(s.payouts))
	}
}

func TestFailureModes_ByAmount(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	cases := []struct {
		amount string
		status int
		code   string
	}{
		{"10013", 500, "psp_internal_error"},
		{"10022", 422, "hard_decline"},
		{"10029", 429, "rate_limited"},
	}
	for _, c := range cases {
		st, m, err := post(t, ts.Client(), ts.URL+"/v1/payouts", body("fm-"+c.amount, c.amount), nil)
		if err != nil || st != c.status || code(m) != c.code {
			t.Errorf("amount %s: status=%d code=%q err=%v", c.amount, st, code(m), err)
		}
	}
	// A failed attempt stores nothing, so a GET finds no payout.
	resp, _ := ts.Client().Get(ts.URL + "/v1/payouts?reference_id=fm-10013")
	if resp.StatusCode != 404 {
		t.Errorf("GET after 500: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestFailureMode_Timeout(t *testing.T) {
	_, ts := newTestServer(t, Config{TimeoutHold: 400 * time.Millisecond})
	c := &http.Client{Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, _, err := post(t, c, ts.URL+"/v1/payouts", body("to-1", "10099"), nil)
	if err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("want client timeout, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout test took too long")
	}
	// A patient client eventually gets 504 after the hold.
	st, m, err := post(t, ts.Client(), ts.URL+"/v1/payouts", body("to-2", "10099"), nil)
	if err != nil || st != http.StatusGatewayTimeout || code(m) != "psp_timeout" {
		t.Fatalf("after hold: %d %v %v", st, m, err)
	}
}

func TestTimeoutHold_DefaultsFromRelayTimeout(t *testing.T) {
	t.Setenv("RELAY_PSP_TIMEOUT_SECONDS", "5")
	t.Setenv("FAKE_PSP_TIMEOUT_HOLD_MS", "")
	if got := ConfigFromEnv().TimeoutHold; got != 10*time.Second {
		t.Fatalf("hold = %s, want 10s", got)
	}
	t.Setenv("FAKE_PSP_TIMEOUT_HOLD_MS", "250")
	if got := ConfigFromEnv().TimeoutHold; got != 250*time.Millisecond {
		t.Fatalf("override hold = %s", got)
	}
}

func TestFailureMode_Drop(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	st, _, err := post(t, ts.Client(), ts.URL+"/v1/payouts", body("drop-1", "10044"), nil)
	if err == nil {
		t.Fatalf("want connection error, got status %d", st)
	}
	if !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "reset") {
		t.Logf("drop error: %v", err)
	}
}

func TestAdminMode_Override(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	st, _, _ := post(t, ts.Client(), ts.URL+"/_admin/mode", `{"reference_id":"ov-1","mode":"500"}`, nil)
	if st != 200 {
		t.Fatalf("set mode: %d", st)
	}
	if st, _, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("ov-1", "10050"), nil); st != 500 {
		t.Fatalf("override 500: %d", st)
	}
	post(t, ts.Client(), ts.URL+"/_admin/mode", `{"reference_id":"ov-2","mode":"ok"}`, nil)
	if st, _, _ := post(t, ts.Client(), ts.URL+"/v1/payouts", body("ov-2", "10013"), nil); st != 200 {
		t.Fatalf("override ok: %d", st)
	}
	if st, _, _ := post(t, ts.Client(), ts.URL+"/_admin/mode", `{"reference_id":"x","mode":"bogus"}`, nil); st != 400 {
		t.Fatalf("bogus mode: %d", st)
	}
}

func TestGetByReference_AndRequestsRecorded(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	raw := body("rec-1", "1")
	post(t, ts.Client(), ts.URL+"/v1/payouts", raw, map[string]string{"X-Payout-Idempotency": "rec-1"})
	resp, _ := ts.Client().Get(ts.URL + "/v1/payouts?reference_id=rec-1")
	var p map[string]any
	json.NewDecoder(resp.Body).Decode(&p)
	resp.Body.Close()
	if resp.StatusCode != 200 || p["payout_id"] != "pout_fake_rec-1" {
		t.Fatalf("GET: %d %v", resp.StatusCode, p)
	}
	resp, _ = ts.Client().Get(ts.URL + "/v1/payouts?reference_id=nope")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("GET unknown: %d", resp.StatusCode)
	}
	resp, _ = ts.Client().Get(ts.URL + "/_admin/requests?reference_id=rec-1&path=/v1/payouts")
	var recs []RecordedRequest
	json.NewDecoder(resp.Body).Decode(&recs)
	resp.Body.Close()
	posts := 0
	for _, r := range recs {
		if r.Method == http.MethodPost {
			posts++
			if r.RawBody != raw {
				t.Fatalf("raw body not preserved: %q", r.RawBody)
			}
			if got := r.Headers["X-Payout-Idempotency"]; len(got) != 1 || got[0] != "rec-1" {
				t.Fatalf("header not recorded: %v", r.Headers)
			}
			if !strings.Contains(r.RawBody, `"amount":1,`) {
				t.Fatalf("amount not integer on the wire: %s", r.RawBody)
			}
		}
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

type captured struct {
	mu      sync.Mutex
	path    string
	headers http.Header
	body    []byte
	n       int
}

func edgeStub(t *testing.T, status int) (*httptest.Server, *captured) {
	c := &captured{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.path, c.headers, c.body = r.URL.Path, r.Header.Clone(), b
		c.n++
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts, c
}

func TestWebhook_HMACSignatureAndBody(t *testing.T) {
	edge, cap := edgeStub(t, 200)
	s, ts := newTestServer(t, Config{EdgeURL: edge.URL})
	secret := "whsec-test-only"
	if st, _, _ := post(t, ts.Client(), ts.URL+"/_admin/config", `{"connector_id":"conn-1","webhook_secret":"`+secret+`"}`, nil); st != 200 {
		t.Fatalf("config: %d", st)
	}
	// The config read-back never exposes the secret.
	resp, _ := ts.Client().Get(ts.URL + "/_admin/config")
	cfgRaw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(cfgRaw, []byte(secret)) {
		t.Fatal("secret leaked by GET /_admin/config")
	}
	post(t, ts.Client(), ts.URL+"/v1/payouts", body("wh-1", "10050"), nil)
	st, m, _ := post(t, ts.Client(), ts.URL+"/_admin/webhook/pout_fake_wh-1?event=payout.processed", "", nil)
	if st != 200 || m["status_code"].(float64) != 200 {
		t.Fatalf("admin webhook: %d %v", st, m)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.path != "/v1/webhooks/razorpay/conn-1" {
		t.Fatalf("path %s", cap.path)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(cap.body)
	if want := hex.EncodeToString(mac.Sum(nil)); cap.headers.Get("X-Razorpay-Signature") != want {
		t.Fatalf("signature mismatch")
	}
	if Sign(cap.body, secret) != cap.headers.Get("X-Razorpay-Signature") {
		t.Fatal("Sign() mismatch")
	}
	if !strings.HasPrefix(cap.headers.Get("X-Razorpay-Event-Id"), "evt_fake_") {
		t.Fatalf("event id %q", cap.headers.Get("X-Razorpay-Event-Id"))
	}
	var ev struct {
		Entity  string `json:"entity"`
		Event   string `json:"event"`
		Payload struct {
			Payout struct {
				Entity struct {
					ID          string          `json:"id"`
					Amount      json.RawMessage `json:"amount"`
					Currency    string          `json:"currency"`
					Status      string          `json:"status"`
					Mode        string          `json:"mode"`
					UTR         string          `json:"utr"`
					ReferenceID string          `json:"reference_id"`
				} `json:"entity"`
			} `json:"payout"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(cap.body, &ev); err != nil {
		t.Fatal(err)
	}
	e := ev.Payload.Payout.Entity
	if ev.Entity != "event" || ev.Event != "payout.processed" || e.ID != "pout_fake_wh-1" || string(e.Amount) != "10050" ||
		e.Currency != "INR" || e.Status != "processed" || e.Mode != "IMPS" || e.ReferenceID != "wh-1" ||
		len(e.UTR) != 19 || !strings.HasPrefix(e.UTR, "FAKEUTR") || e.UTR != FakeUTR("wh-1") {
		t.Fatalf("unexpected body: %s", cap.body)
	}
	s.mu.Lock()
	if s.byID["pout_fake_wh-1"].Status != "processed" {
		t.Fatal("payout status not updated")
	}
	s.mu.Unlock()
}

func TestWebhook_AutoAfterSuccess(t *testing.T) {
	edge, cap := edgeStub(t, 200)
	_, ts := newTestServer(t, Config{EdgeURL: edge.URL, AutoWebhookMS: 20, ConnectorID: "conn-a", WebhookSecret: "s"})
	post(t, ts.Client(), ts.URL+"/v1/payouts", body("auto-1", "1"), nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cap.mu.Lock()
		n := cap.n
		cap.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("auto webhook not delivered")
}

func TestWebhook_UnknownPayoutAndUnconfigured(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	if st, _, _ := post(t, ts.Client(), ts.URL+"/_admin/webhook/pout_fake_missing", "", nil); st != 404 {
		t.Fatalf("unknown payout: %d", st)
	}
	post(t, ts.Client(), ts.URL+"/v1/payouts", body("unc-1", "1"), nil)
	if st, _, _ := post(t, ts.Client(), ts.URL+"/_admin/webhook/pout_fake_unc-1", "", nil); st != 409 {
		t.Fatalf("unconfigured: %d", st)
	}
}

func TestReset(t *testing.T) {
	s, ts := newTestServer(t, Config{ConnectorID: "c", WebhookSecret: "s"})
	post(t, ts.Client(), ts.URL+"/v1/payouts", body("rs-1", "1"), nil)
	post(t, ts.Client(), ts.URL+"/_admin/reset", "", nil)
	s.mu.Lock()
	if len(s.payouts) != 0 || len(s.requests) != 0 || s.cfg.ConnectorID != "c" {
		t.Fatal("reset did not clear state or dropped config")
	}
	s.mu.Unlock()
	post(t, ts.Client(), ts.URL+"/_admin/reset?all=1", "", nil)
	if s.cfg.WebhookSecret != "" {
		t.Fatal("reset?all=1 kept secret")
	}
}

func TestDetokenizeStub(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	st, m, _ := post(t, ts.Client(), ts.URL+"/v1/detokenize", `{"account_number":"tok_abc","name":"tok_def","ifsc":"HDFC0000001"}`, nil)
	if st != 200 || m["account_number"] != "000000000001" || m["name"] != "E2E Payee" || m["ifsc"] != "HDFC0000001" {
		t.Fatalf("detokenize: %d %v", st, m)
	}
	resp, _ := ts.Client().Get(ts.URL + "/health")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health %d", resp.StatusCode)
	}
}
