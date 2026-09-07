#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-hongbomiao-harbor recipe; run with: just demo-run corpus-hongbomiao-harbor)
# The fifth OpenTofu-native crossing, and a THIRD disjoint slice of
# hongbo-miao/hongbomiao.com (live/corpus-manifest.json, same pin as
# corpus-hongbomiao-labelbox and corpus-hongbomiao-storage): the "Harbor"
# section of environments/production/aws/kubernetes/main.tofu (S3 bucket +
# IAM user + inline user policy) - the ONE module block in that whole
# environment that needs no EKS cluster, no OIDC provider and no remote
# state, unlike every other IAM-role module there
# (velero_iam_role/mimir_iam_role/... all take
# amazon_eks_cluster_oidc_provider(_arn) from the real EKS cluster this
# file also builds). Exercises aws_iam_user/aws_iam_user_policy, a
# genuinely different resource pair from Labelbox's aws_iam_role/
# aws_iam_role_policy, both already-ratified DefaultTable rows. All five
# stages pass for real: 3 resources cold-deployed, 2 stamped (the inline
# user policy is correctly UNTAGGABLE), an empty replan with the state
# file deleted and identities re-asserted against the AWS CLI's own
# answer, a genuine no-op apply, and drift on the bucket's tags
# reconverging without touching the user. See the script's own header for
# why network/main.tofu (zero resources) and every OIDC-coupled IAM role
# in kubernetes/main.tofu were ruled out. Needs Docker, the AWS CLI, and
# the real `tofu` binary; runs on its own port (4728).
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for hongbo-miao/hongbomiao.com (live/corpus-manifest.json, pinned by
# commit - no tag, see that entry's own comment for why), a THIRD, disjoint
# slice of the same repository already crossed by
# live/e2e/corpus-hongbomiao-labelbox/run.sh (the "general" environment's
# Labelbox integration) and live/e2e/corpus-hongbomiao-storage/run.sh (the
# "storage" environment's remote-state-free bootstrap) - HANDOFF.md's own
# suggestion for broadening the OpenTofu-native lane without a fresh
# sourcing search, since estate-gen's own convention treats separate
# module examples/sections of one repository as legitimately separate
# crossing targets.
#
# THE SCOPING DECISION. hongbomiao.com's infrastructure/opentofu tree has
# exactly four AWS environments (general, storage, network, kubernetes),
# all of it cross-wired through terraform_remote_state, plus three
# non-AWS environments (nebius, cloudflare, snowflake) that floci - an
# AWS-only emulator - cannot run at all: those provider blocks target
# real Nebius/Cloudflare/Snowflake endpoints, not anything floci emulates,
# so no cold_deploy stage could even target floci for them. Surveyed for a
# THIRD self-contained AWS slice disjoint from Labelbox and storage:
#   - environments/production/aws/network/main.tofu is ALL data sources
#     (aws_vpc, aws_subnets) - zero resources, nothing to migrate.
#   - environments/production/aws/kubernetes/main.tofu opens with
#     `data "terraform_remote_state" "production_aws_network_..."` and
#     nearly every IAM-role module call in it
#     (velero_iam_role/mimir_iam_role/loki_iam_role/tempo_iam_role/
#     label_studio_iam_role/mlflow_run_iam_role/... - fifteen modules
#     under modules/amazon-eks/*_iam_role) takes
#     amazon_eks_cluster_oidc_provider(_arn) sourced from
#     module.amazon_eks_cluster, which this file builds via the full
#     terraform-aws-modules/eks module against real subnets from that
#     remote state - the same shape as the terraform-popular lane's own
#     terraform-aws-eks examples/basic crossing (cold_deploy/migrate PASS,
#     test_plan FAIL), too large and coupled to redo cheaply here.
#   - ONE section of kubernetes/main.tofu is genuinely self-contained: the
#     "Harbor" section (S3 bucket + IAM user + inline user policy) is the
#     ONLY module block in the whole environment that creates an
#     `aws_iam_user` rather than an OIDC-federated `aws_iam_role` - it
#     needs no EKS cluster, no OIDC provider, no remote state, nothing
#     this file's own EKS cluster resource produces. That is what this
#     script crosses - copied byte-identical from the pinned commit
#     (diffed below at DELTA), with this script's own root wiring
#     standing in for the remote-state-coupled environment root the same
#     way corpus-hongbomiao-labelbox's does for "general" and
#     corpus-hongbomiao-storage's does for "storage".
#
# What this slice contributes that Labelbox and storage did not:
# `aws_iam_user`/`aws_iam_user_policy` (an inline user policy, not a role
# policy) - both already-ratified DefaultTable rows
# (internal/live/identity/table_generated.go), the user's identity a plain
# client-named `name` component and the inline policy's a composite
# `USERNAME:POLICYNAME`, the same shape as `aws_iam_role`/
# `aws_iam_role_policy` in Labelbox but a genuinely different resource
# pair - so no "no orphan recovery" warning is expected here, unlike
# Labelbox's schema-fallback CORS configuration.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified two leaf modules - the honest proof
#                     the module code is real and buildable, and the
#                     source of genuinely unmarked live infra for stage 2.
#   2. MIGRATE        `choudoufu live-import -approve` against that cold
#                     state.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan is EMPTY *and* assert the rendered identity
#                     strings against the AWS CLI's own answer.
#   4. TEST APPLY     apply the empty plan; assert a genuine no-op by
#                     comparing the estate's tagged-object count before and
#                     after.
#   5. DRIFT AND      mutate one live object's tag out of band via the AWS
#      RECONVERGE     CLI directly against floci, replan, assert the diff
#                     proposes fixing exactly that one object, apply, and
#                     confirm the live tag is back to what the config says.
#
# BREAK=1 corrupts one expected tofu-address ahead of stage 2's assertion
# and tampers a second, unrelated live object ahead of stage 5's, so both
# assertions are proven load-bearing rather than a grep that always matches.
#
#   bash live/e2e/corpus-hongbomiao-harbor/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1
# (stock terraform cannot load a directory of .tofu files at all - see
# corpus-hongbomiao-labelbox's own header for the positive proof; not
# repeated here to keep this script shorter, but the same
# `terraform init` == "no configuration files" check is not needed a third
# time in one campaign and is skipped here on purpose).
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4728, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion and
#                 tamper a second object ahead of stage 5's, proving both
#                 are load-bearing. Set to "rename" to exercise day2_rename's
#                 own break control instead - renaming module harbor_iam_user
#                 WITHOUT a moved block, which must be refused. day2_replace
#                 (PART F) has no BREAK control of its own in this script -
#                 it targets the untaggable, composed-of-arguments inline
#                 policy (see PART F's own header for why neither taggable
#                 object here can be force-replaced), which has no marker
#                 to manufacture a collision on; that control's load-
#                 bearing-ness is proven by corpus-evoteum-modules and
#                 corpus-giantswarm-crossplane's own BREAK=replace instead.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control instead of
#                 the real remove checks: keep module.harbor_iam_user_renamed's
#                 block in the config and assert no destroy is proposed for
#                 it (the Break text in tools/gauntlet/stages.go for
#                 day2_remove is literally "keep the block; no destroy may
#                 be proposed"). Independent of BREAK and BREAK=rename, and
#                 only reachable when neither is set, because day2_remove
#                 starts from day2_rename's own real, completed rename.
#   BREAK_GREEN   set to 1 to run the greenfield stage's own break control
#                 instead of the real object-by-object comparison: drop one
#                 object from the actual inventory before the count check
#                 (the Break text in tools/gauntlet/stages.go for greenfield
#                 is literally "Drop one resource from the expected
#                 inventory; the comparison must fail"). Independent of the
#                 other BREAK flags - greenfield runs before all of them,
#                 right after STAGE 1's cold deploy.
#   BREAK_COUNT   set to 1 to run day2_count's own break control instead of
#                 the real scale-down checks: after the real scale-down
#                 plan, assert the WRONG instance (count_test[0] rather
#                 than count_test[1]) was the one destroyed (the Break
#                 text in tools/gauntlet/stages.go for day2_count is
#                 literally "Expect a different instance to be destroyed;
#                 the assertion must fail"). Only reachable when BREAK is
#                 not "rename" and BREAK_REMOVE is not 1, because
#                 day2_count starts from day2_remove's own real, completed
#                 removal.
#   BREAK_APPROVAL
#                 set to 1 to run plan_approval's own negative control
#                 instead of the real refusal check (PART P): after the
#                 world has moved out of band, assert the saved plan file
#                 APPLIES cleanly - the Break text in
#                 tools/gauntlet/stages.go for plan_approval is literally
#                 "Apply the planfile after a mutation and expect success;
#                 the run must refuse", so this assertion has to fail.
#                 Independent of every BREAK above, and the only one of them
#                 under which PART P runs at all - the others deliberately
#                 leave the estate somewhere PART P does not describe, and
#                 it reports no verdict there.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_AWS="$ROOT/.corpus/hongbomiao/infrastructure/opentofu/modules/aws"
SRC_EKS="$ROOT/.corpus/hongbomiao/infrastructure/opentofu/modules/amazon-eks"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4728}"
FLOCI_NAME="choudoufu-corpus-hongbomiao-harbor-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="hongbomiao-harbor-crossing"
BUCKET_NAME="${ESTATE_NAME}-hm-harbor"
USER_NAME="${ESTATE_NAME}-hm-harbor-user"
POLICY_NAME="S3ReadWritePolicy-${BUCKET_NAME}"

cleanup() {
  docker rm -f "$FLOCI_NAME" "${FLOCI_GREEN_NAME:-}" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }

# The gauntlet protocol (live/GAUNTLET.md): each stage reports its verdict on
# stdout so tools/gauntlet records it. CURRENT_STAGE names the stage a
# failure belongs to; fail() reports it before exiting.
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/gauntlet.sh"
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1"
[ -d "$SRC_AWS/amazon_s3_bucket" ] && [ -d "$SRC_EKS/harbor_iam_user" ] \
  || fail "$SRC_AWS/amazon_s3_bucket or $SRC_EKS/harbor_iam_user is missing - fetch hongbo-miao/hongbomiao.com at the pin in live/corpus-manifest.json first"
log "  cold deploy binary: tofu ($(tofu version | head -1))"

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

