#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-security-group's flagship "complete"
# example (.corpus/security-group/examples/complete, pinned in
# live/corpus-manifest.json at tag v6.0.0, commit 58d8e895), crossed through
# choudoufu against floci - the real, five-stage pipeline (cold deploy,
# migrate, test plan, test apply, drift and reconverge). This module is a
# common dependency of many other terraform-aws-modules (rds, eks and others
# wire security groups through it), and a prior crossing
# (corpus-rds-complete-postgres) already found and filed a real defect
# through it (#304) as a side effect of a different estate - this script is
# the first time the module's own flagship example is crossed directly.
#
# v6.0.0 rewrote this module from the classic single-`aws_security_group`-
# with-dynamic-`ingress`-blocks shape to one `aws_security_group` plus
# per-rule `aws_vpc_security_group_ingress_rule`/`egress_rule` resources
# (for_each over a rules map), `aws_vpc_security_group_vpc_association` for
# cross-VPC association, and `aws_vpc_security_group_rules_exclusive` for
# drift enforcement. #304's pattern (a static `lookup()`-keyed `count.index`
# into `ingress_with_cidr_blocks`) is the OLD shape - it does not exist
# anywhere in this example, because v6.0.0 dropped that variable and its
# dynamic-block loop entirely in favor of the for_each-over-a-map shape
# above. So this crossing does NOT hit #304 - a different, real gap in the
# same family surfaced instead (see DEFECT B below), which is exactly the
# "if you hit something different, that's new" case.
#
# 68 resources: the root `security_group` module (1 SG, 7 ingress rules, 1
# egress rule, 1 vpc_association, 1 rules_exclusive), its `postgresql`
# preset submodule (1 SG, 2 ingress rules from setproduct(preset, cidr), 1
# egress rule, 1 rules_exclusive), its `consul` preset submodule (1 SG, 22
# ingress rules from setproduct(11 presets, {cidr, referenced_sg}), 1
# rules_exclusive), a standalone `aws_security_group.app` referenced by
# `mysql-from-app`/`consul`'s referenced_security_group_id ingress rules, an
# `aws_ec2_managed_prefix_list.dns` referenced by a prefix-list ingress
# rule, two `disabled_*` modules at `create = false` (0 instances), and two
# `terraform-aws-modules/vpc` registry module calls (`vpc`, `vpc_secondary`,
# resolving to v6.6.1 - the same version and module
# live/e2e/corpus-vpc-complete/run.sh crosses - each contributing 1 VPC + 3
# private subnets + 3 route tables + 3 associations + the default_* adopter
# trio).
#
# DEFECT A (floci, EMULATOR GAP, filed lex00/floci#57). EC2
# AssociateSecurityGroupVpc ("security groups for multiple VPCs", the action
# behind aws_vpc_security_group_vpc_association) has no floci handler:
# "UnsupportedOperation: Operation AssociateSecurityGroupVpc is not
# supported." DELTA 2 removes the estate's one `vpc_associations` block
# (module.security_group's "secondary" entry) so the other 67 resources can
# stand up for real; `module.vpc_secondary` itself is left in place (nothing
# else in the example depends on removing it) and still applies cleanly on
# its own.
#
# #305 (aws_default_network_acl/route_table/security_group - the vpc
# module's default-object adopters, which this estate creates via its two
# nested terraform-aws-vpc calls, 6 sites) is FIXED: all three types are now
# ratified server-assigned in the admission table, the same shape as their
# non-default siblings aws_network_acl/aws_route_table/aws_security_group,
# and resolve through their own tofu-address marker once stamped.
#
# DEFECT B (choudoufu, real gap, distinct from #304/#305 - filed
# #307, NOW FIXED). aws_vpc_security_group_rules_exclusive was not
# in the admission table (internal/live/identity/table_generated.go) and the
# pinned AWS provider release (6.59.0) ships no resource identity schema for
# it either (live/survey-full.json: "identity_schema": false, path "moves to
# Ops"), so identity.Report could not settle it and it was a hard
# unadmitted-type refusal at every one of its 3 instances in this estate (the
# main, postgresql and consul security groups each get one, since
# enable_exclusive_rules defaults to true). This is NOT #305 (fixed, see
# above) and NOT #304 (a static lookup()-into-count.index pattern that does
# not exist anywhere in this v6.0.0 example). What was recoverable without a
# provider identity schema: the resource's own import documentation is
# unambiguous - "import exclusive management of security group rules using
# the security_group_id" - and security_group_id is the type's one required,
# ForceNew argument, always a direct reference to the aws_security_group
# resource it governs. This is the same shape as
# aws_vpc_security_group_vpc_association's own admitted row (client-supplied
# arguments only, composed over a tagged parent's identity), just without a
# provider-shipped identity schema to derive it from mechanically - doc-only
# admission, not schema-only. FIXED by ratifying the row row-gen's own
# classifier already proposed from live/import-grammar.json's scraped Import
# section (tools/row-gen/ratified.json's "aws_vpc_security_group_
# rules_exclusive": bucketClientNamed, ArgName security_group_id) - no
# extractor or classifier change needed, the row-gen pipeline already
# produced this exact proposal via classifyUnmapped + tryGrammarComposite's
# single-argument branch; it had simply never been ratified. Now resolves
# via identity.Report reading security_group_id directly; it has no tags
# argument, though, so it reads UNTAGGABLE rather than eligible for
# stamping (see stage 2's own log).
#
# #313 (choudoufu, config-language-subset wall, NOT an admission gap -
# filed #313). With #305 and #307 both fixed, live-plan no longer refuses
# on "Resource type is outside the live-markers subset" for ANY type here -
# but stage 3 still does not produce a clean plan. #313 was filed for the
# 239 diagnostics it hit at that point, and had TWO independent root causes.
#
# #313 root cause A (data source), FIXED. This estate's main.tf line 22
# computes `local.azs = slice(data.aws_availability_zones.available.names,
# 0, 3)`, which both nested terraform-aws-vpc calls use in per-AZ
# for_each/count expressions throughout their own subnet/route-table
# resources. That was never an architecture question: live-plan has read
# data sources through a real ReadDataSource RPC since #179
# (internal/live/dataread, wired at internal/command/live_plan.go's
# statelessDataReads). The value simply could not CROSS a module call.
# internal/live/identity's resolver.callerVariables rebuilt a module
# instance's var.* closure only when some call on the path carried its own
# count or for_each; module "vpc" carries neither, so var.azs was answered
# by the closure internal/configs froze at load time, which by construction
# has never seen a data lookup. dataread classified the source readable and
# read it for real; resolution then refused the child's count anyway.
# Fixed by resolver.frozenClosureIsStale, which now also rebuilds when an
# ancestor module instance carries read coverage. 50 sites -> 0 here, and
# 57 of the 204 .corpus directories with an eligible demanded source
# improved on the same change.
#
# #313 root cause B (resource attribute), NOW FIXED - and the paragraph
# below, kept verbatim, is what this file said about it right up until it
# was. Read both: the framing was half right, and the half that was wrong is
# the interesting half. module.consul's map does read
# `aws_security_group.app.id` directly, that value genuinely cannot be
# resolved from configuration, and it still is not - nothing here decided
# the managed-attribute question. What was wrong is the assumption that the
# estate needed the value at all. The consul submodule crosses eleven preset
# names in its own default against one caller key to build 22 ingress rules;
# every key is written down, and the rules are tagged, server-assigned
# resources that resolve through their own markers. The unknowable leaf was
# refusing a key set the configuration states outright, two module calls
# further down, because internal/live/identity/partialargs.go's tolerant
# rebuild could not compose across more than ONE module call and could not
# answer an argument written as merge() rather than as a constructor. 2
# "Dynamic value in static context" + 5 cascaded sites -> 0. See #191 and
# the fixtures modulearg-nested-partial / modulearg-nested-dynkey.
#
# What that framing said, before the measurement: module.consul's
# ingress_referenced_security_group_id map uses `aws_security_group.app.id`
# directly. A data source is safe to read unconditionally - that is what a
# data block IS - while a managed resource's attribute may not exist yet
# within the same plan, so it stays refused. 2 "Dynamic value in static
# context" sites + 5 cascaded "Unable to compute static value" sites.
#
# NEWLY REVEALED by fixing A, tracked separately as #321, and now FIXED: 12
# "Identity not resolvable from configuration" sites, all
# `element(aws_subnet.private[*].id, count.index)` and
# `element(aws_route_table.private[*].id, ...)` in the vpc module's
# aws_route_table_association.private (6 instances x 2 identity
# arguments). These were not new behavior and not a regression - before A
# was fixed the block's own count refused, so it never expanded and its
# arguments were never reached. This was HANDOFF.md's documented "a refusal
# that fired once at the block level starts firing per argument" shape: a
# splat through element(), which identity resolution could not follow.
# internal/live/identity/splat.go's resolveElementCall now recognizes
# element(R[*].attr, idx) structurally as "instance idx (wrapped modulo R's
# own instance count, exactly as element() itself wraps it) of R, attribute
# attr" - the same live object a direct R[idx].attr traversal
# (resolveIndexedTraversal) already resolves, reached through element()'s
# second spelling. Both aws_subnet.private and aws_route_table.private are
# server-assigned (tagged) resources with a provably-known expansion, so no
# injectivity proof is needed: this is not a value written into a tag, it is
# a reference to a specific tagged sibling instance. 12 -> 0 here.
#
# What #321 explicitly left open, not attempted there or here: a
# configuration where the block holding element(<resource-splat>,
# count.index) has its OWN count knowable without a data read hits
# internal/live/lint/count_index.go's RuleCountIndex FIRST (it refuses
# count.index inside any collection-accessor call on sight, and its
# domain-render fallback never accepts a managed-resource-rooted
# collection), which gates the whole plan before resolution ever runs -
# resolveElementCall only fires here because this block's own count is
# itself gated behind #313's data source, which lint's earlier, data-read-
# free scan cannot see either, so lint treats the block as having no
# instances (admission.go's blockHasNoInstances) and skips it. See
# splat.go's own comment for the details and the open question (whether an
# argument that maps several siblings onto the same parent is still safe
# once the OTHER identity component that varies is considered).
#
# NEWLY REACHED by fixing #313 root cause B, filed as #332, NOT FIXED here.
# With every analysis-layer refusal gone, live-plan gets all the way to
# PROJECTION and actually imports all 67 resources through the provider. Two
# fail, both aws_default_route_table, one per nested vpc call: 2 "empty
# result" + 2 "Cannot import for projection". Same shape as #321 one layer
# further out - the wall was always there, nothing had ever reached it.
#
# It is neither a choudoufu analysis gap nor a floci gap. The provider
# imports aws_default_route_table by the VPC's id (its Import section:
# "import Default VPC route tables using the vpc_id", example vpc-33cc44dd),
# and tools/row-gen/ratified.json overrode that text on the reasoning that
# the resource's schema has no vpc_id ARGUMENT. It has none - but vpc_id is
# a computed ATTRIBUTE, and the importer looks the VPC's main route table up
# by it. Proved with stock terraform 1.15.8 and the real AWS provider 6.59.0
# against this same floci, no choudoufu in the loop:
#
#   terraform import aws_default_route_table.x rtb-d70fbe5fd3315bbad
#     -> Error: empty result                                      (exit 1)
#   terraform import aws_default_route_table.x vpc-dc10ae31
#     -> Import successful!                                       (exit 0)
#
# A route table's ARN carries no VPC id, so the ARN-composition path
# (internal/live/discovery's importIDFromARN / arnCompositeImportID) cannot
# reach it; discovery has to carry a second attribute off the object it
# already found by marker. The rest of the default_* family
# (network_acl, security_group, vpc, subnet, vpc_dhcp_options) all document
# import by their own id and are unaffected - this is a singleton, checked
# against the doc cache rather than assumed.
#
# So stage 3 goes 239 diagnostics -> 19 -> 7 -> 4, every analysis-layer
# refusal is at zero and asserted by absence, and what blocks the estate is
# one wrong ratified import identity. Assertions below hold the exact counts
# so a change to any of them is visible.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, unmarked infrastructure, 67 of the
#                          module's 68 resources (DELTA 2, #57).
#   stage 2  migrate       PASS - real: 58 of 67 resource instances stamped
#                          for real (39 VERIFIED + 19 DRIFTED - most of the
#                          drift is a provider-normalized
#                          referenced_security_group_id format difference at
#                          read time, and the two default network ACLs drift
#                          on subnet_ids/egress/ingress because the module
#                          never sets subnet_ids and AWS auto-associates
#                          every subnet in the VPC to it; see stage 2's own
#                          log), the rest correctly skipped (9 UNTAGGABLE -
#                          aws_route_table_association x6, no tags argument,
#                          + the 3 rules_exclusive instances, #307 fixed but
#                          still untaggable - 0 UNADMITTED_TYPE; #305's
#                          default_* trio is admitted and stamped above),
#                          asserted against live-import's own report AND
#                          confirmed independently through the AWS CLI.
#   stage 3  test plan     BLOCKED, for real, at 4 sites (was 239, then 19,
#                          then 7). Every analysis-layer wall - #305, #307,
#                          #313 root causes A and B, #321 - contributes zero
#                          refusals and each zero is asserted by absence.
#                          What remains is #332: aws_default_route_table's
#                          ratified import identity is the route table's own
#                          id where the provider wants the VPC's, so the
#                          projection's own import of it fails, 2 + 2 sites.
#                          The reading this line used to carry, kept because
#                          it was wrong in an instructive way: "#313's root
#                          cause B alone: a same-plan resource attribute
#                          (2 + 5 cascade), deliberately out of scope by the
#                          maintainer's own ruling on #313."
#                          Specific counts and resource addresses asserted
#                          against a real live-plan run, state file deleted
#                          first, BREAK=1 negative control.
#   stage 4  test apply    NOT RUN - depends on stage 3.
#   stage 5  drift/reconverge  NOT RUN - depends on stages 3-4.
#
#   bash live/e2e/corpus-security-group-complete/run.sh
#
# Needs Docker, the AWS CLI, terraform (real, stock terraform - stage 1 is
# deliberately NOT choudoufu) on PATH, network access for `terraform init`
# to resolve terraform-aws-modules/vpc from the registry (same as
# corpus-vpc-complete), and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4721, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected stage-3 site counts and
#                one expected unadmitted-type name, proving those
#                assertions are load-bearing. Stages 1 and 2 are unaffected;
#                stage 3 is the one that must fail.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice) and every delta lands on a copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/security-group"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4721}"
FLOCI_NAME="choudoufu-corpus-security-group-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="security-group-complete-crossing"
REGION="eu-west-1"
INSTANCES=67
ELIGIBLE=58
SKIPPED=9

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# copy_tree DEST - the security-group module root (incl. every preset
# submodule) plus examples/complete, preserving the relative layout the
# example's `source = "../../"` / `"../../modules/*"` need.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/security-group/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/security-group/"
  cp -R "$SRC/examples/complete" "$dest/security-group/examples/complete"
  rm -rf "$dest/security-group/examples/complete/.terraform" \
         "$dest/security-group/examples/complete/.terraform.lock.hcl" \
         "$dest/security-group/examples/complete/terraform.tfstate" \
         "$dest/security-group/examples/complete/terraform.tfstate.backup"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
