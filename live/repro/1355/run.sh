#!/usr/bin/env bash
# GitHub issue #1355's reproduction harness, against floci only. Manual: it
# is not a claim, not a workflow step, and nothing in CI runs it.
#
#   live/repro/1355/run.sh <name> <iters-per-loop> <loops> [jitter-ms] [maxkeys] [cpu-hogs]
#
# Starts the pinned emulator (live/floci-image) on FLOCI_PORT (default 4791)
# unless FLOCI_REUSE=1 says one is already listening there, builds choudoufu
# from this checkout unless CHOUDOUFU_BIN names a binary, then runs <loops>
# copies of loop.sh at once. Each loop repeats the issue's sequence as its
# own estate: apply, out-of-band put-object-tagging on a.b's record, change
# input, apply, apply -destroy. An iteration passes only when the three runs
# exit 0, report 2 added / 2 changed / 2 destroyed, and no record is left.
#
# Stressors, each optional:
#   jitter-ms   each loop talks to floci through jitterproxy.py, which waits
#               a random 0..jitter-ms before forwarding and before answering
#   maxkeys     the proxy rewrites every ListObjectsV2 to this max-keys, so
#               the listing pages with continuation tokens
#   cpu-hogs    busy loops beside the runs
#   SHARED=1    every loop uses one bucket
#   RETAG=<n>   n retagger.py processes re-tag every record object and write
#               noise objects in the bucket for the whole run
#   RM_CACHE_ALWAYS=1   delete the state cache before every apply
#   TOFU_LIVE_RECORD_READ_PARALLELISM   passed through to choudoufu
#
# Everything it starts - container, proxies, retaggers, hogs - is stopped on
# exit. Results are in $OUT (default: a temp dir), one TSV row per iteration.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
NAME="$1" N="$2" L="$3" J="${4:-0}" MK="${5:-0}" HOGS="${6:-0}"
FLOCI="${FLOCI_PORT:-4791}"
OUT="${OUT:-$(mktemp -d)}"
R="$OUT/$NAME"; mkdir -p "$R"
CONTAINER=""
pids=(); hogs=()
cleanup() {
  kill "${pids[@]}" "${hogs[@]}" 2>/dev/null; wait 2>/dev/null
  [ -n "$CONTAINER" ] && docker rm -f "$CONTAINER" >/dev/null 2>&1
}
trap cleanup EXIT

if [ "${FLOCI_REUSE:-0}" != 1 ]; then
  CONTAINER="chdf1355-$FLOCI"
  docker run -d --name "$CONTAINER" -p "127.0.0.1:$FLOCI:4566" "$(cat "$ROOT/live/floci-image")" >/dev/null || exit 2
  for _ in $(seq 1 60); do curl -fsS "http://localhost:$FLOCI/_localstack/health" >/dev/null 2>&1 && break; sleep 1; done
fi
curl -fsS "http://localhost:$FLOCI/_localstack/health" >/dev/null 2>&1 || { echo "floci is not answering on :$FLOCI"; exit 2; }

BIN="${CHOUDOUFU_BIN:-}"
if [ -z "$BIN" ]; then
  BIN="$OUT/choudoufu"
  (cd "$ROOT" && env -u PWD go build -o "$BIN" ./cmd/choudoufu) || exit 2
fi

for _ in $(seq 1 "$HOGS"); do ( while :; do :; done ) & hogs+=($!); done
loops=()
for l in $(seq 1 "$L"); do
  if [ "${SHARED:-0}" = 1 ]; then B="b1355-$NAME-shared"; else B="b1355-$NAME-$l"; fi
  B="$(echo "$B" | tr 'A-Z_' 'a-z-' | cut -c1-60)"
  bash "$HERE/mkbucket.sh" "http://localhost:$FLOCI" "$B" >/dev/null 2>&1
  if [ "${RETAG:-0}" -gt 0 ] && { [ "${SHARED:-0}" != 1 ] || [ "$l" = 1 ]; }; then
    for r in $(seq 1 "$RETAG"); do
      python3 "$HERE/retagger.py" "http://localhost:$FLOCI" "$B" 86400 "$R/retag-$l-$r.counts" 2>/dev/null & pids+=($!)
    done
  fi
  EP="http://localhost:$FLOCI"
  if [ "$J" != 0 ] || [ "$MK" != 0 ]; then
    port=$((FLOCI + 100 + l))
    python3 "$HERE/jitterproxy.py" "$port" "$FLOCI" "$J" "$MK" "$R/proxy-$l.counts" 2>"$R/proxy-$l.err" & pids+=($!)
    sleep 0.3
    EP="http://localhost:$port"
  fi
  bash "$HERE/loop.sh" "$BIN" "$EP" "http://localhost:$FLOCI" "$B" "$R/loop-$l" "$N" "$NAME-$l" > "$R/loop-$l.out" 2>&1 &
  loops+=($!)
done
wait "${loops[@]}"
sleep 2.5
cat "$R"/loop-*/results.tsv > "$R/all.tsv" 2>/dev/null
total=$(wc -l < "$R/all.tsv" | tr -d ' ')
bad=$(awk -F'\t' '$9!=1' "$R/all.tsv" | wc -l | tr -d ' ')
echo "$NAME: iterations=$total anomalies=$bad (results: $R/all.tsv)"
cat "$R"/loop-*.out
for f in "$R"/proxy-*.counts "$R"/retag-*.counts; do [ -f "$f" ] && echo "$(basename "$f"): $(cat "$f")"; done
[ "$total" -gt 0 ] && [ "$bad" = 0 ]
