#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator:
# .corpus/rds/examples/complete-postgres, from terraform-aws-modules/
# terraform-aws-rds (pinned in live/corpus-manifest.json at tag v7.2.1,
# commit 9920097a4). RDS is one of the most commonly deployed AWS services
# via Terraform, Postgres is the most common engine choice, and this module
# is the de facto standard way people provision it - the configuration an
# average user copies when they first reach for RDS. It had never been
# crossed against a cloud before this script existed; #102 only ever used
# it for a static, offline refusal-ranking measurement.
#
# DEFECT A (choudoufu, FIXED - cec3c4b9b1). `choudoufu live-import`
# originally considered root-module managed resource instances ONLY
# (internal/live/liveimport/ratify.go: "if !mod.Addr.IsRoot() { ...
# continue }", cited in its own doc comment as "see issue #59"). Every
# single resource in this estate - all 39 of them - lives inside a child
# module (module.vpc, module.security_group, module.db, module.db_default),
# because that is how virtually every reusable, realistic Terraform
# configuration is written. Issue #59 ("Epic: admit child modules") is
# CLOSED, and identity/discovery/stamp/lint/mv all walked module trees
# already (59b static, 59c keyed for_each) - but liveimport's own
# restriction was never lifted to match. This crossing's first run found
# that regression directly: "0 of 0 resource instance(s) are eligible for
# stamping" and "39 resource instance(s) in a non-root module were not
# considered". cec3c4b9b1 (found independently crossing terraform-aws-
# modules/terraform-aws-lambda's "simple" example, live/e2e/corpus-lambda-
# simple/run.sh) removed the root-only skip; Ratify now walks every module.
# Re-verified against this estate below: migrate now stamps 26 of 39
# resources for real, confirmed by reading the primary DB instance's tags
# directly through the AWS CLI.
#
# FOUR IDENTITY-LAYER DEFECTS, filed and now ALL FIXED, hit for real in
# stage 3 below (test plan) one after another - each fix REVEALED the next
# wall rather than unblocking the estate outright, HANDOFF.md's own
# "clearing one refusal exposes another underneath" shape:
#
#   #304 (identity: count-index-in-tag can't trace a static lookup() into a
#   module's own bundled table), FIXED - `69038634d0`, merged `9aaca0ee10`.
#   A real live-plan against the really-migrated estate originally refused
#   exactly 7 sites under rule count-index-in-tag ("count.index is not
#   available in resource arguments"), all of them aws_security_group_rule.
#   ingress_with_cidr_blocks (this estate's own ingress rule, and a
#   near-universal terraform-aws-modules/security-group pattern): the
#   identity-bearing arguments are built through `lookup(var.
#   ingress_with_cidr_blocks[count.index], "from_port", var.rules[lookup(
#   var.ingress_with_cidr_blocks[count.index], "rule", "_")][0])` - a
#   lookup() whose DEFAULT branch is itself a nested lookup()-keyed index
#   into another variable, both fully static (a user-supplied literal plus
#   the module's own bundled rules table, no managed resource involved
#   anywhere in the chain). `StaticEvaluator.EvaluateStructural` (internal/
#   lang/eval.go) now validates each reference inside a list-of-objects
#   module argument individually instead of treating the whole variable as
#   one pass/fail unit, and only lets a refused reference's absence pass
#   silently when the value actually being computed comes back wholly
#   known anyway. count-index-in-tag: 7 -> 0 here, confirmed by the real
#   re-run below.
#
#   Fixing #304 did not unblock the estate - it REVEALED two further walls
#   #304's own refusal had been masking (the block's instance count itself
#   could not be determined before the fix, so its arguments were never
#   even reached): 19 "Identity not resolvable from configuration" sites
#   (18 element(<resource>[*].id, count.index)/element(coalescelist(...),
#   idx) sites across module.vpc's aws_route_table_association public/
#   private/database families, + 1 concat(splat, splat, [literal])[N] via
#   a local for security_group_id) and 14 "Module output not supported in
#   static context"/"Unable to compute static value" sites (7 each). Stage
#   3 went 7 -> 33 diagnostics at that point - a real rise in the raw
#   count, and exactly HANDOFF's own "'entries WORSE must be 0' is not the
#   gate it reads as" shape: a refusal that fired once at the block level
#   starts firing per instance and per argument once the block's own count
#   resolves, which is more information, not a regression.
#
#   #321 (identity: element(<resource>[*].id, count.index) refuses even
#   though the source resource's expansion and every instance's own tofu-
#   address marker are both statically known), FIXED - `626ca84739` (merge
#   of `c33a47288a` + `b7f1ef3183`). internal/live/identity/splat.go's new
#   resolveElementCall resolves the shape structurally to the same sibling
#   instance a direct R[idx].attr traversal already resolves - no
#   injectivity proof needed, since this selects a live object via its own
#   marker rather than writing a value into a tag. Filed against
#   corpus-security-group-complete; re-verified for real against THIS
#   estate as a second, independent generalization: 15 of the 18 element()
#   sites gone (all 9 subnet_id sites across public/private/database, plus
#   6 of 9 route_table_id sites for public and private). The remaining 3
#   (database's route_table_id, wrapped in coalescelist()) and the 1
#   concat() site fell outside this fix's own applicability check and were
#   filed as #324.
#
#   #324 (identity: element(coalescelist(...), idx) and concat(splat,...)
#   [N] via a local - two further list-combinator shapes resolveElementCall
#   doesn't reach), FIXED, both items - item 2 (concat via a local)
#   `80d3766b3e`, merged `79ffbe4732`; item 1 (coalescelist) `c25957cbdf`,
#   merged `49744a5617`. resolveConcatIndex and resolveElementCoalescelist
#   (both internal/live/identity/splat.go) generalize the same provable-
#   length reasoning to concat()'s flattened argument list and
#   coalescelist()'s first-provably-non-empty-argument selection. Verified
#   for real against this estate: item 2 cleared aws_security_group_rule.
#   ingress_with_cidr_blocks[*].security_group_id; item 1 cleared all 3
#   database.route_table_id sites. "Identity not resolvable from
#   configuration": 19 -> 0 here, confirmed absent in the real run below.
#
#   #323 (identity: tolerantVariables covers count/for_each key-set
#   resolution only, not per-attribute identity-VALUE rendering), FIXED -
#   `3d62366625`, merged `fb95168e63`. resolveExpr gained a third caller of
#   tolerantRetry (tolerantPart, internal/live/identity/partialargs.go),
#   accepting a retried value only when it comes back wholly known,
#   non-null and carries no mark anywhere - the same discipline #304's fix
#   applies one layer up (count/for_each domains), now reused for a plain
#   identity-value argument. Verified for real against this estate:
#   "Module output not supported in static context" / "Unable to compute
#   static value" go 7+7 -> 1+1.
#
# So stage 3 goes 7 diagnostics (masked by #304) -> 33 (revealed once #304
# lifted the mask) -> 14 (#321+#324 clear every "Identity not resolvable"
# site) -> 2 (#323 narrows the module-output cascade to its one genuinely
# unknowable leaf), and stays BLOCKED at exactly 2 sites - both tracing to
# the identical single leaf:
#
#   main.tf:198, in module "security_group": cidr_blocks = module.vpc.
#   vpc_cidr_block
#
# module.vpc's own vpc_cidr_block output (.corpus/vpc/outputs.tf:21-24) is
# `try(aws_vpc.this[0].cidr_block, null)` - a MANAGED RESOURCE's own
# attribute, read through a module output rather than a data source. This
# is #313's root cause B (see corpus-security-group-complete/run.sh's own
# header for the full ruling): a data source is safe to read
# unconditionally during plan - that is what a data block IS, and
# live-plan has made real ReadDataSource calls since #179 - while a
# managed resource's attribute may not exist yet within the same plan, so
# it stays refused on purpose. corpus-security-group-complete hits the
# identical family directly (aws_security_group.app.id, no module-output
# indirection in between) and is left at 7 sites for the same reason. Two
# independent estates, two different concrete syntaxes, one
# maintainer-scoped boundary - see HANDOFF.md's "This is no longer a
# one-estate question." Not attempted here: revisiting the scope of #313's
# ruling is a maintainer design call, not a script fix.
#
# #305 (admission: aws_default_network_acl/aws_default_route_table/
# aws_default_security_group were unadmitted) is FIXED. aws_default_network_
# acl, aws_default_route_table and aws_default_security_group - the VPC
# module's "adopt the account's default objects" resources, created by this
# estate exactly as terraform-aws-modules/vpc creates them for most of its
# users - are now ratified server-assigned in the admission table, the same
# shape as their non-default siblings aws_network_acl/aws_route_table/
# aws_security_group, and resolve through their own tofu-address marker
# once stamped. This estate's applied INSTANCE count of default_* adopters
# is 3 (see stage 1's resource list); an earlier draft of this script
# counted 5, including aws_default_vpc and aws_vpn_gateway_attachment
# declared at count = 0 in module.vpc blocks this estate's own variables
# never enable, which bc9ef26638 ("a resource block with a provably-zero
# count/for_each has no instance to refuse admission on", already on main)
# stopped from refusing at all - moot now that the 3 real sites resolve
# cleanly too.
#
# TWO REAL FLOCI GAPS (genuine emulator gaps, not choudoufu bugs, filed and
# worked around with documented deltas so stage 1 can still stand the
# estate up for real):
#
#   floci-io/floci#51 (via the lex00/floci fork). aws_db_instance_
#   automated_backups_replication has no matching RDS action
#   (Start/StopDBInstanceAutomatedBackupsReplication). DELTA 2 removes that
#   module and the KMS key that only fed it.
#
#   lex00/floci#52. SecretsManager RotateSecret unconditionally requires a
#   Lambda ARN, but manage_master_user_password_rotation = true - the
#   module's own default posture - creates an RDS-managed secret that uses
#   AWS's Lambda-less "hosted rotation" and never has one, so the apply
#   hangs retrying an InvalidRequestException. DELTA 3 disables it.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, verified, unmarked infrastructure.
#   stage 2  migrate       PASS - real: 26 of 39 resource instances stamped
#                          for real (20 VERIFIED + 6 DRIFTED), the other 13
#                          correctly skipped (13 UNTAGGABLE by provider
#                          schema; #305's default_* trio is admitted now and
#                          stamped above), all asserted against live-import's
#                          own report AND confirmed independently through
#                          the AWS CLI.
#   stage 3  test plan     BLOCKED, for real, at exactly 2 sites (was 7,
#                          then 33 once #304's mask lifted, then 14 once
#                          #321+#324 cleared every "Identity not resolvable"
#                          site, then 2 once #323 narrowed the remaining
#                          module-output cascade) - #304, #305, #321, #324
#                          and #323 no longer contribute any refusal. What
#                          remains is #313's root cause B alone (a same-plan
#                          resource attribute crossing a module boundary),
#                          deliberately out of scope by the maintainer's own
#                          ruling on #313. Specific counts and the exact
#                          diagnostic text asserted against a real live-plan
#                          run on the really-migrated estate, state file
#                          deleted first, with a BREAK=1 negative control.
#   stage 4  test apply    NOT RUN - depends on stage 3, which does not
#                          produce a clean plan while #313's root cause B
#                          stands.
#   stage 5  drift/reconverge  NOT RUN - depends on stages 3-4.
#
# A partial, honestly-reported pass is the point: this is the real, current
# behavior of choudoufu against a real, popular module, not a green claim
# routed around the truth.
#
#   bash live/e2e/corpus-rds-complete-postgres/run.sh
#
# Needs Docker, the AWS CLI, terraform (real, stock terraform - stage 1 is
# deliberately NOT choudoufu, to prove the estate is real and buildable on
# its own), and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4720, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt every expected stage-3 site count -
#                #304's count-index-in-tag, #305's unadmitted-type,
#                #321+#324's Identity-not-resolvable, and #313 root cause
#                B's own remaining Module-output/Unable-to-compute counts -
#                proving those assertions are load-bearing rather than a
#                grep that always matches. Stages 1 and 2 are unaffected
#                and still pass; stage 3 is the one that must fail.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice - once for the cold, unmarked
# deploy, once for the migration attempt) and every delta below lands on a
# copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/rds"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4720}"
FLOCI_NAME="choudoufu-corpus-rds-complete-postgres-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="rds-complete-postgres"
REGION="eu-west-1"
INSTANCES=39
ELIGIBLE=26
SKIPPED=13

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# copy_tree DEST - the rds module root plus examples/complete-postgres,
# preserving the relative layout the example's `source = "../../"` needs.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/rds/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/rds/"
  cp -R "$SRC/examples/complete-postgres" "$dest/rds/examples/complete-postgres"
  rm -rf "$dest/rds/examples/complete-postgres/.terraform" \
         "$dest/rds/examples/complete-postgres/.terraform.lock.hcl" \
         "$dest/rds/examples/complete-postgres/terraform.tfstate" \
         "$dest/rds/examples/complete-postgres/terraform.tfstate.backup"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
