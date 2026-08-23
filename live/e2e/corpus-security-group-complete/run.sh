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
# #332 (choudoufu, real defect in a ratified row) is FIXED. Fixing #313 root
# cause B let live-plan reach PROJECTION and actually import all 67 resources
# through the provider, and two then failed - both aws_default_route_table,
# one per nested vpc call: 2 "empty result" + 2 "Cannot import for
# projection". Same shape as #321 one layer further out; the wall was always
# there and nothing had ever reached it.
#
# It was neither an analysis gap nor a floci gap. The provider imports
# aws_default_route_table by the VPC's id (its Import section: "import
# Default VPC route tables using the vpc_id", example vpc-33cc44dd), and
# tools/row-gen/ratified.json overrode that text on the reasoning that the
# resource's schema has no vpc_id ARGUMENT. It has none - but vpc_id is a
# computed ATTRIBUTE, and the importer looks the VPC's main route table up by
# it. Proved with stock terraform 1.15.8 and the real AWS provider 6.59.0
# against this same floci, no choudoufu in the loop:
#
#   terraform import aws_default_route_table.x rtb-d70fbe5fd3315bbad
#     -> Error: empty result                                      (exit 1)
#   terraform import aws_default_route_table.x vpc-dc10ae31
#     -> Import successful!                                       (exit 0)
#
# The row is corrected (identity_attrs ["vpc_id"], import_syntax "vpc-ID")
# and discovery now recomposes the import identity off the vpc_id attribute
# of the object aws_route_table's own list call surfaced, rather than reusing
# the rtb-… id that list call was identified by. A route table's ARN carries
# no VPC id, so the ARN-composition path (importIDFromARN) genuinely cannot
# reach it and now declines rather than composing the wrong string. The rest
# of the default_* family (network_acl, security_group, vpc, subnet,
# vpc_dhcp_options) all document import by their own id and are unaffected -
# a singleton, checked against the doc cache rather than assumed.
#
# WHAT #332 NEWLY REACHED, one layer further out again: with every choudoufu
# refusal at zero the plan imports all 67 resources AND diffs them, and the
# AWS provider itself failed on exactly one, fatally, aborting the plan
# before a single diff line was ever printed:
#
#   Error: Provider produced invalid plan
#   Provider "…/hashicorp/aws" has indicated "requires replacement" on
#   module.security_group.aws_vpc_security_group_ingress_rule
#     .this["dns-from-prefix-list"]
#   for a non-existent attribute path cty.Path{cty.GetAttrStep{Name:""}}.
#   This is a bug in the provider, which should be reported in the provider's
#   own issue tracker.
#
# That is the provider's own words, confirmed against stock in step 1c: a
# plain `terraform plan`, no choudoufu anywhere, this estate's own real
# state, right after its own real apply, hits the byte-identical diagnostic.
# HANDOFF.md's third row - stock fails too - and it names no choudoufu code.
#
# NOW FIXED, choudoufu-side (2026-08-22). The diagnostic names an attribute
# path with an empty GetAttrStep - a step no schema, from any provider, ever
# defines, because attribute names are never empty identifiers. That is
# provably NOT the same thing as "replace the whole object" (which is the
# ZERO-length path, and already resolves without error) and provably not a
# real, if wrong, attribute name either (which stays a fatal error, unit-
# tested as a negative control). It is a plan modifier that built a path at
# runtime and never filled in which attribute it meant.
# internal/plans.RequiresReplacePathIsDegenerate is the shared rule
# (internal/tofu/node_resource_abstract_instance.go's plan(), the classic
# engine every choudoufu command still runs through, and
# internal/resources/managed_plan.go's not-yet-wired twin copy of the same
# filtering logic - both call sites, so the fix reaches every RequiresReplace
# diagnostic in either engine, for any resource type, any provider): a path
# whose only content is an empty attribute name is dropped from the replace
# set - never forcing a spurious destroy/create - with a loud WARNING
# instead of a fatal error, and the resource's real attributes are still
# compared for real changes on their own, independently of this one dropped
# signal, so a genuine, well-formed replace still forces a replace.
# internal/tofu/context_plan_test.go's
# TestContext2Plan_requiresReplaceMalformedPathDropped and
# TestContext2Plan_requiresReplaceBogusNamedPathStillErrors are the positive
# and negative cases; internal/resources/managed_plan_test.go carries the
# same pair for the second engine.
#
# Fixing that let the plan finish rendering for the first time ever - every
# earlier run of this script died on the fatal error before a single diff
# line was printed - and rendering revealed two things that fatal error had
# been hiding:
#
# FOUND AND FIXED, choudoufu's own test harness, not choudoufu's code
# (2026-08-22): most of the plan's diff was module.security_group's and
# module.consul's referenced_security_group_id rules showing
# "000000000000/sg-xxx" (live) -> "sg-xxx" (config) forever. Root-caused with
# an isolated repro against PLAIN STOCK TERRAFORM, no choudoufu involved: a
# lone self-referencing aws_vpc_security_group_ingress_rule against this same
# floci, with DELTA 1's skip_requesting_account_id = true, reproduces the
# identical byte-for-byte diff on every plan; the same repro with that one
# flag removed (letting the provider call STS, which floci answers with
# Account "000000000000" - the same UserId floci puts on the referenced
# group) plans clean. skip_requesting_account_id never let the provider's
# own (correct) same-account normalization see that the two account ids
# agree. Fixed by removing the flag from DELTA 1: not a choudoufu code
# change, a corrected test fixture, proven against stock. 13 sites (all-
# from-self, mysql-from-app, and 11 consul preset rules) -> 0.
#
# CONFIRMED, NOT choudoufu's, left as the estate's one real remaining
# defect, filed lex00/floci#102: dns-from-prefix-list's own replace.
# DescribeSecurityGroupRules never returns PrefixListId for a rule created
# with one - verified directly against floci with the AWS CLI, no terraform
# or choudoufu involved, the rule genuinely created with
# prefix_list_id = "pl-…" and every subsequent read answering null. Every
# fresh reader of that rule (stock's own refresh in step 1c, and choudoufu's
# live-plan alike) therefore sees prefix_list_id unset, and the provider's
# own (separate, correctly-named, not the bug above) ForceNew logic on that
# attribute reacts exactly as it should to what it was told: replace. Given
# what floci says, the replace is genuinely warranted; given the estate's
# real, unchanged configuration, it is not - and no marker, identity, or
# plan-validation code of choudoufu's decides any of it.
# aws_vpc_security_group_rules_exclusive.this[0] shows the same replace's
# one downstream consequence (its ingress_rule_ids list losing a
# known-after-apply id), not a second gap.
#
# ALSO SEEN, ON SOME RUNS, confirmed NOT this unit's to fix: as many as 21
# more changed objects, all pure tofu-slot completions - tags/tags_all
# gaining "tofu-slot" and nothing else - the same deliberate, cross-estate,
# ALREADY DOCUMENTED gap live/e2e/corpus-vpc-complete/run.sh's own header
# calls "THE TOFU-SLOT FINDING" (see also live/e2e/corpus-iam-policy/run.sh
# and internal/live/stamp/doc.go): a slot is a position in the full live
# set, which live-import's one-state-file view cannot compute, so it can be
# proposed fresh on the first post-migrate plan - but whether it actually IS
# proposed depends on exactly how migrate's own stamping and this plan's own
# read interleave, so the count is NOT stable run to run (one real run
# showed only the 1 floci-gap object below; another showed 25). The two
# default network ACLs, when they appear among the changed set, additionally
# carry their own PRE-EXISTING egress/ingress representation churn - called
# out in stage 2's own log above, present in this estate before any of
# today's work, unrelated to it.
#
# So stage 3 goes from 239 diagnostics -> 19 -> 7 -> 4 -> 1 fatal error, to 0
# fatal errors, 0 choudoufu differences of any kind, and a changed-object
# count that is NOT hardcoded below because it is not stable: the one
# constant, present on every run without exception, is the confirmed, filed
# floci gap and its one downstream consequence; everything else is tofu-slot
# noise this script logs but does not gate on, for the same reason corpus-
# vpc-complete's own stage 3 check does not gate on an exact total either.
# None of it is a choudoufu wall.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, unmarked infrastructure, 67 of the
#                          module's 68 resources (DELTA 2, #57). Step 1c
#                          additionally runs a plain stock `terraform plan`
#                          against this same real state, right after this
#                          same real apply, no choudoufu involved - now a
#                          clean plan (lex00/floci#102 fixed), with the
#                          residue being a separate, confirmed floci gap
#                          (lex00/floci#104) reproduced identically through
#                          plain stock terraform.
#   stage 2  migrate       PASS - real: 58 of 67 resource instances stamped
#                          for real (53 VERIFIED + 5 DRIFTED, now that the
#                          account-id fix resolved 13 of the 19
#                          referenced_security_group_id sites that used to
#                          drift, and lex00/floci#102 moved dns-from-prefix-
#                          list itself from DRIFTED to VERIFIED; the
#                          remaining 5 are the two default network ACLs'
#                          pre-existing churn (now root-caused to lex00/
#                          floci#104) and the main/postgresql/consul
#                          security groups' own cty-typed empty-set
#                          representation, which the real plan's diff engine
#                          - unlike migrate's own drift comparison - resolves
#                          as no change at all, confirmed in stage 3's own
#                          plan output), the rest correctly skipped (9
#                          UNTAGGABLE - aws_route_table_association x6, no
#                          tags argument, + the 3 rules_exclusive instances,
#                          #307 fixed but still untaggable - 0
#                          UNADMITTED_TYPE; #305's default_* trio is admitted
#                          and stamped above), asserted against live-import's
#                          own report AND confirmed independently through the
#                          AWS CLI.
#   stage 3  test plan     NOT EMPTY, for real, at a run-dependent changed-
#                          object count (see above), 0 of them choudoufu's.
#                          Every choudoufu wall - #305, #307, #313 root
#                          causes A and B, #321, #332, the malformed-
#                          RequiresReplace-path bug, and the account-id
#                          churn - contributes zero and each zero is
#                          asserted by absence or by an exact, checked count.
#                          The two default route tables' import identities
#                          are additionally re-derived from AWS itself and
#                          asserted by value (step 3a), because an absent
#                          diagnostic is not evidence that a marker is
#                          right. What remains, on every run without
#                          exception, is 1 confirmed, filed floci gap
#                          (lex00/floci#102) plus its 1 downstream
#                          consequence; some runs additionally show tofu-slot
#                          completions (cross-estate, documented, not this
#                          unit's) and the 2 default NACLs' own pre-existing
#                          churn.
#                          The required counts and addresses are asserted
#                          against a real live-plan run, state file deleted
#                          first, BREAK=1 negative control; the run-dependent
#                          total is logged, not gated on.
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

