#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-index-wait.sh: proof for issues #1032, #1046, #1049
# and #1143.
#
# terralith-scale.sh used to walk straight from migrate into test_plan with
# no gap at all. #1046 found that the Resource Groups Tagging API's own
# search index is a separate, eventually-consistent copy of the tags migrate
# just wrote and verified. #1049's fix makes discovery refuse a
# count/for_each aws_iam_policy instance with DIRECT_READ_UNRESOLVED while
# the index is silent for it, rather than proposing a create the provider
# would reject - the safe outcome, but only useful if test_plan actually runs
# once the account has caught up (or, failing that, records how far it had
# NOT caught up). So terralith-scale.sh grew index_wait() between migrate and
# test_plan.
#
# #1143 then found that index_wait's TARGET was unreachable. It polled for
# VERIFIED = 33*SCALE+5 - every object migrate stamps - and a majority of
# those are types resourcegroupstaggingapi does not return: iam:role in any
# region at all, and iam:policy / iam:instance-profile / route53 zones only
# in us-east-1, IAM and Route53 being global services. Three real-AWS runs
# plateaued at exactly the reachable ceiling and were read as an index
# settling slowly. terralith-scale.sh now carries index_partition(), which
# splits VERIFIED by type into what the index can hold from the run's own
# region and what it cannot, and index_wait() polls to that and names what it
# is NOT waiting for.
#
# This self-test extracts index_partition(), index_partition_is_total() and
# index_wait() VERBATIM out of a real terralith-scale.sh (default: the one
# shipped beside this script), the same way selftest-teardown-timeout.sh
# extracts teardown() - no AWS calls, no docker, no terraform, no go build -
# and stubs `aws` (via a fake livecert_rgta_count-shaped
# resourcegroupstaggingapi get-resources) so the tag index's own count climbs
# across polls exactly the way a real account's would. The VERIFIED formula
# is extracted verbatim too, rather than restated here, so the partition is
# checked against production's own definition of what gets stamped.
#
# Cases:
#   A. index_partition reproduces the three real-AWS ceilings on record
#      (#1134/#1143): 104 at scale 50 in us-east-2, 1105 at scale 50 seen
#      from us-east-1, 260 at scale 128 in us-east-2 - and the split is total
#      at every scale tried.
#   B. index_partition_is_total goes RED when the split stops summing to
#      VERIFIED (driven by moving VERIFIED out from under it).
#   C. index_wait converges on the reachable target in a non-us-east-1 run.
#      This is the case that was IMPOSSIBLE before #1143: the 2026-09-11
#      scale-50 run sat at exactly 104 for its full 3600s bound because it
#      was waiting for 1655.
#   D. index_wait converges in us-east-1, where the global half counts and
#      the target is 1105 rather than 104.
#   E. a genuine lag: the target is reachable and is not reached, the bound
#      trips, and the run gets a NOT CONVERGED line plus index_converged=no
#      rather than a line that reads like progress. index_wait still returns
#      0 so test_plan runs and records the product's own refusal.
#   F. a zero target skips the wait outright and makes no AWS call at all,
#      rather than "converging" on 0 of 0.
#
# Usage: bash live/live-cert/selftest-index-wait.sh
#   TERRALITH_SCALE_SH=<path> to extract from a different revision, e.g. the
#   pre-fix content via process substitution:
#     TERRALITH_SCALE_SH=<(git show HEAD~1:live/live-cert/terralith-scale.sh) \
#       bash live/live-cert/selftest-index-wait.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
pass=1
log() { printf '%s\n' "$*"; }
fail_case() { log "FAIL: $*"; pass=0; }

# Materialize SRC_ARG into a plain file: it may be a process substitution,
# and extract_func below reads it once but a FIFO source is safest handled
# this way regardless (matches selftest-teardown-timeout.sh).
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"

log "=== selftest-index-wait: extracting from $SRC_ARG ==="