[ -d "$SRC/examples/complete-postgres" ] || fail "$SRC/examples/complete-postgres is missing - run 'just corpus-fetch' first"

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

PLAIN="$WORK/plain"
copy_tree "$PLAIN"
PLAIN_EST="$PLAIN/rds/examples/complete-postgres"
log "  estate copied out of .corpus into $PLAIN_EST"

# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 39 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 1' "$PLAIN_EST/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"
log "  DELTA 1  emulator flags on the provider block             (onboarding)"

# DELTA 2, EMULATOR GAP (floci-io/floci#51): aws_db_instance_automated_
# backups_replication has no matching RDS action in floci. The KMS key
# existed only to feed it, so it goes too.
perl -0pi -e 's/provider "aws" \{\n  alias  = "region2"\n  region = local\.region2\n\}\n\nmodule "kms" \{.*?\n\}\n\nmodule "db_automated_backups_replication" \{.*?\n\}\n\n/# DELTA 2 (EMULATOR GAP, floci-io\/floci#51): region2 provider, the kms\n# module, and db_automated_backups_replication removed.\n# aws_db_instance_automated_backups_replication calls RDS\n# StartDBInstanceAutomatedBackupsReplication, which floci does not implement.\n\n/s' "$PLAIN_EST/main.tf"
grep -q 'DELTA 2' "$PLAIN_EST/main.tf" || fail "DELTA 2 did not match the automated-backups-replication block - the corpus pin has moved"
grep -q 'module "kms"' "$PLAIN_EST/main.tf" && fail "DELTA 2 left the kms module behind"
log "  DELTA 2  automated-backups-replication + kms removed      (EMULATOR GAP, floci-io/floci#51)"

