#!/usr/bin/env bash
set -uo pipefail

# Issue #564's own proof (composition since extended by #574's
# count/for_each/module-nested expansion): tools/terralith-gen generates a
# stock-Terraform terralith (no live block, no record_store,
# tofu-estate/tofu-address marker anywhere - see tools/terralith-gen/
# shape_test.go's mechanical check for that half), and a plain
# `terraform apply` stands it up against floci at the small tier
# (-scale 1), then a plain `terraform destroy` tears it down - exercised
# deliberately before #565/#566/#567 attempt anything larger, per #546's
# own rule ("Teardown is exercised at each tier before growing to the
# next"). #574 added a module call (modules/team_pod, wrapped with
# for_each) to the estate this proves apply/destroy against, so this run
# is also #574's own apply+destroy proof for the module-nested bucket.
#
# This is the oracle-free half on purpose: unlike live/e2e/destroy-teardown,
# there is no choudoufu binary anywhere in this script. The subject here is
# whether the GENERATOR's output is valid, appliable, destroyable stock
# Terraform - the thing #546's later children (the migration measurement)
# will point choudoufu at. choudoufu is deliberately absent from this
# script; #566 is where it first appears against this same generator.
#
#   bash live/e2e/terralith-scale/run.sh
#
# Env overrides:
#   SCALE        the -scale value passed to terralith-gen (default 1, the
#                "genuinely small tier" #564 asks to prove first)
#   FLOCI_PORT   host port for the emulator (default 4745, clear of every
#                other shape fixture's own default)
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output or the emulator's own answer through the AWS
# CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
gauntlet_begin

WORK="$(mktemp -d)"
SCALE="${SCALE:-1}"
FLOCI_PORT="${FLOCI_PORT:-4745}"
FLOCI_NAME="choudoufu-terralith-scale-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
PREFIX="tls$$"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "${CURRENT_STAGE:-}" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# inventory prints one line per live object this run's own prefix could
# have created, across every resource kind the estate uses - never a bare
# count (this repo has shipped that mistake before; see CLAUDE.md).
inventory() {
  {
    awsl iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text
    awsl iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].PolicyName" --output text
    awsl iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text
    awsl route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text
    awsl ecs list-clusters --query 'clusterArns' --output text | tr '\t' '\n' | grep -F "${PREFIX}-cluster" || true
    awsl ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[].VpcId' --output text
  } | tr '\t' '\n' | grep -v '^$' | sort
}

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "a real terraform binary is not on PATH - this proof is about the generator's output being valid STOCK Terraform, so terraform itself is the oracle, not a checked-out tool"

# ── 1. generate ───────────────────────────────────────────────────────────
log "=== 1. terralith-gen -scale $SCALE -prefix $PREFIX ==="
( cd "$ROOT" && env -u PWD go run ./tools/terralith-gen -scale "$SCALE" -prefix "$PREFIX" -out "$WORK/terralith" ) \
  || fail "terralith-gen failed"
EXPECTED=$((74 * SCALE + 5))
log "  expect ${EXPECTED} resources at scale=${SCALE} (62*scale identity [36 named + 2 service-exec + 12 count-expanded + 12 module-nested, issue #574], 1+2*scale container, 1+10*scale dns [one for_each block, #574], 3 supporting)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
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
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# ── 3. terraform apply (plain stock Terraform, no state trickery) ─────────
#
# cold_deploy opens here and is reported at the end of section 6, not after
# the apply assertion below. Its verdict is the apply's - "the stock binary
# applies the unmodified configuration against the emulator, with no live
# block and no choudoufu involved" - and reporting it late only makes it
# stricter, never looser. The reason to report it late is the destroy: a
# stock teardown that fails would otherwise exit this script non-zero with
# no stage reading `fail` anywhere in the row, which is precisely what
# tools/gauntlet's TestNonzeroExitCodeImpliesAFailingStage refuses. There is
# no active stage of its own for a STOCK destroy (day2_teardown is planned,
# and is about `choudoufu apply -destroy`), so it stays inside this one.
gauntlet_begin_stage cold_deploy
log "=== 3. terraform apply ==="
( cd "$WORK/terralith" && terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "terraform init failed"
APPLY1="$(cd "$WORK/terralith" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY1" | grep -E '^Error|^│' | head -30
  fail "terraform apply failed"; }
grep -qE "Apply complete! Resources: ${EXPECTED} added, 0 changed, 0 destroyed" <<< "$APPLY1" \
  || { grep -E 'Apply complete' <<< "$APPLY1"; fail "apply did not create exactly ${EXPECTED} resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY1")"