gauntlet_begin

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

CURRENT_STAGE=cold_deploy
# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 67 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block.
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$PLAIN_EST/main.tf"
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

# ── 1c. stock control: does STOCK's own plan hit the same provider bug? ────
#
# UPDATED 2026-08-22 against the combined lex00/floci image carrying #99,
# #100, #101, #102 and #103 (see live/floci-image): THE BUG THIS CONTROL
# WAS WRITTEN TO CONFIRM IS GONE. lex00/floci#102 fixed
# DescribeSecurityGroupRules to return PrefixListId for a rule created with
# one; dns-from-prefix-list's own read is no longer null, the AWS provider's
# ForceNew logic on that attribute has nothing bogus to react to, and the
# malformed cty.Path{cty.GetAttrStep{Name:""}} requires-replace step this
# control used to reproduce (byte-identical, through plain stock terraform,
# zero choudoufu in the loop - see the prior revision of this comment, kept
# in git history) does not appear at all, on either binary. That is a
# genuine confirmation, not an absence of evidence: the exact same command
# against the exact same real state, which used to fail every run without
# exception, now succeeds every run without exception.
#
# What stock's plan shows INSTEAD - still with zero choudoufu anywhere in
# the loop - is a separate, narrower, ALSO CONFIRMED floci gap: the two
# `aws_default_network_acl` resources (module.vpc's and
# module.vpc_secondary's) propose replacing their egress/ingress rule sets
# with byte-identical content on every single plan, forever. Root-caused at
# floci's own API level (no terraform or choudoufu involved): an entry
# created through `CreateNetworkAclEntry` (as opposed to a VPC's own
# auto-populated defaults) loses its CidrBlock/Ipv6CidrBlock field on every
# subsequent read - confirmed directly against a fresh floci container with
# the AWS CLI alone, filed as lex00/floci#104. Both sides of a Terraform
# `SetValue` element need every field to hash identically, so the field
# silently dropping on read makes the "current" and "desired" rule entries
# compare as two different set elements even though their config-declared
# content never changed, forcing a remove-then-add pair that never settles.
# This is HANDOFF's third row (stock fails too, in the sense of also
# showing the diff) and fourth row (the emulator is wrong) at once: the
# root cause is floci's, and the fix is floci's to make - not attempted
# here; #104 is filed and the residue is asserted below by exact address so
# the estate's own row stays honest about what is actually left.
log ""
log "=== 1c. stock control: stock's own plan, no choudoufu, same state ==="
STOCK_PLAN_OUT="$(cd "$PLAIN_EST" && terraform plan -input=false -no-color 2>&1)"
STOCK_PLAN_RC=$?
[ "$STOCK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_PLAN_OUT" | tail -60; fail "stock's own plan exited $STOCK_PLAN_RC - the malformed-path bug (lex00/floci#102, FIXED as of this image) may have come back, or something new broke"; }
grep -qF 'Error: Provider produced invalid plan' <<< "$STOCK_PLAN_OUT" \
  && { printf '%s\n' "$STOCK_PLAN_OUT" | tail -60; fail "stock's own plan still reproduces 'Provider produced invalid plan' - lex00/floci#102 may not be fixed in this image after all"; }
