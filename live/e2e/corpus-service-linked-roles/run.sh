#!/usr/bin/env bash
set -uo pipefail

# A real government estate crossed against a real emulator: issue #274's
# step 6, for
# .corpus/govuk-infrastructure/terraform/deployments/service-linked-roles.
#
# One instance, hand-written by GDS and not by anyone here:
#
#   aws_iam_service_linked_role.es_role   server-assigned ARN, "es_role"
#
# This is the smallest of the twenty-eight estates #274 counts: a single
# resource block, no data sources, no modules, one provider. It is still a
# real crossing and not a fixture, because IAM - not this configuration -
# decides the role's name (AWSServiceRoleForEs on this emulator; something
# else on real AWS's own per-service convention), so the only way a second
# run finds the one it already owns is to enumerate the account and read a
# marker off what comes back. table_generated.go's own entry for this type
# says as much: "the provider's own docs say the role name is not an
# argument you provide."
#
# What this estate contributes that nothing already crossed does:
#
#   1. lifecycle { prevent_destroy = true } on the one resource. Nothing
#      here destroys it, but a real onboarding candidate would carry this
#      and the crossing has to tolerate it rather than working around it.
#
#   2. variables-common.tf IS A REAL SYMLINK in the corpus checkout
#      (../../variables/variables-common.tf, shared across every
#      govuk-infrastructure deployment - the same file
#      corpus-mobile-backend/run.sh's DELTA 4 already documents), and it
#      declares seven variables this estate never references. OpenTofu
#      still requires a value for every undefaulted variable at plan time
#      regardless of whether anything reads it, so DELTA 3 below supplies
#      all of them.
#
#   bash live/e2e/corpus-service-linked-roles/run.sh
#
# Needs Docker, the AWS CLI, and a populated .corpus (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4707, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected identity string before
#                step 5, proving the identity assertion is load-bearing
#                rather than a grep that always matches.
#
# .corpus is shared across every worktree and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-infrastructure/terraform/deployments/service-linked-roles"
WORK="$(mktemp -d)"
EST="$WORK/service-linked-roles"
FLOCI_PORT="${FLOCI_PORT:-4707}"
FLOCI_NAME="choudoufu-corpus-service-linked-roles-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="govuk-service-linked-roles"
REGION="eu-west-1"
ACCOUNT="000000000000"
INSTANCES=1
SERVICE_NAME="es.amazonaws.com"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
[ -d "$SRC" ] || fail "$SRC is missing - run 'just corpus-fetch' first"

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