[ -d "$SRC/examples/complete" ] || fail "$SRC/examples/complete is missing - run 'just corpus-fetch' first"

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
PLAIN_EST="$PLAIN/security-group/examples/complete"
log "  estate copied out of .corpus into $PLAIN_EST"

# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 67 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 1' "$PLAIN_EST/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"
log "  DELTA 1  emulator flags on the provider block             (onboarding)"

# DELTA 2, EMULATOR GAP (lex00/floci#57): EC2 AssociateSecurityGroupVpc has
# no floci handler. Removes the one vpc_associations block; module.vpc_
# secondary itself is left standing (nothing else depends on removing it).
perl -0pi -e 's/\n  vpc_associations = \{\n    secondary = \{\n      vpc_id = module\.vpc_secondary\.vpc_id\n    \}\n  \}\n\n/\n  # DELTA 2 (EMULATOR GAP, lex00\/floci#57): cross-VPC association removed.\n  # aws_vpc_security_group_vpc_association calls EC2 AssociateSecurityGroupVpc,\n  # which floci does not implement.\n\n/' "$PLAIN_EST/main.tf"
grep -q 'DELTA 2' "$PLAIN_EST/main.tf" || fail "DELTA 2 did not match the vpc_associations block - the corpus pin has moved"
grep -q '^  vpc_associations = {' "$PLAIN_EST/main.tf" && fail "DELTA 2 left a vpc_associations block behind"
log "  DELTA 2  vpc_associations removed                         (EMULATOR GAP, lex00/floci#57)"

