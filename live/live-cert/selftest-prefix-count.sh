#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-prefix-count.sh: proof for issue #1421.
#
# terralith-scale.sh's s3_prefix_count read a FAILED list-objects-v2 as a
# count of zero: `2>/dev/null ... | grep -c . || true`. In the values check
# that was a false failure, which is loud. In teardown it was the quiet
# direction: a throttled or unauthenticated listing printed "0 object(s) to
# delete", the `s3 rm` was skipped, and the run's records stayed in the
# operator's bucket under a line that read as clean.
#
# This self-test extracts s3_prefix_count() and teardown() verbatim out of a
# real terralith-scale.sh and drives them against a fake `aws` whose
# list-objects-v2 does one of four things, chosen by a mode file: returns
# two keys, returns nothing, exits 255 with the CLI's own throttling
# message, or prints garbage and exits 0. The function must count 2, count
# 0, and print FATAL and return non-zero for the last two with nothing on
# stdout. teardown under the failing listing must print its refusal naming
# the prefix, must not print "0 object(s) to delete" or "remaining after
# delete", and must not run `s3 rm`; under the two-key listing it must (the
# control). No docker, no real AWS, no terraform, no go build.
#
# Usage: bash live/live-cert/selftest-prefix-count.sh
#   TERRALITH_SCALE_SH=<path> to extract from a different revision, e.g.
#   the pre-fix content via process substitution:
#     TERRALITH_SCALE_SH=<(git show main:live/live-cert/terralith-scale.sh) \
#       bash live/live-cert/selftest-prefix-count.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
# The status is saved and re-raised, and a run that stops before its last
# case is refused (#1421, the shape #1419 gave selftest-oidc-bootstrap.sh).
selftest_finished=0
# shellcheck disable=SC2154 # selftest_rc is assigned on the trap's first line
trap 'selftest_rc=$?
      rm -rf "$WORK"
      if [ "$selftest_finished" != 1 ]; then
        echo "FAIL: this selftest stopped before its last case, so most of it never ran. Read the output above for where." >&2
        exit 1
      fi
      exit $selftest_rc' EXIT

log() { printf '%s\n' "$*"; }
pass=1
fail_note() { log "FAIL: $*"; pass=0; }

# Materialize SRC_ARG into a plain file: it may be a process substitution,
# and a FIFO can only be read once.
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"

log "=== selftest-prefix-count: extracting from $SRC_ARG ==="

# Same brace-depth/heredoc-aware extractor selftest-record-store-s3.sh,
# selftest-teardown-timeout.sh, selftest-index-wait.sh and
# selftest-hold-resume.sh use. Kept identical on purpose.
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

S3COUNT_SRC="$(extract_func s3_prefix_count "$SRC")"
SSMCOUNT_SRC="$(extract_func ssm_prefix_count "$SRC")"
CHECKED_SRC="$(extract_func checked_list "$SRC")"
VERIFY_SRC="$(extract_func verify_empty "$SRC")"
TEARDOWN_SRC="$(extract_func teardown "$SRC")"
PROVIDER_SRC="$(extract_func provider_block "$SRC")"
for pair in "S3COUNT_SRC:s3_prefix_count" "SSMCOUNT_SRC:ssm_prefix_count" "CHECKED_SRC:checked_list" \
            "VERIFY_SRC:verify_empty" "TEARDOWN_SRC:teardown" "PROVIDER_SRC:provider_block"; do
  var="${pair%%:*}"; fn="${pair#*:}"
  [ -n "${!var}" ] || { log "FAIL: could not find ${fn}() in $SRC_ARG"; exit 1; }
done
log "  extracted s3_prefix_count ($(printf '%s\n' "$S3COUNT_SRC" | wc -l | tr -d ' ') lines) and teardown ($(printf '%s\n' "$TEARDOWN_SRC" | wc -l | tr -d ' ') lines)"

########################################################################
# The fake aws: one mode file, one call log.
########################################################################
BIN="$WORK/bin"; mkdir -p "$BIN"
MODE="$WORK/mode"
CALLS="$WORK/calls"
: > "$CALLS"
cat > "$BIN/aws" <<STUBEOF
#!/usr/bin/env bash
MODE=$(printf '%q' "$MODE")
CALLS=$(printf '%q' "$CALLS")
STUBEOF
cat >> "$BIN/aws" <<'STUBEOF'
args=(); prefix=""
while [ $# -gt 0 ]; do
  case "$1" in
    --endpoint-url|--region|--output|--query|--bucket|--scope|--filters|--tag-filters|--hosted-zone-id|--cluster|--service|--status) shift 2 ;;
    --prefix) prefix="$2"; shift 2 ;;
    --*) shift ;;
    *) args+=("$1"); shift ;;
  esac