# variables-common.tf is a real symlink pointing outside $SRC; `cp *.tf`
# dereferences it and copies its content, which is what makes the copy
# self-contained.
mkdir -p "$EST"
cp "$SRC"/*.tf "$EST/"
[ -f "$EST/main.tf" ] && [ -f "$EST/variables-common.tf" ] \
  || fail "the estate copy is missing main.tf or variables-common.tf"
[ ! -L "$EST/variables-common.tf" ] \
  || fail "variables-common.tf copied as a symlink rather than its dereferenced content"
RES_N="$(grep -hc '^resource "' "$EST"/*.tf | awk '{s+=$1} END {print s}')"
[ "$RES_N" = "$INSTANCES" ] \
  || fail "the estate declares $RES_N resource blocks, expected $INSTANCES - the corpus pin has moved"
grep -qF 'aws_service_name = "'"$SERVICE_NAME"'"' "$EST/main.tf" \
  || fail "the resource no longer targets $SERVICE_NAME - the corpus pin has moved"
log "  estate copied out of .corpus into $EST ($RES_N resource block)"

# ── 1. the deltas ───────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="

# DELTA 1, ordinary onboarding. The estate declares `cloud { organization =
# "govuk" workspaces { tags = [...] } }`; a module may declare remote state
# or a live block, never both, so the cloud block goes and the live block
# replaces it. This is #268 in its Terraform-Cloud form.
perl -0pi -e 's/^  cloud \{\n    organization = "govuk"\n    workspaces \{\n      tags = \[[^\]]*\]\n    \}\n  \}\n/  live {\n    estate = "'"$ESTATE"'" # DELTA 1\n  }\n/m' "$EST/main.tf"
grep -q 'DELTA 1' "$EST/main.tf" || { sed -n '1,20p' "$EST/main.tf"; fail "DELTA 1 did not match the cloud block - the corpus pin has moved"; }
grep -q 'cloud {' "$EST/main.tf" && fail "DELTA 1 left a cloud block behind"
log "  DELTA 1  cloud block removed, live block added    (onboarding, #268)"

# DELTA 2, emulator wiring: the flags with no environment-variable form.
perl -0pi -e 's/^(provider "aws" \{\n  region = var\.aws_region\n)/$1\n  access_key                  = "test" # DELTA 2\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_requesting_account_id  = true\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/m' "$EST/main.tf"
grep -q 'DELTA 2' "$EST/main.tf" || { sed -n '/provider "aws"/,/^}/p' "$EST/main.tf"; fail "DELTA 2 did not match the provider block"; }
log "  DELTA 2  emulator flags on the provider          (emulator)"

# No provider pin: the estate's own `version = "~> 6.28"` resolves to a
# release with list resources intact, the same absence-is-a-finding result
# corpus-govuk-oidc/run.sh already established for the identical constraint.
grep -q 'version = "~> 6.28"' "$EST/main.tf" \
  || { grep -n 'version' "$EST/main.tf"; fail "the aws constraint is no longer ~> 6.28, so the 'no provider pin needed' claim is unchecked"; }

# DELTA 3, values for variables-common.tf's seven undefaulted variables.
# None of them is read by anything in this estate - only govuk_environment
# is, and only via the provider's default_tags - but OpenTofu requires a
# value for every undefaulted variable regardless.
cat > "$EST/crossing.auto.tfvars" <<'EOF'
govuk_environment          = "test"
publishing_service_domain  = "www.test.gov.uk"
vpc_cidr                   = "10.40.0.0/16"

eks_control_plane_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.0.0/24" }
}
eks_public_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.1.0/24" }
}
eks_private_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.2.0/24" }
}
legacy_private_subnets = {
  a = { az = "eu-west-1a", cidr = "10.40.3.0/24", nat = false }
}
EOF
log "  DELTA 3  tfvars for variables-common.tf's seven unused-but-required vars"

# The provider's default_tags block is left exactly as GDS wrote it. Step 4
# checks the markers survived the merge with it.
grep -q 'default_tags' "$EST/main.tf" \
  || fail "the estate no longer declares default_tags, so the marker-merge check in step 4 measures nothing"

# The lifecycle block stays untouched: this crossing does not destroy
# anything, so prevent_destroy is never exercised, but the script must not
# quietly strip it either.
grep -q 'prevent_destroy = true' "$EST/main.tf" \
  || fail "the estate no longer declares prevent_destroy, so this crossing is no longer testing what its header claims"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (iam) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: $INSTANCES instance ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly $INSTANCES instance"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# ── 4. what is live, and what carries a marker ──────────────────────────────
log "=== 4. the live role, its marker, and GDS's own tag ==="
ROLE_NAME="$(awsl iam list-roles --path-prefix "/aws-service-role/${SERVICE_NAME}/" \
  --query 'Roles[0].RoleName' --output text)"
[ -n "$ROLE_NAME" ] && [ "$ROLE_NAME" != "None" ] \
  || fail "IAM holds no service-linked role under /aws-service-role/${SERVICE_NAME}/ after an apply that reported creating one"
ROLE_N="$(awsl iam list-roles --path-prefix "/aws-service-role/${SERVICE_NAME}/" \
  --query 'length(Roles)' --output text)"
[ "$ROLE_N" = "1" ] || fail "IAM holds $ROLE_N service-linked roles under that path, expected 1"
ROLE_ARN="arn:aws:iam::${ACCOUNT}:role/aws-service-role/${SERVICE_NAME}/${ROLE_NAME}"
awsl iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1 || fail "IAM holds no role named $ROLE_NAME"
log "  role $ROLE_NAME"
log "  arn  $ROLE_ARN"

tag_of() { # tag_of <key> <list-cmd...> -- reads a tag's value off one object
  local key="$1"; shift
  "$@" --query "Tags[?Key=='$key'].Value | [0]" --output text 2>/dev/null || echo None
}
ROLE_ADDR="$(tag_of tofu-address awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$ROLE_ADDR" = "aws_iam_service_linked_role.es_role" ] \
  || fail "the role carries tofu-address=$ROLE_ADDR"
ROLE_EST="$(tag_of tofu-estate awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$ROLE_EST" = "$ESTATE" ] || fail "the role carries tofu-estate=$ROLE_EST"
log "  carries tofu-address=$ROLE_ADDR and tofu-estate=$ROLE_EST"

# The markers did not displace GDS's own default_tags. A stamping pass that
# REPLACED the tags argument rather than merging into it would leave every
# assertion above green and quietly strip GDS's tags off the one live
# object.
GDS_TAG="$(tag_of project awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$GDS_TAG" = "GOV.UK" ] \
  || { awsl iam list-role-tags --role-name "$ROLE_NAME" --output text; fail "the role's own project default_tag is $GDS_TAG - the markers displaced it"; }
log "  and the provider's default_tags survived: project=$GDS_TAG"

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. no state file, and the rendered identity read out of the run ==="
plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error|^│' | head -30; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"
       grep -E '^ +[+~-] [a-z_]+ +=' <<< "$PLAN_OUT" | head -20
       fail "the plan proposes a resource change. If it proposes CREATING a service-linked role, that is the defect this script exists for: the live one was not enumerated."; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT="$ROLE_ARN"
if [ "${BREAK:-}" = "1" ]; then
  # Not a string nothing could produce: the same account, the same path,
  # and a role name one character off - what a derivation that mangled
  # IAM's own naming convention would render.
  WANT="arn:aws:iam::${ACCOUNT}:role/aws-service-role/${SERVICE_NAME}/${ROLE_NAME}X"
  log "  BREAK=1: expecting $WANT, the SAME account and the SAME shape of ARN"
  log "           as the real one, just with the role name corrupted. The"
  log "           plan above stayed empty. This step must fail."
fi
grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
  grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
  fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
}
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | grep -c .)"
[ "$GOT_N" = "$INSTANCES" ] || fail "the run materialized $GOT_N distinct identities, expected $INSTANCES"
log "  the rendered identity asserted as a string, and no second one:"
log "    $WANT"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing either ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }
grep -qF "from import identity \"$WANT\"" <<< "$PLAN2_OUT" || fail "the second run did not materialize \"$WANT\""
log "  the second cold plan is empty too, with the same identity"

# ── 7. and applying it adds nothing ─────────────────────────────────────────
log "=== 7. applying it adds nothing, and IAM still holds ONE role ==="
APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply was not a no-op"; }
AFTER_N="$(awsl iam list-roles --path-prefix "/aws-service-role/${SERVICE_NAME}/" --query 'length(Roles)' --output text)"
[ "$AFTER_N" = "1" ] \
  || fail "IAM now holds $AFTER_N service-linked roles under that path, not 1 - the run created a second one over the one it already owned"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  $(grep -E 'Apply complete' <<< "$APPLY2_OUT" | head -1)"
log "  still 1 service-linked role, and it is the same one"

log ""
log "=== PASS ==="
log ""
log "The smallest of #274's twenty-eight estates - one resource, a"
log "government department's own root module - applied against an"
log "emulator, stripped of its state file, and replanned empty twice. The"
log "one rendered identity was checked as a string against IAM's own"
log "answer, GDS's own default_tags survived the marker write, and IAM"
log "still holds one service-linked role rather than two."
log ""
log "It cost two deltas plus a tfvars file: the cloud block for a live"
log "block, the emulator flags, and values for seven variables this"
log "estate never reads. No provider pin, no record_store."
log ""
log "Run again with BREAK=1: everything above step 5 still passes and step"
log "5 goes red."
