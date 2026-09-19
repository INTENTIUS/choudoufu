#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-record-store-s3.sh: proof for issue #1145.
#
# terralith-scale.sh branched on RECORD_STORE_BACKEND in two places and both
# were written `if ssm ... else <local disk>`, so a run with
# RECORD_STORE_BACKEND=s3 took the else in both:
#
#   1. the state-model values-piece check was skipped entirely, while
#      logging `record_store is "s3" (local disk), so the cloud values piece
#      is NOT under test` - a check that could not fail, giving a reason
#      that named a backend the run was not using;
#   2. teardown's record-store cleanup was skipped, so every object the run
#      wrote stayed in the bucket, under a "VERIFIED EMPTY by listing"
#      verdict that had never listed the store at all.
#
# This self-test extracts verify_empty()/checked_list()/ssm_prefix_count()/
# s3_prefix_count()/teardown() and the 4a2 state-model REGION verbatim out
# of a real terralith-scale.sh and runs them against a stub `aws` whose
# whole world is two text files - one line per S3 object key, one per SSM
# parameter name. No docker, no real AWS, no terraform, no go build.
#
# The stub is not trusted on its own. LIVECERT_SELFTEST_ENDPOINT=<url>
# swaps it for the REAL aws CLI against a real S3 implementation (floci:
# `docker run -d --rm -p 4899:4566 $(cat live/floci-image)`, then
# LIVECERT_SELFTEST_ENDPOINT=http://127.0.0.1:4899), so the same assertions
# run against the real wire. Every behaviour the stub imitates - including
# list-objects-v2 rendering an unmatched prefix as the literal string
# "None", which is why s3_prefix_count carries its own JMESPath default -
# was measured against floci first and is re-provable by that flag.
#
# Usage: bash live/live-cert/selftest-record-store-s3.sh
#   TERRALITH_SCALE_SH=<path> to extract from a different revision, e.g.
#   the pre-fix content via process substitution:
#     TERRALITH_SCALE_SH=<(git show main:live/live-cert/terralith-scale.sh) \
#       bash live/live-cert/selftest-record-store-s3.sh
#   LIVECERT_SELFTEST_ENDPOINT=<url> to use the real aws CLI against a real
#   S3/SSM endpoint instead of the stub.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

log() { printf '%s\n' "$*"; }
pass=1
fail_note() { log "FAIL: $*"; pass=0; }

# Materialize SRC_ARG into a plain file: it may be a process substitution
# (e.g. TERRALITH_SCALE_SH=<(git show ...)), and a FIFO can only be read
# once, while the extractors below read it several times.
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"

log "=== selftest-record-store-s3: extracting from $SRC_ARG ==="

# Same brace-depth/heredoc-aware extractor selftest-teardown-timeout.sh,
# selftest-index-wait.sh and selftest-hold-resume.sh use. Kept identical on
# purpose: a divergence here would be a fourth slightly-different copy.
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

# The values-piece check is not a function, it is a REGION of the script's
# top-level flow, so it is extracted between the two stage banners that
# bracket it. Both banners have been stable since the 4a2 block was added;
# a rename fails loudly below rather than silently testing nothing.
extract_region() {
  local from="$1" to="$2" f="$3"
  awk -v from="$from" -v to="$to" '
    index($0, from) { grab = 1 }
    grab && index($0, to) { exit }
    grab { print }
  ' "$f"
}

VERIFY_SRC="$(extract_func verify_empty "$SRC")"
CHECKED_SRC="$(extract_func checked_list "$SRC")"
SSMCOUNT_SRC="$(extract_func ssm_prefix_count "$SRC")"
S3COUNT_SRC="$(extract_func s3_prefix_count "$SRC")"
TEARDOWN_SRC="$(extract_func teardown "$SRC")"
PROVIDER_SRC="$(extract_func provider_block "$SRC")"
VALUES_SRC="$(extract_region '=== 4a2. state model' '=== 4b. test_plan' "$SRC")"

[ -n "$VERIFY_SRC" ]   || { log "FAIL: could not find verify_empty() in $SRC_ARG"; exit 1; }
[ -n "$CHECKED_SRC" ]  || { log "FAIL: could not find checked_list() in $SRC_ARG"; exit 1; }
[ -n "$SSMCOUNT_SRC" ] || { log "FAIL: could not find ssm_prefix_count() in $SRC_ARG"; exit 1; }
[ -n "$TEARDOWN_SRC" ] || { log "FAIL: could not find teardown() in $SRC_ARG"; exit 1; }
[ -n "$PROVIDER_SRC" ] || { log "FAIL: could not find provider_block() in $SRC_ARG"; exit 1; }
[ -n "$VALUES_SRC" ]   || { log "FAIL: could not find the 4a2 state-model region in $SRC_ARG"; exit 1; }
# s3_prefix_count is the fix's own helper: absent on the pre-fix script,
# which is a RED result to report rather than an extraction error to die on.
if [ -z "$S3COUNT_SRC" ]; then
  log "  note: $SRC_ARG has no s3_prefix_count() - expected on a pre-#1145 revision; the cases below will show what it did instead"
  S3COUNT_SRC='s3_prefix_count() { echo "s3_prefix_count is not defined in this revision" >&2; echo 0; }'
fi

BUCKET="chdf1145-selftest"
PREFIX="sel1145"
ESTATE="tl-livecert-$PREFIX"
REGION="us-east-1"