BEFORE="$(inventory)"
BEFORE_N="$(grep -c . <<< "$BEFORE" || true)"
[ "$BEFORE_N" -gt 0 ] || fail "inventory reports 0 objects right after a successful apply - the check is not measuring anything"
log "  inventory after apply: $BEFORE_N objects across IAM/Route53/ECS/EC2"

# ── 4. control: a leftover object must make the inventory non-empty ───────
# Run before destroy, not after, so this proof does not depend on destroy
# already having worked - the same "prove the check has teeth" discipline
# live/e2e/destroy-teardown/run.sh's own BREAK control uses.
log "=== 4. control: confirm the inventory check is not vacuous ==="
LEFTOVER="$(awsl iam create-role --role-name "${PREFIX}-control-role" \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  --query 'Role.RoleName' --output text)" || fail "control: could not create the deliberately-added role"
CONTROL="$(inventory)"
grep -qF "$LEFTOVER" <<< "$CONTROL" || fail "control failed: $LEFTOVER was created but the inventory check did not see it - it is not measuring anything"
awsl iam delete-role --role-name "$LEFTOVER" >/dev/null 2>&1 || true
log "  BREAK proved: a deliberately-added role was correctly seen by the inventory check"

# ── 5. terraform destroy ───────────────────────────────────────────────────
log "=== 5. terraform destroy ==="
DESTROY1="$(cd "$WORK/terralith" && terraform destroy -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$DESTROY1" | grep -E '^Error|^│' | head -30
  fail "terraform destroy failed"; }
grep -qE "Destroy complete! Resources: ${EXPECTED} destroyed" <<< "$DESTROY1" \
  || { grep -E 'Destroy complete' <<< "$DESTROY1"; fail "destroy did not remove exactly ${EXPECTED} resources"; }
log "  $(grep -E 'Destroy complete' <<< "$DESTROY1")"

# ── 6. the empty-account assertion: enumerated, not counted ───────────────
log "=== 6. the empty-account assertion ==="
AFTER="$(inventory)"
[ -z "$AFTER" ] || { printf '%s\n' "$AFTER"; fail "the account is not empty after terraform destroy: $AFTER"; }
log "  terraform destroy leaves this prefix's account empty, enumerated across every resource kind the estate used"

gauntlet_stage cold_deploy pass "stock terraform applied ${EXPECTED} resources at scale=${SCALE} from unmodified terralith-gen output (no live block, no choudoufu binary in this script at all), ${BEFORE_N} objects enumerated live across IAM/Route53/ECS/EC2 with a BREAK control proving the enumeration has teeth, then stock destroy removed all ${EXPECTED} and left the prefix's account enumerated empty"

# ── Every other active stage: not_run, and why ────────────────────────────
#
# Reported honestly rather than left to the artifact's backfill, so the row
# says WHY it is empty rather than merely that it is.
#
# This script is deliberately choudoufu-free (see the header): its subject
# is whether the GENERATOR's output is valid, appliable, destroyable stock
# Terraform. Every stage below needs the choudoufu binary, so none of them
# is a thing this script declines to check - they are things it is not the
# script for, yet. Writing them is the crossing work `gauntlet next` will
# now surface as real units for this estate.
#
# Note what is NOT claimed here: migrate, test_plan and test_apply are
# recorded `pass` for this estate in live/gauntlet.json's separate live_cert
# array, against a real AWS account. That is different evidence about a
# different target and it is deliberately not copied into this row - an
# emulator row and a live-AWS certification are kept apart on purpose
# (tools/gauntlet/livecert.go's own doc comment).
for _stage in migrate test_plan test_apply drift_reconverge \
              day2_rename day2_remove day2_count day2_replace \
              greenfield strict; do
  gauntlet_stage "$_stage" not_run "this crossing script is deliberately choudoufu-free (issue #564): it proves the generator emits valid stock Terraform that applies and destroys, and every stage past cold_deploy needs the choudoufu binary this script does not invoke"
done

gauntlet_end

log ""
log "=== PASS ==="
log ""
log "tools/terralith-gen -scale $SCALE generated $EXPECTED resources of valid"
log "stock Terraform (no choudoufu construct - see shape_test.go's static"
log "check), a plain terraform apply stood them up against floci, and a"
log "plain terraform destroy removed every one of them - enumerated, not"
log "counted, with a control proving the enumeration has teeth."
