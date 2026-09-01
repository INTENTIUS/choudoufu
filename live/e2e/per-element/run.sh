#!/usr/bin/env bash
# (moved from the justfile's retired demo-per-element recipe; run with: just demo-run per-element)
# Component.PerElement end to end: a set-valued identity tail rendered one
# sorted segment per element, binding a live object with no state file and no
# tag - aws_iam_user_group_membership is untaggable, so the identity has no
# carrier and re-derives from the declaration. Two of the three memberships
# declare their groups OUT OF ORDER, and the run asserts the declared-order
# string never appears: a set has no order on the wire, so only the rendered
# string can tell a sorted identity from a copied one. Needs Docker and the
# AWS CLI; runs on its own port (4604) so it can run beside `just demo`.
set -euo pipefail

# Component.PerElement, end to end against a real emulator.
#
# The claim under test: a set-valued identity tail renders one segment per
# element in canonical sorted order, that string binds a real live object,
# and a run that has lost its state file converges - twice.
#
# Why this exists. The per-element row landed with unit coverage
# (internal/live/identity/perelement_test.go) and an offline probe reading
# blocked=0 for the corpus estate that motivated it
# (.corpus/simpleinfra/terraform/team-members-access). Neither of those is a
# cloud. Issue #266 is the reason that gap matters: that defect passed every
# offline check while live-plan proposed creating a resource the estate
# already owned, once per run, forever. So step 6 below is not decoration.
#
# What this fixture can prove that a unit test cannot:
#
#   1. the rendered string is the one a real provider accepts as an import
#      identity, against a real API surface;
#   2. the instance binds to a live object by that string alone, with no
#      state file and no tag - aws_iam_user_group_membership is untaggable,
#      so the identity has no carrier at all and re-derives from the
#      declaration every run;
#   3. it converges. One empty plan is not evidence; #266's defect produced
#      one new resource per run because every run was in the same position as
#      the last.
#
# And what makes it a measurement rather than an exercise: two of the three
# memberships DECLARE THEIR GROUPS OUT OF ORDER. A wrong order would still
# round-trip on the wire - the provider splits the import ID on "/" and puts
# the tail in a set, which has no order - so nothing would fail. Step 5 reads
# the rendered string out of the trace log and asserts both the sorted string
# is present AND the declared-order string is absent. That second half is
# what a boolean cannot say.
#
#   bash live/e2e/per-element/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4604, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601 and
#                create-over's 4602, so every harness can run at once).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, or the emulator's own answer
# through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/per-element"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4604}"
FLOCI_NAME="choudoufu-per-element-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="per-element-e2e"

# The three identities the row must render, and the two it must not. The
# "must not" half is the fixture's whole reason for declaring its groups out
# of order: a set has no order on the wire, so only the rendered string can
# tell a sorted identity from a declared-order one.
SOLO_WANT='pe-e2e-solo/pe-e2e-mike'
PAIR_WANT='pe-e2e-pair/pe-e2e-alpha/pe-e2e-zulu'
TRIO_WANT='pe-e2e-trio/pe-e2e-alpha/pe-e2e-mike/pe-e2e-zulu'
PAIR_DECLARED='pe-e2e-pair/pe-e2e-zulu/pe-e2e-alpha'
TRIO_DECLARED='pe-e2e-trio/pe-e2e-zulu/pe-e2e-alpha/pe-e2e-mike'

cleanup() {
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
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

MAIN="$WORK/estate"
mkdir -p "$MAIN"
cp "$FIXTURE/main.tf" "$MAIN/main.tf"

# ── 2. stand the estate up ──────────────────────────────────────────────────
# 3 groups + 3 users + 3 memberships. Asserted exactly: a fixture that
# quietly stopped creating the trio would still pass every plan assertion
# below, because there would be nothing left to disagree about.
log "=== 2. apply: 3 groups, 3 users, 3 memberships ==="
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 9 added' <<< "$APPLY_OUT" \
  || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the apply did not create exactly 9 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the memberships back through the AWS CLI, never through choudoufu. If
# a membership is not really there, every plan assertion below would pass for
# the wrong reason - an instance that binds to nothing and an instance that
# was never created both produce silence.
check_member() {
  local user="$1"; shift
  local got
  got="$(awsl iam list-groups-for-user --user-name "$user" \
          --query 'Groups[].GroupName' --output text | tr '\t' '\n' | sort | tr '\n' ' ')"
  local want=""
  for g in "$@"; do want="$want$g "; done
  [ "$got" = "$want" ] || fail "$user is in groups [$got], expected [$want]"
}
check_member pe-e2e-solo pe-e2e-mike
check_member pe-e2e-pair pe-e2e-alpha pe-e2e-zulu
check_member pe-e2e-trio pe-e2e-alpha pe-e2e-mike pe-e2e-zulu
log "  all three memberships live, with the group sets the configuration declares"

# The memberships carry no marker, and that is the point rather than a gap.
# aws_iam_user_group_membership has no tag map at all; its identity needs no
# carrier because it re-derives from the declaration. The three users DO
# carry markers, so the tagging index has something to answer with and a
# dead index would not read as a passing run.
MARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null | tr '\t' '\n' | grep -c 'user/pe-e2e-' || true)"
[ "$MARKED" = "3" ] \
  || fail "expected 3 marked users in the tagging index, got $MARKED - the index is not answering, so nothing below is measuring discovery"