# Pin the provider version for reproducibility, same discipline
# corpus-vpc-complete uses (this checkout's admission tables were generated
# against 6.59.0).
perl -0pi -e 's/version = ">= 6\.29"/version = "= 6.59.0"/' "$PLAIN_EST/versions.tf"
grep -q '= 6.59.0' "$PLAIN_EST/versions.tf" || fail "the version pin did not match versions.tf - the corpus pin has moved"

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
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
log "  real terraform.tfstate, zero choudoufu markers - 4 security groups"
log "  (main + app + postgresql preset + consul preset), 31 ingress rules,"
log "  2 egress rules, 3 rules_exclusive enforcers, 1 managed prefix list,"
log "  2 VPCs with 3 private subnets/route tables/associations each, plus"
log "  each VPC's default_* adopter trio"

# Confirmed unmarked: read the main security group's tags directly, never
# through choudoufu.
MAIN_SG_ID="$(terraform -chdir="$PLAIN_EST" output -raw security_group_id)"
[ -n "$MAIN_SG_ID" ] && [ "$MAIN_SG_ID" != "None" ] || fail "could not read the main security group's id from terraform output"
MARKER_COUNT="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the main security group already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $MAIN_SG_ID carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/security-group/examples/complete"
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$ADOPTED_EST/main.tf"
perl -0pi -e 's/\n  vpc_associations = \{\n    secondary = \{\n      vpc_id = module\.vpc_secondary\.vpc_id\n    \}\n  \}\n\n/\n  # DELTA 2 (EMULATOR GAP, lex00\/floci#57): cross-VPC association removed.\n\n/' "$ADOPTED_EST/main.tf"
perl -0pi -e 's/version = ">= 6\.29"/version = "= 6.59.0"/' "$ADOPTED_EST/versions.tf"

