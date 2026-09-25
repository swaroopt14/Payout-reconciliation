# Zord Phase 1 — Razorpay Connector Implementation Plan

**Date:** August 29, 2026  
**Status:** PLANNING  
**Scope:** Backend only — no financial state changes

---

## Executive Summary

Implement a secure, reusable Razorpay connector that can:
- Store Test/Live credentials by reference (never raw secrets in DB)
- Make authenticated read-only API calls via Basic Auth
- Handle pagination, retries, timeouts, and error classification
- Return redacted connection-test results
- Leave no secrets in logs, Kafka events, or DB

---

## Current State Analysis

### Existing `connectors` table (already migrated)
```sql
-- backend/edge/db/migrations/20260703063716_create_connectors.sql
CREATE TABLE IF NOT EXISTS "connectors" (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    provider TEXT NOT NULL,
    connector_id TEXT NOT NULL,
    secret_ref TEXT,
    secret TEXT,           -- ⚠️ Phase 1: migrate away from storing raw secrets
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now(),
    CONSTRAINT unique_provider_connector UNIQUE (provider, connector_id),
    CONSTRAINT unique_tenant_connector UNIQUE (tenant_id, provider, connector_id)
);
```

**Gap:** No `provider_mode`, no health-check columns, no mode constraint, no unique index per mode.

### Existing env files updated
- `backend/console/.env.local` ✅ — has `RZP_KEY_ID`, `RZP_KEY_SECRET`
- `backend/edge/.env` ✅ — has full Razorpay config block

### Existing extension points to reuse
| What exists | Where |
|---|---|
| Connector CRUD | `zord-edge/handler/` (no dedicated connector handler yet) |
| Vault/encryption | `zord-edge/vault/` (encrypt, decrypt, key, signing) |
| Auth & tenant context | `zord-edge/middleware/` (jwt_auth, tenant, session) |
| Outcome polling boundary | `zord-outcome-engine/internal/` (no `poll/` dir yet) |
| Event contract spec | `backend/shared/zord-event-contract/spec.json` |
| Tracing | Both services have `tracing/tracing.go` |

---

## Implementation Phases (Step-by-Step)

### STEP 1: Database Migration — Extend `connectors` Table

**File:** `backend/edge/db/migrations/20260826_add_razorpay_connector_fields.sql`

```sql
-- +goose Up
ALTER TABLE connectors
    ADD COLUMN IF NOT EXISTS provider_mode TEXT NOT NULL DEFAULT 'test',
    ADD COLUMN IF NOT EXISTS api_key_ref TEXT,
    ADD COLUMN IF NOT EXISTS api_secret_ref TEXT,
    ADD COLUMN IF NOT EXISTS webhook_secret_ref TEXT,
    ADD COLUMN IF NOT EXISTS provider_account_id TEXT,
    ADD COLUMN IF NOT EXISTS last_health_check_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_health_status TEXT,
    ADD COLUMN IF NOT EXISTS last_health_error_code TEXT;

ALTER TABLE connectors
    ADD CONSTRAINT connectors_provider_mode_check
    CHECK (provider_mode IN ('test', 'live'));

CREATE UNIQUE INDEX IF NOT EXISTS connectors_tenant_provider_mode_uq
    ON connectors (tenant_id, provider, provider_mode);
```

**Deliberate decisions:**
- Keeps existing `secret` column for backward compat (will be deprecated)
- Adds `_ref` columns for secret-manager references
- Unique index prevents duplicate test/live connector per tenant+provider
- Mode check constraint enforces data integrity at DB level

---

### STEP 2: Environment & Config — Razorpay Provider Config

**Files to create:**
```
backend/recon/.env              # with Razorpay keys
backend/recon/internal/poll/providers/razorpay/config.go
```

**Config struct:**
```go
type Config struct {
    BaseURL     string
    KeyID       string
    KeySecret   string
    Mode        Mode        // "test" or "live"
    Timeout     time.Duration
    MaxRetries  int
    BaseDelay   time.Duration
    MaxPageSize int
}
```

**Validation rules:**
- BaseURL, KeyID, KeySecret required
- Mode must be "test" or "live"
- Timeout must be > 0
- MaxRetries between 0 and 5
- Live mode + test keys = reject

