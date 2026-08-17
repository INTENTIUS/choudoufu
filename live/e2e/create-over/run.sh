#!/usr/bin/env bash
set -euo pipefail

# Create-over-existing, end to end against a real emulator.
#
# THIS SCRIPT PINNED A DEFECT (issue #266) and now pins its fix. Read it in
# that order, because every assertion below is the inverse of one that used to
# be here, and the failure messages are written for whoever finds it red.
#
# The defect, in one sentence: a needs-discovery resource whose type loses its
# tags on the provider's list path was invisible to marker discovery, so a
# live-plan proposed CREATING a resource the estate already owned - and an
# apply then created a second one, carrying the same ownership marker as the
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
# discover anything". The control binding and the subject not isolated the
# tag-losing list path as the cause, and it still isolates it now that the
# subject binds too: step 7 turns the fix off and the control keeps binding.
#
# What fixed it: the data was on the wire the whole time. The estate-wide
# tagging sweep makes one GetResources call whose answer carries the role's
# ARN and its tofu-estate/tofu-address tags; step 2 asserts that directly
# through the AWS CLI. That call is now made before the config-driven scan
# rather than after it (internal/live/discovery/bindtags.go, markerIndex), and
# its tags are joined onto listed objects by identifier - so a listed role
# that arrived with an empty tag map gets the marker it actually carries. No
# ARN join table, no second API call.
#
# The join refuses rather than guesses: the tagged resource's own
# tofu-address has to name the type being listed, it has to carry this
# estate, and exactly one candidate may match. Step 7 pins what happens when
# it cannot fire at all.
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
# scenery: it is the premise of the whole fix. The run below reads that index
# through a call it already makes, and joins its tags onto the listed objects
# whose own list call dropped them. Step 7 takes the index away and asserts
# what is left.
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

# 4b. THE SUBJECT. The role's list call still returns no tags - nothing about
# the provider changed - but the estate's tag index does carry them, and the
# join in internal/live/discovery/bindtags.go attaches them to the listed
# object by identifier. So the instance binds and there is nothing to create.
grep -qE '# aws_iam_role\.subject will be created' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "aws_iam_role.subject is proposed for creation, which is issue #266 back. Applying that plan creates a second live role carrying the first one's ownership marker, once per run, without an error. The join lives in internal/live/discovery/bindtags.go; step 7 below runs the same fixture with the tag index switched off and pins what the honest fallback looks like, so compare the two outputs before deciding which half broke."; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the plan is not empty. aws_iam_role.subject is not being created (4b passed), so something else moved; read the plan before assuming either direction."; }

# 4c. And the estate's own role is no longer filed as somebody else's. Under
# the defect this said "Foreign resources: 1 live resource not owned by
# estate", naming the role the run was looking for.
grep -qE '^Foreign resources: none' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Foreign resources:'
       fail "the plan does not report 'Foreign resources: none'. The estate owns exactly two live resources and both carry its markers, so anything else here means one of them was read as unowned - the defect's own signature, even if the plan happens to be empty. Under the defect this line read 'Foreign resources: 1 live resource not owned by estate'."; }
log "  subject: aws_iam_role.subject bound to $ROLE1 from the tag index; nothing proposed, nothing foreign"

# ── 5. an apply changes nothing ─────────────────────────────────────────────
# The plan is a proposal; this is what it does. Under the defect this apply
# added a role and left two live objects carrying one ownership marker - the
# condition live/MARKERS.md calls a collision, written by this tool into the
# cloud.
log "=== 5. apply the (empty) proposal ==="
APPLY2_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30
  fail "the second apply failed"
}
grep -qE 'Resources: 1 added' <<< "$APPLY2_OUT" \
  && { printf '%s\n' "$APPLY2_OUT" | tail -20
       fail "the second apply added a resource. That is the create-over defect doing its damage even though the plan looked clean; read both outputs."; }

ROLES2="$(role_names)"
COUNT2="$(wc -l <<< "$ROLES2" | tr -d ' ')"
[ "$COUNT2" = "1" ] \
  || fail "expected exactly 1 $ROLE_PREFIX* role after the second apply, got $COUNT2: $(tr '\n' ' ' <<< "$ROLES2")"