# Same brace-depth/heredoc-aware extractor selftest-teardown-timeout.sh uses,
# copied rather than shared, so this self-test has no import of its own to
# keep in sync - none of the three functions has a heredoc, but the extractor
# stays safe if a future edit adds one.
extract_func() {
  local name="$1" f="$2"
  awk -v want="${name}() {" '
    BEGIN { grab = 0; depth = 0; heredoc = "" }
    {
      line = $0
      if (heredoc != "") {
        print line
        if (line == heredoc) heredoc = ""
        next
      }
      if (!grab) {
        if (line == want) { grab = 1; depth = 1; print line }
        next
      }
      if (match(line, /<<-?["\x27]?[A-Za-z_][A-Za-z0-9_]*["\x27]?/)) {
        tok = substr(line, RSTART, RLENGTH)
        sub(/^<<-?/, "", tok)
        gsub(/["\x27]/, "", tok)
        heredoc = tok
      }
      print line
      n = line; gsub(/[^{]/, "", n); depth += length(n)
      n = line; gsub(/[^}]/, "", n); depth -= length(n)
      if (depth == 0) exit
    }
  ' "$f"
}

INDEX_WAIT_SRC="$(extract_func index_wait "$SRC")"
if [ -z "$INDEX_WAIT_SRC" ]; then
  log "FAIL: could not find index_wait() in $SRC - the wait step is missing (this is the RED result on a pre-#1046 script)"
  exit 1
fi
PARTITION_SRC="$(extract_func index_partition "$SRC")"
if [ -z "$PARTITION_SRC" ]; then
  log "FAIL: could not find index_partition() in $SRC - index_wait has no reachable target to poll to, so it is polling for every stamped object again (this is the RED result on a pre-#1143 script)"
  exit 1
fi
TOTAL_SRC="$(extract_func index_partition_is_total "$SRC")"
if [ -z "$TOTAL_SRC" ]; then
  log "FAIL: could not find index_partition_is_total() in $SRC - nothing checks that the split still covers every stamped object (pre-#1143)"
  exit 1
fi

# The VERIFIED formula, verbatim, so the partition is checked against
# production's own definition of what migrate stamps rather than against a
# number restated in this file that could drift with it.
VERIFIED_SRC="$(grep -E '^VERIFIED=\$\(\(' "$SRC")"
if [ -z "$VERIFIED_SRC" ]; then
  log "FAIL: could not find the VERIFIED=\$((...)) assignment in $SRC"
  exit 1
fi
log "  extracted index_partition(), index_partition_is_total(), index_wait() and: $VERIFIED_SRC"

# ── A/B: the partition itself ───────────────────────────────────────────

# partition_at prints "<target> <regional> <global> <unindexed> <verified>"
# for a scale and region, from the extracted production code.
partition_at() {
  local scale="$1" region="$2"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -uo pipefail'
    printf 'SCALE=%q\n' "$scale"
    printf '%s\n' "$VERIFIED_SRC"
    printf '%s\n' "$PARTITION_SRC"
    printf 'printf "%%s %%s\\n" "$(index_partition %q)" "$VERIFIED"\n' "$region"
  } > "$WORK/partition.sh"
  bash "$WORK/partition.sh"
}

check_partition() {
  local scale="$1" region="$2" want_target="$3" note="$4"
  local out target regional global unindexed verified
  out="$(partition_at "$scale" "$region")"
  IFS=' ' read -r target regional global unindexed verified <<< "$out"
  log "  scale=$scale region=$region -> target=$target (regional=$regional global=$global unindexed=$unindexed) of verified=$verified"
  if [ "$target" != "$want_target" ]; then
    fail_case "scale=$scale region=$region: target is $target, but the recorded real-AWS measurement is $want_target ($note)"
    return
  fi
  if [ $(( regional + global + unindexed )) -ne "$verified" ]; then
    fail_case "scale=$scale: the split sums to $(( regional + global + unindexed )), not VERIFIED=$verified - it no longer covers every stamped object"
    return
  fi
  log "    confirmed: matches the recorded $want_target ($note), and the split is total"
}

log ""
log "=== case A: index_partition reproduces the three real-AWS ceilings on record (#1134, #1143) ==="
check_partition 50 us-east-2 104 "101 ecs + 3 ec2, the 2026-09-11 scale-50 run's own plateau"
check_partition 50 us-east-1 1105 "104 regional + 1000 iam + 1 route53 zone, the unioned figure in #1143"
check_partition 128 us-east-2 260 "257 ecs + 3 ec2, the scale-128 run recorded in #1143's comment"
# Not a recorded measurement, just the shape at the scale the emulator runs:
# the target must still be total and must still exclude the roles.
check_partition 1 us-east-1 27 "2*1+4 regional + 20*1+1 global; no separate measurement, checked for totality and for excluding the 11 roles"

log ""
log "=== case B: index_partition_is_total goes RED when the split stops covering VERIFIED ==="
# Written from the failure, not from the implementation: move VERIFIED out
# from under the split (as a change to terralith-gen's composition would) and
# the guard must refuse and name both numbers.
{
  printf '%s\n' '#!/usr/bin/env bash'
  printf '%s\n' 'set -uo pipefail'
  printf 'SCALE=50\n'
  printf 'VERIFIED=9999\n'
  printf '%s\n' "$PARTITION_SRC"
  printf '%s\n' "$TOTAL_SRC"
  printf '%s\n' 'if out="$(index_partition_is_total us-east-1)"; then echo "RETURNED-ZERO"; else echo "REFUSED: $out"; fi'
} > "$WORK/total_red.sh"
TOTAL_RED_OUT="$(bash "$WORK/total_red.sh")"
log "  $TOTAL_RED_OUT"
case "$TOTAL_RED_OUT" in
  REFUSED:*9999*) log "    confirmed: refused, and the diagnosis names VERIFIED" ;;
  *) fail_case "index_partition_is_total accepted a split that does not sum to VERIFIED - the drift guard cannot fail, so it proves nothing: $TOTAL_RED_OUT" ;;
