#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-giantswarm-crossplane recipe; run with: just demo-run corpus-giantswarm-crossplane)
# The sixth OpenTofu-native crossing, from a fresh sourcing search:
# giantswarm/giantswarm-aws-account-prerequisites (live/corpus-manifest.json,
# pinned by tag v8.2.2 and commit), the crossplane/ module - Giant Swarm's
# own customer-facing AWS account prerequisites, genuine .tofu files, and
# the first estate in this lane from a commercial vendor's production
# repository rather than a module registry, a personal monorepo or a
# single-maintainer accelerator. All five stages pass for real against the
# fully unmodified module as of 2026-08-19: test_plan was BLOCKED at exactly
# 2 sites, both unadmitted *_exclusive enforcer types, and #334 ratified both
# rows, so the script's own cut-down control stage retired with the block it
# controlled for. Needs Docker, the AWS CLI, and the real `tofu` binary; runs
# on its own port (4729).
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for giantswarm/giantswarm-aws-account-prerequisites (live/corpus-manifest.json,
# pinned by tag v8.2.2 AND commit f1a7d8d51086824a97749b1a8a13327c6f081f72) -
# the SEVENTH estate in the OpenTofu-native lane, and the first sourced from a
# commercial vendor's own production customer-onboarding repository rather than
# a module registry, a personal monorepo or a single-maintainer accelerator.
#
# WHY THIS IS OPENTOFU-NATIVE, not merely OpenTofu-compatible. Four
# independent pieces of evidence, all checkable at the pinned commit:
#   1. The repository's own README opens "This repository contains OpenTofu
#      configuration to prepare AWS accounts for running Giant Swarm clusters
#      based on Cluster API Provider for AWS (CAPA)", and its directory index
#      is headed "## OpenTofu modules in this repository". It never describes
#      itself as Terraform configuration, and never claims compatibility with
#      both.
#   2. .github/workflows/tofu-checks.yaml ("name: OpenTofu checks") installs
#      OpenTofu via opentofu/setup-opentofu and runs `tofu init`, `tofu
#      validate` and `tofu fmt` - the string "terraform" does not appear in
#      that workflow at all.
#   3. The crossplane/ directory this crossing runs is genuinely
#      .tofu-suffixed - providers.tofu, role.tofu, variables.tofu - the
#      strongest form of self-description in this lane, the same standard
#      corpus-hongbomiao-* meets and which corpus-overture-tiles and
#      corpus-xancloud-iac (both plain .tf) do not.
#   4. It is real and maintained by a real company: Giant Swarm GmbH's managed
#      Kubernetes platform, repository created 2019-10-30, Apache-2.0, 5 forks,
#      a CHANGELOG, SECURITY.md, DCO and CODEOWNERS, semantic-release tags
#      through v8.2.2 (2026-07-16), and named human contributors landing real
#      functional changes as recently as 2026-07-16 (#231, service-quota
#      changes) and 2026-06-02 (#227, EFS CSI driver permissions).
#
# THE SCOPING DECISION. The repository ships six directories. Five are
# excluded, each for a stated reason:
#   - admin-role/, read-role/, capa-controller-role/ are plain .tf, not
#     .tofu, so they carry only the repository-level OpenTofu evidence above
#     and none of the file-level evidence (3). They are also the same
#     resource shape as crossplane/ (an IAM role plus one large managed
#     policy), so crossing them would add coverage of nothing crossplane/
#     does not already reach.
#   - service-quotas/ is aws_servicequotas_service_quota over an
#     account-level quota table - an account singleton with no per-estate
#     object to mark, the shape corpus-xancloud-iac's own sourcing pass
#     already rejected once.
#   - onboarding/ is a bootstrap main.tf whose whole job is to run the other
#     directories.
#   crossplane/ is the one directory that is both genuinely .tofu-suffixed
#   and completely self-contained: no remote state, no EKS cluster, no OIDC
#   provider read off a live cluster (the reason corpus-hongbomiao-harbor
#   had to scope around fifteen sibling IAM modules), no data source that
#   needs anything to exist first. Its only data source is
#   `data "aws_partition" "current"`, which makes no API call at all.
#
# It is copied byte-identical from the pinned commit and called as a child
# module by this script's own root wiring, the same convention
# corpus-hongbomiao-labelbox/-storage/-harbor use - and the repository's own
# README calls these directories "OpenTofu modules", so this is how its
# author describes using them too. This script supplies `installation_name`
# (the module's one required variable) and one entry in the module's own
# documented `additional_policies` map; `gs_user_account`,
# `additional_policies_arns` and the rest keep the module's own defaults.
#
# What this slice contributes that the six earlier OpenTofu-native crossings
# did not: `aws_iam_role_policies_exclusive` and
# `aws_iam_role_policy_attachments_exclusive`, two AWS provider "exclusive
# set manager" resources, plus `templatefile()` over a JSON policy file, a
# map-keyed `for_each` on aws_iam_role_policy, and a `toset()`-keyed
# `for_each` that provably resolves to zero instances.
#
# STAGE 3 WAS BLOCKED AT EXACTLY TWO SITES, AND #334 UNBLOCKED IT. When this
# crossing first landed (0bd3ac80b7), both exclusive types were unadmitted:
# neither has a resource identity schema in aws 6.59.0 and neither had a
# generated table row, so `live-plan` refused each with Rule: unadmitted-type.
# That block was asserted here at an exact site count and an exact type list,
# and a control stage cut exactly those two resource blocks out to prove they
# were the WHOLE of what blocked the estate.
#
# They were. #334 ratified both rows in tools/row-gen/ratified.json and this
# script now drives all five stages against the fully unmodified module. The
# control stage is gone with the block it controlled for; what it proved is
# recorded in live/corpus-crossing-manifest.json rather than re-run every time.
#
# What #334 turned out to be is worth keeping, because the issue guessed
# otherwise: row-gen was never the obstacle. `go run ./tools/row-gen -service
# '(no CFN model)'` proposed both rows all along, client-named, under the same
# rule that produced the aws_vpc_security_group_rules_exclusive row #307
# ratified - "import-grammar precedence: composed_of_arguments, single
# argument, arity confirmed against the example string" - resolving `role_name`
# off the provider's own Import documentation ("% terraform import
# aws_iam_role_policies_exclusive.example MyRole"). Nobody had ratified the
# proposal. The `force_new` difference the issue flagged as a possible gate
# (present on security_group_id, absent on role_name) is not one; that branch
# reads no force_new field.
#
# Both types are UNTAGGABLE - no `tags` argument in the pinned v6.59.0
# Argument Reference - so neither carries a marker of its own, and that is the
# ordinary derived-from-tagged case rather than a gap: `role_name` names the
# tagged aws_iam_role this module also declares, so each one's whole identity
# re-derives from the configuration every run. Stage 3 asserts that by value
# against the live objects' own content, not by a boolean.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                    the unmodified crossplane module - the honest proof the
#                    module is real and buildable, and the source of
#                    genuinely unmarked live infra for stage 2.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                    state; markers re-read through the AWS CLI directly.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan`, assert the
#                    plan is EMPTY, and re-assert identities by value - the
#                    role's marker via the AWS CLI, and the two untaggable
#                    exclusive resources' own live content, which is the only
#                    evidence their re-derived identity found the right
#                    objects.
#   4. TEST APPLY    apply the empty plan; assert a genuine no-op by the
#                    estate's tagged-object count before and after.
#   5. DRIFT AND     mutate one live object out of band, replan, assert the
#      RECONVERGE    diff proposes fixing exactly that one object.
#
# BREAK=1 corrupts stage 2's expected tofu-address, so that assertion is
# proven load-bearing rather than a grep that always matches. It fails fast at
# stage 2, so the later negative controls have their own switches:
# BREAK_STAGE3=1 corrupts stage 3's expected inline-policy name (the one
# assertion that proves aws_iam_role_policies_exclusive resolved to the right
# live role), and BREAK_STAGE5=1 tampers a second object so stage 5's
# exactly-one-object assertion must fail.
#
#   bash live/e2e/corpus-giantswarm-crossplane/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4729, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion. Set to
#                 "rename" to exercise day2_rename's own break control
#                 instead - renaming module crossplane WITHOUT a moved
#                 block, which must not reproduce the real legs' zero-churn
#                 no-op plan. Set to "replace" to exercise day2_replace's
#                 own break control (PART F): manufacture the coexistence a
#                 skipped destroy would leave behind.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control instead.
#   BREAK_COUNT   set to 1 to run day2_count's own break control instead: on
#                 the real scale-down plan, assert the WRONG instance
#                 (count_test[0] rather than count_test[1]) was destroyed.
#                 The stage must report fail.
#   BREAK_STAGE3  set to 1 to corrupt stage 3's expected inline-policy name.
#   BREAK_STAGE5  set to 1 to tamper a second object before stage 5's replan.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/giantswarm-aws-prereqs/crossplane"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4729}"
FLOCI_NAME="choudoufu-corpus-giantswarm-crossplane-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="giantswarm-crossplane-crossing"
INSTALLATION="gsprereqs"

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# separate namespace stock (real `tofu` - see this script's header for why
# plain `terraform` cannot even parse this .tofu-only module) applies the
# identical module into as that stage's own oracle. +1000/+2000 keeps this
# estate's own [main, green, oracle] port triple disjoint from every other
# live/e2e script's own FLOCI_PORT default (all under 4800) and from a
# sibling batch estate's triple one port over - see corpus-ecs-fargate's
# own greenfield header for the real collision +20 hit on a live run.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1000))
FLOCI_GREEN_NAME="choudoufu-corpus-giantswarm-crossplane-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2000))
FLOCI_ORACLE_NAME="choudoufu-corpus-giantswarm-crossplane-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
GREEN="$WORK/green"
GREEN_ORACLE="$WORK/green-oracle"
GREEN_ESTATE_NAME="giantswarm-crossplane-greenfield"
GREEN_INSTALLATION="gsgreen"
ROLE_NAME="giantswarm-${INSTALLATION}-crossplane"
POLICY_ARN="arn:aws:iam::000000000000:policy/giantswarm-${INSTALLATION}-crossplane"
EXTRA_POLICY_NAME="extra-tagging"

# This script runs TWO `tofu init`s (the plain cold-deploy copy and the
# estate copy), each of which would otherwise re-download the ~500MB AWS provider
# into its own scratch directory. Point them all at OpenTofu's own conventional
# shared plugin cache so only the first one can ever pay for a download; an
# operator who already exports TF_PLUGIN_CACHE_DIR keeps theirs.
#
# #339: the shared cache records no checksums, so an init in a directory with
# no .terraform.lock.hcl re-downloads the whole package purely to compute
# them, even when the cache already holds that exact version - measured at
# 320s per init on this estate, twice over. TF_PLUGIN_CACHE_MAY_BREAK_
# DEPENDENCY_LOCK_FILE is OpenTofu's own CLI-config accommodation for exactly
# this (internal/command/cliconfig/cliconfig.go's PluginCacheMayBreakDependency
# LockFile, plumbed to the installer's allowSkippingInstallWithoutHashes):
# with a package already in the global cache, init trusts it instead of
# re-fetching and re-verifying it, and records only the local platform's
# checksum. That is the accepted trade-off for this harness - every directory
# here is a throwaway mktemp copy, never committed, never run on a second
# platform - and it fixes every init in this script generically, not just
# the second one, unlike a per-directory lock-file copy (see #339 for that
# earlier, narrower fix and why this replaces it).
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
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
[ -f "$SRC/role.tofu" ] \
  || fail "$SRC/role.tofu is missing - fetch giantswarm/giantswarm-aws-account-prerequisites at the pin in live/corpus-manifest.json first"
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

# The module ships three .tofu files and two JSON policy documents. Assert the
# extension up front rather than describing it in prose only: if a future pin
# renames them to .tf, the OpenTofu-native evidence (3) in the header stops
# being true and this crossing should say so loudly rather than keep running.
for f in providers.tofu role.tofu variables.tofu; do
  [ -f "$SRC/$f" ] || fail "$SRC/$f is missing - the pinned crossplane module no longer ships genuine .tofu files, which is this crossing's own OpenTofu-native evidence"
done
[ -z "$(find "$SRC" -maxdepth 1 -name '*.tf' -print -quit)" ] \
  || fail "$SRC now contains a .tf file - the pinned crossplane module is no longer .tofu-only"
log "  crossplane module is .tofu-only: providers.tofu role.tofu variables.tofu"

# copy_module <destdir>: the real, unmodified module, .tofu extension and all.
copy_module() { mkdir -p "$1"; cp -R "$SRC" "$1/crossplane"; }

# remove_module_block FILE NAME - day2_remove's edit: delete a whole
# top-level `module "NAME" { ... }` block from a root main.tofu. This
# estate has exactly one module call and no root-level standalone resource
# (see PART D-ORACLE's own header), so the module call is the only
# removable boundary, the same way it is the only renameable one.
remove_module_block() {
  local file="$1" name="$2"
  sed -i.bak "/^module \"$name\" {\$/,/^}\$/d" "$file"
  rm -f "$file.bak"
  grep -q "module \"$name\"" "$file" \
    && fail "removing module \"$name\"'s block did not match in $file - the corpus pin has moved"
}

# write_root <destdir> <installation> <live_block>: this crossing's own root
# wiring. The module call below uses the module's OWN documented variable
# names and nothing else; the provider block is floci's connection flags.
write_root() {
  local dest="$1" installation="$2" live_block="$3"
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

module "crossplane" {
  source            = "./crossplane"
  installation_name = "$installation"

  additional_policies = {
    "$EXTRA_POLICY_NAME" = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":[\"ec2:CreateTags\"],\"Resource\":\"*\"}]}"
  }
}
EOF
}

LIVE_BLOCK='
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'

copy_module "$PLAIN"
write_root "$PLAIN" "$INSTALLATION" ""
log "  crossplane module copied unmodified out of .corpus/giantswarm-aws-prereqs into $PLAIN"

# DELTA: confirm the copy is byte-identical to the pinned commit - the only
# thing this crossing adds is its OWN root file, never an edit to Giant
# Swarm's own module code.
diff -rq "$SRC" "$PLAIN/crossplane" >/dev/null \
  || fail "crossplane/ differs from the pinned commit - this crossing must run the real, unmodified module"
log "  DELTA confirmed: crossplane/ is byte-identical to the pinned commit; only this script's own root file was added"

copy_module "$ESTATE"
write_root "$ESTATE" "$INSTALLATION" "$LIVE_BLOCK"
log "  estate copy written to $ESTATE (stages 2-3: choudoufu, live block added)"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (iam) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified module) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 6 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 6 resource instances"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