# copy_leaf_modules <destdir>: the two real, unmodified leaf modules - every
# file in them keeps hongbomiao.com's own .tofu extension.
copy_leaf_modules() {
  local dest="$1"
  mkdir -p "$dest/modules/aws" "$dest/modules/amazon-eks"
  cp -R "$SRC_AWS/amazon_s3_bucket" "$dest/modules/aws/amazon_s3_bucket"
  cp -R "$SRC_EKS/harbor_iam_user" "$dest/modules/amazon-eks/harbor_iam_user"
}

# write_root <destdir> <live_block>: this crossing's own root wiring,
# standing in for the remote-state-coupled environment root (see header).
# The two module calls below use the SAME source paths and the SAME
# argument names as hongbomiao.com's own aws/kubernetes/main.tofu's
# "Harbor" section, other than the estate-scoped bucket/user names. Written
# with a .tofu extension, matching every file this project's own real
# modules use.
write_root() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tofu" <<EOF
terraform {
  required_version = ">= 1.11"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
$live_block
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

provider "aws" {
  alias  = "production"
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

locals {
  common_tags = {
    "hm_environment" = "production"
    "hm_team"        = "hongbomiao"
    "hm_managed_by"  = "opentofu"
  }
}

# Harbor - S3 bucket
module "s3_bucket_hm_harbor" {
  providers      = { aws = aws.production }
  source         = "./modules/aws/amazon_s3_bucket"
  s3_bucket_name = "$BUCKET_NAME"
  common_tags    = local.common_tags
}

# Harbor - IAM user
module "harbor_iam_user" {
  providers         = { aws = aws.production }
  source            = "./modules/amazon-eks/harbor_iam_user"
  aws_iam_user_name = "$USER_NAME"
  s3_bucket_name    = module.s3_bucket_hm_harbor.name
  common_tags       = local.common_tags
}
EOF
}

copy_leaf_modules "$PLAIN"
write_root "$PLAIN" ""
log "  two leaf modules copied unmodified out of .corpus/hongbomiao into $PLAIN"

# DELTA: confirm the copy is byte-identical to the pinned commit - the only
# things this crossing adds are its OWN root file and provider block, never
# an edit to hongbomiao.com's own module code.
diff -rq "$SRC_AWS/amazon_s3_bucket" "$PLAIN/modules/aws/amazon_s3_bucket" >/dev/null \
  || fail "modules/aws/amazon_s3_bucket differs from the pinned commit - this crossing must run the real, unmodified module"
diff -rq "$SRC_EKS/harbor_iam_user" "$PLAIN/modules/amazon-eks/harbor_iam_user" >/dev/null \
  || fail "modules/amazon-eks/harbor_iam_user differs from the pinned commit - this crossing must run the real, unmodified module"
log "  DELTA confirmed: both leaf modules are byte-identical to the pinned commit; only this script's own root file was added"

copy_leaf_modules "$ESTATE"
write_root "$ESTATE" '
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  estate copy written to $ESTATE (stages 2-5: choudoufu, live block added)"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${HEALTH:-}" && grep -q '"iam"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${HEALTH:-}" && grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (s3/iam) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified modules) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
gauntlet_stage cold_deploy pass "$(grep -E 'Apply complete' <<< "$COLD_OUT"); 0 objects carry tofu-estate=$ESTATE_NAME before migration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The estate's two taggable roots: a `moved` block renames the WHOLE module
# call "s3_bucket_hm_harbor" (its own aws_s3_bucket.main is the only object
# it carries, and is referenced by harbor_iam_user's s3_bucket_name), and
# "choudoufu live-mv" (below, after drift_reconverge) renames the whole
# module call "harbor_iam_user" with no moved block at all - a leaf no
# other module references. Neither leaf module's own source is touched
# (DELTA discipline). aws_s3_bucket.main carries
# `lifecycle { prevent_destroy = true }` in the real module, so BREAK=1's
# rename-without-moved control below renames the user, never the bucket.
# The stock oracle (real tofu - stock terraform cannot see this .tofu-only
# estate at all, see header) runs the same two renames, through moved
# blocks only, on a copy of cold_deploy's own state - before choudoufu or
# live-import ever touch these objects.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE: stock tofu, the same two renames through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/module "s3_bucket_hm_harbor" {/module "s3_bucket_hm_harbor_renamed" {/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module\.s3_bucket_hm_harbor\.name/module.s3_bucket_hm_harbor_renamed.name/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module "harbor_iam_user" {/module "harbor_iam_user_renamed" {/' "$PLAIN_ORACLE/main.tofu"
rm -f "$PLAIN_ORACLE/main.tofu.bak"
cat >> "$PLAIN_ORACLE/main.tofu" <<'EOF'

moved {
  from = module.s3_bucket_hm_harbor
  to   = module.s3_bucket_hm_harbor_renamed
}

moved {
  from = module.harbor_iam_user
  to   = module.harbor_iam_user_renamed
}
EOF
( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && tofu plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART E-ORACLE: REMOVE A BLOCK, stock oracle (day2_remove, live/GAUNTLET.md
# #7). Another separate copy of cold_deploy's own state, so this destroy has
# nothing to do with the rename oracle above. Removes module.harbor_iam_user's
# block entirely - it is a leaf no other module references (unlike
# s3_bucket_hm_harbor, which harbor_iam_user's s3_bucket_name reads, and
# which also carries `lifecycle { prevent_destroy = true }` in the real
# module - the reason BOTH this oracle and Part E below remove the user, not
# the bucket). The module holds TWO resources - aws_iam_user (taggable) and
# aws_iam_user_policy (untaggable, inline) - so removing its block is a real
# instance of live/GAUNTLET.md #7's "blocks for untaggable children whose
# parents stay" concern: the untaggable inline policy has to be destroyed
# ahead of its own parent user in an order IAM accepts (a user cannot be
# deleted while an inline policy is still attached).
gauntlet_begin_stage day2_remove
log "=== E-ORACLE: stock tofu, delete module.harbor_iam_user's block on cold_deploy's own state ==="
PLAIN_REMOVE_ORACLE="$WORK/plain-remove-oracle"
cp -r "$PLAIN" "$PLAIN_REMOVE_ORACLE"
perl -0pi -e 's/\n# Harbor - IAM user\nmodule "harbor_iam_user" \{.*?\n\}\n//s' "$PLAIN_REMOVE_ORACLE/main.tofu"
grep -q 'module "harbor_iam_user"' "$PLAIN_REMOVE_ORACLE/main.tofu" \
  && fail "removing module.harbor_iam_user's block from the oracle copy did not match - this script's own root wiring has moved"
( cd "$PLAIN_REMOVE_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_REMOVE_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_REMOVE_ORACLE" && tofu plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.harbor_iam_user\.aws_iam_user\.hm_harbor_iam_user will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the IAM user when module.harbor_iam_user's block is removed"; }
grep -qE '^  # module\.harbor_iam_user\.aws_iam_user_policy\.hm_aws_iam_user_policy will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying the inline user policy when module.harbor_iam_user's block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly two destroys (the user and its inline policy)"; }
log "  stock: exactly two destroys (the IAM user and its inline policy), nothing else, on the state cold_deploy produced"

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock oracle (day2_replace, live/GAUNTLET.md #9):
# "Stock's replace of the same resource leaves the same single object." A
# THIRD separate copy of cold_deploy's own state ($PLAIN), unrenamed and
# unremoved, so this oracle has nothing to do with the rename/remove
# oracles above. Neither of this estate's two taggable objects can be
# force-replaced here (see PART F's own header for the full reasoning,
# discovered BY this oracle, in this order): the bucket carries
# `lifecycle { prevent_destroy = true }` in the real module, and - VERIFIED
# HERE FIRST, against stock, no choudoufu in the loop - aws_iam_user's
# `name` argument is genuinely NOT ForceNew (AWS's IAM UpdateUser API
# supports renaming a user in place; unlike aws_iam_role/aws_iam_policy in
# corpus-giantswarm-crossplane's own F-ORACLE, where name IS ForceNew).
# What DOES force-replace is the untaggable, composed-of-arguments
# aws_iam_user_policy - changing what its own `name` argument evaluates to
# (module source: `name = "S3ReadWritePolicy-${var.s3_bucket_name}"`)
# changes its identity and forces a replace at the SAME declared address
# (IAM's PutUserPolicy/DeleteUserPolicy have no rename op either), with the
# user itself completely untouched. PLAN ONLY, never applied - same
# convention as the rename/remove oracles above.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_replace
log "=== F-ORACLE: stock tofu, on cold_deploy's own state - confirm aws_iam_user_name is NOT ForceNew, then force-replace the inline policy instead ==="
PLAIN_ORACLE_REPLACE="$WORK/plain-oracle-replace"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE"
rm -rf "$PLAIN_ORACLE_REPLACE/.terraform"
sed -i.bak "s/aws_iam_user_name = \"$USER_NAME\"/aws_iam_user_name = \"${USER_NAME}-v2\"/" "$PLAIN_ORACLE_REPLACE/main.tofu"
rm -f "$PLAIN_ORACLE_REPLACE/main.tofu.bak"
grep -q "${USER_NAME}-v2" "$PLAIN_ORACLE_REPLACE/main.tofu" \
  || fail "changing module.harbor_iam_user's aws_iam_user_name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
NAME_CHANGE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE" && tofu plan -input=false -no-color 2>&1)"; NAME_CHANGE_PLAN_RC=$?
[ "$NAME_CHANGE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$NAME_CHANGE_PLAN_OUT" | tail -40; fail "the aws_iam_user_name-change stock plan exited $NAME_CHANGE_PLAN_RC"; }
grep -qE '^  # module\.harbor_iam_user\.aws_iam_user\.hm_harbor_iam_user will be updated in-place' <<< "$NAME_CHANGE_PLAN_OUT" \
  || { printf '%s\n' "$NAME_CHANGE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock's own behaviour for aws_iam_user_name has changed - it no longer updates the user in-place (see PART F's own header: this estate's whole design rests on aws_iam_user's name NOT being ForceNew)"; }
log "  stock CONFIRMS aws_iam_user.name is not ForceNew: renaming updates the user in-place, never replaces it - the reason this section targets the inline policy instead"

PLAIN_ORACLE_REPLACE2="$WORK/plain-oracle-replace2"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE2"
rm -rf "$PLAIN_ORACLE_REPLACE2/.terraform"
sed -i.bak "s/s3_bucket_name    = module.s3_bucket_hm_harbor.name/s3_bucket_name    = \"${BUCKET_NAME}-policy-v2\"/" "$PLAIN_ORACLE_REPLACE2/main.tofu"
rm -f "$PLAIN_ORACLE_REPLACE2/main.tofu.bak"
grep -q "${BUCKET_NAME}-policy-v2" "$PLAIN_ORACLE_REPLACE2/main.tofu" \
  || fail "changing module.harbor_iam_user's s3_bucket_name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$PLAIN_ORACLE_REPLACE2" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REPLACE2" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's second reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE2" && tofu plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.harbor_iam_user\.aws_iam_user_policy\.hm_aws_iam_user_policy must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.harbor_iam_user's inline policy when its own name-driving argument changes"; }
grep -qE '^  # module\.harbor_iam_user\.aws_iam_user\.hm_harbor_iam_user will be' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes touching the user itself - this section's whole point is that only the inline policy is affected"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add and one destroy at the same address"; }
log "  stock: exactly one replace proposed (the inline policy only, user untouched) at the same declared address, on the state cold_deploy produced - plan only, not applied"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active stage)
# ══════════════════════════════════════════════════════════════════════════
#
# One more, fresh floci container. STAGE 1's own container ($FLOCI_NAME on
# $FLOCI_PORT) is reused as THIS stage's oracle rather than standing up a
# third one: nothing between cold_deploy and here has applied, changed or
# destroyed anything in it - the day2_rename and day2_remove oracle blocks
# above only run `tofu plan` against COPIES of cold_deploy's state (never
# `apply`), so it still holds exactly stock's unmodified, unmarked
# cold-deploy inventory - the oracle live/GAUNTLET.md #13 names verbatim
# ("the cloud after stock's cold deploy"). Greenfield applies the identical,
# unmodified leaf modules (a live block added, nothing else) into a
# namespace of its own with choudoufu directly - no migration at all. The
# SAME bucket and user names are reused (a fresh, isolated floci container
# is a separate account, so there is no collision), which makes the
# object-by-object comparison below a byte-for-byte one.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-hongbomiao-harbor-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
GREEN_ESTATE_NAME="hongbomiao-harbor-greenfield"

docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
for _ in $(seq 1 45); do
  GREEN_HEALTH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"s3"' <<< "${GREEN_HEALTH:-}" && grep -q '"iam"' <<< "${GREEN_HEALTH:-}" && break
  sleep 2
done
grep -q '"s3"' <<< "${GREEN_HEALTH:-}" && grep -q '"iam"' <<< "${GREEN_HEALTH:-}" \
  || fail "the greenfield floci did not come up healthy (s3/iam) at $GREEN_ENDPOINT"
log "  healthy: greenfield=$GREEN_ENDPOINT"

GREEN="$WORK/greenfield"
copy_leaf_modules "$GREEN"
write_root "$GREEN" '
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  greenfield estate written to $GREEN (same two unmodified leaf modules, a live block from the start)"

gauntlet_begin_stage greenfield
log "=== PART GREENFIELD 1. choudoufu apply directly, no migration ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; GREEN_APPLY_RC=$?
[ "$GREEN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

log "=== PART GREENFIELD 2. markers, read through the AWS CLI directly ==="
awslg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
GREEN_WANT_BUCKET_ADDR="module.s3_bucket_hm_harbor.aws_s3_bucket.main"
GREEN_WANT_USER_ADDR="module.harbor_iam_user.aws_iam_user.hm_harbor_iam_user"
GREEN_BUCKET_ADDR="$(awslg s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_BUCKET_ADDR" = "$GREEN_WANT_BUCKET_ADDR" ] || fail "the greenfield bucket carries tofu-address=$GREEN_BUCKET_ADDR, not $GREEN_WANT_BUCKET_ADDR"
GREEN_BUCKET_ESTATE="$(awslg s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_BUCKET_ESTATE" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield bucket carries tofu-estate=$GREEN_BUCKET_ESTATE, not $GREEN_ESTATE_NAME"
GREEN_USER_ADDR="$(awslg iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_USER_ADDR" = "$GREEN_WANT_USER_ADDR" ] || fail "the greenfield user carries tofu-address=$GREEN_USER_ADDR, not $GREEN_WANT_USER_ADDR"
log "  bucket $BUCKET_NAME -> tofu-address=$GREEN_BUCKET_ADDR tofu-estate=$GREEN_BUCKET_ESTATE; user $USER_NAME -> tofu-address=$GREEN_USER_ADDR - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD 3. the local record store holds one record per instance, taggable and untaggable alike (#364 A2) ==="
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" = "3" ] || fail "expected 3 records under the local record store after the greenfield apply (bucket, user, inline policy), found $GREEN_RECORD_FILES"
log "  3 records persisted, one per managed instance including the untaggable inline policy, read directly off the local record store"

log "=== PART GREENFIELD 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD 5. delete the local record store; plan a third time ==="
rm -rf "$GREEN/.tofu-records"
GREEN_PLAN2_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN2_RC=$?
[ "$GREEN_PLAN2_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN2_OUT" | tail -30; fail "the third greenfield plan (no local record store) exited $GREEN_PLAN2_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN2_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN2_OUT"; fail "the third greenfield plan is not empty with no local record store - the objects are not being found by their tags alone"; }
log "  No changes, with zero local memory of the run that created them"

log "=== PART GREENFIELD 6. object-by-object against stock's own cold-deploy container (STAGE 1, untouched since) ==="
# resourcegroupstaggingapi only ever returns TAGGABLE objects (the bucket and
# the user) - the untaggable inline policy is never in that count, so it is
# counted separately, by whether its content comes back readable at all.
GREEN_TAGGABLE_COUNT="$(awslg resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$GREEN_TAGGABLE_COUNT" = "2" ] || fail "the greenfield estate has $GREEN_TAGGABLE_COUNT taggable objects, expected 2 (the bucket and the user)"
GREEN_POLICY_DOC="$(awslg iam get-user-policy --user-name "$USER_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument' --output json 2>/dev/null || true)"
COLD_POLICY_DOC="$(awsl iam get-user-policy --user-name "$USER_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument' --output json 2>/dev/null || true)"
GREEN_TOTAL_COUNT=2
[ -n "$GREEN_POLICY_DOC" ] && [ "$GREEN_POLICY_DOC" != "None" ] && GREEN_TOTAL_COUNT=3
if [ "${BREAK_GREEN:-}" = "1" ]; then
  GREEN_TOTAL_COUNT=$((GREEN_TOTAL_COUNT - 1))
  log "  BREAK_GREEN=1: dropped one object from the actual inventory - the count comparison below must fail"
fi
[ "$GREEN_TOTAL_COUNT" = "3" ] \
  || fail "the greenfield estate has $GREEN_TOTAL_COUNT objects (2 taggable plus the inline policy, if readable), expected 3 - the object-by-object comparison against stock's cold deploy must fail on a dropped resource"
[ "$GREEN_POLICY_DOC" = "$COLD_POLICY_DOC" ] || fail "the inline user policy's document differs between the greenfield estate and stock's cold deploy"
GREEN_BUCKET_LOCATION="$(awslg s3api get-bucket-location --bucket "$BUCKET_NAME" --query 'LocationConstraint' --output text 2>&1 || true)"
COLD_BUCKET_LOCATION="$(awsl s3api get-bucket-location --bucket "$BUCKET_NAME" --query 'LocationConstraint' --output text 2>&1 || true)"
[ "$GREEN_BUCKET_LOCATION" = "$COLD_BUCKET_LOCATION" ] || fail "the bucket's location differs between the greenfield estate and stock's cold deploy"
GREEN_USER_PATH="$(awslg iam get-user --user-name "$USER_NAME" --query 'User.Path' --output text)"
COLD_USER_PATH="$(awsl iam get-user --user-name "$USER_NAME" --query 'User.Path' --output text)"
[ "$GREEN_USER_PATH" = "$COLD_USER_PATH" ] || fail "the user's path differs between the greenfield estate and stock's cold deploy"
log "  2 taggable objects plus the inline policy match stock's cold-deploy container object by object (policy document, bucket location, user path), marker tags never compared"

log ""
log "PART GREENFIELD (greenfield): PASS"
gauntlet_stage greenfield pass "3 resources from nothing (bucket, user, untaggable inline policy), markers verified via the AWS CLI, 3 records in the local record store (#364 A2), replan empty both with and without the local record store, all objects match stock's cold-deploy container (STAGE 1, untouched) object by object, marker tags never compared"
log ""
gauntlet_end_stage
# $FLOCI_GREEN_NAME/$GREEN_ENDPOINT is deliberately kept alive past this
# point, unlike every other estate's own greenfield container: day2_count,
# far below, reuses it as its stock oracle's idle account, the same
# discipline reference-ec2-vpc's own B1.7 and corpus-iam-read-only-
# policy's own G-ORACLE both use ("the greenfield account already
# finished with, holding only a completely different-named estate,
# never touched again") - torn down for good only by the top-level
# cleanup() trap, alongside $FLOCI_NAME, when the whole script exits.

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "2 of 3 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 3 as eligible (the S3 bucket and the IAM user - the inline user policy is correctly UNTAGGABLE, see header)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE (1)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 1 UNTAGGABLE resource (the inline user policy)"
log "  2 of 3 verified against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 2 of 3 resources cleanly"; }
log "  2 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_BUCKET_ADDR="module.s3_bucket_hm_harbor.aws_s3_bucket.main"
WANT_USER_ADDR="module.harbor_iam_user.aws_iam_user.hm_harbor_iam_user"
if [ "${BREAK:-}" = "1" ]; then
  WANT_USER_ADDR="module.harbor_iam_user.aws_iam_user.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the IAM user on purpose - this check must fail"
fi

GOT_BUCKET_ADDR="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ADDR" = "$WANT_BUCKET_ADDR" ] || fail "the S3 bucket carries tofu-address=$GOT_BUCKET_ADDR, not $WANT_BUCKET_ADDR"
GOT_BUCKET_ESTATE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_BUCKET_ESTATE" = "$ESTATE_NAME" ] || fail "the S3 bucket carries tofu-estate=$GOT_BUCKET_ESTATE, not $ESTATE_NAME"
log "  bucket $BUCKET_NAME -> tofu-address=$GOT_BUCKET_ADDR tofu-estate=$GOT_BUCKET_ESTATE"