esac
# ... and green on the real formula, at the same scale.
{
  printf '%s\n' '#!/usr/bin/env bash'
  printf '%s\n' 'set -uo pipefail'
  printf 'SCALE=50\n'
  printf '%s\n' "$VERIFIED_SRC"
  printf '%s\n' "$PARTITION_SRC"
  printf '%s\n' "$TOTAL_SRC"
  printf '%s\n' 'if index_partition_is_total us-east-1 >/dev/null; then echo "ACCEPTED"; else echo "REFUSED"; fi'
} > "$WORK/total_green.sh"
TOTAL_GREEN_OUT="$(bash "$WORK/total_green.sh")"
if [ "$TOTAL_GREEN_OUT" = "ACCEPTED" ]; then
  log "    confirmed: accepts the real VERIFIED formula at the same scale"
else
  fail_case "index_partition_is_total refused production's own VERIFIED formula at scale 50: $TOTAL_GREEN_OUT"
fi

# ── C-F: index_wait itself ──────────────────────────────────────────────

# run_case builds a fresh fake `aws` that returns the given sequence of
# resourcegroupstaggingapi get-resources counts (one per poll, comma
# separated - e.g. "104,104,900,1655"), runs index_wait() in a minimal
# stubbed harness at the given scale and region, and prints its stdout for
# the caller to check, followed by one RESULT line carrying the globals
# index_wait is contracted to set.
# Args: <label> <counts-csv> <scale> <region> <wait_s> <poll_s> [partition-override]
run_case() {
  local label="$1" counts_csv="$2" scale="$3" region="$4" wait_s="$5" poll_s="$6" override="${7:-}"
  local case_dir="$WORK/$label"
  mkdir -p "$case_dir/bin"
  local seq_file="$case_dir/seq"
  printf '%s' "$counts_csv" | tr ',' '\n' > "$seq_file"
  local state_file="$case_dir/next_line"
  printf '1\n' > "$state_file"

  # Fakes exactly what livecert_rgta_count (lib/live-cert.sh) calls:
  # `aws resourcegroupstaggingapi get-resources --tag-filters
  # Key=tofu-estate,Values=<estate> --query
  # 'ResourceTagMappingList[].ResourceARN' --output text`. Each invocation
  # advances one line through the pre-scripted sequence and prints that many
  # fake ARNs, tab-separated (matching the real CLI's --output text
  # concatenation livecert_rgta_count relies on) - so the count index_wait
  # sees is exactly the count this self-test scripted, not a re-derivation
  # of AWS's own pagination shape (selftest-pagination.sh already covers
  # that seam).
  cat > "$case_dir/bin/aws" <<FAKEEOF
#!/usr/bin/env bash
args="\$*"
case "\$args" in
  *resourcegroupstaggingapi*get-resources*'ResourceTagMappingList[].ResourceARN'*)
    n=\$(sed -n "\$(cat '$state_file')p" '$seq_file')
    ln=\$(( \$(cat '$state_file') + 1 ))
    printf '%s\n' "\$ln" > '$state_file'
    if [ -z "\$n" ]; then
      echo "fake aws: index_wait polled past the scripted sequence ($counts_csv)" >&2
      exit 2
    fi
    i=0
    out=""
    while [ "\$i" -lt "\$n" ]; do
      out="\${out:+\$out\$(printf '\t')}arn:aws:x:\$i"
      i=\$((i + 1))
    done
    printf '%s\n' "\$out"
    ;;
  *)
    echo "fake aws: unrecognized invocation for this self-test: \$args" >&2
    exit 2
    ;;