done
# The call log holds the positional words only ("s3api list-objects-v2",
# "s3 rm s3://bucket/prefix"): livecert_aws puts --region in front of
# every call, so the raw argument line never starts with the service.
printf '%s\n' "${args[*]}" >> "$CALLS"
case "${args[0]:-}/${args[1]:-}" in
  s3api/list-objects-v2)
    case "$(cat "$MODE")" in
      keys)    printf '%s.store-sentinel\t%saws_vpc/YXdzX3ZwYy5tYWlu\n' "$prefix" "$prefix" ;;
      empty)   ;;
      fail)    echo "An error occurred (SlowDown) when calling the ListObjectsV2 operation (reached max retries: 4): Please reduce your request rate." >&2; exit 255 ;;
      garbage) printf 'None\nWARNING: something the CLI chose to say on stdout\n' ;;
      *)       echo "fake aws: unknown mode $(cat "$MODE")" >&2; exit 97 ;;
    esac
    ;;
  *) ;;   # every other listing: empty and successful, an account already clean
esac
exit 0
STUBEOF
chmod +x "$BIN/aws"
PATH="$BIN:$PATH"; export PATH

BUCKET="chdf1421-selftest"
PREFIX="sel1421"
ESTATE="livecert-$PREFIX"
S3_PREFIX="choudoufu/livecert/$PREFIX/"
HINT_S3_PREFIX="tofu-hints/$ESTATE/"

# The function alone, in this shell, under the stub.
livecert_aws() { aws "$@"; }
RECORD_STORE_BUCKET="$BUCKET"
eval "$S3COUNT_SRC"

# count_case <mode> <want: 2|0|FATAL> <label>
count_case() {
  local mode="$1" want="$2" label="$3" out err rc
  printf '%s\n' "$mode" > "$MODE"
  log ""
  log "=== case $label: list-objects-v2 $mode ==="
  err="$WORK/err.$mode"
  out="$(s3_prefix_count "$S3_PREFIX" 2>"$err")"; rc=$?
  log "  stdout=[$out] rc=$rc stderr=[$(tr '\n' ' ' < "$err" | cut -c1-160)]"
  case "$want" in
    FATAL)
      [ "$rc" -ne 0 ] || fail_note "case $label: a $mode listing returned $rc, so a caller would take [$out] as a measurement"
      [ -z "$out" ] || fail_note "case $label: a $mode listing printed [$out] on stdout - that is a number a caller can mistake for a count"
      grep -q '^FATAL: s3_prefix_count' "$err" || fail_note "case $label: no FATAL line on stderr for a $mode listing"
      grep -qF "s3://$BUCKET/$S3_PREFIX" "$err" || fail_note "case $label: the FATAL line does not name s3://$BUCKET/$S3_PREFIX"
      ;;
    *)
      [ "$rc" -eq 0 ] || fail_note "case $label: a $mode listing returned $rc, wanted 0"
      [ "$out" = "$want" ] || fail_note "case $label: counted [$out], wanted $want"
      ! grep -q 'FATAL' "$err" || fail_note "case $label: FATAL printed for a listing that succeeded"
      ;;
  esac
}
count_case keys    2     "a: two keys"
count_case empty   0     "b: an empty prefix"
count_case fail    FATAL "c: exit 255 with the CLI's throttling message"
count_case garbage FATAL "d: garbage on stdout, exit 0"
[ "$(grep -c '' "$WORK/err.fail")" -ge 1 ] && grep -qF 'exited 255' "$WORK/err.fail" \
  || fail_note "case c: the FATAL line does not carry the CLI's exit status 255"

########################################################################
# teardown, extracted, under the failing listing and under the two keys.
########################################################################
runner() {  # runner <path>: teardown()'s runner, the shape selftest-record-store-s3.sh case 4 uses
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -uo pipefail'
    printf 'ROOT=%q\n' "$ROOT"
    printf '%s\n' '. "$ROOT/live/live-cert/lib/live-cert.sh"'
    printf '%s\n' "$CHECKED_SRC"
    printf '%s\n' "$SSMCOUNT_SRC"
    printf '%s\n' "$S3COUNT_SRC"
    printf '%s\n' "$PROVIDER_SRC"
    printf '%s\n' "$VERIFY_SRC"
    printf '%s\n' "$TEARDOWN_SRC"
    printf '%s\n' 'log() { printf "%s\n" "$*"; }'
    printf '%s\n' 'fail() { printf "FAIL-CALLED: %s\n" "$*"; exit 9; }'
    printf '%s\n' 'heartbeat_stop() { :; }'
    printf '%s\n' 'sweep() { printf "  [harness] sweep called\n"; }'
    printf '%s\n' 'gauntlet_floci_teardown() { :; }'
    printf '%s\n' 'livecert_rgta_count() { echo 0; }'
    printf 'PREFIX=%q\n' "$PREFIX"
    printf 'ESTATE=%q\n' "$ESTATE"
    printf 'RUN_ID=%q\n' "selftest-1421"
    printf 'REGION=us-east-1\n'
    printf 'ENDPOINT=\n'
    printf 'RECORD_STORE_BUCKET=%q\n' "$BUCKET"
    printf 'S3_PREFIX=%q\n' "$S3_PREFIX"
    printf 'HINT_S3_PREFIX=%q\n' "$HINT_S3_PREFIX"
    printf 'SSM_PREFIX=/choudoufu/livecert/%s\n' "$PREFIX"
    printf 'HINT_SSM_PREFIX=/tofu-hints/%s\n' "$ESTATE"
    printf 'TARGET=aws\n'
    printf 'RECORD_STORE_BACKEND=s3\n'
    printf 'TEARDOWN_DONE=0\n'
    printf 'MIGRATE_DONE=0\n'
    printf 'LIVECERT_HOLD=0\n'
    printf 'LIVECERT_KEEP_WORK=1\n'
    printf 'UNTRUSTED_TEARDOWN_TIMEOUT_S=5\n'
    printf 'SCALE=1\n'
    printf 'ADOPTED_DIR=%q\n' "$WORK/adopted"
    printf 'COLD_DIR=%q\n' "$WORK/cold"
    printf 'WORK=%q\n' "$WORK/tdwork"
    printf 'FLOCI_NAME=none\n'
    printf '%s\n' 'mkdir -p "$WORK"'
    printf '%s\n' 'teardown'
  } > "$1"
}
R="$WORK/teardown-runner.sh"
runner "$R"
mkdir -p "$WORK/cold"   # no terraform.tfstate: the trusted destroy is skipped