########################################################################
# The stub world: two text files, one key/name per line.
########################################################################
BIN="$WORK/bin"; mkdir -p "$BIN"
OBJ="$WORK/s3-objects"
PARAM="$WORK/ssm-params"

ENDPOINT="${LIVECERT_SELFTEST_ENDPOINT:-}"
if [ -n "$ENDPOINT" ]; then
  log "=== selftest-record-store-s3: LIVECERT_SELFTEST_ENDPOINT=$ENDPOINT - using the REAL aws CLI against it, not the stub ==="
  command -v aws >/dev/null 2>&1 || { log "FAIL: aws is not on PATH"; exit 1; }
  # test/test, forced, never "${AWS_ACCESS_KEY_ID:-test}" (#1380). The
  # endpoint this mode names is an emulator - floci on a loopback port - and
  # the :- form sent the caller's REAL key to it whenever one happened to be
  # exported, which is both a credential leaving the machine for a process
  # nobody audited and a run whose identity depends on the caller's shell.
  # An emulator accepts anything; a real endpoint named here by mistake now
  # gets a key that cannot authenticate, which fails loudly instead of
  # acting.
  export AWS_ACCESS_KEY_ID=test
  export AWS_SECRET_ACCESS_KEY=test
  unset AWS_SESSION_TOKEN AWS_SECURITY_TOKEN AWS_PROFILE AWS_DEFAULT_PROFILE
  export AWS_PAGER=""
  aws --endpoint-url "$ENDPOINT" --region "$REGION" s3api create-bucket --bucket "$BUCKET" >/dev/null 2>&1 || true
else
  cat > "$BIN/aws" <<STUBEOF
#!/usr/bin/env bash
# Stub aws. Its whole world is two files, one key/name per line. Every
# behaviour below was measured against floci first - notably the "None"
# rendering of an unmatched list-objects-v2 prefix - and
# LIVECERT_SELFTEST_ENDPOINT re-runs the same assertions against the real
# CLI to keep this honest.
OBJ=$(printf '%q' "$OBJ")
PARAM=$(printf '%q' "$PARAM")
STUBEOF
cat >> "$BIN/aws" <<'STUBEOF'
args=()
query=""
prefix=""
path=""
bucket=""
name=""
recursive=0
while [ $# -gt 0 ]; do
  case "$1" in
    --endpoint-url|--region|--output|--status|--scope|--family-prefix|--cluster|--filters|--tag-filters|--hosted-zone-id|--service|--desired-count|--policy-arn|--role-name|--instance-profile-name|--policy-name)
      shift 2 ;;
    --query)     query="$2"; shift 2 ;;
    --prefix)    prefix="$2"; shift 2 ;;
    --path)      path="$2"; shift 2 ;;
    --bucket)    bucket="$2"; shift 2 ;;
    --name)      name="$2"; shift 2 ;;
    --recursive) recursive=1; shift ;;
    --*)         shift ;;
    *)           args+=("$1"); shift ;;
  esac
done

svc="${args[0]:-}"
op="${args[1]:-}"

emit_text() {  # stdin: matching lines. Renders them the way --output text does.
  local lines; lines="$(cat)"
  if [ -z "$lines" ]; then
    # An absent JMESPath key renders as the literal "None"; a
    # present-but-empty list renders as nothing. `|| `[]`` in the query is
    # exactly the difference. Measured against floci 2026-09-17.
    case "$query" in
      *'|| `[]`'*) : ;;
      'Contents[].Key') printf 'None\n' ;;
      *) : ;;
    esac
    return 0
  fi
  printf '%s\n' "$lines" | paste -sd '\t' -
}

case "$svc/$op" in
  s3api/list-objects-v2)
    grep -F -- "$prefix" "$OBJ" 2>/dev/null | grep -E "^$(printf '%s' "$prefix" | sed 's/[.[\*^$/]/\\&/g')" | emit_text
    ;;
  s3/rm)
    # args[1] is "rm", args[2] is s3://bucket/prefix
    target="${args[2]:-}"
    p="${target#s3://}"; p="${p#*/}"
    [ "$recursive" = "1" ] || { echo "stub aws: s3 rm without --recursive" >&2; exit 2; }
    tmp="$(mktemp)"
    grep -vE "^$(printf '%s' "$p" | sed 's/[.[\*^$/]/\\&/g')" "$OBJ" > "$tmp" 2>/dev/null
    mv "$tmp" "$OBJ"
    ;;
  ssm/get-parameters-by-path)
    # A present-but-empty Parameters list renders as nothing, never "None".
    grep -E "^$(printf '%s' "$path" | sed 's/[.[\*^$/]/\\&/g')" "$PARAM" 2>/dev/null | emit_text
    ;;
  ssm/delete-parameter)
    tmp="$(mktemp)"
    grep -vxF -- "$name" "$PARAM" > "$tmp" 2>/dev/null
    mv "$tmp" "$PARAM"
    ;;
  *)
    # Every other listing (iam/ec2/route53/ecs) comes back empty and
    # successful: an account already clean apart from the record store.
    ;;
esac
exit 0
STUBEOF
  chmod +x "$BIN/aws"
  PATH="$BIN:$PATH"
  export PATH
  : > "$OBJ"
  : > "$PARAM"
fi

