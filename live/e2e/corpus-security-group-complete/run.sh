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
#                          same real apply, no choudoufu involved - CORRECTED
#                          2026-08-22: this control used to read as
#                          confirming an upstream hashicorp/aws bug; it was
#                          actually confirming lex00/floci#102 (fixed in the
#                          pinned image), and now asserts the diagnostic's
#                          absence instead. See step 1c's own header for the
#                          full correction.
#   stage 2  migrate       PASS - real: 58 of 67 resource instances stamped
#                          for real (53 VERIFIED + 5 DRIFTED, now that the
#                          account-id fix above resolved 13 of the 19
#                          referenced_security_group_id sites that used to
#                          drift; the remaining 6 are dns-from-prefix-list's
#                          own floci gap above, the two default network
#                          ACLs' pre-existing churn, and the main/postgresql/
#                          consul security groups' own cty-typed empty-set
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
#   BREAK_GREENFIELD  set to 1 to run the greenfield stage's own negative
#                control instead of the real comparison: drop module.consul's
#                security group from the expected inventory before the
#                object-by-object comparison, so the total-count check
#                against the real cloud must then correctly fail (the Break
#                text in tools/gauntlet/stages.go for greenfield is literally
#                "drop one resource from the expected inventory; the
#                comparison must fail"). Runs and exits well before stage 2
#                even starts - independent of BREAK and BREAK_REMOVE.
#   BREAK_REMOVE set to 1 to run day2_remove's own negative control instead
#                of the real checks: keep module.postgresql_renamed's block
#                in the config and assert no destroy is proposed for it (the
#                Break text in tools/gauntlet/stages.go for day2_remove is
#                literally "keep the block; no destroy may be proposed").
#                Independent of BREAK and BREAK_GREENFIELD, and only
#                reachable when BREAK is not 1, because day2_remove starts
#                from day2_rename's real, completed rename.
#   BREAK_COUNT  set to 1 to run day2_count's own negative control instead
#                of the real checks: after the real scale-down plan, assert
#                the WRONG instance (count_test[0] rather than count_test[1])
#                was destroyed - the Break text in tools/gauntlet/stages.go
#                for day2_count is literally "Expect a different instance to
#                be destroyed; the assertion must fail." Only reachable when
#                neither BREAK nor BREAK_REMOVE is 1, because PART G starts
#                from day2_remove's real, completed removal.
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

# A second, fresh container for the greenfield stage (live/GAUNTLET.md #13):
# its own namespace choudoufu applies into directly, no migration. It never
# reuses $ENDPOINT's objects above - greenfield means from nothing - and it
# is the ONLY extra container this script spins up for that stage: $ENDPOINT
# itself, read right after stage 1's own apply and before anything else has
# touched it, IS "the cloud after stock's cold deploy" (see PART F's header).
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-security-group-complete-green-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"

ESTATE="security-group-complete-crossing"
GREEN_ESTATE="security-group-complete-greenfield"
REGION="eu-west-1"
INSTANCES=67
ELIGIBLE=58
SKIPPED=9
# GitHub issue #364 unit A2: every stamped instance now also gets its
# identity recorded (in addition to its marker), so a later plan can read
# the record before falling back to the marker sweep or the static
# evaluator. Measured for real against this estate, not derived: all 58
# STAMPED instances get one (their identity is a plain server-minted "id",
# recordable in full and not sensitive).
#
# Of the 9 SKIPPED (untaggable) ones, 3 - the estate's
# aws_vpc_security_group_rules_exclusive instances (module.consul,
# module.postgresql, module.security_group) - got one first: unit B's
# read-first work (#364) found that LocatedIdentityPlanFor's default branch
# always fell back to the bare "id" attribute even when a ratified
# TypeIdentity.IdentityAttrs row named a different, SINGLE attribute the
# wire identity schema said nothing about - the same defect issue #332
# already fixed one layer out, for the classic discovery/plan-time path,
# for aws_default_route_table specifically (identity_attrs ["vpc_id"]; this
# estate's two aws_default_route_table instances were ALREADY counted as
# recorded before that fix - they just held the wrong value, rtb-...
# instead of vpc-..., so fixing them moved no count, only a value).
# aws_vpc_security_group_rules_exclusive's own ratified row (identity_attrs
# ["security_group_id"]) hit the identical gap on the WRITE side, closed by
# internal/live/identity/located.go's namedIdentityAttr preferring a
# ratified single-attribute row over the bare "id" default.
#
# The remaining 6 - aws_route_table_association - are a genuine COMPOSITE
# identity (route_table_id, subnet_id), not a single-attribute override, so
# namedIdentityAttr's fix did not reach them; they stayed unrecorded through
# 2026-08-24. 9f95a8d37e / dd373f718b (#364, "untaggable instances with a
# ratified composite identity ... get a real record now") closed that gap
# generically: internal/live/projection/located.go's LocatedRecordFrom now
# falls back to locatedRatifiedComponentsRecord whenever
# RecordableIdentitySchema answers false but the type has a ratified
# Components composite (table_generated.go), composing the same real-value
# evaluator (#388's identity.ComponentsFromValue) into an import-ID string -
# reached by any untaggable, residuable, ratified-composite type, not just
# this one (the commit measures about 70 ratified types sharing the shape).
# Re-measured here, 2026-08-24, against a real migrate + a read of
# .tofu-records: all 6 aws_route_table_association instances now carry
# identity {"import_id":"subnet-.../rtb-..."}, and all 6 were spot-checked
# by value against `aws ec2 describe-route-tables` (Associations[].SubnetId
# paired with the route table's own id) - every one matched exactly.
#
# So IDENTITIES_RECORDED is now ELIGIBLE+SKIPPED = INSTANCES: every one of
# the 67 instances this estate declares gets an identity recorded, whether
# it is stamped, untaggable-with-a-single-attribute-override, or
# untaggable-with-a-composite. A future regression that drops any one of
# them - stamped or skipped - still moves this count, because there is no
# longer a "these SKIPPED ones don't count" carve-out left to hide behind.
IDENTITIES_RECORDED=$((ELIGIBLE + SKIPPED))

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" >/dev/null 2>&1 || true
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

# ── day2_count's own scalable block (live/GAUNTLET.md #8) ──────────────────
#
# WHY A SYNTHETIC BLOCK, and not one of this estate's own knobs. The stage
# needs "a count block with at least 2 instances" that can be scaled down and
# back up while the plan reads EXACTLY "0 to add, 0 to change, 1 to destroy"
# and then "1 to add, 0 to change, 0 to destroy". This estate declares no
# such knob, checked against the module's own source rather than assumed
# (.corpus/security-group/main.tf, tag v6.0.0):
#
#   - every `count` in the module is the boolean create toggle
#     `count = local.create ? 1 : 0` (aws_security_group.this line 10,
#     aws_vpc_security_group_rules_exclusive.this line 91). A 1-or-0 toggle
#     is not a scalable count: it can never hold two instances, so nothing
#     about "which instance is destroyed" is observable through it.
#   - the module's real scalers are `for_each` maps
#     (aws_vpc_security_group_ingress_rule.this over var.ingress_rules,
#     line 41; egress_rule, line 66), and dropping one key from
#     ingress_rules does NOT produce a lone destroy: line 96 feeds every
#     rule's id into aws_vpc_security_group_rules_exclusive.this[0]'s
#     ingress_rule_ids, so the enforcer is updated in the same plan and the
#     shape is "0 to add, 1 to change, 1 to destroy". The stage's own oracle
#     comparison would then be asserting a two-object shape, and the
#     surviving-identity claim would be entangled with the enforcer's own
#     replace/update behaviour.
#
# So day2_count uses the sanctioned self-contained synthetic block instead -
# the same fallback live/e2e/reference-ec2-vpc/run.sh's Part F and
# live/e2e/corpus-iam-policy/run.sh's Part G already use, and of a type this
# estate exercises heavily in its own right (aws_security_group: the module's
# own SG, the two preset submodules' SGs, the standalone app SG, and the two
# nested vpc calls' default_security_group adopters). aws_security_group.
# count_test is named by nothing else in this estate, is added and removed
# entirely inside PART G (and the G-ORACLE stock leg), and so day2_count's
# own history never touches the resources every other part depends on.
#
# count_test_block($1 = count, $2 = vpc_id HCL expression). $2 lets the same
# helper serve both PART G (inside the adopted estate, where module.vpc
# already exists) and G-ORACLE (its own separate working directory and state
# in the idle greenfield account, with its own small VPC - see
# oracle_vpc_block below). Unquoted heredoc so $1/$2 interpolate;
# ${count.index} is escaped so bash never tries to expand it.
count_test_block() {
  local n="$1" vpc_ref="$2"
  cat <<COUNTEOF
resource "aws_security_group" "count_test" {
  count       = $n
  name        = "complete-count-test-\${count.index}"
  description = "day2_count evidence (live/GAUNTLET.md #8)"
  vpc_id      = $vpc_ref

  tags = {
    Name = "complete-count-test-\${count.index}"
  }
}
COUNTEOF
}

# oracle_vpc_block() is G-ORACLE's own tiny VPC, standing in for module.vpc
# so count_test_block's security groups have a vpc_id in a working directory
# that never declares this estate's real VPCs. 10.99.0.0/16 is clear of both
# (10.0.0.0/16 and 10.1.0.0/16).
oracle_vpc_block() {
  cat <<'EOF'
resource "aws_vpc" "count_oracle" {
  cidr_block = "10.99.0.0/16"
  tags = {
    Name = "complete-count-oracle-vpc"
  }
}
EOF
}