# aws_iam_role_policy_attachment.additional_policy_attachments is a
# toset()-keyed for_each over the module's own empty-by-default
# additional_policies_arns, so it provably resolves to zero instances. Assert
# that rather than assume it: if a future pin gives it a default, the 6-count
# above moves and the reason should be visible here.
grep -qE 'additional_policy_attachments' <<< "$COLD_OUT" \
  && fail "aws_iam_role_policy_attachment.additional_policy_attachments produced an instance - the module's additional_policies_arns default is no longer empty"
log "  confirmed: the toset()-keyed for_each on additional_policy_attachments resolves to zero instances"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
gauntlet_stage cold_deploy pass "6 resource instances added, 0 already tofu-estate-marked before migration"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13) - two MORE, fresh floci
# containers, neither reusing a single object stage 1's plain apply created.
# choudoufu applies the identical, unmodified module directly with a live
# block from the start, no migration, no state file ever existing; the
# estate's own oracle is stock `tofu` applying the SAME module fresh in a
# third, independent namespace, compared structurally via the AWS CLI on
# both endpoints, never through tofu state.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage greenfield
log "=== G0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"iam"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"iam"' <<< "${GH:-}" || fail "floci did not come up healthy (iam) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create.
# Same fix, same precedent as corpus-alb-complete's own 898091b8f2.
GREEN_LIVE_BLOCK='
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      no_source_create = "create"
    }
  }'
copy_module "$GREEN"
write_root "$GREEN" "$GREEN_INSTALLATION" "$GREEN_LIVE_BLOCK"
copy_module "$GREEN_ORACLE"
write_root "$GREEN_ORACLE" "$GREEN_INSTALLATION" ""

GREEN_ROLE_NAME="giantswarm-${GREEN_INSTALLATION}-crossplane"
GREEN_POLICY_ARN="arn:aws:iam::000000000000:policy/giantswarm-${GREEN_INSTALLATION}-crossplane"

log "=== G1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -60; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 6 added, 0 changed, 0 destroyed' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 6 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }

log "=== G2. the role's marker, read through the AWS CLI directly ==="
GREEN_ROLE_ADDR="$(awsg iam list-role-tags --role-name "$GREEN_ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_ROLE_ADDR" = "module.crossplane.aws_iam_role.giantswarm_crossplane_role" ] || fail "the greenfield role carries tofu-address=$GREEN_ROLE_ADDR, not module.crossplane.aws_iam_role.giantswarm_crossplane_role"
GREEN_ROLE_ESTATE="$(awsg iam list-role-tags --role-name "$GREEN_ROLE_NAME" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_ROLE_ESTATE" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield role carries tofu-estate=$GREEN_ROLE_ESTATE, not $GREEN_ESTATE_NAME"
log "  $GREEN_ROLE_NAME carries tofu-address=$GREEN_ROLE_ADDR tofu-estate=$GREEN_ROLE_ESTATE - read via the AWS CLI, not choudoufu's own report"

log "=== G3. the record store holds every instance, including the 4 untaggable ones (#364 A2) ==="
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" = "6" ] || fail "expected 6 records under the local record store after the greenfield apply (one per managed instance), found $GREEN_RECORD_FILES"
log "  6 records persisted, one per managed instance, read directly off the local record store"

