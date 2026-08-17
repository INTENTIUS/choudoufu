#!/usr/bin/env bash
set -euo pipefail

# Create-over-existing, end to end against a real emulator.
#
# THIS SCRIPT PINS A DEFECT. Every assertion below describes what choudoufu
# does today, not what it should do. Exit 0 means the defect is still there.
# See "when this goes red" at the bottom for what to change when it is fixed.
#
# The claim, in one sentence: a needs-discovery resource whose type loses its
# tags on the provider's list path is invisible to marker discovery, so a
# live-plan proposes CREATING a resource this estate already owns - and an
# apply then creates a second one, carrying the same ownership marker as the
# first, once per run, without an error.
#
# Two resources, differing in exactly one property. Both are
# ClassNeedsDiscovery: nothing in the configuration names either of them, so
# both depend entirely on reading an ownership marker off a listed object.
#
#   aws_vpc.control      ec2:DescribeVpcs returns the object's TagSet, so
#                        discovery reads the marker and binds the instance.
#   aws_iam_role.subject iam:ListRoles returns no tags, and the AWS provider
#                        (6.58.0, internal/service/iam/role_list.go) issues no
#                        GetRole per member, so the listed object arrives with
#                        an empty tag map and its marker cannot be read.
#
# The control is what makes this a measurement rather than an anecdote. If
# both were proposed for creation, the finding would be "this harness cannot
# discover anything". The control binding and the subject not isolates the
# tag-losing list path as the cause.
#
# What the run does NOT do is stay silent, and that is worth reading in the
# output: it reports the estate's own role under "Foreign resources ... not
# owned by estate", with "tags: (none)", because that is what the list call
# said. The one live object choudoufu is looking for is on screen, named,
# described as somebody else's.
#
# The data needed to bind it is on the wire in the same run. The estate-wide
# tagging sweep makes one GetResources call whose answer carries the role's
# ARN and its tofu-estate/tofu-address tags; step 2 asserts that directly
# through the AWS CLI. internal/live/discovery/tagging.go's sweepViaTagging
# discards it, because a type the config-driven scan already covers is
# skipped (`if !inUniverse[out.typeName] { continue }`).
#
#   bash live/e2e/create-over/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4602, clear of run.sh's
#                4566, dataread-projection's 4599 and tagging-sweep's 4601 so
#                every harness can run at once).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, or the emulator's own answer
# through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/create-over"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4602}"
FLOCI_NAME="choudoufu-create-over-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="create-over-e2e"
ROLE_PREFIX="create-over-e2e-"
VPC_CIDR="10.77.0.0/16"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# role_names prints the estate's role names, one per line, newest last is not
# guaranteed so callers count rather than index.
role_names() {
  awsl iam list-roles \
    --query "Roles[?starts_with(RoleName, \`${ROLE_PREFIX}\`)].RoleName" \
    --output text | tr '\t' '\n' | grep -v '^$' || true
}

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"

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
  grep -q '"iam"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
grep -q '"iam"' <<< "$HEALTH" || fail "floci did not come up healthy (iam) at $ENDPOINT"
grep -q '"ec2"' <<< "$HEALTH" || fail "floci does not report an ec2 service; the control resource needs it"
log "  healthy, iam and ec2 present"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

MAIN="$WORK/estate"
mkdir -p "$MAIN"
cp "$FIXTURE/main.tf" "$MAIN/main.tf"

# ── 2. stand the estate up ──────────────────────────────────────────────────
log "=== 2. apply: one VPC, one role, both marked ==="
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT"
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the apply did not create exactly 2 resources"; }

# Read both back through the AWS CLI, never through choudoufu: the markers
# have to be really there or every assertion below fails for the wrong reason.
ROLES="$(role_names)"
[ "$(wc -l <<< "$ROLES" | tr -d ' ')" = "1" ] \
  || fail "expected exactly one $ROLE_PREFIX* role after the apply, got: $(tr '\n' ' ' <<< "$ROLES")"
ROLE1="$ROLES"
ROLE_TAGS="$(awsl iam list-role-tags --role-name "$ROLE1" --output json)"
grep -q '"tofu-estate"' <<< "$ROLE_TAGS" \
  || { printf '%s\n' "$ROLE_TAGS"; fail "$ROLE1 does not carry a tofu-estate tag; the fixture's premise is broken"; }
grep -q 'aws_iam_role.subject' <<< "$ROLE_TAGS" \
  || { printf '%s\n' "$ROLE_TAGS"; fail "$ROLE1 does not carry tofu-address=aws_iam_role.subject"; }

