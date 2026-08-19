#!/usr/bin/env bash
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
# STAGE 3 IS BLOCKED, AND THE BLOCK IS EXACTLY TWO SITES. Both exclusive
# types are unadmitted: neither has a resource identity schema in aws 6.59.0
# and neither has a generated table row, so `live-plan` refuses each with
# Rule: unadmitted-type. That is this crossing's finding and it is asserted
# below at an exact site count and an exact type list, not merely observed -
# and NOTHING ELSE in this estate refuses. Filed as INTENTIUS/choudoufu#334,
# with the observation that both types have the identical import grammar
# shape to aws_vpc_security_group_rules_exclusive, which #307 admitted via
# row-gen's own tryGrammarComposite path (64cac28120): a single required
# argument naming the parent (`role_name` here, `security_group_id` there),
# the whole import ID with no separator. The one recorded difference is that
# live/import-grammar.json carries "force_new": true on
# security_group_id and carries no force_new flag on either role_name.
#
# STAGE 3b is a CONTROL, not a stage verdict. It re-runs the whole
# migration against a copy of the same module with the two exclusive blocks
# - and only those two blocks - deleted, and asserts that stages 3, 4 and 5
# then all clear. That is what turns "blocked at stage 3" into "blocked at
# stage 3 by exactly these two resource types and nothing else", which is
# the claim #334 rests on. It runs against the same floci under a second
# installation name so the two estates never touch each other's objects.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                    the unmodified crossplane module - the honest proof the
#                    module is real and buildable, and the source of
#                    genuinely unmarked live infra for stage 2.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                    state; markers re-read through the AWS CLI directly.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan`, and assert
#                    the refusal surface EXACTLY - 2 errors, both
#                    unadmitted-type, both named.
#   3b. CONTROL      the same estate minus the two exclusive blocks, driven
#                    through test plan / test apply / drift and reconverge.
#   4. TEST APPLY    NOT REACHED - needs a genuinely empty first plan.
#   5. DRIFT AND     NOT REACHED - same reason.
#      RECONVERGE
#
# BREAK=1 corrupts stage 2's expected tofu-address and stage 3's expected
# refusal-site count, so both assertions are proven load-bearing rather than
# a grep that always matches. It fails fast at stage 2, so stage 3's negative
# control must be exercised with BREAK_STAGE3=1 on its own.
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
#   BREAK         set to 1 to corrupt stage 2's identity assertion.
#   BREAK_STAGE3  set to 1 to corrupt stage 3's expected refusal-site count.
#   SKIP_CONTROL  set to 1 to skip stage 3b (the control), halving runtime.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/giantswarm-aws-prereqs/crossplane"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
CTRL_PLAIN="$WORK/ctrl-plain"
CTRL_ESTATE="$WORK/ctrl-estate"
FLOCI_PORT="${FLOCI_PORT:-4729}"
FLOCI_NAME="choudoufu-corpus-giantswarm-crossplane-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="giantswarm-crossplane-crossing"
INSTALLATION="gsprereqs"
ROLE_NAME="giantswarm-${INSTALLATION}-crossplane"
POLICY_ARN="arn:aws:iam::000000000000:policy/giantswarm-${INSTALLATION}-crossplane"
CTRL_ESTATE_NAME="giantswarm-crossplane-control"
CTRL_INSTALLATION="gsnoexcl"
CTRL_ROLE_NAME="giantswarm-${CTRL_INSTALLATION}-crossplane"
EXTRA_POLICY_NAME="extra-tagging"

# This script runs FOUR `tofu init`s (plain, estate, and the two control
# copies), each of which would otherwise re-download the ~500MB AWS provider
# into its own scratch directory. Point them all at OpenTofu's own conventional
# shared plugin cache so only the first one can ever pay for a download; an
# operator who already exports TF_PLUGIN_CACHE_DIR keeps theirs.
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
mkdir -p "$TF_PLUGIN_CACHE_DIR"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "2 of 6 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 6 as eligible (the IAM role and the managed policy)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE (2)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 2 UNTAGGABLE resources (the inline role policy and the managed-policy attachment)"
grep -qF "UNADMITTED_TYPE (2)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 2 UNADMITTED_TYPE resources (the two *_exclusive enforcers - see the header and #334)"
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
log "  2 of 6 verified against the live system (both DRIFTED on benign shadow attributes); 2 UNTAGGABLE, 2 UNADMITTED_TYPE; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 failed, 4 skipped" <<< "$APPROVE_OUT" \
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, refusal surface asserted EXACTLY
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"

if grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT"; then
  fail "live-plan came back EMPTY - #334 has been fixed and this script's stage 3 must be rewritten to assert a pass, not a block"
fi
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited 0 without an empty plan - re-read the output"; }

# The refusal surface, asserted exactly rather than sampled. Every ^Error: in
# this plan must be unadmitted-type, and there must be exactly two of them.
N_ERRORS="$(grep -cE '^Error: ' <<< "$PLAN_OUT")"
N_UNADMITTED="$(grep -cF 'Rule: unadmitted-type.' <<< "$PLAN_OUT")"
WANT_ERRORS=2
if [ "${BREAK_STAGE3:-}" = "1" ]; then
  WANT_ERRORS=3
  log "  BREAK_STAGE3=1: expecting 3 refusal sites on purpose - this check must fail"
fi
[ "$N_ERRORS" = "$WANT_ERRORS" ] \
  || { grep -E '^Error: ' <<< "$PLAN_OUT"; fail "expected exactly $WANT_ERRORS refusal sites in the whole plan, got $N_ERRORS"; }
if [ "${BREAK_STAGE3:-}" = "1" ]; then
  fail "BREAK_STAGE3=1: the plan's real refusal-site count matched the WRONG expected value above - stage 3's assertion is not load-bearing"
fi
[ "$N_UNADMITTED" = "2" ] \
  || fail "expected both refusals to be Rule: unadmitted-type, got $N_UNADMITTED of $N_ERRORS"
# Match the SOURCE SNIPPET each diagnostic echoes, not its prose. The prose is
# hard-wrapped at the terminal width, and the type name lands on either side of
# a line break depending on how long it is - a literal grep for
# `resource type "<type>" is not in the` matches the longer of these two names
# and silently misses the shorter one. The snippet line is verbatim source.
grep -qE '^ +[0-9]+: resource "aws_iam_role_policy_attachments_exclusive" "exclusive_policy_attachments" \{' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -nE '^ +[0-9]+: resource'; fail "aws_iam_role_policy_attachments_exclusive is not one of the two refusal sites - re-read the plan"; }
grep -qE '^ +[0-9]+: resource "aws_iam_role_policies_exclusive" "exclusive_inline_policies" \{' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -nE '^ +[0-9]+: resource'; fail "aws_iam_role_policies_exclusive is not one of the two refusal sites - re-read the plan"; }
log "  refusal surface is EXACTLY 2 sites, both unadmitted-type:"
log "    module.crossplane.aws_iam_role_policy_attachments_exclusive.exclusive_policy_attachments"
log "    module.crossplane.aws_iam_role_policies_exclusive.exclusive_inline_policies"
log "  no other rule fires anywhere in this estate - no identity, expansion,"
log "  projection, dataread or stamp refusal at all (#334)"

log ""
log "STAGE 3 (test plan): BLOCKED for real, at exactly 2 sites - see #334"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3b: CONTROL - the same estate minus the two exclusive blocks
# ══════════════════════════════════════════════════════════════════════════
if [ "${SKIP_CONTROL:-}" = "1" ]; then
  log "=== STAGE 3b: control SKIPPED (SKIP_CONTROL=1) ==="
else
log "=== STAGE 3b: control - the same module with ONLY the two exclusive blocks removed ==="

