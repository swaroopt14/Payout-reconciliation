# Functional Integration Tests — Payout Reconciliation

Automated tests that verify **business logic works end-to-end** after every deployment.

Unlike performance tests (which check "can it handle load?"), these verify "does it actually work?"

---

## What It Catches

| Issue | How It Catches It |
|-------|-------------------|
| Database not storing records | Creates tenant → queries it back → fails if not found |
| API key generation broken | Creates tenant → uses key → fails if 401 |
| Ingest not saving to S3/DB | Ingests payment → queries intents → fails if 0 results |
| CSV upload broken | Uploads CSV → checks response has results |
| Outcome engine DB down | Queries supported-psps → fails if 500 |
| Intelligence DB disconnected | Queries projections → fails if 500 |
| DLQ not accessible | Queries DLQ → fails if not 200 |
| Service pods crashed | Health check → fails if not 200 |
| Secrets misconfigured | Any 500 response indicates env var / secret issue |
| Kong routing broken | Any endpoint unreachable = routing config issue |

---

## Tests Run (in order)

| # | Test | What It Verifies |
|---|------|-----------------|
| 01 | edge health | Pod alive, can respond |
| 02 | intent-engine health | Pod alive, can respond |
| 03 | outcome-engine health | Pod alive, can respond |
| 04 | evidence health | Pod alive, can respond |
| 05 | intelligence health | Pod alive, can respond |
| 06 | prompt-layer health | Pod alive, can respond |
| 07 | relay health | Pod alive, can respond |
| 08 | Create tenant | Admin key works, DB stores tenant |
| 09 | Query tenant by ID | DB read works, tenant persisted |
| 10 | Single payment ingest | Auth works, S3 stores envelope, DB records it |
| 11 | Bulk CSV ingest | File upload works, CSV parsing works |
| 12 | Query intents | Intent-engine DB has records from ingest |
| 13 | Supported PSPs | Outcome-engine responds, PSP registry loaded |
| 14 | Intelligence KPIs | Intelligence DB connected, projections query works |
| 15 | DLQ query | Intent-engine DLQ table accessible |
| 16 | List tenants | Admin endpoint works, DB returns data |

---

## Run locally

```bash
bash functional-tests/run-tests.sh http://localhost:8080 local-admin-key ./results
```

---

## Output

- **Slack:** Table showing each test pass/fail with details
- **JSON:** `results.json` with full machine-readable results
- **Console:** Color-coded terminal output

---

## Duration

~30 seconds to 1 minute. Designed to be fast so you can run after every deployment.

---