VPC_ID="$(awsl ec2 describe-vpcs --query "Vpcs[?CidrBlock==\`${VPC_CIDR}\`].VpcId" --output text)"
[ -n "$VPC_ID" ] || fail "no VPC with CIDR $VPC_CIDR exists after the apply"
log "  role $ROLE1 and vpc $VPC_ID live, both carrying this estate's markers"

# And the markers are in the Resource Groups Tagging index too. This is not
# scenery: it is the assertion that the run below HAS the answer available to
# it in a call it already makes, and throws it away.
SWEPT="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null || echo "")"
grep -q "role/$ROLE1" <<< "$SWEPT" \
  || fail "the emulator's tagging index does not hold $ROLE1 (got: $SWEPT); this harness cannot make its point about the discarded candidate"
grep -q "vpc/$VPC_ID" <<< "$SWEPT" \
  || fail "the emulator's tagging index does not hold $VPC_ID (got: $SWEPT)"
log "  the tagging index holds both ARNs, tagged with this estate"

# ── 3. delete the state file ────────────────────────────────────────────────
log "=== 3. delete the state file ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
[ ! -f "$MAIN/terraform.tfstate" ] || fail "the state file is still there"
log "  gone; nothing but cloud tags says what this estate owns"

# ── 4. live-plan ────────────────────────────────────────────────────────────
log "=== 4. live-plan: what does it propose? ==="
set +e
PLAN_OUT="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
set -e
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }

# 4a. THE CONTROL. aws_vpc.control is needs-discovery too and must have been
# found by its marker. If this fails, nothing else in this script means
# anything - the harness would be measuring a broken discovery pass rather
# than a tag-losing list path.
grep -qE '# aws_vpc\.control will be created' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the CONTROL failed: aws_vpc.control is proposed for creation too, so this run discovered nothing at all and the subject's result says nothing about tag-losing list paths. Fix the harness before reading anything else here."; }
log "  control: aws_vpc.control bound by its marker, not proposed for creation"

# 4b. THE DEFECT. Read the failure message before changing this.
grep -qE '# aws_iam_role\.subject will be created' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "aws_iam_role.subject is NOT proposed for creation. That is GOOD NEWS and means the create-over defect this script pins has been fixed: marker discovery can now see a resource whose type loses its tags on the provider's list path. Invert steps 4b, 4c and 5 (the plan should propose nothing, and the apply should add nothing), and say in the commit which change did it."; }
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Plan:'
       fail "the plan summary is not 'Plan: 1 to add, 0 to change, 0 to destroy'. Something other than the pinned defect moved; read the plan before assuming either direction."; }

# 4c. And the run has the live role on screen while it does it, filed under
# the wrong heading. This is the assertion that the failure is not merely
# "nothing was found" but "the object was found and called somebody else's".
grep -qE 'Foreign resources: 1 live resource not owned by estate' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the plan does not report exactly one foreign resource. The defect's signature is that the estate's own role is listed as unowned because its listed object arrived with no tags; if that changed, re-read the output."; }
grep -q "$ROLE1" <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the plan never names the live role $ROLE1, so it did not even list it - a different failure from the one this script pins"; }
log "  defect: aws_iam_role.subject proposed for creation while $ROLE1 is on screen, reported as foreign"

# ── 5. what an apply then does ──────────────────────────────────────────────
# The plan is a proposal; this is the harm. A name_prefix role has no unique
# name to collide on, so the create succeeds and the estate ends up with two
# live roles carrying one ownership marker - which is the condition
# live/MARKERS.md calls a collision, written by this tool into the cloud.
log "=== 5. apply the proposal ==="
APPLY2_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30
  fail "the second apply failed. For a type whose name IS in the configuration the create-over defect surfaces here, as an AlreadyExists error; this fixture uses name_prefix precisely so that it does not, and a failure means the fixture changed."
}
grep -qE 'Apply complete! Resources: 1 added' <<< "$APPLY2_OUT" \
  || { printf '%s\n' "$APPLY2_OUT" | tail -20; fail "the second apply did not add exactly 1 resource"; }

ROLES2="$(role_names)"
COUNT2="$(wc -l <<< "$ROLES2" | tr -d ' ')"
[ "$COUNT2" = "2" ] \
  || fail "expected 2 $ROLE_PREFIX* roles after the second apply, got $COUNT2: $(tr '\n' ' ' <<< "$ROLES2")"

# Both carry the same marker. Asserted through the tagging index rather than
# per-role tag reads, so one query answers "how many live objects claim this
# address".
DUPES="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-address,Values=aws_iam_role.subject" \
  --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null | tr '\t' '\n' | grep -c 'role/' || true)"
[ "$DUPES" = "2" ] \
  || fail "expected 2 live roles carrying tofu-address=aws_iam_role.subject, got $DUPES"
log "  two live roles now carry tofu-address=aws_iam_role.subject:"
while read -r r; do log "    $r"; done <<< "$ROLES2"

# ── 6. and it does not converge ─────────────────────────────────────────────
# Neither role's marker is readable off the list path, so the next run is in
# exactly the same position as the last one. The defect is not a one-off
# double; it is one new resource per run, forever.
log "=== 6. the next run proposes another one ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN3_OUT="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN3_RC=$?
set -e
[ "$PLAN3_RC" -eq 0 ] || { printf '%s\n' "$PLAN3_OUT" | tail -30; fail "the third live-plan exited $PLAN3_RC"; }
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$PLAN3_OUT" \
  || { printf '%s\n' "$PLAN3_OUT" | grep -E '^Plan:'
       fail "the third plan is not 'Plan: 1 to add'. If it now reports a collision or refuses, that is a partial fix and this step should be re-read rather than deleted."; }
grep -qE 'Foreign resources: 2 live resources not owned by estate' <<< "$PLAN3_OUT" \
  || { printf '%s\n' "$PLAN3_OUT" | grep -vE '^\s*$' | tail -30
       fail "the third plan does not report both roles as foreign"; }
log "  a third role would be created; both existing ones still read as foreign"

# ── 7. no state file, ever ──────────────────────────────────────────────────
[ ! -f "$MAIN/terraform.tfstate" ] \
  || fail "a state file exists after live-plan - it must never be read or written"

log ""
log "=== PASS - the defect is present, as pinned ==="
log ""
log "When this goes red because aws_iam_role.subject stops being proposed for"
log "creation, that is the fix landing. Invert 4b, 4c, 5 and 6: the plan should"
log "propose nothing, the apply should add nothing, and there should still be"
log "exactly one role. Keep the control in 4a exactly as it is."