STOCK_NACL_N="$(grep -cE '^  # module\.(vpc|vpc_secondary)\.aws_default_network_acl\.this\[0\] will be updated' <<< "$STOCK_PLAN_OUT")"
[ "$STOCK_NACL_N" = "2" ] \
  || { printf '%s\n' "$STOCK_PLAN_OUT" | grep -E '^  # .+ will be|^Plan:'; fail "expected exactly 2 default-network-acl updates (lex00/floci#104) in stock's own plan, got $STOCK_NACL_N - the residue has moved"; }
log "  CONFIRMED: plain, stock terraform - no choudoufu, its own real state,"
log "  right after its own real apply - plans CLEANLY (exit 0), with the"
log "  malformed-path bug (lex00/floci#102) nowhere in it. The only diff"
log "  stock itself proposes is the 2 default-network-acl updates"
log "  (lex00/floci#104, filed - CreateNetworkAclEntry drops CidrBlock/"
log "  Ipv6CidrBlock on read), reproduced with zero choudoufu in the loop."

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "$INSTANCES resources (DELTA 2, lex00/floci#57)"
CURRENT_STAGE=migrate

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/security-group/examples/complete"
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$ADOPTED_EST/main.tf"
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
# UPDATED 2026-08-22 (combined floci image, #99-#103): dns-from-prefix-list
# itself moved from DRIFTED to VERIFIED - lex00/floci#102 fixed
# DescribeSecurityGroupRules to return PrefixListId for real, so its live
# read now matches the recorded state instead of reading back null. 52->53
# VERIFIED, 6->5 DRIFTED; the remaining 5 (the two default network ACLs'
# egress/ingress/subnet_ids and the main/postgresql/consul security groups'
# own cty-typed empty-set representation) are unrelated to today's fixes.
[ "${VERIFIED_N:-0}" = "53" ] || fail "expected 53 VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "5" ] || fail "expected 5 DRIFTED, got ${DRIFTED_N:-0}"
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
log "  $ELIGIBLE of $INSTANCES eligible (53 VERIFIED + 5 DRIFTED); $SKIPPED skipped"
log "  (9 UNTAGGABLE - aws_route_table_association x6, no tags argument, +"
log "  the 3 rules_exclusive instances (#307 fixed: now admitted, still"
log "  untaggable) - 0 UNADMITTED_TYPE); #305's default_* trio (6 sites,"
log "  both module.vpc and module.vpc_secondary) is admitted and eligible"
log "  above; nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $SKIPPED skipped." <<< "$APPROVE_OUT" \
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
gauntlet_stage migrate pass "$ELIGIBLE of $INSTANCES stamped"
CURRENT_STAGE=test_plan

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file"

