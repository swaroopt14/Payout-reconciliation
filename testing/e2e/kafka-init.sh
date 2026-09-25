#!/bin/bash
# One-shot: creates every topic from spec §2 explicitly (auto-create is off).
# RF=1, 3 partitions. Idempotent (--if-not-exists).
set -euo pipefail
BS="${KAFKA_BOOTSTRAP:-kafka:9092}"
PARTS="${KAFKA_TOPIC_PARTITIONS:-3}"

TOPICS="
payments.ledger.events.v1
pii.tokenize.request
pii.tokenize.result
pii.tokenize.request.dlq
payments.intent.events.v1
payments.dispatch.events.v1
payments.bank.events.v1
payments.intent.dlq
batch.canonicalization.completed
zord.vector.index.request.v1
payments.outcome.events.v1
canonical.settlement.created
attachment.decision.created
variance.record.created
batch.summary.updated
attachment.unresolved.flagged
attachment.ambiguous.flagged
attachment.review.required
relay.dlq.publish_failure
relay.dlq.poison
"

for i in $(seq 1 30); do
  kafka-topics --bootstrap-server "$BS" --list >/dev/null 2>&1 && break
  echo "kafka-init: waiting for $BS ($i)"; sleep 2
done

for t in $TOPICS; do
  extra=""
  case "$t" in relay.dlq.*|*.dlq) extra="--config retention.ms=604800000" ;; esac
  # shellcheck disable=SC2086
  kafka-topics --bootstrap-server "$BS" --create --if-not-exists --topic "$t" \
    --partitions "$PARTS" --replication-factor 1 $extra
done

existing="$(kafka-topics --bootstrap-server "$BS" --list)"
missing=0
for t in $TOPICS; do
  echo "$existing" | grep -qx "$t" || { echo "kafka-init: MISSING $t" >&2; missing=1; }
done
[ "$missing" -eq 0 ] || exit 1
echo "kafka-init: $(echo "$TOPICS" | grep -c .) topics present"