# DELTA 3, EMULATOR GAP (lex00/floci#52): floci's SecretsManager RotateSecret
# unconditionally requires a Lambda ARN. RDS-managed secret rotation
# (manage_master_user_password_rotation = true, the module's own default
# posture alongside manage_master_user_password) is Lambda-less on real AWS,
# so the apply hangs retrying an InvalidRequestException against floci.
perl -pi -e 's/^(  manage_master_user_password_rotation)(\s*)= true$/$1$2= false # DELTA 3 (EMULATOR GAP, lex00\/floci#52)/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 3' "$PLAIN_EST/main.tf" || fail "DELTA 3 did not match manage_master_user_password_rotation - the corpus pin has moved"
log "  DELTA 3  manage_master_user_password_rotation disabled    (EMULATOR GAP, lex00/floci#52)"

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"rds"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"rds"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (rds) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

log "=== 1b. terraform init + apply ==="
( cd "$PLAIN_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "plain terraform init failed"; }
PLAIN_APPLY_OUT="$(cd "$PLAIN_EST" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY_OUT" | tail -60
  fail "the plain terraform apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$PLAIN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT"; fail "the apply did not create exactly $INSTANCES resources - the corpus pin or the emulator has moved"; }
[ -f "$PLAIN_EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"
log "  $(grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT")"
log "  real terraform.tfstate, zero choudoufu markers - VPC, security group,"
log "  2 RDS Postgres instances (module.db + module.db_default), parameter"
log "  group, 2 CloudWatch log groups, enhanced-monitoring IAM role"