# seed_store <n> puts n record objects plus the sentinel plus the guided
# hint plus one unrelated object into the store, and the SSM equivalents.
# reset_store empties it.
reset_store() {
  if [ -n "$ENDPOINT" ]; then
    aws --endpoint-url "$ENDPOINT" --region "$REGION" s3 rm "s3://$BUCKET" --recursive >/dev/null 2>&1
  else
    : > "$OBJ"; : > "$PARAM"
  fi
}
put_object() {
  if [ -n "$ENDPOINT" ]; then
    local body; body="$(mktemp)"; printf 'x\n' > "$body"
    aws --endpoint-url "$ENDPOINT" --region "$REGION" s3api put-object --bucket "$BUCKET" --key "$1" --body "$body" >/dev/null 2>&1
    rm -f "$body"
  else
    printf '%s\n' "$1" >> "$OBJ"
  fi
}
list_all_objects() {
  if [ -n "$ENDPOINT" ]; then
    aws --endpoint-url "$ENDPOINT" --region "$REGION" s3api list-objects-v2 --bucket "$BUCKET" \
      --query 'Contents[].Key || `[]`' --output text 2>/dev/null | tr '\t' '\n' | grep -c . || true
  else
    grep -c . "$OBJ" || true
  fi
}
count_matching() {
  if [ -n "$ENDPOINT" ]; then
    aws --endpoint-url "$ENDPOINT" --region "$REGION" s3api list-objects-v2 --bucket "$BUCKET" --prefix "$1" \
      --query 'Contents[].Key || `[]`' --output text 2>/dev/null | tr '\t' '\n' | grep -c . || true
  else
    grep -cE "^$(printf '%s' "$1" | sed 's/[.[\*^$/]/\\&/g')" "$OBJ" || true
  fi
}

seed_store() {
  reset_store
  put_object "choudoufu/livecert/$PREFIX/.store-sentinel"
  put_object "choudoufu/livecert/$PREFIX/aws_vpc/YXdzX3ZwYy5tYWlu"
  put_object "choudoufu/livecert/$PREFIX/null_resource/bnVsbF9yZXNvdXJjZS5lZmZlY3Q"
  # NOT under the configured key_prefix: HintKey() is
  # "tofu-hints/<estate>/guided" by construction. Measured against floci.
  put_object "tofu-hints/$ESTATE/guided"
  # Somebody else's object in the same bucket. Teardown must not touch it.
  put_object "someone-elses/data/keep-me"
}

# common_preamble emits the variable assignments and stubs every case's
# runner needs before the extracted code.
common_preamble() {
  printf '%s\n' '#!/usr/bin/env bash'
  printf '%s\n' 'set -uo pipefail'
  printf 'ROOT=%q\n' "$ROOT"
  printf '%s\n' '. "$ROOT/live/live-cert/lib/live-cert.sh"'
  printf '%s\n' "$CHECKED_SRC"
  printf '%s\n' "$SSMCOUNT_SRC"
  printf '%s\n' "$S3COUNT_SRC"
  printf '%s\n' 'log() { printf "%s\n" "$*"; }'
  printf '%s\n' 'fail() { printf "FAIL-CALLED: %s\n" "$*"; exit 9; }'
  # The IDENTITY half of the 4a2 region is not what this self-test is about,
  # and it is the one piece that genuinely needs an account: it counts
  # resourcegroupstaggingapi matches for tofu-estate. Stubbed to "one
  # resource" so the values half - the whole subject of #1145 - is the only
  # thing any case below can fail on, in both stub and real-endpoint mode.
  # The tofu-cert-run counter verify_empty uses for its informational line
  # answers 0, because no case here creates a tagged resource.
  printf '%s\n' 'livecert_rgta_count() { case "$1" in tofu-estate) echo 1 ;; *) echo 0 ;; esac; }'
  printf 'PREFIX=%q\n' "$PREFIX"
  printf 'ESTATE=%q\n' "$ESTATE"
  printf 'RUN_ID=%q\n' "selftest-1145"
  printf 'REGION=%q\n' "$REGION"
  printf 'ENDPOINT=%q\n' "$ENDPOINT"
  printf 'RECORD_STORE_BUCKET=%q\n' "$BUCKET"
  printf 'RECORD_KEY_PREFIX=%q\n' "choudoufu/livecert/$PREFIX"
  printf 'SSM_PREFIX=%q\n' "/choudoufu/livecert/$PREFIX"
  printf 'S3_PREFIX=%q\n' "choudoufu/livecert/$PREFIX/"
  printf 'HINT_SSM_PREFIX=%q\n' "/tofu-hints/$ESTATE"
  printf 'HINT_S3_PREFIX=%q\n' "tofu-hints/$ESTATE/"
}

########################################################################
# Case 1: the values-piece check, RECORD_STORE_BACKEND=s3, store WRITTEN.
# It must run, name s3, and not say "local disk".
########################################################################
log ""
log "=== case 1: values-piece check with RECORD_STORE_BACKEND=s3 over a store that WAS written ==="
seed_store
R1="$WORK/case1.sh"
{
  common_preamble
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=s3\n'
  printf '%s\n' "$VALUES_SRC"
} > "$R1"
C1_OUT="$(bash "$R1" 2>&1)"; C1_RC=$?
printf '%s\n' "$C1_OUT" | sed 's/^/    | /'
if [ "$C1_RC" -ne 0 ]; then
  fail_note "the values-piece check exited $C1_RC over a store holding 3 objects - it must pass"
fi
if grep -qF '(local disk)' <<< "$C1_OUT"; then
  fail_note "the values-piece check called an s3 store \"local disk\" - defect 2 of #1145, still present"