log "=== G4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== G5. stock oracle - the identical module applied fresh in its own namespace ==="
( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -60; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 6 added, 0 changed, 0 destroyed' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 6 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

log "=== G6. object-by-object comparison, via the AWS CLI on both endpoints, marker tags never compared ==="
crossplane_shape() { # $1 = endpoint $2 = role name $3 = policy arn - a
                      # normalised structural fact sheet, read via the AWS
                      # CLI, never through tofu state.
  local ep="$1" role="$2" parn="$3"
  aws --endpoint-url "$ep" --region "$REGION" iam get-role --role-name "$role" \
    --query "Role.Description" --output text 2>/dev/null | sed 's/^/role_description=/'
  aws --endpoint-url "$ep" --region "$REGION" iam list-attached-role-policies --role-name "$role" \
    --query "length(AttachedPolicies)" --output text 2>/dev/null | sed 's/^/attached_policy_count=/'
  aws --endpoint-url "$ep" --region "$REGION" iam list-role-policies --role-name "$role" \
    --query "sort(PolicyNames)" --output text 2>/dev/null | tr '\t' ',' | sed 's/^/inline_policy_names_sorted=/'
  aws --endpoint-url "$ep" --region "$REGION" iam get-policy --policy-arn "$parn" \
    --query "Policy.Description" --output text 2>/dev/null | sed 's/^/policy_description=/'
}
GREEN_SHAPE="$(crossplane_shape "$GREEN_ENDPOINT" "$GREEN_ROLE_NAME" "$GREEN_POLICY_ARN" | sort)"
ORACLE_SHAPE="$(crossplane_shape "$ORACLE_ENDPOINT" "$GREEN_ROLE_NAME" "$GREEN_POLICY_ARN" | sort)"
if [ "$GREEN_SHAPE" != "$ORACLE_SHAPE" ]; then
  diff <(printf '%s\n' "$GREEN_SHAPE") <(printf '%s\n' "$ORACLE_SHAPE") || true
  fail "the greenfield estate's object inventory does not match stock's cold deploy, object by object, in its own namespace"
fi
log "  object-by-object match: role description, attached-policy count, sorted inline-policy names, and the managed policy's description - identical between the greenfield estate and stock's cold deploy in its own namespace, marker tags never part of the comparison"

gauntlet_stage greenfield pass "6 resources from nothing (role, managed policy, 4 untaggable), role marker verified via the AWS CLI, 6 records in the local record store (#364 A2, one per managed instance), replan empty, stock oracle in its own namespace matches structurally on the role and the managed policy"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# This estate has exactly ONE module call ("crossplane"), unlike every
# other OpenTofu-native crossing in this campaign, and no root-level
# standalone resource of its own (see header's scoping decision - the
# other five real directories in this repository are excluded). Both
# taggable objects (the IAM role and the managed policy) live inside that
# one module, whose source stays byte-identical to the pinned commit
# throughout (DELTA discipline), so the only renameable boundary at all is
# the module call itself. Both legs therefore rename the SAME module
# SEQUENTIALLY: a `moved` block relocates module.crossplane ->
# .crossplane_renamed first (D1, zero churn across both taggable objects
# in one operation), then "choudoufu live-mv" relocates
# module.crossplane_renamed -> .crossplane_final with no moved block at
# all (D2) - two independent renames of the one module boundary this
# estate has, each proving its own mechanism the same way every other
# estate's two legs prove theirs on two different objects. The stock
# oracle (real tofu - stock terraform cannot see this .tofu-only estate at
# all, see header) runs the same two renames, chained through moved blocks
# only, on a copy of cold_deploy's own state - before choudoufu or
# live-import ever touch these objects.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE: stock tofu, the same chained module rename through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/module "crossplane" {/module "crossplane_final" {/' "$PLAIN_ORACLE/main.tofu"
rm -f "$PLAIN_ORACLE/main.tofu.bak"
cat >> "$PLAIN_ORACLE/main.tofu" <<'EOF'

moved {
  from = module.crossplane
  to   = module.crossplane_renamed
}

moved {
  from = module.crossplane_renamed
  to   = module.crossplane_final
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
log "  stock: zero churn on cold_deploy's own state - the chained move reports only the moves, no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7): same principle as the
# rename oracle above - a SEPARATE copy of cold_deploy's own state,
# unrenamed, so this removal has nothing to do with the rename this script
# also exercises. module.crossplane is the only removable boundary this
# estate has (same as Part D-ORACLE's own header on the only renameable
# one), so its whole block is what gets deleted - a stronger test than a
# single object: two taggable resources plus four untaggable, composed-of-
# arguments ones (aws_iam_role_policy, aws_iam_role_policies_exclusive,
# aws_iam_role_policy_attachments_exclusive, aws_iam_role_policy_attachment)
# all destroyed together.
gauntlet_begin_stage day2_remove
log "=== D-ORACLE (day2_remove): stock tofu, delete module.crossplane's block on cold_deploy's own state ==="
PLAIN_ORACLE_REMOVE="$WORK/plain-oracle-remove"
cp -r "$PLAIN" "$PLAIN_ORACLE_REMOVE"
remove_module_block "$PLAIN_ORACLE_REMOVE/main.tofu" "crossplane"
( cd "$PLAIN_ORACLE_REMOVE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REMOVE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REMOVE" && tofu plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
REMOVE_ORACLE_CHANGES="$(grep -oE '^  # \S+ will be (destroyed|created|updated in-place)' <<< "$REMOVE_ORACLE_PLAN_OUT" | sed -E 's/^  # //' | sort -u)"
REMOVE_ORACLE_N="$(printf '%s\n' "$REMOVE_ORACLE_CHANGES" | grep -c . || true)"
[ "$REMOVE_ORACLE_N" -ge 1 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -30; fail "stock's day2_remove oracle proposes no resource action at all when module.crossplane's block is removed"; }
grep -qF "module.crossplane.aws_iam_role.giantswarm_crossplane_role will be destroyed" <<< "$REMOVE_ORACLE_CHANGES" \
  || { printf '%s\n' "$REMOVE_ORACLE_CHANGES"; fail "stock's day2_remove oracle does not destroy the role itself"; }
log "  stock: $REMOVE_ORACLE_N resource action(s) removing module.crossplane's block:"
printf '%s\n' "$REMOVE_ORACLE_CHANGES" | while read -r line; do log "    $line"; done
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock oracle (day2_replace, live/GAUNTLET.md #9):
# "Stock's replace of the same resource leaves the same single object." A
# THIRD separate copy of cold_deploy's own state ($PLAIN), unrenamed and
# unremoved, so this oracle has nothing to do with the rename/remove
# oracles above. module.crossplane's `installation_name` variable feeds
# BOTH taggable objects' own `name` argument (role.tofu: "giantswarm-
# ${var.installation_name}-crossplane" for the role AND the managed policy
# - the module exposes no narrower override), so changing it - a real,
# upstream-declared ForceNew argument on both aws_iam_role and
# aws_iam_policy (IAM has no RenameRole/RenamePolicy API) - forces stock to
# replace both at the SAME declared addresses, cascading into their
# untaggable dependents (the policy attachment and the "extra-tagging"
# inline policy, both keyed on the role/policy identity that just
# changed). PLAN ONLY, never applied - same convention as the rename/
# remove oracles above: this copy shares floci's account with $ESTATE.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_replace
log "=== F-ORACLE: stock tofu, force-replace module.crossplane's role+policy via installation_name, on cold_deploy's own state ==="
PLAIN_ORACLE_REPLACE="$WORK/plain-oracle-replace"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE"
rm -rf "$PLAIN_ORACLE_REPLACE/.terraform"
sed -i.bak "s/installation_name = \"$INSTALLATION\"/installation_name = \"${INSTALLATION}-v2\"/" "$PLAIN_ORACLE_REPLACE/main.tofu"
rm -f "$PLAIN_ORACLE_REPLACE/main.tofu.bak"
grep -q "${INSTALLATION}-v2" "$PLAIN_ORACLE_REPLACE/main.tofu" \
  || fail "changing module.crossplane's installation_name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE" && tofu plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.crossplane\.aws_iam_role\.giantswarm_crossplane_role must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.crossplane's role when installation_name changes"; }
grep -qE '^  # module\.crossplane\.aws_iam_policy\.giantswarm_crossplane_policy must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.crossplane's policy when installation_name changes"; }
REPLACE_ORACLE_ADD="$(grep -oE 'Plan: [0-9]+ to add' <<< "$REPLACE_ORACLE_PLAN_OUT" | grep -oE '[0-9]+')"
REPLACE_ORACLE_DESTROY="$(grep -oE '[0-9]+ to destroy\.' <<< "$REPLACE_ORACLE_PLAN_OUT" | grep -oE '^[0-9]+')"
[ -n "$REPLACE_ORACLE_ADD" ] && [ "$REPLACE_ORACLE_ADD" = "$REPLACE_ORACLE_DESTROY" ] && [ "$REPLACE_ORACLE_ADD" -ge 2 ] \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -15; fail "stock's replace plan does not show an equal, at-least-2 add/destroy cascade (role+policy at minimum)"; }
log "  stock: $REPLACE_ORACLE_ADD to add / $REPLACE_ORACLE_DESTROY to destroy, role and policy both replaced at their same declared addresses, on the state cold_deploy produced - plan only, not applied"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
log "=== STAGE 2: choudoufu live-import ==="

# #339's fix: TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE, exported near
# the top of this script alongside TF_PLUGIN_CACHE_DIR, replaces the
# lock-file-copy this stage used to do by hand (see #339's history for the
# per-directory hack it retires). That copy only fixed THIS directory pair,
# in THIS script - the env var fixes the same defect for every init in every
# script sharing the cache, including a cold script's very first init against
# an already-warm cache. The estate's own init still runs below, against its
# own configuration, and still has to succeed.
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "2 of 6 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 6 as eligible (the IAM role and the managed policy)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
# Four untaggable, not two: the inline role policy and the managed-policy
# attachment, plus the two *_exclusive enforcers, which #334 moved out of
# UNADMITTED_TYPE and into UNTAGGABLE by ratifying their rows. Neither type
# has a `tags` argument, so being admitted does not make either stampable -
# their identity re-derives from role_name instead. Assert the absence of the
# UNADMITTED_TYPE bucket outright, so a regression that un-admits either type
# is loud here rather than only at stage 3.
grep -qF "UNTAGGABLE (4)" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "expected exactly 4 UNTAGGABLE resources (the inline role policy, the managed-policy attachment, and the two *_exclusive enforcers #334 admitted)"; }
grep -qE '^UNADMITTED_TYPE \(' <<< "$IMPORT_OUT" \
  && { printf '%s\n' "$IMPORT_OUT"; fail "live-import still reports an UNADMITTED_TYPE bucket - #334's ratified rows are not in this binary's table"; }
# The two eligible instances land in DRIFTED, not VERIFIED, and the reason is
# benign and deterministic: the AWS provider's own deprecated shadow
# attributes on aws_iam_role (inline_policy, managed_policy_arns) and
# aws_iam_policy (attachment_count) reflect sibling resources created after
# the state snapshot was taken. Same class as the finding already recorded in
# corpus-hongbomiao-labelbox's manifest entry. Asserted so a change of class
# is visible rather than silently absorbed.
grep -qF "DRIFTED (2)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 2 DRIFTED resources (the role's inline_policy/managed_policy_arns and the policy's attachment_count shadow attributes)"
# Bucket HEADINGS only: the summary line itself reads "(VERIFIED or DRIFTED)",
# so a bare grep for the word matches every run and proves nothing.
grep -qE '^(VERIFIED|FAILED) \(' <<< "$IMPORT_OUT" \
  && fail "live-import reported a VERIFIED or FAILED bucket this crossing does not expect - re-read the whole output before changing the assertions above"
log "  2 of 6 verified against the live system (both DRIFTED on benign shadow attributes); 4 UNTAGGABLE, no UNADMITTED_TYPE; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 4 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 2 of 6 resources cleanly"; }
log "  2 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_ROLE_ADDR="module.crossplane.aws_iam_role.giantswarm_crossplane_role"
WANT_POLICY_ADDR="module.crossplane.aws_iam_policy.giantswarm_crossplane_policy"
if [ "${BREAK:-}" = "1" ]; then
  WANT_POLICY_ADDR="module.crossplane.aws_iam_policy.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the managed policy on purpose - this check must fail"
fi

GOT_ROLE_ADDR="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ROLE_ADDR" = "$WANT_ROLE_ADDR" ] || fail "the IAM role carries tofu-address=$GOT_ROLE_ADDR, not $WANT_ROLE_ADDR"
GOT_ROLE_ESTATE="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_ROLE_ESTATE" = "$ESTATE_NAME" ] || fail "the IAM role carries tofu-estate=$GOT_ROLE_ESTATE, not $ESTATE_NAME"
log "  role   $ROLE_NAME -> tofu-address=$GOT_ROLE_ADDR tofu-estate=$GOT_ROLE_ESTATE"

GOT_POLICY_ADDR="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_POLICY_ADDR" = "$WANT_POLICY_ADDR" ] || fail "the managed policy carries tofu-address=$GOT_POLICY_ADDR, not $WANT_POLICY_ADDR"
log "  policy $POLICY_ARN -> tofu-address=$GOT_POLICY_ADDR"

# The module's own two tags must survive the stamp untouched - a marker that
# replaces rather than merges an existing tag set is the exact defect
# TestMarkerSurvivesIncrementalTagUpdate pins, and this estate happens to be
# a second, real instance of the same shape.
GOT_INSTALLATION="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='installation'].Value | [0]" --output text)"
[ "$GOT_INSTALLATION" = "$INSTALLATION" ] \
  || fail "the module's own installation tag is \"$GOT_INSTALLATION\" after stamping, not \"$INSTALLATION\" - the marker replaced the tag set instead of merging into it"
log "  the module's own installation=$GOT_INSTALLATION tag survived the stamp"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the policy's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
gauntlet_stage migrate pass "2 of 6 stamped (role, managed policy), 4 untaggable skipped, module's own tags survived the stamp"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, EMPTY + identities by value
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #|^Error: ' <<< "$PLAN_OUT"; fail "live-plan is not empty"; }

# #334's own regression guard, stated as an absence. If either ratified row
# ever leaves internal/live/identity's table, this is the diagnostic that
# comes back, and the empty-plan check above would already have caught it -
# but naming the rule makes the failure say WHY rather than only that.
grep -qF "Rule: unadmitted-type." <<< "$PLAN_OUT" \
  && { grep -E '^Error: ' <<< "$PLAN_OUT"; fail "an unadmitted-type refusal is back - #334's ratified rows are not in this binary's table"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"
log "  no unadmitted-type refusal anywhere in the plan (#334)"

# ── identities, re-asserted BY VALUE against the live objects ──────────────
# The state file is gone, so every answer below can only have come from the
# live system: from the marker for the two tagged resources, and from the
# configuration's own re-derivation for the four untaggable ones.
GOT_ROLE_ADDR2="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ROLE_ADDR2" = "$WANT_ROLE_ADDR" ] \
  || fail "the role's tofu-address changed across the empty plan: $WANT_ROLE_ADDR -> $GOT_ROLE_ADDR2"
GOT_POLICY_ADDR2="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_POLICY_ADDR2" = "$WANT_POLICY_ADDR" ] \
  || fail "the managed policy's tofu-address changed across the empty plan: $WANT_POLICY_ADDR -> $GOT_POLICY_ADDR2"
log "  role and managed-policy tofu-address unchanged across the empty plan"

# The two *_exclusive resources #334 admitted carry NO marker - both are
# untaggable - so there is no tag to re-read and the only honest assertion is
# the live content each one enforces, read directly. Their rendered identity
# (identity.DefaultTable's ROLE_NAME import syntax) is the bare role name, so
# if the derivation had produced anything else, live-plan would have imported
# some other role's policy set and the plan above could not have been empty
# with these values still correct.
WANT_INLINE="$EXTRA_POLICY_NAME"
if [ "${BREAK_STAGE3:-}" = "1" ]; then
  WANT_INLINE="a-policy-name-this-role-does-not-have"
  log "  BREAK_STAGE3=1: expecting a wrong inline-policy name on purpose - this check must fail"
fi
GOT_INLINE="$(awsl iam list-role-policies --role-name "$ROLE_NAME" --query 'PolicyNames' --output text)"
[ "$GOT_INLINE" = "$WANT_INLINE" ] \
  || fail "aws_iam_role_policies_exclusive's role ($ROLE_NAME) carries inline policies [$GOT_INLINE], not [$WANT_INLINE] - its re-derived identity did not name the live object the configuration means"
if [ "${BREAK_STAGE3:-}" = "1" ]; then
  fail "BREAK_STAGE3=1: the role's real inline-policy set matched the WRONG expected value above - stage 3's identity assertion is not load-bearing"