# DELTA 3, onboarding: add the live block. No record_store needed - this
# estate has no effects-only (null_resource/time_*/random_*) resources.
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \"= 6\.59\.0\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 3 did not match versions.tf - the corpus pin has moved"
log "  DELTA 3  live block added                                  (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
printf '%s\n' "$IMPORT_OUT" > /tmp/import_out_full2.txt
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - this estate's resource shape has moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "39" ] || fail "expected 39 VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "19" ] || fail "expected 19 DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "9" ] || fail "expected 9 UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "0" ] || fail "expected 0 UNADMITTED_TYPE, got ${UNADMITTED_N:-0}"
# #305 fixed: the default_* trio (6 sites, both module.vpc and
# module.vpc_secondary) is now admitted, so it must appear in the eligible
# (VERIFIED or DRIFTED) block.
ELIGIBLE_BLOCK="$(sed -n '/^VERIFIED (/,/^UNTAGGABLE (/p' <<< "$IMPORT_OUT")"
UNTAGGABLE_BLOCK="$(sed -n '/^UNTAGGABLE (/,/^$/p' <<< "$IMPORT_OUT")"
for addr in 'module.vpc.aws_default_network_acl.this[0]' 'module.vpc.aws_default_route_table.default[0]' \
            'module.vpc.aws_default_security_group.this[0]' 'module.vpc_secondary.aws_default_network_acl.this[0]' \
            'module.vpc_secondary.aws_default_route_table.default[0]' 'module.vpc_secondary.aws_default_security_group.this[0]'; do
  grep -qF "$addr" <<< "$ELIGIBLE_BLOCK" || fail "expected $addr among VERIFIED/DRIFTED (#305 fixed) - not found"
