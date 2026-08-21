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
#   BREAK         set to 1 to corrupt stage 2's identity assertion.
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, EMPTY + identities by value
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
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
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
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
log ""

log "=== PASS: all five stages, real, against giantswarm/giantswarm-aws-account- ==="
log "=== prerequisites v8.2.2's own unmodified crossplane/ module, .tofu         ==="
log "=== extension throughout, with both *_exclusive enforcers admitted (#334)   ==="