GOT_USER_ADDR="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_USER_ADDR" = "$WANT_USER_ADDR" ] || fail "the IAM user carries tofu-address=$GOT_USER_ADDR, not $WANT_USER_ADDR"
log "  user   $USER_NAME -> tofu-address=$GOT_USER_ADDR"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the user's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
gauntlet_stage migrate pass "2 of 3 stamped (bucket, user), 1 UNTAGGABLE (inline policy); bucket $BUCKET_NAME -> tofu-address=$GOT_BUCKET_ADDR, user $USER_NAME -> tofu-address=$GOT_USER_ADDR"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "live-plan is not empty"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"

# Re-assert identities directly against the live objects, after the local
# state file was deleted - any answer below can only have come from the
# marker (or, for the untaggable inline policy, the re-derived identity) on
# the live object itself.
BUCKET_ADDR2="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$BUCKET_ADDR2" = "$WANT_BUCKET_ADDR" ] || fail "the bucket's tofu-address changed across the empty plan: $WANT_BUCKET_ADDR -> $BUCKET_ADDR2"
USER_ADDR2="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$USER_ADDR2" = "$WANT_USER_ADDR" ] || fail "the user's tofu-address changed across the empty plan: $WANT_USER_ADDR -> $USER_ADDR2"

# The inline user policy has no tag to re-read, so its identity assertion is
# the live object's OWN content, read directly - the plan came back empty
# above, meaning the composite USERNAME:POLICYNAME derivation found exactly
# this live object with no diff, and this independently confirms what it
# found is correct.
GOT_POLICY_RESOURCE="$(awsl iam get-user-policy --user-name "$USER_NAME" --policy-name "$POLICY_NAME" --query 'PolicyDocument.Statement[0].Resource[0]' --output text)"
[ "$GOT_POLICY_RESOURCE" = "arn:aws:s3:::$BUCKET_NAME" ] || fail "the live inline user policy's first Resource ($GOT_POLICY_RESOURCE) does not match the configuration"
log "  identity re-check: bucket and user tofu-address unchanged; the inline user policy's resource ARN, read directly off the live object, still matches the configuration"