done
# #307 fixed: aws_vpc_security_group_rules_exclusive is now admitted
# (client-named off its own security_group_id argument), so it no longer
# reads as UNADMITTED_TYPE - identity.Report resolves it. It has no tags
# argument in the provider schema, though, so it lands in UNTAGGABLE
# instead: known and correctly skipped for lack of a place to carry a
# marker, not unknown outright.
for addr in 'module.consul.module.security_group.aws_vpc_security_group_rules_exclusive.this[0]' \
            'module.postgresql.module.security_group.aws_vpc_security_group_rules_exclusive.this[0]' \
            'module.security_group.aws_vpc_security_group_rules_exclusive.this[0]'; do
  grep -qF "$addr" <<< "$UNTAGGABLE_BLOCK" || fail "expected $addr among UNTAGGABLE (#307 fixed)"
done
log "  $ELIGIBLE of $INSTANCES eligible (39 VERIFIED + 19 DRIFTED); $SKIPPED skipped"
log "  (9 UNTAGGABLE - aws_route_table_association x6, no tags argument, +"
log "  the 3 rules_exclusive instances (#307 fixed: now admitted, still"
log "  untaggable) - 0 UNADMITTED_TYPE); #305's default_* trio (6 sites,"
log "  both module.vpc and module.vpc_secondary) is admitted and eligible"
log "  above; nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, 0 failed, $SKIPPED skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ELIGIBLE of $INSTANCES resources cleanly"; }
log "  $ELIGIBLE stamped, 0 failed, $SKIPPED skipped - matches the dry run exactly"