# Delete from the module's own "// Ensure exclusivity of attached policies and
# inline policies" comment to end of file - the two resource blocks and
# nothing else. Assert the cut landed where it was meant to.
make_control_copy() {
  local dest="$1"
  copy_module "$dest"
  python3 - "$dest/crossplane/role.tofu" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
marker = "// Ensure exclusivity of attached policies and inline policies"
if marker not in s:
    sys.exit("control cut: the module no longer carries the exclusivity comment this cut anchors on")
open(p, "w").write(s[:s.index(marker)].rstrip() + "\n")
PY
}
make_control_copy "$CTRL_PLAIN" || fail "control: could not cut the two exclusive blocks"
make_control_copy "$CTRL_ESTATE" || fail "control: could not cut the two exclusive blocks"
for d in "$CTRL_PLAIN" "$CTRL_ESTATE"; do
  grep -q 'aws_iam_role_policies_exclusive' "$d/crossplane/role.tofu" \
    && fail "control: aws_iam_role_policies_exclusive survived the cut"
  grep -q 'aws_iam_role_policy_attachments_exclusive' "$d/crossplane/role.tofu" \
    && fail "control: aws_iam_role_policy_attachments_exclusive survived the cut"
  grep -q 'aws_iam_role_policy_attachment" "additional_policy_attachments' "$d/crossplane/role.tofu" \
    || fail "control: the cut removed more than the two exclusive blocks"