fi
GOT_ATTACHED="$(awsl iam list-attached-role-policies --role-name "$ROLE_NAME" --query 'AttachedPolicies[].PolicyArn' --output text)"
[ "$GOT_ATTACHED" = "$POLICY_ARN" ] \
  || fail "aws_iam_role_policy_attachments_exclusive's role ($ROLE_NAME) carries attached policies [$GOT_ATTACHED], not [$POLICY_ARN]"
log "  identity re-check, by value, on the two untaggable *_exclusive resources:"
log "    aws_iam_role_policies_exclusive        -> role_name=$ROLE_NAME, inline policies [$GOT_INLINE]"
log "    aws_iam_role_policy_attachments_exclusive -> role_name=$ROLE_NAME, attached [$GOT_ATTACHED]"

log ""
log "STAGE 3 (test plan): PASS"
gauntlet_stage test_plan pass "live-plan empty, role/policy tofu-address unchanged, both *_exclusive resources re-derived by value"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$BEFORE_N" = "2" ] \
  || fail "expected 2 objects carrying tofu-estate=$ESTATE_NAME before the no-op apply (the role and the managed policy), got $BEFORE_N"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply"

# An exclusive-set enforcer that had resolved to the WRONG role would have
# reconciled that role's policies during this apply, so re-read both live sets
# once more rather than trusting the 0-changed line alone.
STILL_INLINE="$(awsl iam list-role-policies --role-name "$ROLE_NAME" --query 'PolicyNames' --output text)"
[ "$STILL_INLINE" = "$EXTRA_POLICY_NAME" ] \
  || fail "the role's inline policy set is [$STILL_INLINE] after the no-op apply, not [$EXTRA_POLICY_NAME]"
STILL_ATTACHED="$(awsl iam list-attached-role-policies --role-name "$ROLE_NAME" --query 'AttachedPolicies[].PolicyArn' --output text)"
[ "$STILL_ATTACHED" = "$POLICY_ARN" ] \
  || fail "the role's attached policy set is [$STILL_ATTACHED] after the no-op apply, not [$POLICY_ARN]"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
log "  both exclusive sets unchanged across the apply - neither enforcer touched another role"

log ""
log "STAGE 4 (test apply): PASS"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); object count unchanged at $BEFORE_N, both exclusive sets unchanged"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object's tag out of band) ==="

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  awsl iam tag-policy --policy-arn "$POLICY_ARN" --tags Key=installation,Value=tampered-by-BREAK >/dev/null
  log "  BREAK_STAGE5=1: also tampered the managed policy's installation tag - stage 5"
  log "                  must now see TWO drifted objects and fail the single-object assertion"
fi

awsl iam tag-role --role-name "$ROLE_NAME" --tags Key=installation,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='installation'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $ROLE_NAME's installation tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "                  one - the single-object assertion and reconverge apply below are skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "module.crossplane.aws_iam_role.giantswarm_crossplane_role" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the IAM role"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='installation'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "$INSTALLATION" ] \
    || fail "the role's installation tag is \"$FIXED_VALUE\" after reconverging, not \"$INSTALLATION\""
  log "  reconverged: $ROLE_NAME's installation tag is back to \"$INSTALLATION\", read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
gauntlet_stage drift_reconverge pass "role's installation tag tampered, exactly the IAM role proposed and reconciled, apply changed 1, tag reads back as configured"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  role $ROLE_NAME, policy $POLICY_ARN (both module.crossplane)"

if [ "${BREAK:-}" = "rename" ]; then
  log "=== D1 (BREAK=rename). rename module crossplane -> crossplane_broken WITHOUT a moved block ==="
  sed -i.bak 's/module "crossplane" {/module "crossplane_broken" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the BREAK=rename reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  # Verified directly: this module's two client-named taggable objects take
  # genuinely different paths under a bare rename, and neither reproduces
  # the real legs' zero-churn result. The role shows corpus-eks-basic's own
  # textbook Break shape - "[UNOWNED]" naming the old, still-marked address,
  # then a plain destroy of module.crossplane.aws_iam_role.* paired with a
  # create of module.crossplane_broken.aws_iam_role.* (an "Owned and
  # undeclared" object, correctly destroyed rather than silently adopted
  # under the new name). The managed policy takes an entirely different
  # route: "[NEEDS_DISCOVERY]" because aws_iam_policy's import identity is
  # the whole ARN as one opaque provider-required string, not one this
  # stateless walk resolves the old marked object through here, so it is
  # simply proposed as a fresh create with no destroy of its own old address
  # at all - never treated as a collision. A dependent untaggable child
  # (aws_iam_role_policy_attachment) is also proposed as a create, since it
  # needs both to exist first. Net: "Plan: 3 to add, 1 to change, 1 to
  # destroy." This assertion does not hard-code that exact multi-resource
  # shape (fragile against unrelated drift in this module's untaggable
  # children); it proves the control is load-bearing the same way the
  # tolerant checks in corpus-hongbomiao-harbor/-storage do, by requiring
  # only that the result differs from the real legs' zero-churn no-op.
  [ "$BREAK_PLAN_RC" -eq 0 ] \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the plan exited $BREAK_PLAN_RC - see header"; }
  grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$BREAK_PLAN_OUT" \
    && { printf '%s\n' "$BREAK_PLAN_OUT" | tail -10; fail "BREAK=rename: renaming without a moved block reproduced the real legs' zero-churn no-op plan - this stage's check is not load-bearing"; }
  log "  BREAK=rename: the plan is not the real legs' zero-churn no-op (see the PR for the exact shape observed) - proves the moved-block/live-mv checks below are load-bearing"
else
  log "=== D1. choudoufu, moved block: module crossplane -> crossplane_renamed ==="
  sed -i.bak 's/module "crossplane" {/module "crossplane_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  cat >> "$ESTATE/main.tofu" <<'EOF'

