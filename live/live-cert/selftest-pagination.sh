#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-pagination.sh: proof for issue #1047.
#
# terralith-scale.sh's "4a2. state model" identity check (and several other
# spots - test_apply's before/after counts, livecert_verify_empty's
# informational count) ran `aws resourcegroupstaggingapi get-resources ...
# --query 'length(ResourceTagMappingList)' --output text` and compared the
# result as a single number. The AWS CLI applies `length(...)` PER RESULT
# PAGE (get-resources pages at 50 by default), so a filter matching more
# than one page prints one number per page - "50\n50\n4" for 104 resources
# at scale=50 on the 2026-09-11 run - and every numeric comparison against
# that string errors, which made a real run wrongly report the identity
# piece as unused while 1,655 resources actually carried the tag.
#
# This self-test stubs `aws` with a three-page fake (no real AWS calls) and
# checks two things:
#   1. the OLD query form genuinely does yield one line per page against
#      such a fake, i.e. the bug this issue describes is real and not a
#      one-off CLI quirk (documentation, not a regression guard - this is
#      true regardless of whether the shipped code has been fixed);
#   2. the shared fix (lib/live-cert.sh's livecert_rgta_count, issue
#      #1047) yields exactly ONE line, the correct total across all three
#      pages - THIS is the regression guard: it calls the actual function
#      the repo ships, so a revert or a new bad call site reintroduces the
#      bug and this test goes red.
# It also greps the repo for the old query form so a NEW call site written
# the old way is caught even if it never gets exercised.
#
# Usage: bash live/live-cert/selftest-pagination.sh
#   LIVECERT_DIR=<path> to check a different revision's live/live-cert/
#   (e.g. a temp checkout of the pre-fix tree) instead of the one beside
#   this script.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIVECERT_DIR="${LIVECERT_DIR:-$ROOT/live/live-cert}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
pass=1
log() { printf '%s\n' "$*"; }

log "=== selftest-pagination: checking $LIVECERT_DIR ==="

# ── the fake: a three-page resourcegroupstaggingapi get-resources ────────
# Page sizes 2, 2, 1 (5 resources total, tofu-cert-run=faketag) - small
# enough to read at a glance, multi-page enough to prove pagination is
# actually exercised (a single-page fake would pass the buggy old query
# form too and prove nothing).
BIN="$WORK/bin"; mkdir -p "$BIN"
cat > "$BIN/aws" <<'FAKEEOF'
#!/usr/bin/env bash
# Fakes exactly the two AWS CLI behaviors this issue is about, for
# `resourcegroupstaggingapi get-resources` with a 5-resource, 3-page result
# (page sizes 2, 2, 1):
#   - `--query 'length(ResourceTagMappingList)' --output text` really does
#     apply length() PER PAGE against real AWS, printing one number per
#     page ("2\n2\n1") rather than one total - the bug.
#   - a plain array query (`ResourceTagMappingList[].ResourceARN`) under
#     `--output text` DOES correctly concatenate every page's items,
#     tab/newline-separated - this part of the CLI is not broken, which is
#     exactly what the fix relies on.
args="$*"
case "$args" in
  *resourcegroupstaggingapi*get-resources*)
    case "$args" in
      *'length(ResourceTagMappingList)'*)
        printf '2\n2\n1\n'
        ;;
      *'ResourceTagMappingList[].ResourceARN'*)
        printf 'arn:aws:x:1\tarn:aws:x:2\narn:aws:x:3\tarn:aws:x:4\narn:aws:x:5\n'
        ;;
      *)
        echo "fake aws: unrecognized get-resources --query: $args" >&2
        exit 2
        ;;
    esac
    ;;
  *)
    echo "fake aws: unrecognized invocation: $args" >&2
    exit 2
    ;;
esac
FAKEEOF
chmod +x "$BIN/aws"
export PATH="$BIN:$PATH"

# ── 1. documents the bug: the OLD form really does yield 3 lines ─────────
log "=== 1. the OLD query form ('length(ResourceTagMappingList)') against the 3-page fake ==="
OLD_OUT="$(aws resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-cert-run,Values=faketag" \
  --query 'length(ResourceTagMappingList)' --output text)"
