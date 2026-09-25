# Clearline B12 E2E harness

Docker Compose stack for `TestE2E_PayoutFile_SamePaiseEveryHop_OneRecord_CorrectStatus`
(spec: `b12-harness-spec.md`, §1–§4). **Test-only.** No live PSP keys are
ever used; the `preflight` container refuses to start the stack otherwise.

## Run

From the repo root:

```bash
testing/e2e/gen-env.sh                 # once per checkout; --force to rotate
docker compose -f testing/e2e/docker-compose.harness.yml \
  --env-file testing/e2e/.env.harness up -d --build --wait
docker compose -f testing/e2e/docker-compose.harness.yml \
  --env-file testing/e2e/.env.harness ps
# tear down (no volumes are persisted, so state is always fresh)
docker compose -f testing/e2e/docker-compose.harness.yml \
  --env-file testing/e2e/.env.harness down -v
```

`gen-env.sh` writes (both untracked, see `.gitignore`):

* `testing/e2e/.env.harness`: per-run `openssl rand` values. There's one
  shared `JWT_SIGNING_SECRET` for router, edge, intents and recon, one
  shared `RELAY_AUTH_TOKEN`, one `SERVICE_JWT_SIGNING_SECRET` shared by vault
  and intents, base64 32-byte `ZORD_VAULT_KEY` and `CLEARLINE_SECRETS_KEY`,
  the Postgres password, `ROUTER_AUTH_TOKEN`, `TOKEN_SECRET` and others, plus
  `E2E_USER_PASSWORD` for signups.
* `testing/e2e/secrets/ed25519_private.pem`: the edge `SIGNING_KEY_PATH` key
  (PKCS#8), mounted read-only at `/run/secrets/ed25519_private.pem`.

fake-psp unit tests: `cd testing/e2e/fake-psp && go test ./...`

## Startup order

| Step | Services | Gate |
|---|---|---|
| 0 | `preflight` (one-shot) | exits 0; every other service depends on it |
| 1 | `postgres`, `kafka` (KRaft, 1 node, auto-create off, 3 partitions), `localstack` (s3,kms), `redis` | healthy |
| 2 | `pg-init` (6 DBs + `backend/relay/db/init.sql` → `zord_relay_db`), `kafka-init` (20 topics from §2, RF=1), LocalStack ready.d hook (5 buckets + KMS key `alias/clearline-e2e`) | exit 0 / localstack healthy |
| 3 | `router` (self-migrates), `vault` | `/v1/health`, vault `/ready` (KMS DescribeKey) |
| 4 | `edge`, `intents`, `recon` (goose on boot) | `/ready` |
| 5 | `fake-psp` | `/health` |
| 5b | `harness-setup` (one-shot): signs up tenants A/B, creates the razorpay connector, sets its webhook secret, configures fake-psp, writes `.env.harness.runtime` | exit 0 |
| 6 | `relay` (probes its token enclave at boot, leases edge/intents/recon) | `/ready` |

## Host ports (127.0.0.1 only)

These are chosen to avoid the Mac's known collisions (8091 router vs
Airflow, 5434 recon vs router Postgres). Each one can be overridden with
the env var shown.

| Service | Host | Container | Override |
|---|---|---|---|
| postgres (user `clearline`, all 6 DBs) | 15432 | 5432 | `E2E_POSTGRES_PORT` |
| edge | 18080 | 8080 | `E2E_EDGE_PORT` |
| recon | 18081 | 8081 | `E2E_RECON_PORT` |
| relay (health/ready/metrics/operator) | 18082 | 8082 | `E2E_RELAY_PORT` |
| intents (`/v1/intents`, tenant-isolation check) | 18083 | 8083 | `E2E_INTENTS_PORT` |
| router | 18091 | 8091 | `E2E_ROUTER_PORT` |
| fake-psp | 18099 | 8099 | `E2E_FAKE_PSP_PORT` |

Kafka, LocalStack, Redis and vault aren't published. Use
`docker compose ... exec kafka kafka-console-consumer ...` or
`exec vault wget -qO- http://127.0.0.1:8087/ready` to reach them.
Postgres DSN: `postgres://clearline:$POSTGRES_PASSWORD@127.0.0.1:15432/<db>?sslmode=disable`.