moved {
  from = module.crossplane
  to   = module.crossplane_renamed
}
EOF
  # Renaming a MODULE CALL (not a resource label) changes the module
  # instance registry .terraform tracks, unlike a plain resource rename -
  # a re-init is required even though the source path itself is unchanged.
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  # FIXED by gauntlet:sweep-moved-alias (internal/live/discovery/recordorphan_read.go):
  # this leg regressed after 610511fb73 (the record-orphan-read sweep, #405's
  # day2_remove fix) - it used to be zero churn (0 add, 2 change, 0 destroy -
  # the tagged role and policy alone) and started proposing destroying the
  # four untaggable, composed-of-arguments children under the OLD
  # module.crossplane address (aws_iam_role_policies_exclusive,
  # aws_iam_role_policy["extra-tagging"], aws_iam_role_policy_attachment,
  # aws_iam_role_policy_attachments_exclusive) while the role and policy
  # moved as "updated in-place" same as before. Root cause, read directly
  # off recordorphan_read.go with no tofu in the loop: recordOrphanReadSweep
  # had its own rename-safety check (the `pending` map, built from
  # res.Unbound) but that check only recognized "a declared instance of the
  # SAME address is unclaimed" - it never consulted moved.Aliases /
  # moved.Honoured(req.Config) the way the marker path already does
  # (discovery.go's declared.alias* methods, threaded through movedStmts).
  # So the moment this moved block relocated module.crossplane's children,
  # the record-orphan-read leg read their OLD-address kind=identity
  # records as genuinely undeclared and proposed destroying them, even
  # though the SAME instances stayed declared one module level over and a
  # `moved` block explicitly said so - the exact HANDOFF row 2 shape ("the
  # plans differ"), introduced BY the row-2 fix for day2_remove rather
  # than fixed by it. live-mv did not hit this (RecordStore.MoveRecord
  # re-keys the record store directly, 8bd0d47e4e); only a bare HCL
  # `moved` block did. gauntlet:sweep-moved-alias closes it generically:
  # before classifying a record's address as an orphan, recordOrphanReadSweep
  # now folds moved.Aliases(movedStmts, r.Addr) for every currently-declared
  # resolution into the same "already accounted for" set it already
  # withholds on - mirroring declaredInstances' own marker-path alias index
  # and builder.locatedIdentityWithAliases' record-rung lookup (c5f530c48d),
  # rather than a third, type-specific rule. This plan is now zero churn
  # again, confirmed by the assertions below. See D3 further down for the
  # SECOND, still-open defect this fix does not reach.
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename now proposes destroying the four untaggable *_exclusive/role_policy/role_policy_attachment children under the OLD module.crossplane address instead of zero churn - a regression from 610511fb73's record-orphan-read sweep, which has no moved-block awareness (see the comment immediately above this assertion for the exact code-level root cause); day2_remove's own post-fix status for this estate could not be re-measured this run because of it"; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" = "2" ] \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename plan proposes $N_CHANGED_D1 in-place changes, not 2 (the role and the managed policy)"; }
  grep -qF "Plan: 0 to add, 2 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary is not exactly 2 in-place changes"; }
  grep -qE '~ +"tofu-address" += +"module\.crossplane\.aws_iam_role\.giantswarm_crossplane_role" +-> +"module\.crossplane_renamed\.aws_iam_role\.giantswarm_crossplane_role"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the role's tofu-address marker being rewritten from the old address to the new one"; }
  grep -qE '~ +"tofu-address" += +"module\.crossplane\.aws_iam_policy\.giantswarm_crossplane_policy" +-> +"module\.crossplane_renamed\.aws_iam_policy\.giantswarm_crossplane_policy"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the policy's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, 2 in-place tags updates (role and policy) - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 2 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly 2 in-place changes"; }

  ROLE_ADDR_D_AFTER="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ROLE_ADDR_D_AFTER" = "module.crossplane_renamed.aws_iam_role.giantswarm_crossplane_role" ] \
    || fail "the role carries tofu-address=$ROLE_ADDR_D_AFTER after the rename, not module.crossplane_renamed.aws_iam_role.giantswarm_crossplane_role"
  POLICY_ADDR_D_AFTER="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$POLICY_ADDR_D_AFTER" = "module.crossplane_renamed.aws_iam_policy.giantswarm_crossplane_policy" ] \
    || fail "the policy carries tofu-address=$POLICY_ADDR_D_AFTER after the rename, not module.crossplane_renamed.aws_iam_policy.giantswarm_crossplane_policy"
  log "  $ROLE_NAME and $POLICY_ARN unchanged, tofu-address now under module.crossplane_renamed - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module crossplane_renamed -> crossplane_final, no moved block at all ==="
  sed -i.bak 's/module "crossplane_renamed" {/module "crossplane_final" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
  # Both taggable objects live under the SAME renamed module boundary, so
  # live-mv is invoked once per object - it rewrites one live resource's own
  # marker per call, the same as every other estate's live-mv leg.
  MV_ROLE_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.crossplane_renamed.aws_iam_role.giantswarm_crossplane_role module.crossplane_final.aws_iam_role.giantswarm_crossplane_role 2>&1)"; MV_ROLE_RC=$?
  [ "$MV_ROLE_RC" -eq 0 ] || { printf '%s\n' "$MV_ROLE_OUT" | tail -30; fail "choudoufu live-mv (role) exited $MV_ROLE_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_ROLE_OUT" \
    || { printf '%s\n' "$MV_ROLE_OUT"; fail "live-mv (role) did not report a real write"; }
  grep -qF '"module.crossplane_renamed.aws_iam_role.giantswarm_crossplane_role" -> "module.crossplane_final.aws_iam_role.giantswarm_crossplane_role"' <<< "$MV_ROLE_OUT" \
    || { printf '%s\n' "$MV_ROLE_OUT"; fail "live-mv (role) did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv (role): $(grep -F 'live ID' <<< "$MV_ROLE_OUT")"

  MV_POLICY_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.crossplane_renamed.aws_iam_policy.giantswarm_crossplane_policy module.crossplane_final.aws_iam_policy.giantswarm_crossplane_policy 2>&1)"; MV_POLICY_RC=$?
  [ "$MV_POLICY_RC" -eq 0 ] || { printf '%s\n' "$MV_POLICY_OUT" | tail -30; fail "choudoufu live-mv (policy) exited $MV_POLICY_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_POLICY_OUT" \
    || { printf '%s\n' "$MV_POLICY_OUT"; fail "live-mv (policy) did not report a real write"; }
  grep -qF '"module.crossplane_renamed.aws_iam_policy.giantswarm_crossplane_policy" -> "module.crossplane_final.aws_iam_policy.giantswarm_crossplane_policy"' <<< "$MV_POLICY_OUT" \
    || { printf '%s\n' "$MV_POLICY_OUT"; fail "live-mv (policy) did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv (policy): $(grep -F 'live ID' <<< "$MV_POLICY_OUT")"

  ROLE_ADDR_D2_AFTER="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ROLE_ADDR_D2_AFTER" = "module.crossplane_final.aws_iam_role.giantswarm_crossplane_role" ] \
    || fail "the role carries tofu-address=$ROLE_ADDR_D2_AFTER after live-mv, not module.crossplane_final.aws_iam_role.giantswarm_crossplane_role"
  POLICY_ADDR_D2_AFTER="$(awsl iam list-policy-tags --policy-arn "$POLICY_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$POLICY_ADDR_D2_AFTER" = "module.crossplane_final.aws_iam_policy.giantswarm_crossplane_policy" ] \
    || fail "the policy carries tofu-address=$POLICY_ADDR_D2_AFTER after live-mv, not module.crossplane_final.aws_iam_policy.giantswarm_crossplane_policy"
  log "  $ROLE_NAME and $POLICY_ARN unchanged, tofu-address now under module.crossplane_final - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  # RE-VERIFIED against gauntlet:sweep-moved-alias (recordOrphanReadSweep now
  # consults moved.Aliases/moved.Honoured, internal/live/discovery/recordorphan_read.go):
  # D1's own assertion above is now clean - a plain `moved` block relocating
  # module.crossplane no longer destroys the four untaggable children under
  # the OLD address; that is this unit's own fix, confirmed working here.
  #
  # FIXED by gauntlet:giantswarm-mv-children (internal/live/mv/mv.go's
  # propagateModuleRename): this D3 check used to fail on a SECOND, DIFFERENT
  # defect the alias-consult fix above did not reach and was not meant to -
  # D2's live-mv call only rewrote the ownership MARKER on the two taggable
  # siblings (role, policy) it was explicitly given, with no notion of
  # "move every record-located descendant of this module too", and D2's own
  # module rename is a bare HCL edit with NO `moved` block at all (by
  # design). A record-located sibling with no marker of its own
  # (identity.SingleParentComponent's own boundary - aws_iam_role_policy
  # ["extra-tagging"], aws_iam_role_policy_attachment) was left stale-keyed
  # wherever an earlier hop's own apply had last refreshed it, with nothing
  # carrying it the rest of the way to module.crossplane_final.
  #
  # propagateModuleRename now chases req.Old's own `moved`-block alias chain
  # (moved.Origins over moved.Honoured, the same primitive the alias-consult
  # fix already uses on the read side) to find every earlier address this
  # module boundary carried, and reconciles a duplicate record left behind
  # by an intermediate hop's own apply rather than erroring on it (a second
  # wall found running this exact estate for real: two records for the same
  # instance, one fresh at req.Old's own module and one further-back stale
  # copy, both landing on the same destination - the fresher one wins the
  # move, the staler one is deleted rather than left to resurface as a
  # false orphan later). Both previously-stale children now follow D1 AND
  # D2 in one live-mv call, confirmed empty below.
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty: gauntlet:giantswarm-mv-children's own fix (propagateModuleRename chasing a moved-block hop and reconciling a superseded record) regressed - re-check internal/live/mv/mv.go"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.crossplane renamed to .crossplane_renamed with zero churn (0 add, 2 change, 0 destroy - role and policy), markers rewritten in place; live-mv: .crossplane_renamed renamed to .crossplane_final with zero churn, both markers rewritten in place (one live-mv call per taggable object); stock oracle over the same chained module rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"


  # ══════════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.crossplane_final
  # (originally module.crossplane) is bound and converged, and is otherwise
  # untouched by anything else in this script until PART E removes it below
  # - the two day-2 stages compose on the SAME addresses rather than needing
  # a second standalone object. installation_name feeds BOTH taggable
  # objects' own `name` argument (see F-ORACLE's own header comment above),
  # so this changes it once and expects BOTH the role and the managed
  # policy to be forced to replace at their SAME declared addresses,
  # cascading into the untaggable dependents keyed on whichever of the two
  # just changed (the policy attachment references both; the "extra-
  # tagging" inline policy references the role). This does not hard-code
  # the exact resource-by-resource cascade shape (fragile against the same
  # kind of unrelated multi-resource variance PART D's own BREAK=rename
  # comment already documents for this estate) - it asserts the role and
  # policy are each explicitly named "must be replaced", and that the
  # plan's own add/destroy counts are equal and at least 2, the same
  # tolerant-but-load-bearing style D-ORACLE's own REMOVE_ORACLE_CHANGES
  # uses a few sections up.
  #
  # THE create_before_destroy SCOPE NOTE (full reasoning in corpus-sqs-
  # basic's own PART F). OpenTofu core rejects a `lifecycle` block on a
  # `module` call, and patching the vendored crossplane/ directory's own
  # resources to add create_before_destroy would cross this corpus's own
  # byte-identical DELTA discipline (see header), so this evidence pass
  # exercises the default destroy-then-create ordering instead. BREAK=
  # replace manufactures the create-before-destroy collision shape
  # directly via the AWS CLI, the same way corpus-sqs-basic's does.
  #
  # aws_iam_role (like corpus-evoteum-modules' aws_dynamodb_table.this)
  # carries no count/for_each, so a manufactured collision on it takes the
  # same VERIFIED scalar-resource path that estate's own PART F documents:
  # a named "Live resource displaced from the address it is marked for"
  # warning at rc=0, not corpus-sqs-basic's fungible-set "Two live
  # resources claiming one slot" hard refusal.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ROLE_ADDR="module.crossplane_final.aws_iam_role.giantswarm_crossplane_role"
  F_ROLE_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_iam_role/$(record_key "$F_ROLE_ADDR")"

  log "=== F0. capture the live role/policy and the role's record ahead of the forced replace ==="
  [ -f "$F_ROLE_RECORD" ] || fail "no local record file found for $F_ROLE_ADDR ahead of day2_replace"
  F_OLD_ROLE_IMPORT_ID="$(record_import_id "$F_ROLE_RECORD")"
  [ "$F_OLD_ROLE_IMPORT_ID" = "$ROLE_NAME" ] || fail "the record for $F_ROLE_ADDR names $F_OLD_ROLE_IMPORT_ID ahead of day2_replace, not $ROLE_NAME"
  F_OLD_ROLE_ADDR_TAG="$(awsl iam list-role-tags --role-name "$ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_OLD_ROLE_ADDR_TAG" = "$F_ROLE_ADDR" ] || fail "$ROLE_NAME does not carry tofu-address=$F_ROLE_ADDR ahead of day2_replace"
  log "  $ROLE_NAME, record import_id=$F_OLD_ROLE_IMPORT_ID, tofu-address=$F_OLD_ROLE_ADDR_TAG; $POLICY_ARN also present at module.crossplane_final.aws_iam_policy.giantswarm_crossplane_policy"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live role carrying the SAME tofu-address as the
    # one a genuine replace would destroy - the state "skip the destroy
    # half" of a create-before-destroy replace would leave, produced
    # directly via the AWS CLI rather than by actually interrupting an
    # apply (day2_crash's own job).
    BREAK_COLLISION_ROLE="${ROLE_NAME}-collision"
    awsl iam create-role --role-name "$BREAK_COLLISION_ROLE" \
      --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
      --tags "Key=tofu-estate,Value=$ESTATE_NAME" "Key=tofu-address,Value=$F_ROLE_ADDR" \
      >/dev/null || fail "BREAK=replace: could not create the collision role"
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    awsl iam delete-role --role-name "$BREAK_COLLISION_ROLE" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -eq 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the plan exited $BREAK_PLAN_RC - expected rc=0 with a named displaced-resource warning (see corpus-evoteum-modules' own PART F for the verified shape)"; }
    grep -qF 'Warning: Live resource displaced from the address it is marked for' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the plan succeeded with two live roles claiming the same tofu-address but did not report the collision - this stage's check is not load-bearing"; }
    grep -qF "$BREAK_COLLISION_ROLE" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the displaced-resource warning does not name the collision role ($BREAK_COLLISION_ROLE)"; }
    grep -qF "$F_ROLE_ADDR" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the displaced-resource warning does not name the contested address ($F_ROLE_ADDR)"; }
    log "  BREAK=replace: choudoufu correctly reported the collision by name (\"Live resource displaced from the address it is marked for\", naming both $BREAK_COLLISION_ROLE and $F_ROLE_ADDR) rather than silently proposing nothing - the same scalar-resource shape corpus-evoteum-modules' own PART F verified"
  else
    log "=== F1. choudoufu: change the ForceNew installation_name argument, forcing role+policy replace at the same declared addresses ==="
    sed -i.bak "s/installation_name = \"$INSTALLATION\"/installation_name = \"${INSTALLATION}-v2\"/" "$ESTATE/main.tofu"
    rm -f "$ESTATE/main.tofu.bak"
    grep -q "${INSTALLATION}-v2" "$ESTATE/main.tofu" || fail "changing module.crossplane_final's installation_name argument did not match - the corpus pin has moved"
    F_NEW_ROLE_NAME="giantswarm-${INSTALLATION}-v2-crossplane"
    F_NEW_POLICY_ARN="arn:aws:iam::000000000000:policy/giantswarm-${INSTALLATION}-v2-crossplane"

    F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.crossplane_final\.aws_iam_role\.giantswarm_crossplane_role must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.crossplane_final's role when installation_name changes"; }
    grep -qE '^  # module\.crossplane_final\.aws_iam_policy\.giantswarm_crossplane_policy must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.crossplane_final's policy when installation_name changes"; }
    F_ADD="$(grep -oE 'Plan: [0-9]+ to add' <<< "$F_PLAN_OUT" | grep -oE '[0-9]+')"
    F_CHANGE="$(grep -oE '[0-9]+ to change' <<< "$F_PLAN_OUT" | grep -oE '^[0-9]+')"
    F_DESTROY="$(grep -oE '[0-9]+ to destroy\.' <<< "$F_PLAN_OUT" | grep -oE '^[0-9]+')"
    [ -n "$F_ADD" ] && [ "$F_ADD" = "$F_DESTROY" ] && [ "$F_ADD" -ge 2 ] \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -20; fail "the day2_replace plan does not show an equal, at-least-2 add/destroy cascade (role+policy at minimum)"; }
    log "  choudoufu: $F_ADD to add / $F_CHANGE to change / $F_DESTROY to destroy - role and policy both forced to replace at the same declared addresses"

    F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE "Resources: $F_ADD added, $F_CHANGE changed, $F_DESTROY destroyed" <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match its own planned $F_ADD add / $F_CHANGE change / $F_DESTROY destroy"; }

    if F_OLD_ROLE_STILL="$(awsl iam get-role --role-name "$ROLE_NAME" 2>&1)"; then
      echo "$F_OLD_ROLE_STILL"; fail "$ROLE_NAME still exists after the replace - the old role was orphaned, not destroyed"
    fi
    grep -qi 'NoSuchEntity' <<< "$F_OLD_ROLE_STILL" \
      || { echo "$F_OLD_ROLE_STILL"; fail "get-role for $ROLE_NAME failed with an unexpected error, not NoSuchEntity - it may still exist"; }
    log "  $ROLE_NAME no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ROLE_ADDR_TAG="$(awsl iam list-role-tags --role-name "$F_NEW_ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_NEW_ROLE_ADDR_TAG" = "$F_ROLE_ADDR" ] \
      || fail "$F_NEW_ROLE_NAME carries tofu-address=$F_NEW_ROLE_ADDR_TAG after the replace, not $F_ROLE_ADDR - the marker did not move onto the new object"
    log "  $F_NEW_ROLE_NAME (the new role) carries tofu-address=$F_NEW_ROLE_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW role's import_id (its name), not the one captured in F0.
    F_NEW_ROLE_IMPORT_ID="$(record_import_id "$F_ROLE_RECORD")"
    [ "$F_NEW_ROLE_IMPORT_ID" = "$F_NEW_ROLE_NAME" ] \
      || fail "the record for $F_ROLE_ADDR names $F_NEW_ROLE_IMPORT_ID after the replace, not the new role $F_NEW_ROLE_NAME - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_ROLE_IMPORT_ID" != "$F_OLD_ROLE_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ROLE_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_ROLE_IMPORT_ID -> $F_NEW_ROLE_IMPORT_ID at the same key ($F_ROLE_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  No changes. The replace is complete and invisible to the next plan."

    # PART E below reads $ROLE_NAME/$POLICY_ARN for its own AWS CLI checks
    # and its own log line; the live objects it must find are now the ones
    # this replace just created.
    ROLE_NAME="$F_NEW_ROLE_NAME"
    POLICY_ARN="$F_NEW_POLICY_ARN"

    gauntlet_stage day2_replace pass "choudoufu: changing module.crossplane_final's ForceNew installation_name argument proposed a $F_ADD add / $F_CHANGE change / $F_DESTROY destroy cascade with the role and the managed policy each explicitly named 'must be replaced' at their same declared addresses, applied cleanly; the old role ($F_OLD_ROLE_IMPORT_ID) is confirmed gone and the new role ($F_NEW_ROLE_NAME) carries the marker, both via the AWS CLI; the local record store's record at the role's address now names the new role, not the destroyed one ($F_OLD_ROLE_IMPORT_ID -> $F_NEW_ROLE_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes an equal add/destroy cascade (>=2) with role and policy both replaced at the same addresses (plan only, not applied - it shares floci's account with \$ESTATE); BREAK=replace confirms a manufactured marker collision is reported loudly (a named 'Live resource displaced from the address it is marked for' warning, the scalar-resource shape) rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment and corpus-sqs-basic's matching one."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active stage - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.crossplane_final
  # (originally module.crossplane) is bound and converged. It is the whole
  # target here too, same as the stock oracle above - the only removable
  # boundary this estate has.
  #
  # BREAK_REMOVE=1 exercises this stage's own break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go for day2_remove is literally
  # "keep the block; no destroy may be proposed".

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live ids one more time ==="
  log "  role $ROLE_NAME, policy $POLICY_ARN (both module.crossplane_final)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.crossplane_final's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.crossplane_final\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_REMOVE=1: a destroy was proposed under module.crossplane_final even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: the kept-block plan is not empty"; }
    log "  BREAK_REMOVE=1: correctly proposes nothing - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.crossplane_final's block ==="
    remove_module_block "$ESTATE/main.tofu" "crossplane_final"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -30
      fail "choudoufu withheld a destroy under module.crossplane_final as a possible rename (discovery.go's classifyOrphans) even though no other module.crossplane* block exists anywhere in this config - this is an honest wall, not a pass"
    fi
    REMOVE_CHANGES="$(grep -oE '^  # \S+ will be (destroyed|created|updated in-place)' <<< "$REMOVE_PLAN_OUT" | sed -E 's/^  # //' | sort -u)"
    REMOVE_N="$(printf '%s\n' "$REMOVE_CHANGES" | grep -c . || true)"
    # A real, named wall (HANDOFF row 2: the plans differ), reproduced
    # directly with no tofu in the loop: with module.crossplane_final's
    # block deleted, this estate's root config declares literally ZERO
    # resource or module blocks (it is the only module call this estate
    # has - see this script's own header). A standalone repro (apply this
    # exact config, delete the module block, choudoufu live-plan again,
    # nothing else involved) reproduces the same "No changes" answer even
    # though all 6 objects this estate's tofu-estate tag still marks are
    # genuinely live - discovery's estate-wide sweep does not fire when
    # the configuration it is walking declares nothing at all, an edge
    # case distinct from corpus-ecs-fargate's own day2_remove finding in
    # this same batch (there, 61 OTHER resources stayed declared
    # elsewhere in the same estate, so the sweep DID run and only the
    # composed-of-arguments untaggable children it swept for were
    # missed). Whether these two symptoms share one root cause or are two
    # separate gaps is not established here; both are real, both are
    # named, neither is fixed in this script-only unit.
    grep -qF "module.crossplane_final.aws_iam_role.giantswarm_crossplane_role will be destroyed" <<< "$REMOVE_CHANGES" \
      || { printf '%s\n' "$REMOVE_CHANGES"; fail "choudoufu proposes no destroy at all for module.crossplane_final's block (not even the role itself, tagged though it is) - see the comment immediately above this assertion for the reproduced root cause (an estate whose configuration declares zero resource/module blocks is never swept for its own orphaned tagged objects)"; }
    # FIXED (gauntlet:rename-beneficiaries, 2026-08-25) - the oracle-
    # comparison design issue named below by gauntlet:giantswarm-mv-children.
    # PLAIN_ORACLE_REMOVE is deliberately built from $PLAIN, cold_deploy's
    # OWN, never-renamed state (module.crossplane throughout - see the
    # D-ORACLE comment above STAGE 2), so that this stage's oracle answers
    # "what does stock destroy when the block is removed" with nothing else
    # in play, the same isolation principle the day2_rename D-ORACLE above
    # applies to the rename question alone. By the time this stage runs,
    # choudoufu's own estate has gone through Part D's real, verified
    # rename chain (module.crossplane -> .crossplane_renamed ->
    # .crossplane_final), so a literal string comparison of the two plans'
    # addresses was always going to disagree on the module name alone,
    # independent of whether the underlying instance set and action set
    # genuinely match. The stage's own Oracle text (live/GAUNTLET.md #7)
    # asks whether stock "plans the same destroys in a working order," not
    # whether the two plans share a label a real, separately-verified
    # rename legitimately changed - day2_rename's own Oracle text next to
    # it already normalises marker tags before comparing, so normalising
    # the module prefix here is the same convention, not a new one.
    # Normalise choudoufu's own module prefix back to the oracle's
    # un-renamed one before comparing; REMOVE_CHANGES itself (used above,
    # and in the pass detail below) stays untouched, so the log and the
    # role-destroy assertion still show what choudoufu genuinely proposed
    # under module.crossplane_final.
    REMOVE_CHANGES_NORMALIZED="$(sed -E 's/^module\.crossplane_final\./module.crossplane./' <<< "$REMOVE_CHANGES")"
    [ "$REMOVE_CHANGES_NORMALIZED" = "$REMOVE_ORACLE_CHANGES" ] \
      || {
        printf 'choudoufu (%s, module-normalised):\n%s\nstock oracle (%s):\n%s\n' "$REMOVE_N" "$REMOVE_CHANGES_NORMALIZED" "$REMOVE_ORACLE_N" "$REMOVE_ORACLE_CHANGES"
        fail "choudoufu's day2_remove plan differs from stock's oracle on the instance set or action set, module name normalised out of both sides (module.crossplane_final -> module.crossplane) - a real difference, not the module-name artifact this assertion used to trip on"
      }
    log "  choudoufu: $REMOVE_N resource action(s), address-for-address and action-for-action identical to stock's oracle on cold_deploy's own state (module name normalised: module.crossplane_final -> module.crossplane, the label Part D's own verified rename chain legitimately changed)"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Apply complete!' <<< "$REMOVE_APPLY_OUT" \
      || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply did not complete"; }
    log "  $(grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT")"

    if E_ROLE_STILL="$(awsl iam get-role --role-name "$ROLE_NAME" 2>&1)"; then
      echo "$E_ROLE_STILL"; fail "$ROLE_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    grep -qi 'NoSuchEntity' <<< "$E_ROLE_STILL" \
      || { echo "$E_ROLE_STILL"; fail "get-role for $ROLE_NAME failed with an unexpected error, not NoSuchEntity - it may still exist"; }
    log "  $ROLE_NAME no longer exists (NoSuchEntity) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.crossplane_final's block proposed $REMOVE_N resource action(s), address-for-address and action-for-action identical to stock's oracle on cold_deploy's own state; applied cleanly; the role is genuinely gone from the live account (get-role now returns NoSuchEntity, read via the AWS CLI, not choudoufu's own report); classifyOrphans did not withhold any destroy because no other module.crossplane* block is declared anywhere in this config; the next plan is empty"

    # ══════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active stage - live/GAUNTLET.md #8;
    # issue #643's board-repair sweep)
    # ══════════════════════════════════════════════════════════════════════
    #
    # WHY A SYNTHETIC BLOCK, checked against .corpus rather than assumed.
    # The pinned crossplane module declares exactly two expansion knobs, both
    # in role.tofu and both reachable from this script's own root wiring:
    #
    #   aws_iam_role_policy.additional_policies
    #     for_each = var.additional_policies                (line 33)
    #   aws_iam_role_policy_attachment.additional_policy_attachments
    #     for_each = toset(var.additional_policies_arns)    (line 40)
    #
    # There is no `count` anywhere in the module, and neither for_each can
    # carry this stage. Both types are UNTAGGABLE (no `tags` argument in the
    # pinned v6.59.0 Argument Reference - this script's own STAGE 2 asserts
    # them into the UNTAGGABLE bucket by name), so neither instance carries a
    # tofu-address marker at all, and "every surviving instance keeps its
    # identity" could not be read back off the live object the way the stage
    # requires. The second one is additionally proved to resolve to ZERO
    # instances at STAGE 1. And the inline-policy set the first one expands
    # into is policed by aws_iam_role_policies_exclusive in the same module,
    # so scaling it moves two resources' live content at once rather than
    # one. That is this estate's version of the same answer every sibling
    # day2_count unit reached about a terraform-aws-modules-style
    # `count = local.create ? 1 : 0`: a real knob that is not a scalable one.
    #
    # So this section uses the sanctioned fallback (live/GAUNTLET.md #8, with
    # reference-ec2-vpc's Part F and corpus-iam-policy's Part G as
    # precedent): a NEW, self-contained synthetic block, count = 2, of a type
    # this estate ALREADY exercises - aws_iam_role, the module's own primary
    # object and one of its only two taggable types - that nothing else in
    # this estate references. It is written to its own file
    # ($ESTATE/day2_count.tofu, .tofu like everything else here), never into
    # the vendored module, so the header's byte-identical DELTA discipline is
    # untouched.
    #
    # THE DESTROY WITNESS, established directly against floci first with no
    # tofu in the loop (HANDOFF: "read the API directly"), on the pinned
    # image, before any assertion below was written:
    #
    #   create probe-count-test-0        -> RoleId AROAK1UQR4CC8GA10BDZ
    #   create probe-count-test-1        -> RoleId AROA8FRGRV7GAEB6SGJP
    #   delete probe-count-test-1        -> get-role now NoSuchEntity
    #   create probe-count-test-1 (SAME name)
    #                                    -> RoleId AROAUNW2HNJSV6P5XNMB
    #                                       CreateDate 07:26:41 -> 07:26:44
    #   probe-count-test-0               -> RoleId and CreateDate unchanged
    #
    # An IAM role's name is deterministic from configuration and its ARN is
    # derived from that name, so BOTH come back identical across a real
    # destroy and recreate - neither is a witness. What AWS mints per object
    # is the RoleId (the AROA... unique id its own IAM documentation defines
    # as assigned at creation, and the reason AWS tells you to pin a
    # principal by unique id rather than by ARN when you care about
    # delete-and-recreate). floci reproduces that, and its CreateDate carries
    # microseconds, so both discriminate a same-second recreate. This section
    # therefore proves the destroy by a CHANGED RoleId under an UNCHANGED
    # deterministic name - the corpus-hongbomiao-storage shape (same name,
    # changed creation timestamp) and the reference-ec2-vpc shape (a new
    # server-minted id) at once, rather than by a name_prefix suffix moving,
    # which only ever proves a different name was used.
    #
    # G-ORACLE is this stage's stock oracle (live/GAUNTLET.md #8: "Stock's
    # plan for the same count change, normalised"): the IDENTICAL count block
    # emitted by the same shell function, stood up by real `tofu` (this
    # estate's stock binary - plain terraform cannot parse a .tofu estate at
    # all, see this script's header) in its own working directory, against
    # $ORACLE_ENDPOINT - the third container PART GREENFIELD's own oracle
    # used and has not touched since G6, so it is idle, and its only objects
    # are named from $GREEN_INSTALLATION, disjoint from these. A separate
    # container rather than a shared account means the oracle's own
    # unmarked roles can never be mistaken for choudoufu's marked ones by a
    # name lookup, which is the trap corpus-iam-policy's Part G had to add a
    # teardown for.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: it asserts the WRONG instance was destroyed (count_test[0]
    # rather than count_test[1]), which is the Break text in
    # tools/gauntlet/stages.go for day2_count verbatim - "Expect a different
    # instance to be destroyed; the assertion must fail" - and reports
    # verdict=fail either way, saying which. Independent of BREAK,
    # BREAK_REMOVE, BREAK_STAGE3 and BREAK_STAGE5; only reachable on the
    # real path, since day2_count starts from Part E's completed removal.
    gauntlet_begin_stage day2_count
    COUNT_TEST_PREFIX="giantswarm-crossplane-count-test-"
    CT0_NAME="${COUNT_TEST_PREFIX}0"
    CT1_NAME="${COUNT_TEST_PREFIX}1"

    # One emitter for both sides, so the real leg and the oracle differ only
    # in which binary runs them and which account they run against - never in
    # the configuration itself.
    count_test_block() { # $1 = count
      local n="$1"
      cat <<COUNTEOF
resource "aws_iam_role" "count_test" {
  count = $n
  name  = "$COUNT_TEST_PREFIX\${count.index}"
  path  = "/"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = {
    purpose = "day2_count evidence"
  }
}
COUNTEOF
    }
    oracle_count_provider() {
      cat <<COUNTPROVEOF
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

COUNTPROVEOF
    }
    # The two witnesses, read straight off the IAM API on whichever endpoint
    # is asked for. Empty (never a stale value) when the role is gone.
    role_id_on() { aws --endpoint-url "$1" --region "$REGION" iam get-role --role-name "$2" --query 'Role.RoleId' --output text 2>/dev/null || true; }
    role_created_on() { aws --endpoint-url "$1" --region "$REGION" iam get-role --role-name "$2" --query 'Role.CreateDate' --output text 2>/dev/null || true; }
    # The change lines a count plan is allowed to carry, normalised to the
    # bare "<address> will be <action>" the stage's Oracle text compares.
    count_actions() { grep -oE '^  # aws_iam_role\.count_test\[[0-9]+\] will be [a-z]+' <<< "$1" | sed -E 's/^  # //' | sort -u; }

    log "=== G-ORACLE. stock tofu: the identical 2-instance count block, scaled 2 -> 1 -> 2, in the idle greenfield-oracle account ==="
    PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
    mkdir -p "$PLAIN_ORACLE_COUNT"
    { oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tofu"
    ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
    ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_COUNT_APPLY_RC=$?
    [ "$ORACLE_COUNT_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
    grep -qE 'Apply complete! Resources: 2 added, 0 changed, 0 destroyed' <<< "$ORACLE_COUNT_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_COUNT_APPLY_OUT"; fail "stock did not create exactly 2 count_test roles for the day2_count oracle"; }
    ORACLE_CT0_ID="$(role_id_on "$ORACLE_ENDPOINT" "$CT0_NAME")"
    ORACLE_CT1_ID="$(role_id_on "$ORACLE_ENDPOINT" "$CT1_NAME")"
    ORACLE_CT0_CREATED="$(role_created_on "$ORACLE_ENDPOINT" "$CT0_NAME")"
    ORACLE_CT1_CREATED="$(role_created_on "$ORACLE_ENDPOINT" "$CT1_NAME")"
    [ -n "$ORACLE_CT0_ID" ] && [ "$ORACLE_CT0_ID" != "None" ] || fail "no oracle count_test[0] role ($CT0_NAME) found after stock's baseline apply"
    [ -n "$ORACLE_CT1_ID" ] && [ "$ORACLE_CT1_ID" != "None" ] || fail "no oracle count_test[1] role ($CT1_NAME) found after stock's baseline apply"
    [ "$ORACLE_CT0_ID" != "$ORACLE_CT1_ID" ] || fail "sanity: stock's two count_test roles share one RoleId ($ORACLE_CT0_ID) - the witness this section rests on is not per-object"
    log "  stock: 2 instances created, count_test[0]=$CT0_NAME (RoleId=$ORACLE_CT0_ID) count_test[1]=$CT1_NAME (RoleId=$ORACLE_CT1_ID)"

    { oracle_count_provider; count_test_block 1; } > "$PLAIN_ORACLE_COUNT/main.tofu"
    ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
    [ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
    ORACLE_DOWN_ACTIONS="$(count_actions "$ORACLE_DOWN_PLAN_OUT")"
    [ "$ORACLE_DOWN_ACTIONS" = "aws_iam_role.count_test[1] will be destroyed" ] \
      || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan is not exactly \"count_test[1] will be destroyed\", it is [$ORACLE_DOWN_ACTIONS]"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
    ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_DOWN_APPLY_RC=$?
    [ "$ORACLE_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
    [ -z "$(role_id_on "$ORACLE_ENDPOINT" "$CT1_NAME")" ] \
      || fail "stock's count_test[1] role ($CT1_NAME) still exists after the oracle's scale-down destroy"
    [ "$(role_id_on "$ORACLE_ENDPOINT" "$CT0_NAME")" = "$ORACLE_CT0_ID" ] \
      || fail "stock's surviving count_test[0] changed RoleId across the scale-down - stock did not leave the lower index alone"
    [ "$(role_created_on "$ORACLE_ENDPOINT" "$CT0_NAME")" = "$ORACLE_CT0_CREATED" ] \
      || fail "stock's surviving count_test[0] changed CreateDate across the scale-down"
    log "  stock: exactly one destroy (count_test[1], RoleId $ORACLE_CT1_ID, now NoSuchEntity), count_test[0] RoleId and CreateDate unchanged"

    { oracle_count_provider; count_test_block 2; } > "$PLAIN_ORACLE_COUNT/main.tofu"
    ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
    [ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
    ORACLE_UP_ACTIONS="$(count_actions "$ORACLE_UP_PLAN_OUT")"
    [ "$ORACLE_UP_ACTIONS" = "aws_iam_role.count_test[1] will be created" ] \
      || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan is not exactly \"count_test[1] will be created\", it is [$ORACLE_UP_ACTIONS]"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
      || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
    ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)"; ORACLE_UP_APPLY_RC=$?
    [ "$ORACLE_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
    ORACLE_CT1_NEW_ID="$(role_id_on "$ORACLE_ENDPOINT" "$CT1_NAME")"
    ORACLE_CT1_NEW_CREATED="$(role_created_on "$ORACLE_ENDPOINT" "$CT1_NAME")"
    [ -n "$ORACLE_CT1_NEW_ID" ] && [ "$ORACLE_CT1_NEW_ID" != "None" ] || fail "no oracle count_test[1] role found after stock's scale-up"
    [ "$ORACLE_CT1_NEW_ID" != "$ORACLE_CT1_ID" ] \
      || fail "stock's recreated count_test[1] came back with the SAME RoleId ($ORACLE_CT1_ID) it had before being destroyed - the oracle's own destroy was not real"
    [ "$ORACLE_CT1_NEW_CREATED" != "$ORACLE_CT1_CREATED" ] \
      || fail "stock's recreated count_test[1] came back with the SAME CreateDate it had before being destroyed"
    [ "$(role_id_on "$ORACLE_ENDPOINT" "$CT0_NAME")" = "$ORACLE_CT0_ID" ] \
      || fail "stock's count_test[0] changed RoleId across the scale-up"
    log "  stock: exactly one create (count_test[1] back under the SAME name $CT1_NAME but a NEW RoleId $ORACLE_CT1_NEW_ID, was $ORACLE_CT1_ID), count_test[0] RoleId $ORACLE_CT0_ID untouched across the whole down-then-up cycle"

    log "=== G1. choudoufu: add aws_iam_role.count_test, count = 2 ==="
    count_test_block 2 > "$ESTATE/day2_count.tofu"
    COUNT_ADD_PLAN_OUT="$(plan_into 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -40; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -15; fail "adding the count block did not plan exactly 2 creates"; }
    COUNT_ADD_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -40; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    CT0_ID="$(role_id_on "$ENDPOINT" "$CT0_NAME")"
    CT1_ID="$(role_id_on "$ENDPOINT" "$CT1_NAME")"
    CT0_CREATED="$(role_created_on "$ENDPOINT" "$CT0_NAME")"
    CT1_CREATED="$(role_created_on "$ENDPOINT" "$CT1_NAME")"
    [ -n "$CT0_ID" ] && [ "$CT0_ID" != "None" ] || fail "no live count_test[0] role ($CT0_NAME) after the count-block-add apply"
    [ -n "$CT1_ID" ] && [ "$CT1_ID" != "None" ] || fail "no live count_test[1] role ($CT1_NAME) after the count-block-add apply"
    [ "$CT0_ID" != "$CT1_ID" ] || fail "sanity: both count_test roles share one RoleId ($CT0_ID)"
    # live/MARKERS.md's escaping rule: a count instance's tag value is
    # colon-escaped, aws_eip.this[2] -> aws_eip.this:2.
    CT0_ADDR_TAG="$(awsl iam list-role-tags --role-name "$CT0_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    CT1_ADDR_TAG="$(awsl iam list-role-tags --role-name "$CT1_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT0_ADDR_TAG" = 'aws_iam_role.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $CT0_ADDR_TAG, not aws_iam_role.count_test:0 (live/MARKERS.md's colon escaping for an indexed address)"
    [ "$CT1_ADDR_TAG" = 'aws_iam_role.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $CT1_ADDR_TAG, not aws_iam_role.count_test:1"
    CT0_ESTATE_TAG="$(awsl iam list-role-tags --role-name "$CT0_NAME" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
    [ "$CT0_ESTATE_TAG" = "$ESTATE_NAME" ] || fail "count_test[0] carries tofu-estate=$CT0_ESTATE_TAG, not $ESTATE_NAME"
    CT0_PURPOSE_TAG="$(awsl iam list-role-tags --role-name "$CT0_NAME" --query "Tags[?Key=='purpose'].Value | [0]" --output text)"
    [ "$CT0_PURPOSE_TAG" = "day2_count evidence" ] \
      || fail "count_test[0]'s own purpose tag is \"$CT0_PURPOSE_TAG\" after stamping - the marker replaced the block's tag set instead of merging into it"
    log "  2 instances created: index 0 = $CT0_NAME (RoleId=$CT0_ID, tofu-address=$CT0_ADDR_TAG), index 1 = $CT1_NAME (RoleId=$CT1_ID, tofu-address=$CT1_ADDR_TAG) - all read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the two new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances bind their own markers and plan empty immediately"

    log "=== G2. scale count down: 2 -> 1 ==="
    count_test_block 1 > "$ESTATE/day2_count.tofu"
    COUNT_DOWN_PLAN_OUT="$(plan_into 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      # tools/gauntlet/stages.go's Break text for day2_count, verbatim:
      # "Expect a different instance to be destroyed; the assertion must
      # fail." So this asserts the LOWER index was destroyed - the one the
      # real leg proves is untouched - and nothing else changes. Either
      # branch below reports verdict=fail; the first one is the expected
      # outcome and its detail says the real assertion has teeth.
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) is the one destroyed"
      grep -qE '^  # aws_iam_role\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'
             fail "BREAK_COUNT=1: the scale-down plan does not destroy count_test[0] - it destroys the HIGHER index, count_test[1] ($CT1_NAME, RoleId $CT1_ID), leaving count_test[0] ($CT0_NAME, RoleId $CT0_ID) alone, exactly as it must; the real leg's which-instance assertion is load-bearing"; }
      fail "BREAK_COUNT=1: the scale-down plan really did propose destroying count_test[0], the LOWER index - choudoufu destroyed the wrong instance"
    fi

    COUNT_DOWN_ACTIONS="$(count_actions "$COUNT_DOWN_PLAN_OUT")"
    [ "$COUNT_DOWN_ACTIONS" = "aws_iam_role.count_test[1] will be destroyed" ] \
      || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan is not exactly \"count_test[1] will be destroyed\", it is [$COUNT_DOWN_ACTIONS]"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
    # The stage's own Oracle text, applied: stock's plan for the same count
    # change, normalised. Both sides declare the same address, so the
    # normalisation is the action-line extraction itself.
    [ "$COUNT_DOWN_ACTIONS" = "$ORACLE_DOWN_ACTIONS" ] \
      || fail "choudoufu's scale-down plan [$COUNT_DOWN_ACTIONS] differs from stock's [$ORACLE_DOWN_ACTIONS] for the identical count change"
    log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same action set stock proposed"

    COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
    [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

    if CT1_STILL="$(awsl iam get-role --role-name "$CT1_NAME" 2>&1)"; then
      echo "$CT1_STILL"; fail "count_test[1] ($CT1_NAME) still exists in the live account after the scale-down destroy"
    fi
    grep -qi 'NoSuchEntity' <<< "$CT1_STILL" \
      || { echo "$CT1_STILL"; fail "get-role for $CT1_NAME failed with an unexpected error, not NoSuchEntity - it may still exist"; }
    CT0_ID_AFTER_DOWN="$(role_id_on "$ENDPOINT" "$CT0_NAME")"
    [ "$CT0_ID_AFTER_DOWN" = "$CT0_ID" ] \
      || fail "count_test[0]'s RoleId changed across the scale-down ($CT0_ID -> $CT0_ID_AFTER_DOWN) - the survivor was destroyed and recreated, not left alone"
    [ "$(role_created_on "$ENDPOINT" "$CT0_NAME")" = "$CT0_CREATED" ] \
      || fail "count_test[0]'s CreateDate changed across the scale-down - the survivor is a different object"
    CT0_ADDR_AFTER_DOWN="$(awsl iam list-role-tags --role-name "$CT0_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT0_ADDR_AFTER_DOWN" = 'aws_iam_role.count_test:0' ] \
      || fail "count_test[0]'s tofu-address marker changed across the scale-down: $CT0_ADDR_AFTER_DOWN"
    # The local record store, by value (HANDOFF's safety rule, the #398-guard
    # shape). A destroyed count instance's record is TOMBSTONED, not deleted:
    # the envelope's top-level "identity" is cleared and a "tombstone" entry
    # added, so the honest check is has(tombstone) and not has(identity),
    # never file absence.
    CT1_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_iam_role/$(record_key 'aws_iam_role.count_test[1]')"
    [ -f "$CT1_RECORD" ] || fail "no local record file found for aws_iam_role.count_test[1] after the scale-down - expected a tombstoned record, not none at all"
    jq -e 'has("tombstone") and (has("identity") | not)' "$CT1_RECORD" >/dev/null \
      || fail "the record at aws_iam_role.count_test[1] after the scale-down is not tombstoned: $(cat "$CT1_RECORD")"
    log "  $CT1_NAME (count_test[1]) is gone (NoSuchEntity) and its local record is tombstoned, not deleted; $CT0_NAME (count_test[0]) keeps RoleId $CT0_ID, its CreateDate and its marker - all read via the AWS CLI and the record store, never through choudoufu's own report"

    log "=== G3. scale count back up: 1 -> 2 ==="
    count_test_block 2 > "$ESTATE/day2_count.tofu"
    COUNT_UP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_UP_PLAN_RC=$?
    [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
    COUNT_UP_ACTIONS="$(count_actions "$COUNT_UP_PLAN_OUT")"
    [ "$COUNT_UP_ACTIONS" = "aws_iam_role.count_test[1] will be created" ] \
      || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan is not exactly \"count_test[1] will be created\", it is [$COUNT_UP_ACTIONS]"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
    [ "$COUNT_UP_ACTIONS" = "$ORACLE_UP_ACTIONS" ] \
      || fail "choudoufu's scale-up plan [$COUNT_UP_ACTIONS] differs from stock's [$ORACLE_UP_ACTIONS] for the identical count change"
    log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same action set stock proposed"

    COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
    [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

    CT1_NEW_ID="$(role_id_on "$ENDPOINT" "$CT1_NAME")"
    CT1_NEW_CREATED="$(role_created_on "$ENDPOINT" "$CT1_NAME")"
    [ -n "$CT1_NEW_ID" ] && [ "$CT1_NEW_ID" != "None" ] || fail "no live count_test[1] role ($CT1_NAME) found after the scale-up"
    [ "$CT1_NEW_ID" != "$CT1_ID" ] \
      || fail "count_test[1] came back with the SAME RoleId ($CT1_ID) it had before being destroyed - the destroy in G2 was not real"
    [ "$CT1_NEW_CREATED" != "$CT1_CREATED" ] \
      || fail "count_test[1] came back with the SAME CreateDate ($CT1_CREATED) it had before being destroyed"
    CT1_NEW_ADDR_TAG="$(awsl iam list-role-tags --role-name "$CT1_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT1_NEW_ADDR_TAG" = 'aws_iam_role.count_test:1' ] \
      || fail "the recreated count_test[1] ($CT1_NAME) carries tofu-address=$CT1_NEW_ADDR_TAG, not aws_iam_role.count_test:1"
    CT0_ID_AFTER_UP="$(role_id_on "$ENDPOINT" "$CT0_NAME")"
    [ "$CT0_ID_AFTER_UP" = "$CT0_ID" ] \
      || fail "count_test[0]'s RoleId changed across the scale-up ($CT0_ID -> $CT0_ID_AFTER_UP)"
    [ "$(role_created_on "$ENDPOINT" "$CT0_NAME")" = "$CT0_CREATED" ] \
      || fail "count_test[0]'s CreateDate changed across the scale-up"
    [ "$(awsl iam list-role-tags --role-name "$CT0_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)" = 'aws_iam_role.count_test:0' ] \
      || fail "count_test[0]'s tofu-address marker changed across the scale-up"
    CT1_NEW_RECORD_ID="$(record_import_id "$CT1_RECORD" 2>/dev/null || true)"
    [ "$CT1_NEW_RECORD_ID" = "$CT1_NAME" ] \
      || fail "the record at aws_iam_role.count_test[1] after the scale-up names $CT1_NEW_RECORD_ID, not the recreated role $CT1_NAME - the tombstone was not replaced by a live identity"
    log "  count_test[1] recreated under the SAME deterministic name ($CT1_NAME) but a NEW RoleId ($CT1_NEW_ID, was $CT1_ID) and a new CreateDate ($CT1_NEW_CREATED, was $CT1_CREATED), tofu-address=$CT1_NEW_ADDR_TAG, record re-identified; count_test[0] ($CT0_NAME, RoleId $CT0_ID) untouched throughout the down-then-up cycle"

    log "=== G4. one more plan: config and reality agree, nothing left to propose ==="
    COUNT_FINAL_PLAN_OUT="$(plan_into 2>&1)"; COUNT_FINAL_PLAN_RC=$?
    [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
    log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

    log ""
    log "PART G (day2_count): PASS"
    gauntlet_stage day2_count pass "choudoufu: scaling aws_iam_role.count_test from 2 to 1 proposed exactly \"count_test[1] will be destroyed\" (0 add, 0 change, 1 destroy) and applied it, leaving count_test[0]'s server-minted RoleId ($CT0_ID), its CreateDate and its tofu-address=aws_iam_role.count_test:0 marker all unchanged, and tombstoning count_test[1]'s local record (has tombstone, no identity - the #398-guard shape); scaling back from 1 to 2 proposed exactly \"count_test[1] will be created\" (1 add, 0 change, 0 destroy) and brought it back under the SAME deterministic name ($CT1_NAME) with a NEW RoleId ($CT1_ID -> $CT1_NEW_ID) and a new CreateDate, re-marked aws_iam_role.count_test:1 and re-identified in the record store, while count_test[0] stayed untouched throughout; the next plan is empty. Every identity here is read back through the AWS CLI and the local record store, never through choudoufu's own report, and the destroy witness is the RoleId rather than the name or the ARN because both of those are deterministic from configuration and come back identical - confirmed against floci directly, no tofu in the loop, before the assertions were written. Stock oracle (G-ORACLE): real tofu standing the IDENTICAL count block up in the idle greenfield-oracle account showed the identical shape - destroy the higher index only, create the higher index back under the same name with a new RoleId ($ORACLE_CT1_ID -> $ORACLE_CT1_NEW_ID), the lower index's RoleId and CreateDate unchanged both times - and the two sides' normalised action sets are compared literally, not just described. Synthetic block, per live/GAUNTLET.md #8's sanctioned fallback: the pinned crossplane module declares no count at all and its only two for_each knobs (aws_iam_role_policy.additional_policies over var.additional_policies, aws_iam_role_policy_attachment.additional_policy_attachments over toset(var.additional_policies_arns)) are both UNTAGGABLE types that carry no marker to keep an identity in, the second provably resolving to zero instances, and the first's inline-policy set is additionally policed by aws_iam_role_policies_exclusive in the same module; aws_iam_role.count_test reuses a type this estate already exercises and lives in its own day2_count.tofu beside the estate's root wiring (\$ESTATE), so the vendored module stays byte-identical. BREAK_COUNT=1 asserts the WRONG instance (count_test[0]) was destroyed and reports fail, proving the which-instance assertion is load-bearing."
    log ""
    gauntlet_end_stage
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end

log "=== PASS: all five stages, real, against giantswarm/giantswarm-aws-account- ==="
log "=== prerequisites v8.2.2's own unmodified crossplane/ module, .tofu         ==="
log "=== extension throughout, with both *_exclusive enforcers admitted (#334)   ==="