---

### STEP 3: Provider-Neutral Interface

**File:** `backend/recon/internal/poll/provider.go`

```go
type OutcomeProvider interface {
    Name() string
    HealthCheck(ctx context.Context) error
    FetchPayment(ctx context.Context, ref PaymentReference) (any, error)
    FetchPayments(ctx context.Context, window TimeWindow, page Page) (any, error)
    FetchSettlements(ctx context.Context, window TimeWindow, page Page) (any, error)
}
```

---

### STEP 4: Razorpay HTTP Client

**File:** `backend/recon/internal/poll/providers/razorpay/client.go`

Request pipeline:
```
Validate path + query
  → Create request with context timeout
  → Set Accept + User-Agent headers
  → Set Basic Auth (keyID:keySecret)
  → Add trace correlation ID
  → Execute with retry helper
  → Classify status code
  → Decode typed response or return ProviderError
```

**Authentication:** Razorpay uses Basic Auth:
```go
req.SetBasicAuth(c.keyID, c.keySecret)
```

**Retry policy:**
- Retry only on: 408, 429, 5xx, network errors
- Do NOT retry: 400, 401, 403, 404, decode errors
- Backoff: 250ms → 500ms → 1000ms + jitter
- Honor `Retry-After` header on 429
- Bound total duration with `context.WithTimeout`

---

### STEP 5: Error Classification

**File:** `backend/recon/internal/poll/providers/razorpay/errors.go`

| HTTP Status | ErrorKind | Retryable |
|---|---|---|
| 200 | — (success) | No |
| 400 | bad_request | No |
| 401 | unauthorized | No |
| 403 | forbidden | No |
| 404 | not_found | No |
| 408 | timeout | Yes |
| 429 | rate_limited | Yes (honor Retry-After) |
| 500-599 | provider_error | Yes (bounded) |
| network error | transport_error | Yes (bounded) |
| invalid JSON | decode_error | No |

---

### STEP 6: Pagination

**File:** `backend/recon/internal/poll/providers/razorpay/pagination.go`

Bounded iterator pattern:
```go
func (c *Client) ListPayments(ctx context.Context, window TimeWindow, fn func(Payment) error) error
```

Stops when:
- Returned items count is 0
- Provider reports no more records
- Max page count reached
- Context cancelled
- Non-retryable error

---

### STEP 7: Typed Provider Models

**File:** `backend/recon/internal/poll/providers/razorpay/types.go`

Razorpay-specific DTOs (NOT used by frontend or DB):
```go
type PaymentResponse struct {
    ID       string `json:"id"`
    Entity   string `json:"entity"`
    Amount   int64  `json:"amount"`
    Currency string `json:"currency"`
    Status   string `json:"status"`
    OrderID  string `json:"order_id"`
    Method   string `json:"method"`
    Captured bool   `json:"captured"`
    CreatedAt int64 `json:"created_at"`
}

type ListResponse[T any] struct {
    Entity string `json:"entity"`
    Count  int    `json:"count"`
    Items  []T    `json:"items"`
}
```

---

### STEP 8: Health Check (Connection Test)

**File:** `backend/recon/internal/poll/providers/razorpay/health.go`

Calls a safe read-only Razorpay endpoint to verify credentials.

Success response:
```json
{
    "provider": "razorpay",
    "mode": "test",
    "status": "healthy",
    "checked_at": "2026-08-29T...",
    "latency_ms": 184
}
```

Failure response:
```json
{
    "provider": "razorpay",
    "mode": "test",
    "status": "unauthorized",
    "error_code": "RAZORPAY_AUTH_FAILED",
    "message": "Razorpay credentials were rejected",
    "checked_at": "2026-08-29T..."
}
```

---

### STEP 9: Redaction Helpers

**File:** `backend/recon/internal/poll/providers/razorpay/redact.go`

NEVER log:
- Authorization header
- API secret
- Webhook secret
- Full request/response body with sensitive fields

Safe to log:
- `provider=razorpay`, `mode=test`, `operation=health_check`
- `http_status=200`, `latency_ms=184`, `trace_id=...`

---

