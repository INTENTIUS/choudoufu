#!/usr/bin/env bash
# (moved from the justfile's retired demo-provisioner-taint recipe; run with: just demo-run provisioner-taint)
# Issue #353's crossing: a create-time provisioner that FAILS on an ordinary
# marker-tracked cloud resource. internal/live/stamp marks the object before
# the create request goes out, so the moment the bucket exists it looks
# healthy to every later run; the tofu-provisioned namespace is the only
# thing that says otherwise, and this proves it end to end - the apply
# fails, the object is live and fully marked, the next plan (with no state
# file) proposes replacing it, and the provisioner really re-runs, counted
# from the shell's own side effects rather than from a plan verdict. Also
# pins the three things that must NOT happen: on_failure = continue records
# nothing, changing the provisioner's command text between runs changes
# nothing, and a record must not outlive the failure it describes - the
# operator deletes the half-built object by hand, the next apply re-creates
# it and the provisioner succeeds, and the plan after that has to be empty
# rather than proposing to destroy a healthy bucket. Needs Docker and the
# AWS CLI; runs on its own port (4742) so it can run beside `just demo`.
set -euo pipefail

# GitHub issue #353's crossing: a create-time provisioner on a
# MARKER-TRACKED CLOUD RESOURCE, end to end against a real emulator.
#
# The claim under test, in one sentence: an apply whose create-time
# provisioner failed leaves a live, fully-marked, half-built object behind,
# and the NEXT plan has to say so - which stock does from the tainted bit in
# its state file, and this fork does from one record's Provisioned member
# (internal/live/projection/record.go, GitHub issue #364 unit A1's merged
# envelope - the tofu-provisioned namespace this comment used to name was
# folded into it), because a live marker cannot carry that bit.
#
# Why this exists and a unit test does not replace it. Every piece can be
# individually correct and the crossing still fail. internal/live/stamp
# writes tofu-estate/tofu-address BEFORE the create request goes out, so the
# instant the bucket exists it is fully marked and looks, to every later
# run, exactly like a healthy one. The record has to be written by an apply
# that FAILED (which is not the path anything else in this repository
# exercises), read back by a plan with no state file, and turned into a
# replacement by stock's own graph. Those are three different processes and
# two different commands.
#
# What makes it a measurement rather than an exercise:
#
#   1. The provisioner appends a line to ./provisioner-runs.txt on every
#      real execution. "The provisioner re-ran" is therefore counted from
#      the shell's own side effects, not inferred from a plan verdict - a
#      replace that proposed itself and then did not run the command would
#      pass every plan-level assertion and fail step 6.
#   2. Step 4 is a live mutation check, run every time and not only under
#      BREAK=1: the taint record is moved aside, the plan is re-run, and the
#      run FAILS unless that plan comes out EMPTY. That is the silent
#      under-run issue #353 is about, reproduced deliberately, so step 3's
#      assertion cannot be passing for some other reason.
#   3. Step 7 changes the provisioner's command text between runs and
#      requires the plan to stay empty. Stock has no memory of a
#      provisioner's content and never re-runs one because its command
#      changed; a hash-and-diff design (explicitly rejected in issue #353)
#      would propose a replacement here.
#   4. Steps 13 to 16 walk the other direction, which is the one that
#      destroys a live resource when it is wrong: the operator deletes the
#      half-built object BY HAND (through the AWS CLI, not through
#      choudoufu, which is the natural response to a half-built object), the
#      next apply re-creates it and the provisioner SUCCEEDS. The record has
#      to be gone afterwards. It was not, until the fix these steps pin: the
#      plan that re-created the object read it as ABSENT and so never read
#      the record, and write-back only cleared records the plan had read. A
#      healthy bucket was then proposed for destruction on every plan, with
#      an already-fixed provisioner failure given as the reason.
#
#   bash live/e2e/provisioner-taint/run.sh
#
# Needs Docker and the AWS CLI.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4742, clear of every
#                other harness's own default).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to report step 4's negative control as the run's
#                own result, proving this script's exit code is not
#                vacuously 0.
#   DEBUG_KEEP   set to 1 to skip the exit trap and leave the container and
#                the work directory behind.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, a file on disk, or the
# emulator's own answer through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/provisioner-taint"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4742}"
FLOCI_NAME="choudoufu-provisioner-taint-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="provisioner-taint-e2e"

