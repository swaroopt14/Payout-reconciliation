#!/bin/sh
# One-shot preflight for the B12 E2E harness. Every other service depends
# on this with condition: service_completed_successfully.
# Fails (exit 1) if:
#   * any value in .env.harness or in this container's env looks like a
#     live Razorpay key,
#   * RAZORPAY_ALLOW_LIVE, RAZORPAY_KEY_ID, RAZORPAY_KEY_SECRET,
#     RAZORPAY_LIVE_* or RAZORPAY_E2E is set (host shell or .env.harness),
#   * RELAY_DISPATCH_ENABLED is true without CLEARLINE_HARNESS=1 AND a
#     RELAY_PSP_BASE_URL whose host is fake-psp.
# Only variable NAMES are printed, never values.
set -eu

ENV_FILE="${PREFLIGHT_ENV_FILE:-/harness/.env.harness}"
fail=0
bad() { echo "preflight: FAIL: $*" >&2; fail=1; }

# Assembled at runtime so the literal prefix never sits in a tracked file.
LIVE_PAT="$(printf '%s%s' 'rz' 'p_live_')[A-Za-z0-9]+"

if [ ! -r "$ENV_FILE" ]; then
  bad "env file $ENV_FILE not mounted/readable (run testing/e2e/gen-env.sh)"
else
  hits="$(grep -E "$LIVE_PAT" "$ENV_FILE" | grep -v '^[[:space:]]*#' | sed 's/=.*//' | tr '\n' ' ' || true)"
  [ -n "$hits" ] && bad "live-key-shaped value in .env.harness for: $hits"
  names="$(grep -E '^[[:space:]]*(export[[:space:]]+)?(RAZORPAY_ALLOW_LIVE|RAZORPAY_KEY_ID|RAZORPAY_KEY_SECRET|RAZORPAY_E2E|RAZORPAY_LIVE_[A-Za-z0-9_]*)=' "$ENV_FILE" \
    | sed -E 's/^[[:space:]]*(export[[:space:]]+)?//; s/=.*//' | tr '\n' ' ' || true)"
  [ -n "$names" ] && bad "forbidden variables defined in .env.harness: $names"
fi

hits="$(env | grep -E "$LIVE_PAT" | sed 's/=.*//' | tr '\n' ' ' || true)"
[ -n "$hits" ] && bad "live-key-shaped value in environment for: $hits"

for n in RAZORPAY_ALLOW_LIVE RAZORPAY_KEY_ID RAZORPAY_KEY_SECRET RAZORPAY_E2E; do
  eval "v=\${$n:-}"
  [ -n "$v" ] && bad "$n is set"
done
live_names="$(env | grep -E '^RAZORPAY_LIVE_[A-Za-z0-9_]*=.' | sed 's/=.*//' | tr '\n' ' ' || true)"
[ -n "$live_names" ] && bad "RAZORPAY_LIVE_* is set: $live_names"

# Dispatch policy.
d="$(printf '%s' "${RELAY_DISPATCH_ENABLED:-false}" | tr 'A-Z' 'a-z')"
case "$d" in
  true|1|yes|on)
    [ "${CLEARLINE_HARNESS:-}" = "1" ] || bad "RELAY_DISPATCH_ENABLED=true requires CLEARLINE_HARNESS=1"
    host="$(printf '%s' "${RELAY_PSP_BASE_URL:-}" | sed -E 's#^[a-zA-Z][a-zA-Z0-9+.-]*://##; s#[/?].*$##; s#^.*@##; s#:[0-9]+$##')"
    [ "$host" = "fake-psp" ] || bad "RELAY_DISPATCH_ENABLED=true requires RELAY_PSP_BASE_URL host fake-psp (got host '$host')"
    mode="B (dispatch on, fake-psp only)"
    ;;
  false|0|no|off|"") mode="A (dispatch off)" ;;
  *) bad "RELAY_DISPATCH_ENABLED has unrecognised value" ; mode="?" ;;
esac

if [ "$fail" -ne 0 ]; then
  echo "preflight: refusing to start the harness" >&2
  exit 1
fi
echo "preflight: OK, mode $mode"