plan_into() { ( cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"
PLAN_RC=$?
# live-plan now SUCCEEDS: the malformed-path bug (below) no longer aborts the
# run, so a nonzero exit here means something genuinely new is wrong.
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC unexpectedly - every choudoufu wall this estate has ever hit is fixed and asserted absent below, so a nonzero exit means the estate has moved"; }

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
# #332, FIXED. Clearing the four walls above let the plan get all the way to
# PROJECTION and actually import every one of the 67 resources, and two of
# them then failed - both aws_default_route_table, one per nested VPC call.
#
# That was never a choudoufu analysis gap and never a floci gap. The provider
# imports aws_default_route_table by the VPC's id, not by the route table's
# own - its Import section says so ("import Default VPC route tables using the
# vpc_id", vpc-33cc44dd in the example) - and the ratified row in
# tools/row-gen/ratified.json had overridden that text on the reasoning that
# the resource's schema has no vpc_id ARGUMENT. It has none, but vpc_id is a
# computed ATTRIBUTE and the provider's importer looks the VPC's main route
# table up by it. Proved with stock terraform 1.15.8 and the real AWS provider
# 6.59.0 against this same floci, no choudoufu involved:
#
#   terraform import aws_default_route_table.x rtb-d70fbe5fd3315bbad
#     -> Error: empty result                                (exit 1)
#   terraform import aws_default_route_table.x vpc-dc10ae31
#     -> Import successful!                                 (exit 0)
#
# Fixed by correcting the row (identity_attrs ["vpc_id"], import_syntax
# "vpc-ID") and by splitting discovery's own conflation of two facts. An
# aws_default_* type and its plain sibling share ONE live object -
# aws_route_table's list call returns the VPC's default route table alongside
# every other - and discovery used to prove that by requiring the two ratified
# rows to name the same import identity, which is the separate question of
# whether the id it already read carries forward. Those are now two predicates
# (defaultAdopterSiblings and sameRatifiedIdentity), and when they disagree the
# binding recomposes the import identity off the listed object's own vpc_id
# attribute - the same mechanism issue #302 already uses to read a
# service-linked role's arn off an object iam:ListRoles surfaced. No type name
# appears in that code.
WANT_PROJECTION_IMPORT_N=0
WANT_EMPTY_RESULT_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_UNADMITTED_N=1
  WANT_DYNAMIC_N=1
  WANT_STATIC_CASCADE_N=1
  WANT_UNRESOLVABLE_N=1
  WANT_PROJECTION_IMPORT_N=1
  WANT_EMPTY_RESULT_N=1
  log "  BREAK=1: expecting one site of each analysis-layer refusal and one"
  log "           each of the projection-import and empty-result sites."
  log "           There should be 0 of all six now - #305, #307, #313 A and"
  log "           B, #321 and #332 are all fixed and asserted by absence."
  log "           None of these are real. This step must fail."
fi

UNADMITTED_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
[ "$UNADMITTED_N" = "$WANT_UNADMITTED_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "expected $WANT_UNADMITTED_N unadmitted-type sites (#305 and #307 both fixed), got $UNADMITTED_N"; }
# Belt-and-suspenders: neither #305's default_* trio nor #307's
# rules_exclusive may appear in any diagnostic's declaring-line code frame.
# A PROJECTION diagnostic carries no code frame, so this stays exactly as
# tight as it was even though aws_default_route_table does now appear in the
# output by name - see WANT_PROJECTION_IMPORT_N above and the assertion
# below, which is what covers that.
#
# Matched against the diagnostic's own verbatim NUMBERED source snippet
# (`^ +[0-9]+: resource "type" "name" {`, the same shape #332's own
# crossing landed for corpus-giantswarm-crossplane's identical assertion
# bug), not a bare `grep -cF "resource \"$t\""` over the whole plan output.
# Once #332 let this estate's plan reach PROJECTION, an ordinary, non-error
# plan-diff line - `~ resource "aws_default_network_acl" "this" {`,
# reconciling a real DRIFTED resource migrate legitimately stamped - can
# contain the same substring a diagnostic's code frame does. Only the
# numbered form is a diagnostic; a plan diff line carries no line number.
for t in aws_default_network_acl aws_default_route_table aws_default_security_group aws_vpc_security_group_rules_exclusive; do
  N="$(grep -cE "^ +[0-9]+: resource \"$t\" \"" <<< "$PLAN_OUT")"
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
  # Same shape as the belt-and-suspenders loop above: matched against the
  # diagnostic's own verbatim NUMBERED source snippet, not a bare substring
  # count over the whole plan. Once #332 lets this type resolve correctly,
  # a genuinely DRIFTED aws_default_route_table instance shows up in the
  # plan's ORDINARY, non-error diff too (reconciling the tofu-address tag
  # migrate has not yet written for it) - `~ resource "aws_default_route_table"
  # "default" {` with no line number, which the analysis-layer counts above
  # (PROJECTION_IMPORT_N, EMPTY_RESULT_N, and the four-type loop) already
  # cover exhaustively. This check exists to catch the type reappearing in a
  # diagnostic OUTSIDE those already-named shapes, not to forbid an ordinary
  # diff line.
  N="$(grep -cE '^ +[0-9]+: resource "aws_default_route_table" "' <<< "$PLAN_OUT")"
  [ "$N" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|aws_default_route_table'; fail "expected aws_default_route_table to be named by no diagnostic at all (#332 fixed), got $N"; }
fi

# #313 root cause A, asserted by ABSENCE. This is the load-bearing half of
# the fix: the data source's value now crosses the module call, so not one
# diagnostic may name it. If this ever comes back, the ancestor-coverage
# rebuild in internal/live/identity's resolver.frozenClosureIsStale has
# regressed, and every per-AZ subnet, route table and association in both
# nested vpc calls is unresolvable again.
#
# Excludes the data source's own ordinary refresh log (`data.aws_
# availability_zones.available: Reading...`/`: Read complete after ...`) -
# once #332 lets the plan reach real execution, the data source is actually
# read as part of a normal, successful plan, and that progress log mentions
# the same name a diagnostic would. Only a mention OUTSIDE that shape is the
# thing this assertion exists to catch.
! grep -vE ': (Reading\.\.\.|Read complete)' <<< "$PLAN_OUT" | grep -qF 'aws_availability_zones' \
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


# #332's own wall let the plan reach PROJECTION and diff every resource for
# the first time - which is what let the malformed-RequiresReplace-path bug
# actually be REACHED, back when it still existed. lex00/floci#102 (in the
# image this script now pins) fixed the root cause outright: dns-from-
# prefix-list's own read no longer comes back with prefix_list_id unset, so
# the AWS provider's ForceNew logic on that attribute has nothing bogus to
# react to, and the malformed cty.Path{cty.GetAttrStep{Name:""}} step never
# gets generated at all. internal/plans.RequiresReplacePathIsDegenerate (the
# choudoufu-side tolerance for exactly that malformed shape, still load-
# bearing for any OTHER provider bug of the same shape) has nothing to catch
# here any more, and that is asserted below by absence, not skipped.
#
# What is asserted here: zero fatal errors of any kind, and zero of the
# malformed-requires-replacement-path warnings that used to fire on every
# run without exception.
WANT_TOTAL_ERR_N=0
WANT_MALFORMED_WARN_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_TOTAL_ERR_N=1
  WANT_MALFORMED_WARN_N=1
  log "  BREAK=1: expecting 1 fatal Error and 1 malformed-path warning"
  log "           (neither is real any more - lex00/floci#102 fixed the root"
  log "           cause, so the malformed path is never generated at all)."
  log "           This step must fail."
fi
TOTAL_ERR_N="$(grep -c '^Error: ' <<< "$PLAN_OUT")"
[ "$TOTAL_ERR_N" = "$WANT_TOTAL_ERR_N" ] || { printf '%s
' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_TOTAL_ERR_N fatal error(s), got $TOTAL_ERR_N - every choudoufu-side wall (#305, #307, #313 A and B, #321, #332) and the malformed-path bug (lex00/floci#102) are fixed and asserted absent above, so a fatal error here is new"; }
MALFORMED_WARN_N="$(grep -c '^Warning: Provider produced a malformed requires-replacement path$' <<< "$PLAN_OUT")"
[ "$MALFORMED_WARN_N" = "$WANT_MALFORMED_WARN_N" ] || { printf '%s
' "$PLAN_OUT" | grep -E '^Warning:' | sort | uniq -c; fail "expected $WANT_MALFORMED_WARN_N malformed-requires-replacement-path warning(s), got $MALFORMED_WARN_N - lex00/floci#102 may have regressed, or this shape has moved"; }

# The floci gap the malformed-path fix let through to the surface used to be
# CONFIRMED and filed (lex00/floci#102, DescribeSecurityGroupRules never
# returned PrefixListId) - fixed in the image this script now pins, and
# asserted fixed by absence: no site may propose replacing dns-from-prefix-
# list any more.
WANT_REPLACE_ADDR_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_REPLACE_ADDR_N=1
  log "  BREAK=1: expecting dns-from-prefix-list to need replacing (not"
  log "           real any more - lex00/floci#102 is fixed). This step must"
  log "           fail."
fi
REPLACE_DNS_N="$(grep -cF 'module.security_group.aws_vpc_security_group_ingress_rule.this["dns-from-prefix-list"] must be replaced' <<< "$PLAN_OUT")"
[ "$REPLACE_DNS_N" = "$WANT_REPLACE_ADDR_N" ] || { printf '%s
' "$PLAN_OUT" | grep -E '^  # .+ must be replaced$'; fail "expected $WANT_REPLACE_ADDR_N dns-from-prefix-list replace site(s), got $REPLACE_DNS_N - lex00/floci#102 may have regressed"; }

# Every OTHER changed object in this plan is logged, not asserted at an
# exact total: a slot is minted from the live set's own high-water mark
# (internal/live/slots/doc.go), and depending on exactly how migrate's
# stamping and this plan's own read interleave, this run may or may not
# still see every count-based resource proposing to complete its tofu-slot
# tag - THE SAME already-documented, cross-estate, DELIBERATE gap live/e2e/
# corpus-vpc-complete/run.sh's own header calls "THE TOFU-SLOT FINDING" (see
# also live/e2e/corpus-iam-policy/run.sh and internal/live/stamp/doc.go):
# live-import cannot mint a slot from a single state file, because a slot is
# a position in the full live set, which a per-resource state file view
# cannot see. A hardcoded count here would be exactly the kind of number
# that looks precise and is actually just this run's luck.
#
# What IS asserted at an exact count, and is NOT optional, is the residue
# lex00/floci#104 (filed, NOT fixed here - see step 1c) forces onto both
# `aws_default_network_acl` instances, present on every run without
# exception: CreateNetworkAclEntry drops CidrBlock/Ipv6CidrBlock on read, so
# both default network ACLs' rule sets never stop proposing to replace
# themselves with byte-identical content. Note the header comparison here
# does NOT anchor on the verb alone ("will be updated" always continues " in-
# place" in real terraform/tofu output; an earlier revision of this
# assertion anchored on end-of-line and could never match a real update
# header at all - fixed here, since a check that can never fire is worse
# than no check).
CHANGED_HEADERS="$(grep -E '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$PLAN_OUT")"
CHANGED_N="$(printf '%s
' "$CHANGED_HEADERS" | grep -c .)"
NACL_N="$(grep -cE '^  # module\.(vpc|vpc_secondary)\.aws_default_network_acl\.this\[0\] will be updated' <<< "$PLAN_OUT")"
[ "$NACL_N" = "2" ] || { printf '%s
' "$PLAN_OUT" | grep -E '^  # .+ will be|^Plan:'; fail "expected exactly 2 default-network-acl updates (lex00/floci#104), got $NACL_N - the residue has moved"; }
log "  $CHANGED_N object(s) changed this run (tofu-slot completions vary run"
log "  to run, per THE TOFU-SLOT FINDING above); the 2 that are NOT"
log "  optional, confirmed present on every run, are the default network"
log "  ACLs' byte-identical replace pair (lex00/floci#104, filed, not fixed):"
printf '%s
' "$CHANGED_HEADERS" | while IFS= read -r line; do [ -n "$line" ] && log "    $line"; done

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
log "  #332 confirmed FIXED: aws_default_route_table is imported by the VPC's"
log "  id, read off the vpc_id attribute of the object its sibling's list"
log "  call surfaced - zero diagnostics (was 2 + 2), and the type is named by"
log "  no diagnostic at all, asserted above."

# ── 3a. #332's identity, asserted BY VALUE against the AWS CLI ────────────
#
# An absent diagnostic is not evidence that a marker is right - HANDOFF.md's
# "a wrong marker outranks a missing one" is the standing reason this block
# exists, and it is the half that a verdict-level check cannot see. So the two
# default route tables' import identities are re-derived here from AWS itself,
# with no choudoufu in the loop:
#
#   1. find each VPC by its own marker (tofu-address names the module call),
#   2. ask AWS for THAT VPC's main route table,
#   3. assert the object it answers with is the one carrying the
#      aws_default_route_table marker for the same module call.
#
# Step 2 is exactly what the provider's importer does with the string
# choudoufu now hands it, so agreement here is the assertion on the rendered
# identity: the VPC id binds this object and no other. Before #332 the
# rendered identity was the rtb-… id, which the real provider answers "empty
# result" for. This runs regardless of the plan's exit code - it reads AWS,
# not the plan.
log "=== 3a. #332: each default route table's import identity, re-derived from AWS ==="
for call in vpc vpc_secondary; do
  VPC_ID="$(awsl ec2 describe-vpcs \
    --filters "Name=tag:tofu-address,Values=module.$call.aws_vpc.this:0" "Name=tag:tofu-estate,Values=$ESTATE" \
    --query 'Vpcs[0].VpcId' --output text)"
  [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "could not find module.$call's VPC by its own marker"

  # The import identity choudoufu resolves for
  # module.<call>.aws_default_route_table.default[0] IS this VPC id. Ask AWS
  # what that id imports to.
  MAIN_RTB="$(awsl ec2 describe-route-tables \
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=association.main,Values=true" \
    --query 'RouteTables[0].RouteTableId' --output text)"
  [ -n "$MAIN_RTB" ] && [ "$MAIN_RTB" != "None" ] || fail "AWS returned no main route table for $VPC_ID (module.$call)"

  # ... and the object it answered with must be the one this estate marked as
  # that module call's aws_default_route_table, not some other table.
  WANT_RTB_ADDR="module.$call.aws_default_route_table.default:0"
  GOT_RTB_ADDR="$(awsl ec2 describe-tags \
    --filters "Name=resource-id,Values=$MAIN_RTB" "Name=key,Values=tofu-address" \
    --query 'Tags[0].Value' --output text)"
  [ "$GOT_RTB_ADDR" = "$WANT_RTB_ADDR" ] \
    || fail "importing module.$call's default route table by $VPC_ID lands on $MAIN_RTB, which carries tofu-address=$GOT_RTB_ADDR, not $WANT_RTB_ADDR"

  # And the converse, which is what makes this an identity assertion rather
  # than an existence one: the route table's OWN id is a different string, so
  # the two are not accidentally interchangeable here.
  [ "$MAIN_RTB" != "$VPC_ID" ] || fail "the route table id and the VPC id are the same string; this assertion proves nothing"
  log "  module.$call: import identity $VPC_ID -> AWS's main route table $MAIN_RTB, carrying $GOT_RTB_ADDR"
done
log "  both default route tables bind by the VPC's id, confirmed against AWS"
log "  itself and never through choudoufu's own report"

log "  Analysis-layer refusals, total: $((DYNAMIC_N + STATIC_CASCADE_N + UNRESOLVABLE_N + UNADMITTED_N)) (was 239, then 19, then 7)."
log "  choudoufu refusals of every layer, total: 0 (#332's 4 were the last)."
log "  The malformed-RequiresReplace-path bug: root cause FIXED (lex00/"
log "  floci#102). It does not merely fail to abort the plan any more (0"
log "  fatal errors) - it does not fire at all any more (0 malformed-path"
log "  warnings), because dns-from-prefix-list's own read no longer comes"
log "  back with prefix_list_id unset. internal/plans."
log "  RequiresReplacePathIsDegenerate stays load-bearing for any OTHER"
log "  provider bug of the same shape, asserted by absence above."
log "  The referenced_security_group_id/account-id churn (13 sites: all-"
log "  from-self, mysql-from-app, 11 consul rules): FIXED, by correcting"
log "  DELTA 1 rather than choudoufu - skip_requesting_account_id kept the"
log "  provider from ever learning its own account id, so its own (correct)"
log "  same-account normalization could never fire. Proven with an isolated"
log "  repro against plain stock terraform, no choudoufu involved."
log "  Left, at $CHANGED_N changed object(s) this run (this total varies with"
log "  tofu-slot timing, see above): any tofu-slot completions present are"
log "  the same deliberate, cross-estate, already-documented gap corpus-vpc-"
log "  complete's own header calls THE TOFU-SLOT FINDING (not this unit's"
log "  wall), and the 2 that are NOT optional, present on every run without"
log "  exception, are the two default network ACLs' byte-identical replace"
log "  pair - now root-caused, not merely observed: lex00/floci#104 (filed,"
log "  not fixed), CreateNetworkAclEntry drops CidrBlock/Ipv6CidrBlock on"
log "  read, reproduced identically through plain stock terraform (step 1c),"
log "  zero choudoufu anywhere in the loop."

log ""
log "STAGE 3 (test_plan): NOT EMPTY, for real, at $CHANGED_N changed object(s)"
log "this run (0 of them a choudoufu refusal, a choudoufu identity defect, or"
log "anything else choudoufu's own code decides). Every wall this estate has"
log "ever hit (#305, #307, #313 A and B, #321, #332, the malformed-"
log "RequiresReplace-path bug, and the account-id churn) is fixed and"
log "confirmed absent above; both default route tables' import identities"
log "are asserted BY VALUE against AWS. What is left, on every run without"
log "exception, is one confirmed, filed floci gap (lex00/floci#104, a"
log "different one than yesterday's #102) forcing the two default network"
log "ACLs to churn; some runs additionally show tofu-slot completions"
log "(cross-estate, not this unit's) - see the header for the full"
log "accounting and the code paths that prove each claim."
log ""
gauntlet_stage test_plan fail "NOT EMPTY at $CHANGED_N changed object(s) this run, 0 of them choudoufu's: the malformed-RequiresReplace-path bug (lex00/floci#102) and the account-id/referenced_security_group_id churn are BOTH fixed and confirmed absent (0 fatal errors, 13 sites resolved); the 2 that are NOT optional, present on every run, are the default network ACLs' byte-identical replace pair, root-caused to a NEW confirmed floci gap (lex00/floci#104, CreateNetworkAclEntry drops CidrBlock/Ipv6CidrBlock on read), reproduced identically through plain stock terraform; some runs additionally show tofu-slot completions (THE TOFU-SLOT FINDING, cross-estate, not this unit's)"
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
gauntlet_stage test_apply not_run "depends on stage 3, which does not produce a clean plan"
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="
gauntlet_stage drift_reconverge not_run "depends on stages 3-4"
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS (67 resources; DELTA 2, lex00/floci#57)"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          NOT EMPTY at $CHANGED_N changed object(s), 0 choudoufu's - the malformed-path bug (lex00/floci#102) and the account-id churn are both FIXED; left is tofu-slot (cross-estate, not this unit's, run-dependent) and a NEW confirmed floci gap (lex00/floci#104), present every run; see header"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "67 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