# Confirmed unmarked: read the primary DB instance's tags directly, never
# through choudoufu.
DB_ARN="$(awsl rds describe-db-instances --db-instance-identifier complete-postgresql \
  --query 'DBInstances[0].DBInstanceArn' --output text)"
[ -n "$DB_ARN" ] && [ "$DB_ARN" != "None" ] || fail "could not find the complete-postgresql DB instance through the AWS CLI"
MARKER_COUNT="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" \
  --query "length(TagList[?Key=='tofu-address'])" --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the DB instance already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $DB_ARN carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/rds/examples/complete-postgres"
# Carry the same three deltas so the adopted config is otherwise identical
# to what is actually standing (module structure has to match for live-plan
# to resolve the same addresses).
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$ADOPTED_EST/main.tf"
perl -0pi -e 's/provider "aws" \{\n  alias  = "region2"\n  region = local\.region2\n\}\n\nmodule "kms" \{.*?\n\}\n\nmodule "db_automated_backups_replication" \{.*?\n\}\n\n/# DELTA 2 (EMULATOR GAP, floci-io\/floci#51)\n\n/s' "$ADOPTED_EST/main.tf"
perl -pi -e 's/^(  manage_master_user_password_rotation)(\s*)= true$/$1$2= false # DELTA 3 (EMULATOR GAP, lex00\/floci#52)/' "$ADOPTED_EST/main.tf"