OLD_LINES=$(printf '%s\n' "$OLD_OUT" | wc -l | tr -d ' ')
log "  output: $(printf '%s' "$OLD_OUT" | tr '\n' ',')  (${OLD_LINES} line(s))"
if [ "$OLD_LINES" -gt 1 ]; then
  log "  confirmed: the old form yields one line per page, not one total - this is the bug #1047 describes"
else
  log "FAIL: the fake did not reproduce multi-line output for the old query form - this self-test's fake is broken, not proving anything"
  pass=0
fi
# The actual failure mode: a numeric comparison against multi-line output
# errors rather than silently doing the wrong thing - demonstrate that too.
if [ "$OLD_OUT" -gt 0 ] 2>/tmp/selftest_pagination_cmperr.$$; then
  log "FAIL: '[ \"\$OLD_OUT\" -gt 0 ]' did not error on multi-line input as expected"
  pass=0
else
  CMP_ERR="$(cat /tmp/selftest_pagination_cmperr.$$ 2>/dev/null)"
  log "  confirmed: a numeric comparison against that output errors: ${CMP_ERR:-<no stderr captured>}"
fi
rm -f /tmp/selftest_pagination_cmperr.$$

# ── 2. the regression guard: the SHIPPED fix, against the same fake ──────
log "=== 2. the fix: lib/live-cert.sh's livecert_rgta_count against the same 3-page fake ==="
LIB="$LIVECERT_DIR/lib/live-cert.sh"
if [ ! -f "$LIB" ]; then
  log "FAIL: $LIB does not exist"
  pass=0
else
  # Used indirectly: livecert_aws (called by livecert_rgta_count, defined
  # by the sourced lib below) reads both as globals.
  # shellcheck disable=SC2034
  REGION=us-east-1
  # shellcheck disable=SC2034
  ENDPOINT=""
  # shellcheck disable=SC1090
  if ! source "$LIB" 2>"$WORK/source.err"; then
    log "FAIL: sourcing $LIB failed: $(cat "$WORK/source.err")"
    pass=0
  fi
  if ! declare -f livecert_rgta_count >/dev/null 2>&1; then
    log "FAIL: livecert_rgta_count is not defined after sourcing $LIB - the fix is missing"
    pass=0
  else
    NEW_OUT="$(livecert_rgta_count tofu-cert-run faketag)"
    NEW_LINES=$(printf '%s\n' "$NEW_OUT" | wc -l | tr -d ' ')
    log "  output: '$NEW_OUT' (${NEW_LINES} line(s))"
    if [ "$NEW_LINES" = "1" ] && [ "$NEW_OUT" = "5" ]; then
      log "  confirmed: one line, correct total (5) across all three pages"
    else
      log "FAIL: expected exactly one line reading '5', got ${NEW_LINES} line(s): '$NEW_OUT'"
      pass=0
    fi
    # The comparison the real 4a2 check makes must now succeed cleanly.
    if [ "${NEW_OUT:-0}" -gt 0 ] 2>"$WORK/cmpok.err"; then
      log "  confirmed: '[ \"\${ident_n:-0}\" -gt 0 ]' (4a2's own gate) succeeds cleanly against it"
    else
      log "FAIL: the fixed count did not compare cleanly: $(cat "$WORK/cmpok.err")"
      pass=0
    fi
  fi
fi

# ── 3. no call site anywhere under live/live-cert/ still uses the old form
# (comment lines that only quote the old form to document the bug - this
# self-test's own header included - are not offenders; only a line that is
# not a comment is)
log "=== 3. grep: no remaining live use of the old length(ResourceTagMappingList) form ==="
OFFENDERS="$(grep -rn --exclude=selftest-pagination.sh "length(ResourceTagMappingList)" "$LIVECERT_DIR" 2>/dev/null \
  | grep -vE ':[[:space:]]*#' || true)"
if [ -z "$OFFENDERS" ]; then
  log "  none found"
else
  log "FAIL: the old query form is still present in:"
  printf '%s\n' "$OFFENDERS" | sed 's/^/    /'
  pass=0
fi

log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-pagination: PASS ==="
else
  log "=== selftest-pagination: FAIL - see above ==="
fi
exit $((1 - pass))
