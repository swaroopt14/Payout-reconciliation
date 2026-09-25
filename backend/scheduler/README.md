# Clearline scheduler (Airflow)

Event-first recon wakes live here. Timers stay as backup.

## Signal vs timer

| Path | When it fires | Role |
|------|---------------|------|
| **`signal_recon_dag`** | Airflow Assets for ERP commit, refund, bank file (or local `bridge.trigger_signal`) | **PRIMARY** |
| `razorpay_payment_backfill_dag` | every 15m timedelta | **BACKUP** timer |
| `razorpay_settlement_backfill_dag` | every 1h timedelta | **BACKUP** timer |
| `reconciliation_freshness_dag` | every 30m timedelta | **BACKUP** timer |

Signals never call Razorpay/PSP URLs. They POST outcome-engine internal APIs only (`/internal/reconciliation/run`), scoped by `tenant_id` + `connector_id`. Cash-schedule projections stay `schedule_projection` in Go — never MATCHED, never bank cash.

## How each signal fires

### 1. ERP commit → `erp_commit`

- **Source events:** `merchant.books.normalized.v1`, `import.completed.v1` (written on merchant import commit in `backend/recon/internal/imports/service.go`).
- **Asset URI:** `asset://clearline/signals/erp.commit`
- **Kafka:** expected on outcome outbox → relay → `payments.outcome.events.v1`, but **relay `route_allow_list` does not yet include merchant.books.** Local bridge + Asset/manual trigger work today; see TODO in `plugins/signals/topics.py`.

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

## Local trigger (tests / until Kafka→Asset watcher)

```python
from signals.bridge import trigger_signal, handle_inbound_event

payload = trigger_signal("bank_file", tenant_id="t1", connector_id="c1")
# payload["action"] -> POST /internal/reconciliation/run
```

Wire a thin Kafka consumer later to mark the matching Airflow Asset (or REST-trigger this DAG with `conf.signal`).

## Layout

```
dags/signal_recon_dag.py          # event-first
plugins/signals/                  # topics, assets, bridge, calendar gate
plugins/operators/signal_operator.py
tests/test_signal_scheduler.py
```

## Tests

```bash
cd backend/scheduler && python -m unittest tests.test_signal_scheduler tests.test_razorpay_dags
```
