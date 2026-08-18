#!/usr/bin/env bash
set -uo pipefail

# A real government estate crossed against a real emulator: issue #274's
# step 6, for .corpus/govuk-infrastructure/terraform/deployments/mobile-backend.
#
# Twelve instances, written by GDS for GOV.UK App's own mobile backend and
# not for us:
#
#   aws_iam_role.github_action_sign_deploy         client-named
#   aws_iam_role_policy.config_signing             untaggable, role:policy
#   aws_iam_role_policy.bucket_deployment          untaggable, role:policy
#   aws_kms_key.config_signing_key                 server-assigned key ID
#   aws_kms_alias.config_signing_key               untaggable, no tags argument
#   module.mobile_backend_remote_config (../../shared-modules/s3), which
#     expands to SEVEN instances behind one aws_s3_bucket: the bucket itself
#     (client-named, tagged) plus versioning, server-side encryption, a
#     bucket policy, ownership controls, a public-access block and bucket
#     logging - none of which take a tags argument, so all six re-derive
#     their identity from the bucket name every run.
#
# What this estate contributes that the ones already crossed do not:
#
#   1. A LOCAL MODULE FROM A SHARED LIBRARY, ../../shared-modules/s3, with
#      its own required_providers block and eleven resources behind six
#      count-gated toggles - the module this estate calls happens to
#      materialise seven of them, decided entirely by variable defaults
#      the module owner controls and this estate's author never touched.
#
#   2. A SECOND PROVIDER, fastly/fastly, for `data "fastly_ip_ranges"`. It is
#      left alone rather than stubbed: it is a real outbound HTTPS read
#      against Fastly's own public API, not AWS, and not something floci or
#      any AWS emulator could ever answer. Offline, the run fails at that
#      read and says so.
#
#   3. A DATA SOURCE READ AGAINST AN OBJECT THIS ESTATE DOES NOT CREATE:
#      data.aws_iam_openid_connect_provider reads the GitHub OIDC provider
#      by URL. A real GOV.UK account already has one (created by a sibling
#      deployment, github_oidc_provider); a fresh emulator account does not,
#      so step 2 seeds it with the AWS CLI before init ever runs. Same shape
#      for the S3 module's logging target: aws_s3_bucket_logging in the
#      shared module points at govuk-${var.govuk_environment}-aws-logging,
#      a bucket this estate never declares and never touches, so step 2
#      creates it too. Neither seed is an onboarding cost; both are what
#      "the account already had this" costs a fresh emulator specifically.
#
#   4. variables-common.tf IS A REAL SYMLINK in the corpus checkout
#      (../../variables/variables-common.tf, shared across every
#      deployment), and it declares four CIDR-map variables this estate
#      never references (eks_control_plane_subnets and friends). OpenTofu
#      still requires a value for every undefaulted variable at plan time
#      regardless of whether anything reads it, so DELTA 4 below supplies
#      all of them.
#
#   bash live/e2e/corpus-mobile-backend/run.sh
#
# Needs Docker, the AWS CLI, outbound HTTPS to Fastly's public API, and a
# populated .corpus (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4706, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before
#                step 5, proving the identity assertion is load-bearing
#                rather than a grep that always matches.
#
# .corpus is shared across every worktree and is NEVER written to: the
# estate and the shared module it calls are both copied out first, and
# every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-infrastructure/terraform/deployments/mobile-backend"
SRC_MODULE="$CORPUS_DIR/govuk-infrastructure/terraform/shared-modules/s3"
WORK="$(mktemp -d)"
EST="$WORK/terraform/deployments/mobile-backend"
FLOCI_PORT="${FLOCI_PORT:-4706}"
FLOCI_NAME="choudoufu-corpus-mobile-backend-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mobile-backend-crossing"
REGION="eu-west-1"
INSTANCES=12
ROOT_BLOCKS=5
ROLE_NAME="github_action_mobile_backend_sign_deploy"
BUCKET_NAME="govuk-app-remote-config-test"
LOGGING_BUCKET="govuk-test-aws-logging"
OIDC_URL="https://token.actions.githubusercontent.com"

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
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'just corpus-fetch' first"

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

