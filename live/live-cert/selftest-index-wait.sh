#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-index-wait.sh: proof for issue #1032, #1046, #1049.
#
# terralith-scale.sh used to walk straight from migrate into test_plan with
# no gap at all. #1046 found that the Resource Groups Tagging API's own
# search index is a separate, eventually-consistent copy of the tags migrate
# just wrote and verified: on the 2026-09-11 scale-50 run the index held 104
# of 1,655 stamped resources 21 minutes after migrate finished with zero
# failures. #1049's fix makes discovery refuse a count/for_each
# aws_iam_policy instance with DIRECT_READ_UNRESOLVED while the index is
# silent for it, rather than proposing a create the provider would reject -
# the safe outcome, but only useful if test_plan actually runs once the
# account has caught up (or, failing that, records how far it had NOT caught
# up).
#
# terralith-scale.sh now calls a new index_wait() function between migrate
# and test_plan: it polls livecert_rgta_count for tofu-estate=$ESTATE - the
# SAME query 4a2's identity check already makes - every LIVECERT_INDEX_POLL_S
# seconds until it reaches $VERIFIED stamped, or LIVECERT_INDEX_WAIT_S runs
# out, and sets INDEX_LAG_S to the elapsed seconds either way. It never fails
# the run: a lagged index after the bound is exactly the condition #1046/
# #1049 are about, and test_plan's own refusal (DIRECT_READ_UNRESOLVED) is
# what records it - index_wait's job is only to give the account a real
# chance to converge first, and to say how long that took either way.
#
# This self-test extracts index_wait() VERBATIM out of a real
# terralith-scale.sh (default: the one shipped beside this script), the same
# way selftest-teardown-timeout.sh extracts teardown() - no AWS calls, no
# docker, no terraform, no go build - and stubs `aws` (via a fake
# livecert_rgta_count-shaped resourcegroupstaggingapi get-resources) so the
# tag index's own count climbs across polls exactly the way a real account's
# would.
#
# Two cases:
#   1. the count climbs 104, 104, 900, 1655 across four 1-second-interval
#      polls against a target of 1655 - RED on the pre-fix script (index_wait
#      does not exist at all, so extraction itself fails and test_plan would
#      have run at 104 with nothing to say why), GREEN after (the converged
#      line prints and the function returns having given the account the
#      chance to catch up).
#   2. the count never reaches the target - the bound trips and the
#      "proceeding" line prints, and the function still returns (never
#      fails), which is what lets test_plan run anyway and record the
#      product's own refusal.
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

# Materialize SRC_ARG into a plain file: it may be a process substitution,
# and extract_func below reads it once but a FIFO source is safest handled
# this way regardless (matches selftest-teardown-timeout.sh).
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"

log "=== selftest-index-wait: extracting index_wait() from $SRC_ARG ==="

# Same brace-depth/heredoc-aware extractor selftest-teardown-timeout.sh uses,
# copied rather than shared, so this self-test has no import of its own to
# keep in sync - index_wait() itself has no heredoc, but the extractor stays
# safe if a future edit adds one.
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
  log "FAIL: could not find index_wait() in $SRC - the wait step is missing (this is the RED result on the pre-fix script)"
  exit 1
fi

BIN="$WORK/bin"; mkdir -p "$BIN"

# run_case builds a fresh fake `aws` that returns the given sequence of
# resourcegroupstaggingapi get-resources counts (one per poll, comma
# separated - e.g. "104,104,900,1655"), runs index_wait() in a minimal
# stubbed harness, and prints its stdout/return code for the caller to
# check. Args: <label> <counts-csv> <verified> <wait_s> <poll_s>
run_case() {
  local label="$1" counts_csv="$2" verified="$3" wait_s="$4" poll_s="$5"
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
    printf 'REGION=us-east-1\n'
    printf 'ENDPOINT=\n'
    printf 'ESTATE=tl-livecert-selftest\n'
    printf 'VERIFIED=%q\n' "$verified"
    printf 'LIVECERT_INDEX_WAIT_S=%q\n' "$wait_s"
    printf 'LIVECERT_INDEX_POLL_S=%q\n' "$poll_s"
    printf 'INDEX_LAG_S=0\n'
    printf '%s\n' "$INDEX_WAIT_SRC"
    printf '%s\n' 'index_wait'
  } > "$runner"

  # The fake aws goes first on PATH, set at invocation rather than baked into
  # the runner's own text, so nothing here has to fight shellcheck over a
  # single-quoted "$PATH" that is meant to expand only when runner.sh runs.
  PATH="$case_dir/bin:$PATH" bash "$runner"
}

log ""
log "=== case 1: converges (104, 104, 900, 1655 against a target of 1655, 1s interval) ==="
CASE1_OUT="$(run_case case1 "104,104,900,1655" 1655 1800 1)"
CASE1_RC=$?
printf '%s\n' "$CASE1_OUT" | sed 's/^/  /'
if [ "$CASE1_RC" -ne 0 ]; then
  log "FAIL: index_wait exited $CASE1_RC on the converging sequence - it must return 0 either way"
  pass=0
fi
if ! grep -qE '^index converged after [0-9]+s: 1655 of 1655$' <<< "$CASE1_OUT"; then
  log "FAIL: no 'index converged after <s>s: 1655 of 1655' line in case 1's output"
  pass=0
else
  log "  confirmed: the converged line printed"
fi
CASE1_POLLS="$(grep -cE '^  index wait: t=' <<< "$CASE1_OUT")"
if [ "$CASE1_POLLS" != "4" ]; then
  log "FAIL: expected 4 poll lines (one per scripted count), got $CASE1_POLLS"
  pass=0
else
  log "  confirmed: polled exactly 4 times, once per scripted count (104, 104, 900, 1655), before returning"
fi
if grep -qE '^index still at' <<< "$CASE1_OUT"; then
  log "FAIL: case 1 printed the 'still at ... proceeding' line - it should have converged instead"
  pass=0
fi

log ""
log "=== case 2: never converges (stays at 104 against a target of 1655), bound trips at 2s, 1s interval ==="
CASE2_OUT="$(run_case case2 "104,104,104,104,104,104" 1655 2 1)"
CASE2_RC=$?
printf '%s\n' "$CASE2_OUT" | sed 's/^/  /'
if [ "$CASE2_RC" -ne 0 ]; then
  log "FAIL: index_wait exited $CASE2_RC when the bound tripped - it must return 0 (never fail the run) so test_plan still runs and records the product's own refusal"
  pass=0
fi
if ! grep -qE '^index still at 104 of 1655 after 2s, proceeding$' <<< "$CASE2_OUT"; then
  log "FAIL: no 'index still at 104 of 1655 after 2s, proceeding' line in case 2's output"
  pass=0
else
  log "  confirmed: the bound-tripped 'proceeding' line printed"
fi
if grep -qE '^index converged' <<< "$CASE2_OUT"; then
  log "FAIL: case 2 printed a converged line - it should never have reached the target"
  pass=0
fi

log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-index-wait: PASS ==="
else
  log "=== selftest-index-wait: FAIL - see above ==="
fi
exit $((1 - pass))
