# Payout smoke simulator

Single-container API simulator for **manual payout-command UI review**. All backend services are served from **one port** (`8099` by default) with realistic multi-batch fixtures.

The Next.js console BFF still runs locally (`npm run dev`); it proxies to this simulator instead of real microservices.

## How this compares to `intel/docker-compose.test.yml`

| | Intelligence `docker-compose.test.yml` | Payout smoke simulator |
|---|----------------------------------------|-------------------------|
| **Purpose** | Run real intelligence service + Kafka + ML + Postgres | Fake all payout APIs on one port for UI review |
| **Demo batch data** | **Not auto-generated** — `init.sql` only creates schema + policy seeds | **10 batches generated in memory** at startup |
| **Where KPI/batch values come from** | Kafka events → `ProjectionService` → Postgres projections/snapshots, **or** Go tests insert rows directly | JavaScript fixtures (`buildSmokeBatches`) return JSON |
| **Integration tests** | `dashboard_e2e_test.go` calls `seedSnapshot()` / `seedAction()` per test tenant | N/A — static catalogue, configurable count |
| **Containers** | 5 (intelligence, postgres, kafka, ml-service, kafka-ui) | **1** |

Intelligence test data pattern (from `internal/handlers/dashboard_e2e_test.go`):

1. Unique `tenant_id` per test run
2. `INSERT INTO intelligence_snapshots` with JSON `snapshot_json` (LEAKAGE, PATTERN, etc.)
3. `INSERT INTO action_contracts` for recommendation KPIs
4. HTTP handler reads snapshots → dashboard API response

The smoke simulator **mirrors those response shapes** without Postgres — batches are built like multiple per-batch snapshot seeds:

```js
buildSmokeBatches() // batch-2026-06-12-payroll … batch-2026-06-21-close-out
```

## Quick start

```bash
# 1. Start the simulator (10 batches by default)
cd backend/payout-smoke-simulator
docker compose up -d --build

# 2. Wire the console to the simulator
cd ../console
# Point every backend *_URL and PROMPT_LAYER_URL at http://localhost:8099 in .env.local
# (see Console env below). Do not commit .env.local.
npm install
npm run dev
```

Open http://localhost:3000/signin and sign in with an **allowed** email and password (login allow-list in `.env.local`). Random accounts are rejected.

## Upload-first mode (default)

After **password login** (`POST /v1/auth/login`), the simulator clears readiness. Stages unlock independently for the **same** batch id:

1. `POST /v1/bulk-ingest` — obligation / intent file → Intent Journal + pre-settlement APIs  
2. `POST /v1/settlement/upload` — settlement file → Settlement Journal  
3. Both → match / outcome / proof / leakage KPIs  

Every new login resets this again.

| Variable | Default | Purpose |
|----------|---------|---------|
| `SMOKE_UPLOAD_FIRST` | `1` | Stage-gated until the relevant upload(s) |
| `SMOKE_PRESEED_DATA` | unset | Set `1` to restore always-populated catalogue (old demo behaviour) |

Debug: `GET /healthz` → `upload_readiness`, or `POST /v1/smoke/reset-uploads`.

## Default batch catalogue

- **`SMOKE_DEMO_DAY_COUNT` (default 366)** — full calendar year of dated values for Home Month/Quarter/Year charts.
- **`SMOKE_BATCH_COUNT` (default 10)** — capped journal/evidence sidebar list (plus pinned Jun demo batches).

Each listed journal batch = **15 payment intents** + **15 settlement observations**. Home trend uses `GET /v1/intelligence/dashboard/leakage?from_date=&to_date=` per day against the full-year catalogue.

Pinned journal deep-link batches (always included when the list is capped):

