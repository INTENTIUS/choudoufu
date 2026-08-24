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
# unknowable leaf) -> 0. All four of those sites traced to the identical
# single leaf:
#
#   main.tf:224, in module "security_group": cidr_blocks = module.vpc.
#   vpc_cidr_block
#
# and it was called #313's root cause B for two crossings running - a
# managed resource's own attribute (module.vpc's vpc_cidr_block output is
# `try(aws_vpc.this[0].cidr_block, null)`) read across a module boundary,
# deliberately refused because the attribute may not exist yet within the
# same plan. That reading was wrong for THIS estate, and the thing that
# settled it is that the very same reference, spelled with a constant index
# and no lookup(), resolved the whole time.
#
# What actually stopped it was ROUTING, and both halves are now fixed
# (internal/live/identity/computedselect.go):
#
#   - `var.ingress_with_cidr_blocks[count.index]` is an IndexExpr, not a
#     traversal, so resolver.namedLeaf's hcl.AbsTraversalForExpr gate
#     declined before the chase across the module-call boundary began. The
#     index is now FOLDED - evaluated in this instance's own scope, exactly
#     as resolveIndexedTraversal already evaluates one for
#     aws_subnet.this[count.index].id.
#   - `lookup(<a module-call argument>, "k", <default>)` had no route at
#     all: resolveLookupCall reads each.value alone. The call is now folded
#     into one attribute step, exactly as eachValueSelector already folds
#     the same call for each.value.
#
# Both hand the result to the identical resolveNamed / resolveModuleOutput
# chase the literal-index spelling already took, under every restriction
# that chase already enforces, plus one it does not: a declared type that
# does not convert the selected leaf to itself declines, because lookup()
# answers a dropped attribute with its third argument SILENTLY and
# rendering the caller's expression there would be a wrong marker.
#
# The identity is not a prediction. It is a DEFERRED read of
# aws_vpc.this[0].cidr_block - parentPart's answer for a needs-discovery
# parent since #346's second half - rendered off the live object, which is
# the right answer to #313's own objection rather than an exception to it.
# Step 3 below reads it back through the AWS CLI and asserts it by value.
#
# #313's root cause B itself is untouched and still stands where it really
# is: corpus-security-group-complete's own 7 sites read
# aws_security_group.app.id directly, with no module hop and no chase to
# route, and this fix reaches none of them.
#
# GITHUB ISSUE #368 IS NOT THIS ESTATE'S BLOCKER EITHER, and its own framing
# of this estate was refuted by measurement before this fix landed. #368 was
# filed on the reading that what stops this configuration is the FUNCTION
# applied to the deferred value - `cidr_blocks = compact(split(",", lookup(
# var.ingress_with_cidr_blocks[count.index], "cidr_blocks", join(",",
# var.ingress_cidr_blocks))))` - and that "the gap is specifically the
# function application, not the routing". #368 landed that function
# application (identity.ParentRef grew a render-time Transform;
# corpus-ecs-fargate's own eight identity diagnostics went to zero on it)
# and this estate did not move: still exactly 2 sites, same lines, same
# text. It was the last step this estate needed AFTER the routing, not
# instead of it, and the four-variant reduction that settles it lives in
# internal/live/identity/testdata/deferred-through-module-list with
# deferred_through_module_list_test.go rendering every variant by value.
#
# WHAT IS LEFT, none of it identity, is what step 3 now counts by cause -
# and every line of it is now MEASURED rather than read off the plan and
# attributed by eye, which is what the 2026-08-22 pass changed:
#
#    3 instances want the tofu-slot marker live-import does not write. That
#      was 22 until choudoufu #372 landed. The claim #372 rests on: for a
#      live count set carrying NO slot at all there is nothing for a
#      discovery pass to discover, because the assignment discovery reaches
#      is internal/live/slots.Sequential - slot i for index i, frozen from
#      the same per-instance tofu-address values the migration is already
#      writing in the same call. So live-import writes it, gated on the
#      type being server-assigned, which is the one half of "is this
#      instance ClassNeedsDiscovery" the type table settles without a
#      resolution pass. The 19 that went were the VPC and security-group
#      submodules' count instances; the 3 that stayed - aws_db_subnet_group,
#      aws_default_network_acl, aws_iam_role.enhanced_monitoring - are
#      client-named types whose class depends on their own configuration.
#      Writing a slot for those anyway is not merely unhelpful, it is
#      wrong: measured on corpus-overture-tiles and
#      corpus-dynamodb-table-basic, the next plan proposes REMOVING it.
#      See internal/live/liveimport/slot.go's gate 4.
#    1 aws_db_parameter_group is created, and the reason is the EMULATOR,
#      not the name_prefix sentence the plan prints. name_prefix is why no
#      import identity exists; marker discovery is what would find the
#      object anyway, and on floci it never can. Measured directly at step
#      3f: DescribeDBParameterGroups returns NO DBParameterGroupArn for it,
#      and AddTagsToResource on its 'pg' ARN answers "Tagging for resource
#      type 'pg' is not yet implemented by Floci". Real RDS returns
#      DBParameterGroupArn on every DBParameterGroup and accepts a 'pg' ARN,
#      so the marker would land there. live-import already reports the
#      failure honestly rather than claiming the stamp - "the write reported
#      no error, but the object read back afterwards does not carry the new
#      markers" - and NEEDS_DISCOVERY is the correct classification. Nothing
#      on this side to fix; it wants a floci issue.
#    2 aws_db_instance must be replaced, and STOCK PROPOSES THE SAME TWO.
#      Step 3g now puts the identical question to stock terraform against
#      its own state file and the same cloud, and asserts the answers match:
#      the same two addresses, forced by the same line, `~ storage_encrypted
#      = false -> true # forces replacement`. floci's DescribeDBInstances
#      returns no StorageEncrypted at all (the AWS CLI reads it None), so
#      both binaries read it back false. HANDOFF's third row - stock fails
#      too - proven rather than asserted.
#    1 aws_db_subnet_group and 1 aws_default_network_acl are the same
#      round-trip gap in tags and in rule blocks respectively: the subnet
#      group's Example/Name/Repository tags, set at CreateDBSubnetGroup
#      time, are absent from ListTagsForResource afterwards, while the
#      tofu-* tags written later through AddTagsToResource persist.
#
# THE ONE THING IN THAT RESIDUE THAT WAS CHOUDOUFU'S, found by this crossing
# and FIXED in the same pass (HANDOFF's second row - the plans differ):
# a config-only NESTED BLOCK the provider never reads back was never carried
# in the record, so every stateless replan proposed adding it, forever.
# terraform-aws-modules writes `timeouts { create = "10m" delete = "15m" }`
# on its security group and `timeouts { create = "5m" update = "5m" }` on
# the VPC's default route table; the state file holds them and stock's plan
# renders them "(1 unchanged block hidden)", while choudoufu proposed
# "+ timeouts {...}" on both. internal/live/projection's residueCandidates
# walked schema.Block.Attributes only, with a doc comment stating nested
# blocks as a deliberate bound - and the bound's own stated REASON (a set-
# or map-nested block has no stable per-element form for a whole-value
# comparison) does not reach NestingSingle, which is one value in the
# implied object type exactly like a flat attribute. So the rule is now
# "NestingSingle blocks with nothing sensitive or write-only anywhere inside
# them", the collection modes stay out with their reason intact, and the
# safety is unchanged: classifyResidue's two-read discriminator still
# decides, and aws_default_network_acl's egress/ingress are the worked
# example of a block that fails it. It names no type and no block name.
# Measured here: "+ timeouts {" 2 -> 0, both blocks now render stock's own
# "(1 unchanged block hidden)", and no other line of the plan moved (24
# action headers before and after, 3 to add / 21 to change / 2 to destroy
# both times). Asserted by value in
# internal/live/projection/residue_test.go, and step 3e asserts BOTH
# directions here - the proposal gone AND the block still present.
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
#                          for real (21 VERIFIED + 5 DRIFTED - round-5 repin to
#                          827a6c5a fixed module.db.module.db_instance.aws_db_instance.this[0]'s
#                          enabled_cloudwatch_logs_exports round-trip, lex00/floci PR #121/#120,
#                          moving it out of DRIFTED), the other 13
#                          UNTAGGABLE by provider schema in the dry run -
#                          of which -approve records 1 (module.db_default's
#                          random_id.snapshot_identifier, record-backed
#                          since #340, seeded into the record store rather
#                          than skipped) and correctly skips 12; #305's
#                          default_* trio is admitted now and stamped above.
#                          All asserted against live-import's own report AND
#                          confirmed independently through the AWS CLI.
#   stage 3  test plan     BLOCKED, for real - but the identity layer is
#                          CLEAR: 0 refusals of any kind, where this estate
#                          stood at 7 (masked by #304), then 33 (once the
#                          mask lifted), then 14 (once #321+#324 cleared
#                          every "Identity not resolvable" site), then 2
#                          (once #323 narrowed the module-output cascade),
#                          then 0 (once computedselect.go routed the last
#                          two). Every ingress rule binds to the live object
#                          its identity names, and that identity is read
#                          back BY VALUE through the AWS CLI rather than
#                          through choudoufu's own report. RE-MEASURED
#                          2026-08-23 against current main (26a9d898e4): the
#                          slot, create and replace walls this estate stood
#                          on are ALL gone now - 0 instances missing
#                          tofu-slot (was 3, was 22 - a7073177ed closed
#                          #372's client-named-type remainder), 0 create
#                          (was 1 - the same fix settles the name_prefix
#                          parameter group's slot at migrate time, and
#                          floci's RDS 'pg' tagging works now too, step 3f),
#                          0 replacements (was 2 - 0a2f0291a0's third-image
#                          repin fixed floci's StorageEncrypted round-trip,
#                          step 3g). RE-MEASURED again once INTENTIUS/
#                          choudoufu#393 closed: fillResidue now tells an
#                          ImportResourceState stub's
#                          unconfirmed SDKv2 schema default apart from a
#                          value ReadResource actually produced, so
#                          module.db_default's aws_db_instance no longer
#                          proposes "skip_final_snapshot = true -> false"
#                          forever - that item is FIXED and asserted absent
#                          from BOTH binaries' plans at step 3g. What is
#                          left is 3 in-place updates, on the same three
#                          addresses, now for one reason only:
#                          lex00/floci#120 (8 aws_db_instance/
#                          aws_db_parameter_group arguments floci's
#                          Describe calls never echo back, confirmed
#                          against AWS's own documented API shapes and
#                          reproduced identically on stock terraform's own
#                          plan at step 3g - HANDOFF's third row, the
#                          estate still has to clear once the emulator
#                          does). The other residue item that WAS
#                          choudoufu's and fixable inline - "+ timeouts
#                          {...}" on two instances, a config-only
#                          NestingSingle block the record did not carry -
#                          was fixed in the same earlier pass and is 0.
#                          BREAK=1 is the negative control.
#   stage 4  test apply    NOT RUN - depends on stage 3, which does not
#                          produce a clean plan while lex00/floci#120's
#                          round-trip gaps stand.
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
#   BREAK        set to 1 to corrupt every expected stage-3 count - the
#                five refusal counts this estate no longer has, the
#                aws_security_group_rule action count, every residue count,
#                and the "+ timeouts {" count the block-shaped residue fix
#                drove to zero - proving those assertions are load-bearing
#                rather than a grep that always matches. Stages 1 and 2 are
#                unaffected and still pass; stage 3 is the one that must
#                fail.
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
# SKIPPED is the DRY RUN's untaggable bucket, which -approve then splits in
# two (issue #340): module.db_default's random_id.snapshot_identifier is
# record-backed, so -approve seeds the record store for it and reports it
# RECORDED rather than SKIPPED. The dry run's own UNTAGGABLE count does not
# move - ratifyRecordBacked still answers StatusUntaggable - so only the
# -approve summary line splits.
RECORDED=1
APPROVE_SKIPPED=$((SKIPPED - RECORDED))

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