log "=== 2c. the main security group's marker, read through the AWS CLI directly ==="
WANT_SG_ADDR="module.security_group.aws_security_group.this:0"
GOT_SG_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
[ "$GOT_SG_ADDR" = "$WANT_SG_ADDR" ] || fail "the main security group carries tofu-address=$GOT_SG_ADDR, not $WANT_SG_ADDR"
GOT_SG_ESTATE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-estate" --query "Tags[0].Value" --output text)"
[ "$GOT_SG_ESTATE" = "$ESTATE" ] || fail "the main security group carries tofu-estate=$GOT_SG_ESTATE, not $ESTATE"
log "  $MAIN_SG_ID now carries tofu-address=$GOT_SG_ADDR tofu-estate=$GOT_SG_ESTATE"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - the remaining stage-3 walls may be fixed; update this script"; }

# EVERY analysis-layer refusal is now gone from this estate, and each of
# these four zeros is a separate, once-real wall asserted by absence.
#
#   #305/#307   "Resource type is outside the live-markers subset"
#   #313 A + B  "Dynamic value in static context" and its
#               "Unable to compute static value" cascade
#   #321        "Identity not resolvable from configuration"
#
# #313 root cause B was the last of them and was recorded here, and in
# HANDOFF.md, as a maintainer scope decision rather than a bug: module.consul's
# ingress_referenced_security_group_id map reads aws_security_group.app.id,
# a same-plan managed-resource attribute. That framing was half right. The
# VALUE genuinely is not resolvable and still is not resolved - nothing here
# reads a managed resource's own attribute out of configuration. What was
# wrong is that the value was never needed: the map's KEYS are eleven preset
# names in the consul submodule's own default crossed with one caller key,
# every one of them written down, and the rules they name are tagged,
# server-assigned resources that resolve through their own markers. One
# unknowable leaf was refusing a key set the configuration states outright,
# two module calls further down. internal/live/identity/partialargs.go's
# tolerant rebuild already substituted an unknown for exactly that leaf; it
# could not compose across more than ONE module call, and could not answer an
# argument the caller wrote as merge() rather than as a constructor. Both are
# fixed, and the leaf itself still refuses - see the fixtures
# modulearg-nested-partial and modulearg-nested-dynkey.
WANT_UNADMITTED_N=0
WANT_DYNAMIC_N=0
WANT_STATIC_CASCADE_N=0
WANT_UNRESOLVABLE_N=0
# What that newly REACHED, in the same "a refusal that fired at the block
# level starts firing per argument" shape #321 arrived in - except one layer
# further out, because the plan now gets all the way to PROJECTION and
# actually imports every one of the 67 resources. Two of them fail, both
# aws_default_route_table, one per nested VPC call.
#
# This is not a choudoufu analysis gap and not a floci gap. The provider
# imports aws_default_route_table by the VPC's id, not by the route table's
# own - its Import section says so ("import Default VPC route tables using
# the vpc_id", vpc-33cc44dd in the example) - and the ratified row in
# tools/row-gen/ratified.json overrode that text on the reasoning that the
# resource's schema has no vpc_id ARGUMENT. It does not, but vpc_id is a
# computed ATTRIBUTE and the provider's importer looks up the VPC's main
# route table by it. Proved with stock terraform 1.15.8 and the real AWS
# provider 6.59.0 against this same floci, no choudoufu involved:
#
#   terraform import aws_default_route_table.x rtb-d70fbe5fd3315bbad
#     -> Error: empty result                                (exit 1)
#   terraform import aws_default_route_table.x vpc-dc10ae31
#     -> Import successful!                                 (exit 0)
#
# "empty result" is byte-identical to what live-plan reports here. Filed as
# #332; not fixable from the ARN alone (a route table's ARN carries no VPC
# id), so it needs discovery to carry a second attribute off the object it
# already found by marker. aws_default_network_acl, aws_default_security_group,
# aws_default_vpc, aws_default_subnet and aws_default_vpc_dhcp_options all
# document import by their own id and are unaffected - this is a singleton.
WANT_PROJECTION_IMPORT_N=2
WANT_EMPTY_RESULT_N=2
if [ "${BREAK:-}" = "1" ]; then
  WANT_UNADMITTED_N=1
  WANT_DYNAMIC_N=1
  WANT_STATIC_CASCADE_N=1
  WANT_UNRESOLVABLE_N=1
  WANT_PROJECTION_IMPORT_N=3
  WANT_EMPTY_RESULT_N=3
  log "  BREAK=1: expecting one site of each analysis-layer refusal (there"
  log "           should be 0 of all four now - #305, #307, #313 A and B,"
  log "           and #321 are all fixed and asserted by absence), and 3"
  log "           each of the projection-import and empty-result sites (one"
  log "           more than the real 2). None of these are real. This step"
  log "           must fail."