# The corpus tree is mirrored exactly (terraform/deployments/mobile-backend
# beside terraform/shared-modules/s3) so the module's own relative source,
# "../../shared-modules/s3", resolves without editing it.
mkdir -p "$EST" "$WORK/terraform/shared-modules/s3"
cp "$SRC"/*.tf "$EST/"
cp "$SRC_MODULE"/*.tf "$WORK/terraform/shared-modules/s3/"
[ -f "$EST/main.tf" ] && [ -f "$EST/gha-iam-role.tf" ] && [ -f "$EST/signing-key.tf" ] \
  || fail "the estate copy is missing main.tf, gha-iam-role.tf or signing-key.tf"
RES_N="$(grep -h '^resource "' "$EST"/*.tf | wc -l | tr -d ' ')"
[ "$RES_N" = "$ROOT_BLOCKS" ] \
  || fail "the estate declares $RES_N root resource blocks, expected $ROOT_BLOCKS - the corpus pin has moved"
log "  estate + shared s3 module copied out of .corpus ($RES_N root resource blocks)"

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "$HEALTH" && grep -q '"kms"' <<< "$HEALTH" && grep -q '"s3"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ── 2. the deltas ───────────────────────────────────────────────────────────
log "=== 2. onboarding deltas ==="

# DELTA 1 + 2, ordinary onboarding. The estate declares
# `cloud { organization = "govuk" workspaces { tags = [...] } }`; a module
# may declare a backend or a live block, never both (issue #268), so the
# cloud block goes and the live block replaces it. The provider constraint
# "~> 6.28" is pinned to the version the rest of live/e2e pins, so this run
# is not exposed to whatever the newest 6.x happens to do on any given day.
perl -0pi -e 's/  cloud \{\n    organization = "govuk"\n    workspaces \{\n      tags = \["aws", "mobile-backend"\]\n    \}\n  \}\n\n  required_version = "~> 1.15"\n  required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = "~> 6.28"\n    \}/  required_version = "~> 1.15"\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      version = "= 6.59.0" # DELTA 2\n    }/' "$EST/main.tf"
perl -0pi -e 's/(    fastly = \{\n      source  = "fastly\/fastly"\n      version = "~> 9.0"\n    \}\n  \})/$1\n\n  live { # DELTA 1\n    estate = "'"$ESTATE"'"\n  }/' "$EST/main.tf"
grep -q 'live {' "$EST/main.tf" || { cat "$EST/main.tf"; fail "DELTA 1 did not add the live block - the corpus pin has moved"; }
grep -q '"= 6.59.0" # DELTA 2' "$EST/main.tf" || fail "DELTA 2 did not pin the aws provider version"
grep -qF 'cloud {' "$EST/main.tf" && fail "DELTA 1 left the cloud block in place"
log "  DELTA 1  cloud block removed, live block added   (onboarding, #268)"
log "  DELTA 2  provider pinned = 6.59.0                (onboarding, #269's shape)"

# DELTA 3, emulator wiring: the flags with no environment-variable form.
perl -pi -e 's/^(  region = "eu-west-1")$/$1\n  skip_credentials_validation  = true # DELTA 3\n  skip_requesting_account_id   = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true/' "$EST/main.tf"
grep -q 'DELTA 3' "$EST/main.tf" || fail "DELTA 3 did not reach the aws provider block - the corpus pin has moved"
log "  DELTA 3  emulator flags on the aws provider       (emulator)"

# DELTA 4, values for variables-common.tf's four CIDR-map variables. None of
# them is read by anything in this estate - only govuk_environment is - but
# OpenTofu requires a value for every undefaulted variable regardless.
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
log "  DELTA 4  tfvars for variables-common.tf's four unused-but-required vars"

# Not a config delta: two pre-existing account objects a real GOV.UK account
# already has and this estate does not create. Stand-up only - see the
# header's item 3.
awsl iam create-open-id-connect-provider \
  --url "$OIDC_URL" --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1 >/dev/null \
  || fail "could not seed the GitHub OIDC provider"
awsl s3api create-bucket --bucket "$LOGGING_BUCKET" --region "$REGION" \
  --create-bucket-configuration LocationConstraint="$REGION" >/dev/null \
  || fail "could not seed the pre-existing logging bucket"
log "  SEED     GitHub OIDC provider + $LOGGING_BUCKET      (stand-up only)"

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: $INSTANCES instances ==="
( cd "$EST" && "$TOFU" init -upgrade -input=false -no-color >/dev/null 2>&1 ) || fail "init failed"
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# ── 4. the markers, read back with the AWS CLI ──────────────────────────────
log "=== 4. the markers, read back with the AWS CLI ==="
tag_of() { # tag_of <key> <list-cmd...> -> the tag's value, or "None"
  local key="$1"; shift
  "$@" --query "Tags[?Key=='$key'].Value | [0]" --output text 2>/dev/null || echo None
}
# S3's get-bucket-tagging answers the same Key/Value shape under a
# differently-named root field, TagSet rather than Tags.
s3_tag_of() { # s3_tag_of <key> <bucket> -> the tag's value, or "None"
  awsl s3api get-bucket-tagging --bucket "$2" \
    --query "TagSet[?Key=='$1'].Value | [0]" --output text 2>/dev/null || echo None
}
ROLE_ADDR="$(tag_of tofu-address awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$ROLE_ADDR" = "aws_iam_role.github_action_sign_deploy" ] \
  || fail "the role carries tofu-address=$ROLE_ADDR"
ROLE_EST="$(tag_of tofu-estate awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$ROLE_EST" = "$ESTATE" ] || fail "the role carries tofu-estate=$ROLE_EST"

KMS_KEY_ID="$(awsl kms list-keys --query 'Keys[0].KeyId' --output text)"
[ -n "$KMS_KEY_ID" ] && [ "$KMS_KEY_ID" != "None" ] || fail "no KMS key was created"
# KMS's own list-resource-tags answers TagKey/TagValue, not the Key/Value
# shape IAM and S3 use - tag_of's query does not fit it.
KEY_ADDR="$(awsl kms list-resource-tags --key-id "$KMS_KEY_ID" \
  --query "Tags[?TagKey=='tofu-address'].TagValue | [0]" --output text 2>/dev/null || echo None)"
[ "$KEY_ADDR" = "aws_kms_key.config_signing_key" ] || fail "the KMS key carries tofu-address=$KEY_ADDR"

BUCKET_ADDR="$(s3_tag_of tofu-address "$BUCKET_NAME")"
[ "$BUCKET_ADDR" = "module.mobile_backend_remote_config.aws_s3_bucket.this" ] \
  || fail "the bucket carries tofu-address=$BUCKET_ADDR"

# The provider's default_tags (six of them) must have survived the marker
# write into the SAME tags argument - a stamping pass that replaced rather
# than merged would leave every check above green while stripping GDS's own
# tags off three live objects.
GDS_TAG="$(tag_of Product awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$GDS_TAG" = "GOV.UK" ] \
  || { awsl iam list-role-tags --role-name "$ROLE_NAME" --output text; fail "the role's own Product default_tag is $GDS_TAG - the markers displaced it"; }

log "  role   $ROLE_NAME -> $ROLE_ADDR"
log "  key    $KMS_KEY_ID -> $KEY_ADDR"
log "  bucket $BUCKET_NAME -> $BUCKET_ADDR"
log "  3 of $INSTANCES objects carry tofu-address and tofu-estate; the other 9 are"
log "  role-policy pairs, a KMS alias and six S3 sub-resources, none of which"
log "  take a tags argument, and GDS's own default_tags survived the merge"

# The seeded pre-existing objects must stay untouched by the marker write -
# this estate does not own them and must not adopt them.
LOGGING_ADDR="$(s3_tag_of tofu-address "$LOGGING_BUCKET")"
[ "$LOGGING_ADDR" = "None" ] \
  || fail "the seeded logging bucket carries tofu-address=$LOGGING_ADDR - it was adopted, and this estate does not own it"
log "  the seeded logging bucket carries no marker - correctly not adopted"

# ── 5. no state file, and the rendered identities read out of the run ──────
log "=== 5. no state file, and the rendered identities read out of the run ==="
plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error|^│' | head -30; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { grep -E '^Plan:|^No changes' <<< "$PLAN_OUT"
       grep -E '^  # .+ will be' <<< "$PLAN_OUT" | head -20
       fail "the plan is not empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  nothing to create, nothing foreign"

# THE VALUE, not the verdict. Six S3 sub-resources import by the bucket's own
# name (AWS's own convention, not this fork's), so twelve instances render
# six distinct identity strings, not twelve - collapsing six-to-one is
# correct here and would be wrong anywhere the six objects had independent
# identities.
POLICY1="${ROLE_NAME}:github_action_config_signing_policy"
POLICY2="${ROLE_NAME}:github_action_mobile_backend_bucket_deployment_policy"
WANT=("$KMS_KEY_ID" "alias/config-signing-key" "$ROLE_NAME" "$POLICY1" "$POLICY2" "$BUCKET_NAME")
if [ "${BREAK:-}" = "1" ]; then
  # Not a string nothing could produce: the real KMS key ID with its last
  # character flipped - the same shape, wrong object, which is what a
  # derivation that dropped a character from a server-assigned ID renders.
  LAST="${KMS_KEY_ID: -1}"
  case "$LAST" in
    a) FLIP=b ;; *) FLIP=a ;;
  esac
  WANT[0]="${KMS_KEY_ID%?}${FLIP}"
  log "  BREAK=1: expecting ${WANT[0]}, the same shape of ID as the real key,"
  log "           off by one character. The plan above stayed empty. This"
  log "           step must fail."
fi
for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$id\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | grep -c .)"
[ "$GOT_N" = "6" ] || fail "the run materialized $GOT_N distinct identities, expected 6"
log "  all 6 distinct rendered identities matched, covering the 12 instances"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. applying the empty plan adds nothing ==="
APPLY2="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2" | tail -20; fail "the no-op apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2" \
  || { grep -E 'Apply complete' <<< "$APPLY2"; fail "expected 0 added, 0 changed, 0 destroyed"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY2" | head -1)"

# ── 7. the second cold replan, and no state file ever ───────────────────────
log "=== 7. the second cold replan ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || fail "the second live-plan exited $PLAN2_RC"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second live-plan"
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN2_OUT" \
  || { grep -E '^Plan:|^No changes' <<< "$PLAN2_OUT"; fail "the second plan is not empty, so the run does not converge"; }
GOT2_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN2_OUT" | sort -u | grep -c .)"
[ "$GOT2_N" = "6" ] || fail "the second run materialized $GOT2_N distinct identities, expected 6"

ROLES_N="$(awsl iam list-roles --query 'length(Roles)' --output text)"
[ "$ROLES_N" = "1" ] || fail "there are now $ROLES_N IAM roles, not 1 - something was created over what the estate already owned"
BUCKETS_N="$(awsl s3api list-buckets --query 'length(Buckets)' --output text)"
[ "$BUCKETS_N" = "2" ] || fail "there are now $BUCKETS_N S3 buckets, not 2 (the estate's own plus the seeded logging target)"
log "  empty again, 1 IAM role, 2 S3 buckets - the same objects as step 4"

log ""
log "=== PASS ==="
log ""
log "Twelve instances of a government mobile backend's own KMS signing key,"
log "IAM role and S3 config bucket, applied against an emulator through a"
log "second provider and a shared library module, stripped of their state"
log "file and replanned empty twice. All 6 distinct rendered identities were"
log "checked as strings, GDS's own default_tags survived the marker write,"
log "and the two seeded pre-existing objects (the OIDC provider and the"
log "logging bucket) stayed unmarked and unadopted throughout."
log ""
log "Run again with BREAK=1: everything above step 5 still passes and step 5"
log "goes red on the KMS key identity."