gauntlet_begin

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

CURRENT_STAGE=cold_deploy
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
gauntlet_stage cold_deploy pass "$INSTANCES resources, once for real"
CURRENT_STAGE=migrate

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
[ "${VERIFIED_N:-0}" = "21" ] || fail "expected 21 VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "5" ] || fail "expected 5 DRIFTED, got ${DRIFTED_N:-0}"
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
log "  $ELIGIBLE of $INSTANCES eligible (21 VERIFIED + 5 DRIFTED); $SKIPPED skipped"
log "  (13 UNTAGGABLE by provider schema); #305's default_* trio is admitted"
log "  now and eligible above; nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, $RECORDED newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $APPROVE_SKIPPED skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ELIGIBLE of $INSTANCES resources cleanly ($RECORDED recorded, $APPROVE_SKIPPED skipped)"; }
log "  $ELIGIBLE stamped, $RECORDED recorded (random_id.snapshot_identifier), 0 failed,"
log "  $APPROVE_SKIPPED skipped - $SKIPPED untaggable in the dry run, one of them record-backed"

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
gauntlet_stage migrate pass "$ELIGIBLE of $INSTANCES stamped"
CURRENT_STAGE=test_plan

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
# The exit code now points the other way. This estate stood at exactly 2
# identity refusals for four fixes running; both are gone, so live-plan
# completes and prints a plan. What it prints is not empty, and the rest of
# this stage says exactly what is in it and why none of it is identity.
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "live-plan exited $PLAN_RC - it exits 0 now that the identity layer is clear, so a non-zero exit is a new refusal"; }
# choudoufu wraps its "In module.X, ... RESOURCE.NAME:" context lines at a
# fixed column when captured non-interactively, sometimes splitting the
# resource name onto its own line. Flattened to one line per diagnostic
# clause (blank-line-separated) so a substring match is not at the mercy of
# where the wrap happened to land.
PLAN_FLAT="$(awk 'BEGIN{RS=""} {gsub(/\n/," "); print; print "@@CLAUSE@@"}' <<< "$PLAN_OUT")"
# The plan's own action list, which is what "did this resource bind" is read
# off. Every other mention of a type name in the output - the 992-type
# not-swept list, the foreign-resource block - is not an action and must not
# be matched.
PLAN_ACTIONS="$(grep -E '^  # ' <<< "$PLAN_OUT")"

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
# THE UNIT THIS RUN RECORDS. #313's root cause B was never this estate's
# wall - it was the ROUTING to it, and both halves are fixed:
# internal/live/identity/computedselect.go folds `var.<list>[count.index]`
# and `lookup(<a module-call argument>, "k", d)` into the traversal the
# author would have written with a constant index, and hands the result to
# the same resolveNamed chase the literal-index spelling already took. The
# module output IS read - as a DEFERRED read of aws_vpc.this[0].cidr_block,
# rendered from the live object rather than predicted from configuration -
# which is parentPart's answer for a needs-discovery parent since #346's
# second half. So both counts are 0 now, and every OTHER refusal is 0 too:
# this estate's identity layer is clear.
WANT_MODOUT_N=0
WANT_CASCADE_N=0
WANT_ERR_N=0
# The rules the fix is actually about: terraform-aws-modules/security-group
# declares one aws_security_group_rule per element of
# ingress_with_cidr_blocks, and this estate's element reads
# module.vpc.vpc_cidr_block. A rule that did not resolve, or resolved to a
# string no live object carries, would appear in the plan's action list as
# "will be created". None may.
WANT_RULE_ACTIONS=0
# What IS left, none of it identity, each with its own cause. RE-MEASURED
# 2026-08-23 against current main (26a9d898e4): the wall this estate stood
# on when SLOT_N/CREATE_N/REPLACE_N were last 3/1/2 has moved twice since,
# and this crossing re-verified all three moves for real rather than
# trusting the artifact's prior detail line:
#
#  - 0 instances want the tofu-slot marker (was 3, was 22 before that).
#    a7073177ed ("live-import: settle tofu-slot at migrate time for a
#    slotless count set", on main 2026-08-22) closed the remainder #372
#    left open: the three CLIENT-NAMED count instances (aws_db_subnet_group,
#    aws_default_network_acl, aws_iam_role.enhanced_monitoring) now settle
#    their slot at migrate time too, the same way the server-assigned
#    majority already did.
#  - 0 creates (was 1). The SAME fix reaches the name_prefix parameter
#    group: it is also a slotless count instance of a server-assigned type,
#    so live-import now settles its slot and tags it directly at migrate
#    time instead of leaving it for marker discovery to find later - and
#    discovery could never find it anyway (see the floci gap below), so
#    this used to surface as a create. It now surfaces as an ordinary
#    in-place update, asserted at step 3f.
#  - 0 replacements (was 2). 0a2f0291a0 ("gauntlet: repin to the third
#    combined emulator image", on main 2026-08-22) fixed floci's
#    StorageEncrypted round-trip: it now reads back exactly what was
#    requested, so the attribute that used to force a replace no longer
#    appears in the plan at all. Confirmed live at step 3g.
#  - 3 in-place updates remain (unchanged in COUNT, but not in cause - the
#    three addresses are the same, module.db and module.db_default's
#    aws_db_instance plus module.db's aws_db_parameter_group, but every
#    attribute driving the diff is new). Two classes, both settled against
#    an independent oracle rather than inferred:
#      * lex00/floci#120 (EMULATOR GAP, filed by this crossing): floci's
#        DescribeDBInstances/DescribeDBParameters never echo back port,
#        backup_window, monitoring_interval, monitoring_role_arn,
#        performance_insights_retention_period, engine_lifecycle_support,
#        enabled_cloudwatch_logs_exports, max_allocated_storage, or a
#        parameter block's apply_method - all eight are real, documented
#        fields of AWS's own DBInstance/Parameter response shapes
#        (confirmed against botocore's rds/2014-10-31/service-2.json, not
#        assumed), and floci returns null (or, for port and
#        backup_window, an outright wrong value) for every one of them
#        regardless of what was actually requested. Confirmed against
#        floci's raw API directly, no tofu in the loop, and confirmed a
#        second, independent way at step 3g: stock terraform's OWN plan
#        against its OWN real, never-deleted state file shows the
#        identical diffs on the identical attributes, because it too
#        refreshes through the same broken emulator. HANDOFF's third row.
#      * INTENTIUS/choudoufu#393 (CHOUDOUFU DEFECT, filed by this
#        crossing) was module.db_default's aws_db_instance additionally
#        proposing "skip_final_snapshot = true -> false" forever, even
#        though the estate's record store correctly held "false" for it.
#        Root cause, confirmed with instrumented debug builds: the AWS
#        provider's ImportResourceState stub for aws_db_instance is
#        seeded with the resource's own SDK schema default (true) for
#        this attribute before any live read happens, and
#        internal/live/projection's fillResidue treated any non-zero bool
#        as "the provider answered", so it trusted the stub's true over
#        the correctly-recorded false. Confirmed absent from stock's own
#        plan at step 3g (stock never goes through an import stub - its
#        real state already has the true applied value), so this was
#        choudoufu's alone, not stock's and not the emulator's. FIXED:
#        fillResidue now takes a provenance signal - the exact PriorState
#        importAndRead fed ReadResource before any read ran - and treats
#        a value that comes back bit-for-bit unchanged from that stub as
#        carrying no information, for exactly the population (a name a
#        residue record already exists for) that classifyResidue already
#        proved the provider does not source from the remote at all. The
#        general zero-value rule (carriesNoInformation) is untouched;
#        classifyResidue's own two-read discriminator has no import stub
#        in its loop and keeps drawing its conclusions the same way it
#        always did. Asserted absent from choudoufu's own plan too, at
#        step 3g, alongside stock's.
WANT_SLOT_N=0
WANT_CREATE_N=0
WANT_REPLACE_N=0
WANT_UPDATE_N=3
# THE BLOCK-SHAPED RESIDUE FINDING, this crossing's own, and the one thing
# in the residue that was choudoufu's. terraform-aws-modules/security-group
# and terraform-aws-modules/vpc both write a `timeouts` block:
#
#   timeouts { create = "10m" delete = "15m" }   aws_security_group
#   timeouts { create = "5m"  update = "5m"  }   aws_default_route_table
#
# The provider's Read never sources that block from the API - it only
# preserves whatever prior it was handed - so a stock state file is the only
# thing that ever held it. internal/live/projection's residue store is what
# holds such a value here, and it walked schema.Block.Attributes ONLY, with
# a doc comment stating nested blocks as a deliberate bound. So the block was
# never a candidate, never recorded, and every stateless replan proposed
# `+ timeouts {...}` on those two instances forever, while stock's plan on
# its own state renders the identical block "(1 unchanged block hidden)".
# HANDOFF's second row: the plans differ, so it is a defect.
#
# The bound's stated REASON is that the classifier compares one whole value
# before and after and a set- or map-nested block has no stable per-element
# form for that. That reason does not reach NestingSingle, which is one value
# in the implied object type exactly like a flat attribute - so the rule is
# now "NestingSingle blocks, nothing sensitive or write-only anywhere inside
# them", and the collection modes stay out with their reason intact. It names
# no type and no block: `timeouts` is simply the single-nested block
# hashicorp/aws puts on most of its resources. Asserted by value in
# internal/live/projection/residue_test.go's
# TestResidueCarriesASingleNestedBlockByValue, and asserted here as the
# absence of the proposal plus the presence of stock's own rendering.
WANT_TIMEOUTS_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_TIMEOUTS_N=1
  WANT_CIDX_N=1
  WANT_DEFAULT_N=1
  WANT_UNRESOLVABLE_N=1
  WANT_MODOUT_N=1
  WANT_CASCADE_N=1
  WANT_ERR_N=1
  WANT_RULE_ACTIONS=1
  WANT_SLOT_N=1
  WANT_CREATE_N=1
  WANT_REPLACE_N=1
  WANT_UPDATE_N=2
  log "  BREAK=1: expecting one of every refusal this estate no longer has"
  log "           (#304's count-index-in-tag, #305's unadmitted-type,"
  log "           #321+#324's Identity-not-resolvable, and the"
  log "           Module-output and Unable-to-compute sites the routing"
  log "           fix cleared), one"
  log "           aws_security_group_rule action, and every residue count"
  log "           off by one. None of these are real. This step must fail."
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