### STEP 10: Edge Connector API Endpoints

**File:** `backend/edge/handler/connector_handler.go`

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/connectors/razorpay` | Save connector config |
| POST | `/v1/connectors/razorpay/test` | Trigger connection test |
| GET | `/v1/connectors/razorpay/status` | Get health status |
| PATCH | `/v1/connectors/razorpay/:connector_id` | Update config |

**Flow:**
```
1. Edge validates tenant + request schema
2. Edge saves connector metadata + secret refs (NOT raw secrets)
3. Edge calls outcome-engine internal endpoint for connection test
4. Outcome-engine resolves secrets, makes Razorpay API call
5. Returns redacted health result
6. Edge updates connector health columns
```

---

### STEP 11: Edge Connector Service

**File:** `backend/edge/services/connector_service.go`

- Create/update connector records
- Resolve secret references (env vars for local, vault for prod)
- Validate mode isolation (test vs live)
- Return safe status responses (no secrets in JSON)

---

### STEP 12: Edge Connector Model

**File:** `backend/edge/model/connector.go`

```go
type Connector struct {
    ID                   uuid.UUID  `json:"id"`
    TenantID             uuid.UUID  `json:"tenant_id"`
    Provider             string     `json:"provider"`
    ConnectorID          string     `json:"connector_id"`
    ProviderMode         string     `json:"provider_mode"`
    ApiKeyRef            *string    `json:"api_key_ref"`
    ApiSecretRef         *string    `json:"api_secret_ref"`
    WebhookSecretRef     *string    `json:"webhook_secret_ref"`
    ProviderAccountID    *string    `json:"provider_account_id"`
    Active               bool       `json:"active"`
    LastHealthCheckAt    *time.Time `json:"last_health_check_at"`
    LastHealthStatus     *string    `json:"last_health_status"`
    LastHealthErrorCode  *string    `json:"last_health_error_code"`
    CreatedAt            time.Time  `json:"created_at"`
    UpdatedAt            time.Time  `json:"updated_at"`
}
```

---

### STEP 13: Internal Health Endpoint (Outcome Engine)

**File:** `backend/recon/internal/handler/connector_health_handler.go`

Internal HTTP endpoint that edge calls to trigger connection test:
```
POST /internal/v1/connectors/health-test
Body: { "provider": "razorpay", "key_id_ref": "...", "key_secret_ref": "...", "mode": "test" }
```

---

### STEP 14: Unit Tests

| Test File | Coverage |
|---|---|
| `config_test.go` | Valid config, missing fields, invalid mode, zero timeout |
| `client_test.go` | Basic Auth, correct URL, headers, 200/400/401/403/404/429/500 handling |
| `pagination_test.go` | First page, empty page, max limit, context cancel |
| `retry_test.go` | Bounded retries, backoff, Retry-After, no-retry on 4xx |
| `timeout_test.go` | Context deadline, request timeout |
| `redaction_test.go` | No secrets in log buffer |
| `razorpay_connector_config_test.go` (edge) | Tenant auth, mode isolation, status update |

All tests use `httptest.Server` — never real Razorpay API.

---

### STEP 15: Integration Test

**File:** `backend/recon/docker-compose.test.yml`

```
1. Start Postgres + Kafka + Redis + edge + outcome-engine
2. Register test tenant
3. Create Razorpay Test connector with fake secret refs
4. Configure outcome-engine to use mock Razorpay server
5. Call connector test endpoint
6. Assert healthy result
7. Assert connector health columns updated
8. Assert no secret in logs or Kafka events
```

---

### STEP 16: Manual Verification (After Tests Pass)

```bash
# 1. Start services
docker compose -f docker-compose.test.yml up -d --build

# 2. Create connector
curl -X POST http://localhost:8080/v1/connectors/razorpay \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <tenant-token>' \
  -d '{
    "mode": "test",
    "key_id": "rzp_test_FAKEKEYID",
    "key_secret": "fake_key_secret_not_real"
  }'

# 3. Run connection test
curl -X POST http://localhost:8080/v1/connectors/razorpay/test \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <tenant-token>' \
  -d '{"connector_id":"<id>"}'