log ""
log "STAGE 3 (test plan): PASS"
gauntlet_stage test_plan pass "empty plan; identity re-check: bucket and user tofu-address unchanged, inline policy's resource ARN still matches the configuration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object's tag out of band) ==="

if [ "${BREAK:-}" = "1" ]; then
  awsl iam tag-user --user-name "$USER_NAME" --tags Key=hm_team,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered the IAM user's hm_team tag - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

awsl s3api put-bucket-tagging --bucket "$BUCKET_NAME" --tagging '{
  "TagSet": [
    {"Key": "hm_environment", "Value": "production"},
    {"Key": "hm_team", "Value": "tampered-out-of-band"},
    {"Key": "hm_managed_by", "Value": "opentofu"},
    {"Key": "hm_resource_name", "Value": "'"$BUCKET_NAME"'"},
    {"Key": "tofu-address", "Value": "'"$WANT_BUCKET_ADDR"'"},
    {"Key": "tofu-estate", "Value": "'"$ESTATE_NAME"'"}
  ]
}'
DRIFTED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $BUCKET_NAME's hm_team tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "           one - the single-object assertion and reconverge apply below are skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "module.s3_bucket_hm_harbor.aws_s3_bucket.main" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the S3 bucket"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "hongbomiao" ] \
    || fail "the bucket's hm_team tag is \"$FIXED_VALUE\" after reconverging, not \"hongbomiao\""
  log "  reconverged: $BUCKET_NAME's hm_team tag is back to \"hongbomiao\", read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