# The unit: the routing that made module.vpc.vpc_cidr_block unreachable.
MODOUT_N="$(grep -c '^Error: Module output not supported in static context$' <<< "$PLAN_OUT")"
CASCADE_N="$(grep -c '^Error: Unable to compute static value$' <<< "$PLAN_OUT")"
ERR_N="$(grep -c '^Error: ' <<< "$PLAN_OUT")"
[ "$MODOUT_N" = "$WANT_MODOUT_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_MODOUT_N 'Module output not supported in static context' sites, got $MODOUT_N"; }
[ "$CASCADE_N" = "$WANT_CASCADE_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_CASCADE_N 'Unable to compute static value' sites, got $CASCADE_N"; }
[ "$ERR_N" = "$WANT_ERR_N" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected $WANT_ERR_N refusals of ANY kind, got $ERR_N"; }
log "  the routing fix confirmed for real: 0 Module-output-not-supported"
log "  sites, 0 Unable-to-compute-static-value sites, and 0 refusals of any"
log "  kind at all. main.tf:224's cidr_blocks = module.vpc.vpc_cidr_block"
log "  is read now - as a deferred read of aws_vpc.this[0].cidr_block, off"
log "  the live object, not predicted from configuration."

# The rule the fix is about, asserted by BINDING rather than by an absent
# diagnostic: a resolved-but-wrong identity refuses nothing and shows up
# here as a create.
RULE_ACTIONS="$(grep -c 'aws_security_group_rule' <<< "$PLAN_ACTIONS")"
[ "$RULE_ACTIONS" = "$WANT_RULE_ACTIONS" ] || { printf '%s\n' "$PLAN_ACTIONS"; fail "expected $WANT_RULE_ACTIONS aws_security_group_rule actions in the plan - every ingress rule must bind to the live object its identity names, got $RULE_ACTIONS"; }

# ...and by VALUE, against the AWS CLI rather than against choudoufu's own
# report. The identity the fix renders is
# <the security group's id>_ingress_tcp_5432_5432_<the VPC's cidr_block>,
# and both halves are readable from the cloud independently. An identity
# that had taken lookup()'s own fallback, or the caller's from_port, or the
# module's name would not spell this - and the rule bound above, so this is
# the string choudoufu matched the live object on.
VPC_CIDR="$(awsl ec2 describe-vpcs --query "Vpcs[?Tags[?Key=='tofu-address' && Value=='module.vpc.aws_vpc.this:0']].CidrBlock | [0]" --output text)"
[ -n "$VPC_CIDR" ] && [ "$VPC_CIDR" != "None" ] || fail "could not read the VPC's cidr_block through the AWS CLI"
SG_ID="$(awsl ec2 describe-security-groups --query "SecurityGroups[?Tags[?Key=='tofu-address' && Value=='module.security_group.aws_security_group.this_name_prefix:0']].GroupId | [0]" --output text)"
[ -n "$SG_ID" ] && [ "$SG_ID" != "None" ] || fail "could not find the security group through the AWS CLI"
RULE_CIDR="$(awsl ec2 describe-security-groups --group-ids "$SG_ID" \
  --query "SecurityGroups[0].IpPermissions[?FromPort==\`5432\`].IpRanges[0].CidrIp | [0]" --output text)"
[ "$RULE_CIDR" = "$VPC_CIDR" ] || fail "the live ingress rule on $SG_ID carries CidrIp=$RULE_CIDR, but the VPC's cidr_block is $VPC_CIDR - the identity this fix renders is built from the second, so they have to agree"
log "  identity confirmed BY VALUE through the AWS CLI: $SG_ID's port-5432"
log "  ingress rule carries $RULE_CIDR, which is module.vpc's own"
log "  cidr_block, which is the last component of the identity"
log "  ${SG_ID}_ingress_tcp_5432_5432_${VPC_CIDR} that the rule bound on."

# The residue, counted by cause so that a change in WHICH resources need
# what is caught and not only the totals.
# Per INSTANCE, not per line: tofu-slot lands twice in every block that
# wants it, once under tags and once under tags_all.
SLOT_N="$(awk '
  /^  # / { if (hit) n++; hit=0; inblock=1; next }
  inblock && /^[[:space:]]*\+[[:space:]]+"tofu-slot"/ { hit=1 }
  END { if (hit) n++; print n+0 }
' <<< "$PLAN_OUT")"
CREATE_N="$(grep -c 'will be created$' <<< "$PLAN_ACTIONS")"
REPLACE_N="$(grep -c 'must be replaced$' <<< "$PLAN_ACTIONS")"
UPDATE_N="$(grep -c 'will be updated in-place$' <<< "$PLAN_ACTIONS")"
[ "$SLOT_N" = "$WANT_SLOT_N" ] || { printf '%s\n' "$PLAN_ACTIONS"; fail "expected $WANT_SLOT_N instances wanting the tofu-slot marker live-import does not write, got $SLOT_N"; }
[ "$CREATE_N" = "$WANT_CREATE_N" ] || { printf '%s\n' "$PLAN_ACTIONS"; fail "expected $WANT_CREATE_N create (the name_prefix parameter group), got $CREATE_N"; }
[ "$REPLACE_N" = "$WANT_REPLACE_N" ] || { printf '%s\n' "$PLAN_ACTIONS"; fail "expected $WANT_REPLACE_N replacements (floci does not echo back what the apply set), got $REPLACE_N"; }
[ "$UPDATE_N" = "$WANT_UPDATE_N" ] || { printf '%s\n' "$PLAN_ACTIONS"; fail "expected $WANT_UPDATE_N in-place updates, got $UPDATE_N"; }
grep -qE '^  # module\.db\.module\.db_parameter_group\.aws_db_parameter_group\.this\[0\] will be updated in-place$' <<< "$PLAN_ACTIONS" \
  || { printf '%s\n' "$PLAN_ACTIONS"; fail "the name_prefix parameter group must be an ordinary in-place update now, resolved by its own tofu-address marker rather than proposed as a create - see step 3f"; }

# ── 3e. the block-shaped residue, asserted both ways ───────────────────────
# The proposal must be gone, AND the block must still be there: an assertion
# that only checked for the absence of "+ timeouts" would also pass if the
# block had vanished from the plan altogether, which is a different and
# worse outcome.
TIMEOUTS_N="$(grep -cE '^[[:space:]]*\+ timeouts \{$' <<< "$PLAN_OUT")"
[ "$TIMEOUTS_N" = "$WANT_TIMEOUTS_N" ] || {
  grep -nE '^  # |^[[:space:]]*\+ timeouts \{$' <<< "$PLAN_OUT"
  fail "expected $WANT_TIMEOUTS_N '+ timeouts {' proposals - a config-only NestingSingle block the provider never reads back belongs in the residue record, not in every replan - got $TIMEOUTS_N"; }
# Since choudoufu #372 both of these objects are usually out of the plan
# ENTIRELY - their only remaining diff was the tofu-slot tag live-import now
# writes at migrate time - and an object with no plan body at all is a
# strictly stronger statement than one whose timeouts block renders
# unchanged. So the check is conditional on the object being in the plan,
# and the "did the block vanish from the record" worry the comment above
# names is covered by TIMEOUTS_N: a block that stopped being recorded comes
# back as "+ timeouts {", which is asserted to be 0 a few lines up.
for addr in 'module.security_group.aws_security_group.this_name_prefix[0]' \
            'module.vpc.aws_default_route_table.default[0]'; do
  # index() rather than a regex: these addresses carry [0], and an address
  # spliced into a dynamic awk regex would read it as a character class.
  BLOCK_PRESENT="$(awk -v a="  # $addr " 'index($0, a) == 1 { print "YES"; exit }' <<< "$PLAN_OUT")"
  if [ "$BLOCK_PRESENT" != "YES" ]; then
    log "  $addr is not in the plan at all - stronger than an unchanged-block render"
    continue
  fi
  BLOCK_HIT="$(awk -v a="  # $addr " '
    index($0, "  # ") == 1 { inblock = (index($0, a) == 1) }
    inblock && index($0, "# (1 unchanged block hidden)") > 0 { print "HIT"; exit }
  ' <<< "$PLAN_OUT")"
  [ "$BLOCK_HIT" = "HIT" ] \
    || { awk -v a="  # $addr " 'index($0,"  # ")==1 { inblock=(index($0,a)==1) } inblock' <<< "$PLAN_OUT"
         fail "$addr is in the plan and no longer renders its timeouts block as '(1 unchanged block hidden)' - which is exactly what stock's own plan renders for it, and the point of recording the block was to agree with that"; }
done
log "  the block-shaped residue is closed for real: 0 '+ timeouts {'"
log "  proposals, and both blocks that declare one render '(1 unchanged"
log "  block hidden)' - the identical line stock's plan renders. The rule is"
log "  NestingSingle blocks with nothing sensitive or write-only inside;"
log "  it names no type and no block name."

# ── 3f. the parameter group's marker, settled against floci's own API ──────
# This used to be a create: the previously recorded detail for this estate
# said floci implemented no tagging for the RDS 'pg' resource type at all
# and DescribeDBParameterGroups returned no DBParameterGroupArn to tag by,
# so marker discovery could never find a name_prefix object with no state
# to import it from. Re-verified here directly against floci's own API,
# with no tofu in the loop: that is no longer true. DescribeDBParameterGroups
# now returns a real DBParameterGroupArn, AddTagsToResource on it succeeds,
# and the tags migrate's -approve step wrote during stage 2 are already on
# the live object - HANDOFF's "a fixed wall makes stale scripts fail" again,
# just on the emulator's side of the wall rather than choudoufu's. Whether
# this was already true before 0a2f0291a0's third-image repin and simply
# went unverified, or the repin itself fixed it, does not change what is
# true now, which is what this asserts.
PG_NAME="$(awsl rds describe-db-parameter-groups \
  --query "DBParameterGroups[?starts_with(DBParameterGroupName, 'complete-postgresql')].DBParameterGroupName | [0]" --output text)"
[ -n "$PG_NAME" ] && [ "$PG_NAME" != "None" ] || fail "could not find the name_prefix parameter group through the AWS CLI"
PG_ARN_READ="$(awsl rds describe-db-parameter-groups --db-parameter-group-name "$PG_NAME" \
  --query 'DBParameterGroups[0].DBParameterGroupArn' --output text)"
[ -n "$PG_ARN_READ" ] && [ "$PG_ARN_READ" != "None" ] \
  || fail "floci returns no DBParameterGroupArn for $PG_NAME any more - re-measure whether the parameter group is a create again before trusting this section"
PG_ADDR_TAG="$(awsl rds list-tags-for-resource --resource-name "$PG_ARN_READ" \
  --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
WANT_PG_ADDR="module.db.module.db_parameter_group.aws_db_parameter_group.this:0"
[ "$PG_ADDR_TAG" = "$WANT_PG_ADDR" ] \
  || fail "the parameter group carries tofu-address=$PG_ADDR_TAG through the AWS CLI, not $WANT_PG_ADDR - live-import's -approve step should have stamped it in stage 2"
log "  the parameter group's marker is settled against floci's own API, not"
log "  inferred: DescribeDBParameterGroups now returns a real"
log "  DBParameterGroupArn for $PG_NAME, and the AWS CLI confirms it"
log "  already carries tofu-address=$PG_ADDR_TAG - the emulator gap the"
log "  prior recorded detail named for this instance is gone, and the plan"
log "  resolves it as an ordinary in-place update rather than a create."

# ── 3g. the remaining diffs, put to STOCK as the oracle (HANDOFF row 3) ────
# storage_encrypted forcing a replacement was the last recorded detail's
# blocker 3. Re-verified directly against floci's own API first: it now
# round-trips correctly, so there is nothing left for either binary to force
# a replacement over.
LIVE_ENCRYPTED="$(awsl rds describe-db-instances --db-instance-identifier complete-postgresql \
  --query 'DBInstances[0].StorageEncrypted' --output text)"
[ "$LIVE_ENCRYPTED" = "True" ] \
  || fail "floci returns StorageEncrypted=$LIVE_ENCRYPTED for complete-postgresql, not True - the round-trip gap this section records as fixed may have regressed"
log "  storage_encrypted round-trips correctly now (floci returns True,"
log "  matching config) - 0a2f0291a0's third-image repin fixed the gap that"
log "  used to force $WANT_REPLACE_N replacements here; neither binary"
log "  proposes one any more."
#
# What's left is the 8-attribute-plus-parameter-block set the header
# documents (lex00/floci#120), plus a check that the one choudoufu-only
# residue defect (INTENTIUS/choudoufu#393) really has gone from BOTH
# plans now that it is fixed. Both get an independent oracle here: the plain
# estate from stage 1 still has its own terraform.tfstate and its own
# .terraform, so stock can be asked the identical question against the
# identical cloud, with real state instead of a stateless replan. Tag noise
# is expected in stock's plan and deliberately not asserted on: stage 2
# stamped markers onto objects stock's state does not know about, so stock
# proposes removing them.
log "=== 3g. the same question put to stock terraform (HANDOFF row 3) ==="
STOCK_PLAN_OUT="$(cd "$PLAIN_EST" && terraform plan -input=false -no-color 2>&1)"
STOCK_PLAN_RC=$?
[ "$STOCK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_PLAN_OUT" | tail -40; fail "stock terraform plan against its own state exited $STOCK_PLAN_RC - the oracle has to run for this stage to record what it records"; }
STOCK_ACTIONS="$(grep -E '^  # ' <<< "$STOCK_PLAN_OUT")"
STOCK_REPLACE_N="$(grep -c 'must be replaced$' <<< "$STOCK_ACTIONS")"
[ "$STOCK_REPLACE_N" = "0" ] \
  || { printf '%s\n' "$STOCK_ACTIONS"; fail "stock proposes $STOCK_REPLACE_N replacements against its OWN state file - storage_encrypted's round-trip fix was expected to remove every replacement on both sides, not just ours"; }
# The 8 floci#120 attributes: same addresses, same attribute names, on
# BOTH binaries - proof this is the emulator's gap and not a plan defect of
# ours (stock never goes through choudoufu's projection at all).
for attr in 'backup_window' 'port' 'enabled_cloudwatch_logs_exports' 'engine_lifecycle_support' \
            'max_allocated_storage' 'monitoring_interval' 'monitoring_role_arn' 'performance_insights_retention_period'; do
  grep -qE "^ *[+~] +${attr}( *=| \{)" <<< "$STOCK_PLAN_OUT" \
    || { printf '%s\n' "$STOCK_PLAN_OUT" | grep -E "^  # |$attr"; fail "stock's own plan no longer shows $attr changing - lex00/floci#120 may have been fixed; re-measure this section rather than trusting a stale attribute list"; }
  grep -qE "^ *[+~] +${attr}( *=| \{)" <<< "$PLAN_OUT" \
    || { printf '%s\n' "$PLAN_OUT" | grep -E "^  # |$attr"; fail "choudoufu no longer shows $attr changing, but stock still does - that would make this ours to fix, not the emulator's"; }
done
grep -qE '^ *\+ parameter \{$' <<< "$STOCK_PLAN_OUT" \
  || fail "stock's own plan no longer proposes adding a parameter block - lex00/floci#120's apply_method gap may have been fixed"
log "  stock terraform, on its OWN state file and the same cloud, shows the"
log "  identical 8 attributes changing on the identical two aws_db_instance"
log "  addresses, plus the identical parameter-block churn on"
log "  aws_db_parameter_group - all traced to lex00/floci#120's round-trip"
log "  gaps, confirmed on an independent binary that never goes through"
log "  choudoufu's own projection. HANDOFF's third row: stock fails too."
#
# INTENTIUS/choudoufu#393, both ways now that it is fixed.
#
# skip_final_snapshot must NOT appear in stock's plan for db_default,
# because stock's real state already holds the true applied value and
# never goes through an import stub at all. If it ever does, #393's
# "choudoufu-only" framing is wrong and this section needs to be re-read
# before anything else in it is trusted.
grep -qF 'skip_final_snapshot' <<< "$STOCK_PLAN_OUT" \
  && { printf '%s\n' "$STOCK_PLAN_OUT" | grep -B5 'skip_final_snapshot'; fail "stock's own plan shows skip_final_snapshot changing - INTENTIUS/choudoufu#393 was filed on the premise that this is choudoufu-only; re-read that issue before trusting it"; }
# And it must NOT appear in choudoufu's OWN plan either, now that
# fillResidue distinguishes an import stub's unconfirmed SDK default from a
# value ReadResource actually produced (residue.go's importStub provenance
# check). Before that fix this line failed every run: the stub's
# `skip_final_snapshot = true` outranked the correctly recorded `false` and
# the plan proposed the same update forever, absent from stock the whole
# time (proven above), so it was never drift and never converged.
grep -qF 'skip_final_snapshot' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -B5 'skip_final_snapshot'; fail "choudoufu's plan still shows skip_final_snapshot changing on module.db_default's aws_db_instance - INTENTIUS/choudoufu#393 has regressed"; }
log "  skip_final_snapshot does not appear in EITHER plan any more, on"
log "  either instance - INTENTIUS/choudoufu#393 is FIXED: fillResidue now"
log "  tells an import stub's unconfirmed SDKv2 schema default apart from a"
log "  value ReadResource actually produced, so the correctly recorded"
log "  residue (false) is no longer outranked by the stub's own default"
log "  (true) on module.db_default's aws_db_instance."

log ""
log "STAGE 3 (test_plan): the identity layer is CLEAR for real - 0 refusals"
log "of any kind, where this estate stood at 7, then 33, then 14, then 2."
log "The plan is not empty, and nothing left in it is identity. The slot,"
log "create and replace walls this estate stood on are now gone too:"
log "  $SLOT_N instances want the tofu-slot marker (was 3, was 22) -"
log "     a7073177ed closed #372's own client-named-type remainder"
log "  $CREATE_N create (was 1, the name_prefix parameter group) - the same"
log "     fix settles its slot at migrate time, and floci's RDS 'pg' tagging"
log "     works now too (re-verified at step 3f), so it resolves by its"
log "     own marker like any other instance"
log "  $REPLACE_N replacements (was 2) - 0a2f0291a0's third-image repin"
log "     fixed floci's StorageEncrypted round-trip (asserted at step 3g)"
log "  0 '+ timeouts {' proposals, was 2 - the block-shaped residue gap this"
log "     crossing found, fixed generically for NestingSingle blocks"
log "What's left is $UPDATE_N in-place updates, on the same three addresses"
log "as before but for one reason now instead of two: lex00/floci#120 (8"
log "aws_db_instance/aws_db_parameter_group arguments floci's Describe"
log "calls never echo back, confirmed against AWS's own documented API"
log "shapes and reproduced identically on stock terraform's own plan at"
log "step 3g - HANDOFF's third row, the estate still has to clear once the"
log "emulator does). INTENTIUS/choudoufu#393 (skip_final_snapshot's phantom"
log "true -> false update on module.db_default) is FIXED: fillResidue can"
log "now tell an import stub's unconfirmed SDKv2 default apart from a"
log "value ReadResource actually produced."
log ""
gauntlet_stage test_plan fail "identity CLEAR for real: 0 refusals of any kind (was 7, then 33, then 14, then 2). The block-shaped residue gap this crossing found is FIXED: internal/live/projection's residue filter walked schema.Block.Attributes only, so a config-only NestingSingle block the provider never reads back (terraform-aws-modules' timeouts{}) was never recorded and every replan proposed adding it - '+ timeouts {' 2 -> 0 here, and both blocks now render stock's own '(1 unchanged block hidden)'. The three walls this estate stood on before this crossing are ALL gone: $SLOT_N instances missing tofu-slot (was 3, was 22 - a7073177ed, 2026-08-22, closed #372's client-named-type remainder), $CREATE_N create (was 1 - the same fix settles the name_prefix parameter group's slot at migrate time, and floci's RDS 'pg' tagging now works too, re-verified against floci's own API at step 3f rather than trusted from the prior recorded detail), $REPLACE_N replacements (was 2 - 0a2f0291a0, 2026-08-22, fixed floci's StorageEncrypted round-trip, asserted live at step 3g). INTENTIUS/choudoufu#393 is also FIXED (module.db_default's aws_db_instance no longer proposes skip_final_snapshot=true->false: fillResidue now distinguishes an import stub's unconfirmed SDKv2 schema default from a value ReadResource actually produced, asserted against both binaries' own plans at step 3g). What is left is $UPDATE_N in-place updates on the same three addresses, now for a single reason: lex00/floci#120 (floci's DescribeDBInstances/DescribeDBParameters never echo back port, backup_window, monitoring_interval, monitoring_role_arn, performance_insights_retention_period, engine_lifecycle_support, enabled_cloudwatch_logs_exports, max_allocated_storage, or a parameter block's apply_method, all eight confirmed as real documented AWS API fields against botocore's own service model and reproduced identically on stock terraform's own plan against its own real state file at step 3g - HANDOFF's third row, the estate still has to clear once the emulator does)"
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
gauntlet_stage test_apply not_run "depends on stage 3, which does not produce a clean plan"
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="
gauntlet_stage drift_reconverge not_run "depends on stages 3-4"
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          identity CLEAR (0 refusals, was 7, then 33, then 14, then 2); slot/create/replace walls and choudoufu#393 all fixed - blocked instead on $UPDATE_N in-place updates, floci#120's round-trip gaps alone (see header)"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "39 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