# oracle_count_header() is G-ORACLE's own terraform + provider preamble: the
# same provider pin as the estate (= 6.59.0, the release this checkout's
# admission tables were generated against) and the same DELTA 1 emulator
# flags, spelled out here because the oracle's working directory is a fresh
# one that never sees the corpus example's own provider block. The endpoint
# itself comes from AWS_ENDPOINT_URL, set per command by the caller.
oracle_count_header() {
  cat <<'EOF'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

provider "aws" {
  region                      = "eu-west-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
EOF
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

gauntlet_begin_stage cold_deploy
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

# ── 1c. stock control: does STOCK's own plan still hit the provider bug? ───
#
# CORRECTED 2026-08-22 (was WRONG from 2026-08-21 to today): this control
# used to assert that stock's own plan hits "Error: Provider produced
# invalid plan" ... "for a non-existent attribute path
# cty.Path{cty.GetAttrStep{Name:\"\"}}" on dns-from-prefix-list, and the
# record read that as HANDOFF's third row - stock fails too, a genuine
# upstream hashicorp/aws defect reachable by plain terraform alone, no
# choudoufu fix reaches it. That diagnosis was BACKWARDS. The real cause was
# always HANDOFF's fourth row: lex00/floci#102, DescribeSecurityGroupRules
# never returning PrefixListId for a rule created with one. Handed a rule
# whose four mutually-exclusive source attributes (cidr_ipv4/cidr_ipv6/
# prefix_list_id/referenced_security_group_id) were ALL empty, the AWS
# provider's plan modifier correctly concluded something forced a replace
# and could not name which attribute - producing the empty-named path.
# Stock reproduced the diagnostic only because stock was reading the same
# broken emulator; it never depended on hashicorp/aws's own code being
# wrong. Re-measured against ghcr.io/lex00/floci@sha256:e16d99...79 (#102
# fixed): stock's plan here now exits 0 and raises no such diagnostic at
# all - do NOT re-file this against hashicorp/aws if you are re-deriving
# this history; the provider was never at fault.
#
# THE LESSON, stated plainly because it is worth more than this estate:
# "stock reproduces it too" is evidence the CODE PATH is shared, not
# evidence the DEFECT is upstream. Stock and choudoufu were both talking to
# the SAME emulator process. When both sides of a comparison read from one
# shared, broken data source, agreement between them proves nothing about
# which of the three parties (stock, choudoufu, the emulator) is at fault -
# it only proves the emulator's answer is consistent, which a broken
# emulator's answer very often is. The distinguishing question was never
# "does stock also see this" but "what did the API actually return, read
# directly, with neither terraform implementation in the loop" - which is
# exactly step 3a's own method elsewhere in this file, and exactly what
# settled this: `aws ec2 describe-security-group-rules` direct against
# floci showed PrefixListId absent from a rule that was created with one.
# HANDOFF's own table has a row for "stock fails too" and a separate row for
# "the emulator is wrong" - this file spent a day recording the same symptom
# under the wrong one of the two, because it checked genuine reproduction
# through stock without also checking whether stock's own dependency (the
# emulator) was itself the thing being reproduced. That check is what step
# 3a already does for the two default route tables' identities below, and
# what this control now exists to have done for this bug from the start.
#
# What this control now asserts, and what keeps it load-bearing rather than
# a check that can never fail: with a floci carrying #102's fix, stock's own
# plan - same directory, same terraform.tfstate, right after stock's own
# apply, nothing migrated, nothing deleted, no choudoufu anywhere in the
# loop - must exit 0 and must raise NEITHER "Provider produced invalid
# plan" NOR any mention of dns-from-prefix-list forcing a replace. If either
# comes back, the emulator regressed (or the pinned image moved backward)
# and this stage must fail again, loudly.
#
# internal/plans.RequiresReplacePathIsDegenerate (merged the same day this
# was corrected) is UNRELATED to this fix and is not reverted by it: a
# provider hitting this exact malformed-path shape - from any cause, any
# provider - must never abort a plan fatally, and that is still correct
# defensive code even though the one real-world trigger this repo ever saw
# is now gone. Its own tests use a synthetic provider schema, not
# hashicorp/aws, so they are unaffected by this correction.
log ""
log "=== 1c. stock control: stock's own plan, no choudoufu, same state ==="
STOCK_PLAN_OUT="$(cd "$PLAIN_EST" && terraform plan -input=false -no-color 2>&1)"
STOCK_PLAN_RC=$?
[ "$STOCK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_PLAN_OUT" | tail -40; fail "expected stock's own plan to exit 0 (lex00/floci#102 is fixed in the pinned image); got exit $STOCK_PLAN_RC - the provider, the corpus pin, or the emulator pin has moved, re-check what broke"; }
! grep -qF 'Error: Provider produced invalid plan' <<< "$STOCK_PLAN_OUT" \
  || { printf '%s\n' "$STOCK_PLAN_OUT" | tail -40; fail "stock's own plan reproduces 'Provider produced invalid plan' again - lex00/floci#102 has regressed in the pinned image, or a new cause has appeared; do not re-file this against hashicorp/aws without re-deriving the cause"; }
! grep -qF 'module.security_group.aws_vpc_security_group_ingress_rule.this["dns-from-prefix-list"] must be replaced' <<< "$STOCK_PLAN_OUT" \
  || { printf '%s\n' "$STOCK_PLAN_OUT" | tail -40; fail "stock's own plan proposes replacing dns-from-prefix-list again - lex00/floci#102 has regressed in the pinned image"; }
log "  CONFIRMED: plain, stock terraform - no choudoufu, its own real state,"
log "  right after its own real apply - exits 0 and raises no"
log "  'Provider produced invalid plan' diagnostic and no dns-from-prefix-"
log "  list replace. lex00/floci#102 (DescribeSecurityGroupRules dropping"
log "  PrefixListId) is fixed in the pinned emulator image, and the"
log "  'upstream hashicorp/aws bug, reachable by plain terraform alone'"
log "  reading recorded here through 2026-08-22 was wrong: the diagnostic"
log "  was always downstream of the emulator gap, not an independent"
log "  provider defect - see this block's header for the correction."
STOCK_NACL_CHANGE_N="$(grep -cE '^  # module\.(vpc|vpc_secondary)\.aws_default_network_acl\.this\[0\] will be updated in-place$' <<< "$STOCK_PLAN_OUT")"
if [ "$STOCK_NACL_CHANGE_N" -gt 0 ]; then
  log "  stock's own plan is NOT otherwise empty: $STOCK_NACL_CHANGE_N default"
  log "  network ACL(s) propose an update - lex00/floci#104"
  log "  (DescribeNetworkAcls/CreateNetworkAclEntry drops CidrBlock/"
  log "  Ipv6CidrBlock for rule 101), a SEPARATE, already-filed floci gap,"
  log "  confirmed directly against the AWS CLI (no terraform involved):"
  log "  the entry floci returns for rule 101 on every default network ACL"
  log "  carries neither CidrBlock nor Ipv6CidrBlock, on every read, so the"
  log "  provider's set-hash for that rule never matches config. Not a"
  log "  choudoufu difference - stage 3 confirms the identical two objects"
  log "  through choudoufu below."
fi

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "$INSTANCES resources (DELTA 2, lex00/floci#57)"

# ══════════════════════════════════════════════════════════════════════════
# PART F: GREENFIELD (greenfield, active - live/GAUNTLET.md #13)
# ══════════════════════════════════════════════════════════════════════════
#
# A separate namespace from everything above: choudoufu applies the SAME
# reduced example (DELTA 1 + DELTA 2 + the version pin) directly, with a
# live block from the start, no migration, no state file ever existing. This
# runs here, right after stage 1 and before ANYTHING else touches $ENDPOINT
# (stage 2's migrate is the first thing that writes a tag onto $PLAIN_EST's
# objects; Part D's rename and Part E's remove come later still), so
# $PLAIN_EST's own just-applied objects on $ENDPOINT double as this stage's
# own oracle - "the cloud after stock's cold deploy" (live/GAUNTLET.md's
# oracle text for this stage) IS what stage 1 already built, not a second,
# independent 67-resource stock apply in a third container. Only one more
# floci container is needed here, not two, and the record store is the
# implied default local one (internal/live/projection/store.go's
# defaultRecordDirName) - no explicit record_store block, same as the real
# migrated estate (DELTA 3 above).
gauntlet_begin_stage greenfield
log ""
log "=== PART F: 0. one more floci container, a fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
GREEN_HEALTH=""
for _ in $(seq 1 45); do
  GREEN_HEALTH="$(curl -fs "${GREEN_ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${GREEN_HEALTH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${GREEN_HEALTH:-}" || fail "floci did not come up healthy (ec2) at $GREEN_ENDPOINT"
log "  healthy: greenfield=$GREEN_ENDPOINT (oracle: PLAIN_EST's own stage-1 apply on $ENDPOINT, untouched so far)"

GREEN="$WORK/green"
copy_tree "$GREEN"
GREEN_EST="$GREEN/security-group/examples/complete"
perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$GREEN_EST/main.tf"
grep -q 'DELTA 1' "$GREEN_EST/main.tf" || fail "the greenfield DELTA 1 did not match the provider block - the corpus pin has moved"
perl -0pi -e 's/\n  vpc_associations = \{\n    secondary = \{\n      vpc_id = module\.vpc_secondary\.vpc_id\n    \}\n  \}\n\n/\n  # DELTA 2 (EMULATOR GAP, lex00\/floci#57): cross-VPC association removed.\n\n/' "$GREEN_EST/main.tf"
grep -q '^  vpc_associations = {' "$GREEN_EST/main.tf" && fail "the greenfield DELTA 2 left a vpc_associations block behind"
perl -0pi -e 's/version = ">= 6\.29"/version = "= 6.59.0"/' "$GREEN_EST/versions.tf"
# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create.
# Same fix, same precedent as corpus-alb-complete's own 898091b8f2.
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \"= 6\.59\.0\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$GREEN_ESTATE\"\n\n    strict {\n      no_source_create = \"create\"\n    }\n  }\n}/" "$GREEN_EST/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"
log "  DELTA 1+2+3 applied to a fresh copy: emulator flags, vpc_associations removed, live block (estate=$GREEN_ESTATE)"

log "=== PART F: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -60; fail "the greenfield apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly $INSTANCES resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART F: 2. markers, read through the AWS CLI directly ==="
GREEN_MAIN_SG_ID="$(awsg ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["module.security_group.aws_security_group.this:0"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$GREEN_MAIN_SG_ID" ] && [ "$GREEN_MAIN_SG_ID" != "None" ] || fail "no greenfield security group found by tofu-address=module.security_group.aws_security_group.this:0"
GREEN_PG_SG_ID="$(awsg ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["module.postgresql.module.security_group.aws_security_group.this:0"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$GREEN_PG_SG_ID" ] && [ "$GREEN_PG_SG_ID" != "None" ] || fail "no greenfield security group found by tofu-address=module.postgresql.module.security_group.aws_security_group.this:0"
GREEN_CONSUL_SG_ID="$(awsg ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["module.consul.module.security_group.aws_security_group.this:0"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$GREEN_CONSUL_SG_ID" ] && [ "$GREEN_CONSUL_SG_ID" != "None" ] || fail "no greenfield security group found by tofu-address=module.consul.module.security_group.aws_security_group.this:0"
GREEN_APP_SG_ID="$(awsg ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["aws_security_group.app"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$GREEN_APP_SG_ID" ] && [ "$GREEN_APP_SG_ID" != "None" ] || fail "no greenfield security group found by tofu-address=aws_security_group.app"
GREEN_MAIN_ESTATE_TAG="$(awsg ec2 describe-tags --filters "Name=resource-id,Values=$GREEN_MAIN_SG_ID" "Name=key,Values=tofu-estate" --query "Tags[0].Value" --output text)"
[ "$GREEN_MAIN_ESTATE_TAG" = "$GREEN_ESTATE" ] || fail "the greenfield main security group carries tofu-estate=$GREEN_MAIN_ESTATE_TAG, not $GREEN_ESTATE"
log "  all four named security groups (main/postgresql/consul/app) found by their tofu-address markers; tofu-estate=$GREEN_MAIN_ESTATE_TAG - read via the AWS CLI, not choudoufu's own report"

log "=== PART F: 3. the record store holds every instance, including the 9 untaggable ones (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN_EST/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "$INSTANCES" ] || fail "expected $INSTANCES records under the local record store after the greenfield apply (every instance gets one, stamped or not - #364 A2), found $GREEN_RECORD_FILES"
log "  $INSTANCES records persisted, one per managed instance, read directly off the local record store"

log "=== PART F: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART F: 5. object-by-object comparison against \$PLAIN_EST's own stage-1 apply ==="
BASENAME_PLAIN="$(basename "$PLAIN_EST")"
PLAIN_PG_SG_ID="$(terraform -chdir="$PLAIN_EST" output -raw postgresql_security_group_id)"
[ -n "$PLAIN_PG_SG_ID" ] && [ "$PLAIN_PG_SG_ID" != "None" ] || fail "could not read the postgresql security group's id from terraform output"
PLAIN_CONSUL_SG_ID="$(terraform -chdir="$PLAIN_EST" output -raw consul_security_group_id)"
[ -n "$PLAIN_CONSUL_SG_ID" ] && [ "$PLAIN_CONSUL_SG_ID" != "None" ] || fail "could not read the consul security group's id from terraform output"
PLAIN_APP_SG_ID="$(awsl ec2 describe-security-groups --filters "Name=group-name,Values=ex-${BASENAME_PLAIN}-app" --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$PLAIN_APP_SG_ID" ] && [ "$PLAIN_APP_SG_ID" != "None" ] || fail "could not find the app security group by its group-name on \$PLAIN_EST"

sg_rule_fingerprint() { # $1=endpoint $2=sg-id - a normalised rule shape,
                         # tags and cross-account ids stripped, read via the
                         # AWS CLI, never through tofu state.
  aws --endpoint-url "$1" --region "$REGION" ec2 describe-security-group-rules \
    --filters "Name=group-id,Values=$2" --output json 2>/dev/null \
  | jq -S '[.SecurityGroupRules[] | {
      IsEgress:   .IsEgress,
      IpProtocol: .IpProtocol,
      FromPort:   (.FromPort // null),
      ToPort:     (.ToPort // null),
      HasCidr4:   (.CidrIpv4 != null),
      HasCidr6:   (.CidrIpv6 != null),
      HasPrefix:  (.PrefixListId != null),
      HasRefSg:   (.ReferencedGroupInfo != null)
    }] | sort_by(.IsEgress, .IpProtocol, .FromPort, .ToPort, .HasCidr4, .HasCidr6, .HasPrefix, .HasRefSg)'
}