# 4. Check DB (no secrets visible)
SELECT id, tenant_id, provider, provider_mode, status,
       last_health_check_at, last_health_status
FROM connectors WHERE provider = 'razorpay';
```

---

## File Creation Summary

| # | File | Service | Type |
|---|---|---|---|
| 1 | `zord-edge/db/migrations/20260826_add_razorpay_connector_fields.sql` | edge | Migration |
| 2 | `zord-edge/.env` | edge | Config ✅ DONE |
| 3 | `zord-edge/model/connector.go` | edge | Model |
| 4 | `zord-edge/handler/connector_handler.go` | edge | Handler |
| 5 | `zord-edge/services/connector_service.go` | edge | Service |
| 6 | `zord-edge/testing/razorpay_connector_config_test.go` | edge | Test |
| 7 | `zord-outcome-engine/.env` | outcome | Config |
| 8 | `zord-outcome-engine/internal/poll/provider.go` | outcome | Interface |
| 9 | `zord-outcome-engine/internal/poll/providers/razorpay/config.go` | outcome | Config |
| 10 | `zord-outcome-engine/internal/poll/providers/razorpay/client.go` | outcome | HTTP Client |
| 11 | `zord-outcome-engine/internal/poll/providers/razorpay/types.go` | outcome | DTOs |
| 12 | `zord-outcome-engine/internal/poll/providers/razorpay/errors.go` | outcome | Errors |
| 13 | `zord-outcome-engine/internal/poll/providers/razorpay/pagination.go` | outcome | Pagination |
| 14 | `zord-outcome-engine/internal/poll/providers/razorpay/redact.go` | outcome | Security |
| 15 | `zord-outcome-engine/internal/poll/providers/razorpay/health.go` | outcome | Health |
| 16 | `zord-outcome-engine/internal/poll/providers/razorpay/payments.go` | outcome | Read-only ops |
| 17 | `zord-outcome-engine/internal/poll/providers/razorpay/settlements.go` | outcome | Placeholder |
| 18 | `zord-outcome-engine/internal/poll/providers/razorpay/client_test.go` | outcome | Test |
| 19 | `zord-outcome-engine/internal/poll/providers/razorpay/pagination_test.go` | outcome | Test |
| 20 | `zord-outcome-engine/internal/poll/providers/razorpay/retry_test.go` | outcome | Test |
| 21 | `zord-outcome-engine/internal/poll/providers/razorpay/timeout_test.go` | outcome | Test |
| 22 | `zord-outcome-engine/internal/poll/providers/razorpay/redaction_test.go` | outcome | Test |
| 23 | `zord-outcome-engine/internal/persistence/connector_cursor_repo.go` | outcome | Repo (optional) |
| 24 | `zord-outcome-engine/internal/handler/connector_health_handler.go` | outcome | Handler |
| 25 | `shared/zord-event-contract/provider_connector.v1.json` | shared | Contract (opt) |
| 26 | `docs/razorpay/phase1-connection-test.md` | docs | Documentation |

---

## What Phase 1 Does NOT Do

- ❌ Webhook signature validation (Phase 2)
- ❌ payment.captured event processing (Phase 2)
- ❌ Settlement reconciliation (Phase 3)
- ❌ Bank statement ingestion (Phase 3)
- ❌ UTR matching (Phase 3)
- ❌ Fee/tax accounting (Phase 3)
- ❌ Razorpay mutations/refunds (Phase 4)
- ❌ AI agent actions (Phase 5)

---

## Implementation Order

```
Step 1:  Migration (edge)
Step 2:  Config (outcome-engine)
Step 3:  Provider interface (outcome-engine)
Step 4:  Razorpay client (outcome-engine)
Step 5:  Error types (outcome-engine)
Step 6:  Pagination (outcome-engine)
Step 7:  Provider DTOs (outcome-engine)
Step 8:  Health check (outcome-engine)
Step 9:  Redaction helpers (outcome-engine)
Step 10: Edge connector handler
Step 11: Edge connector service
Step 12: Edge connector model
Step 13: Internal health endpoint
Step 14: Unit tests
Step 15: Integration test
Step 16: Manual verification
```

**Estimated effort:** 4-6 implementation sessions