## harness-setup and `.env.harness.runtime`

`harness-setup.sh` runs once `edge`, `intents`, `recon` and `fake-psp` are
healthy. It writes `testing/e2e/.env.harness.runtime` (gitignored,
single-quoted `KEY='value'` lines):

`E2E_RUN_ID`, `E2E_TENANT_{A,B}_{ID,NAME,EMAIL,JWT}`, `E2E_CONNECTOR_ID`,
`E2E_WEBHOOK_SECRET`, `RELAY_DISPATCH_CONNECTOR_UUID_MAP`.

* Tenant passwords are `E2E_USER_PASSWORD` from `.env.harness`, so the test
  can log in again if a JWT expires.
* Connector keys are test-mode placeholders built at runtime. They are
  never written to disk or logged.
* relay's entrypoint wrapper sources the file (`set -a; . file; exec
  /app/relay`). The E2E test reads the same file; strip the single quotes.
* Every `up` that re-runs harness-setup creates fresh tenants. If relay
  isn't recreated, run `up -d --force-recreate relay` so it picks up the new
  map.
* `RELAY_DISPATCH_CONNECTOR_UUID_MAP` defaults to a JSON object
  `{"<tenant A slug>":"<connector uuid>"}`. **Verify this against
  `backend/relay/cmd/main.go:193`.** You can switch with
  `HARNESS_CONNECTOR_MAP_FORMAT=kv` (`key=uuid`) or
  `HARNESS_CONNECTOR_MAP_KEY=<key>`.

## Relay modes

* **Mode A (default)**: `RELAY_DISPATCH_ENABLED=false`. The path stops at
  `payments.intent.events.v1`. No router call, dispatch or dispatch events.
* **Mode B (EM-confirmed; still needs Go's HTTP PSP client, [DEP R-PSP])**:
  `RELAY_DISPATCH_ENABLED=true` in `.env.harness`. Preflight only lets this
  through together with `CLEARLINE_HARNESS=1` and `RELAY_PSP_BASE_URL` host
  `fake-psp`; both are fixed in the compose file. Today relay still builds
  `psp.NewDemoClient` (no HTTP), so fake-psp won't see POSTs until R-PSP
  lands.

Relay reads `testing/e2e/relay.harness.yaml` (`RELAY_CONFIG_FILE`). Its
services list is only intent-engine, ledger-service (edge) and
outcome-engine (recon). `RELAY_*` env overrides scalar keys.
`RELAY_TOKEN_ENCLAVE_BASE_URL` points at fake-psp (`/health` +
`POST /v1/detokenize` stub) until relay sends a service JWT to vault
([DEP R-DETOK]).

## Preflight rules (`preflight.sh`)

The stack fails to start if any of these is true:

* A value in `.env.harness`, or in the preflight env, matches the live-key
  pattern.
* `RAZORPAY_ALLOW_LIVE`, `RAZORPAY_KEY_ID`, `RAZORPAY_KEY_SECRET`,
  `RAZORPAY_LIVE_*` or `RAZORPAY_E2E` is set in the host shell or defined in
  `.env.harness`.
* Dispatch is enabled without `CLEARLINE_HARNESS=1` plus a fake-psp host.

Recon's environment never contains those RAZORPAY vars. Connector test
key placeholders are built at runtime in test code and never written to
files.

## S3 and KMS

* All AWS SDKs get `AWS_ENDPOINT_URL=http://localstack:4566` with `test`/`test`
  credentials.
* edge, intents and recon additionally get
  `AWS_ENDPOINT_URL_S3=http://s3.localhost.localstack.cloud:4566` and
  `S3_FORCE_PATH_STYLE=true`. That flag is honoured once the backend change
  lands.
* LocalStack carries the network aliases `s3.localhost.localstack.cloud`,
  `<bucket>.s3.localhost.localstack.cloud` and `<bucket>.localstack`, so
  virtual-host addressing resolves even without path style.