cleanup() {
  if [ "${DEBUG_KEEP:-}" = "1" ]; then
    printf 'DEBUG_KEEP=1: leaving %s and container %s behind\n' "$WORK" "$FLOCI_NAME" >&2
    return
  fi
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU from $ROOT"
fi

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
grep -q '"s3"' <<< "$HEALTH" || fail "floci did not come up healthy (s3) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

MAIN="$WORK/estate"
mkdir -p "$MAIN"
cp "$FIXTURE/main.tf" "$MAIN/main.tf"
RUNLOG="$MAIN/provisioner-runs.txt"
RECORDS="$MAIN/.tofu-records"
# GitHub issue #364 unit A1 folded the once-separate "tofu-provisioned"
# root into the single per-instance envelope every record now lives in,
# under the one "tofu-records" namespace root
# (internal/live/projection/record.go's RecordKeyPrefix and RecordKey).
PROVISIONED="$RECORDS/tofu-records/$ESTATE"

# provisioned_key reproduces internal/live/projection's RecordKey encoding
# for one instance address: unpadded base64url of the address string.
# Spelled out rather than discovered by globbing the directory, on
# purpose - a step that globbed would still pass if the addresses were
# wrong, which is precisely the failure mode where one resource's failed
# provisioner forces a replacement of another.
provisioned_key() { printf '%s' "$1" | base64 | tr -d '=\n' | tr '+/' '-_'; }

APP_ADDR='aws_s3_bucket.app[0]'
TOLERANT_ADDR='aws_s3_bucket.tolerant'
CONTROL_ADDR='aws_s3_bucket.control'

APP_REC="$PROVISIONED/aws_s3_bucket/$(provisioned_key "$APP_ADDR")"
TOLERANT_REC="$PROVISIONED/aws_s3_bucket/$(provisioned_key "$TOLERANT_ADDR")"
CONTROL_REC="$PROVISIONED/aws_s3_bucket/$(provisioned_key "$CONTROL_ADDR")"

# is_tainted <recfile> - exit 0 iff the record exists and its "provisioned"
# member says tainted:true. A record's mere existence stopped being that
# signal once record-primary identity (rulings/20260823-foundation-order-ruling.md
# item 1) started writing an identity+residue record for every instance a
# create actually reached, tainted or not (issue #541 found this: the
# control and tolerant buckets, whose provisioners never taint them, now
# have record files of their own - identity and residue, no "provisioned"
# member at all). The "provisioned" member, and its "tainted" field
# specifically, is the only thing that still means what "taint record"
# always meant in this script.
is_tainted() {
  [ -f "$1" ] || return 1
  python3 -c '
import json,sys
try:
    p = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
prov = p.get("provisioned")
sys.exit(0 if isinstance(prov, dict) and prov.get("tainted") is True else 1)
' "$1"
}

# count_taint_records counts records under $PROVISIONED whose "provisioned"
# member says tainted:true - not every file in the tree, which now also
# holds one identity+residue record per ordinary applied instance. See
# is_tainted.
count_taint_records() {
  local n=0 f
  while IFS= read -r f; do
    is_tainted "$f" && n=$((n + 1))
  done < <(find "$PROVISIONED" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null)
  printf '%s' "$n"
}

run_log_lines() {
  [ -f "$RUNLOG" ] || { printf '0'; return; }
  wc -l < "$RUNLOG" | tr -d ' '
}

# A fresh prior state on every plan. Live mode rebuilds it from the cloud
# and the record store rather than from a state file, and deleting the file
# is what makes that claim testable rather than assumed: if the tainted bit
# were surviving in terraform.tfstate, every assertion below would pass with
# the record store doing nothing at all.
tofu_plan() {
  rm -f "$MAIN/terraform.tfstate"
  ( cd "$MAIN" && "$TOFU" plan -input=false -no-color -detailed-exitcode "$@" )
}
tofu_apply() {
  rm -f "$MAIN/terraform.tfstate"
  ( cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color -parallelism=1 "$@" )
}

# ── 2. apply with a FAILING create-time provisioner ─────────────────────────
log "=== 2. apply: aws_s3_bucket.app's create-time provisioner fails ==="
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"

set +e
APPLY1="$(tofu_apply 2>&1)"
APPLY1_RC=$?
set -e
[ "$APPLY1_RC" != "0" ] \
  || { tail -30 <<< "$APPLY1"; fail "the apply SUCCEEDED, but its provisioner runs 'exit 3' - the fixture is not exercising a failure at all"; }
grep -q 'local-exec provisioner error' <<< "$APPLY1" \
  || { tail -30 <<< "$APPLY1"; fail "the apply failed, but not on the provisioner - some other error is being measured"; }
log "  apply failed on the provisioner, as designed"

# Everything ahead of app in the depends_on chain was created; app's own
# bucket exists too, because the provider's create SUCCEEDED and only the
# provisioner after it failed. That is the whole shape of the problem.
LIVE="$(awsl s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | sort | tr '\n' ' ')"
for b in pt-e2e-app pt-e2e-control pt-e2e-shrinker-0 pt-e2e-shrinker-1 pt-e2e-tolerant; do
  grep -qw "$b" <<< "$LIVE" || fail "$b is not live after the apply; live buckets are: $LIVE"
done
log "  all five buckets are live, including the half-provisioned one"

# And app is FULLY MARKED. This is the premise of issue #353 stated as an
# assertion rather than as prose: the markers went on before the create, so
# nothing about the live object distinguishes it from a healthy one.
APP_TAGS="$(awsl s3api get-bucket-tagging --bucket pt-e2e-app --output json)"
grep -q "\"$ESTATE\"" <<< "$APP_TAGS" || { printf '%s\n' "$APP_TAGS"; fail "pt-e2e-app carries no tofu-estate marker"; }
# The marker's own escaping: markers.EscapeAddress renders an index key as
# ":0", since "[" and "]" are outside the AWS tag-value charset. Asserted in
# that spelling rather than the address spelling, so a change to the
# escaping shows up here rather than silently passing a looser grep.
grep -q '"aws_s3_bucket.app:0"' <<< "$APP_TAGS" || { printf '%s\n' "$APP_TAGS"; fail "pt-e2e-app carries no tofu-address marker"; }
log "  pt-e2e-app carries both ownership markers: nothing about the live object says its provisioner failed"

# ── 3. the record: the taint bit, and now the identity and residue too ──────
# GitHub issue #364 unit A1 folded the once-separate "tofu-provisioned"
# namespace into the merged per-instance envelope
# (internal/live/projection/record.go): the taint bit is now the
# envelope's Provisioned member rather than the whole payload, and it is
# the envelope's "kind" field - not a directory literal - that keeps this
# key out of the listing which proposes destroying undeclared records.
#
# This used to assert identity, residue and object were ALL absent - "exactly
# the taint bit and nothing else". Issue #541 found that stale: the create
# request that only the PROVISIONER failed after did succeed, so the object
# genuinely exists with a genuine identity and genuine residue arguments
# (force_destroy, unset in main.tf so its default reads back), and
# record-primary identity (rulings/20260823-foundation-order-ruling.md item 1 -
# "every instance's identity in the record, written by live-import and by
# EVERY apply") now records both unconditionally, on any apply that reaches
# a successful create, whether or not a later provisioner step in the SAME
# apply then fails. That is a strictly more complete record, not a wrong
# one - the object field (RECORD_ADMITTED logical types only; aws_s3_bucket
# is an ordinary taggable cloud type) is still absent, and the taint bit is
# still exactly one bit with nothing describing the provisioner's content,
# which is the part issue #353 actually rejected.
log "=== 3. the provisioner taint record: taint bit, plus the identity and residue a real create leaves behind ==="
[ -f "$APP_REC" ] || { find "$RECORDS" -type f | sort; fail "no taint record for $APP_ADDR at $APP_REC"; }
python3 -c '
import json,sys
p=json.load(open(sys.argv[1]))
assert p["address"]==sys.argv[2], "the record names %s, not %s" % (p["address"], sys.argv[2])
assert p["kind"]=="identity", "the record kind is %r, not identity - a provisioner taint has become delete authority" % (p.get("kind"),)
assert p.get("object") is None, "the record carries an object member - aws_s3_bucket is not a RECORD_ADMITTED logical type, so nothing may authorize deletion from this record alone: %r" % (p,)
ident = p.get("identity")
assert ident is not None, "the create succeeded (the bucket is live), so the record should carry its identity too: %r" % (p,)
assert ident.get("import_id") == "pt-e2e-app", "the record identity is %r, not the bucket this apply actually created" % (ident,)
res = p.get("residue")
assert res is not None, "the create succeeded, so residue classification should have run and recorded force_destroy: %r" % (p,)
attrs = res.get("attributes", {})
assert "force_destroy" in attrs, "the residue does not carry force_destroy, the one argument this resource leaves unset: %r" % (res,)
prov = p.get("provisioned")
assert prov is not None, "the record carries no provisioned member: %r" % (p,)
assert prov["tainted"] is True, "the record does not say tainted: %r" % (prov,)
assert set(prov) == {"tainted"}, "the provisioned member carries unexpected fields %r - this member stores ONE BIT, and a memory of the provisioner CONTENT is the design issue #353 rejected" % (sorted(prov),)
' "$APP_REC" "$APP_ADDR" || fail "the taint record's content is wrong (see above)"
log "  $APP_ADDR has a taint record: its real identity, its real residue (force_destroy), and one taint bit - nothing that names the provisioner's own content"

# on_failure = continue means the failure was not an error at all, so stock
# does not taint - and neither may this. A mechanism that taints on any
# provisioner error would write a record here, and the tolerant bucket would
# then be proposed for replacement on every plan forever. Both DO now have
# record files - an ordinary identity+residue record, same as control's -
# since their creates succeeded too; what must not exist is a "provisioned"
# member saying tainted:true.
! is_tainted "$TOLERANT_REC" || fail "$TOLERANT_ADDR got a taint record despite on_failure = continue"
! is_tainted "$CONTROL_REC" || fail "$CONTROL_ADDR got a taint record despite declaring no provisioner at all"
N="$(count_taint_records)"
[ "$N" = "1" ] || { find "$PROVISIONED" -type f | sort; fail "expected exactly 1 taint record, found $N"; }
log "  exactly one: not the on_failure=continue bucket, not the bucket with no provisioner"

[ "$(run_log_lines)" = "2" ] || { cat "$RUNLOG"; fail "expected 2 provisioner executions (tolerant, app), got $(run_log_lines)"; }
log "  2 real provisioner executions so far: $(tr '\n' ' ' < "$RUNLOG")"

# ── 4. the next plan proposes a replace - the whole feature ─────────────────
log "=== 4. re-plan with no state file: the half-built object must be replaced ==="
set +e
PLAN1="$(tofu_plan 2>&1)"
PLAN1_RC=$?
set -e
[ "$PLAN1_RC" = "2" ] \
  || { grep -vE '^(Warning|.*tagging\.go|.*ARN join table)' <<< "$PLAN1" | tail -30; fail "the re-plan exited $PLAN1_RC, want 2 (changes proposed)"; }
grep -qF "$APP_ADDR is tainted, so it must be replaced" <<< "$PLAN1" \
  || { grep -E '^  # aws_s3_bucket|^Plan:' <<< "$PLAN1"; fail "the plan does not propose replacing $APP_ADDR because it is tainted"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$PLAN1" \
  || { grep -E '^Plan:' <<< "$PLAN1"; fail "the plan proposes something other than exactly one replacement - the taint must reach app and nothing else"; }
log "  '$APP_ADDR is tainted, so it must be replaced'; 1 to add, 0 to change, 1 to destroy"

# The negative control, and the reason step 4's assertion means anything.
# Move the record aside and the SAME plan must come out empty - which is the
# silent under-run issue #353 describes, reproduced on purpose. Run every
# time, not only under BREAK=1, because it costs one plan.
mv "$APP_REC" "$WORK/app-record-set-aside"
set +e
PLAN_NOREC="$(tofu_plan 2>&1)"
PLAN_NOREC_RC=$?
set -e
mv "$WORK/app-record-set-aside" "$APP_REC"
if [ "$PLAN_NOREC_RC" != "0" ] || ! grep -qF 'No changes.' <<< "$PLAN_NOREC"; then
  grep -E '^  # aws_s3_bucket|^Plan:|^No changes' <<< "$PLAN_NOREC"
  fail "with the taint record removed the plan still proposed changes (exit $PLAN_NOREC_RC). Something OTHER than the record is carrying the tainted bit, so step 4 is not measuring what it claims to."
fi
log "  negative control: with the record moved aside the same plan says 'No changes' - the record is what carries the bit, and nothing else does"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: reporting the negative control above as this run's own result, to prove the exit code is not vacuously 0"
fi

# ── 5. apply with a SUCCEEDING provisioner ──────────────────────────────────
log "=== 5. apply the replacement, with the provisioner now succeeding ==="
set +e
APPLY2="$(tofu_apply -var app_command='exit 0' -var app_marker=run-2 2>&1)"
APPLY2_RC=$?
set -e
[ "$APPLY2_RC" = "0" ] || { tail -30 <<< "$APPLY2"; fail "the replacement apply failed"; }
grep -qF 'Apply complete! Resources: 1 added, 0 changed, 1 destroyed.' <<< "$APPLY2" \
  || { grep -E 'Apply complete' <<< "$APPLY2"; fail "the replacement apply did not report exactly one added and one destroyed"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY2")"

# ── 6. the provisioner actually RE-RAN, and the record is gone ──────────────
log "=== 6. the shell's own account of what ran ==="
[ "$(run_log_lines)" = "3" ] || { cat "$RUNLOG"; fail "expected 3 provisioner executions after the replacement, got $(run_log_lines)"; }
[ "$(tail -n1 "$RUNLOG")" = "run-2" ] \
  || { cat "$RUNLOG"; fail "the last provisioner execution was not the replacement's - the plan proposed a replace and the command did not actually run"; }
grep -cx 'tolerant' "$RUNLOG" | grep -qx 1 \
  || { cat "$RUNLOG"; fail "the on_failure=continue bucket's provisioner ran again; it was never supposed to be replaced"; }
log "  3 executions, the last one run-2: the provisioner really re-ran on the replacement"

N="$(count_taint_records)"
[ "$N" = "0" ] || { find "$PROVISIONED" -type f | sort; fail "the taint record survived a successful re-run ($N left); the bucket would be proposed for replacement on every plan from now on"; }
log "  the taint record is gone: a succeeding provisioner clears its predecessor's failure"

log "=== 7. a clean re-plan proposes nothing ==="
set +e
PLAN2="$(tofu_plan -var app_command='exit 0' -var app_marker=run-2 2>&1)"
PLAN2_RC=$?
set -e
[ "$PLAN2_RC" = "0" ] && grep -qF 'No changes.' <<< "$PLAN2" \
  || { grep -E '^  # aws_s3_bucket|^Plan:' <<< "$PLAN2"; fail "the clean re-plan proposed changes (exit $PLAN2_RC)"; }
log "  no changes"

# ── 8. a CHANGED provisioner command must change nothing ────────────────────
# Parity, pinned against the design issue #353 rejected. Stock keeps no
# memory of a provisioner's content and never re-runs one because the
# command changed; a stored hash of the provisioner block would propose a
# replacement right here, and it would be a memory stock does not have.
log "=== 8. changing the provisioner's command text: the plan must stay empty ==="
set +e
PLAN3="$(tofu_plan -var app_command='exit 0' -var app_marker=THIS-IS-A-DIFFERENT-COMMAND 2>&1)"
PLAN3_RC=$?
set -e
[ "$PLAN3_RC" = "0" ] && grep -qF 'No changes.' <<< "$PLAN3" \
  || { grep -E '^  # aws_s3_bucket|^Plan:' <<< "$PLAN3"; fail "changing the provisioner's command produced a plan (exit $PLAN3_RC). Stock does not re-run a provisioner because its command changed, and neither may this - see issue #353's rejected hash-and-diff design."; }
[ "$(run_log_lines)" = "3" ] || { cat "$RUNLOG"; fail "a plan executed a provisioner; plan time is a preview and must never run one"; }
log "  no changes, and no provisioner ran: plan time is still a preview"

# ── 9. the destroy-time half, which needs no TAINT record at all ────────────
# Stock only runs a destroy-time provisioner when it is also calling the
# provider's delete, strictly before it. On failure the delete never
# happens, so the object survives WITH ITS MARKER - and the marker's
# continued existence already is the "still needs destroying" signal. This
# step proves that claim rather than restating it: the destroy fails, the
# bucket is still there, still marked, and nothing about the "provisioned"
# member of its (already-existing, ordinary) record changes - see
# count_taint_records's own comment for why "record" and "taint record" are
# no longer the same question in this script.
log "=== 9. a FAILING destroy-time provisioner on a count shrink ==="
touch "$MAIN/fail-destroy"
set +e
APPLY3="$(tofu_apply -var app_command='exit 0' -var app_marker=run-2 -var shrink_count=1 2>&1)"
APPLY3_RC=$?
set -e
[ "$APPLY3_RC" != "0" ] || { tail -20 <<< "$APPLY3"; fail "the destroy-time provisioner did not fail; the fixture is not exercising it"; }
grep -q 'local-exec provisioner error' <<< "$APPLY3" \
  || { tail -30 <<< "$APPLY3"; fail "the apply failed, but not on the destroy-time provisioner"; }

LIVE="$(awsl s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | sort | tr '\n' ' ')"
grep -qw pt-e2e-shrinker-1 <<< "$LIVE" \
  || fail "pt-e2e-shrinker-1 was deleted even though its destroy-time provisioner failed; stock runs the provisioner FIRST and skips the delete"
SHRINK_TAGS="$(awsl s3api get-bucket-tagging --bucket pt-e2e-shrinker-1 --output json)"
grep -q "\"$ESTATE\"" <<< "$SHRINK_TAGS" \
  || { printf '%s\n' "$SHRINK_TAGS"; fail "pt-e2e-shrinker-1 lost its ownership marker; the marker IS the retry signal for this case, so losing it strands the object"; }
N="$(count_taint_records)"
[ "$N" = "0" ] || { find "$PROVISIONED" -type f | sort; fail "a failed DESTROY-time provisioner tainted $N record(s); it must taint none - the surviving marker is the signal"; }
log "  the bucket survives, still marked, and no taint record was written: the marker is the retry signal"

log "=== 10. the retry destroys it, re-running the provisioner ==="
rm -f "$MAIN/fail-destroy"
set +e
APPLY4="$(tofu_apply -var app_command='exit 0' -var app_marker=run-2 -var shrink_count=1 2>&1)"
APPLY4_RC=$?
set -e
[ "$APPLY4_RC" = "0" ] || { tail -30 <<< "$APPLY4"; fail "the retry apply failed"; }
grep -qF 'Apply complete! Resources: 0 added, 0 changed, 1 destroyed.' <<< "$APPLY4" \
  || { grep -E 'Apply complete' <<< "$APPLY4"; fail "the retry did not destroy exactly the one shrunk instance"; }
[ "$(grep -cx 'destroy-1' "$RUNLOG")" = "2" ] \
  || { cat "$RUNLOG"; fail "the destroy-time provisioner did not run twice; at-least-once through the marker is the claim being made"; }
LIVE="$(awsl s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | sort | tr '\n' ' ')"
grep -qw pt-e2e-shrinker-1 <<< "$LIVE" \
  && fail "pt-e2e-shrinker-1 is still live after a successful destroy"
log "  destroyed on the retry, provisioner ran twice: at-least-once, through the marker and no new storage"

# ── 11. a second failure, arranged through count rather than by hand ───
# Step 6 proved a SUCCEEDING re-run clears the record. This proves the other
# way out: an instance the configuration stops declaring is destroyed
# through the ordinary path, and its record must go with it. Getting there
# needs a fresh create-time failure, and a create needs a destroy first -
# so the count goes to 0 and back to 1. Nothing edits the store by hand at
# any point.
log "=== 11. destroy app, then re-create it with a failing provisioner ==="
set +e
APPLY5="$(tofu_apply -var app_command='exit 0' -var app_marker=run-2 -var app_count=0 -var shrink_count=1 2>&1)"
APPLY5_RC=$?
set -e
[ "$APPLY5_RC" = "0" ] || { tail -30 <<< "$APPLY5"; fail "destroying app failed"; }
grep -qF 'Apply complete! Resources: 0 added, 0 changed, 1 destroyed.' <<< "$APPLY5" \
  || { grep -E 'Apply complete' <<< "$APPLY5"; fail "the count-to-zero apply did not destroy exactly the one app instance"; }
[ "$(count_taint_records)" = "0" ] || { find "$PROVISIONED" -type f | sort; fail "destroying a HEALTHY instance wrote a taint record"; }

set +e
APPLY6="$(tofu_apply -var app_command='exit 3' -var app_marker=run-3 -var shrink_count=1 2>&1)"
APPLY6_RC=$?
set -e
[ "$APPLY6_RC" != "0" ] || { tail -20 <<< "$APPLY6"; fail "the re-create apply succeeded; its provisioner runs 'exit 3'"; }
is_tainted "$APP_REC" || { find "$PROVISIONED" -type f | sort; fail "the second create-time failure wrote no taint record"; }
[ "$(count_taint_records)" = "1" ] || { find "$PROVISIONED" -type f | sort; fail "expected exactly 1 taint record after the second failure"; }
[ "$(tail -n1 "$RUNLOG")" = "run-3" ] || { cat "$RUNLOG"; fail "the re-created instance's provisioner did not run"; }
log "  destroyed clean (no taint record), re-created with a failure (one taint record)"

log "=== 12. destroy the tainted instance: its record goes with it ==="
set +e
APPLY7="$(tofu_apply -var app_command='exit 3' -var app_marker=run-3 -var app_count=0 -var shrink_count=1 2>&1)"
APPLY7_RC=$?
set -e
[ "$APPLY7_RC" = "0" ] || { tail -30 <<< "$APPLY7"; fail "the destroy apply failed"; }
grep -qF 'Apply complete! Resources: 0 added, 0 changed, 1 destroyed.' <<< "$APPLY7" \
  || { grep -E 'Apply complete' <<< "$APPLY7"; fail "the destroy apply did not destroy exactly the one app instance"; }
LIVE="$(awsl s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | sort | tr '\n' ' ')"
grep -qw pt-e2e-app <<< "$LIVE" && fail "pt-e2e-app is still live after being destroyed"
N="$(count_taint_records)"
[ "$N" = "0" ] || { find "$PROVISIONED" -type f | sort; fail "$N taint record(s) survived the instance being destroyed; the key would outlive everything it describes"; }
log "  1 destroyed, no taint record left"

# ── 13-16. the operator deletes the half-built object by hand ───────────────
# The severe follow-on defect, walked the way an operator walks it.
#
# Steps 5 and 6 cleared the record through a REPLACE, where the plan read the
# record on its way to proposing the replacement, so write-back held a
# version for it. That is the easy path and it was the only one covered.
# Deleting the object out of band takes the other path: the next plan reads
# ABSENCE, returns from materialize before the taint record is ever
# consulted, and proposes a plain create. Write-back then has no version for
# a record that is very much still there.
#
# Until the fix, that record survived a completely successful provision, and
# every plan afterwards said "is tainted, so it must be replaced" about a
# live, healthy bucket. Step 16 is the assertion that would have failed.
log "=== 13. re-create app with a failing provisioner, ready to be deleted by hand ==="
set +e
APPLY8="$(tofu_apply -var app_command='exit 3' -var app_marker=run-4 -var shrink_count=1 2>&1)"
APPLY8_RC=$?
set -e
[ "$APPLY8_RC" != "0" ] || { tail -20 <<< "$APPLY8"; fail "the re-create apply succeeded; its provisioner runs 'exit 3'"; }
is_tainted "$APP_REC" || { find "$PROVISIONED" -type f | sort; fail "the create-time failure wrote no taint record"; }
[ "$(count_taint_records)" = "1" ] || { find "$PROVISIONED" -type f | sort; fail "expected exactly 1 taint record after the failure"; }
log "  half-built bucket is live and marked, one taint record stands"

log "=== 14. delete the half-built bucket out of band; the plan must propose a plain CREATE ==="
awsl s3api delete-bucket --bucket pt-e2e-app >/dev/null \
  || fail "deleting pt-e2e-app out of band failed"
LIVE="$(awsl s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | sort | tr '\n' ' ')"
grep -qw pt-e2e-app <<< "$LIVE" && fail "pt-e2e-app is still live after being deleted out of band; live buckets are: $LIVE"
set +e
PLAN4="$(tofu_plan -var app_command='exit 3' -var app_marker=run-4 -var shrink_count=1 2>&1)"
PLAN4_RC=$?
set -e
[ "$PLAN4_RC" = "2" ] \
  || { grep -vE '^(Warning|.*tagging\.go|.*ARN join table)' <<< "$PLAN4" | tail -30; fail "the plan after the out-of-band delete exited $PLAN4_RC, want 2 (changes proposed)"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$PLAN4" \
  || { grep -E '^Plan:' <<< "$PLAN4"; fail "the plan does not propose exactly one plain create for the deleted bucket"; }
grep -qF 'is tainted, so it must be replaced' <<< "$PLAN4" \
  && { grep -E '^  # aws_s3_bucket|^Plan:' <<< "$PLAN4"; fail "the plan proposes a REPLACE for an object that does not exist; there is nothing to destroy"; }
# The mechanism of the bug, asserted rather than assumed: this plan did not
# consult the record at all, so nothing about it can clear the record later.
[ "$(count_taint_records)" = "1" ] || { find "$PROVISIONED" -type f | sort; fail "the plan changed the store; a plan must never write"; }
log "  1 to add, 0 to destroy, and the taint record is untouched: this plan never read it"

log "=== 15. the re-create succeeds, and the stale record must go with the failure it described ==="
set +e
APPLY9="$(tofu_apply -var app_command='exit 0' -var app_marker=run-5 -var shrink_count=1 2>&1)"
APPLY9_RC=$?
set -e
[ "$APPLY9_RC" = "0" ] || { tail -30 <<< "$APPLY9"; fail "the re-create apply failed"; }
grep -qF 'Apply complete! Resources: 1 added, 0 changed, 0 destroyed.' <<< "$APPLY9" \
  || { grep -E 'Apply complete' <<< "$APPLY9"; fail "the re-create apply did not add exactly one resource"; }
[ "$(tail -n1 "$RUNLOG")" = "run-5" ] \
  || { cat "$RUNLOG"; fail "the provisioner did not run on the re-created bucket, so 'it succeeded this time' is not established"; }
N="$(count_taint_records)"
[ "$N" = "0" ] || { find "$PROVISIONED" -type f | sort; fail "the taint record survived a successful provision on a re-created object ($N left). The record describes a failure that has been fixed, and the next plan will destroy a healthy bucket because of it."; }
log "  the provisioner ran and succeeded, and the record it would have outlived is gone"

log "=== 16. the plan after that: a healthy bucket, and nothing proposed ==="
set +e
PLAN5="$(tofu_plan -var app_command='exit 0' -var app_marker=run-5 -var shrink_count=1 2>&1)"
PLAN5_RC=$?
set -e
grep -qF 'is tainted, so it must be replaced' <<< "$PLAN5" \
  && { grep -E '^  # aws_s3_bucket|^Plan:' <<< "$PLAN5"; fail "a live, healthy, freshly provisioned bucket is proposed for replacement, and the reason given is a provisioner failure that was fixed an apply ago. This is the defect these four steps exist for."; }
[ "$PLAN5_RC" = "0" ] && grep -qF 'No changes.' <<< "$PLAN5" \
  || { grep -E '^  # aws_s3_bucket|^Plan:' <<< "$PLAN5"; fail "the plan after a clean re-create proposed changes (exit $PLAN5_RC)"; }
log "  no changes"

log "=== PASS ==="
