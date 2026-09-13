# Performance Testing — Payout Reconciliation

Automated end-to-end load testing for all 9 backend microservices via Kong API Gateway.

---

## Architecture

```
k6 → local services → 9 backend processes
                                    ↓
                           Slack + Grafana + Kibana
```

---

## 9 Tests — Complete Coverage

| # | Test | Services Tested | VUs | Duration |
|---|------|----------------|-----|----------|
| 1 | Health Check | All 9 service health endpoints | 100 | 3.5 min |
| 2 | Tenant Registration | edge (admin API) | 20 | 3 min |
| 3 | Ingest Pipeline | edge (single + bulk CSV) | 10 | 3 min |
| 4 | Full E2E Flow | ALL services (12-step journey) | 20 | 5 min |
| 5 | Rate Limiting | Kong plugins (bulk, settlement, AI) | 5+3+5 | 30 sec |
| 6 | Spike Test | All Kong routes simultaneously | 200 | 55 sec |
| 7 | Intelligence Surface | intelligence + evidence + outcome | 15 | 3 min |
| 8 | AI Copilot | prompt-layer (query + chat) | 10 | 3 min |
| 9 | Security & CORS | Kong security headers + CORS + rate headers | 20 | 2 min |

---

## What Gets Tested (Every Endpoint)

### edge (Port 8080)
- `POST /v1/admin/tenantReg` — Create tenant
- `GET  /v1/admin/tenants` — List tenants
- `GET  /v1/admin/tenants/:id` — Get tenant
- `POST /v1/ingest` — Single payment JSON
- `POST /v1/bulk-ingest` — Bulk CSV upload
- `GET  /edge/health` — Health check

### intent-engine (Port 8083)
- `GET /v1/intents` — Query intents
- `GET /v1/dlq` — Dead letter queue
- `GET /v1/etl` — ETL run status
- `GET /intent/health` — Health check

### relay (Port 8082)
- `GET /v1/dispatch` — Dispatch status
- `GET /relay/health` — Health check

### outcome-engine (Port 8081)
- `GET  /v1/settlement/supported-psps` — List PSPs
- `GET  /v1/settlement/observations/batches` — Batch observations
- `GET  /v1/reconciliation` — Reconciliation results
- `GET  /outcome/health` — Health check

### evidence (Port 8088)
- `GET  /v1/evidence/packs` — List evidence packs
- `GET  /v1/verify` — Merkle verification
- `GET  /evidence/health` — Health check

### intelligence (Port 8089)
- `GET /v1/projections` — Risk scores / KPIs
- `GET /v1/policies` — Policy rules
- `GET /v1/rca` — Root cause analysis
- `GET /intelligence/health` — Health check

### prompt-layer (Port 8086)
- `POST /v1/query` — AI natural language query
- `POST /v1/chat` — AI conversation
- `GET  /prompt/health` — Health check

### token-enclave (Port 8087)
- `GET /token/health` — Health check

### console (Port 3000)
- `GET /` — Frontend dashboard

---

## Performance Targets

| Metric | Target |
|--------|--------|
| Health check p95 latency | < 500ms |
| Tenant registration p95 | < 2s |
| Ingest pipeline p95 | < 5s |
| Full E2E flow p95 | < 5s |
| Intelligence APIs p95 | < 3s |
| AI copilot p95 | < 10s (LLM) |
| Spike test (200 users) | < 5% gateway errors |
| Rate limiting | 429 returned after limit |
| Security headers | All present on every response |

---

## Slack Notification

Beautiful card with:
- Pass/fail status for all 9 tests (with emoji icons)
- Key metrics (p95, throughput, total requests)
- Direct links to the k6 report
- Color-coded: green (all pass), orange (partial), red (pipeline error)

---

## Grafana Dashboard Links

| Dashboard | URL | UID |
|-----------|-----|-----|
| Platform Health & Alerts | http://localhost:3001/d/platform-health | platform-health |
| PostgreSQL & Kafka | http://localhost:3001/d/data-layer | data-layer |
| Node & Infrastructure | http://localhost:3001/d/nodes-infra | nodes-infra |

---

## Folder Structure

```
performance-tests/
├── README.md
├── generate-report.sh
├── run-all.sh
└── scripts/
    ├── 01-health-check.js
    ├── 02-tenant-registration.js
    ├── 03-bulk-ingest.js
    ├── 04-full-flow.js
    ├── 05-rate-limit.js
    ├── 06-spike-test.js
    ├── 07-intelligence-surface.js
    ├── 08-ai-copilot.js
    └── 09-security-headers.js
```

---

## Run Locally

```bash
# Install k6: https://k6.io/docs/get-started/installation/
k6 run --env BASE_URL="http://localhost:8080" --env ADMIN_KEY="admin" performance-tests/scripts/01-health-check.js
```

---