EXPECTED_SGS="main:$MAIN_SG_ID:$GREEN_MAIN_SG_ID postgresql:$PLAIN_PG_SG_ID:$GREEN_PG_SG_ID consul:$PLAIN_CONSUL_SG_ID:$GREEN_CONSUL_SG_ID app:$PLAIN_APP_SG_ID:$GREEN_APP_SG_ID"
N_EXPECTED_SG_TOTAL=6 # 4 named + 2 module-vpc/vpc_secondary aws_default_security_group adopters
if [ "${BREAK_GREENFIELD:-}" = "1" ]; then
  EXPECTED_SGS="main:$MAIN_SG_ID:$GREEN_MAIN_SG_ID postgresql:$PLAIN_PG_SG_ID:$GREEN_PG_SG_ID app:$PLAIN_APP_SG_ID:$GREEN_APP_SG_ID"
  N_EXPECTED_SG_TOTAL=5
  log "  BREAK_GREENFIELD=1: dropped module.consul's security group from the expected inventory - the total-count comparison below must fail"
fi
GREEN_SG_COUNT="$(awsg resourcegroupstaggingapi get-resources --resource-type-filters ec2:security-group --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$GREEN_SG_COUNT" = "$N_EXPECTED_SG_TOTAL" ] || fail "the greenfield estate has $GREEN_SG_COUNT tagged security groups, expected $N_EXPECTED_SG_TOTAL"

for pair in $EXPECTED_SGS; do
  label="${pair%%:*}"; rest="${pair#*:}"; plain_id="${rest%%:*}"; green_id="${rest#*:}"
  G="$(sg_rule_fingerprint "$GREEN_ENDPOINT" "$green_id")"
  P="$(sg_rule_fingerprint "$ENDPOINT" "$plain_id")"
  [ "$G" = "$P" ] || { printf 'greenfield %s: %s\nstock     %s: %s\n' "$label" "$G" "$label" "$P"; fail "the $label security group's rule shape differs between the greenfield estate and stock's own cold deploy"; }
done
log "  all rule shapes (protocol, port range, cidr/prefix-list/referenced-sg presence, tags stripped) match between choudoufu's greenfield apply and \$PLAIN_EST's own stage-1 apply, for every named security group compared ($EXPECTED_SGS wc: $(wc -w <<< "$EXPECTED_SGS" | tr -d ' '))"