# DELTA 4, onboarding: add the live block. record_store is needed for
# module.db_default's random_id.snapshot_identifier (an effects-only
# resource - see the record-store fixture; skip_final_snapshot defaults to
# false and module.db_default does not override it).
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \">= 6.28\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 4 did not match versions.tf - the corpus pin has moved"
log "  DELTA 4  live block + local record_store added             (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - DEFECT A's fix (cec3c4b9b1) may have regressed, or this estate's resource shape has moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

# The eligible/skipped split, asserted by category so a shift in WHICH
# resources land where (not just the totals) is caught too.
VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "20" ] || fail "expected 20 VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "6" ] || fail "expected 6 DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "13" ] || fail "expected 13 UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "0" ] || fail "expected 0 UNADMITTED_TYPE (#305 fixed), got ${UNADMITTED_N:-0}"
# #305 fixed: the default_* trio (3 sites) is now admitted, so it must
# appear in the eligible (VERIFIED or DRIFTED) block, never in
# UNADMITTED_TYPE (which no longer exists in this estate's report at all).
ELIGIBLE_BLOCK="$(sed -n '/^VERIFIED (/,/^UNTAGGABLE (/p' <<< "$IMPORT_OUT")"
for addr in 'module.vpc.aws_default_network_acl.this[0]' 'module.vpc.aws_default_route_table.default[0]' \
            'module.vpc.aws_default_security_group.this[0]'; do
  grep -qF "$addr" <<< "$ELIGIBLE_BLOCK" || fail "expected $addr among VERIFIED/DRIFTED (#305 fixed) - not found"
done
log "  $ELIGIBLE of $INSTANCES eligible (20 VERIFIED + 6 DRIFTED); $SKIPPED skipped"
log "  (13 UNTAGGABLE by provider schema); #305's default_* trio is admitted"
log "  now and eligible above; nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, 0 failed, $SKIPPED skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ELIGIBLE of $INSTANCES resources cleanly"; }
log "  $ELIGIBLE stamped, 0 failed, $SKIPPED skipped - matches the dry run exactly"

log "=== 2c. the primary DB instance's marker, read through the AWS CLI directly ==="
WANT_DB_ADDR="module.db.module.db_instance.aws_db_instance.this:0"
GOT_DB_ADDR="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" \
  --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_DB_ADDR" = "$WANT_DB_ADDR" ] || fail "the primary DB instance carries tofu-address=$GOT_DB_ADDR, not $WANT_DB_ADDR"