log "  3 users marked in the tagging index; the memberships carry no tag, by design"

# ── 3. delete the state file ────────────────────────────────────────────────
log "=== 3. delete the state file ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
[ ! -f "$MAIN/terraform.tfstate" ] || fail "the state file is still there"
log "  gone; nothing but the configuration says which membership is which"

# ── 4. live-plan proposes nothing ───────────────────────────────────────────
log "=== 4. live-plan from the declaration alone ==="
set +e
PLAN_OUT="$(cd "$MAIN" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
set -e
[ "$PLAN_RC" -eq 0 ] || { grep -vE '^\d{4}-' <<< "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { grep -vE '^\d{4}-' <<< "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the plan is not empty. If a membership is proposed for creation, the identity it rendered did not name the live object - read step 5's strings before assuming which half is wrong."; }
# "nothing was swept" rather than "none", and the difference is the fixture's
# own shape rather than a weaker assertion. Every type here is client-named
# or untaggable - there is no needs-discovery type for the sweep to list - so
# the honest report is that no sweep happened. What must not appear is a
# count: that would mean a live object carrying this estate's markers was
# read as unowned, which is #266's own signature even behind an empty plan.
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"
       fail "the plan reports foreign resources. The estate owns every live object named here and the three users carry its markers, so anything counted on this line means one of them was read as unowned."; }
log "  nothing to create, nothing foreign"

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
# An empty plan says the memberships bound. It does not say what they bound
# BY, and a set-valued tail can be wrong in a way no live read rejects: the
# provider splits the import ID on "/" and puts the tail in a set, so
# declared order and sorted order both find the same object. The only place
# the ordering rule is observable is the rendered string itself.
log "=== 5. the rendered identities, read out of the run ==="
want_identity() {
  grep -qF "from import identity \"$1\"" <<< "$PLAN_OUT" \
    || { grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
         fail "no instance materialized from import identity \"$1\". The identities the run actually rendered are listed above."; }
  log "    $1"
}
want_identity "$SOLO_WANT"
want_identity "$PAIR_WANT"
want_identity "$TRIO_WANT"

# And the control. These are the strings declaration order would produce.
# They must not appear - if one does, the rendering is following the
# configuration's order rather than sorting, and every assertion above would
# still have passed.
for bad in "$PAIR_DECLARED" "$TRIO_DECLARED"; do
  grep -qF "$bad" <<< "$PLAN_OUT" \
    && fail "the run rendered \"$bad\", which is the order the configuration DECLARES rather than canonical sorted order. Nothing on the wire would have rejected it: the provider puts the tail in a set. See Component.PerElement in internal/live/identity/table.go and perElementParts in internal/live/identity/perelement.go."
done
log "  declared-order strings absent; the tail is sorted, not copied"

# ── 6. and it converges ─────────────────────────────────────────────────────
# The #266 assertion. One empty plan is not evidence: that defect produced
# one new resource per run because every run started where the last one did.
log "=== 6. the next run proposes nothing either ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN2_OUT="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN2_RC=$?
set -e
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN2_OUT" \
  || { printf '%s\n' "$PLAN2_OUT" | grep -vE '^\s*$' | tail -30
       fail "the second plan is not empty, so the run does not converge"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN2_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN2_OUT"; fail "the second plan reports foreign resources"; }

# An empty plan is a proposal. This is what applying it does: still 3
# memberships, not 6.
APPLY2_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: [1-9][0-9]* added' <<< "$APPLY2_OUT" \
  && { printf '%s\n' "$APPLY2_OUT" | tail -20
       fail "the second apply added a resource over one the estate already owns - issue #266's shape, on the per-element row"; }
check_member pe-e2e-solo pe-e2e-mike
check_member pe-e2e-pair pe-e2e-alpha pe-e2e-zulu
check_member pe-e2e-trio pe-e2e-alpha pe-e2e-mike pe-e2e-zulu
log "  converged: nothing proposed, nothing added, the same three memberships"

# ── 7. no state file, ever ──────────────────────────────────────────────────
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
( cd "$MAIN" && "$TOFU" live-plan -input=false -no-color >/dev/null 2>&1 )
set -e
[ ! -f "$MAIN/terraform.tfstate" ] \
  || fail "a state file exists after live-plan - it must never be read or written"

log ""
log "=== PASS ==="
log ""
log "The per-element identity renders one sorted segment per element, binds a"
log "live object by that string with no state file and no tag, and converges."
log "If step 5 goes red the VALUE moved while every verdict stayed green -"
log "read internal/live/identity/perelement.go, not the plan output."