log ""
log "=== case e: teardown while every listing exits 255 ==="
printf 'fail\n' > "$MODE"
: > "$CALLS"
E_OUT="$(bash "$R" 2>&1)"
printf '%s\n' "$E_OUT" | sed 's/^/    | /'
if grep -qE "s3://$BUCKET/$S3_PREFIX\): 0 object\(s\) to delete" <<< "$E_OUT"; then
  fail_note "case e: teardown printed '0 object(s) to delete' for s3://$BUCKET/$S3_PREFIX over a listing that failed - the failed listing was read as a count of zero (#1421)"
fi
if grep -qE "s3://$BUCKET/$HINT_S3_PREFIX\): 0 object\(s\) to delete" <<< "$E_OUT"; then
  fail_note "case e: teardown printed '0 object(s) to delete' for s3://$BUCKET/$HINT_S3_PREFIX over a listing that failed"
fi
if grep -qE 'remaining after delete: [0-9]' <<< "$E_OUT"; then
  fail_note "case e: teardown printed a 'remaining after delete' count over a listing that failed"
fi
if ! grep -qF "s3://$BUCKET/$S3_PREFIX" <<< "$E_OUT" || ! grep -qiE "could not list|NOT CLEANED UP" <<< "$E_OUT"; then
  fail_note "case e: teardown printed no refusal naming s3://$BUCKET/$S3_PREFIX and the listing failure"
fi
if ! grep -qF 'FATAL: s3_prefix_count' <<< "$E_OUT"; then
  fail_note "case e: no FATAL line from s3_prefix_count in teardown's output"
fi
if grep -q '^s3 rm ' "$CALLS"; then
  fail_note "case e: teardown ran \`s3 rm\` at a prefix it could not list: $(grep '^s3 rm ' "$CALLS" | head -1)"
fi
if grep -qF 'VERIFIED EMPTY' <<< "$E_OUT"; then
  fail_note "case e: teardown printed VERIFIED EMPTY while it could not list the record store"
fi
E_LISTS="$(grep -c '^s3api list-objects-v2$' "$CALLS")"
log "  fake aws saw $E_LISTS list-objects-v2 call(s) and $(grep -c '^s3 rm ' "$CALLS") s3 rm call(s)"
[ "$E_LISTS" -ge 2 ] || fail_note "case e: teardown listed the store $E_LISTS time(s); it should have tried both prefixes"

log ""
log "=== case f (control): teardown while the listing returns two keys ==="
printf 'keys\n' > "$MODE"
: > "$CALLS"
F_OUT="$(bash "$R" 2>&1)"
printf '%s\n' "$F_OUT" | sed 's/^/    | /'
grep -qF "s3://$BUCKET/$S3_PREFIX): 2 object(s) to delete" <<< "$F_OUT" \
  || fail_note "case f: teardown did not count 2 object(s) under s3://$BUCKET/$S3_PREFIX from a listing that returned two keys"
grep -q "^s3 rm s3://$BUCKET/$S3_PREFIX\$" "$CALLS" \
  || fail_note "case f: teardown did not run \`s3 rm\` on s3://$BUCKET/$S3_PREFIX after counting two objects"
grep -q "^s3 rm s3://$BUCKET/$HINT_S3_PREFIX\$" "$CALLS" \
  || fail_note "case f: teardown did not run \`s3 rm\` on s3://$BUCKET/$HINT_S3_PREFIX after counting two objects"
grep -qF 'remaining after delete: 2' <<< "$F_OUT" \
  || fail_note "case f: teardown did not re-count after the delete (the fake never deletes, so 2 is the honest answer)"
! grep -qF 'FATAL' <<< "$F_OUT" || fail_note "case f: a FATAL line over a listing that succeeded"

selftest_finished=1
log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-prefix-count: PASS ==="
  exit 0
fi
log "=== selftest-prefix-count: FAIL ==="
exit 1