fi

UNADMITTED_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
[ "$UNADMITTED_N" = "$WANT_UNADMITTED_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected $WANT_UNADMITTED_N unadmitted-type sites (#305 and #307 both fixed), got $UNADMITTED_N"; }
# Belt-and-suspenders: neither #305's default_* trio nor #307's
# rules_exclusive may appear in any diagnostic's declaring-line code frame.
# A PROJECTION diagnostic carries no code frame, so this stays exactly as
# tight as it was even though aws_default_route_table does now appear in the
# output by name - see WANT_PROJECTION_IMPORT_N above and the assertion
# below, which is what covers that.
for t in aws_default_network_acl aws_default_route_table aws_default_security_group aws_vpc_security_group_rules_exclusive; do
  N="$(grep -cF "resource \"$t\"" <<< "$PLAN_OUT")"
  [ "$N" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected $t to raise no ANALYSIS refusal (both #305 and #307 fixed), but it still appears in a diagnostic's declaring-line code frame"; }
done

# Every analysis-layer refusal, at zero. These four are the walls this estate
# has been blocked on since it was first crossed, and each is asserted at an
# exact count rather than by a floor, so one coming back is visible.
DYNAMIC_N="$(grep -c '^Error: Dynamic value in static context$' <<< "$PLAN_OUT")"
STATIC_CASCADE_N="$(grep -c '^Error: Unable to compute static value$' <<< "$PLAN_OUT")"
[ "$DYNAMIC_N" = "$WANT_DYNAMIC_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_DYNAMIC_N 'Dynamic value in static context' sites (#313 A and B both cleared), got $DYNAMIC_N"; }
[ "$STATIC_CASCADE_N" = "$WANT_STATIC_CASCADE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_STATIC_CASCADE_N 'Unable to compute static value' sites (#313's cascade), got $STATIC_CASCADE_N"; }
UNRESOLVABLE_N="$(grep -c '^Error: Identity not resolvable from configuration$' <<< "$PLAN_OUT")"
[ "$UNRESOLVABLE_N" = "$WANT_UNRESOLVABLE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_UNRESOLVABLE_N 'Identity not resolvable from configuration' sites (the splat-through-element class #313's fix newly reached), got $UNRESOLVABLE_N"; }

# #332, the wall clearing the four above newly REACHED. Asserted at an exact
# count and by the type it names, so this cannot be satisfied by some other
# import failing instead.
PROJECTION_IMPORT_N="$(grep -c '^Error: Cannot import for projection$' <<< "$PLAN_OUT")"
EMPTY_RESULT_N="$(grep -c '^Error: empty result$' <<< "$PLAN_OUT")"
[ "$PROJECTION_IMPORT_N" = "$WANT_PROJECTION_IMPORT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_PROJECTION_IMPORT_N 'Cannot import for projection' sites (#332, aws_default_route_table imports by vpc_id), got $PROJECTION_IMPORT_N"; }
[ "$EMPTY_RESULT_N" = "$WANT_EMPTY_RESULT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_EMPTY_RESULT_N 'empty result' sites (#332, the provider's own error for a route-table id it cannot look a VPC up by), got $EMPTY_RESULT_N"; }
if [ "${BREAK:-}" != "1" ]; then
  N="$(grep -c 'aws_default_route_table' <<< "$PLAN_OUT")"
  [ "$N" -eq "$WANT_PROJECTION_IMPORT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|aws_default_route_table'; fail "expected aws_default_route_table to be named by exactly $WANT_PROJECTION_IMPORT_N diagnostics (#332) and nothing else, got $N"; }
fi

# #313 root cause A, asserted by ABSENCE. This is the load-bearing half of
# the fix: the data source's value now crosses the module call, so not one
# diagnostic may name it. If this ever comes back, the ancestor-coverage
# rebuild in internal/live/identity's resolver.frozenClosureIsStale has
# regressed, and every per-AZ subnet, route table and association in both
# nested vpc calls is unresolvable again.
! grep -qF 'aws_availability_zones' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|aws_availability_zones'; fail "#313's data.aws_availability_zones root cause is back - it must contribute no diagnostic at all"; }
# ... and by presence of what it unblocked: the per-AZ resources whose
# count/for_each reads local.azs through module.vpc's own var.azs are gone
# from the diagnostics entirely.
for t in aws_subnet aws_route_table; do
  N="$(grep -cE "^ *[0-9]+: *resource \"$t\"" <<< "$PLAN_OUT")"
  [ "$N" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:'; fail "$t still appears among live-plan's diagnostics; #313's data-source fix should have resolved every per-AZ instance"; }
done

# #313 root cause B, asserted by ABSENCE. This assertion used to demand the
# opposite - that the diagnostic BE present, because the file recorded it as
# a maintainer scope decision. It is gone, and nothing about a managed
# resource's own attribute was decided to make it go: the estate never needed
# aws_security_group.app.id's VALUE, only the key set it travels with. If
# this comes back, partialargs.go's composition across two module calls has
# regressed and 22 consul ingress rules are unnameable again.
! grep -qF 'Unable to use aws_security_group.app in static context' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|aws_security_group.app'; fail "#313's root cause B is back on aws_security_group.app - it must contribute no diagnostic at all"; }

# #321, asserted by ABSENCE - the load-bearing half of this fix: neither
# element() index position on aws_route_table_association.private may
# contribute a diagnostic any more. If this ever comes back,
# resolveElementCall (internal/live/identity/splat.go) has regressed.
! grep -qF 'element(aws_subnet.private[*].id, count.index)' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|element\('; fail "#321's splat-through-element root cause is back on subnet_id - it must contribute no diagnostic at all"; }
! grep -qF 'element(aws_route_table.private[*].id' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|element\('; fail "#321's splat-through-element root cause is back on route_table_id - it must contribute no diagnostic at all"; }

log "  #305 and #307 confirmed BOTH fixed: zero unadmitted-type sites -"
log "  aws_default_network_acl/route_table/security_group and"
log "  aws_vpc_security_group_rules_exclusive all resolve (through their"
log "  own tofu-address marker, or - rules_exclusive - through"
log "  identity.Report reading security_group_id directly, since it has no"
log "  marker to read)."
log "  #313 root cause A confirmed FIXED: data.aws_availability_zones is"
log "  read by the pre-resolution data-read phase and its value now crosses"
log "  module.vpc's and module.vpc_secondary's own call boundary, so it"
log "  contributes zero diagnostics (was 50) and every per-AZ aws_subnet"
log "  and aws_route_table instance resolves."
log "  #321 confirmed FIXED: element(<resource>[*].id, count.index) on both"
log "  of aws_route_table_association.private's identity arguments now"
log "  resolves structurally to the same-indexed sibling instance - zero"
log "  diagnostics (was 12), confirmed absent above."
log "  #313 root cause B confirmed FIXED, and not by deciding the question it"
log "  was scoped out on: aws_security_group.app.id is STILL not resolved"
log "  from configuration. module.consul's 22 ingress rules are keyed by"
log "  eleven preset names and one caller key, all written down, and the"
log "  unknowable leaf now travels beside that key set instead of poisoning"
log "  it across two module calls - zero diagnostics (was 2 + 5)."
log "  Analysis-layer refusals, total: $((DYNAMIC_N + STATIC_CASCADE_N + UNRESOLVABLE_N + UNADMITTED_N)) (was 239, then 19, then 7)."
log "  Remaining, at $((PROJECTION_IMPORT_N + EMPTY_RESULT_N)) sites, newly REACHED rather than caused:"
log "    $PROJECTION_IMPORT_N + $EMPTY_RESULT_N  #332 - aws_default_route_table imports by the VPC's"
log "           id, not the route table's own, and the ratified row says"
log "           otherwise. One per nested vpc call. Proved against stock"
log "           terraform and the real provider, no choudoufu involved."

log ""
log "STAGE 3 (test_plan): BLOCKED for real, at 4 sites (was 239, then 19,"
log "then 7) - every analysis-layer wall this estate has ever hit (#305,"
log "#307, #313 A and B, #321) is fixed and confirmed absent above. What is"
log "left is one wrong ratified import identity, #332, reachable only now"
log "that the plan gets as far as importing all 67 resources"
log ""
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS (67 resources; DELTA 2, lex00/floci#57)"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          BLOCKED at 4 sites (was 239, then 19, then 7) - every analysis-layer wall is gone; #332 alone remains; see header"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "67 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
