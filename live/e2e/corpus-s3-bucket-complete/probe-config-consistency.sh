#!/usr/bin/env bash
# Direct-API probe: are floci's per-bucket configuration sub-resources
# (?metrics, ?analytics, ?intelligent-tiering, ?inventory) safe against
# concurrent PUTs of different ids on the SAME bucket?
#
# No terraform and no choudoufu in the loop - awscli only - because both
# runners share the emulator and "stock fails too" has to be told apart from
# "the emulator is wrong" by reading the service API directly.
#
# This is the shape corpus-s3-bucket-complete's cold_deploy hits: the module
# declares three aws_s3_bucket_metric and two
# aws_s3_bucket_intelligent_tiering_configuration on one bucket, and
# terraform applies each set concurrently.
#
#   FLOCI_PORT=49000 bash probe-config-consistency.sh
#
set -uo pipefail

PORT="${FLOCI_PORT:-49000}"
ROUNDS="${ROUNDS:-40}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=eu-west-1
export AWS_ENDPOINT_URL="http://localhost.localstack.cloud:${PORT}"
export AWS_PAGER=""

mkb() {
  aws s3api create-bucket --bucket "$1" \
    --create-bucket-configuration LocationConstraint=eu-west-1 >/dev/null 2>&1
}

# put_<kind> <bucket> <id>
put_metrics() {
  aws s3api put-bucket-metrics-configuration --bucket "$1" --id "$2" \
    --metrics-configuration "{\"Id\":\"$2\"}" >/dev/null 2>&1
}
listraw_metrics() {
  aws s3api list-bucket-metrics-configurations --bucket "$1" 2>/dev/null
}
list_metrics() { listraw_metrics "$1" | grep -c '"Id"'; }
get_metrics() {
  aws s3api get-bucket-metrics-configuration --bucket "$1" --id "$2" >/dev/null 2>&1
}

put_tiering() {
  aws s3api put-bucket-intelligent-tiering-configuration --bucket "$1" --id "$2" \
    --intelligent-tiering-configuration \
    "{\"Id\":\"$2\",\"Status\":\"Enabled\",\"Tierings\":[{\"Days\":90,\"AccessTier\":\"ARCHIVE_ACCESS\"}]}" \
    >/dev/null 2>&1
}
listraw_tiering() {
  aws s3api list-bucket-intelligent-tiering-configurations --bucket "$1" 2>/dev/null
}
list_tiering() { listraw_tiering "$1" | grep -c '"Id"'; }
get_tiering() {
  aws s3api get-bucket-intelligent-tiering-configuration --bucket "$1" --id "$2" >/dev/null 2>&1
}

put_analytics() {
  aws s3api put-bucket-analytics-configuration --bucket "$1" --id "$2" \
    --analytics-configuration "{\"Id\":\"$2\",\"StorageClassAnalysis\":{}}" >/dev/null 2>&1
}
listraw_analytics() {
  aws s3api list-bucket-analytics-configurations --bucket "$1" 2>/dev/null
}
list_analytics() { listraw_analytics "$1" | grep -c '"Id"'; }
get_analytics() {
  aws s3api get-bucket-analytics-configuration --bucket "$1" --id "$2" >/dev/null 2>&1
}

# race <kind> <concurrency> <rounds>
race() {
  local kind="$1" conc="$2" rounds="$3"
  local bad=0 lost_total=0 get_hit=0 round i ok seen lost
  for round in $(seq 1 "$rounds"); do
    local B="probe-${kind}-${conc}-${round}-$$"
    mkb "$B"
    local pids=()
    for i in $(seq 1 "$conc"); do
      "put_${kind}" "$B" "id$i" &
      pids+=($!)
    done
    ok=0
    for p in "${pids[@]}"; do wait "$p" && ok=$((ok + 1)); done
    # a generous settle: eventual consistency would close inside this
    sleep 2
    seen="$("list_${kind}" "$B")"
    lost=$((ok - seen))
    if [ "$lost" -gt 0 ]; then
      bad=$((bad + 1))
      lost_total=$((lost_total + lost))
      # Which ids did LIST drop, and can GET still see them? A LinkedHashMap
      # raced on its iteration order loses the entry from values() while the
      # hash lookup may still find it; a genuinely dropped entry loses both.
      for i in $(seq 1 "$conc"); do
        if ! "listraw_${kind}" "$B" | grep -q "\"id$i\""; then
          if "get_${kind}" "$B" "id$i"; then
            get_hit=$((get_hit + 1))
            echo "      round=$round id$i: MISSING from List but GET finds it"
          else
            echo "      round=$round id$i: MISSING from both List and GET"
          fi
        fi
      done
      echo "    $kind conc=$conc round=$round: $ok puts OK, List shows $seen (lost $lost)"
    fi
  done
  echo "  $kind conc=$conc: $bad/$rounds rounds lost a write; $lost_total configurations lost; $get_hit still reachable by GET"
}

echo "=== the estate's exact shape: 3 concurrent ?metrics puts on one bucket ==="
race metrics 3 "$ROUNDS"
echo
echo "=== the estate's other shape: 2 concurrent ?intelligent-tiering puts ==="
race tiering 2 "$ROUNDS"
echo
echo "=== the same generic path, a third kind: 3 concurrent ?analytics puts ==="
race analytics 3 "$ROUNDS"