# One live object claims the address. Asserted through the tagging index
# rather than per-role tag reads, so one query answers "how many live objects
# claim this address" - the same query that returned 2 under the defect.
CLAIMS="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-address,Values=aws_iam_role.subject" \
  --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null | tr '\t' '\n' | grep -c 'role/' || true)"
[ "$CLAIMS" = "1" ] \
  || fail "expected exactly 1 live role carrying tofu-address=aws_iam_role.subject, got $CLAIMS"
log "  still one role, $ROLE1, and one live object claims aws_iam_role.subject"

# ── 6. and it converges ─────────────────────────────────────────────────────
# The defect was not a one-off double: it was one new resource per run,
# forever, because every run was in the same position as the last. So the
# assertion that matters is the SECOND replan, from a deleted state file
# again.
log "=== 6. the next run proposes nothing either ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN3_OUT="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN3_RC=$?
set -e
[ "$PLAN3_RC" -eq 0 ] || { printf '%s\n' "$PLAN3_OUT" | tail -30; fail "the third live-plan exited $PLAN3_RC"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN3_OUT" \
  || { printf '%s\n' "$PLAN3_OUT" | grep -vE '^\s*$' | tail -30
       fail "the third plan is not empty, so the run does not converge"; }
grep -qE '^Foreign resources: none' <<< "$PLAN3_OUT" \
  || { printf '%s\n' "$PLAN3_OUT" | grep -E '^Foreign resources:'
       fail "the third plan does not report 'Foreign resources: none'"; }
log "  converged: nothing to create, nothing foreign"

# ── 7. fail closed, and say so ──────────────────────────────────────────────
# The join reads the Resource Groups Tagging index. TOFU_LIVE_CLOUDCONTROL=off
# takes that index away, which is also what a real account's indexing delay
# looks like for a resource tagged seconds ago. The join must then find
# nothing rather than guess - a wrong bind adopts somebody else's object and
# is worse than the defect - so the run returns to proposing a create. What it
# must NOT do is return to proposing it silently.
log "=== 7. with the tag index off: degrade, and say so ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN4_OUT="$(cd "$MAIN" && TOFU_LIVE_CLOUDCONTROL=off "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN4_RC=$?
set -e
[ "$PLAN4_RC" -eq 0 ] || { printf '%s\n' "$PLAN4_OUT" | tail -30; fail "the tag-index-off live-plan exited $PLAN4_RC"; }
grep -qE '# aws_iam_role\.subject will be created' <<< "$PLAN4_OUT" \
  || { printf '%s\n' "$PLAN4_OUT" | grep -vE '^\s*$' | tail -40
       fail "with TOFU_LIVE_CLOUDCONTROL=off the plan does NOT propose creating aws_iam_role.subject. Either the join found the tags somewhere other than the tagging index - which would mean this step is no longer measuring the fallback - or the fixture changed. Do not delete this step to make it quiet."; }
grep -q 'Unbound instance with unreadable live markers of its type' <<< "$PLAN4_OUT" \
  || { printf '%s\n' "$PLAN4_OUT" | grep -vE '^\s*$' | tail -40
       fail "the tag-index-off run proposes creating a resource that exists and says nothing about why it might be wrong. Degrading to the pre-fix behaviour is acceptable; degrading to it silently is the defect. The finding is discovery.ProblemUnreadableMarker."; }
# The control still binds with the index off - its tags ride along on the
# list call - so the finding is about the subject and nothing else.
grep -qE '# aws_vpc\.control will be created' <<< "$PLAN4_OUT" \
  && { printf '%s\n' "$PLAN4_OUT" | grep -vE '^\s*$' | tail -40
       fail "with the tag index off the CONTROL stopped binding too, so this step is measuring a dead discovery pass rather than the join's fallback"; }
log "  degraded to a create, with the finding on screen, control still bound"

# ── 8. no state file, ever ──────────────────────────────────────────────────
[ ! -f "$MAIN/terraform.tfstate" ] \
  || fail "a state file exists after live-plan - it must never be read or written"

log ""
log "=== PASS - the create-over defect is fixed, and the fallback is honest ==="
log ""
log "This script pinned the DEFECT until issue #266 was fixed; every assertion"
log "above is now the fix. If 4b or 5 goes red, choudoufu is creating a second"
log "live resource over one it already owns - read internal/live/discovery/"
log "bindtags.go, and read step 7's output beside step 4's, because the two"
log "differ only in whether the tag index was available."