fi
if ! grep -qE 'values \(record_store s3 at s3://' <<< "$C1_OUT"; then
  fail_note "the values-piece check never named the s3 store it was given - defect 1 of #1145, still present (the check did not run)"
fi

########################################################################
# Case 2: the same check over an EMPTY store. It must FAIL the stage.
# This is the half that proves case 1 is a check and not a print
# statement: the pre-fix script passes both, which is the whole defect.
########################################################################
log ""
log "=== case 2: values-piece check with RECORD_STORE_BACKEND=s3 over a store that was NEVER written ==="
reset_store
put_object "someone-elses/data/keep-me"
R2="$WORK/case2.sh"
{
  common_preamble
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=s3\n'
  printf '%s\n' "$VALUES_SRC"
} > "$R2"
C2_OUT="$(bash "$R2" 2>&1)"; C2_RC=$?
printf '%s\n' "$C2_OUT" | sed 's/^/    | /'
if [ "$C2_RC" -eq 0 ]; then
  fail_note "the values-piece check PASSED over an s3 store holding nothing - a check that cannot fail (defect 1 of #1145)"
elif ! grep -qF 'values piece unused' <<< "$C2_OUT"; then
  fail_note "the values-piece check exited $C2_RC but never said the values piece was unused: $(head -1 <<< "$C2_OUT")"
fi

########################################################################
# Case 3: an unknown backend must be refused where it is read, not fall
# through to a local-disk claim.
########################################################################
log ""
log "=== case 3: values-piece check with a typo'd RECORD_STORE_BACKEND ==="
R3="$WORK/case3.sh"
{
  common_preamble
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=s33\n'
  printf '%s\n' "$VALUES_SRC"
} > "$R3"
C3_OUT="$(bash "$R3" 2>&1)"; C3_RC=$?
printf '%s\n' "$C3_OUT" | sed 's/^/    | /'
if grep -qF '(local disk)' <<< "$C3_OUT"; then
  fail_note "a typo'd backend was reported as local disk - defect 2 of #1145, still present"
fi
if [ "$C3_RC" -eq 0 ]; then
  fail_note "a typo'd backend passed the values-piece check silently"
fi

########################################################################
# Case 4: teardown must delete both namespaces and leave everything else.
########################################################################
log ""
log "=== case 4: teardown with RECORD_STORE_BACKEND=s3 ==="
seed_store
BEFORE_REC="$(count_matching "choudoufu/livecert/$PREFIX/")"
BEFORE_HINT="$(count_matching "tofu-hints/$ESTATE/")"
BEFORE_OTHER="$(count_matching "someone-elses/")"
log "  before teardown: $BEFORE_REC record object(s), $BEFORE_HINT hint object(s), $BEFORE_OTHER unrelated object(s)"
[ "$BEFORE_REC" = "3" ] || fail_note "seeding is wrong: expected 3 record objects, got $BEFORE_REC"

R4="$WORK/case4.sh"
COLD4="$WORK/cold4"; mkdir -p "$COLD4"   # no terraform.tfstate: trusted destroy is skipped
{
  common_preamble
  printf '%s\n' "$PROVIDER_SRC"
  printf '%s\n' "$VERIFY_SRC"
  printf '%s\n' "$TEARDOWN_SRC"
  printf '%s\n' 'sweep() { printf "  [harness] sweep called\n"; }'
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=s3\n'
  printf 'TEARDOWN_DONE=0\n'
  printf 'MIGRATE_DONE=0\n'
  printf 'LIVECERT_HOLD=0\n'
  printf 'LIVECERT_KEEP_WORK=1\n'
  printf 'UNTRUSTED_TEARDOWN_TIMEOUT_S=5\n'
  printf 'SCALE=1\n'
  printf 'ADOPTED_DIR=%q\n' "$WORK/adopted4"
  printf 'COLD_DIR=%q\n' "$COLD4"
  printf 'WORK=%q\n' "$WORK/case4work"
  printf 'FLOCI_NAME=none\n'
  printf '%s\n' 'mkdir -p "$WORK"'
  printf '%s\n' 'teardown'
} > "$R4"
# teardown()'s own exit status is not asserted: it is invoked from a trap
# in the real script and nothing reads it. The listing after it, and the
# VERIFIED EMPTY line, are the verdict.
C4_OUT="$(bash "$R4" 2>&1)"
printf '%s\n' "$C4_OUT" | sed 's/^/    | /'

AFTER_REC="$(count_matching "choudoufu/livecert/$PREFIX/")"
AFTER_HINT="$(count_matching "tofu-hints/$ESTATE/")"
AFTER_OTHER="$(count_matching "someone-elses/")"
log "  after teardown: $AFTER_REC record object(s), $AFTER_HINT hint object(s), $AFTER_OTHER unrelated object(s)"

[ "$AFTER_REC" = "0" ] || fail_note "teardown left $AFTER_REC record object(s) under choudoufu/livecert/$PREFIX/ - defect 3 of #1145, still present"
[ "$AFTER_HINT" = "0" ] || fail_note "teardown left $AFTER_HINT guided-discovery hint object(s) under tofu-hints/$ESTATE/ - the hint does not live under the configured key_prefix, so a prefix-scoped delete misses it"
[ "$AFTER_OTHER" = "1" ] || fail_note "teardown touched an unrelated object in the same bucket: $BEFORE_OTHER before, $AFTER_OTHER after"

