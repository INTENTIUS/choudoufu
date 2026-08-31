#!/usr/bin/env bash
# Does the per-bucket lost-write rate depend on how loaded the emulator is?
# This is the question that separates "a one-off" from the thing that made
# corpus-s3-bucket-complete pass standalone and fail under `gauntlet run -parallel 5`.
#
# QUIET: one bucket at a time, 3 concurrent metric puts (the estate's shape).
# LOADED: 6 buckets' worth of that same shape all in flight at once (18 puts),
#         which is roughly what 5 estates sharing one emulator produce.
set -uo pipefail

PORT="${FLOCI_PORT:-49000}"
ROUNDS="${ROUNDS:-30}"
FANOUT="${FANOUT:-6}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=eu-west-1
export AWS_ENDPOINT_URL="http://localhost.localstack.cloud:${PORT}"
export AWS_PAGER=""

mkb() {
  aws s3api create-bucket --bucket "$1" \
    --create-bucket-configuration LocationConstraint=eu-west-1 >/dev/null 2>&1
}
# one_bucket <name> - the estate's shape: 3 concurrent metric puts on one bucket
one_bucket() {
  local B="$1" i
  for i in 1 2 3; do
    aws s3api put-bucket-metrics-configuration --bucket "$B" --id "m$i" \
      --metrics-configuration "{\"Id\":\"m$i\"}" >/dev/null 2>&1 &
  done
  wait
}
seen() { aws s3api list-bucket-metrics-configurations --bucket "$1" 2>/dev/null | grep -c '"Id"'; }

echo "=== QUIET: one bucket at a time, 3 concurrent puts, $ROUNDS rounds ==="
bad=0
for r in $(seq 1 "$ROUNDS"); do
  B="load-quiet-$r-$$"; mkb "$B"; one_bucket "$B"
done
sleep 3
for r in $(seq 1 "$ROUNDS"); do
  n="$(seen "load-quiet-$r-$$")"
  [ "$n" -lt 3 ] && { bad=$((bad + 1)); echo "  quiet round $r: only $n/3 present"; }
done
echo "  QUIET: $bad/$ROUNDS buckets lost a configuration"

echo
echo "=== LOADED: $FANOUT buckets in flight at once, $ROUNDS rounds ==="
badl=0; total=0
for r in $(seq 1 "$ROUNDS"); do
  for f in $(seq 1 "$FANOUT"); do mkb "load-busy-$r-$f-$$"; done
  for f in $(seq 1 "$FANOUT"); do one_bucket "load-busy-$r-$f-$$" & done
  wait
done
sleep 3
for r in $(seq 1 "$ROUNDS"); do
  for f in $(seq 1 "$FANOUT"); do
    total=$((total + 1))
    n="$(seen "load-busy-$r-$f-$$")"
    [ "$n" -lt 3 ] && { badl=$((badl + 1)); echo "  loaded round $r bucket $f: only $n/3 present"; }
  done
done
echo "  LOADED: $badl/$total buckets lost a configuration"