esac
FAKEEOF
  chmod +x "$case_dir/bin/aws"

  local runner="$case_dir/runner.sh"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -uo pipefail'
    # The plain log() production's own live/live-cert/terralith-scale.sh
    # defines, unmodified, so the lines this self-test greps for are the
    # exact lines a real run would print.
    printf '%s\n' 'log() { printf "%s\n" "$*"; }'
    # livecert_rgta_count and livecert_aws come from the real lib, unmodified
    # - only the `aws` binary they invoke is faked, so this exercises the
    # actual production query path, not a rewritten one.
    printf '%s\n' "$(cat "$ROOT/live/live-cert/lib/live-cert.sh")"
    printf 'REGION=%q\n' "$region"
    printf 'ENDPOINT=\n'
    printf 'ESTATE=tl-livecert-selftest\n'
    printf 'SCALE=%q\n' "$scale"
    printf '%s\n' "$VERIFIED_SRC"
    printf '%s\n' "$PARTITION_SRC"
    # An override replaces index_partition AFTER the real one is defined, so
    # the case is explicit about substituting it (case F only).
    [ -n "$override" ] && printf '%s\n' "$override"
    printf 'LIVECERT_INDEX_WAIT_S=%q\n' "$wait_s"
    printf 'LIVECERT_INDEX_POLL_S=%q\n' "$poll_s"
    printf 'INDEX_LAG_S=0\n'
    printf 'INDEX_TARGET_N=0\n'
    printf 'INDEX_CONVERGED=skipped\n'
    printf '%s\n' "$INDEX_WAIT_SRC"
    printf '%s\n' 'index_wait'
    printf '%s\n' 'printf "RESULT rc=%s converged=%s target=%s lag=%s\n" "$?" "$INDEX_CONVERGED" "$INDEX_TARGET_N" "$INDEX_LAG_S"'
  } > "$runner"

  # The fake aws goes first on PATH, set at invocation rather than baked into
  # the runner's own text, so nothing here has to fight shellcheck over a
  # single-quoted "$PATH" that is meant to expand only when runner.sh runs.
  PATH="$case_dir/bin:$PATH" bash "$runner"
}

# expect_result greps one RESULT field out of a case's output.
expect_result() {
  local out="$1" field="$2" want="$3" label="$4"
  local got
  got="$(grep -oE "${field}=[^ ]+" <<< "$out" | tail -1)"
  if [ "$got" != "${field}=${want}" ]; then
    fail_case "$label: expected ${field}=${want}, got '${got:-<absent>}'"
    return 1
  fi
  return 0
}

log ""
log "=== case C: converges on the REACHABLE target in us-east-2 at scale 50 (0, 50, 104 -> 104, not 1655) ==="
log "    this is the case that could not happen before #1143: the real run sat at exactly 104 for its full bound"
CASE_C_OUT="$(run_case caseC "0,50,104" 50 us-east-2 1800 1)"
printf '%s\n' "$CASE_C_OUT" | sed 's/^/  /'
if ! grep -qE '^index converged after [0-9]+s: 104 of a reachable 104\.' <<< "$CASE_C_OUT"; then
  fail_case "case C: no 'index converged after <s>s: 104 of a reachable 104' line"
else
  log "  confirmed: converged on 104, the reachable target, not on 1655"
fi
if ! grep -qF 'NOT waiting for 550 aws_iam_role(s)' <<< "$CASE_C_OUT"; then
  fail_case "case C: the wait did not say which roles it is excluding, or said the wrong count - a wait that silently excludes things is how #1143 became invisible"
else
  log "  confirmed: named the 550 excluded roles and why"
fi
if ! grep -qF 'NOT waiting for 1001 global object(s)' <<< "$CASE_C_OUT"; then
  fail_case "case C: the wait did not say that the 1001 global objects are invisible from us-east-2"
else
  log "  confirmed: named the 1001 global objects excluded by region"
fi
if ! grep -qF '1551 stamped object(s) are outside what the tag index can hold' <<< "$CASE_C_OUT"; then
  fail_case "case C: the converged line does not say how far short of the 1655 stamped objects the target is - a reader would take it as 'every object is in the index' (#1143's own 'Do')"
else
  log "  confirmed: the converged line refuses to be read as 1655 of 1655"