# The verdict line has to agree with the listing. A run that leaks and still
# prints VERIFIED EMPTY is the reporting half of defect 3.
if [ "$AFTER_REC" != "0" ] || [ "$AFTER_HINT" != "0" ]; then
  if grep -qF 'VERIFIED EMPTY' <<< "$C4_OUT"; then
    fail_note "teardown printed VERIFIED EMPTY while the store still held $AFTER_REC record and $AFTER_HINT hint object(s)"
  fi
else
  if ! grep -qF 'VERIFIED EMPTY' <<< "$C4_OUT"; then
    fail_note "the store is empty by independent listing but teardown did not report VERIFIED EMPTY"
  fi
fi

########################################################################
# Case 5: verify_empty must call a leak a leak. This is the BREAK arm for
# case 4's verdict: the store is deliberately re-seeded AFTER teardown's
# delete would have run, so verify_empty is the only thing standing
# between a leaking run and a green verdict.
########################################################################
log ""
log "=== case 5 (BREAK arm): verify_empty over a store that still holds something ==="
# Run TWICE, seeding one namespace at a time. A single run seeding both
# would stay green with either listing deleted from verify_empty, because
# the other one would still find its object - an allowlist that bounds who
# rather than what. Proven: deleting only the record-store listing from
# verify_empty left the combined version of this case passing.
R5="$WORK/case5.sh"
{
  common_preamble
  printf '%s\n' "$VERIFY_SRC"
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=s3\n'
  printf '%s\n' 'if verify_empty; then printf "VERDICT: empty\n"; else printf "VERDICT: not empty\n"; fi'
} > "$R5"

reset_store
put_object "choudoufu/livecert/$PREFIX/aws_vpc/YXdzX3ZwYy5tYWlu"
log "  5a: one record object, no hint"
C5A_OUT="$(bash "$R5" 2>&1)"
printf '%s\n' "$C5A_OUT" | sed 's/^/    | /'
if ! grep -qF 'VERDICT: not empty' <<< "$C5A_OUT"; then
  fail_note "verify_empty reported EMPTY over a bucket holding a record object - the VERIFIED EMPTY line does not cover the record namespace"
fi

reset_store
put_object "tofu-hints/$ESTATE/guided"
log "  5b: one hint object, no records"
C5B_OUT="$(bash "$R5" 2>&1)"
printf '%s\n' "$C5B_OUT" | sed 's/^/    | /'
if ! grep -qF 'VERDICT: not empty' <<< "$C5B_OUT"; then
  fail_note "verify_empty reported EMPTY over a bucket holding a guided-discovery hint object - the VERIFIED EMPTY line does not cover the hint namespace"
fi

########################################################################
# Case 6: the ssm backend's TEARDOWN, which is all that is left of it.
# GitHub issue #1346 retired Parameter Store as a record store: choudoufu
# refuses the block and this harness refuses a new run that names it (case
# 6c). What stays is cleanup. A work dir held from before #1346 recorded
# RECORD_STORE_BACKEND=ssm, its parameters are still in the account, and a
# teardown-only dispatch is how they get deleted - so teardown's ssm arm and
# verify_empty's ssm listings are still asserted rather than assumed. The
# values-piece check is not: no run can reach it with ssm any more, and its
# ssm arm is gone (case 6d).
########################################################################
log ""
log "=== case 6: teardown with RECORD_STORE_BACKEND=ssm (cleanup of a run from before #1346) ==="
seed_params() {
  if [ -n "$ENDPOINT" ]; then
    for n in "/choudoufu/livecert/$PREFIX/.store-sentinel" \
             "/choudoufu/livecert/$PREFIX/aws_vpc/YXdzX3ZwYy5tYWlu" \
             "/tofu-hints/$ESTATE/guided" \
             "/someone-elses/keep-me"; do
      aws --endpoint-url "$ENDPOINT" --region "$REGION" ssm put-parameter --name "$n" --value x --type String --overwrite >/dev/null 2>&1
    done
  else
    { printf '/choudoufu/livecert/%s/.store-sentinel\n' "$PREFIX"
      printf '/choudoufu/livecert/%s/aws_vpc/YXdzX3ZwYy5tYWlu\n' "$PREFIX"
      printf '/tofu-hints/%s/guided\n' "$ESTATE"
      printf '/someone-elses/keep-me\n'; } > "$PARAM"
  fi
}
count_params() {
  if [ -n "$ENDPOINT" ]; then
    aws --endpoint-url "$ENDPOINT" --region "$REGION" ssm get-parameters-by-path --path "$1" --recursive \
      --query 'Parameters[].Name' --output text 2>/dev/null | tr '\t' '\n' | grep -c . || true
  else
    grep -cE "^$(printf '%s' "$1" | sed 's/[.[\*^$/]/\\&/g')" "$PARAM" || true
  fi
}
seed_params
log "  before teardown: $(count_params "/choudoufu/livecert/$PREFIX") record param(s), $(count_params "/tofu-hints/$ESTATE") hint param(s), $(count_params "/someone-elses") unrelated"
R6="$WORK/case6.sh"
COLD6="$WORK/cold6"; mkdir -p "$COLD6"
{
  common_preamble
  printf '%s\n' "$PROVIDER_SRC"
  printf '%s\n' "$VERIFY_SRC"
  printf '%s\n' "$TEARDOWN_SRC"
  printf '%s\n' 'sweep() { printf "  [harness] sweep called\n"; }'
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=ssm\n'
  printf 'TEARDOWN_DONE=0\n'
  printf 'MIGRATE_DONE=0\n'
  printf 'LIVECERT_HOLD=0\n'
  printf 'LIVECERT_KEEP_WORK=1\n'
  printf 'UNTRUSTED_TEARDOWN_TIMEOUT_S=5\n'
  printf 'SCALE=1\n'
  printf 'ADOPTED_DIR=%q\n' "$WORK/adopted6"
  printf 'COLD_DIR=%q\n' "$COLD6"
  printf 'WORK=%q\n' "$WORK/case6work"
  printf 'FLOCI_NAME=none\n'
  printf '%s\n' 'mkdir -p "$WORK"'
  printf '%s\n' 'teardown'
} > "$R6"
C6_OUT="$(bash "$R6" 2>&1)"; C6_RC=$?
printf '%s\n' "$C6_OUT" | sed 's/^/    | /'
A6_REC="$(count_params "/choudoufu/livecert/$PREFIX")"
A6_HINT="$(count_params "/tofu-hints/$ESTATE")"
A6_OTHER="$(count_params "/someone-elses")"
log "  after teardown: $A6_REC record param(s), $A6_HINT hint param(s), $A6_OTHER unrelated"
[ "$C6_RC" -eq 0 ] || fail_note "the ssm teardown exited $C6_RC over a store holding records"
[ "$A6_REC" = "0" ] || fail_note "ssm teardown left $A6_REC record parameter(s) under /choudoufu/livecert/$PREFIX"
[ "$A6_HINT" = "0" ] || fail_note "ssm teardown left $A6_HINT guided-discovery hint parameter(s) under /tofu-hints/$ESTATE"
[ "$A6_OTHER" = "1" ] || fail_note "ssm teardown touched an unrelated parameter: 1 before, $A6_OTHER after"