* vault uses LocalStack KMS (`KMS_KEY_ID=alias/clearline-e2e`); its `/ready`
  is the healthcheck.

## fake-psp contract (spec §3)

| Endpoint | Behaviour |
|---|---|
| `POST /v1/payouts` | `amount` must be a bare JSON integer: a float, string, decimal or exponent gives 400 `amount_not_integer`. A missing `reference_id` gives 400. A missing beneficiary gives 422. The raw body and headers are recorded. Idempotency is keyed on `X-Payout-Idempotency`, falling back to `reference_id`: the same body returns the same `pout_fake_<ref>`, and a different body gives 409 `idempotency_conflict`. |
| failure modes (`amount % 100`) | `13` gives 500, `99` holds for `RELAY_PSP_TIMEOUT_SECONDS`+5 s (then 504), `22` gives 422, `29` gives 429, `44` drops the connection |
| `GET /v1/payouts?reference_id=` | 200 or 404 |
| `POST /_admin/config` | `{"connector_id","webhook_secret"}` (optional `auto_webhook_ms`, `timeout_hold_ms`). The secret is never logged or returned. |
| `GET /_admin/requests?reference_id=&path=` | recorded requests (method, headers, raw body, status) |
| `POST /_admin/webhook/{payout_id}?event=payout.processed\|reversed\|failed` | signed webhook to edge `/v1/webhooks/razorpay/{connector_id}` |
| `GET /_admin/webhooks?payout_id=` | webhook delivery log |
| `POST /_admin/mode` | `{"reference_id","mode":"500\|timeout\|decline\|429\|drop\|ok"}` |
| `POST /_admin/reset` | clears state and keeps config (`?all=1` clears config too) |
| `POST /v1/detokenize`, `GET /health` | interim token-enclave stub for relay |

Webhooks are sent automatically `FAKE_PSP_AUTO_WEBHOOK_MS` (default 500) after
a 200, once `/_admin/config` is set. Headers: `X-Razorpay-Event-Id:
evt_fake_<uuid>` and `X-Razorpay-Signature: hex(HMAC-SHA256(raw_body,
webhook_secret))`. The UTR is `FAKEUTR` + 12 digits derived from the
reference id.

## Seed data

`testdata/payouts_seed.csv` and `testdata/bank_row1.csv` are verbatim from
spec §4 (`{{RUN}}` and `{{UTR_ROW1}}` are substituted by the test). Upload
the bank CSV to recon over HTTP with `amount_unit=paise` (primary path).
Recon's B15 ordering fix has landed, so the Kafka bank path
(`payments.bank.events.v1`) is also safe to use as a secondary check.

## Known dependencies (spec §6)

| ID | Blocker | Owner |
|---|---|---|
| R-PSP | Relay has no HTTP PSP client (`relay/cmd/main.go` always uses `DemoClient`); there's also no fake-psp-only guard or `CLEARLINE_HARNESS` gate in relay | Go |
| R-DETOK | Relay→vault detokenize sends no service JWT (vault returns 401); the harness uses the fake-psp stub | Go |
| B6 | No `X-Payout-Idempotency` header on PSP requests | Go |
| B7 | Router error falls back silently to connector + IMPS; there's no HELD path | Go + Rust |
| B8 | No HELD event; recon ignores `DispatchFailed` | Go |
| B15 | (bank-sink ordering: fixed) dispatch_index connector_id ≠ edge connector; no ON CONFLICT | Go |
| R-LINK | canonical_payouts isn't linked to intent/dispatch | Go |
| R-AMT | Router decision doesn't persist `amount_minor` | Rust |
| B3 | NUMERIC amounts with no `_minor`; recon rupee parsing uses float | Go |
| S3 | `S3_FORCE_PATH_STYLE` support in edge/intents/recon S3 clients (being added) | Go |
| Relay schema | Relay boot migrations being added; harness still applies `init.sql` | Go |
| Topic sharing | intents and recon both consume `payments.ledger.events.v1` | Go |
| EM | Mode B confirmed (fake-psp + `CLEARLINE_HARNESS=1` only; defaults stay off) | done |