gauntlet_stage drift_reconverge pass "the plan proposed fixing $N_CHANGED object(s) after the out-of-band tag mutation: $CHANGED_ADDRS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART P: PLAN, REVIEW, APPLY (plan_approval, live/GAUNTLET.md #12, issue #903)
# ══════════════════════════════════════════════════════════════════════════
#
# The pipeline shape CI has always run: plan on the pull request, a human
# approves, apply exactly what was approved. The artifact that crosses that
# gate is the plan file, and under live markers it is an APPROVAL rather
# than an instruction - "apply <planfile>" re-reads the live system, plans
# against what it finds now, and compares that fresh plan with the file's,
# refusing by name and with exit 3 when the two disagree (issue #878,
# internal/command/live_approval.go).
#
# Both arms run on every real run, because only the pair is evidence:
#
#   P2/P3  the world MOVES between the approval and the apply - the Harbor S3 bucket's
#          hm_team tag is changed out of band through the AWS CLI, the same
#          mutation STAGE 5 above already proves this estate's plan notices
#          - and the apply must refuse: exit 3, the named summary, the
#          unapproved row printed by address AND by the live object it was
#          computed against, and the reviewed change still not landed when
#          the Harbor IAM user is read back through the CLI.
#   P4     nothing has moved (the tag is put back first) and the SAME file
#          must APPLY. This is the inverted control that
#          live/smoke/scenarios/apply-what-was-approved.sh reasons out: a
#          comparison which refuses unconditionally is not a check, so P3's
#          refusal is only worth something if the identical artifact goes
#          through when the world is where the approval left it.
#
# The two objects are deliberately disjoint - the change under review is on
# the Harbor IAM user, the out-of-band move is on the Harbor S3 bucket - so the refusal
# has an EXTRA row to name rather than a values-only disagreement about the
# same row.
#
# The reviewed edit is an in-place tag update, never a create or a destroy:
# every later part of this script (D's two renames, F's forced replace, E's
# removal, G's count) re-reads its own objects by their tofu-address marker
# AFTER this part reverts, and a replaced instance would hand them a live id
# minted here rather than by the leg that is supposed to mint it.
#
# Runs only on a real run. Under any of this script's other BREAK controls
# the estate is deliberately left somewhere this part does not describe, so
# it reports no verdict at all and the runner records the stage as not_run,
# never as a pass.
if [ -z "${BREAK:-}" ] && [ -z "${BREAK_REMOVE:-}" ] && [ -z "${BREAK_GREEN:-}" ] \
   && [ -z "${BREAK_COUNT:-}" ]; then
  gauntlet_begin_stage plan_approval
  log "=== PART P: plan, review, apply (the approval gate, live/GAUNTLET.md #12) ==="

  P_REVIEWED_ADDR="$WANT_USER_ADDR"
  P_MOVED_ADDR="$WANT_BUCKET_ADDR"

  log "=== P1. the change under review: one argument of this crossing's own root wiring ==="
  # common_tags is the module's OWN documented variable, and inside
  # harbor_iam_user it reaches exactly one taggable resource
  # ($WANT_USER_ADDR) - the module's other object, where it has one, is an
  # untaggable inline policy that common_tags never reaches. Merging one key
  # into it is an in-place update of exactly one instance and leaves every
  # other module in this root alone, the Harbor S3 bucket included.
  [ "$(grep -cE 'common_tags[[:space:]]+= local\.common_tags' "$ESTATE/main.tofu")" = "2" ] \
    || fail "main.tofu no longer carries exactly 2 \"common_tags = local.common_tags\" module arguments - this crossing's own root wiring has moved"
  perl -0pi -e 's/(module "harbor_iam_user" \{\n(?:[^\n]*\n)*?  common_tags\s+= )local\.common_tags/$1 . "merge(local.common_tags, { Reviewed = \"yes\" })"/e' "$ESTATE/main.tofu"
  [ "$(grep -c 'Reviewed = "yes"' "$ESTATE/main.tofu")" = "1" ] \
    || fail "the reviewed edit did not write exactly one merge(local.common_tags, ...) argument"
  [ "$(grep -cE 'common_tags[[:space:]]+= local\.common_tags' "$ESTATE/main.tofu")" = "1" ] \
    || fail "the reviewed edit changed more than one of the 2 \"common_tags = local.common_tags\" module arguments"
  log "  edited one argument: harbor_iam_user's common_tags now merge in Reviewed = \"yes\""

  P_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color -out=approved.tfplan 2>&1)"; P_PLAN_RC=$?
  [ "$P_PLAN_RC" -eq 0 ] || { printf '%s\n' "$P_PLAN_OUT" | tail -40; fail "plan -out exited $P_PLAN_RC"; }
  [ -f "$ESTATE/approved.tfplan" ] || { printf '%s\n' "$P_PLAN_OUT" | tail -20; fail "plan -out wrote no file"; }
  P_APPROVED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$P_PLAN_OUT" | awk '{print $2}' | sort -u)"
  [ "$P_APPROVED_ADDRS" = "$P_REVIEWED_ADDR" ] \
    || { grep -E '^  # .+ will be' <<< "$P_PLAN_OUT"; fail "the approved plan is about [$P_APPROVED_ADDRS], not $P_REVIEWED_ADDR alone"; }
  if grep -qE '^  # .+ will be (created|destroyed)' <<< "$P_PLAN_OUT"; then
    grep -E '^  # .+ will be' <<< "$P_PLAN_OUT"; fail "the approved plan proposes a create or a destroy; this review is one in-place update"
  fi
  P_PLAN_BYTES="$(wc -c < "$ESTATE/approved.tfplan" | tr -d ' ')"
  log "  approved.tfplan written ($P_PLAN_BYTES bytes of stock-format plan file); the approval is exactly one update, on $P_REVIEWED_ADDR"

  log "=== P2. the world moves between the approval and the apply ==="
  # Captured, not spelled out: P4 puts back exactly the tag set the bucket
  # carried when the approval was taken, markers included.
  P_TAGS_BEFORE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --output json)"
  grep -q '"hm_team"' <<< "$P_TAGS_BEFORE" || fail "$BUCKET_NAME's tag set does not carry hm_team before PART P's own move"
  awsl s3api put-bucket-tagging --bucket "$BUCKET_NAME" --tagging '{
    "TagSet": [
      {"Key": "hm_environment", "Value": "production"},
      {"Key": "hm_team", "Value": "moved-after-approval"},
      {"Key": "hm_managed_by", "Value": "opentofu"},
      {"Key": "hm_resource_name", "Value": "'"$BUCKET_NAME"'"},
      {"Key": "tofu-address", "Value": "'"$P_MOVED_ADDR"'"},
      {"Key": "tofu-estate", "Value": "'"$ESTATE_NAME"'"}
    ]
  }'
  P_MOVED_VALUE="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
  [ "$P_MOVED_VALUE" = "moved-after-approval" ] || fail "the out-of-band move did not take: $BUCKET_NAME's hm_team tag reads \"$P_MOVED_VALUE\""
  log "  $BUCKET_NAME's hm_team tag changed out of band to \"moved-after-approval\" - after the approval, before the apply, through the AWS CLI"

  log "=== P3. apply the approved plan against a world that moved ==="
  P_GATE_RC=0
  P_GATE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_GATE_RC=$?
  if [ "${BREAK_APPROVAL:-}" = "1" ]; then
    # stages.go's own Break line for plan_approval, executed literally:
    # "Apply the planfile after a mutation and expect success; the run must
    # refuse." Expecting success here is the defect this stage exists to
    # catch, so this assertion has to fail.
    [ "$P_GATE_RC" = "0" ] \
      || fail "BREAK_APPROVAL=1: the apply of a plan file approved before the world moved exited $P_GATE_RC, not 0 - the refusal is load-bearing and this expectation is the defect stage 12 catches"
    log "  BREAK_APPROVAL=1: the apply exited 0 with the world moved - stage 12 is NOT load-bearing"
  fi
  [ "$P_GATE_RC" = "3" ] \
    || { printf '%s\n' "$P_GATE_OUT" | tail -40; fail "the apply exited $P_GATE_RC, want 3 - a plan file whose approval no longer covers the run must refuse with its own status"; }
  grep -q "The approved plan no longer matches the live system" <<< "$P_GATE_OUT" \
    || { printf '%s\n' "$P_GATE_OUT" | tail -40; fail "the apply stopped, but not with the named refusal"; }
  # Everything from the refusal's own summary line onward. The fresh plan
  # printed above it also names the moved bucket, so asserting over the whole
  # output would pass on a refusal that named nothing at all.
  P_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$P_GATE_OUT")"
  grep -qF "This apply would do, and the approved plan does not include:" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not classify the difference as a change nobody approved"; }
  grep -qF "$P_MOVED_ADDR" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not name $P_MOVED_ADDR, the change nobody approved"; }
  grep -qF "$BUCKET_NAME" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal names the address but not $BUCKET_NAME, the live object the change was computed against"; }
  grep -qF "Exit status 3" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not tell a pipeline what its exit status means"; }
  if grep -q "Apply complete!" <<< "$P_GATE_OUT"; then
    printf '%s\n' "$P_GATE_OUT" | tail -20; fail "the apply ran anyway after refusing"
  fi
  # Not "no Apply complete line" alone: read the live object the approval
  # was about and confirm the reviewed change did not land.
  P_REVIEWED_TAG="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='Reviewed'].Value | [0]" --output text)"
  [ "$P_REVIEWED_TAG" = "None" ] || [ -z "$P_REVIEWED_TAG" ] \
    || fail "the refused apply still wrote the reviewed change: $USER_NAME carries Reviewed=\"$P_REVIEWED_TAG\""
  printf '%s\n' "$P_REFUSAL" | head -12
  log "  refused by name, exit $P_GATE_RC, nothing applied - and the row it names is exactly the change that appeared after the approval"

  log "=== P4. the inverted control: put the world back, apply the SAME file ==="
  awsl s3api put-bucket-tagging --bucket "$BUCKET_NAME" --tagging "$P_TAGS_BEFORE"
  P_RESTORED="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='hm_team'].Value | [0]" --output text)"
  [ "$P_RESTORED" = "hongbomiao" ] || fail "the out-of-band move was not undone: $BUCKET_NAME's hm_team tag reads \"$P_RESTORED\""
  P_OK_RC=0
  P_OK_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_OK_RC=$?
  [ "$P_OK_RC" = "0" ] \
    || { printf '%s\n' "$P_OK_OUT" | tail -40; fail "the same plan file was refused (exit $P_OK_RC) over a world that had not moved - a comparison that refuses unconditionally is not a check"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_OK_OUT" \
    || { grep -E 'Apply complete' <<< "$P_OK_OUT"; fail "the approved apply did not change exactly the one reviewed resource"; }
  P_LANDED="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='Reviewed'].Value | [0]" --output text)"
  [ "$P_LANDED" = "yes" ] \
    || fail "the approved change did not land: $USER_NAME carries Reviewed=\"$P_LANDED\", want \"yes\""
  log "  the identical artifact applied (0 added, 1 changed, 0 destroyed) and $USER_NAME now carries Reviewed=yes, read via the AWS CLI"

  log "=== P5. put the estate back where the rest of this script expects it ==="
  rm -f "$ESTATE/approved.tfplan"
  perl -0pi -e 's/merge\(local\.common_tags, \{ Reviewed = "yes" \}\)/local.common_tags/' "$ESTATE/main.tofu"
  [ "$(grep -c 'Reviewed = "yes"' "$ESTATE/main.tofu")" = "0" ] \
    || fail "reverting the reviewed edit did not remove the Reviewed key"
  [ "$(grep -cE 'common_tags[[:space:]]+= local\.common_tags' "$ESTATE/main.tofu")" = "2" ] \
    || fail "reverting the reviewed edit did not restore all 2 \"common_tags = local.common_tags\" arguments"
  P_BACK_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; P_BACK_RC=$?
  [ "$P_BACK_RC" -eq 0 ] || { printf '%s\n' "$P_BACK_OUT" | tail -40; fail "the revert apply failed"; }
  P_GONE="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='Reviewed'].Value | [0]" --output text)"
  [ "$P_GONE" = "None" ] || [ -z "$P_GONE" ] \
    || fail "the reviewed tag is still on $USER_NAME after the revert: \"$P_GONE\""
  P_FINAL_OUT="$(plan_into 2>&1)"; P_FINAL_RC=$?
  [ "$P_FINAL_RC" -eq 0 ] || { printf '%s\n' "$P_FINAL_OUT" | tail -40; fail "the post-revert plan exited $P_FINAL_RC"; }
  if grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$P_FINAL_OUT"; then
    grep -E '^  # .+ will be' <<< "$P_FINAL_OUT"; fail "the estate is not converged again after PART P"
  fi
  log "  reverted; the estate is converged again and PART D starts from where it would have"

  log ""
  log "PART P (plan, review, apply): PASS"
  gauntlet_stage plan_approval pass "one argument edited (harbor_iam_user's common_tags gain Reviewed=yes), \"plan -out=approved.tfplan\" wrote a $P_PLAN_BYTES-byte stock-format plan file whose whole change set is one update on $P_REVIEWED_ADDR; the world then moved out of band ($BUCKET_NAME's hm_team tag, through the AWS CLI, never through choudoufu) and \"apply approved.tfplan\" refused with \"The approved plan no longer matches the live system\" at exit 3, classifying the drift under \"This apply would do, and the approved plan does not include:\" and naming both $P_MOVED_ADDR and the live $BUCKET_NAME it was computed against, with \"Exit status 3\" spelled out for a pipeline; nothing was applied - $USER_NAME still carried no Reviewed tag, read back through the AWS CLI rather than from the absence of an \"Apply complete!\" line. Inverted control on the same run (the shape live/smoke/scenarios/apply-what-was-approved.sh reasons out): with the tag put back and nothing else changed, the IDENTICAL file applied - 0 added, 1 changed, 0 destroyed - and $USER_NAME read back with Reviewed=yes, so the refusal is earned by the drift and not handed out to every plan file. BREAK_APPROVAL=1 asserts stage 12's own recorded Break line (apply the planfile after a mutation and expect success) and correctly fails"
  log ""
fi

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  bucket $BUCKET_NAME (module.s3_bucket_hm_harbor), user $USER_NAME (module.harbor_iam_user)"

if [ "${BREAK:-}" = "rename" ]; then
  log "=== D1 (BREAK=rename). rename module harbor_iam_user -> harbor_iam_user_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "harbor_iam_user" {/module "harbor_iam_user_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the BREAK=rename reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  # GitHub issue #403 part 1 re-checked this control's earlier finding of
  # GENUINE cross-run nondeterminism (warning fires or not, create is
  # proposed or not, independently) and could not reproduce it: TEN
  # consecutive isolated BREAK=rename runs against the current main and the
  # current emulator image came back byte-identical (warning=1, create=1
  # every time - see the PR for the exact repro). Reading every map range
  # in internal/live/discovery's decision path (bind, classifyOrphans,
  # indexCountBlocks, typeNames/bindTypeNames) - the sweep the earlier
  # finding suspected but had not done - found none left unsorted where a
  # decision or a message depends on it: every map-ordered accumulation
  # there flows into a sort.Strings/sort.Slice call before anything reads
  # it back, or only sets an idempotent flag. The mechanism that actually
  # fires (internal/live/projection/ownership.go's addressNames, GitHub
  # issue #244) is a single deterministic per-instance comparison with no
  # map in it at all. Left un-narrowed here (aws_iam_user's identity being
  # client-named is what makes BOTH the warning and the create fire
  # together, not two independent coin flips), but the load-bearing check
  # below is unchanged and deliberately still tolerant of either shape:
  # the one invariant that actually matters, and the one this assertion
  # enforces, is the dangerous case that must never happen - the plan must
  # never propose DESTROYING the live user under its old, still-marked
  # address (that would orphan a marked object). Beyond that, this control
  # only needs to prove day2_rename's real checks below are load-bearing -
  # i.e. that skipping the moved block does NOT reproduce their zero-churn
  # result - which it does, stably, by proposing a create.
  [ "$BREAK_PLAN_RC" -eq 0 ] \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the plan exited $BREAK_PLAN_RC - expected a clean exit (see header)"; }
  grep -qE '^  # module\.harbor_iam_user\.aws_iam_user\.hm_harbor_iam_user will be destroyed' <<< "$BREAK_PLAN_OUT" \
    && { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: the plan proposes destroying the live user under its old address - a wrong marker could have been written"; }
  HAS_WARNING=0; grep -qF "Live resource marked for another address" <<< "$BREAK_PLAN_OUT" && HAS_WARNING=1
  HAS_CREATE=0; grep -qE '^  # module\.harbor_iam_user_renamed\.aws_iam_user\.hm_harbor_iam_user will be created' <<< "$BREAK_PLAN_OUT" && HAS_CREATE=1
  if [ "$HAS_WARNING" = "1" ]; then
    grep -qF "module.harbor_iam_user.aws_iam_user.hm_harbor_iam_user" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the warning did not name the IAM user's old address"; }
    grep -qF "module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the warning did not name the renamed address it collides with"; }
  fi
  { [ "$HAS_WARNING" = "1" ] || [ "$HAS_CREATE" = "1" ]; } \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: renaming without a moved block neither warned of the ambiguity nor proposed a create - this stage's check is not load-bearing"; }
  log "  BREAK=rename (this run's shape: warning=$HAS_WARNING create=$HAS_CREATE): never destroys the live user's old marker; proves the moved-block/live-mv checks below are load-bearing - see the PR for the nondeterminism between shapes"