# The same one-namespace-at-a-time BREAK arm case 5 runs for s3, so each of
# the ssm listings in verify_empty is independently load-bearing.
R6V="$WORK/case6v.sh"
{
  common_preamble
  printf '%s\n' "$VERIFY_SRC"
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=ssm\n'
  printf '%s\n' 'if verify_empty; then printf "VERDICT: empty\n"; else printf "VERDICT: not empty\n"; fi'
} > "$R6V"
seed_one_param() {
  if [ -n "$ENDPOINT" ]; then
    for n in $(aws --endpoint-url "$ENDPOINT" --region "$REGION" ssm describe-parameters --query 'Parameters[].Name' --output text 2>/dev/null | tr '\t' '\n'); do
      aws --endpoint-url "$ENDPOINT" --region "$REGION" ssm delete-parameter --name "$n" >/dev/null 2>&1
    done
    aws --endpoint-url "$ENDPOINT" --region "$REGION" ssm put-parameter --name "$1" --value x --type String --overwrite >/dev/null 2>&1
  else
    printf '%s\n' "$1" > "$PARAM"
  fi
}
seed_one_param "/choudoufu/livecert/$PREFIX/aws_vpc/YXdzX3ZwYy5tYWlu"
log "  6a (BREAK arm): verify_empty with one ssm record parameter, no hint"
C6A="$(bash "$R6V" 2>&1)"; printf '%s\n' "$C6A" | sed 's/^/    | /'
grep -qF 'VERDICT: not empty' <<< "$C6A" || fail_note "verify_empty reported EMPTY over Parameter Store holding a record parameter"
seed_one_param "/tofu-hints/$ESTATE/guided"
log "  6b (BREAK arm): verify_empty with one ssm hint parameter, no records"
C6B="$(bash "$R6V" 2>&1)"; printf '%s\n' "$C6B" | sed 's/^/    | /'
grep -qF 'VERDICT: not empty' <<< "$C6B" || fail_note "verify_empty reported EMPTY over Parameter Store holding a guided-discovery hint parameter"

# 6c: a NEW run that names ssm is refused, with the reason and the
# replacement, and TARGET=aws with no backend named asks for a bucket.
#
# The selection block is extracted from between its two marker lines and run
# ALONE. It is never tested by executing terralith-scale.sh, and that is not
# a style choice: a selection that fails to refuse lets the script carry on
# into a run, and with TARGET=aws that is a paid run. The first version of
# this case executed the script, and a mutation test of it started a real
# cold deploy (GitHub issue #1346). After the block runs, this harness only
# echoes what it decided.
log "  6c: the backend selection refuses a new ssm run, and defaults aws to s3"
SELECT_SRC="$(sed -n '/^# >>> record store backend selection$/,/^# <<< record store backend selection$/p' "$SRC_ARG")"
if [ -z "$SELECT_SRC" ]; then
  fail_note "$SRC_ARG has no '# >>> record store backend selection' block to extract; the selection is untested"
