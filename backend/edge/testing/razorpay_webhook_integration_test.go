//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"zord-edge/db"
	"zord-edge/handler"
	"zord-edge/internal/secretbox"
	"zord-edge/validator"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// TestRazorpayWebhook_PostgresIdempotency exercises the full Phase 2 contract
// against a real database. Run with:
//
//	DATABASE_URL=postgres://... go test -tags=integration ./testing -count=1
func TestRazorpayWebhook_PostgresIdempotency(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlDB.Ping(); err != nil {
		t.Fatal(err)
	}
	db.DB = sqlDB

	tenantID := uuid.Must(uuid.NewV7())
	connectorID := uuid.Must(uuid.NewV7())
	secret := "whsec_integration_test"
	ctx := context.Background()
	// D32: connectors.secret is stored encrypted (enc:v1:).
	if os.Getenv(secretbox.EnvKey) == "" {
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		t.Setenv(secretbox.EnvKey, base64.StdEncoding.EncodeToString(k))
	}
	storedSecret, err := secretbox.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO connectors (id, tenant_id, provider, connector_id, secret, active, provider_mode)
		VALUES ($1,$2,'razorpay',$3,$4,true,'test')
	`, connectorID, tenantID, "int-"+connectorID.String(), storedSecret); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM ingress_outbox WHERE idempotency_key = $1`, "evt_int_1")
		_, _ = sqlDB.Exec(`DELETE FROM provider_webhook_receipts WHERE connector_id = $1`, connectorID)
		_, _ = sqlDB.Exec(`DELETE FROM connectors WHERE id = $1`, connectorID)
	})

	body, err := os.ReadFile(filepath.Join("..", "testdata", "razorpay", "payment_captured.json"))
	if err != nil {
		t.Fatal(err)
	}
	sig := validator.SignRazorpayWebhook(body, secret)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &handler.Handler{}
	r.POST("/v1/webhooks/razorpay/:connectorID", h.HandleRazorpayWebhook)

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/razorpay/"+connectorID.String(), bytes.NewReader(body))
		req.Header.Set("x-razorpay-event-id", "evt_int_1")
		req.Header.Set("X-Razorpay-Signature", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != 200 {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	var accepted map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &accepted)
	if accepted["status"] != "accepted" {
		t.Fatalf("status=%v", accepted["status"])
	}

	sum := sha256.Sum256(body)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	var (
		receiptCount  int
		eventID       string
		bodyHash      string
		deliveryCount int
		receiptID     string
	)
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*), MIN(event_id), MIN(raw_body_hash), MIN(delivery_count), MIN(id::text)
		FROM provider_webhook_receipts WHERE connector_id = $1
	`, connectorID).Scan(&receiptCount, &eventID, &bodyHash, &deliveryCount, &receiptID); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 1 || eventID != "evt_int_1" || bodyHash != wantHash {
		t.Fatalf("receipt count=%d event=%s hash=%s", receiptCount, eventID, bodyHash)
	}

	var outboxCount int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*) FROM ingress_outbox
		WHERE idempotency_key = $1 AND event_type = 'provider.observation.received'
	`, "evt_int_1").Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox=%d", outboxCount)
	}

	second := post()
	if second.Code != 200 {
		t.Fatalf("second=%d %s", second.Code, second.Body.String())
	}
	var dup map[string]any
	_ = json.Unmarshal(second.Body.Bytes(), &dup)
	if dup["status"] != "duplicate" {
		t.Fatalf("dup status=%v", dup["status"])
	}
	if dup["receipt_id"] != accepted["receipt_id"] {
		t.Fatalf("receipt id changed")
	}

	if err := sqlDB.QueryRow(`
		SELECT COUNT(*), MIN(delivery_count)
		FROM provider_webhook_receipts WHERE connector_id = $1
	`, connectorID).Scan(&receiptCount, &deliveryCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 1 || deliveryCount != 2 {
		t.Fatalf("after dup count=%d delivery=%d", receiptCount, deliveryCount)
	}
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*) FROM ingress_outbox WHERE idempotency_key = $1
	`, "evt_int_1").Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("second outbox created: %d", outboxCount)
	}
}