else
  log "=== D1. choudoufu, moved block: module s3_bucket_hm_harbor -> s3_bucket_hm_harbor_renamed ==="
  sed -i.bak 's/module "s3_bucket_hm_harbor" {/module "s3_bucket_hm_harbor_renamed" {/' "$ESTATE/main.tofu"
  sed -i.bak 's/module\.s3_bucket_hm_harbor\.name/module.s3_bucket_hm_harbor_renamed.name/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  cat >> "$ESTATE/main.tofu" <<'EOF'

moved {
  from = module.s3_bucket_hm_harbor
  to   = module.s3_bucket_hm_harbor_renamed
}
EOF
  # Renaming a MODULE CALL (not a resource label) changes the module
  # instance registry .terraform tracks, unlike a plain resource rename -
  # a re-init is required even though the source path itself is unchanged.
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # module\.s3_bucket_hm_harbor_renamed\.aws_s3_bucket\.main will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed bucket"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.s3_bucket_hm_harbor\.aws_s3_bucket\.main" +-> +"module\.s3_bucket_hm_harbor_renamed\.aws_s3_bucket\.main"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the bucket's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  BUCKET_ADDR_D_AFTER="$(awsl s3api get-bucket-tagging --bucket "$BUCKET_NAME" --query "TagSet[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$BUCKET_ADDR_D_AFTER" = "module.s3_bucket_hm_harbor_renamed.aws_s3_bucket.main" ] \
    || fail "the bucket carries tofu-address=$BUCKET_ADDR_D_AFTER after the rename, not module.s3_bucket_hm_harbor_renamed.aws_s3_bucket.main"
  log "  $BUCKET_NAME unchanged, tofu-address now module.s3_bucket_hm_harbor_renamed.aws_s3_bucket.main - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module harbor_iam_user -> harbor_iam_user_renamed, no moved block at all ==="
  sed -i.bak 's/module "harbor_iam_user" {/module "harbor_iam_user_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.harbor_iam_user.aws_iam_user.hm_harbor_iam_user module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.harbor_iam_user.aws_iam_user.hm_harbor_iam_user" -> "module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  USER_ADDR_D_AFTER="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$USER_ADDR_D_AFTER" = "module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user" ] \
    || fail "the user carries tofu-address=$USER_ADDR_D_AFTER after live-mv, not module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user"
  log "  $USER_NAME unchanged, tofu-address now module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.s3_bucket_hm_harbor renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: module.harbor_iam_user renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # NEITHER of this estate's two taggable objects can be force-replaced
  # here, for two DIFFERENT, both real, both verified-with-no-tofu-in-the-
  # loop reasons:
  #   - module.s3_bucket_hm_harbor_renamed's bucket carries
  #     `lifecycle { prevent_destroy = true }` in the real, unmodified
  #     module (see header and D-ORACLE's own comment above) - a
  #     prevented-destroy resource cannot be force-replaced at all, by
  #     design.
  #   - module.harbor_iam_user_renamed's `aws_iam_user_name` argument was
  #     tried FIRST and found NOT to be ForceNew: a direct F-ORACLE run
  #     (stock tofu, no choudoufu, this estate's very first attempt at this
  #     section) showed `aws_iam_user.hm_harbor_iam_user will be updated
  #     in-place` when the name argument changes, not "must be replaced" -
  #     AWS's IAM UpdateUser API genuinely supports renaming a user, unlike
  #     UpdateRole/the policy APIs (see corpus-giantswarm-crossplane's own
  #     PART F, where aws_iam_role's name IS ForceNew - the same-looking
  #     "client-named IAM object" shape does not imply the same ForceNew
  #     behaviour across IAM resource types, and this is the concrete
  #     counter-example).
  #
  # What DOES force-replace, confirmed the same way: the untaggable,
  # composed-of-arguments aws_iam_user_policy child. Its own `name`
  # argument (module source: `name = "S3ReadWritePolicy-
  # ${var.s3_bucket_name}"`) is part of its identity (USERNAME:POLICYNAME,
  # internal/live/identity/table_generated.go), so changing what
  # `s3_bucket_name` evaluates to changes the inline policy's own name and
  # forces a replace at the same declared address - real AWS behaviour
  # (IAM's PutUserPolicy/DeleteUserPolicy have no rename op either). This
  # estate's `s3_bucket_name` module argument is normally a live reference
  # (`module.s3_bucket_hm_harbor_renamed.name`) rather than a literal; it
  # is overwritten here with a literal string instead - the real bucket
  # module and its live object are NEVER touched, only the string this
  # inline policy's own name and JSON body are built from. Neither the
  # user nor the bucket changes at all in this section.
  #
  # THE MARKER-COLLISION SCOPE NOTE. aws_iam_user_policy has no `tags`
  # argument at all - it is untaggable, resolved structurally from its
  # parent's identity rather than by a marker sweep - so corpus-evoteum-
  # modules' and corpus-giantswarm-crossplane's own BREAK=replace collision
  # (a second live object planted with the SAME tofu-address tag) has
  # nothing to plant a tag on here, and this section does not attempt it:
  # those two estates' own BREAK=replace runs already prove that check is
  # load-bearing for the taggable, marker-based shape. What this section
  # asserts instead is the create/destroy mechanics and the record store
  # move, on the shape this estate's real objects can actually exercise.
  #
  # THE create_before_destroy SCOPE NOTE (full reasoning in corpus-sqs-
  # basic's own PART F). OpenTofu core rejects a `lifecycle` block on a
  # `module` call, and patching the vendored harbor_iam_user module's own
  # resources to add create_before_destroy would cross this corpus's own
  # DELTA discipline (see header), so this evidence pass exercises the
  # default destroy-then-create ordering instead.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.harbor_iam_user_renamed.aws_iam_user_policy.hm_aws_iam_user_policy"
  F_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_iam_user_policy/$(record_key "$F_ADDR")"
  F_OLD_POLICY_NAME="$POLICY_NAME"

  log "=== F0. capture the live inline policy and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "${USER_NAME}:${F_OLD_POLICY_NAME}" ] \
    || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not ${USER_NAME}:${F_OLD_POLICY_NAME}"
  awsl iam get-user-policy --user-name "$USER_NAME" --policy-name "$F_OLD_POLICY_NAME" >/dev/null \
    || fail "the inline policy $F_OLD_POLICY_NAME does not exist on $USER_NAME ahead of day2_replace"
  log "  ${USER_NAME}:${F_OLD_POLICY_NAME}, record import_id=$F_OLD_IMPORT_ID"

  log "=== F1. choudoufu: change the s3_bucket_name argument feeding the inline policy's ForceNew name, forcing a replace at the same declared address ==="
  F_NEW_BUCKET_NAME_ARG="${BUCKET_NAME}-policy-v2"
  F_NEW_POLICY_NAME="S3ReadWritePolicy-${F_NEW_BUCKET_NAME_ARG}"
  sed -i.bak "s/s3_bucket_name    = module.s3_bucket_hm_harbor_renamed.name/s3_bucket_name    = \"${F_NEW_BUCKET_NAME_ARG}\"/" "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  grep -q "${F_NEW_BUCKET_NAME_ARG}" "$ESTATE/main.tofu" \
    || fail "changing module.harbor_iam_user_renamed's s3_bucket_name argument did not match - the corpus pin has moved"

  F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
  [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
  grep -qE '^  # module\.harbor_iam_user_renamed\.aws_iam_user_policy\.hm_aws_iam_user_policy must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.harbor_iam_user_renamed's inline policy when its ForceNew name argument changes"; }
  grep -qE '^  # module\.harbor_iam_user_renamed\.aws_iam_user\.hm_harbor_iam_user will be' <<< "$F_PLAN_OUT" \
    && { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the user itself is proposed for a change - this section's whole point is that ONLY the inline policy is touched"; }
  grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add and one destroy at the same address"; }
  log "  choudoufu: exactly one forced replace at the same declared address (module.harbor_iam_user_renamed.aws_iam_user_policy.hm_aws_iam_user_policy), the user itself untouched"

  F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
  [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
  grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add and one destroy"; }

  if F_OLD_STILL="$(awsl iam get-user-policy --user-name "$USER_NAME" --policy-name "$F_OLD_POLICY_NAME" 2>&1)"; then
    echo "$F_OLD_STILL"; fail "$F_OLD_POLICY_NAME still exists on $USER_NAME after the replace - the old object was orphaned, not destroyed"
  fi
  grep -qi 'NoSuchEntity' <<< "$F_OLD_STILL" \
    || { echo "$F_OLD_STILL"; fail "get-user-policy for $F_OLD_POLICY_NAME failed with an unexpected error, not NoSuchEntity - it may still exist"; }
  log "  $F_OLD_POLICY_NAME no longer exists on $USER_NAME (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

  awsl iam get-user-policy --user-name "$USER_NAME" --policy-name "$F_NEW_POLICY_NAME" >/dev/null \
    || fail "the new inline policy $F_NEW_POLICY_NAME does not exist on $USER_NAME after the replace"
  log "  $F_NEW_POLICY_NAME (the new inline policy) exists on $USER_NAME, read via the AWS CLI"

  # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
  # #398-guard shape: a stale record still naming the destroyed object
  # would be exactly the wrong-marker failure that outranks a missing
  # one). The local record file at the SAME address must now hold the
  # NEW inline policy's composite import_id, not the one captured in F0.
  F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_NEW_IMPORT_ID" = "${USER_NAME}:${F_NEW_POLICY_NAME}" ] \
    || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object ${USER_NAME}:${F_NEW_POLICY_NAME} - a stale record still claiming the destroyed object, the #398-guard shape"
  [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
    || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
  log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

  log "=== F2. one more plan: config and reality agree, nothing left to propose ==="
  F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
  [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
  log "  No changes. The replace is complete and invisible to the next plan."

  gauntlet_stage day2_replace pass "choudoufu: changing the s3_bucket_name argument feeding module.harbor_iam_user_renamed's inline policy proposed exactly one replace at the same declared address (1 add, 0 change, 1 destroy; -/+ destroy and then create), applied cleanly, with the user itself completely untouched; the old inline policy ($F_OLD_POLICY_NAME) is confirmed gone from $USER_NAME and the new one ($F_NEW_POLICY_NAME) exists in its place, both via the AWS CLI; the local record store's record at the same address now names the new composite identity, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) confirms both that aws_iam_user's own name argument is NOT ForceNew (updated in-place, not replaced - the reason this section targets the inline policy instead of the user) and that the inline policy itself IS force-replaced the same way. Scope notes: (1) this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see corpus-sqs-basic's own PART F; (2) BREAK=replace's marker-collision control is not exercised here - aws_iam_user_policy is untaggable and resolved structurally, with no marker to plant a collision on, so that control's load-bearing-ness is proven instead by corpus-evoteum-modules and corpus-giantswarm-crossplane's own PART F sections against the taggable shape."
  gauntlet_end_stage
  log ""
  log ""

  log ""

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.harbor_iam_user_renamed
  # (originally module.harbor_iam_user) is bound and converged. It is the
  # one removed here, not module.s3_bucket_hm_harbor_renamed - the real
  # module carries `lifecycle { prevent_destroy = true }` on the bucket
  # (see header and the E-ORACLE comment above), and harbor_iam_user is a
  # leaf nothing else references. Its block holds TWO resources - the
  # taggable aws_iam_user and the untaggable, inline aws_iam_user_policy -
  # so this is a real instance of live/GAUNTLET.md #7's "blocks for
  # untaggable children whose parents stay" concern even though both are
  # removed together: IAM refuses to delete a user with an inline policy
  # still attached, so the destroy order the cloud accepts here is the
  # policy first, then the user - exactly what Terraform's own dependency
  # graph (policy -> user via `user = aws_iam_user....name`) produces.
  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live ids one more time ==="
  E_USER_ADDR_BEFORE="$(awsl iam list-user-tags --user-name "$USER_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || true)"
  [ "$E_USER_ADDR_BEFORE" = "module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user" ] \
    || fail "$USER_NAME does not carry tofu-address=module.harbor_iam_user_renamed.aws_iam_user.hm_harbor_iam_user before day2_remove even starts (got $E_USER_ADDR_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.harbor_iam_user_renamed's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.harbor_iam_user_renamed\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for module.harbor_iam_user_renamed even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.harbor_iam_user_renamed's block ==="
    perl -0pi -e 's/\n# Harbor - IAM user\nmodule "harbor_iam_user_renamed" \{.*?\n\}\n//s' "$ESTATE/main.tofu"
    grep -q 'module "harbor_iam_user_renamed"' "$ESTATE/main.tofu" \
      && fail "removing module.harbor_iam_user_renamed's block did not match - the config has moved"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of module.harbor_iam_user_renamed as a possible rename - this is the honest wall, not a pass"
    fi
    grep -qE '^  # module\.harbor_iam_user_renamed\.aws_iam_user\.hm_harbor_iam_user will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the IAM user when module.harbor_iam_user_renamed's block is deleted"; }
    grep -qE '^  # module\.harbor_iam_user_renamed\.aws_iam_user_policy\.hm_aws_iam_user_policy will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the inline user policy when module.harbor_iam_user_renamed's block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly two destroys"; }
    log "  choudoufu: exactly two destroys (the IAM user and its inline policy), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly two destroys"; }

    if E_STILL="$(awsl iam get-user --user-name "$USER_NAME" 2>&1)"; then
      echo "$E_STILL"; fail "$USER_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  $USER_NAME no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting module.harbor_iam_user_renamed's block proposed exactly two destroys (0 add, 0 change, 2 destroy - the untaggable inline policy and its taggable parent user), applied cleanly (0 added, 0 changed, 2 destroyed) in an order IAM accepted, the user is genuinely gone from the live account (iam get-user on the old name now returns NoSuchEntity, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes exactly two destroys for the same objects"
    log ""

    # ════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8, issue
    # #359/#488)
    # ════════════════════════════════════════════════════════════════════
    #
    # Starts from Part E's real, completed state: the estate plans empty
    # with module.harbor_iam_user_renamed gone (Part E just destroyed this
    # estate's only removable object; the renamed bucket module stays -
    # `lifecycle { prevent_destroy = true }`, see PART F's own header).
    # Neither of this estate's two leaf modules
    # (amazon_s3_bucket/harbor_iam_user) takes a numeric count or for_each
    # argument a caller can vary - both are single, plain module calls -
    # so there is no honest countable knob of this estate's own (issue
    # #488's fallback clause, the same one corpus-iam-read-only-policy's
    # own PART G documents). A NEW, entirely synthetic resource
    # (aws_iam_user.count_test, count_test_block() below) is added here,
    # in its own file, so day2_count's own history is self-contained and
    # never revisits an address any other stage already used - the same
    # discipline live/e2e/reference-ec2-vpc/run.sh's own Part F and
    # corpus-iam-read-only-policy's own PART G use.
    #
    # IDENTITY, established directly against floci with no tofu in the
    # loop before writing a single assertion below: `aws iam create-user
    # --user-name probe --path /example/`, then delete, then create again
    # with the same name and path - the ARN
    # (arn:aws:iam::<account>:user/<path><name>) came back byte-identical
    # both times (deterministic from account+path+name, exactly like
    # aws_iam_policy's ARN - see corpus-iam-read-only-policy's own
    # finding), but UserId (AIDA...) came back DIFFERENT each time - an
    # AWS-assigned identifier, minted fresh on every CreateUser call,
    # independent of name/path. UserId, not ARN, is this section's
    # "genuinely a new object" discriminator below.
    #
    # THE ORACLE REUSES THE IDLE GREENFIELD ACCOUNT ($GREEN_ENDPOINT,
    # $FLOCI_GREEN_NAME - kept alive past PART GREENFIELD for exactly this,
    # see that section's own closing comment), applied for real with plain
    # `tofu`, never the shared $ENDPOINT this estate's own real objects
    # live on - the same discipline reference-ec2-vpc's own B1.7 and
    # corpus-iam-read-only-policy's own G-ORACLE use. A sibling estate's
    # earlier attempt at this shape (PR #502, corpus-iam-policy) applied
    # its stock oracle for real on the SAME account the real leg reads
    # from, and its untagged leftover objects were then picked up by the
    # real leg's own list-based identity lookup, which read
    # tofu-address=None off them - a false failure with nothing wrong in
    # choudoufu. A separate account, never shared with $ENDPOINT, makes
    # that collision structurally impossible rather than merely avoided.
    # count_test's own name ("hm-harbor-count-test-N") collides with
    # nothing PART GREENFIELD's own bucket/user ($BUCKET_NAME/$USER_NAME)
    # ever named, and nothing else touches $GREEN_ENDPOINT again after
    # this section.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of
    # the real checks: after the real scale-down plan, assert the WRONG
    # instance (count_test[0] rather than count_test[1]) was the one
    # destroyed - the Break text in tools/gauntlet/stages.go for
    # day2_count, verbatim: "Expect a different instance to be destroyed;
    # the assertion must fail."

    gauntlet_begin_stage day2_count
    count_test_block() { # $1 = count
      local n="$1"
      cat <<COUNTEOF
resource "aws_iam_user" "count_test" {
  count = $n
  name  = "hm-harbor-count-test-\${count.index}"
  path  = "/example/"
  tags = {
    "hm_environment" = "production"
    "hm_team"        = "hongbomiao"
    "hm_managed_by"  = "opentofu"
  }
}
COUNTEOF
    }
    oracle_count_provider() {
      cat <<EOF
terraform {
  required_version = ">= 1.11"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

EOF
    }
    awslo() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

    log "=== G-ORACLE: stock, create a 2-instance count block, scale it to 1 and back, in the (idle) greenfield account ==="
    PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
    mkdir -p "$PLAIN_ORACLE_COUNT"
    { oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
    ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
    ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
      printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
    grep -qE 'Apply complete! Resources: 2 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
      || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 2 count-test users for the day2_count oracle"; }

    ORACLE_CT0_ARN="$(awslo iam get-user --user-name hm-harbor-count-test-0 --query 'User.Arn' --output text)"
    ORACLE_CT1_ARN="$(awslo iam get-user --user-name hm-harbor-count-test-1 --query 'User.Arn' --output text)"
    [ -n "$ORACLE_CT0_ARN" ] && [ "$ORACLE_CT0_ARN" != "None" ] || fail "no oracle count_test[0] user found by name"
    [ -n "$ORACLE_CT1_ARN" ] && [ "$ORACLE_CT1_ARN" != "None" ] || fail "no oracle count_test[1] user found by name"
    ORACLE_CT0_ID="$(awslo iam get-user --user-name hm-harbor-count-test-0 --query 'User.UserId' --output text)"
    ORACLE_CT1_ID="$(awslo iam get-user --user-name hm-harbor-count-test-1 --query 'User.UserId' --output text)"
    log "  stock: 2 instances created, count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) count_test[1]=$ORACLE_CT1_ARN (id=$ORACLE_CT1_ID)"

    { oracle_count_provider; count_test_block 1; } > "$PLAIN_ORACLE_COUNT/main.tf"
    ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
    [ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
    grep -qE '^  # aws_iam_user\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
    grep -qE '^  # aws_iam_user\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
      && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
    ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
      printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
    ORACLE_CT0_ID_AFTER_DOWN="$(awslo iam get-user --user-name hm-harbor-count-test-0 --query 'User.UserId' --output text 2>/dev/null || true)"
    [ "$ORACLE_CT0_ID_AFTER_DOWN" = "$ORACLE_CT0_ID" ] || fail "stock's surviving count_test[0] changed UserId across the scale-down ($ORACLE_CT0_ID -> $ORACLE_CT0_ID_AFTER_DOWN)"
    if ORACLE_CT1_STILL="$(awslo iam get-user --user-name hm-harbor-count-test-1 2>&1)"; then
      echo "$ORACLE_CT1_STILL"; fail "stock's count_test[1] ($ORACLE_CT1_ARN) still exists after the scale-down destroy"
    fi
    log "  stock: exactly one destroy (count_test[1]=$ORACLE_CT1_ARN), count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) unchanged"

    { oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tf"
    ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
    [ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
    grep -qE '^  # aws_iam_user\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
      || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
    grep -qE '^  # aws_iam_user\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
      && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
      || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
    ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
      printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
    ORACLE_CT1_NEW_ARN="$(awslo iam get-user --user-name hm-harbor-count-test-1 --query 'User.Arn' --output text)"
    [ -n "$ORACLE_CT1_NEW_ARN" ] && [ "$ORACLE_CT1_NEW_ARN" != "None" ] || fail "no oracle count_test[1] user found after the scale-up"
    [ "$ORACLE_CT1_NEW_ARN" = "$ORACLE_CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($ORACLE_CT1_NEW_ARN) differs from its pre-destroy ARN ($ORACLE_CT1_ARN) - unexpected: aws_iam_user's ARN is name/path-derived and should be identical both times"
    ORACLE_CT1_NEW_ID="$(awslo iam get-user --user-name hm-harbor-count-test-1 --query 'User.UserId' --output text)"
    [ "$ORACLE_CT1_NEW_ID" != "$ORACLE_CT1_ID" ] || fail "stock's recreated count_test[1] came back with the SAME UserId it had before being destroyed - the destroy was not real"
    ORACLE_CT0_ID_AFTER_UP="$(awslo iam get-user --user-name hm-harbor-count-test-0 --query 'User.UserId' --output text 2>/dev/null || true)"
    [ "$ORACLE_CT0_ID_AFTER_UP" = "$ORACLE_CT0_ID" ] || fail "stock's count_test[0] changed UserId across the scale-up"
    log "  stock: exactly one create (count_test[1], same ARN $ORACLE_CT1_NEW_ARN - deterministic from name+path - but a NEW UserId $ORACLE_CT1_NEW_ID, was $ORACLE_CT1_ID), count_test[0]=$ORACLE_CT0_ARN (id=$ORACLE_CT0_ID) unchanged throughout"
    gauntlet_end_stage

    gauntlet_begin_stage day2_count
    log "=== G0. choudoufu: add aws_iam_user.count_test, count = 2 ==="
    count_test_block 2 > "$ESTATE/day2_count.tf"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the count-block-add reinit failed"; }
    COUNT_ADD_PLAN_OUT="$(plan_into 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -30; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates"; }
    COUNT_ADD_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -30; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    CT0_ARN="$(awsl iam get-user --user-name hm-harbor-count-test-0 --query 'User.Arn' --output text)"
    CT1_ARN="$(awsl iam get-user --user-name hm-harbor-count-test-1 --query 'User.Arn' --output text)"
    [ -n "$CT0_ARN" ] && [ "$CT0_ARN" != "None" ] || fail "no live count_test[0] user found by name"
    [ -n "$CT1_ARN" ] && [ "$CT1_ARN" != "None" ] || fail "no live count_test[1] user found by name"
    CT0_ADDR_TAG="$(awsl iam list-user-tags --user-name hm-harbor-count-test-0 --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    CT1_ADDR_TAG="$(awsl iam list-user-tags --user-name hm-harbor-count-test-1 --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT0_ADDR_TAG" = 'aws_iam_user.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $CT0_ADDR_TAG, not aws_iam_user.count_test:0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
    [ "$CT1_ADDR_TAG" = 'aws_iam_user.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $CT1_ADDR_TAG, not aws_iam_user.count_test:1"
    # aws_iam_user's ARN is name/path-derived, not server-random (verified
    # directly against the emulator ahead of writing this stage, no tofu in
    # the loop - see the header above), so a destroy+recreate under the
    # same name yields the SAME ARN. UserId, not ARN, is what the
    # "genuinely a new object" checks below compare.
    CT0_ID="$(awsl iam get-user --user-name hm-harbor-count-test-0 --query 'User.UserId' --output text)"
    CT1_ID="$(awsl iam get-user --user-name hm-harbor-count-test-1 --query 'User.UserId' --output text)"
    [ -n "$CT0_ID" ] && [ "$CT0_ID" != "None" ] || fail "live count_test[0] has no UserId"
    [ -n "$CT1_ID" ] && [ "$CT1_ID" != "None" ] || fail "live count_test[1] has no UserId"
    log "  2 instances created: index 0 = $CT0_ARN (tofu-address=$CT0_ADDR_TAG, id=$CT0_ID), index 1 = $CT1_ARN (tofu-address=$CT1_ADDR_TAG, id=$CT1_ID) - read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances plan empty immediately after creation"

    log "=== G1. scale count down: 2 -> 1 ==="
    count_test_block 1 > "$ESTATE/day2_count.tf"
    COUNT_DOWN_PLAN_OUT="$(plan_into 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -30; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
      if grep -qE '^  # aws_iam_user\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT"; then
        fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
      fi
      log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
    else
      grep -qE '^  # aws_iam_user\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
      grep -qE '^  # aws_iam_user\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

      COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -30; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

      CT0_ID_AFTER_DOWN="$(awsl iam get-user --user-name hm-harbor-count-test-0 --query 'User.UserId' --output text 2>/dev/null || true)"
      [ "$CT0_ID_AFTER_DOWN" = "$CT0_ID" ] || fail "count_test[0]'s UserId changed across the scale-down ($CT0_ID -> $CT0_ID_AFTER_DOWN) - it was destroyed and recreated, not left alone"
      if CT1_STILL="$(awsl iam get-user --user-name hm-harbor-count-test-1 2>&1)"; then
        echo "$CT1_STILL"; fail "count_test[1] ($CT1_ARN) still exists in the live account after the scale-down destroy"
      fi
      CT0_ADDR_AFTER_DOWN="$(awsl iam list-user-tags --user-name hm-harbor-count-test-0 --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT0_ADDR_AFTER_DOWN" = 'aws_iam_user.count_test:0' ] || fail "count_test[0]'s tofu-address tag changed across the scale-down: $CT0_ADDR_AFTER_DOWN"
      log "  $CT1_ARN (count_test[1]) no longer exists (NoSuchEntity); $CT0_ARN (count_test[0]) unchanged UserId ($CT0_ID) and marker - all read via the AWS CLI"

      log "=== G2. scale count back up: 1 -> 2 ==="
      count_test_block 2 > "$ESTATE/day2_count.tf"
      COUNT_UP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -30; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qE '^  # aws_iam_user\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
      grep -qE '^  # aws_iam_user\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
      log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

      COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -30; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

      CT1_NEW_ARN="$(awsl iam get-user --user-name hm-harbor-count-test-1 --query 'User.Arn' --output text)"
      [ -n "$CT1_NEW_ARN" ] && [ "$CT1_NEW_ARN" != "None" ] || fail "no live count_test[1] user found by name after the scale-up"
      [ "$CT1_NEW_ARN" = "$CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($CT1_NEW_ARN) differs from its pre-destroy ARN ($CT1_ARN) - unexpected: aws_iam_user's ARN is name/path-derived and should be identical both times"
      CT1_NEW_ID="$(awsl iam get-user --user-name hm-harbor-count-test-1 --query 'User.UserId' --output text)"
      [ "$CT1_NEW_ID" != "$CT1_ID" ] || fail "count_test[1] came back with the SAME UserId ($CT1_ID) it had before being destroyed - the destroy in G1 was not real"
      CT1_NEW_ADDR_TAG="$(awsl iam list-user-tags --user-name hm-harbor-count-test-1 --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT1_NEW_ADDR_TAG" = 'aws_iam_user.count_test:1' ] || fail "the recreated count_test[1] ($CT1_NEW_ARN) carries tofu-address=$CT1_NEW_ADDR_TAG, not aws_iam_user.count_test:1"
      CT0_ID_AFTER_UP="$(awsl iam get-user --user-name hm-harbor-count-test-0 --query 'User.UserId' --output text 2>/dev/null || true)"
      [ "$CT0_ID_AFTER_UP" = "$CT0_ID" ] || fail "count_test[0]'s UserId changed across the scale-up"
      log "  count_test[1] recreated under the same ARN ($CT1_NEW_ARN, deterministic from name+path) but a NEW UserId ($CT1_NEW_ID, was $CT1_ID), tofu-address=$CT1_NEW_ADDR_TAG; count_test[0] ($CT0_ARN, id=$CT0_ID) untouched throughout the down-then-up cycle - all read via the AWS CLI"

      log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(plan_into 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -30; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      gauntlet_stage day2_count pass "choudoufu: scaling aws_iam_user.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), leaving count_test[0]'s live UserId and tofu-address marker unchanged; scaling back from 1 to 2 created exactly count_test[1] under the SAME ARN (deterministic from name+path) but a NEW UserId (0 add, 0 change -> 1 add, 0 change, 0 destroy) while count_test[0] stayed untouched throughout; the next plan is empty; the G-ORACLE stock oracle on the same 2-instance count block, applied for real in the idle greenfield account, shows the identical shape: destroy the higher index only, create the higher index back under the same ARN but a new UserId, the lower index's UserId unchanged both times"
    fi
    gauntlet_end_stage
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end

log "=== PASS: all five stages, real, against hongbo-miao/hongbomiao.com's own ==="
log "=== unmodified Harbor S3+IAM-user leaf modules, .tofu extension throughout ==="