else
  run_selection() { # <env assignments...> ; prints the block's output then "DECIDED backend=<b> rc=<n>"
    ( unset RECORD_STORE_BUCKET RECORD_STORE_BACKEND TARGET TEARDOWN_ONLY_DIR LIVECERT_TEARDOWN_ONLY
      for kv in "$@"; do export "$kv"; done
      bash -c 'set -u; PREFIX=sel1346; REGION=us-east-1; TEARDOWN_ONLY_DIR="${TEARDOWN_ONLY_DIR:-}"
'"$SELECT_SRC"'
echo "DECIDED backend=$RECORD_STORE_BACKEND"' 2>&1; echo "RC=$?" )
  }
  C6C="$(run_selection TARGET=floci RECORD_STORE_BACKEND=ssm)"
  printf '%s\n' "$C6C" | sed 's/^/    | /'
  grep -qF 'RC=2' <<< "$C6C" || fail_note "a new RECORD_STORE_BACKEND=ssm run was not refused with exit 2"
  grep -qF 'DECIDED' <<< "$C6C" && fail_note "a new RECORD_STORE_BACKEND=ssm run got past the selection"
  for want in 'retired as a record store' '#1346' 'RECORD_STORE_BACKEND=s3' 'RECORD_STORE_BUCKET'; do
    grep -qF "$want" <<< "$C6C" || fail_note "the ssm refusal does not say \"$want\""
  done
  C6C2="$(run_selection TARGET=aws)"
  printf '%s\n' "$C6C2" | sed 's/^/    | /'
  grep -qF 'RECORD_STORE_BACKEND=s3 needs RECORD_STORE_BUCKET' <<< "$C6C2" \
    || fail_note "TARGET=aws with no backend named did not default to s3 and ask for its bucket"
  grep -qF 'DECIDED' <<< "$C6C2" && fail_note "TARGET=aws with no backend and no bucket got past the selection"
  C6C3="$(run_selection TARGET=aws RECORD_STORE_BACKEND=ssm TEARDOWN_ONLY_DIR=/held/from/before)"
  printf '%s\n' "$C6C3" | sed 's/^/    | /'
  grep -qF 'DECIDED backend=ssm' <<< "$C6C3" || fail_note "a teardown-only dispatch of an ssm work dir was refused; its parameters could never be cleaned up"
  C6C4="$(run_selection TARGET=floci)"
  grep -qF 'DECIDED backend=local' <<< "$C6C4" || fail_note "TARGET=floci with no backend named did not default to local: $C6C4"
fi

# 6d: the values-piece check has no ssm arm, so ssm there is the loud default.
log "  6d: the values-piece check refuses ssm as a backend it cannot list"
R6D="$WORK/case6d.sh"
{
  common_preamble
  printf 'TARGET=aws\n'
  printf 'RECORD_STORE_BACKEND=ssm\n'
  printf '%s\n' "$VALUES_SRC"
} > "$R6D"
C6D="$(bash "$R6D" 2>&1)"; C6D_RC=$?
printf '%s\n' "$C6D" | sed 's/^/    | /'
[ "$C6D_RC" -ne 0 ] || fail_note "the values-piece check passed for RECORD_STORE_BACKEND=ssm; it has no arm for it and must refuse"
grep -qF 'unknown record_store backend "ssm"' <<< "$C6D" || fail_note "the values-piece check did not name ssm as a backend it cannot list"

########################################################################
# Case 7: `terralith-scale.sh teardown <work dir>` on a HELD s3 estate.
# Teardown deleting the objects is worth nothing if the one dispatch a
# held estate is torn down through cannot reach it, and it could not: the
# cold-deploy marker recorded RECORD_STORE_BACKEND but not
# RECORD_STORE_BUCKET, so the `${RECORD_STORE_BUCKET:?}` refusal fired
# during variable setup, long before teardown() - a loud exit that still
# leaves every object in the bucket.
#
# The dispatch is EXTRACTED between its marker comments and run on its own,
# exactly as case 6c runs the backend selection, and for the reason 6c
# gives: this case used to execute terralith-scale.sh with a marker reading
# TARGET=aws, so a mutation of the dispatch's own `exit 0` would have
# carried it into stage 0 and a paid cold deploy with three PATH stubs as
# the only thing in the way (issue #1380). The span from part 1's marker to
# part 2's still covers everything this case is about: the marker read that
# carries RECORD_STORE_BUCKET, the selection whose `${RECORD_STORE_BUCKET:?}`
# refusal is the defect, the derived S3_PREFIX/HINT_S3_PREFIX teardown
# deletes by, and the dispatch that calls teardown().
#
# It needs the stub `aws` on PATH and so does not run in real-endpoint mode.
########################################################################
log ""
log "=== case 7: teardown-only dispatch on a held s3 estate ==="

# Same extractor and same refusal as selftest-hold-resume.sh case 3, copied
# rather than shared for the reason the function extractor above is copied.
# The tail check is what makes it an extraction: sed's range prints to EOF
# when the closing address never matches, and running THAT would be running
# the whole harness under a TARGET=aws marker.
DISPATCH_END='# <<< teardown-only dispatch part 2'
extract_dispatch() {
  local out
  out="$(sed -n '/^# >>> teardown-only dispatch part 1$/,/^# <<< teardown-only dispatch part 2$/p' "$1")"
  [ -n "$out" ] || return 1
  [ "$(printf '%s\n' "$out" | tail -1)" = "$DISPATCH_END" ] || return 1
  printf '%s\n' "$out"
}
DISPATCH_SRC="$(extract_dispatch "$SRC")" || DISPATCH_SRC=""

if [ -n "$ENDPOINT" ]; then
  log "  skipped in real-endpoint mode: this case drives the dispatch through a stub PATH"
elif ! grep -qF 'RECORD_STORE_BUCKET=%s' "$SRC"; then
  fail_note "$SRC_ARG never writes RECORD_STORE_BUCKET into the cold-deploy marker, so \`terralith-scale.sh teardown <dir>\` on a held s3 estate exits at the RECORD_STORE_BUCKET:? refusal with every object still in the bucket - this is the RED result on a pre-#1145 revision"
elif [ -z "$DISPATCH_SRC" ]; then
  if [ "$SRC_ARG" = "$ROOT/live/live-cert/terralith-scale.sh" ]; then
    fail_note "the shipped terralith-scale.sh has no complete '# >>> teardown-only dispatch part 1' ... '$DISPATCH_END' span to extract, so this case cannot run the dispatch without executing the harness, which #1380 forbids; restore the markers (they bracket comments only)"
  else
    log "  $SRC_ARG carries no marked dispatch span (a revision from before #1380); the grep above is the whole check for it, because running it would mean executing the harness"
  fi
else
  seed_store
  C7DIR="$WORK/held7"; mkdir -p "$C7DIR/cold"
  : > "$C7DIR/cold/terraform.tfstate"
  {
    printf 'PREFIX=%s\n' "$PREFIX"
    printf 'SCALE=1\n'
    printf 'TARGET=aws\n'
    printf 'REGION=%s\n' "$REGION"
    printf 'RUN_ID=selftest-1145-held\n'
    printf 'RECORD_STORE_BACKEND=s3\n'
    printf 'RECORD_STORE_BUCKET=%s\n' "$BUCKET"
    printf 'EXPECTED=79\n'
    printf 'TIMESTAMP=2026-01-01T00:00:00Z\n'
  } > "$C7DIR/.livecert-cold-state"
  mkdir -p "$C7DIR/fakebin"
  cp "$BIN/aws" "$C7DIR/fakebin/aws"
  MARKER7="$C7DIR/trusted_destroy_ran"
  cat > "$C7DIR/fakebin/terraform" <<EOF
#!/usr/bin/env bash
touch "$MARKER7"
exit 0
EOF
  cat > "$C7DIR/fakebin/choudoufu" <<'EOF'
#!/usr/bin/env bash
echo "fake choudoufu: not used by teardown-only" >&2
exit 1
EOF
  chmod +x "$C7DIR/fakebin/terraform" "$C7DIR/fakebin/choudoufu"

  # ROOT, LIB and the two `source` lines are the only things the span does
  # not carry: the real script computes them from ${BASH_SOURCE[0]} at the
  # top of the file, above part 1's marker. The last line is a tripwire,
  # unreachable while the dispatch ends in `exit 0`; on the real script the
  # statement after the dispatch is "0. tools", and after that a cold deploy.
  R7="$C7DIR/dispatch.sh"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -uo pipefail'
    printf 'ROOT=%q\n' "$ROOT"
    printf 'LIB=%q\n' "$ROOT/live/live-cert/lib"
    printf '%s\n' 'source "$ROOT/live/e2e/lib/gauntlet.sh"'
    printf '%s\n' 'source "$LIB/live-cert.sh"'
    printf '%s\n' "$DISPATCH_SRC"
    printf '%s\n' 'printf "DISPATCH-FELL-THROUGH\n"'
  } > "$R7"

  # Credentials scrubbed as belt-and-braces (#1380). The safety argument is
  # that the text above ends at the dispatch; this is because teardown()
  # here runs `s3 rm --recursive` and `ssm delete-parameter`, which are
  # destructive in whatever account an ambient chain would resolve to if the
  # stub ahead of it on PATH were ever missed.
  C7_OUT="$(PATH="$C7DIR/fakebin:$PATH" \
    env -u AWS_PROFILE -u AWS_DEFAULT_PROFILE -u AWS_SESSION_TOKEN -u AWS_SECURITY_TOKEN \
      AWS_ACCESS_KEY_ID=invalid AWS_SECRET_ACCESS_KEY=invalid \
      AWS_EC2_METADATA_DISABLED=true \
      AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null \
      AWS_ENDPOINT_URL=http://127.0.0.1:9 \
      TF_COLD_BIN="$C7DIR/fakebin/terraform" \
      TOFU_BIN="$C7DIR/fakebin/choudoufu" \
      LIVECERT_TEARDOWN_ONLY="$C7DIR" \
      LIVECERT_KEEP_WORK=1 \
      bash "$R7" 2>&1)"; C7_RC=$?
  printf '%s\n' "$C7_OUT" | sed 's/^/    | /'
  grep -qF 'DISPATCH-FELL-THROUGH' <<< "$C7_OUT" \
    && fail_note "the teardown-only dispatch did not exit - on the real script the next statement is \"0. tools\", and after it a cold deploy with the marker's TARGET=aws"
  A7_REC="$(count_matching "choudoufu/livecert/$PREFIX/")"
  A7_HINT="$(count_matching "tofu-hints/$ESTATE/")"
  log "  after teardown-only: $A7_REC record object(s), $A7_HINT hint object(s)"
  [ "$C7_RC" -eq 0 ] || fail_note "teardown-only on a held s3 estate exited $C7_RC"
  [ -f "$MARKER7" ] || fail_note "teardown-only never ran the trusted stock destroy"
  [ "$A7_REC" = "0" ] || fail_note "teardown-only left $A7_REC record object(s) in the bucket"
  [ "$A7_HINT" = "0" ] || fail_note "teardown-only left $A7_HINT hint object(s) in the bucket"
fi

########################################################################
log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-record-store-s3: PASS ==="
  exit 0
fi
log "=== selftest-record-store-s3: FAIL ==="
exit 1
