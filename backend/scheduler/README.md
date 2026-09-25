# Clearline scheduler (Airflow)

Event-first recon wakes live here. Timers stay as backup.

## Signal vs timer

| Path | When it fires | Role |
|------|---------------|------|
| **`signal_recon_dag`** | Airflow Assets for ERP commit, refund, bank file (or local `bridge.trigger_signal`) | **PRIMARY** |
| `razorpay_payment_backfill_dag` | every 15m timedelta | **BACKUP** timer |
| `razorpay_settlement_backfill_dag` | every 1h timedelta, banking days only | **BACKUP** timer |
| `razorpay_payout_backfill_dag` | every 30m timedelta, 26h lookback, 24x7 (no banking gate) | **BACKUP** timer (D26) |
| `reconciliation_freshness_dag` | every 30m timedelta | **BACKUP** timer |

Signals never call Razorpay/PSP URLs. They POST outcome-engine internal APIs only (`/internal/reconciliation/run`), scoped by `tenant_id` + `connector_id`. Cash-schedule projections stay `schedule_projection` in Go — never MATCHED, never bank cash.

## How each signal fires

### 1. ERP commit → `erp_commit`

- **Source events:** `merchant.books.normalized.v1`, `import.completed.v1` (written on merchant import commit in `backend/recon/internal/imports/service.go`).
- **Asset URI:** `asset://clearline/signals/erp.commit`
- **Kafka:** outcome outbox → relay → `payments.outcome.events.v1` (relay `route_allow_list` includes merchant.books.normalized.v1). Consumed by `plugins/signals/kafka_intake.py`.

### 2. Refund → `refund`

- **Source:** provider observations with `provider_event_type` `refund.*` on `payments.ledger.events.v1` (`KAFKA_OBSERVATION_TOPIC`).
- **Asset URI:** `asset://clearline/signals/refund`
- **Kafka ready:** yes.

### 3. Bank file → `bank_file`

- **Source event:** `bank.statement.received` on `payments.bank.events.v1` (`KAFKA_BANK_TOPIC`).
- **Asset URI:** `asset://clearline/signals/bank.file`
- **Kafka ready:** yes (outcome-engine already consumes this topic).

## Banking-day gate

Python helper in `plugins/signals/banking_calendar.py` mirrors Go `BankingCalendar` / `ReferenceIndiaHolidays` / `IsBankingDay` / `NextBankingDay` (weekends + Republic Day, Independence Day, Gandhi Jayanti). It does **not** reimplement holiday money bucketing — that stays in Go `BuildCashSchedule`.

On non-banking days the DAG branches to `skip_non_banking_day` and records `deferred_to=NextBankingDay`.

## Timer pull (D26)

Razorpay switches a webhook off after 24h of failed deliveries, so the backup
timers pull on their own schedule. They only ask recon to run a job
(`POST /internal/backfill/{payments,settlements,payouts}` with `X-Relay-Token`
+ `X-Relay-Tenant-ID`); recon pulls the provider and feeds the SAME intake as
webhooks, which de-dups by provider id (payouts: `canonical_payouts_uidx`
on tenant, connector, provider, payout_id). A payout seen by both is counted once.

## Kafka intake

`plugins/signals/kafka_intake.py` subscribes to the kafka_ready topics
(`payments.bank.events.v1`, `payments.ledger.events.v1`,
`payments.outcome.events.v1`), decodes relay OutboxEvents and feeds
`bridge.handle_inbound_event` → `trigger_signal`. `confluent_kafka` is imported
lazily; tests inject a fake consumer. Run it as its own process:
`python -m signals.kafka_intake` (env: `SCHEDULER_KAFKA_BOOTSTRAP_SERVERS`,
`SCHEDULER_KAFKA_GROUP_ID`, `SCHEDULER_DEFAULT_CONNECTOR_ID`). The trigger
callback currently logs the payload; wiring it to mark the Airflow Asset /
REST-trigger `signal_recon_dag` is the deployment step.

## Local trigger (tests)

```python
from signals.bridge import trigger_signal, handle_inbound_event

payload = trigger_signal("bank_file", tenant_id="t1", connector_id="c1")
# payload["action"] -> POST /internal/reconciliation/run
```


## Layout

```
dags/signal_recon_dag.py          # event-first
dags/razorpay_payout_backfill_dag.py  # D26 payout timer pull (backup)
plugins/signals/                  # topics, assets, bridge, calendar gate, kafka_intake
plugins/operators/signal_operator.py
tests/                            # run: PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=plugins:dags:. python3 -m unittest discover -s tests
```

## Tests

```bash
cd backend/scheduler && python -m unittest tests.test_signal_scheduler tests.test_razorpay_dags
```