done
write_root "$CTRL_PLAIN" "$CTRL_INSTALLATION" ""
write_root "$CTRL_ESTATE" "$CTRL_INSTALLATION" '
  live {
    estate = "'"$CTRL_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  cut verified: exactly the two exclusive blocks removed, nothing else"

# Reuse the already-initialized provider directory rather than running two
# more `tofu init`s. The control's provider requirements and module source are
# identical to the estate it is a control FOR - that is the whole point of it -
# so a fresh init would re-materialize the same ~500MB AWS provider twice more
# and is by far the most expensive thing this script could do. -c is APFS
# clonefile (instant, no bytes copied); the fallback is a plain recursive copy.
reuse_init() {
  local from="$1" to="$2"
  cp -Rc "$from/.terraform" "$to/.terraform" 2>/dev/null \
    || cp -R "$from/.terraform" "$to/.terraform" \
    || return 1
  cp "$from/.terraform.lock.hcl" "$to/.terraform.lock.hcl" || return 1
}
reuse_init "$PLAIN" "$CTRL_PLAIN" || fail "control: could not reuse the plain estate's initialized provider directory"
reuse_init "$ESTATE" "$CTRL_ESTATE" || fail "control: could not reuse the estate's initialized provider directory"
log "  reused both initialized provider directories - no further tofu init needed"
CTRL_COLD="$(cd "$CTRL_PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; CTRL_RC=$?
[ "$CTRL_RC" -eq 0 ] || { printf '%s\n' "$CTRL_COLD" | tail -30; fail "control: cold deploy failed"; }
grep -qE 'Apply complete! Resources: 4 added, 0 changed, 0 destroyed' <<< "$CTRL_COLD" \
  || { grep -E 'Apply complete' <<< "$CTRL_COLD"; fail "control: cold deploy did not create exactly 4 resource instances"; }
log "  control cold deploy: 4 resources"

CTRL_APPROVE="$(cd "$CTRL_ESTATE" && "$TOFU" live-import -state="$CTRL_PLAIN/terraform.tfstate" -estate="$CTRL_ESTATE_NAME" -approve -no-color 2>&1)"
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 failed, 2 skipped" <<< "$CTRL_APPROVE" \
  || { printf '%s\n' "$CTRL_APPROVE" | tail -20; fail "control: migrate did not stamp exactly 2 of 4"; }
log "  control migrate: 2 stamped, 2 correctly untaggable"

rm -f "$CTRL_ESTATE/terraform.tfstate" "$CTRL_ESTATE/terraform.tfstate.backup"
ctrl_plan() { ( cd "$CTRL_ESTATE" && "$TOFU" live-plan -input=false -no-color ); }
CTRL_PLAN="$(ctrl_plan 2>&1)"; CTRL_PLAN_RC=$?
[ "$CTRL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CTRL_PLAN" | tail -40; fail "control: live-plan exited $CTRL_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$CTRL_PLAN" \
  || { grep -E '^  #|^Error: ' <<< "$CTRL_PLAN"; fail "control: live-plan is not empty - the two exclusive types are NOT the only thing blocking this estate, and #334's central claim is wrong"; }
CTRL_ROLE_ADDR="$(awsl iam list-role-tags --role-name "$CTRL_ROLE_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$CTRL_ROLE_ADDR" = "module.crossplane.aws_iam_role.giantswarm_crossplane_role" ] \
  || fail "control: the role's tofu-address is $CTRL_ROLE_ADDR after the state was deleted"
log "  control test plan: EMPTY, with zero local memory of the migration; role marker re-read via the AWS CLI"

CTRL_BEFORE="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$CTRL_ESTATE_NAME" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
CTRL_APPLY="$(cd "$CTRL_ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CTRL_APPLY_RC=$?
[ "$CTRL_APPLY_RC" -eq 0 ] || { printf '%s\n' "$CTRL_APPLY" | tail -30; fail "control: the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$CTRL_APPLY" \
  || { grep -E 'Apply complete' <<< "$CTRL_APPLY"; fail "control: the post-migration apply was not a no-op"; }
CTRL_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$CTRL_ESTATE_NAME" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$CTRL_AFTER" = "$CTRL_BEFORE" ] || fail "control: object count changed across a no-op apply: $CTRL_BEFORE -> $CTRL_AFTER"
[ ! -f "$CTRL_ESTATE/terraform.tfstate" ] || fail "control: a state file exists after the apply"
log "  control test apply: genuine no-op, $CTRL_BEFORE objects before and after, no state file either time"

awsl iam tag-role --role-name "$CTRL_ROLE_NAME" --tags Key=installation,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl iam list-role-tags --role-name "$CTRL_ROLE_NAME" --query "Tags[?Key=='installation'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "control: the out-of-band tag mutation did not take"
CTRL_DRIFT_PLAN="$(ctrl_plan 2>&1)"; CTRL_DRIFT_RC=$?
[ "$CTRL_DRIFT_RC" -eq 0 ] || { printf '%s\n' "$CTRL_DRIFT_PLAN" | tail -40; fail "control: the drift-detection plan exited $CTRL_DRIFT_RC"; }
CTRL_CHANGED="$(grep -oE '^  # \S+ will be updated' <<< "$CTRL_DRIFT_PLAN" | awk '{print $2}' | sort -u)"
CTRL_N="$(printf '%s\n' "$CTRL_CHANGED" | grep -c . || true)"
[ "$CTRL_N" = "1" ] \
  || { printf '%s\n' "$CTRL_DRIFT_PLAN" | grep -E '^  # .+ will be'; fail "control: expected exactly 1 object proposed for a fix, got $CTRL_N"; }
[ "$CTRL_CHANGED" = "module.crossplane.aws_iam_role.giantswarm_crossplane_role" ] \
  || fail "control: the plan proposes fixing $CTRL_CHANGED, not the IAM role"
CTRL_RECONVERGE="$(cd "$CTRL_ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$CTRL_RECONVERGE" \
  || { grep -E 'Apply complete' <<< "$CTRL_RECONVERGE"; fail "control: the reconverge apply did not change exactly 1 resource"; }
CTRL_FIXED="$(awsl iam list-role-tags --role-name "$CTRL_ROLE_NAME" --query "Tags[?Key=='installation'].Value | [0]" --output text)"
[ "$CTRL_FIXED" = "$CTRL_INSTALLATION" ] \
  || fail "control: the role's installation tag is \"$CTRL_FIXED\" after reconverging, not \"$CTRL_INSTALLATION\""
log "  control drift and reconverge: exactly one object proposed and fixed"

log ""
log "STAGE 3b (control): PASS - stages 3, 4 and 5 all clear once, and only"
log "once, the two unadmitted *_exclusive blocks are removed. #334 is the"
log "whole of what blocks this estate."
log ""
fi

# ══════════════════════════════════════════════════════════════════════════
# STAGES 4 and 5: NOT REACHED
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGES 4 and 5: NOT RUN ==="
log "  Both need a genuinely empty first plan from the UNMODIFIED module,"
log "  which stage 3 does not reach. Stage 3b above is a control on a cut-down"
log "  copy and is deliberately NOT counted as a pass for stage 4 or 5 in"
log "  live/corpus-crossing-manifest.json."
log ""

log "=== RESULT: cold_deploy PASS, migrate PASS, test_plan BLOCKED (#334), ==="
log "=== test_apply and drift_reconverge NOT RUN, against                  ==="
log "=== giantswarm/giantswarm-aws-account-prerequisites v8.2.2's own      ==="
log "=== unmodified crossplane/ module, .tofu extension throughout         ==="
