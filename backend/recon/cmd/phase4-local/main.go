package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"zord-outcome-engine/handlers"
	"zord-outcome-engine/internal/observe"
	"zord-outcome-engine/internal/persistence"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/models"
	"zord-outcome-engine/routes"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	_ = godotenv.Load()
	_ = godotenv.Load(".env")

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = "postgres://postgres@127.0.0.1:5433/zord_outcome_phase3?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fail("db open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		fail("db ping: %v", err)
	}

	store := persistence.NewSQLStore(db)
	obs := observe.NewProcessor(store)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	paymentID := "pay_local_" + suffix
	tenant := "11111111-1111-1111-1111-111111111111"
	connector := "22222222-2222-2222-2222-222222222222"

	mustApply(obs, envelope(tenant, connector, paymentID, "evt_auth_"+suffix, "payment.authorized", "authorized", false))
	mustApply(obs, envelope(tenant, connector, paymentID, "evt_cap_"+suffix, "payment.captured", "captured", true))
	mustApply(obs, envelope(tenant, connector, paymentID, "evt_late_"+suffix, "payment.authorized", "authorized", false))
	dup := mustApply(obs, envelope(tenant, connector, paymentID, "evt_cap_"+suffix, "payment.captured", "captured", true))
	if dup != observe.ResultDuplicate {
		fail("expected duplicate on replay, got %s", dup)
	}

	pay, ok, err := store.GetCanonicalPayment(ctx, tenant, connector, paymentID)
	if err != nil || !ok {
		fail("canonical lookup ok=%v err=%v", ok, err)
	}
	if pay.CanonicalStatus != "captured" {
		fail("late authorized regressed canonical_status=%s", pay.CanonicalStatus)
	}
	if !pay.Captured {
		fail("expected captured=true")
	}

	var events int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM provider_payment_observation_events
		WHERE tenant_id=$1 AND connector_id=$2 AND payment_id=$3`,
		tenant, connector, paymentID,
	).Scan(&events); err != nil {
		fail("count events: %v", err)
	}
	if events < 3 {
		fail("expected at least 3 observation events, got %d", events)
	}

	os.Setenv("RELAY_AUTH_TOKEN", "phase4-local-token")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	routes.PaymentRoutes(r, &handlers.PaymentHandler{Store: store})
	req := httptest.NewRequest(http.MethodGet,
		"/internal/payments/"+paymentID+"?tenant_id="+tenant+"&connector_id="+connector, nil)
	req.Header.Set("X-Relay-Token", "phase4-local-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		fail("GET /internal/payments status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		fail("decode GET body: %v", err)
	}
	if body["canonical_status"] != "captured" {
		fail("GET canonical_status=%v", body["canonical_status"])
	}
	if _, leak := body["email"]; leak {
		fail("GET body leaked email")
	}

	fmt.Printf("phase4 local: payment_id=%s canonical_status=%s captured=%t sources=%v events=%d get=200\n",
		pay.PaymentID, pay.CanonicalStatus, pay.Captured, pay.Sources, events)

	tryRazorpayHealth(ctx)
}

func envelope(tenant, connector, paymentID, eventID, eventType, status string, captured bool) observe.Envelope {
	created := time.Now().UTC()
	return observe.Envelope{
		EventName:          observe.EventObservationReceived,
		SchemaVersion:      models.SchemaVersionV1,
		TenantID:           tenant,
		ConnectorID:        connector,
		Provider:           "razorpay",
		ProviderMode:       "test",
		ProviderEventID:    eventID,
		ProviderEventType:  eventType,
		ProviderEntityType: "payment",
		ProviderEntityID:   paymentID,
		ReceiptID:          "33333333-3333-3333-3333-333333333333",
		RawBodyHash:        "sha256:" + eventID,
		Amount:             50000,
		Currency:           "INR",
		Status:             status,
		OrderID:            "order_local_1",
		Captured:           captured,
		ProviderCreatedAt:  &created,
	}
}

func mustApply(p *observe.Processor, env observe.Envelope) observe.ResultKind {
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		fail("apply %s: %v", env.ProviderEventType, err)
	}
	fmt.Printf("  observe %s -> %s\n", env.ProviderEventType, res.Kind)
	return res.Kind
}

func tryRazorpayHealth(ctx context.Context) {
	cfg := razorpay.DefaultConfig()
	cfg.Mode = razorpay.ModeTest
	cfg.KeyID = strings.TrimSpace(os.Getenv("RAZORPAY_KEY_ID"))
	cfg.KeySecret = strings.TrimSpace(os.Getenv("RAZORPAY_KEY_SECRET"))
	if cfg.KeyID == "" || cfg.KeySecret == "" {
		fmt.Println("razorpay test-mode: skipped (no keys in env)")
		return
	}
	if !strings.HasPrefix(cfg.KeyID, "rzp_test_") || os.Getenv("RAZORPAY_ALLOW_LIVE") == "true" {
		fmt.Println("razorpay test-mode: skipped (live keys refused)")
		return
	}
	client, err := razorpay.NewClient(cfg, nil, nil, nil)
	if err != nil {
		fmt.Printf("razorpay client: %v\n", redact(err))
		return
	}
	health, err := client.HealthCheck(ctx)
	if err != nil {
		fmt.Printf("razorpay health: %v\n", redact(err))
		return
	}
	fmt.Printf("razorpay test-mode health=%s latency_ms=%d\n", health.Status, health.LatencyMs)
	to := time.Now().UTC()
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	page, _, err := client.FetchPayments(ctx, razorpay.PaymentFetchOptions{From: from, To: to, Skip: 0, Count: 5})
	if err != nil {
		fmt.Printf("razorpay list: %v\n", redact(err))
		return
	}
	fmt.Printf("razorpay test-mode payments_in_account=%d (0 is ok for an empty Test Mode account)\n", len(page.Items))
}

func redact(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, os.Getenv("RAZORPAY_KEY_SECRET"), "[redacted]")
	msg = strings.ReplaceAll(msg, os.Getenv("RAZORPAY_KEY_ID"), "[redacted]")
	return fmt.Errorf("%s", msg)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