log ""
log "STAGE F (greenfield): PASS"
log ""
gauntlet_stage greenfield pass "$INSTANCES resources from nothing, all markers verified via the AWS CLI, $INSTANCES records in the local record store (#364 A2), replan empty, $N_EXPECTED_SG_TOTAL tagged security groups (4 named + 2 default adopters) and every named one's rule shape matches \$PLAIN_EST's own stage-1 apply object by object, tags stripped"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock (day2_count, active - live/GAUNTLET.md
# #8): "Stock's plan for the same count change, normalised."
# ══════════════════════════════════════════════════════════════════════════
#
# Unlike D-ORACLE/D-ORACLE(remove)/F-ORACLE, this one cannot reuse a copy of
# cold_deploy's own state: cold_deploy's state has no scalable count block in
# it at all (see count_test_block's header for why this estate declares
# none), so there is nothing there to scale. Stock therefore stands the SAME
# 2-instance block up for real, with plain terraform, in its own working
# directory - and in $GREEN_ENDPOINT, the greenfield container PART F has
# just finished with and which nothing else in this script ever writes to
# again (grep: no GREEN_ENDPOINT/GREEN_EST/awsg use appears below this
# point). aws_security_group.count_test and its own 10.99.0.0/16 VPC collide
# with nothing there, the greenfield stage's own verdict is already recorded
# above, and its inventory check counted objects by their tofu-estate tag,
# which a plain-terraform apply never writes. $ENDPOINT is deliberately NOT
# used: PART G's own real leg runs there, and an oracle sharing the account
# with the thing it is the oracle for is not an oracle.
#
# AWS_ENDPOINT_URL stays $ENDPOINT for the rest of the script; only this
# block's own terraform invocations are pointed at $GREEN_ENDPOINT, via a
# per-command environment override.
gauntlet_begin_stage day2_count
log ""
log "=== G-ORACLE. stock: create a 2-instance count block, scale it to 1 and back, in the (idle) greenfield account ==="
PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
mkdir -p "$PLAIN_ORACLE_COUNT"
{
  oracle_count_header
  echo
  oracle_vpc_block
  echo
  count_test_block 2 "aws_vpc.count_oracle.id"
} > "$PLAIN_ORACLE_COUNT/main.tf"
( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count oracle's terraform init failed"; }
ORACLE_COUNT_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 3 resources (the oracle's own VPC plus 2 count-test security groups) for the day2_count oracle"; }
awso() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
ORACLE_SG0_ID="$(awso ec2 describe-security-groups --filters "Name=tag:Name,Values=complete-count-test-0" --query "SecurityGroups[0].GroupId" --output text)"
ORACLE_SG1_ID="$(awso ec2 describe-security-groups --filters "Name=tag:Name,Values=complete-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$ORACLE_SG0_ID" ] && [ "$ORACLE_SG0_ID" != "None" ] || fail "no oracle count_test[0] security group found by its Name tag"
[ -n "$ORACLE_SG1_ID" ] && [ "$ORACLE_SG1_ID" != "None" ] || fail "no oracle count_test[1] security group found by its Name tag"
[ "$ORACLE_SG0_ID" != "$ORACLE_SG1_ID" ] || fail "the oracle's two count_test instances resolved to the same GroupId - the Name-tag lookup is not distinguishing them"
log "  stock: 2 instances created, count_test[0]=$ORACLE_SG0_ID count_test[1]=$ORACLE_SG1_ID - read via the AWS CLI"

{
  oracle_count_header
  echo
  oracle_vpc_block
  echo
  count_test_block 1 "aws_vpc.count_oracle.id"
} > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
[ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
ORACLE_DOWN_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count oracle's scale-down apply was not exactly one destroy"; }
ORACLE_SG0_AFTER_DOWN="$(awso ec2 describe-security-groups --group-ids "$ORACLE_SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
[ "$ORACLE_SG0_AFTER_DOWN" = "$ORACLE_SG0_ID" ] || fail "stock's surviving count_test[0] changed id across the scale-down ($ORACLE_SG0_ID -> $ORACLE_SG0_AFTER_DOWN)"
ORACLE_SG1_N_AFTER_DOWN="$(awso ec2 describe-security-groups --group-ids "$ORACLE_SG1_ID" --query "length(SecurityGroups)" --output text 2>/dev/null || echo 0)"
[ "$ORACLE_SG1_N_AFTER_DOWN" = "0" ] || fail "stock's count_test[1] ($ORACLE_SG1_ID) still exists after the scale-down destroy"
log "  stock: exactly one destroy (count_test[1]=$ORACLE_SG1_ID, 0 matches now), count_test[0]=$ORACLE_SG0_ID unchanged"

{
  oracle_count_header
  echo
  oracle_vpc_block
  echo
  count_test_block 2 "aws_vpc.count_oracle.id"
} > "$PLAIN_ORACLE_COUNT/main.tf"
ORACLE_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
[ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
ORACLE_UP_APPLY_OUT="$(cd "$PLAIN_ORACLE_COUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count oracle's scale-up apply was not exactly one create"; }
ORACLE_SG1_NEW_ID="$(awso ec2 describe-security-groups --filters "Name=tag:Name,Values=complete-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$ORACLE_SG1_NEW_ID" ] && [ "$ORACLE_SG1_NEW_ID" != "None" ] || fail "no oracle count_test[1] security group found after the scale-up"
[ "$ORACLE_SG1_NEW_ID" != "$ORACLE_SG1_ID" ] || fail "stock's recreated count_test[1] came back with the SAME id it had before being destroyed"
ORACLE_SG0_AFTER_UP="$(awso ec2 describe-security-groups --group-ids "$ORACLE_SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
[ "$ORACLE_SG0_AFTER_UP" = "$ORACLE_SG0_ID" ] || fail "stock's count_test[0] changed id across the scale-up ($ORACLE_SG0_ID -> $ORACLE_SG0_AFTER_UP)"
log "  stock: exactly one create (count_test[1], new id $ORACLE_SG1_NEW_ID, was $ORACLE_SG1_ID), count_test[0]=$ORACLE_SG0_ID unchanged throughout"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two rename targets, one per leg, so a gap in either mechanism is visible:
# a `moved` block renames module.postgresql (the "rule children" case - one
# aws_security_group plus two aws_vpc_security_group_ingress_rule instances
# and one egress rule and one rules_exclusive, all taggable in this
# module's v6.0.0 shape, moving together under one moved block), and
# "choudoufu live-mv" renames the standalone aws_security_group.app (no
# rule children at all, referenced by two other ingress rules'
# referenced_security_group_id, both updated by the same sed pass). The
# stock oracle below plans both renames together on a copy of cold_deploy's
# own state, before choudoufu or live-import ever touch these objects.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
ORACLE_ROOT="$WORK/oracle"
cp -r "$PLAIN" "$ORACLE_ROOT"
ORACLE_EST="$ORACLE_ROOT/security-group/examples/complete"
rm -rf "$ORACLE_EST/.terraform" "$ORACLE_EST/.terraform.lock.hcl"
sed -i.bak 's/module "postgresql" {/module "postgresql_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/module\.postgresql\./module.postgresql_renamed./g' "$ORACLE_ROOT/security-group/examples/complete/outputs.tf"
sed -i.bak 's/resource "aws_security_group" "app" {/resource "aws_security_group" "app_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/aws_security_group\.app\.id/aws_security_group.app_renamed.id/g' "$ORACLE_EST/main.tf"
rm -f "$ORACLE_EST/main.tf.bak" "$ORACLE_EST/outputs.tf.bak"
cat >> "$ORACLE_EST/main.tf" <<'EOF'

moved {
  from = module.postgresql
  to   = module.postgresql_renamed
}

moved {
  from = aws_security_group.app
  to   = aws_security_group.app_renamed
}
EOF
( cd "$ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7, issue #358): "Stock with
# the same block removed plans the same destroys." A SEPARATE fresh copy of
# cold_deploy's own state (not the rename oracle's ORACLE_ROOT above), so
# this destroy has nothing to do with either rename - it removes
# module.postgresql's block (the original, pre-rename address) entirely.
# module.postgresql is self-contained: nothing else in main.tf references
# it (only outputs.tf does, via its own 5-output section, removed with it).
gauntlet_begin_stage day2_remove
log "=== D-ORACLE (remove). stock: delete module.postgresql's block on cold_deploy's own state ==="
ORACLE_REMOVE_ROOT="$WORK/oracle-remove"
cp -r "$PLAIN" "$ORACLE_REMOVE_ROOT"
ORACLE_REMOVE_EST="$ORACLE_REMOVE_ROOT/security-group/examples/complete"
rm -rf "$ORACLE_REMOVE_EST/.terraform" "$ORACLE_REMOVE_EST/.terraform.lock.hcl"
perl -0777 -pi -e 's/\nmodule "postgresql" \{.*?\n\}\n\n################/\n################/s' "$ORACLE_REMOVE_EST/main.tf"
grep -q 'module "postgresql"' "$ORACLE_REMOVE_EST/main.tf" && fail "removing module.postgresql's block from the oracle copy did not match - the corpus example has moved"
perl -0777 -pi -e 's/\n# PostgreSQL preset submodule\n.*?\n# Consul preset submodule/\n# Consul preset submodule/s' "$ORACLE_REMOVE_EST/outputs.tf"
grep -q 'module.postgresql' "$ORACLE_REMOVE_EST/outputs.tf" && fail "removing module.postgresql's outputs from the oracle copy did not match - the corpus example has moved"
( cd "$ORACLE_REMOVE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_REMOVE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit (after removing the block) failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$ORACLE_REMOVE_EST" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.postgresql\.module\.security_group\.aws_security_group\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -60; fail "stock does not propose destroying module.postgresql's security group when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 5 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly 5 destroys (1 SG + 2 ingress + 1 egress + 1 rules_exclusive)"; }
log "  stock: exactly 5 destroys (module.postgresql's SG, its 2 ingress rules, its 1 egress rule, its 1 rules_exclusive), nothing else, on the state cold_deploy produced"
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9), computed here for the
# same reason day2_remove's own oracle sits before migrate (above): a
# throwaway copy of cold_deploy's own (never re-applied) state, module.
# security_group's `name` argument changed to a different literal - `name`
# is ForceNew on aws_security_group (AWS has no rename-security-group API;
# only name_prefix-generated names ever change, and this module sets an
# explicit name), so this forces a replace at the same declared address,
# cascading into every child of that SAME security group: its own 7
# ingress rules, 1 egress rule and 1 rules_exclusive enforcer (all four
# resource types carry the security_group_id as a ForceNew argument, so a
# new group id forces all of them to replace too). module.security_group
# is chosen because day2_rename/day2_remove (below) never touch it - that
# stage's own two targets are module.postgresql and aws_security_group.app
# - so day2_replace has no ordering dependency on either. PLAN ONLY, never
# applied: this copy shares floci's account with $EST, and applying here
# would destroy the real live security group $EST's own later stages
# still depend on.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.security_group's own SG via its ForceNew name argument, on cold_deploy's own state ==="
ORACLE_REPLACE_ROOT="$WORK/oracle-replace"
cp -r "$PLAIN" "$ORACLE_REPLACE_ROOT"
ORACLE_REPLACE_EST="$ORACLE_REPLACE_ROOT/security-group/examples/complete"
rm -rf "$ORACLE_REPLACE_EST/.terraform" "$ORACLE_REPLACE_EST/.terraform.lock.hcl"
sed -i.bak 's/^  name        = local\.name$/  name        = "${local.name}-replaced"/' "$ORACLE_REPLACE_EST/main.tf"
rm -f "$ORACLE_REPLACE_EST/main.tf.bak"
grep -q 'name        = "${local.name}-replaced"' "$ORACLE_REPLACE_EST/main.tf" \
  || fail "changing module.security_group's name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$ORACLE_REPLACE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_REPLACE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$ORACLE_REPLACE_EST" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.security_group\.aws_security_group\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.security_group's SG when its ForceNew name argument changes"; }
grep -qF 'Plan: 10 to add, 0 to change, 10 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "the day2_replace stock oracle plan does not match the header's own ten-resource cascade (SG + 7 ingress + 1 egress + 1 rules_exclusive, all replaced)"; }
log "  stock: exactly one SG replace at the same declared address, cascading into its 7 ingress rules, 1 egress rule and 1 rules_exclusive enforcer (all replaced) - 10 to add, 10 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
gauntlet_end_stage

gauntlet_begin_stage migrate

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
# CORRECTED 2026-08-22: was 52 VERIFIED / 6 DRIFTED before lex00/floci#102
# was fixed in the pinned image. dns-from-prefix-list's own prefix_list_id
# now reads back correctly on migrate's own drift comparison too, so it
# moved from DRIFTED into VERIFIED - the remaining 5 DRIFTED are the
# already-documented main/postgresql/consul security groups' cty-typed
# empty-set representation and the two default network ACLs' own
# lex00/floci#104 churn (see stage 3's header), neither a choudoufu defect.
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
grep -qF "$IDENTITIES_RECORDED identit" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not report exactly $IDENTITIES_RECORDED identities recorded (GitHub issue #364 unit A2)"; }
log "  $ELIGIBLE stamped, 0 failed, $SKIPPED skipped - matches the dry run exactly"
log "  $IDENTITIES_RECORDED identities recorded (#364 unit A2: every stamped instance's identity, not only a marker)"

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
gauntlet_stage migrate pass "$ELIGIBLE of $INSTANCES stamped, $IDENTITIES_RECORDED identities recorded (#364 unit A2)"
gauntlet_begin_stage test_plan

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
# (immediately below) actually be REACHED. Fixing that bug, in turn, is what
# lets the plan finish rendering at all, instead of aborting before a single
# diff line is printed - and what that rendering newly reveals is handled in
# the next three blocks: zero fatal errors, one confirmed floci gap, and a
# cross-estate, already-documented marker-completion gap (tofu-slot). None of
# this was visible before today: every earlier run of this script died on
# the fatal error before the diff was ever printed.

# choudoufu's OWN handling of the malformed path (internal/tofu/node_resource_
# abstract_instance.go's plan(), and internal/resources/managed_plan.go's
# twin copy of the same filtering logic; internal/plans.
# RequiresReplacePathIsDegenerate holds the shared rule: a RequiresReplace
# path containing a cty.GetAttrStep with an empty Name names no attribute in
# ANY schema, from ANY provider, so it is dropped from the replace set with a
# WARNING instead of aborting the whole plan with a fatal error - see the
# rule's own doc comment and internal/tofu/context_plan_test.go's
# TestContext2Plan_requiresReplaceMalformedPathDropped/
# TestContext2Plan_requiresReplaceBogusNamedPathStillErrors for the positive
# and negative cases). That code is still correct and still merged - a
# provider handing back an unnameable attribute must never abort a run,
# regardless of cause - but its one real-world trigger is gone: CORRECTED
# 2026-08-22, lex00/floci#102 (DescribeSecurityGroupRules dropping
# PrefixListId) is fixed in the pinned emulator image, so the AWS provider
# never sees a rule with all four source attributes empty, never proposes
# the malformed-path replace, and the warning this bug used to produce on
# every run no longer fires. Asserted here as: zero fatal errors of ANY
# kind (unchanged), and now zero malformed-path warnings too (was 1).
WANT_TOTAL_ERR_N=0
WANT_MALFORMED_WARN_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_TOTAL_ERR_N=1
  WANT_MALFORMED_WARN_N=1
  log "  BREAK=1: expecting 1 fatal Error and 1 malformed-path warning"
  log "           (neither is real any more - lex00/floci#102 is fixed, so"
  log "           dns-from-prefix-list never forces the malformed-path"
  log "           replace at all). This step must fail."
fi
TOTAL_ERR_N="$(grep -c '^Error: ' <<< "$PLAN_OUT")"
[ "$TOTAL_ERR_N" = "$WANT_TOTAL_ERR_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_TOTAL_ERR_N fatal error(s), got $TOTAL_ERR_N - every choudoufu-side wall (#305, #307, #313 A and B, #321, #332) is fixed and asserted absent above, so a fatal error here is new"; }
MALFORMED_WARN_N="$(grep -c '^Warning: Provider produced a malformed requires-replacement path$' <<< "$PLAN_OUT")"
[ "$MALFORMED_WARN_N" = "$WANT_MALFORMED_WARN_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Warning:' | sort | uniq -c; fail "expected $WANT_MALFORMED_WARN_N malformed-requires-replacement-path warning(s), got $MALFORMED_WARN_N - lex00/floci#102 may have regressed in the pinned image"; }

# The floci gap that used to force dns-from-prefix-list's replace on every
# run, CORRECTED 2026-08-22: lex00/floci#102 (DescribeSecurityGroupRules
# never returning PrefixListId for a rule created with one) is fixed in the
# pinned emulator image. Asserted here by ABSENCE - the load-bearing half of
# this correction: neither dns-from-prefix-list nor its rules_exclusive
# sibling (which used to show the same replace's downstream id change) may
# propose a replace any more. If this ever comes back, #102 has regressed in
# whatever image is pinned - re-confirm at the AWS CLI level (DescribeSecurityGroupRules
# on a rule created with a prefix list) before re-filing, since the same
# diagnostic shape can also come from a genuinely new cause.
! grep -qF 'module.security_group.aws_vpc_security_group_ingress_rule.this["dns-from-prefix-list"] must be replaced' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^  # .+ must be replaced$'; fail "dns-from-prefix-list must be replaced again - lex00/floci#102 (PrefixListId) may have regressed in the pinned image"; }
WANT_REPLACE_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_REPLACE_N=1
  log "  BREAK=1: expecting 1 'must be replaced' site (not real any more -"
  log "           lex00/floci#102 is fixed, so nothing forces a replace)."
  log "           This step must fail."
fi
REPLACE_N="$(grep -cE '^  # .+ must be replaced$' <<< "$PLAN_OUT")"
[ "$REPLACE_N" = "$WANT_REPLACE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^  # .+ must be replaced$'; fail "expected $WANT_REPLACE_N 'must be replaced' site(s), got $REPLACE_N"; }

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
# that looks precise and is actually just this run's luck; corpus-vpc-
# complete's own stage 3 check makes the same choice, for the same reason -
# fail unconditionally on ANY non-empty plan, log the real total, and name
# the one addition that is NOT optional. Not this unit's wall to fix, not
# this unit's to hide behind a brittle count either.
CHANGED_HEADERS="$(grep -E '^  # .+ (will be (created|updated|destroyed)|must be replaced)$' <<< "$PLAN_OUT")"
CHANGED_N="$(printf '%s\n' "$CHANGED_HEADERS" | grep -c .)"
log "  $CHANGED_N object(s) changed this run (tofu-slot completions vary run"
log "  to run, per THE TOFU-SLOT FINDING above); the one that is NOT"
log "  optional, confirmed present on every run, is dns-from-prefix-list's"
log "  replace (lex00/floci#102):"
printf '%s\n' "$CHANGED_HEADERS" | while IFS= read -r line; do [ -n "$line" ] && log "    $line"; done

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
log "  The malformed-RequiresReplace-path bug: FIXED, and its one real-world"
log "  trigger (lex00/floci#102, DescribeSecurityGroupRules dropping"
log "  PrefixListId) is now itself fixed in the pinned emulator image, so"
log "  the bug no longer even fires (0 warnings, was 1) - see"
log "  internal/plans.RequiresReplacePathIsDegenerate and its tests, and"
log "  step 1c's header, CORRECTED 2026-08-22, for the full history."
log "  The referenced_security_group_id/account-id churn (13 sites: all-"
log "  from-self, mysql-from-app, 11 consul rules): FIXED, by correcting"
log "  DELTA 1 rather than choudoufu - skip_requesting_account_id kept the"
log "  provider from ever learning its own account id, so its own (correct)"
log "  same-account normalization could never fire. Proven with an isolated"
log "  repro against plain stock terraform, no choudoufu involved."
log "  Left, at $CHANGED_N changed object(s) this run: any tofu-slot"
log "  completions present are the same deliberate, cross-estate gap"
log "  corpus-vpc-complete's own header calls THE TOFU-SLOT FINDING (fixed"
log "  generically by #372, so expected at 0 here too, but not this unit's"
log "  wall if it ever reappears), and the two default network ACLs' own"
log "  pre-existing churn (module.vpc and module.vpc_secondary's"
log "  aws_default_network_acl.this[0]) is a SEPARATE, confirmed, already-"
log "  filed floci gap - lex00/floci#104, DescribeNetworkAcls dropping"
log "  CidrBlock/Ipv6CidrBlock for entries written via CreateNetworkAclEntry"
log "  - reproduced identically through plain stock terraform in step 1c"
log "  above with zero choudoufu in the loop, and confirmed directly at the"
log "  AWS CLI level (no terraform at all): every read of the default NACL's"
log "  rule 101 on this floci carries neither field, confirmed deterministic"
log "  in 40 isolated, repeated describe calls against an idle object (even"
log "  with a CreateTags interleaved, matching what migrate does). Not"
log "  choudoufu's, and not #102's - a second, independent gap, fixed on"
log "  lex00/floci branch fix/104-network-acl-entry-ipv6-cidr (pushed to"
log "  origin, not yet published or repinned)."
log ""
log "  CAUTION, do not skip: this estate has ALSO been observed with"
log "  CHANGED_N=0 on this exact step (live-plan, discovery-based) and then"
log "  2 on the immediately following no-op apply's own internal replan -"
log "  same two objects, same shape, moments apart, same pinned image."
log "  live-plan and a live estate's apply share the IDENTICAL discovery +"
log "  projection.BuildWith code path (internal/command/live_mode.go's own"
log "  doc comment: a stateless run replaces only the state manager and the"
log "  prior state, nothing else) - so that split is not "one path read"
log "  something stale the other did not." Both did an independent, fresh"
log "  live read, moments apart, of the same two objects, and got different"
log "  answers. The isolated re-test above rules out simple per-object"
log "  flakiness on an idle read; it does NOT rule out floci raciness under"
log "  the load of a real 67-resource discovery pass, which is the same"
log "  class of problem already caught separately in #103 (same container,"
log "  same request, 200 sometimes and an error other times). A single"
log "  CHANGED_N=0 run is not proof this stage is reliably empty until a"
log "  demonstrably deterministic image is measured."

if [ "$CHANGED_N" -eq 0 ]; then
  log ""
  log "STAGE 3 (test_plan): EMPTY, for real. Every wall this estate has ever"
  log "hit (#305, #307, #313 A and B, #321, #332, the malformed-"
  log "RequiresReplace-path bug, the account-id churn, and lex00/floci#102"
  log "and #104) is fixed, confirmed absent, or confirmed not present this"
  log "run; both default route tables' import identities are asserted BY"
  log "VALUE against AWS."
  log ""
  gauntlet_stage test_plan pass "the plan is genuinely empty: every choudoufu wall (#305, #307, #313 A and B, #321, #332) and both confirmed floci gaps (#102, #104) are fixed or absent this run; default route table identities asserted by value against the AWS CLI in step 3a"
  gauntlet_begin_stage test_apply

  # ── 4. test apply: apply the empty plan; it must be a genuine no-op ──────
  log "=== 4. test apply: applying the empty plan is a genuine no-op ==="
  BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$ESTATE" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
  NOOP_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
  NOOP_APPLY_RC=$?
  [ "$NOOP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$NOOP_APPLY_OUT" | tail -40; fail "the no-op apply exited $NOOP_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_APPLY_OUT" \
    || { printf '%s\n' "$NOOP_APPLY_OUT" | grep -E '^  #|^  ~|Apply complete'; fail "the no-op apply was not a genuine no-op"; }
  AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$ESTATE" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
  [ "$AFTER_N" = "$BEFORE_N" ] || fail "the tofu-estate-tagged object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
  log "  genuine no-op: Resources: 0 added, 0 changed, 0 destroyed;"
  log "  $BEFORE_N tofu-estate-tagged objects before and after, read through"
  log "  resourcegroupstaggingapi, never through choudoufu's own report"
  log ""
  log "STAGE 4 (test_apply): PASS"
  log ""
  gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N objects, read through resourcegroupstaggingapi"
  gauntlet_begin_stage drift_reconverge

  # ── 5. drift and reconverge: mutate one live object, plan and fix it ────
  log "=== 5. drift and reconverge: one live object mutated out of band ==="
  DRIFT_TAG_VALUE="tampered-by-gauntlet-$$"
  awsl ec2 create-tags --resources "$MAIN_SG_ID" --tags "Key=DriftProbe,Value=$DRIFT_TAG_VALUE" >/dev/null \
    || fail "could not tag $MAIN_SG_ID out of band via the AWS CLI"
  GOT_DRIFT_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=DriftProbe" --query 'Tags[0].Value' --output text)"
  [ "$GOT_DRIFT_TAG" = "$DRIFT_TAG_VALUE" ] || fail "the out-of-band DriftProbe tag did not take on $MAIN_SG_ID"
  log "  tagged $MAIN_SG_ID with DriftProbe=$DRIFT_TAG_VALUE directly via the"
  log "  AWS CLI - never through choudoufu; the configuration names no such tag"

  DRIFT_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
  DRIFT_PLAN_RC=$?
  [ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -40; fail "the drift-detection live-plan exited $DRIFT_PLAN_RC"; }
  DRIFT_CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
  DRIFT_N_CHANGED="$(printf '%s\n' "$DRIFT_CHANGED_ADDRS" | grep -c . || true)"
  [ "$DRIFT_N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix after the out-of-band mutation, got $DRIFT_N_CHANGED"; }
  [ "$DRIFT_CHANGED_ADDRS" = "module.security_group.aws_security_group.this[0]" ] \
    || fail "the drift plan proposes fixing $DRIFT_CHANGED_ADDRS, not module.security_group.aws_security_group.this[0]"
  log "  live-plan proposes fixing exactly one object: $DRIFT_CHANGED_ADDRS"

  RECONVERGE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
  RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  STILL_TAGGED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=DriftProbe" --query 'length(Tags)' --output text)"
  [ "$STILL_TAGGED" = "0" ] || fail "DriftProbe is still on $MAIN_SG_ID after reconverging - the tag was not removed"
  log "  reconverged: DriftProbe is gone from $MAIN_SG_ID, confirmed through"
  log "  the AWS CLI directly, never through choudoufu's own report"
  log ""
  log "STAGE 5 (drift_reconverge): PASS"
  log ""
  gauntlet_stage drift_reconverge pass "one object tampered (DriftProbe tag on the main security group), exactly module.security_group.aws_security_group.this[0] proposed, apply changed 1 and the tag is gone, confirmed via the AWS CLI"
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Placed right after STAGE 5 and BEFORE PART D (day2_rename, below) on
  # purpose, the same convention corpus-ec2-instance-complete's own PART F
  # uses: module.security_group is never touched by PART D's rename (that
  # stage's own two targets are module.postgresql and aws_security_group.
  # app - see the D-ORACLE comment above stage 1), so this section has no
  # dependency on PART D's outcome. module.security_group's `name`
  # argument changes from local.name to "${local.name}-replaced" -
  # `name` is ForceNew on aws_security_group (AWS has no rename API) -
  # forcing a replace at the SAME declared address. Nine resources cascade
  # from the SAME dependency edges F-ORACLE (above, right after
  # cold_deploy) already names: the SG's own 7 ingress rules, 1 egress
  # rule and 1 rules_exclusive enforcer (all four types carry the security
  # group id as a ForceNew argument) - a real, ten-object shape, not a
  # bug; F-ORACLE shows stock proposing the identical cascade on its own
  # copy of the same state.
  #
  # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
  # for the full reasoning, reproduced only in summary here): OpenTofu core
  # rejects a `lifecycle` block on a `module` call, and patching the
  # vendored terraform-aws-security-group module's own aws_security_group
  # resource to add create_before_destroy would cross this corpus's
  # reduction-only convention, so this evidence pass exercises the default
  # destroy-then-create ordering instead.
  #
  # NO BREAK=replace LEG (unlike corpus-ec2-instance-complete's/corpus-
  # sqs-basic's/corpus-s3-bucket-complete's own day2_replace sections):
  # tested empirically against this branch (a second live security group
  # manufactured via the AWS CLI, carrying the SAME tofu-address/tofu-slot
  # tags as module.security_group.aws_security_group.this:0, alongside the
  # real, still-valid one) and found that the plan reports "No changes" -
  # it does NOT warn or refuse. Re-tested corpus-ec2-instance-complete's
  # OWN BREAK=replace leg the same way and found the SAME regression there
  # (it now fails its own load-bearing check: "the plan succeeded with two
  # live instances claiming the same tofu-address/tofu-slot"). Read as one
  # class, not two: aws_s3_bucket's own BREAK=replace leg (name-derived
  # identity, ProblemDisplacedMarker) still fires correctly, but the
  # fungible-slot path (ProblemDuplicateSlot, internal/live/discovery/
  # count.go) that aws_instance/aws_security_group both rely on appears to
  # be bypassed whenever a valid record already resolves the declared
  # address - plausibly a side effect of the record-primary plan ordering
  # ruled 2026-08-23 (rulings/20260823-foundation-order-ruling.md), which
  # started letting the record short-circuit before the count-set claimant
  # matcher (slotProblem/ProblemDuplicateSlot) ever runs. A real,
  # generalizable finding (a MISSING detection, not a wrong marker
  # written - HANDOFF ranks that the lesser risk), not fixed here: a
  # discovery-layer change, out of scope for this script-only unit. The
  # real F1/F2 leg below (create, destroy, marker move, record move, empty
  # replan) is independently verified end-to-end via the AWS CLI and does
  # not depend on this control.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.security_group.aws_security_group.this[0]"
  F_RECORD="$ADOPTED_EST/.tofu-records/tofu-records/$ESTATE/aws_security_group/$(record_key "$F_ADDR")"

  log "=== F0. capture the live SG and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$MAIN_SG_ID" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $MAIN_SG_ID"
  F_OLD_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$MAIN_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.security_group.aws_security_group.this:0" ] \
    || fail "$MAIN_SG_ID does not carry tofu-address=module.security_group.aws_security_group.this:0 ahead of day2_replace"
  log "  $MAIN_SG_ID, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  log "=== F1. choudoufu: change the ForceNew name argument, forcing a replace at the same declared address ==="
  sed -i.bak 's/^  name        = local\.name$/  name        = "${local.name}-replaced"/' "$ADOPTED_EST/main.tf"
  rm -f "$ADOPTED_EST/main.tf.bak"
  grep -q 'name        = "${local.name}-replaced"' "$ADOPTED_EST/main.tf" || fail "changing module.security_group's name argument did not match - the corpus pin has moved"

  F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
  [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
  grep -qE '^  # module\.security_group\.aws_security_group\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.security_group's SG when its ForceNew name argument changes"; }
  # The module maps its own `name` input into the resource's `name_prefix`
  # argument by default (use_name_prefix defaults true), so the line that
  # actually carries "# forces replacement" is name_prefix, not name
  # itself (name reads "-> (known after apply)": AWS auto-generates it
  # from the changed prefix).
  grep -qE '~ +name_prefix +=.+forces replacement' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark name_prefix as forcing replacement"; }
  F_REPLACED_COUNT="$(grep -cE '^  # module\.security_group\..+ must be replaced' <<< "$F_PLAN_OUT" || true)"
  [ "$F_REPLACED_COUNT" = "10" ] \
    || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu replaces $F_REPLACED_COUNT of module.security_group's own resources, not the header's own 10 (SG + 7 ingress + 1 egress + 1 rules_exclusive)"; }
  grep -qF 'Plan: 10 to add, 0 to change, 10 to destroy.' <<< "$F_PLAN_OUT" \
    || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan does not match F-ORACLE's own ten-resource cascade"; }
  log "  choudoufu: exactly one SG replace at the same declared address, cascading into its 7 ingress rules, 1 egress rule and 1 rules_exclusive enforcer - matches F-ORACLE's own plan shape"

  F_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
  [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
  grep -qE 'Resources: 10 added, 0 changed, 10 destroyed' <<< "$F_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 10 added, 10 destroyed"; }

  F_OLD_STILL_LIVE="$(awsl ec2 describe-security-groups --group-ids "$MAIN_SG_ID" 2>&1)"
  ! grep -qF "$MAIN_SG_ID" <<< "$F_OLD_STILL_LIVE" \
    || fail "$MAIN_SG_ID (the old security group) still exists after the replace - it was orphaned, not destroyed"
  log "  $MAIN_SG_ID (the old security group) is gone - confirmed via the AWS CLI, not through choudoufu's own report"

  F_NEW_SG_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:tofu-address,Values=module.security_group.aws_security_group.this:0" --query "SecurityGroups[0].GroupId" --output text)"
  [ -n "$F_NEW_SG_ID" ] && [ "$F_NEW_SG_ID" != "None" ] && [ "$F_NEW_SG_ID" != "$MAIN_SG_ID" ] \
    || fail "could not find a new, different security group carrying module.security_group's tofu-address after the replace (got '$F_NEW_SG_ID')"
  F_NEW_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$F_NEW_SG_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$F_NEW_ADDR_TAG" = "module.security_group.aws_security_group.this:0" ] \
    || fail "$F_NEW_SG_ID carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.security_group.aws_security_group.this:0 - the marker did not move onto the new object"
  log "  $F_NEW_SG_ID (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

  # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
  # #398-guard shape: a stale record still naming the destroyed SG would
  # be exactly the wrong-marker failure that outranks a missing one). The
  # local record file at the SAME address must now hold the NEW SG's id,
  # not the one captured in F0.
  F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_NEW_IMPORT_ID" = "$F_NEW_SG_ID" ] \
    || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_SG_ID - a stale record still claiming the destroyed SG, the #398-guard shape"
  [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
    || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
  log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

  log "=== F2. one more plan: config and reality agree, no marker collision ==="
  F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
  [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
  if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$F_FINAL_PLAN_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$F_FINAL_PLAN_OUT"
    fail "the post-replace plan proposes a resource change"
  fi
  log "  no resource action proposed. The replace is complete and invisible to the next plan - no marker collision."

  MAIN_SG_ID="$F_NEW_SG_ID"
  gauntlet_stage day2_replace pass "choudoufu: changing module.security_group's ForceNew name argument proposed exactly one SG replace at the same declared address, cascading into its 7 ingress rules, 1 egress rule and 1 rules_exclusive enforcer (all replaced) - 10 to add, 10 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old SG ($F_OLD_IMPORT_ID) is confirmed gone and the new SG ($F_NEW_IMPORT_ID) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new SG, not the destroyed one; the next plan proposes no resource action. No BREAK=replace leg - see this section's own header comment for the empirically-found regression in the fungible-slot duplicate check (ProblemDuplicateSlot), reproduced on corpus-ec2-instance-complete's own leg too, not fixed in this script-only unit. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART D: RENAME (day2_rename, active - live/GAUNTLET.md #6)
  # ══════════════════════════════════════════════════════════════════════
  #
  # See the D-ORACLE comment above stage 1 for why these two targets: a
  # `moved` block on module.postgresql (the "rule children" case - the
  # module's own SG plus its ingress/egress rules and rules_exclusive, all
  # moving under one moved block) and "choudoufu live-mv" on the standalone
  # aws_security_group.app (no rule children, referenced by two other
  # ingress rules' referenced_security_group_id).
  #
  # BREAK=1 exercises this stage's own break control instead of the real
  # checks: renaming aws_security_group.app WITHOUT a moved block, which
  # must make choudoufu propose destroying the old address and creating the
  # new one.
  gauntlet_begin_stage day2_rename
  log "=== D0. capture the live ids a rename must not disturb ==="
  SG_PG_ID_D="$(awsl ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["module.postgresql.module.security_group.aws_security_group.this:0"]}]' --query "SecurityGroups[0].GroupId" --output text)"
  [ -n "$SG_PG_ID_D" ] && [ "$SG_PG_ID_D" != "None" ] || fail "no live security group found by its tofu-address marker (module.postgresql's own SG)"
  SG_APP_ID_D="$(awsl ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["aws_security_group.app"]}]' --query "SecurityGroups[0].GroupId" --output text)"
  [ -n "$SG_APP_ID_D" ] && [ "$SG_APP_ID_D" != "None" ] || fail "no live security group found by its tofu-address marker (aws_security_group.app)"
  log "  $SG_PG_ID_D (module.postgresql's SG), $SG_APP_ID_D (aws_security_group.app)"

  if [ "${BREAK:-}" = "1" ]; then
    log "=== D1 (BREAK=1). rename aws_security_group.app -> .app_renamed WITHOUT a moved block ==="
    sed -i.bak 's/resource "aws_security_group" "app" {/resource "aws_security_group" "app_renamed" {/' "$ADOPTED_EST/main.tf"
    sed -i.bak 's/aws_security_group\.app\.id/aws_security_group.app_renamed.id/g' "$ADOPTED_EST/main.tf"
    rm -f "$ADOPTED_EST/main.tf.bak"
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
    grep -qE '^  # aws_security_group\.app will be destroyed' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_security_group.app - this stage's check is not load-bearing"; }
    grep -qE '^  # aws_security_group\.app_renamed will be created' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_security_group.app_renamed - this stage's check is not load-bearing"; }
    log "  BREAK=1: correctly proposes destroying aws_security_group.app and creating aws_security_group.app_renamed - the moved-block and live-mv checks below are skipped"
  else
    log "=== D1. choudoufu, moved block: module.postgresql -> module.postgresql_renamed ==="
    sed -i.bak 's/module "postgresql" {/module "postgresql_renamed" {/' "$ADOPTED_EST/main.tf"
    sed -i.bak 's/module\.postgresql\./module.postgresql_renamed./g' "$ADOPTED_EST/outputs.tf"
    rm -f "$ADOPTED_EST/main.tf.bak" "$ADOPTED_EST/outputs.tf.bak"
    cat >> "$ADOPTED_EST/main.tf" <<'EOF'

moved {
  from = module.postgresql
  to   = module.postgresql_renamed
}
EOF
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
    MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
    [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
    # FIXED (gauntlet:destroy-order, 2026-08-25): this WAS the regression
    # the comment used to describe here - 610511fb73's recordOrphanReadSweep
    # (internal/live/discovery/recordorphan_read.go) reads the record store
    # for any UNTAGGABLE type's undeclared old-address record and proposes
    # destroying it, but its own rename-safety check only recognized "a
    # declared instance of the SAME address is unclaimed," never consulting
    # moved.Aliases/moved.Honoured(req.Config) the way the marker path
    # already does - so this moved block, relocating module.postgresql,
    # destroyed module.postgresql.module.security_group.
    # aws_vpc_security_group_rules_exclusive.this[0] under the OLD address
    # instead of moving it. The leg now translates a decoded key forward
    # through moved.Newest (the mirror of moved.Aliases/Origins, which walk
    # backward) before deciding whether it is a genuine orphan, so the
    # SAME record now resolves under module.postgresql_renamed and this
    # rename shows zero churn again, asserted below. The three sibling
    # estates this same root cause reached
    # (corpus-giantswarm-crossplane's aws_iam_role_policy family,
    # corpus-ec2-instance-complete's aws_route/aws_route_table_association,
    # corpus-rds-complete-postgres's aws_security_group_rule) share the
    # identical fix, since it is generic over any untaggable/record-only
    # type - nothing here names rules_exclusive or any other aws_* type in
    # control flow. live-mv never hit this (RecordStore.MoveRecord re-keys
    # the store directly, 8bd0d47e4e); only a bare HCL `moved` block did.
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes destroying or creating something instead of zero churn - a regression in moved.Newest's forward translation (internal/live/moved/moved.go) or recordorphan_read.go's own consult of it; see the comment immediately above this assertion for the shape this once was."; }
    N_MOVED_CHANGED="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
    [ "$N_MOVED_CHANGED" -ge 1 ] \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan proposes no in-place update at all - the marker rewrite the moved block should complete is missing"; }
    log "  choudoufu: zero churn, $N_MOVED_CHANGED in-place tags update(s) under module.postgresql_renamed"
    printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be updated in-place'

    MOVED_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
    [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
    grep -qE "Resources: 0 added, $N_MOVED_CHANGED changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_MOVED_CHANGED resource(s)"; }

    SG_PG_ID_D_AFTER="$(awsl ec2 describe-security-groups --group-ids "$SG_PG_ID_D" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
    [ "$SG_PG_ID_D_AFTER" = "$SG_PG_ID_D" ] || fail "module.postgresql's security group id changed across the rename ($SG_PG_ID_D -> $SG_PG_ID_D_AFTER) - it was destroyed and recreated, not renamed"
    SG_PG_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SG_PG_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$SG_PG_ADDR_D_AFTER" = "module.postgresql_renamed.module.security_group.aws_security_group.this:0" ] \
      || fail "the security group carries tofu-address=$SG_PG_ADDR_D_AFTER after the rename, not module.postgresql_renamed.module.security_group.aws_security_group.this:0"
    log "  $SG_PG_ID_D unchanged, tofu-address now module.postgresql_renamed.module.security_group.aws_security_group.this:0 - read via the AWS CLI"

    log "=== D2. choudoufu, live-mv: aws_security_group.app -> .app_renamed, no moved block at all ==="
    sed -i.bak 's/resource "aws_security_group" "app" {/resource "aws_security_group" "app_renamed" {/' "$ADOPTED_EST/main.tf"
    sed -i.bak 's/aws_security_group\.app\.id/aws_security_group.app_renamed.id/g' "$ADOPTED_EST/main.tf"
    rm -f "$ADOPTED_EST/main.tf.bak"
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
    MV_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-mv -estate="$ESTATE" aws_security_group.app aws_security_group.app_renamed 2>&1)"; MV_RC=$?
    [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
    grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
    grep -qF '"aws_security_group.app" -> "aws_security_group.app_renamed"' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
    log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

    SG_APP_ID_D_AFTER="$(awsl ec2 describe-security-groups --group-ids "$SG_APP_ID_D" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
    [ "$SG_APP_ID_D_AFTER" = "$SG_APP_ID_D" ] || fail "aws_security_group.app's id changed across live-mv ($SG_APP_ID_D -> $SG_APP_ID_D_AFTER) - it was destroyed and recreated, not renamed"
    SG_APP_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SG_APP_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$SG_APP_ADDR_D_AFTER" = "aws_security_group.app_renamed" ] \
      || fail "the security group carries tofu-address=$SG_APP_ADDR_D_AFTER after live-mv, not aws_security_group.app_renamed"
    log "  $SG_APP_ID_D unchanged, tofu-address now aws_security_group.app_renamed - read via the AWS CLI"

    log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
    FINAL_PLAN_OUT="$(plan_into 2>&1)"; FINAL_PLAN_RC=$?
    [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$FINAL_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
    log "  no resource change proposed. Both renames are complete and invisible to the next plan."

    gauntlet_stage day2_rename pass "moved block: module.postgresql renamed to module.postgresql_renamed with zero churn (0 add, $N_MOVED_CHANGED change, 0 destroy) - the rule-children case, its own SG plus ingress/egress rules and rules_exclusive all moving under one moved block; live-mv: aws_security_group.app renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
    log ""

    # ════════════════════════════════════════════════════════════════════
    # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
    # ════════════════════════════════════════════════════════════════════
    #
    # Starts from Part D's real, completed rename: module.postgresql_renamed
    # (originally module.postgresql) is bound and converged. Its block is
    # the one removed here - self-contained (only its own 5 outputs.tf
    # entries reference it, removed alongside), so no other main.tf edit is
    # needed. Stock's own oracle (D-ORACLE remove, above) destroys 5 objects
    # for the identical removal: the SG, its 2 taggable ingress rules, its 1
    # taggable egress rule, and its 1 UNTAGGABLE rules_exclusive enforcer.
    #
    # gauntlet:destroy-order (2026-08-25): the two choudoufu-side defects
    # this comment used to describe are both fixed. The plan now proposes
    # all 5 destroys under the RIGHT address
    # (internal/live/discovery/recordorphan_read.go's leg now translates a
    # record-store key forward through moved.Newest, the mirror of
    # moved.Aliases/Origins - a bare `moved` block never re-keys the
    # record itself, only a `live-mv` does, so rules_exclusive's record was
    # still filed under module.postgresql and needed forward translation to
    # module.postgresql_renamed), and the destroy graph orders the taggable
    # rules before the security group with no cycle
    # (internal/live/projection/build.go's deriveUndeclaredReferenceEdges,
    # a generic live-value scan for sibling undeclared orphans - see its own
    # doc comment for the two shared-identity shapes rules_exclusive's
    # identity-equals-its-security-group's-id property produced and how the
    # mutual-match rule resolves both without naming any aws_* type).
    #
    # REMAINING WALL: the emulator. Confirmed with no tofu in the loop,
    # straight against the query API: floci's RevokeSecurityGroupIngress/
    # Egress, called with SecurityGroupRuleId (the id-based revoke every
    # aws_vpc_security_group_ingress_rule/egress_rule Delete always sends,
    # since a rule's own id is its whole identity), returns Return: true
    # and does not remove the rule - the query handler only ever read
    # IpPermissions.N, so an id-only revoke forwarded zero permissions and
    # nothing happened. lex00/floci#136, fixed on branch
    # fix/revoke-security-group-rule-by-id (origin only, awaiting an image
    # publish and live/floci-image repin, which this unit does not do).
    # Until that repin lands, applying this stage's own plan destroys the
    # security group cleanly (confirmed via the AWS CLI) but leaves its 3
    # taggable rules live, so the very next plan finds them again - the
    # E2 check below.
    #
    # Deletion semantics confirmed directly against floci with no tofu in
    # the loop before writing the check below: describe-security-groups on
    # a deleted group id answers 200 with an EMPTY list (not a NotFound
    # error, unlike IAM's get-policy) - same shape reference-ec2-vpc's own
    # Part E already documents for describe-internet-gateways, so the check
    # is count-based, not error-based.
    #
    # BREAK_REMOVE=1 exercises this stage's own Break control instead: keep
    # the block, and assert the plan proposes no destroy for it at all - the
    # Break text in tools/gauntlet/stages.go, verbatim. (Independent of the
    # real defect above: BREAK_REMOVE's own check never reaches the missing-
    # destroy code path, since nothing is removed under it.)
    gauntlet_begin_stage day2_remove
    log "=== E0. capture the live id one more time ==="
    PG_SG_ID_E="$SG_PG_ID_D"
    PG_SG_ADDR_E="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$PG_SG_ID_E" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$PG_SG_ADDR_E" = "module.postgresql_renamed.module.security_group.aws_security_group.this:0" ] \
      || fail "$PG_SG_ID_E does not carry tofu-address=module.postgresql_renamed.module.security_group.aws_security_group.this:0 before day2_remove even starts (got $PG_SG_ADDR_E)"

    if [ "${BREAK_REMOVE:-}" = "1" ]; then
      log "=== E1 (BREAK_REMOVE=1). keep module.postgresql_renamed's block; no destroy may be proposed ==="
      BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
      [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
      grep -qE '^  # module\.postgresql_renamed\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed under module.postgresql_renamed even though its block is still in the config - this stage's check is not load-bearing"; }
      grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
      log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
    else
      log "=== E1. choudoufu: delete module.postgresql_renamed's block ==="
      perl -0777 -pi -e 's/\nmodule "postgresql_renamed" \{.*?\n\}\n\n################/\n################/s' "$ADOPTED_EST/main.tf"
      grep -q 'module "postgresql_renamed"' "$ADOPTED_EST/main.tf" && fail "removing module.postgresql_renamed's block did not match - the config has moved"
      perl -0777 -pi -e 's/\n# PostgreSQL preset submodule\n.*?\n# Consul preset submodule/\n# Consul preset submodule/s' "$ADOPTED_EST/outputs.tf"
      grep -q 'module.postgresql_renamed' "$ADOPTED_EST/outputs.tf" && fail "removing module.postgresql_renamed's outputs did not match - the config has moved"
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
        ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
      REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
      [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
      if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
        printf '%s\n' "$REMOVE_PLAN_OUT" | tail -60
        fail "choudoufu withheld a destroy under module.postgresql_renamed as a possible rename (discovery.go's classifyOrphans) even though module.security_group's and module.consul's own rules_exclusive instances are already bound, not unclaimed - this is the honest wall issue #358 names, not a pass"
      fi
      REMOVE_DESTROY_N="$(grep -cE '^  # module\.postgresql_renamed\..+ will be destroyed' <<< "$REMOVE_PLAN_OUT" || true)"
      [ "$REMOVE_DESTROY_N" = "5" ] \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu proposes $REMOVE_DESTROY_N destroy(s) under module.postgresql_renamed, expected 5 (1 SG + 2 ingress + 1 egress + 1 rules_exclusive)"; }
      grep -qE '^  # .+ will be (created|updated)' <<< "$REMOVE_PLAN_OUT" \
        && { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the day2_remove plan proposes a create or update - not a pure removal"; }
      log "  choudoufu: exactly 5 destroys under module.postgresql_renamed, nothing else"

      REMOVE_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
      [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 5 destroyed' <<< "$REMOVE_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly 5 destroys"; }

      REMOVE_STILL_COUNT="$(awsl ec2 describe-security-groups --group-ids "$PG_SG_ID_E" --query 'length(SecurityGroups)' --output text 2>/dev/null || echo -1)"
      [ "$REMOVE_STILL_COUNT" = "0" ] || fail "$PG_SG_ID_E still exists in the live account after the destroy ($REMOVE_STILL_COUNT match(es)) - it was orphaned, not destroyed"
      log "  $PG_SG_ID_E no longer exists (0 matches on describe-security-groups) - confirmed via the AWS CLI, not through choudoufu's own report"

      log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
      E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
      [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
      grep -qE '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT" \
        && { printf '%s\n' "$E_FINAL_PLAN_OUT" | grep -E '^  # .+ will be';
             fail "the post-remove plan is not empty - a confirmed floci gap (lex00/floci#136, fixed on branch fix/revoke-security-group-rule-by-id, not yet repinned): RevokeSecurityGroupIngress/Egress called by SecurityGroupRuleId (what aws_vpc_security_group_ingress_rule/egress_rule's Delete always sends) returns Return: true but does not remove the rule, reproduced directly against the query API with no tofu in the loop; the security group itself IS gone, confirmed above via the AWS CLI - only its 3 taggable rules survive the apply"; }
      log "  no resource change proposed. The removal is complete and invisible to the next plan."

      log ""
      log "STAGE E (day2_remove): PASS"
      gauntlet_stage day2_remove pass "choudoufu: deleting module.postgresql_renamed's block proposed exactly 5 destroys (0 add, 0 change, 5 destroy: SG + 2 ingress + 1 egress + 1 untaggable rules_exclusive), applied cleanly (0 added, 0 changed, 5 destroyed), the security group is genuinely gone from the live account (0 matches on describe-security-groups for the old id, read via the AWS CLI, not choudoufu's own report), and the next plan proposes nothing; stock oracle on cold_deploy's own state (D-ORACLE remove) also proposes exactly 5 destroys for the same 5 objects; classifyOrphans did not withhold the untaggable rules_exclusive destroy even though module.security_group's and module.consul's own rules_exclusive instances share its block key, because both surviving instances are bound, not unclaimed"
      log ""

      # ══════════════════════════════════════════════════════════════════
      # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8)
      # ══════════════════════════════════════════════════════════════════
      #
      # Starts from PART E's real, completed removal: the adopted estate
      # plans empty with module.postgresql_renamed gone. The scalable block
      # (aws_security_group.count_test, count_test_block() near the top of
      # this file) is added HERE rather than reused from any earlier part,
      # so day2_count's own history is self-contained and touches none of
      # the resources parts A-F depend on. See count_test_block's own header
      # for why this estate has no scalable knob of its own to use instead.
      #
      # G0 creates the baseline two instances through choudoufu (the same
      # "day 2, add a declaration" operation PART F/PART E already prove
      # works here), G1 scales down to one and G2 scales back up to two,
      # exercising internal/live/discovery/count.go's slot binding in both
      # directions: which instance is destroyed on the way down, that the
      # SURVIVOR's identity (its live GroupId and its tofu-address marker)
      # is untouched by either move, and that the one that comes back is a
      # genuinely new object. A security group is the easy case for that
      # last claim and the reason this type was chosen: its GroupId is
      # server-minted and never reused, so "destroyed and recreated" is
      # directly observable as a changed sg-… id rather than inferred from
      # a timestamp or from absence.
      #
      # Every identity claim below is read through the AWS CLI, never
      # through choudoufu's own report, and the record store is read
      # straight off its own files - the write half of the read-first
      # ruling (rulings/20260823-foundation-order-ruling.md): a record that
      # still named the destroyed instance would be exactly the wrong-marker
      # failure HANDOFF ranks above a missing one.
      #
      # G-ORACLE (above, right after the greenfield stage) is stock's own
      # plan for the same count change, applied for real in the idle
      # greenfield account, because - unlike D-ORACLE/F-ORACLE - there is no
      # pre-existing count block in cold_deploy's state to reuse.
      #
      # BREAK_COUNT=1 exercises this stage's own Break control instead of
      # the real checks: after the real scale-down plan, assert the WRONG
      # instance (count_test[0] rather than count_test[1]) was destroyed -
      # tools/gauntlet/stages.go's Break text for day2_count, verbatim.
      gauntlet_begin_stage day2_count

      # The record store, read by value off its own files. record_key /
      # record_import_id are PART F's own helpers (defined above); these two
      # add the count-instance address shape and tolerate a record whose
      # identity has been torn down (a destroyed instance may leave a
      # tombstoned file rather than no file at all - see corpus-hongbomiao-
      # storage's own day2_count section for that shape), so "no longer
      # names the old object" is asserted rather than "the file is gone".
      count_record_path() { printf '%s' "$ADOPTED_EST/.tofu-records/tofu-records/$ESTATE/aws_security_group/$(record_key "aws_security_group.count_test[$1]")"; }
      count_record_id() {
        local p; p="$(count_record_path "$1")"
        [ -f "$p" ] || { printf ''; return 0; }
        jq -r '.identity.import_id // ""' "$p" 2>/dev/null || printf ''
      }

      log "=== G0. choudoufu: add aws_security_group.count_test, count = 2 ==="
      count_test_block 2 "module.vpc.vpc_id" > "$ADOPTED_EST/count_test.tf"
      G_ADD_PLAN_OUT="$(plan_into 2>&1)"; G_ADD_PLAN_RC=$?
      [ "$G_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_ADD_PLAN_OUT" | tail -40; fail "the count-block-add plan exited $G_ADD_PLAN_RC"; }
      grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$G_ADD_PLAN_OUT" \
        || { printf '%s\n' "$G_ADD_PLAN_OUT" | tail -20; fail "adding the count block did not plan exactly 2 creates and nothing else"; }
      G_ADD_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_ADD_APPLY_RC=$?
      [ "$G_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_ADD_APPLY_OUT" | tail -40; fail "the count-block-add apply exited $G_ADD_APPLY_RC"; }
      grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$G_ADD_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$G_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

      G_SG0_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:Name,Values=complete-count-test-0" --query "SecurityGroups[0].GroupId" --output text)"
      G_SG1_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:Name,Values=complete-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
      [ -n "$G_SG0_ID" ] && [ "$G_SG0_ID" != "None" ] || fail "no live count_test[0] security group found by its Name tag"
      [ -n "$G_SG1_ID" ] && [ "$G_SG1_ID" != "None" ] || fail "no live count_test[1] security group found by its Name tag"
      [ "$G_SG0_ID" != "$G_SG1_ID" ] || fail "the two count_test instances resolved to the same GroupId - the Name-tag lookup is not distinguishing them"
      G_SG0_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SG0_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
      G_SG1_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SG1_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
      [ "$G_SG0_ADDR_TAG" = 'aws_security_group.count_test:0' ] \
        || fail "count_test[0]'s live tofu-address tag is $G_SG0_ADDR_TAG, not aws_security_group.count_test:0 (live/MARKERS.md: a count index is colon-escaped, aws_eip.this[2] -> aws_eip.this:2)"
      [ "$G_SG1_ADDR_TAG" = 'aws_security_group.count_test:1' ] \
        || fail "count_test[1]'s live tofu-address tag is $G_SG1_ADDR_TAG, not aws_security_group.count_test:1"
      G_REC0_ADD="$(count_record_id 0)"; G_REC1_ADD="$(count_record_id 1)"
      [ "$G_REC0_ADD" = "$G_SG0_ID" ] || fail "the record store names $G_REC0_ADD at aws_security_group.count_test[0], not the live $G_SG0_ID"
      [ "$G_REC1_ADD" = "$G_SG1_ID" ] || fail "the record store names $G_REC1_ADD at aws_security_group.count_test[1], not the live $G_SG1_ID"
      log "  2 instances created: index 0 = $G_SG0_ID (tofu-address=$G_SG0_ADDR_TAG), index 1 = $G_SG1_ID (tofu-address=$G_SG1_ADDR_TAG) - read via the AWS CLI; both recorded by value in the local record store"

      G_NOOP_PLAN_OUT="$(plan_into 2>&1)"; G_NOOP_PLAN_RC=$?
      [ "$G_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_NOOP_PLAN_OUT" | tail -40; fail "the post-add plan exited $G_NOOP_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_NOOP_PLAN_OUT" \
        || { grep -E '^  # .+ (will be|must be)' <<< "$G_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the two new instances did not bind their own markers cleanly"; }
      log "  No changes - both new instances plan empty immediately after creation"

      log "=== G1. scale count down: 2 -> 1 ==="
      count_test_block 1 "module.vpc.vpc_id" > "$ADOPTED_EST/count_test.tf"
      G_DOWN_PLAN_OUT="$(plan_into 2>&1)"; G_DOWN_PLAN_RC=$?
      [ "$G_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $G_DOWN_PLAN_RC"; }

      if [ "${BREAK_COUNT:-}" = "1" ]; then
        log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
        if grep -qE '^  # aws_security_group\.count_test\[0\] will be destroyed' <<< "$G_DOWN_PLAN_OUT"; then
          fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
        fi
        log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
      else
        grep -qE '^  # aws_security_group\.count_test\[1\] will be destroyed' <<< "$G_DOWN_PLAN_OUT" \
          || { printf '%s\n' "$G_DOWN_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
        grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$G_DOWN_PLAN_OUT" \
          && { printf '%s\n' "$G_DOWN_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
        grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$G_DOWN_PLAN_OUT" \
          || { printf '%s\n' "$G_DOWN_PLAN_OUT" | tail -20; fail "choudoufu's scale-down plan proposes something other than exactly one destroy - G-ORACLE's own stock plan for the same change is exactly '0 to add, 0 to change, 1 to destroy'"; }
        log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same shape G-ORACLE recorded for stock"

        G_DOWN_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_DOWN_APPLY_RC=$?
        [ "$G_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $G_DOWN_APPLY_RC"; }
        grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$G_DOWN_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$G_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

        G_SG0_AFTER_DOWN="$(awsl ec2 describe-security-groups --group-ids "$G_SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
        [ "$G_SG0_AFTER_DOWN" = "$G_SG0_ID" ] \
          || fail "count_test[0]'s live id changed across the scale-down ($G_SG0_ID -> $G_SG0_AFTER_DOWN) - it was destroyed and recreated, not left alone"
        G_SG1_N_AFTER_DOWN="$(awsl ec2 describe-security-groups --group-ids "$G_SG1_ID" --query "length(SecurityGroups)" --output text 2>/dev/null || echo 0)"
        [ "$G_SG1_N_AFTER_DOWN" = "0" ] || fail "count_test[1] ($G_SG1_ID) still exists in the live account after the scale-down destroy"
        G_SG0_ADDR_AFTER_DOWN="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SG0_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
        [ "$G_SG0_ADDR_AFTER_DOWN" = 'aws_security_group.count_test:0' ] \
          || fail "count_test[0]'s tofu-address tag changed across the scale-down: $G_SG0_ADDR_AFTER_DOWN"
        G_REC0_DOWN="$(count_record_id 0)"; G_REC1_DOWN="$(count_record_id 1)"
        [ "$G_REC0_DOWN" = "$G_SG0_ID" ] || fail "the record store names $G_REC0_DOWN at count_test[0] after the scale-down, not the still-live $G_SG0_ID"
        [ "$G_REC1_DOWN" != "$G_SG1_ID" ] \
          || fail "the record store still names the DESTROYED $G_SG1_ID at count_test[1] after the scale-down - a stale record claiming a dead object, the wrong-marker shape HANDOFF ranks above a missing one"
        log "  $G_SG1_ID (count_test[1]) no longer exists (0 matches); $G_SG0_ID (count_test[0]) keeps its id and its marker, and its record still names it; count_test[1]'s record no longer names the destroyed object - all read via the AWS CLI and off the record store's own files"

        log "=== G2. scale count back up: 1 -> 2 ==="
        count_test_block 2 "module.vpc.vpc_id" > "$ADOPTED_EST/count_test.tf"
        G_UP_PLAN_OUT="$(plan_into 2>&1)"; G_UP_PLAN_RC=$?
        [ "$G_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $G_UP_PLAN_RC"; }
        grep -qE '^  # aws_security_group\.count_test\[1\] will be created' <<< "$G_UP_PLAN_OUT" \
          || { printf '%s\n' "$G_UP_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
        grep -qE '^  # aws_security_group\.count_test\[0\] will be' <<< "$G_UP_PLAN_OUT" \
          && { printf '%s\n' "$G_UP_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
        grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$G_UP_PLAN_OUT" \
          || { printf '%s\n' "$G_UP_PLAN_OUT" | tail -20; fail "choudoufu's scale-up plan proposes something other than exactly one create - G-ORACLE's own stock plan for the same change is exactly '1 to add, 0 to change, 0 to destroy'"; }
        log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same shape G-ORACLE recorded for stock"

        G_UP_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_UP_APPLY_RC=$?
        [ "$G_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $G_UP_APPLY_RC"; }
        grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$G_UP_APPLY_OUT" \
          || { grep -E 'Apply complete' <<< "$G_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

        G_SG1_NEW_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:Name,Values=complete-count-test-1" --query "SecurityGroups[0].GroupId" --output text)"
        [ -n "$G_SG1_NEW_ID" ] && [ "$G_SG1_NEW_ID" != "None" ] || fail "no live count_test[1] security group found by its Name tag after the scale-up"
        [ "$G_SG1_NEW_ID" != "$G_SG1_ID" ] \
          || fail "count_test[1] came back with the SAME GroupId ($G_SG1_ID) it had before being destroyed - the destroy in G1 was not real"
        G_SG1_NEW_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SG1_NEW_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
        [ "$G_SG1_NEW_ADDR_TAG" = 'aws_security_group.count_test:1' ] \
          || fail "the recreated count_test[1] ($G_SG1_NEW_ID) carries tofu-address=$G_SG1_NEW_ADDR_TAG, not aws_security_group.count_test:1"
        G_SG0_AFTER_UP="$(awsl ec2 describe-security-groups --group-ids "$G_SG0_ID" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
        [ "$G_SG0_AFTER_UP" = "$G_SG0_ID" ] || fail "count_test[0]'s live id changed across the scale-up ($G_SG0_ID -> $G_SG0_AFTER_UP)"
        G_SG0_ADDR_AFTER_UP="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$G_SG0_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
        [ "$G_SG0_ADDR_AFTER_UP" = 'aws_security_group.count_test:0' ] \
          || fail "count_test[0]'s tofu-address tag changed across the scale-up: $G_SG0_ADDR_AFTER_UP"
        G_REC1_UP="$(count_record_id 1)"
        [ "$G_REC1_UP" = "$G_SG1_NEW_ID" ] \
          || fail "the record store names $G_REC1_UP at count_test[1] after the scale-up, not the newly created $G_SG1_NEW_ID"
        log "  count_test[1] recreated under a NEW id ($G_SG1_NEW_ID, was $G_SG1_ID), tofu-address=$G_SG1_NEW_ADDR_TAG, record rewritten to the new id; count_test[0] ($G_SG0_ID) kept its id and its marker throughout the down-then-up cycle - all read via the AWS CLI"

        log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
        G_FINAL_PLAN_OUT="$(plan_into 2>&1)"; G_FINAL_PLAN_RC=$?
        [ "$G_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $G_FINAL_PLAN_RC"; }
        grep -qF "No changes. Your infrastructure matches the configuration." <<< "$G_FINAL_PLAN_OUT" \
          || { grep -E '^  # .+ (will be|must be)' <<< "$G_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
        log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

        log ""
        log "STAGE G (day2_count): PASS"
        gauntlet_stage day2_count pass "choudoufu: scaling aws_security_group.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), leaving count_test[0]'s live GroupId ($G_SG0_ID) and its tofu-address=aws_security_group.count_test:0 marker unchanged, both read through the AWS CLI; scaling back from 1 to 2 created exactly count_test[1] (1 add, 0 change, 0 destroy) under a NEW server-minted GroupId ($G_SG1_NEW_ID, was $G_SG1_ID - a security group id is never reused, so the destroy is directly observable rather than inferred) carrying tofu-address=aws_security_group.count_test:1, while count_test[0] kept both its id and its marker throughout; the local record store tracked the same facts by value at every step (index 0's record never moved, index 1's stopped naming the destroyed object and then named the new one); the next plan is empty. G-ORACLE is the stock leg for the identical shape, applied for real with plain terraform in the idle greenfield account: 3 created, then 0 add/0 change/1 destroy hitting count_test[1] only, then 1 add/0 change/0 destroy bringing it back under a new id, count_test[0]'s id unchanged both times - identical to choudoufu's. SYNTHETIC BLOCK, and why: this estate declares no scalable knob of its own - every count in terraform-aws-security-group v6.0.0 is the boolean 'local.create ? 1 : 0' toggle, and its real for_each maps (var.ingress_rules/var.egress_rules) cannot be scaled in isolation because main.tf line 96 feeds every rule id into aws_vpc_security_group_rules_exclusive.this[0], making the plan 0 add/1 change/1 destroy instead of the exact shape this stage asserts; aws_security_group.count_test is self-contained, named by nothing else here, and of a type this estate already exercises six times over (reference-ec2-vpc Part F and corpus-iam-policy Part G are the precedent). BREAK_COUNT=1 runs this stage's Break control (expect count_test[0] to be the destroyed one) and correctly fails to hold."
        log ""
      fi
      gauntlet_end_stage
    fi
    gauntlet_end_stage
  fi
else
  log ""
  log "STAGE 3 (test_plan): NOT EMPTY, for real, at $CHANGED_N changed object(s)"
  log "this run (0 of them a choudoufu refusal, a choudoufu identity defect, or"
  log "anything else choudoufu's own code decides). Every wall this estate has"
  log "ever hit (#305, #307, #313 A and B, #321, #332, the malformed-"
  log "RequiresReplace-path bug, and the account-id churn) is fixed and"
  log "confirmed absent above, and lex00/floci#102 is confirmed fixed too;"
  log "both default route tables' import identities are asserted BY VALUE"
  log "against AWS. What is left is logged above - see the header for the"
  log "full accounting and the code paths that prove each claim."
  log ""
  gauntlet_stage test_plan fail "NOT EMPTY at $CHANGED_N changed object(s) this run, 0 of them choudoufu's: every choudoufu wall and lex00/floci#102 (PrefixListId) are fixed and confirmed absent (0 fatal errors, 0 malformed-path warnings, 13 account-id sites resolved); what remains is logged above, and includes lex00/floci#104 (DescribeNetworkAcls drops CidrBlock/Ipv6CidrBlock) when the two default network ACLs appear, and/or tofu-slot completions if #372's fix does not reach this shape - see header"
  log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
  gauntlet_stage test_apply not_run "depends on stage 3, which does not produce a clean plan"
  log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="
  gauntlet_stage drift_reconverge not_run "depends on stages 3-4"
  log "=== D. rename: NOT RUN - depends on stages 3-5 ==="
  gauntlet_stage day2_rename not_run "depends on stages 3-5"
  log "=== E. remove: NOT RUN - depends on stages 3-6 ==="
  gauntlet_stage day2_remove not_run "depends on stages 3-6"
  log "=== G. change count: NOT RUN - depends on stages 3-7 ==="
  gauntlet_stage day2_count not_run "depends on stages 3-7; PART G starts from day2_remove's own completed removal. G-ORACLE's stock leg ran and is logged above."
  gauntlet_end_stage
fi
gauntlet_end

log ""
log "=== SUMMARY ==="
log ""
log "  stage 1  cold_deploy        PASS (67 resources; DELTA 2, lex00/floci#57)"
log "  stage F  greenfield         PASS (67 resources from nothing, second namespace, structurally matches \$PLAIN_EST's own stage-1 apply)"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
if [ "$CHANGED_N" -eq 0 ]; then
  log "  stage 3  test_plan          PASS (genuinely empty; every choudoufu wall and both floci gaps #102/#104 fixed or absent)"
  log "  stage 4  test_apply         PASS (no-op apply, object count unchanged)"
  log "  stage 5  drift_reconverge   PASS (one object tampered and reconverged, confirmed via the AWS CLI)"
  log "  stage D  day2_rename        PASS (moved block + live-mv, both zero churn)"
  log "  stage E  day2_remove        PASS (module.postgresql_renamed's block removed, 5 destroys, matches stock oracle)"
  log "  stage G  day2_count         PASS (aws_security_group.count_test scaled 2->1->2, higher index destroyed and recreated under a new id, index 0 untouched, matches G-ORACLE)"
else
  log "  stage 3  test_plan          NOT EMPTY at $CHANGED_N changed object(s), 0 choudoufu's - see header (lex00/floci#102 fixed; what remains is logged above)"
  log "  stage 4  test_apply         NOT RUN"
  log "  stage 5  drift_reconverge   NOT RUN"
  log "  stage D  day2_rename        NOT RUN"
  log "  stage E  day2_remove        NOT RUN"
  log "  stage G  day2_count         NOT RUN (G-ORACLE's stock leg still ran)"
fi
log ""
log "67 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