| Batch ID | Day | Intent ₹ | Settlement ₹ | DLQ | Partner |
|----------|-----|----------|--------------|-----|---------|
| `batch-2026-06-12-payroll` | 12 Jun payroll | 55,000 | 44,000 | 2 | razorpay |
| `batch-2026-06-13-vendor-run` | 13 Jun vendor run | 68,000 | 61,000 | 0 | cashfree |
| `batch-2026-06-14-refunds` | 14 Jun refunds | 48,000 | 51,000 | 1 | razorpay |
| `batch-2026-06-15-contractor` | 15 Jun contractor | 71,000 | 52,000 | 3 | cashfree |
| `batch-2026-06-16-incentives` | 16 Jun incentives | 53,000 | 49,000 | 1 | razorpay |
| `batch-2026-06-17-peak-run` | 17 Jun peak run | 88,000 | 72,000 | 0 | cashfree |
| `batch-2026-06-18-micro-batch` | 18 Jun micro-batch | 41,000 | 35,000 | 2 | razorpay |
| `batch-2026-06-19-partner-payouts` | 19 Jun partner payouts | 67,000 | 61,000 | 1 | cashfree |
| `batch-2026-06-20-sweep` | 20 Jun sweep | 59,000 | 45,000 | 2 | razorpay |
| `batch-2026-06-21-close-out` | 21 Jun close-out | 76,000 | 68,000 | 0 | cashfree |

Use **Month** period on Home to see bars across the full current month (same as master).

Counts use deterministic formulas so each batch differs (pagination still works on large observation sets).

Change journal list size (charts stay full-year unless you also lower `SMOKE_DEMO_DAY_COUNT`):

```bash
SMOKE_BATCH_COUNT=10 docker compose up -d --build
```

## Environment

| Variable | Default | Purpose |
|----------|---------|---------|
| `SMOKE_SIMULATOR_PORT` | `8099` | Host port mapping |
| `SMOKE_TENANT_ID` | `00000000-0000-0000-0000-000000000001` | Tenant on all fixtures |
| `SMOKE_API_KEY` | `local-dev-api-key` | Accepted Bearer key for settlement routes |
| `SMOKE_DEMO_DAY_COUNT` | `366` | Days for home/leakage trend charts |
| `SMOKE_BATCH_COUNT` | `10` | Journal/evidence list batch count |
| `SMOKE_LATENCY_MS` | `0` | Artificial delay on heavy list routes |
| `SMOKE_UPLOAD_FIRST` | `1` | Gate data until obligation + settlement uploads |
| `SMOKE_PRESEED_DATA` | unset | `1` = always show full catalogue (skip upload gate) |
| Postgres (compose) | hardcoded | user `smoke` / db `smoke_audit` / password in root `docker-compose.yml` |

## Login audit (Postgres)

Every `POST /v1/auth/login` records **email, time, IP, user-agent, latency** — **never passwords**.

```bash
# After compose is up and someone signs in via the console:
curl -sS -H "Authorization: Bearer $SMOKE_API_KEY" \
  "http://localhost:8099/v1/smoke/login-audit?limit=20"
```

AWS / DevOps handoff: [`docs/SMOKE_LOGIN_AUDIT_AWS.md`](../docs/SMOKE_LOGIN_AUDIT_AWS.md).

## Health check

```bash
curl -s http://localhost:8099/healthz
# Expect login_audit.backend = "postgres" when DATABASE_URL is set
curl -s http://localhost:8099/healthz | jq .login_audit
# Before uploads (upload-first default): length should be 0
curl -s "http://localhost:8099/api/prod/intents/batch-ids?tenant_id=00000000-0000-0000-0000-000000000001" | jq '.items | length'
curl -s "http://localhost:8099/v1/settlement/observations/batches?tenant_id=00000000-0000-0000-0000-000000000001&client_batch_id=batch-2026-06-12-payroll&page=1&page_size=100" | jq '.pagination'
```

## Local run (without Docker)

```bash
cd backend/payout-smoke-simulator
SMOKE_BATCH_COUNT=10 npm start
```

## Console env

Do **not** commit console env files. Use a local `.env.local` (gitignored) or AWS task/env secrets.

| Mode | What to set |
|------|-------------|
| **Smoke (demo)** | Every backend service URL + `PROMPT_LAYER_URL` → smoke host (`http://localhost:8099` locally, or your smoke ALB on AWS). Match settlement and bulk-ingest API keys to smoke `SMOKE_API_KEY`. |
| **Live backends** | Each backend service URL / `PROMPT_LAYER_URL` → that microservice’s URL. Do not point them at smoke. |

Smoke and live are mutually exclusive for a given console deployment.

### AWS / deployed console

Set the same keys as runtime env (ECS task definition, Amplify, Parameter Store, etc.) — never commit secrets.

## Notes

- Not a replacement for intelligence integration tests or Kafka-driven projections.
- Unimplemented routes return HTTP 404 with `{ error: "smoke_simulator_no_route" }`.
