#!/usr/bin/env bash
# The day2_crash tier-1 fixture, GitHub issue #805, built from #503's proven
# deterministic self-interrupt mechanism (internal/command/apply_e2etesting_crash.go)
# rather than reference-ec2-vpc's full estate - #522's ruling is explicit
# that stage activation is gated on a minimal representative-set fixture,
# not a 26-estate sweep, and reference-ec2-vpc's own Part H already proved
# the mechanism works; this pins it standalone so day2_crash can activate
# without depending on the whole board.
#
# The claim under test, live/GAUNTLET.md #10, word for word: "A replace
# interrupted after the create and before the destroy is recovered by the
# next plan without a human: the old object is destroyed, the new one is
# bound." Oracle: "Stock records the old object as deposed and destroys it
# on the next apply; the outcome after one more apply must be the same" -
# OpenTofu's own documented deposed-object semantics, which this script
# checks choudoufu's own record and plan sequence against by value, the
# same way reference-ec2-vpc's Part H does (no separate stock run needed:
# the deposed/destroy mechanics under test are choudoufu's live-marker
# record and re-plan, not the destroy-graph walker itself, which #557
# already established is unmodified stock).
#
# Three resources: aws_vpc.main, aws_subnet.main, aws_instance.main (the
# create_before_destroy replace target - server-minted identity, its own
# InstanceId). A crash strictly "between the create and the destroy" is
# only reachable through the create-then-destroy ordering create_before_destroy
# requests; the default ordering destroys first and there is no window at
# all - reference-ec2-vpc's own resource_block_crash comment established
# this and it applies unchanged here.
#
# The mechanism (issue #490/#503): internal/command/apply_e2etesting_crash.go's
# PostApply hook self-delivers SIGTERM the instant TOFU_E2E_APPLY_RESOURCE_INTERRUPT's
# named address completes a real, non-null apply (a create, never a destroy).
# Two facts, confirmed by reading the code, make this deterministic BY
# CONSTRUCTION rather than by timing luck: (1) managedResourceExecute deposes
# the old object and writes the new one as current, in memory, before
# PostApply ever fires, and the record's one write-back happens once, after
# the whole (possibly interrupted) graph walk returns; (2) -parallelism=1
# serializes the graph walker to one worker and the hook runs synchronously
# inside that worker's own call stack, so nothing else can be dispatched
# until it returns. This script exercises it ONCE, with no retry loop: the
# construction guarantees the window every time, so a first attempt that
# does not land is a real defect, not bad luck, and is reported as a hard
# failure rather than retried.
#
# What #943 (issue #938) taught, since fixed and re-broken deliberately by
# BREAK_CRASH=1's own control below is a DIFFERENT thing: #938 found that an
# apply destroying a deposed object recorded no tombstone, so the terminated
# object's own lingering tag (still readable through the tagging API after
# termination - documented AWS behaviour, #670's lingering-tag case) read as
# a SECOND live claimant on the next plan, and choudoufu refused the whole
# estate with "Two live resources claiming one address." This script's H3
# step - one more plan after the recovery apply, asserting "No changes" -
# is exactly the assertion that regression broke: before #943's fix this
# step failed with that collision error, not with an empty-plan failure.
#
#   bash live/e2e/crash-interrupt/run.sh
#   BREAK_CRASH=1 bash live/e2e/crash-interrupt/run.sh   # must exit non-zero
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`
#                for the NON-crash binary only - the interrupt-capable
#                binary is always built fresh from this worktree's own
#                source (see step 0b), the same choice #503's own script
#                made, since the point is to exercise this tree's engine.
#   FLOCI_PORT   host port for the emulator (default 4770 - clear of every
#                other shape fixture's own default port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass (or, under BREAK_CRASH=1, an exit code that
# must be non-zero - see the header above), non-zero on a real failure.
# Every assertion reads actual command output, an exit code, the record
# file's own JSON, or the emulator's answer through the AWS CLI - never a
# timeout.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4770}"
FLOCI_NAME="choudoufu-crash-interrupt-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-east-1"

ESTATE="crash-interrupt-e2e"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
record_import_id() { jq -r '.identity.import_id' "$1"; }

provider_block() {
  cat <<'EOF'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "crash-interrupt-e2e"
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
}

# resource_block($1 = ami): the create_before_destroy replace target
# (aws_instance.main) plus the vpc/subnet it needs to launch at all. $1
# drives the ForceNew ami argument so the caller can trigger a real replace
# by rewriting only this value.
resource_block() {
  local ami="$1"
  cat <<EOF
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = {
    Name = "crash-interrupt-vpc"
  }
}

resource "aws_subnet" "main" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.0.1.0/24"
  availability_zone = "us-east-1a"
  tags = {
    Name = "crash-interrupt-subnet"
  }
}

resource "aws_instance" "main" {
  ami           = "$ami"
  instance_type = "t3.micro"
  subnet_id     = aws_subnet.main.id

  tags = {
    Name = "crash-interrupt-instance"
  }

  lifecycle {
    create_before_destroy = true
  }
}
EOF
}

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v jq >/dev/null 2>&1 || fail "jq is not on PATH"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

# 0b. The interrupt-capable binary. Always built fresh from THIS worktree's
# own source, unconditionally - the same choice #503's reference-ec2-vpc
# script made, since the point is to exercise this tree's own engine
# regardless of where $TOFU came from. Behaviourally identical to $TOFU for
# everything except the one env-gated hook: e2eTestingFeatures is an
# ldflags-only gate (cmd/choudoufu/testing.go) and PostApply is a no-op
# unless TOFU_E2E_APPLY_RESOURCE_INTERRUPT names a matching address.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for the deterministic interrupt)"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

ESTATE_DIR="$WORK/estate"
mkdir -p "$ESTATE_DIR"

AMI_A="ami-87654321"
AMI_B="ami-91000001"

{
  provider_block
  echo
  resource_block "$AMI_A"
} > "$ESTATE_DIR/main.tf"

# ── 2. stand the estate up ──────────────────────────────────────────────────
log "=== 2. apply: vpc, subnet, and the create_before_destroy instance ==="
( cd "$ESTATE_DIR" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "init failed"
APPLY1_OUT="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1_OUT" | tail -30
  fail "the initial apply failed"
}
grep -qE 'Apply complete! Resources: 3 added' <<< "$APPLY1_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY1_OUT"; fail "the initial apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY1_OUT")"

PRE_ID="$(awsl ec2 describe-instances \
  --filters "Name=tag:tofu-address,Values=aws_instance.main" "Name=instance-state-name,Values=running,pending" \
  --query "Reservations[0].Instances[0].InstanceId" --output text)"
[ -n "$PRE_ID" ] && [ "$PRE_ID" != "None" ] || fail "no live aws_instance.main found by its marker after the initial apply"
PRE_STATE="$(awsl ec2 describe-instances --instance-ids "$PRE_ID" --query "Reservations[0].Instances[0].State.Name" --output text)"
[ "$PRE_STATE" = "running" ] || fail "the pre-crash instance $PRE_ID is not running (state=$PRE_STATE)"
log "  $PRE_ID running, ami $AMI_A, carrying this estate's marker"

RECORD_KEY="$(record_key aws_instance.main)"
RECORD="$ESTATE_DIR/.tofu-records/tofu-records/$ESTATE/aws_instance/$RECORD_KEY"
[ -f "$RECORD" ] || fail "no record file at $RECORD after the initial apply"

# ── 3. force a create_before_destroy replace via a ForceNew ami change ─────
log "=== 3. rewrite ami: $AMI_A -> $AMI_B (ForceNew) ==="
{
  provider_block
  echo
  resource_block "$AMI_B"
} > "$ESTATE_DIR/main.tf"

# ── 4. interrupt the replace, exactly once, no retry loop ──────────────────
# The mechanism is deterministic by construction (see header). A single
# attempt that does not land here is a real engine defect, not bad luck,
# and is reported as a hard failure below rather than retried.
log "=== 4. interrupt a real create_before_destroy replace between the create committing and the destroy dispatching ==="
CRASH_OUT="$(cd "$ESTATE_DIR" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="aws_instance.main" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; CRASH_RC=$?
log "  interrupted apply exited $CRASH_RC (a genuine self-terminated apply is not expected to exit 0)"

CANDIDATES="$(awsl ec2 describe-instances \
  --filters "Name=tag:tofu-address,Values=aws_instance.main" "Name=instance-state-name,Values=running,pending" \
  --query "Reservations[].Instances[].[InstanceId,ImageId]" --output text 2>/dev/null || true)"
NEW_ID="$(awk -v old="$PRE_ID" -v ami="$AMI_B" '$1 != old && $2 == ami { print $1; exit }' <<< "$CANDIDATES")"
[ -n "$NEW_ID" ] && [ "$NEW_ID" != "None" ] \
  || { printf '%s\n' "$CRASH_OUT" | tail -30; fail "attempt 1 did not land: no live instance carrying ami $AMI_B and this estate's marker exists after the interrupted apply - the deterministic interrupt did not produce a crash window; this is a real defect, not something to retry"; }

OLD_STATE="$(awsl ec2 describe-instances --instance-ids "$PRE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>&1)"
[ "$OLD_STATE" = "running" ] \
  || fail "attempt 1: old instance $PRE_ID is not still running untouched after the interrupt (state=$OLD_STATE) - the destroy leg dispatched before the interrupt landed, so this is not the crash window day2_crash proves"

DEPOSED_COUNT="$(jq '.deposed | length' "$RECORD" 2>/dev/null || echo 0)"
[ "$DEPOSED_COUNT" = "1" ] \
  || fail "attempt 1: the record carries $DEPOSED_COUNT deposed entries after the interrupt, want exactly 1 - the one write-back this fixture is about (current=new, deposed=old, committed together) did not happen as expected"
DEPOSED_ID="$(jq -r '.deposed | to_entries[0].value.identity.import_id' "$RECORD")"
[ "$DEPOSED_ID" = "$PRE_ID" ] \
  || fail "attempt 1: the record's deposed identity is $DEPOSED_ID, want the pre-crash instance $PRE_ID"
CURRENT_ID="$(record_import_id "$RECORD")"
[ "$CURRENT_ID" = "$NEW_ID" ] \
  || fail "attempt 1: the record's current identity is $CURRENT_ID, want the new instance $NEW_ID"

log "  landed on attempt 1 of 1 - deterministic by construction: old $PRE_ID still running (untouched, confirmed via the AWS CLI), new $NEW_ID running (ami $AMI_A -> $AMI_B, confirmed via the AWS CLI); record's one write-back correctly carries current=$NEW_ID deposed=$DEPOSED_ID together"

if [ "${BREAK_CRASH:-}" = "1" ]; then
  # live/GAUNTLET.md #10's own Break text, verbatim: "Interrupt and then
  # assert nothing is proposed; the assertion must fail." A real crash
  # window recovery DOES propose a destroy of the deposed object - that is
  # the whole point of day2_crash - so asserting the opposite must fail,
  # proving the real H2/H3 assertions below are load-bearing rather than a
  # grep that always matches. Unlike reference-ec2-vpc's own embedded
  # BREAK_CRASH=1 arm (which logs the correct outcome and lets the rest of
  # that multi-stage script continue), this standalone fixture treats the
  # wrong assertion holding as the pass/fail boundary directly: the script
  # itself must exit non-zero here, because that is the only way a
  # BREAK_CRASH=1 invocation of a standalone tier-1 fixture can be "proven
  # red" rather than merely logged as correct.
  log "=== BREAK_CRASH=1: assert nothing is proposed after the interrupt - this must fail ==="
  BREAK_PLAN_OUT="$(cd "$ESTATE_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK_CRASH=1 plan itself exited $BREAK_PLAN_RC, which is a different failure than the control is about"; }
  if grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_PLAN_OUT"; then
    fail "BREAK_CRASH=1: the plan after a real interrupted create_before_destroy replace came back empty - the wrong assertion held, so this stage's own check is not load-bearing (this line should never print; if it does, the fixture itself is broken)"
  fi
  grep -qE 'aws_instance\.main \(deposed object [0-9a-f]+\) will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_CRASH=1: the plan after the crash does not even propose the expected destroy - the fixture is not what this control expects"; }
  fail "BREAK_CRASH=1: correctly proposes destroying the deposed object ($DEPOSED_ID) - the empty-plan assertion this control makes on purpose correctly fails to hold, so this script exits non-zero as designed. day2_crash's H2/H3 assertions below are proven load-bearing by this control."
fi

# ── 5. the next plan recovers on its own: destroy the deposed object ───────
log "=== 5. the recovery plan: destroy the deposed object, nothing else ==="
PLAN_OUT="$(cd "$ESTATE_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "the recovery plan exited $PLAN_RC"; }
grep -qE 'aws_instance\.main \(deposed object [0-9a-f]+\) will be destroyed' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^  # .+ will be'; fail "the recovery plan does not propose destroying the deposed object"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | tail -10; fail "the recovery plan proposes something other than exactly one destroy"; }
log "  choudoufu: exactly one destroy - the deposed object ($DEPOSED_ID), nothing else"

APPLY2_OUT="$(cd "$ESTATE_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the recovery apply exited $APPLY2_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the recovery apply did not match the planned 0 add / 1 destroy"; }

OLD_FINAL_STATE="$(awsl ec2 describe-instances --instance-ids "$PRE_ID" --query "Reservations[0].Instances[0].State.Name" --output text 2>&1)"
[ "$OLD_FINAL_STATE" = "terminated" ] || fail "$PRE_ID (the crashed-out old object) is not terminated after the recovery apply (state=$OLD_FINAL_STATE)"
log "  $PRE_ID terminated - confirmed via the AWS CLI, not through choudoufu's own report"

DEPOSED_AFTER="$(jq '.deposed | length' "$RECORD")"
[ "$DEPOSED_AFTER" = "0" ] || fail "the record still carries $DEPOSED_AFTER deposed entries after the recovery apply"
CURRENT_AFTER="$(record_import_id "$RECORD")"
[ "$CURRENT_AFTER" = "$NEW_ID" ] || fail "the record's current identity changed unexpectedly across the recovery apply: $CURRENT_AFTER"
log "  record: the deposed entry is cleared, current identity is unchanged ($NEW_ID)"

# ── 6. one more plan: fully converged, AND no phantom second claimant ──────
# This is the assertion issue #938/PR #943 taught: an apply that destroys a
# deposed object has to record that identity as destroyed (a tombstone), or
# the terminated instance's own lingering tag - still readable through the
# tagging API after termination, documented AWS behaviour, floci reproduces
# it too - reads as a second live claimant of aws_instance.main on this next
# plan. Before #943's fix this step failed with "Two live resources
# claiming one address", not with a non-empty plan; this assertion would
# have caught that regression directly.
log "=== 6. one more plan: fully converged, no phantom second claimant from the terminated object's lingering tag ==="
FINAL_PLAN_OUT="$(cd "$ESTATE_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
[ "$FINAL_PLAN_RC" -eq 0 ] || {
  printf '%s\n' "$FINAL_PLAN_OUT" | tail -30
  if grep -qF "Two live resources claiming one address" <<< "$FINAL_PLAN_OUT"; then
    fail "the post-recovery plan refused with a collision on aws_instance.main - the terminated object $PRE_ID's lingering tag was read as a second live claimant, which means the destroy above wrote no tombstone (issue #938/PR #943's regression, back)"
  fi
  fail "the post-recovery plan exited $FINAL_PLAN_RC"
}
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
  || { grep -E '^  #|^Foreign resources:' <<< "$FINAL_PLAN_OUT"; fail "the post-recovery plan is not empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$FINAL_PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$FINAL_PLAN_OUT"; fail "the post-recovery plan reports a foreign resource - the terminated object's lingering tag is still being read as a live, unowned claimant"; }
log "  No changes, Foreign resources: none. The crash window is closed, recovered without a human, and the destroyed identity is not a phantom second claimant."

log ""
log "=== PASS ==="
log ""
log "A real create_before_destroy replace of aws_instance.main was"
log "interrupted with SIGTERM strictly between the create committing and"
log "the destroy of the deposed old object ever dispatching (deterministic"
log "by construction, landed on attempt 1 of 1). The record's one"
log "write-back correctly carried current+deposed together; the next plan"
log "destroyed exactly the deposed object; the plan after that is empty"
log "with no foreign claimant, proving the destroyed identity was recorded"
log "as a tombstone rather than left for its lingering tag to be read as a"
log "second live claimant (issue #938/PR #943)."