GOT_DB_ESTATE="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" \
  --query "TagList[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_DB_ESTATE" = "$ESTATE" ] || fail "the primary DB instance carries tofu-estate=$GOT_DB_ESTATE, not $ESTATE"
log "  $DB_ARN now carries tofu-address=$GOT_DB_ADDR tofu-estate=$GOT_DB_ESTATE"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file - live-import above never wrote one (cloud tags"
log "  only), and stage 2 never ran a plain-terraform apply in this"
log "  directory, so this asserts what is already true rather than deleting"
log "  something that would otherwise be there"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - #313's root cause B may be fixed too now; update this script"; }
# choudoufu wraps its "In module.X, ... RESOURCE.NAME:" context lines at a
# fixed column when captured non-interactively, sometimes splitting the
# resource name onto its own line. Flattened to one line per diagnostic
# clause (blank-line-separated) so a substring match is not at the mercy of
# where the wrap happened to land.
PLAN_FLAT="$(awk 'BEGIN{RS=""} {gsub(/\n/," "); print; print "@@CLAUSE@@"}' <<< "$PLAN_OUT")"

# #304 fixed: zero count-index-in-tag sites may appear any more -
# aws_security_group_rule.ingress_with_cidr_blocks's lookup()-into-its-
# own-rules-table pattern (a fully static expression - a literal plus the
# module's own bundled rules table, no managed resource involved anywhere
# in the chain) resolves now even though the old rule refused it.
WANT_CIDX_N=0
# #305 fixed: no default_* type may appear as unadmitted.
WANT_DEFAULT_N=0
WANT_TYPES=(aws_default_network_acl aws_default_route_table aws_default_security_group)
# #321+#324 fixed: no "Identity not resolvable from configuration" site
# (element(<resource>[*].id, count.index), element(coalescelist(...), idx)
# or concat(splat,...)[N] via a local, over a tagged sibling's own known
# expansion) may appear any more.
WANT_UNRESOLVABLE_N=0
# #313 root cause B, deliberately UNFIXED - a same-plan resource attribute
# (aws_vpc.this[0].cidr_block) crossing module.vpc's own output boundary
# into module.security_group's var.ingress_with_cidr_blocks. #323 narrowed
# this cascade from 7+7 to exactly 1+1: the module-output diagnostic
# itself, plus the single site that actually reads the tainted leaf
# (aws_security_group_rule.ingress_with_cidr_blocks[0].cidr_blocks) -
# from_port/to_port/protocol on the same resource resolve fine, since
# #323's tolerantPart now keeps every OTHER leaf of the same list-of-
# objects argument strict.
WANT_MODOUT_N=1
WANT_CASCADE_N=1
if [ "${BREAK:-}" = "1" ]; then
  WANT_CIDX_N=1
  WANT_DEFAULT_N=1
  WANT_UNRESOLVABLE_N=1
  WANT_MODOUT_N=2
  WANT_CASCADE_N=2
  log "  BREAK=1: expecting 1 count-index-in-tag site (there should be 0 -"
  log "           #304 is fixed), 1 unadmitted-type site (there should be 0"
  log "           - #305 is fixed), 1 Identity-not-resolvable site (there"
  log "           should be 0 - #321+#324 are fixed), 2 Module-output-not-"
  log "           supported sites and 2 Unable-to-compute-static-value"
  log "           sites (one more each than the real 1+1, #313 root cause"
  log "           B). None of these are real. This step must fail."
fi

CIDX_N="$(grep -c '^Error: count.index is not available in resource arguments$' <<< "$PLAN_OUT")"
[ "$CIDX_N" = "$WANT_CIDX_N" ] || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected $WANT_CIDX_N count-index-in-tag sites (#304 fixed), got $CIDX_N"; }
log "  #304 confirmed fixed: zero count-index-in-tag sites - aws_security_"
log "  group_rule.ingress_with_cidr_blocks's lookup()-into-its-own-rules-"
log "  table pattern now resolves even though it is fully static."

DEFAULT_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
[ "$DEFAULT_N" = "$WANT_DEFAULT_N" ] || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected $WANT_DEFAULT_N unadmitted-type sites (#305 fixed), got $DEFAULT_N"; }
# #305 fixed: none of the three default-object types may appear among the
# unadmitted-type refusals any more - each resolves through its own
# tofu-address marker instead (stage 2 stamped all three sites).
for t in "${WANT_TYPES[@]}"; do
  grep -qE "In module\.[a-z_]+, ${t}\." <<< "$PLAN_FLAT" \
    && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "$t still appears among the unadmitted-type refusals - #305 is not actually fixed"; }
done
log "  #305 confirmed fixed: zero unadmitted-type sites - all three default-"
log "  object adopters this estate creates (aws_default_network_acl,"
log "  aws_default_route_table, aws_default_security_group) now resolve"
log "  via their own tofu-address marker."

UNRESOLVABLE_N="$(grep -c '^Error: Identity not resolvable from configuration$' <<< "$PLAN_OUT")"
[ "$UNRESOLVABLE_N" = "$WANT_UNRESOLVABLE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_UNRESOLVABLE_N 'Identity not resolvable from configuration' sites (#321+#324 fixed), got $UNRESOLVABLE_N"; }
! grep -qF 'element(aws_subnet' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|element\('; fail "#321's splat-through-element root cause is back on subnet_id - it must contribute no diagnostic at all"; }
! grep -qF 'element(aws_route_table' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|element\('; fail "#321's splat-through-element root cause is back on route_table_id - it must contribute no diagnostic at all"; }
! grep -qF 'coalescelist' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|coalescelist'; fail "#324's coalescelist root cause is back on database.route_table_id - it must contribute no diagnostic at all"; }
! grep -qF 'concat(' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|concat\('; fail "#324's concat root cause is back on security_group_id - it must contribute no diagnostic at all"; }
log "  #321+#324 confirmed fixed: zero Identity-not-resolvable sites -"
log "  element(<resource>[*].id, count.index), element(coalescelist(...),"
log "  idx) and concat(splat, splat, [literal])[N] via a local all resolve"
log "  structurally now, confirmed absent by name above."

# #313 root cause B, the sole remaining wall - deliberately UNFIXED.
MODOUT_N="$(grep -c '^Error: Module output not supported in static context$' <<< "$PLAN_OUT")"
CASCADE_N="$(grep -c '^Error: Unable to compute static value$' <<< "$PLAN_OUT")"
[ "$MODOUT_N" = "$WANT_MODOUT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_MODOUT_N 'Module output not supported in static context' sites (#313 root cause B), got $MODOUT_N"; }
[ "$CASCADE_N" = "$WANT_CASCADE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_CASCADE_N 'Unable to compute static value' sites (#313 root cause B's cascade), got $CASCADE_N"; }
grep -qF 'cidr_blocks = module.vpc.vpc_cidr_block' <<< "$PLAN_OUT" \
  || fail "expected the exact main.tf:198 'cidr_blocks = module.vpc.vpc_cidr_block' source line among the diagnostics - the corpus pin may have moved"
log "  #313 root cause B confirmed the sole remaining wall: exactly"
log "  $MODOUT_N Module-output-not-supported site + $CASCADE_N Unable-to-"
log "  compute-static-value cascade site, both tracing to main.tf line 198's"
log "  cidr_blocks = module.vpc.vpc_cidr_block - module.vpc's own"
log "  vpc_cidr_block output reads aws_vpc.this[0].cidr_block, a managed"
log "  resource's own attribute crossing a module boundary, deliberately"
log "  out of scope per the maintainer's ruling on #313 (same family as"
log "  corpus-security-group-complete's own remaining 7 sites)."

log ""
log "STAGE 3 (test_plan): BLOCKED for real, at exactly $((MODOUT_N + CASCADE_N)) sites"
log "(was 7, then 33 once #304's mask lifted, then 14 once #321+#324"
log "cleared) - #304, #305, #321 and #324 are all fixed and confirmed"
log "absent above; #313's root cause B (deliberately out of scope) is the"
log "sole remaining wall"
log ""
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          BLOCKED at 2 sites (was 7, then 33, then 14) - #304, #305, #321, #324 fixed; #313 root cause B remains (choudoufu, see header)"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "39 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