fi
expect_result "$CASE_C_OUT" converged yes "case C" && log "  confirmed: index_converged=yes"
expect_result "$CASE_C_OUT" target 104 "case C" && log "  confirmed: index_target=104 rides into the recorded row"
CASE_C_POLLS="$(grep -cE '^  index wait: t=' <<< "$CASE_C_OUT")"
if [ "$CASE_C_POLLS" != "3" ]; then
  fail_case "case C: expected 3 poll lines (one per scripted count), got $CASE_C_POLLS"
fi

log ""
log "=== case D: us-east-1 counts the global half - target 1105, not 104 ==="
CASE_D_OUT="$(run_case caseD "104,1001,1105" 50 us-east-1 1800 1)"
printf '%s\n' "$CASE_D_OUT" | sed 's/^/  /'
if ! grep -qE '^index converged after [0-9]+s: 1105 of a reachable 1105\.' <<< "$CASE_D_OUT"; then
  fail_case "case D: no 'converged ... 1105 of a reachable 1105' line - a us-east-1 run must count the global objects the index does hold there"
else
  log "  confirmed: converged on 1105"
fi
if grep -qF 'NOT waiting for 1001 global' <<< "$CASE_D_OUT"; then
  fail_case "case D: excluded the global objects in us-east-1, where the index DOES hold them"
fi
if ! grep -qF 'NOT waiting for 550 aws_iam_role(s)' <<< "$CASE_D_OUT"; then
  fail_case "case D: the roles must be excluded in every region, us-east-1 included (#1134)"
else
  log "  confirmed: the roles are still excluded in us-east-1"
fi
expect_result "$CASE_D_OUT" target 1105 "case D" && log "  confirmed: index_target=1105"

log ""
log "=== case E: a genuine lag - reachable target 104, index stuck at 12, bound trips at 2s ==="
CASE_E_OUT="$(run_case caseE "12,12,12,12,12,12" 50 us-east-2 2 1)"
printf '%s\n' "$CASE_E_OUT" | sed 's/^/  /'
if ! grep -qE '^index NOT CONVERGED: 12 of a reachable 104 after 2s\.' <<< "$CASE_E_OUT"; then
  fail_case "case E: no 'index NOT CONVERGED' line - the old wording ('still at N of M, proceeding') read as progress, which is the half of #1143 that made it survivable"
else
  log "  confirmed: the timeout path says NOT CONVERGED, in those words"
fi
if grep -qE '^index converged' <<< "$CASE_E_OUT"; then
  fail_case "case E: printed a converged line on the timeout path"
fi
expect_result "$CASE_E_OUT" rc 0 "case E" \
  && log "  confirmed: still returns 0, so test_plan runs and records the product's own refusal (#1046/#1049)"
expect_result "$CASE_E_OUT" converged no "case E" \
  && log "  confirmed: index_converged=no - a recorded row can no longer be read as a converged measurement"
expect_result "$CASE_E_OUT" target 104 "case E" && log "  confirmed: index_target=104"

log ""
log "=== case F: a zero target skips the wait and makes NO aws call at all ==="
log "    driven by substituting index_partition, since this estate's own regional bucket (2*SCALE+4) is never empty;"
log "    what is under test is index_wait's handling of a target it cannot reach even in principle, not the partition."
# The fake aws is given an EMPTY scripted sequence, so any call at all exits
# 2 with a diagnosis - the case fails loudly if the wait polls.
CASE_F_OVERRIDE='index_partition() { printf "0 0 %s %s\n" "$(( 20 * SCALE + 1 ))" "$(( 13 * SCALE + 4 ))"; }'
CASE_F_OUT="$(run_case caseF "" 50 us-east-2 1800 1 "$CASE_F_OVERRIDE")"
printf '%s\n' "$CASE_F_OUT" | sed 's/^/  /'
if ! grep -qF 'index wait SKIPPED' <<< "$CASE_F_OUT"; then
  fail_case "case F: a zero target did not skip - with 'idx_n >= target' and target 0, the first poll 'converges' on 0 of 0, which is a false pass"
else
  log "  confirmed: skipped, rather than reporting a 0-of-0 convergence"
fi
if grep -qE '^  index wait: t=' <<< "$CASE_F_OUT"; then
  fail_case "case F: polled the tag index despite having no reachable target - that is the dead time #1143 is about"
else
  log "  confirmed: made no AWS call"
fi
expect_result "$CASE_F_OUT" converged na "case F" \
  && log "  confirmed: index_converged=na - not 'yes', which is what a 0-of-0 convergence would have recorded"

log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-index-wait: PASS ==="
else
  log "=== selftest-index-wait: FAIL - see above ==="
fi
exit $((1 - pass))
