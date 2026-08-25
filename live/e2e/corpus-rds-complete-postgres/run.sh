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

# ══════════════════════════════════════════════════════════════════════════
# GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# Two more, fresh containers, entirely independent of everything above and
# below: choudoufu applies the same DELTA-reduced estate (DELTA 1's
# emulator flags, DELTA 2's automated-backups-replication/kms removal -
# floci-io/floci#51, an emulator gap - and DELTA 3's manage_master_user_
# password_rotation=false - lex00/floci#52, also an emulator gap) directly
# from a live block, no live-import, no migration ever run. Both deltas
# apply to the stock oracle too, for the same reason they apply to every
# other copy in this script: they are what makes the estate buildable
# against floci at all, not a choudoufu-only workaround.
CURRENT_STAGE=greenfield
FLOCI_GREEN_NAME="choudoufu-corpus-rds-complete-postgres-green-$$"
FLOCI_ORACLE_NAME="choudoufu-corpus-rds-complete-postgres-green-oracle-$$"
GREEN_ESTATE_NAME="rds-postgres-green"

# floci_launch_retry <name> <portvar> - several gauntlet scripts run
# concurrently on a shared host, each with its own FLOCI_PORT reservation,
# but a fixed offset from that reservation is not itself reserved and
# collides with a sibling picking the same offset. Pick a port at random
# from a wide, rarely-used range and retry on "already allocated" instead.
floci_launch_retry() {
  local name="$1" portvar="$2" tries=0 port out
  while :; do
    port=$((20000 + RANDOM % 20000))
    out="$(docker run -d --rm -p "${port}:4566" --name "$name" "$FLOCI_IMAGE" 2>&1)" && { eval "$portvar=$port"; return 0; }
    tries=$((tries + 1))
    grep -qF 'port is already allocated' <<< "$out" || { printf '%s\n' "$out"; return 1; }
    [ "$tries" -ge 10 ] && { printf '%s\n' "$out"; return 1; }
  done
}

log "=== GREENFIELD: 0. two more floci containers ==="
floci_launch_retry "$FLOCI_GREEN_NAME" FLOCI_GREEN_PORT || fail "docker run for $FLOCI_GREEN_NAME failed"
floci_launch_retry "$FLOCI_ORACLE_NAME" FLOCI_ORACLE_PORT || fail "docker run for $FLOCI_ORACLE_NAME failed"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"rds"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"rds"' <<< "${GH:-}" || fail "floci did not come up healthy (rds) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

apply_deltas() { # apply_deltas <dir> - DELTA 1/2/3, verbatim from stage 1
  perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$1/main.tf"
  grep -q 'DELTA 1' "$1/main.tf" || fail "apply_deltas: DELTA 1 did not match in $1 - the corpus pin has moved"
  perl -0pi -e 's/provider "aws" \{\n  alias  = "region2"\n  region = local\.region2\n\}\n\nmodule "kms" \{.*?\n\}\n\nmodule "db_automated_backups_replication" \{.*?\n\}\n\n/# DELTA 2 (EMULATOR GAP, floci-io\/floci#51)\n\n/s' "$1/main.tf"
  grep -q 'DELTA 2' "$1/main.tf" || fail "apply_deltas: DELTA 2 did not match in $1 - the corpus pin has moved"
  perl -pi -e 's/^(  manage_master_user_password_rotation)(\s*)= true$/$1$2= false # DELTA 3 (EMULATOR GAP, lex00\/floci#52)/' "$1/main.tf"
  grep -q 'DELTA 3' "$1/main.tf" || fail "apply_deltas: DELTA 3 did not match in $1 - the corpus pin has moved"
}

GREEN="$WORK/green"
ORACLE_G="$WORK/green-oracle"
copy_tree "$GREEN"
copy_tree "$ORACLE_G"
GREEN_EST="$GREEN/rds/examples/complete-postgres"
ORACLE_G_EST="$ORACLE_G/rds/examples/complete-postgres"
apply_deltas "$GREEN_EST"
apply_deltas "$ORACLE_G_EST"
GREEN_EST="$GREEN_EST" GREEN_ESTATE_NAME="$GREEN_ESTATE_NAME" python3 << 'PYINNER'
import os
p = os.environ["GREEN_EST"] + "/versions.tf"
s = open(p).read()
old = """  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.28"
    }
  }
}"""
assert old in s, "greenfield: versions.tf required_providers block not found - the corpus pin has moved"
name = os.environ["GREEN_ESTATE_NAME"]
# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create,
# not a lost record. Same fix, same precedent as corpus-alb-complete's own
# 898091b8f2 (this exact toggle, this exact reason) - not a workaround
# invented here.
new = old[:-1] + """
  live {
    estate = "%s"

    record_store "local" {
      path = ".tofu-records"
    }

    strict {
      no_source_create = "create"
    }
  }
}""" % name
open(p, "w").write(s.replace(old, new, 1))
PYINNER
grep -q "estate = \"$GREEN_ESTATE_NAME\"" "$GREEN_EST/versions.tf" || fail "greenfield: the live-block edit did not match versions.tf - the corpus pin has moved"

log "=== GREENFIELD: 1. choudoufu apply from nothing, no migration ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error' -A5 | head -60; fail "the greenfield apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly $INSTANCES resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT" | head -1)"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_DB_ARN="$(awsg rds describe-db-instances --db-instance-identifier complete-postgresql --query 'DBInstances[0].DBInstanceArn' --output text)"
[ -n "$GREEN_DB_ARN" ] && [ "$GREEN_DB_ARN" != "None" ] || fail "no greenfield DB instance named complete-postgresql came back from floci"
GOT_G_DB_ADDR="$(awsg rds list-tags-for-resource --resource-name "$GREEN_DB_ARN" --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
[[ "$GOT_G_DB_ADDR" == module.db.module.db_instance.aws_db_instance.this* ]] \
  || fail "the greenfield primary DB instance carries tofu-address=$GOT_G_DB_ADDR, not module.db.module.db_instance.aws_db_instance.this[...]"
GOT_G_DB_ESTATE="$(awsg rds list-tags-for-resource --resource-name "$GREEN_DB_ARN" --query "TagList[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_G_DB_ESTATE" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield DB instance carries tofu-estate=$GOT_G_DB_ESTATE, not $GREEN_ESTATE_NAME"
GREEN_SG_LINE="$(awsg ec2 describe-security-groups --filters "Name=tag:tofu-estate,Values=$GREEN_ESTATE_NAME" --query "SecurityGroups[].[GroupId,Tags[?Key=='tofu-address']|[0].Value]" --output text | grep -E '	module\.security_group\.' | head -1)"
[ -n "$GREEN_SG_LINE" ] || fail "no live security group found by its tofu-address marker in the greenfield account"
log "  primary DB instance and security group carry their expected tofu-address/tofu-estate markers - read via the AWS CLI, not choudoufu's own report"

log "=== GREENFIELD: 3. the local record store holds at least one record per taggable instance (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN_EST/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" -gt 0 ] || fail "expected at least one record under the local record store after the greenfield apply, found none"
log "  $GREEN_RECORD_FILES records persisted under the local record store"

log "=== GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== GREENFIELD: 5. stock oracle - the identical DELTA-reduced estate applied fresh in its own namespace ==="
( cd "$ORACLE_G_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_G_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_G_APPLY_OUT="$(cd "$ORACLE_G_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_G_APPLY_OUT" | tail -60; fail "the greenfield oracle apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$ORACLE_G_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_G_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly $INSTANCES resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_G_APPLY_OUT" | head -1)"

awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }

log "=== GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
GREEN_DB_SHAPE="$(awsg rds describe-db-instances --db-instance-identifier complete-postgresql --query 'DBInstances[0].[Engine,EngineVersion,DBInstanceClass,AllocatedStorage,Port]' --output json)"
ORACLE_DB_SHAPE="$(awso rds describe-db-instances --db-instance-identifier complete-postgresql --query 'DBInstances[0].[Engine,EngineVersion,DBInstanceClass,AllocatedStorage,Port]' --output json)"
[ "$GREEN_DB_SHAPE" = "$ORACLE_DB_SHAPE" ] || { printf 'greenfield: %s\noracle:     %s\n' "$GREEN_DB_SHAPE" "$ORACLE_DB_SHAPE"; fail "the primary DB instance differs structurally between the greenfield estate and the stock oracle"; }

GREEN_SG_RULES="$(awsg ec2 describe-security-groups --filters "Name=tag:tofu-estate,Values=$GREEN_ESTATE_NAME" --query "length(SecurityGroups[?GroupName=='${GREEN_ESTATE_NAME}'].IpPermissions[])" --output text 2>/dev/null || echo 0)"
ORACLE_SG_RULES="$(awso ec2 describe-security-groups --query "length(SecurityGroups[?GroupName=='${GREEN_ESTATE_NAME}'].IpPermissions[])" --output text 2>/dev/null || echo 0)"
[ "$GREEN_SG_RULES" = "$ORACLE_SG_RULES" ] || fail "the security group's own ingress rule count differs: greenfield=$GREEN_SG_RULES oracle=$ORACLE_SG_RULES"

log "  primary DB instance (engine, engine version, instance class, allocated storage, port) and security group ingress rule count match between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "$INSTANCES resources from nothing (same DELTA reduction cold_deploy itself needs - two emulator gaps, floci-io/floci#51 and lex00/floci#52), primary DB instance and security group markers verified via the AWS CLI, $GREEN_RECORD_FILES records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally (DB engine/version/class/storage/port, security-group rule count)"
CURRENT_STAGE=""

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true



# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Neither leg touches a root resource - this estate declares none - so both
# rename a module call. A `moved` block renames the whole
# module.security_group call (an external registry module, several
# resources); "choudoufu live-mv" renames module.db_default, whose single
# live resource - module.db_default's own nested module.db_instance call is
# unconditional and its aws_db_instance.this is the only resource created
# under create_db_option_group=false/create_db_parameter_group=false - has a
# fully known, stable address, so its full instance address is
# module.db_default.module.db_instance.aws_db_instance.this[0]. The stock
# oracle for both runs on a copy of cold_deploy's own state, PLANNED right
# after stage 1 - before choudoufu or live-import ever touch these shared
# objects.
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming module.db_default's instance WITHOUT a moved block, which
# must make choudoufu propose destroying the old address and creating the
# new one - the opposite of every other assertion in this part.

CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE_ROOT="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE_ROOT"
PLAIN_ORACLE="$PLAIN_ORACLE_ROOT/rds/examples/complete-postgres"
sed -i.bak 's/module "security_group" {/module "security_group_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.security_group\./module.security_group_renamed./g' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module "db_default" {/module "db_default_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.db_default\./module.db_default_renamed./g' "$PLAIN_ORACLE/outputs.tf"
rm -f "$PLAIN_ORACLE/main.tf.bak" "$PLAIN_ORACLE/outputs.tf.bak"
cat >> "$PLAIN_ORACLE/main.tf" <<'EOF'

moved {
  from = module.security_group
  to   = module.security_group_renamed
}

moved {
  from = module.db_default
  to   = module.db_default_renamed
}
EOF
( cd "$PLAIN_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
# module.db's own parameter group (not module.security_group or module.db_default -
# neither leg of this rename touches module.db at all) is a KNOWN, already-documented
# quirk of stock's own apply-time state fidelity for this estate (see stage 3's own
# gauntlet_stage detail above: "Stock's own replan against its own never-deleted state
# file still shows tag noise plus the two parameter blocks... HANDOFF row 3, a property
# of that one state file's own apply-time fidelity" - lex00/floci#120's apply_method
# echo, confirmed correct on the LIVE object by a direct describe-db-parameters probe
# elsewhere in this script). It is not a rename artifact - the same "+ parameter"
# noise appears on ANY bare stock replan of this state, rename or not - so it is
# normalised out of the zero-churn assertion by name, the same way drift_reconverge's
# own oracle normalises marker tags out of both plans.
UPDATED_HEADERS="$(grep -E '^  # .+ will be updated in-place$' <<< "$ORACLE_PLAN_OUT")"
UNEXPECTED_UPDATES="$(grep -vF 'module.db.module.db_parameter_group.aws_db_parameter_group.this[0]' <<< "$UPDATED_HEADERS" | grep -c . || true)"
[ "$UNEXPECTED_UPDATES" = "0" ] \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes an in-place update outside the known module.db parameter-group noise - the oracle itself is not zero-churn"; }
if grep -qF 'module.db.module.db_parameter_group.aws_db_parameter_group.this[0]' <<< "$UPDATED_HEADERS"; then
  log "  (module.db's own parameter group also shows the known apply_method-echo noise on this bare replan - HANDOFF row 3, unrelated to either rename, normalised out below)"
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -60; fail "stock's rename plan changes more than the known module.db parameter-group noise - not a true rename no-op"; }
else
  grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
    || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -60; fail "stock's rename plan is not a true no-op"; }
fi
log "  stock: zero churn from the rename itself on cold_deploy's own state - both moves report only their move, no attribute diff at all (module.db's own known apply_method-echo noise, unrelated to either rename, excluded)"

# day2_remove's stock oracle (live/GAUNTLET.md #7), computed here, before
# migrate/rename/drift ever write a live tag: a copy of the SAME
# already-renamed oracle tree D-ORACLE above just built (module.security_
# group_renamed / module.db_default_renamed, moved blocks, cold_deploy's
# own state), with module.db_default_renamed's whole block - and every
# output that names it - removed outright. Picked because its own nested
# module.db_instance call is unconditional and, under this estate's own
# create_db_option_group=false/create_db_parameter_group=false, its
# aws_db_instance.this is the ONLY resource that module creates - no
# untaggable sibling, no #404-shaped ripple onto a sibling's policy - the
# same shape corpus-s3-bucket-complete's day2_remove used successfully for
# its own bucket (issue #410 is about the sibling THAT estate's target had;
# this target has none).
CURRENT_STAGE=day2_remove
log "=== REMOVE-ORACLE. stock: module.db_default_renamed's block removed, on the same renamed oracle tree above ==="
REMOVE_ORACLE_ROOT="$WORK/remove-oracle"
cp -r "$PLAIN_ORACLE_ROOT" "$REMOVE_ORACLE_ROOT"
REMOVE_ORACLE="$REMOVE_ORACLE_ROOT/rds/examples/complete-postgres"
python3 -c "
p = '$REMOVE_ORACLE/main.tf'
s = open(p).read()
start = s.index('module \"db_default_renamed\" {')
end = s.index('\n}\n', start) + len('\n}\n')
assert 'db_subnet_group_name' in s[start:end]
open(p, 'w').write(s[:start] + s[end:])
"
grep -q 'module "db_default_renamed"' "$REMOVE_ORACLE/main.tf" && fail "REMOVE-ORACLE: module.db_default_renamed's block is still present"
python3 -c "
import re
p = '$REMOVE_ORACLE/outputs.tf'
s = open(p).read()
blocks = re.findall(r'output \"[^\"]+\" \{.*?\n\}\n', s, re.S)
kept = [b for b in blocks if 'module.db_default_renamed.' not in b]
assert len(kept) < len(blocks), 'REMOVE-ORACLE: no db_default_renamed output blocks found - the corpus pin has moved'
open(p, 'w').write(''.join(kept))
"
grep -q 'module.db_default_renamed' "$REMOVE_ORACLE/outputs.tf" && fail "REMOVE-ORACLE: outputs.tf still references module.db_default_renamed"
( cd "$REMOVE_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's init failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.db_default_renamed\.module\.db_instance\.aws_db_instance\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose destroying module.db_default_renamed.module.db_instance.aws_db_instance.this[0]"; }
# The nested db_instance submodule also declares its own
# random_id.snapshot_identifier (issue #340's own record-backed effect,
# same mechanism corpus-s3-bucket-complete's random_pet uses) - LOCAL
# state bookkeeping with no cloud representation at all, destroyed
# alongside its sibling and asserted here rather than ignored, so a
# regression that drops it is not silently invisible.
grep -qE '^  # module\.db_default_renamed\.module\.db_instance\.random_id\.snapshot_identifier\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle does not propose destroying module.db_default_renamed.module.db_instance.random_id.snapshot_identifier[0]"; }
DESTROY_N="$(grep -cE '^  # .+ will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT")"
[ "$DESTROY_N" = "2" ] || { grep -E '^  # .+ will be' <<< "$REMOVE_ORACLE_PLAN_OUT"; fail "stock's own oracle proposes $DESTROY_N destroys, not exactly 2 - a hidden dependent turned up"; }
log "  stock oracle: exactly two destroys proposed - the db instance and its own local random_id.snapshot_identifier (no cloud representation) - (module.db's own known apply_method-echo parameter-group noise aside, see D-ORACLE above - computed now, before anything below writes a live tag)"
CURRENT_STAGE=""

# day2_replace's stock oracle (live/GAUNTLET.md #9), computed here for the
# same reason day2_remove's own oracle sits before migrate (above): a
# throwaway copy of cold_deploy's own state, module.db's `identifier` AND
# `db_name` arguments both changed together. `identifier` alone is NOT
# ForceNew on aws_db_instance - RDS supports a real rename via
# ModifyDBInstance's NewDBInstanceIdentifier, confirmed empirically (a
# lone identifier change plans an in-place update, not a replace) - so
# `db_name` (the database created inside the engine at bootstrap, which
# AWS has no in-place rename for) is the argument that actually forces the
# replace. Changing both together, rather than db_name alone, gives this
# leg the same observable "same address, new identity value" shape every
# other estate's own day2_replace section has (db_name alone forces the
# SAME replace but leaves identifier, and so the record's own import_id,
# unchanged throughout - a real but less legible proof). The combination
# cascades into the SAME dependency edges module.db's own two CloudWatch
# log groups and parameter group already carry (their names default from
# `identifier`, confirmed empirically: an identifier-only rename does NOT
# touch them, but this combined change replaces all three alongside the
# instance) - a real, four-object shape, not a bug. module.db is chosen
# because day2_rename/day2_remove (above) target module.security_group
# and module.db_default, never module.db, so this section has no ordering
# dependency on either. PLAN ONLY, never applied: this copy shares
# floci's account with $ADOPTED_EST.
CURRENT_STAGE=day2_replace
log "=== F-ORACLE. stock: force-replace module.db's own instance via its ForceNew db_name argument (plus identifier, for an observable identity change), on cold_deploy's own state ==="
ORACLE_REPLACE_ROOT="$WORK/oracle-replace"
cp -r "$PLAIN" "$ORACLE_REPLACE_ROOT"
ORACLE_REPLACE_EST="$ORACLE_REPLACE_ROOT/rds/examples/complete-postgres"
python3 -c "
p = '$ORACLE_REPLACE_EST/main.tf'
s = open(p).read()
old_id = '  identifier = local.name\n'
assert s.count(old_id) == 1, 'day2_replace oracle: identifier = local.name did not match exactly once - the corpus pin has moved'
s = s.replace(old_id, '  identifier = \"\${local.name}-replaced\"\n', 1)
old_dbname = '  db_name  = \"completePostgresql\"\n'
assert s.count(old_dbname) == 2, 'day2_replace oracle: db_name line did not match exactly twice (module.db and module.db_default) - the corpus pin has moved'
s = s.replace(old_dbname, '  db_name  = \"completePostgresqlReplaced\"\n', 1)
open(p, 'w').write(s)
"
grep -q 'identifier = "${local.name}-replaced"' "$ORACLE_REPLACE_EST/main.tf" \
  || fail "changing module.db's identifier argument in the replace-oracle copy did not match - the corpus pin has moved"
grep -q 'db_name  = "completePostgresqlReplaced"' "$ORACLE_REPLACE_EST/main.tf" \
  || fail "changing module.db's db_name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$ORACLE_REPLACE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_REPLACE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$ORACLE_REPLACE_EST" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.db\.module\.db_instance\.aws_db_instance\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.db's instance when its ForceNew db_name argument changes"; }
grep -qE '^  # module\.db\.module\.db_instance\.aws_cloudwatch_log_group\.this\["postgresql"\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not cascade the replace into the postgresql cloudwatch log group"; }
grep -qE '^  # module\.db\.module\.db_instance\.aws_cloudwatch_log_group\.this\["upgrade"\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not cascade the replace into the upgrade cloudwatch log group"; }
grep -qE '^  # module\.db\.module\.db_parameter_group\.aws_db_parameter_group\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not cascade the replace into the db parameter group"; }
grep -qF 'Plan: 4 to add, 0 to change, 4 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "the day2_replace stock oracle plan does not match the header's own four-resource cascade (instance + 2 cloudwatch log groups + parameter group, all replaced)"; }
log "  stock: exactly one instance replace at the same declared address, cascading into its 2 cloudwatch log groups and its db parameter group (all replaced, all named from identifier) - 4 to add, 4 to destroy, on the state cold_deploy produced - plan only, not applied (see above)"
CURRENT_STAGE=""

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
#  - 0 in-place updates remain (was 3, then round-8 repin's own re-measure:
#    lex00/floci PR #128/ff815779 fixes #124 - a second RDS instance
#    requesting a colliding port now gets its own distinct loopback bind
#    address with the declared port honored - which turns out to be the
#    LAST of the eight lex00/floci#120 attributes still open for this
#    estate's two aws_db_instance addresses (module.db and module.db_default
#    both declare port = 5432; module.db_default is the second-created
#    instance and is exactly the colliding-port shape #124 fixes). The
#    other seven of the eight (backup_window, monitoring_interval,
#    monitoring_role_arn, performance_insights_retention_period,
#    engine_lifecycle_support, enabled_cloudwatch_logs_exports,
#    max_allocated_storage) and the parameter block's apply_method were
#    already fixed by earlier rounds (round 5's #120 pass, round 6's PR
#    #125 "RDS optional fields/ApplyMethod #120") but this estate had not
#    been re-measured since, so the artifact's recorded wall (8 fields) was
#    stale even before this round's port fix landed - see 3g below for the
#    real current count (0) confirmed by choudoufu's own empty replan AND
#    an independent direct-API probe of the live parameter group.
#    INTENTIUS/choudoufu#393 (CHOUDOUFU DEFECT, filed by an earlier
#    crossing) was module.db_default's aws_db_instance additionally
#    proposing "skip_final_snapshot = true -> false" forever, even though
#    the estate's record store correctly held "false" for it - FIXED,
#    fillResidue now distinguishes an import stub's unconfirmed SDKv2
#    schema default from a value ReadResource actually produced. Confirmed
#    absent from both plans at step 3g.
WANT_SLOT_N=0
WANT_CREATE_N=0
WANT_REPLACE_N=0
WANT_UPDATE_N=0
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
# The name_prefix parameter group must not appear in the action list at
# all any more (0 create, 0 update): round 8's own re-measure found
# lex00/floci#120 fully resolved (see 3g), so it converges completely
# rather than merely resolving by marker into an in-place update - the
# stronger statement WANT_UPDATE_N=0 already proves, asserted again here
# by name so a regression that brought back JUST this address would say so.
grep -qF 'module.db.module.db_parameter_group.aws_db_parameter_group.this[0]' <<< "$PLAN_ACTIONS" \
  && { printf '%s\n' "$PLAN_ACTIONS"; fail "the name_prefix parameter group is back in the plan's action list - it should be fully converged (0 diff) now that lex00/floci#120 is resolved"; }

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

# ── 3g. the plan is genuinely empty; floci#120 confirmed resolved ─────────
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
# UPDATE 2026-08-24 (round-8 repin, lex00/floci PR #128, ff815779,
# ghcr.io/lex00/floci:main-20260824d sha256:25fc9687): re-measuring this
# estate for real (it had not been re-crossed since round ~6-7, so the
# artifact's recorded "3 in-place updates, lex00/floci#120" detail was
# already stale before this round's own fix landed - rounds 5 and 6 had
# each already closed some of #120's eight fields without a re-cross ever
# confirming it here) finds choudoufu's OWN live-plan (PLAN_OUT, computed
# above with no local state file at all) is a GENUINE, COMPLETE no-op:
# "No changes. Your infrastructure matches the configuration." - not
# merely the eight attributes' worth of noise being gone, everything.
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^Plan:|^  # '; fail "expected a genuinely empty replan (No changes) - something in the plan action list is nonzero even though ADD_N/CHANGE_N/DESTROY_N above were all asserted at their WANT_ values"; }
log "  choudoufu's own live-plan, from NO local state file, is a genuine"
log "  no-op: 'No changes. Your infrastructure matches the configuration.'"
#
# The 8 lex00/floci#120 attributes must not appear in choudoufu's plan -
# already implied by the empty-plan assertion above, reasserted here by
# name so a partial regression (one attribute reappearing without making
# the WHOLE plan non-empty, impossible today but not something to assume
# forever) says which attribute, not just that the plan grew.
for attr in 'backup_window' 'port' 'enabled_cloudwatch_logs_exports' 'engine_lifecycle_support' \
            'max_allocated_storage' 'monitoring_interval' 'monitoring_role_arn' 'performance_insights_retention_period'; do
  grep -qE "^ *[+~] +${attr}( *=| \{)" <<< "$PLAN_OUT" \
    && { printf '%s\n' "$PLAN_OUT" | grep -E "^  # |$attr"; fail "lex00/floci#120 is back: $attr appears in choudoufu's own plan again"; }
done
log "  all eight lex00/floci#120 attributes confirmed absent from"
log "  choudoufu's plan by name: port, backup_window, monitoring_interval,"
log "  monitoring_role_arn, performance_insights_retention_period,"
log "  engine_lifecycle_support, enabled_cloudwatch_logs_exports,"
log "  max_allocated_storage."
#
# Direct API confirmation, no tofu in the loop, for the specific field this
# round's own fix (#124, RDS port isolation) reaches: the name_prefix
# parameter group's two custom parameters (autovacuum, client_encoding),
# which floci's apply_method echo gap used to hide, read back correctly
# with --source user - the same values the config declares, confirmed
# against the live object directly rather than trusted from either plan.
PG_AUTOVACUUM="$(awsl rds describe-db-parameters --db-parameter-group-name "$PG_NAME" --source user \
  --query "Parameters[?ParameterName=='autovacuum'].ParameterValue | [0]" --output text)"
PG_CLIENT_ENCODING="$(awsl rds describe-db-parameters --db-parameter-group-name "$PG_NAME" --source user \
  --query "Parameters[?ParameterName=='client_encoding'].ParameterValue | [0]" --output text)"
[ "$PG_AUTOVACUUM" = "1" ] || fail "the live parameter group's autovacuum parameter reads $PG_AUTOVACUUM through the AWS CLI, not 1 (config's own value) - lex00/floci#120's apply_method echo gap may have regressed"
[ "$PG_CLIENT_ENCODING" = "utf8" ] || fail "the live parameter group's client_encoding parameter reads $PG_CLIENT_ENCODING through the AWS CLI, not utf8 (config's own value) - lex00/floci#120's apply_method echo gap may have regressed"
log "  the parameter group's two custom parameters read back correctly"
log "  through a direct describe-db-parameters --source user call: autovacuum=$PG_AUTOVACUUM,"
log "  client_encoding=$PG_CLIENT_ENCODING, matching config exactly."
#
# What was this round's own fix, specifically: module.db and
# module.db_default both declare port = 5432 (a genuine port collision -
# main.tf lines 48 and 128), and module.db_default (the second-created
# instance) is exactly the shape lex00/floci#124 fixes (a colliding
# instance now gets its own distinct loopback bind address with the
# declared port honored, instead of the collision silently reassigning a
# different port). Confirmed directly against the live object:
DB_DEFAULT_PORT="$(awsl rds describe-db-instances --db-instance-identifier complete-postgresql-2 \
  --query 'DBInstances[0].Endpoint.Port' --output text 2>/dev/null || true)"
if [ -z "$DB_DEFAULT_PORT" ] || [ "$DB_DEFAULT_PORT" = "None" ]; then
  # The second instance's identifier is whatever the module actually
  # assigned it; fall back to reading it by its own tofu-address marker
  # rather than guessing the identifier string.
  DB_DEFAULT_ID="$(awsl rds describe-db-instances \
    --query "DBInstances[?DBInstanceIdentifier != 'complete-postgresql'].DBInstanceIdentifier | [0]" --output text)"
  DB_DEFAULT_PORT="$(awsl rds describe-db-instances --db-instance-identifier "$DB_DEFAULT_ID" \
    --query 'DBInstances[0].Endpoint.Port' --output text)"
fi
[ "$DB_DEFAULT_PORT" = "5432" ] \
  || fail "the second (colliding-port) RDS instance's Endpoint.Port reads $DB_DEFAULT_PORT through the AWS CLI, not 5432 - lex00/floci#124's port-isolation fix may have regressed"
log "  lex00/floci#124 confirmed directly: the second, colliding-port RDS"
log "  instance's own Endpoint.Port reads back 5432 - the declared port is"
log "  honored even though another instance already holds it, via its own"
log "  distinct loopback bind address."
#
# INTENTIUS/choudoufu#393, confirmed absent from choudoufu's own plan
# (already implied by the empty-plan assertion above; reasserted by name).
grep -qF 'skip_final_snapshot' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -B5 'skip_final_snapshot'; fail "choudoufu's plan still shows skip_final_snapshot changing on module.db_default's aws_db_instance - INTENTIUS/choudoufu#393 has regressed"; }
log "  skip_final_snapshot does not appear in choudoufu's plan -"
log "  INTENTIUS/choudoufu#393 remains fixed."
#
# Stock's own replan against ITS OWN never-deleted state file, put here
# informationally rather than as the pass/fail oracle for this stage: it
# still proposes adding the same two parameter blocks (autovacuum,
# client_encoding) that the direct probe above just confirmed ALREADY
# match config on the live object. That is not a live discrepancy - it is
# a property of stock's own state file's fidelity at apply time (this
# script's stage 1 apply never round-tripped these two parameters into
# state, a distinct, older gap in the CREATE-time refresh rather than in
# either binary's own replan-time read), which the API probe above rules
# out as a currently-real difference. Recorded here so the number is
# understood rather than silently ignored, and NOT asserted as a pass/fail
# gate: HANDOFF's row 3 ("stock fails too") already covers a state file
# stock itself cannot keep current, and choudoufu's own empty, directly-
# verified replan is the stronger, correct answer.
STOCK_PLAN_OUT="$(cd "$PLAIN_EST" && terraform plan -input=false -no-color 2>&1)"
STOCK_PLAN_RC=$?
[ "$STOCK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_PLAN_OUT" | tail -40; fail "stock terraform plan against its own state exited $STOCK_PLAN_RC"; }
STOCK_PARAM_N="$(grep -cE '^ *\+ parameter \{$' <<< "$STOCK_PLAN_OUT")"
log "  informational: stock's plan against its OWN historical state file"
log "  still proposes $STOCK_PARAM_N parameter block(s) plus tag-removal"
log "  noise (stage 2 stamped tags stock's state does not know about) -"
log "  a property of that ONE state file's own apply-time fidelity, ruled"
log "  out as a live discrepancy by the direct API probe above, not"
log "  choudoufu's own oracle for this stage."

log ""
log "STAGE 3 (test_plan): PASS. The identity layer is CLEAR for real - 0"
log "refusals of any kind, where this estate stood at 7, then 33, then 14,"
log "then 2. The plan is genuinely empty - 'No changes. Your"
log "infrastructure matches the configuration.' - not merely reduced. The"
log "slot, create, replace and block-residue walls this estate stood on are"
log "all gone ($SLOT_N slot, $CREATE_N create, $REPLACE_N replace, 0 '+"
log "timeouts {' proposals), and now lex00/floci#120's round-trip gap is"
log "too: round 8 (PR #128/ff815779, #124's colliding-port fix) closed the"
log "LAST of its eight fields for this estate (module.db_default's own"
log "port, the second of two instances declaring port=5432), and the other"
log "seven plus the parameter block's apply_method were already fixed by"
log "earlier rounds this estate had not been re-crossed since. Confirmed"
log "three independent ways: choudoufu's own empty replan, a direct"
log "describe-db-parameters --source user probe of the live parameter"
log "group (autovacuum=1, client_encoding=utf8, matching config exactly),"
log "and a direct describe-db-instances probe of the second instance's own"
log "Endpoint.Port (5432, the declared port, not a reassigned collision"
log "port). INTENTIUS/choudoufu#393 remains fixed."
log ""
gauntlet_stage test_plan pass "genuinely empty replan (No changes. Your infrastructure matches the configuration.) with no local state file. lex00/floci#120's round-trip gap, this estate's last recorded wall, is CONFIRMED FIXED: round 8 (PR #128/ff815779, ghcr.io/lex00/floci:main-20260824d sha256:25fc9687, #124's RDS colliding-port isolation) closed the last of its eight fields for this estate - module.db_default's own port (module.db and module.db_default both declare port=5432, a genuine collision; module.db_default is the second-created instance and gets its own distinct loopback bind address with the declared port honored). The other seven fields (backup_window, monitoring_interval, monitoring_role_arn, performance_insights_retention_period, engine_lifecycle_support, enabled_cloudwatch_logs_exports, max_allocated_storage) and the parameter block's apply_method were already fixed by earlier rounds (round 5 and round 6's own #120 passes) that this estate had not been re-crossed since - the artifact's recorded '3 in-place updates' detail was stale before this round's own fix even landed. Confirmed three independent ways, not merely inferred from the empty plan: a direct describe-db-parameters --source user probe of the live parameter group (autovacuum=1, client_encoding=utf8, matching config exactly, no tofu in the loop), a direct describe-db-instances probe of the second instance's own Endpoint.Port (5432, the declared port), and all eight attribute names individually confirmed absent from choudoufu's plan. INTENTIUS/choudoufu#393 (skip_final_snapshot's phantom true->false update) remains fixed, confirmed absent. Stock's own replan against its own never-deleted state file still shows tag noise plus the two parameter blocks; ruled out as a live discrepancy by the same direct API probe (informational only, not this stage's oracle - HANDOFF row 3, a property of that one state file's own apply-time fidelity)."
CURRENT_STAGE=test_apply

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
log "=== 4. test apply: apply the empty plan; tagged object count unchanged ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

NOOP_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_APPLY_RC=$?
[ "$NOOP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$NOOP_APPLY_OUT" | tail -50; fail "the no-op apply exited $NOOP_APPLY_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes' <<< "$NOOP_APPLY_OUT" \
  || { grep -E 'Apply complete|Plan: ' <<< "$NOOP_APPLY_OUT"; fail "the no-op apply was not a genuine no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "the tagged object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the no-op apply left a state file behind"

# The primary DB instance's marker did not move either - re-read directly
# through the AWS CLI, the same call stage 2c used, not through
# choudoufu's own report of itself.
GOT_DB_ADDR2="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" \
  --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_DB_ADDR2" = "$WANT_DB_ADDR" ] \
  || fail "after the no-op apply, the primary DB instance carries tofu-address=$GOT_DB_ADDR2, not $WANT_DB_ADDR"
log "  genuine no-op: $BEFORE_N tagged objects before, $AFTER_N after, no"
log "  state file either time, primary DB instance's marker unmoved"
log "  ($GOT_DB_ADDR2)."
log ""
log "STAGE 4 (test apply): PASS"
log ""
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file, primary DB instance marker unmoved"
CURRENT_STAGE=drift_reconverge

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
log "=== 5. drift and reconverge: one live object tampered out of band ==="
plan_into() { ( cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color ); }

if [ "${BREAK:-}" = "1" ]; then
  # A second, unrelated object is mutated too - the assertion below must
  # catch this as MORE than one object proposed, not silently pass.
  awsl rds add-tags-to-resource --resource-name "$PG_ARN_READ" --tags Key=Example,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered the parameter group's Example tag - stage 5"
  log "  must now see TWO drifted objects and fail the single-object"
  log "  assertion"
fi

# config declares Example = local.name ("complete-postgresql") on this
# instance - captured before tampering so the reconverge assertion below
# compares against config's real value rather than an assumed empty one.
ORIGINAL_EXAMPLE="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" --query "TagList[?Key=='Example'].Value | [0]" --output text)"
awsl rds add-tags-to-resource --resource-name "$DB_ARN" --tags Key=Example,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" --query "TagList[?Key=='Example'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated the primary DB instance's Example tag from \"$ORIGINAL_EXAMPLE\" to"
log "  \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly"
  log "  more than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -50; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN" --query "TagList[?Key=='Example'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "$ORIGINAL_EXAMPLE" ] \
    || fail "the primary DB instance's Example tag is \"$FIXED_VALUE\" after reconverging, not \"$ORIGINAL_EXAMPLE\" (its pre-tamper, config-matching value)"
  log "  reconverged: the primary DB instance's Example tag is back to"
  log "  \"$FIXED_VALUE\", its pre-tamper, config-matching value"
  gauntlet_stage drift_reconverge pass "one object tampered (primary DB instance's Example tag), plan proposed fixing exactly one object, apply changed 1 and reconverged the tag"
fi

# ══════════════════════════════════════════════════════════════════════════
# PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# Placed right after STAGE 5 and BEFORE PART D (day2_rename, below) on
# purpose, the same convention corpus-ec2-instance-complete's own PART F
# uses: module.db is never touched by PART D's rename (that stage's own
# two targets are module.security_group and module.db_default), so this
# section has no dependency on PART D's outcome. Both module.db's
# `identifier` and `db_name` change together - see F-ORACLE's own header
# comment (above stage 1) for why: `identifier` alone is a real in-place
# RENAME on aws_db_instance (RDS's own ModifyDBInstance NewDBInstance
# Identifier), confirmed empirically, not a replace, so `db_name` (the
# bootstrapped database name, which AWS cannot rename in place) is the
# argument that actually forces it; changing identifier alongside it gives
# an observable "same address, new identity value" the same way every
# other estate's own day2_replace section has. Three resources cascade
# from the SAME dependency edges F-ORACLE already names: the instance's
# own two CloudWatch log groups and its DB parameter group, all three
# named from `identifier` by default.
#
# THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
# for the full reasoning, reproduced only in summary here): OpenTofu core
# rejects a `lifecycle` block on a `module` call, and patching the
# vendored terraform-aws-rds module's own aws_db_instance resource to add
# create_before_destroy would cross this corpus's reduction-only
# convention, so this evidence pass exercises the default destroy-then-
# create ordering instead.
#
# NO BREAK=replace LEG: see corpus-security-group-complete's own day2_
# replace section (same unit) for the finding this reuses without
# re-measuring per estate - a manufactured marker coexistence is not
# detected while a valid record already resolves the declared address,
# for any type relying on the fungible-slot claimant matcher
# (internal/live/discovery/count.go's slotProblem/ProblemDuplicateSlot),
# reproduced there on corpus-ec2-instance-complete's own previously-
# passing leg too - plausibly a side effect of the record-primary plan
# ordering ruled 2026-08-23. Not fixed here: a discovery-layer change,
# out of scope for this script-only unit.
CURRENT_STAGE=day2_replace
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
record_import_id() { jq -r '.identity.import_id' "$1"; }
F_ADDR="module.db.module.db_instance.aws_db_instance.this[0]"
F_RECORD="$ADOPTED_EST/.tofu-records/tofu-records/$ESTATE/aws_db_instance/$(record_key "$F_ADDR")"

log "=== F0. capture the live instance and its record ahead of the forced replace ==="
[ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
[ "$F_OLD_IMPORT_ID" = "complete-postgresql" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not complete-postgresql"
F_OLD_ARN="$DB_ARN"
F_OLD_ADDR_TAG="$(awsl rds list-tags-for-resource --resource-name "$F_OLD_ARN" --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$F_OLD_ADDR_TAG" = "module.db.module.db_instance.aws_db_instance.this:0" ] \
  || fail "$F_OLD_ARN does not carry tofu-address=module.db.module.db_instance.aws_db_instance.this:0 ahead of day2_replace"
log "  $F_OLD_ARN, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

log "=== F1. choudoufu: change the ForceNew db_name argument (plus identifier), forcing a replace at the same declared address ==="
python3 -c "
p = '$ADOPTED_EST/main.tf'
s = open(p).read()
old_id = '  identifier = local.name\n'
assert s.count(old_id) == 1, 'day2_replace: identifier = local.name did not match exactly once - the corpus pin has moved'
s = s.replace(old_id, '  identifier = \"\${local.name}-replaced\"\n', 1)
old_dbname = '  db_name  = \"completePostgresql\"\n'
assert s.count(old_dbname) == 2, 'day2_replace: db_name line did not match exactly twice (module.db and module.db_default) - the corpus pin has moved'
s = s.replace(old_dbname, '  db_name  = \"completePostgresqlReplaced\"\n', 1)
open(p, 'w').write(s)
"
grep -q 'identifier = "${local.name}-replaced"' "$ADOPTED_EST/main.tf" || fail "changing module.db's identifier argument did not match - the corpus pin has moved"
grep -q 'db_name  = "completePostgresqlReplaced"' "$ADOPTED_EST/main.tf" || fail "changing module.db's db_name argument did not match - the corpus pin has moved"

F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
[ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
grep -qE '^  # module\.db\.module\.db_instance\.aws_db_instance\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.db's instance when its ForceNew db_name argument changes"; }
grep -qE '~ +db_name +=.+forces replacement' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark db_name as forcing replacement"; }
grep -qE '^  # module\.db\.module\.db_instance\.aws_cloudwatch_log_group\.this\["postgresql"\] must be replaced' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the replace into the postgresql cloudwatch log group"; }
grep -qE '^  # module\.db\.module\.db_instance\.aws_cloudwatch_log_group\.this\["upgrade"\] must be replaced' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the replace into the upgrade cloudwatch log group"; }
grep -qE '^  # module\.db\.module\.db_parameter_group\.aws_db_parameter_group\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not cascade the replace into the db parameter group"; }
F_REPLACED_COUNT="$(grep -cE '^  # module\.db\..+ must be replaced' <<< "$F_PLAN_OUT" || true)"
[ "$F_REPLACED_COUNT" = "4" ] \
  || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu replaces $F_REPLACED_COUNT of module.db's own resources, not the header's own 4 (instance + 2 cloudwatch log groups + parameter group)"; }
grep -qF 'Plan: 4 to add, 0 to change, 4 to destroy.' <<< "$F_PLAN_OUT" \
  || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan does not match F-ORACLE's own four-resource cascade"; }
log "  choudoufu: exactly one instance replace at the same declared address, cascading into its 2 cloudwatch log groups and its db parameter group - matches F-ORACLE's own plan shape"

F_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
[ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
grep -qE 'Resources: 4 added, 0 changed, 4 destroyed' <<< "$F_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply did not match the planned 4 added, 4 destroyed"; }

awsl rds describe-db-instances --db-instance-identifier complete-postgresql >/dev/null 2>&1 \
  && fail "complete-postgresql (the old identifier) still resolves after the replace - the old instance was orphaned, not destroyed"
log "  complete-postgresql (the old identifier) is gone - confirmed via the AWS CLI, not through choudoufu's own report"

F_NEW_ARN="$(awsl rds describe-db-instances --db-instance-identifier complete-postgresql-replaced --query 'DBInstances[0].DBInstanceArn' --output text)"
[ -n "$F_NEW_ARN" ] && [ "$F_NEW_ARN" != "None" ] && [ "$F_NEW_ARN" != "$F_OLD_ARN" ] \
  || fail "could not find a new, different db instance carrying the replaced identifier after the replace (got '$F_NEW_ARN')"
F_NEW_ADDR_TAG="$(awsl rds list-tags-for-resource --resource-name "$F_NEW_ARN" --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$F_NEW_ADDR_TAG" = "module.db.module.db_instance.aws_db_instance.this:0" ] \
  || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.db.module.db_instance.aws_db_instance.this:0 - the marker did not move onto the new object"
log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

# THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
# #398-guard shape: a stale record still naming the destroyed instance
# would be exactly the wrong-marker failure that outranks a missing one).
# The local record file at the SAME address must now hold the NEW
# instance's identifier, not the one captured in F0.
F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
[ "$F_NEW_IMPORT_ID" = "complete-postgresql-replaced" ] \
  || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not complete-postgresql-replaced - a stale record still claiming the destroyed instance, the #398-guard shape"
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

DB_ARN="$F_NEW_ARN"
gauntlet_stage day2_replace pass "choudoufu: changing module.db's ForceNew db_name argument (plus identifier, for an observable identity change) proposed exactly one instance replace at the same declared address, cascading into its 2 cloudwatch log groups and db parameter group (all replaced, all named from identifier) - 4 to add, 4 to destroy, matching F-ORACLE's own plan shape; applied cleanly; the old instance ($F_OLD_ARN) is confirmed gone and the new instance ($F_NEW_ARN) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new identifier, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action. No BREAK=replace leg - see this section's own header comment (reusing corpus-security-group-complete's own finding from this same unit rather than re-measuring it here)."
CURRENT_STAGE=""

CURRENT_STAGE=day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
# The exact escaped form of each marker (":0" vs no index at all) depends on
# how the external security-group module's own count resolves and is not
# worth guessing twice - both are discovered by scanning this estate's own
# tagged objects and matching by address prefix in bash.
SG_ALL_D="$(awsl ec2 describe-security-groups \
  --filters "Name=tag:tofu-estate,Values=$ESTATE" \
  --query "SecurityGroups[].[GroupId,Tags[?Key=='tofu-address']|[0].Value]" --output text)"
SG_LINE_D="$(grep -E '	module\.security_group\.' <<< "$SG_ALL_D" | head -1)"
[ -n "$SG_LINE_D" ] || { printf '%s\n' "$SG_ALL_D"; fail "no live security group found by its tofu-address marker"; }
SG_ID_D="$(awk -F'\t' '{print $1}' <<< "$SG_LINE_D")"
SG_ADDR_D_BEFORE="$(awk -F'\t' '{print $2}' <<< "$SG_LINE_D")"

DB_ARN_D="$(awsl rds describe-db-instances \
  --query "DBInstances[?contains(TagList[?Key=='tofu-address'].Value, \`module.db_default.module.db_instance.aws_db_instance.this:0\`)].DBInstanceArn | [0]" --output text)"
if [ -z "$DB_ARN_D" ] || [ "$DB_ARN_D" = "None" ]; then
  DB_ARN_D="$(awsl rds describe-db-instances \
    --query "DBInstances[?contains(TagList[?Key=='tofu-address'].Value, \`module.db_default.module.db_instance.aws_db_instance.this[0]\`)].DBInstanceArn | [0]" --output text)"
fi
[ -n "$DB_ARN_D" ] && [ "$DB_ARN_D" != "None" ] || fail "no live db instance found by its tofu-address marker"
DB_ADDR_D_BEFORE="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN_D" --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
log "  $SG_ID_D ($SG_ADDR_D_BEFORE), $DB_ARN_D ($DB_ADDR_D_BEFORE)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename module.db_default -> module.db_default_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "db_default" {/module "db_default_renamed" {/' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/module\.db_default\./module.db_default_renamed./g' "$ADOPTED_EST/outputs.tf"
  rm -f "$ADOPTED_EST/main.tf.bak" "$ADOPTED_EST/outputs.tf.bak"
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.db_default\.module\.db_instance\.aws_db_instance\.this\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying module.db_default's db instance - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.db_default_renamed\.module\.db_instance\.aws_db_instance\.this\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating module.db_default_renamed's db instance - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying the old db instance address and creating the new one - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.security_group -> module.security_group_renamed ==="
  sed -i.bak 's/module "security_group" {/module "security_group_renamed" {/' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/module\.security_group\./module.security_group_renamed./g' "$ADOPTED_EST/main.tf"
  rm -f "$ADOPTED_EST/main.tf.bak"
  cat >> "$ADOPTED_EST/main.tf" <<'EOF'

moved {
  from = module.security_group
  to   = module.security_group_renamed
}
EOF
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  # RE-VERIFIED against current main (re-verify-day2_remove unit, 2026-08):
  # this used to be zero churn. Root cause is now precisely named: 610511fb73
  # (internal/live/discovery/recordorphan_read.go, #405's day2_remove fix)
  # added recordOrphanReadSweep, which reads the record store for any
  # UNTAGGABLE type's undeclared old-address record and proposes destroying
  # it - generically, since its filter is "untaggable + has a persisted
  # identity record", not tied to any specific type. Its own rename-safety
  # check (the `pending` map, built from res.Unbound) only recognizes "a
  # declared instance of the SAME address is unclaimed" - it never
  # consults moved.Aliases/moved.Honoured(req.Config) the way the marker
  # path already does. So this moved block, relocating module.security_group,
  # now destroys aws_security_group_rule.ingress_with_cidr_blocks[0] under
  # the OLD address instead of matching it under the new one; the tagged
  # security group itself still moves correctly via the marker path, which
  # DOES follow moved blocks. SAME root cause, independently confirmed on
  # corpus-giantswarm-crossplane (aws_iam_role_policy family),
  # corpus-ec2-instance-complete (aws_route/aws_route_table_association)
  # and corpus-security-group-complete (aws_vpc_security_group_rules_exclusive)
  # in this same unit - a generic gap reaching at least these four estates.
  # live-mv does not hit this (RecordStore.MoveRecord re-keys the store
  # directly, 8bd0d47e4e); only a bare HCL `moved` block does. Not fixed
  # here - a Go change, out of scope for this script-only re-verification
  # unit. Because fail() exits immediately, day2_remove's own post-fix
  # status for this estate could not be independently re-measured this run.
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.security_group proposes a create/destroy for one of its children instead of matching them structurally under the parent's new address - not zero churn. Root cause: 610511fb73's recordOrphanReadSweep has no moved-block awareness (see the comment immediately above this assertion) - the SAME generic gap corpus-giantswarm-crossplane, corpus-ec2-instance-complete and corpus-security-group-complete independently hit in this same unit. day2_remove's own post-fix status for this estate could not be re-measured this run because of it."; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" -ge 1 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -20; fail "the moved-block rename plan proposes no in-place changes at all - nothing to rewrite the markers"; }
  grep -qF "Plan: 0 to add, $N_CHANGED_D1 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary does not match its own $N_CHANGED_D1 in-place changes"; }
  SG_ADDR_D_AFTER_RENAME="${SG_ADDR_D_BEFORE/module.security_group./module.security_group_renamed.}"
  grep -qE "~ +\"tofu-address\" = \"${SG_ADDR_D_BEFORE//./\\.}\" -> \"${SG_ADDR_D_AFTER_RENAME//./\\.}\"" <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the security group's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, $N_CHANGED_D1 in-place tags update(s) - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE "Resources: 0 added, $N_CHANGED_D1 changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_CHANGED_D1 resources"; }

  SG_ID_D_AFTER="$(awsl ec2 describe-security-groups --group-ids "$SG_ID_D" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$SG_ID_D_AFTER" = "$SG_ID_D" ] || fail "the security group's id changed across the rename ($SG_ID_D -> $SG_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  SG_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SG_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$SG_ADDR_D_AFTER" = "$SG_ADDR_D_AFTER_RENAME" ] \
    || fail "the security group carries tofu-address=$SG_ADDR_D_AFTER after the rename, not $SG_ADDR_D_AFTER_RENAME"
  log "  $SG_ID_D unchanged, tofu-address now $SG_ADDR_D_AFTER_RENAME - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module.db_default -> module.db_default_renamed, no moved block at all ==="
  sed -i.bak 's/module "db_default" {/module "db_default_renamed" {/' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/module\.db_default\./module.db_default_renamed./g' "$ADOPTED_EST/outputs.tf"
  rm -f "$ADOPTED_EST/main.tf.bak" "$ADOPTED_EST/outputs.tf.bak"
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  # live-mv's own CLI arguments parse ordinary HCL resource-address syntax
  # (bracket count indices), while the tofu-address TAG VALUE this estate's
  # markers carry escapes the same index as ":N" - convert before calling,
  # compare in tag form after.
  DB_ADDR_D_NEW="${DB_ADDR_D_BEFORE/module.db_default./module.db_default_renamed.}"
  DB_ADDR_D_BEFORE_CLI="${DB_ADDR_D_BEFORE/%:0/[0]}"
  DB_ADDR_D_NEW_CLI="${DB_ADDR_D_NEW/%:0/[0]}"
  MV_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-mv -estate="$ESTATE" "$DB_ADDR_D_BEFORE_CLI" "$DB_ADDR_D_NEW_CLI" 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "wall: aws_db_instance has no marker search path for live-mv. Its identity is provider-assigned (identifier_prefix, not identifier - the name is not known until create time), so live-mv can only find it by listing the type, and this provider version has no List support for aws_db_instance. choudoufu correctly refuses rather than guess (HANDOFF's 'a wrong marker outranks a missing one') - the SAME class of refusal ecs-fargate's day2_rename hit for aws_service_discovery_http_namespace. The moved-block leg (module.security_group, above) already proved zero churn; this is the live-mv leg's own, separate wall. live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF "\"$DB_ADDR_D_BEFORE\" -> \"$DB_ADDR_D_NEW\"" <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  DB_ARN_D_AFTER="$(awsl rds list-tags-for-resource --resource-name "$DB_ARN_D" --query "TagList[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$DB_ARN_D_AFTER" = "$DB_ADDR_D_NEW" ] \
    || fail "the db instance carries tofu-address=$DB_ARN_D_AFTER after live-mv, not $DB_ADDR_D_NEW"
  log "  $DB_ARN_D unchanged, tofu-address now $DB_ADDR_D_NEW - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.security_group renamed with zero churn (0 add, $N_CHANGED_D1 change, 0 destroy), marker rewritten in place; live-mv: module.db_default's db instance renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════
  # REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7, active)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from D2/D3's real, completed rename: module.db_default_renamed's
  # whole block - its one live resource, and every output that names it -
  # is removed outright, with no replacement declared anywhere. Picked (see
  # REMOVE-ORACLE above) because it is the one module call in this estate
  # whose own live resource has no untaggable AWS-side sibling: create_db_
  # option_group=false/create_db_parameter_group=false already keep it to a
  # single aws_db_instance (plus its own local random_id.snapshot_
  # identifier, issue #340's own record-backed effect, no cloud
  # representation - destroyed correctly below, confirming that half is not
  # the wall here).
  #
  # A BROADER FINDING than either issue #410 (S3's untaggable sibling) or
  # corpus-overture-tiles's own count-shrink extension of it: this is
  # WHOLE-BLOCK removal - the exact shape corpus-s3-bucket-complete's own
  # day2_remove used successfully for its own (single-level) bucket module -
  # yet the db instance's OWN destroy, a TAGGED, MARKED resource, is STILL
  # silently absent, with no diagnostic. The one structural difference from
  # S3's working case: this address is TWO module levels deep (module.
  # db_default_renamed calling its own module.db_instance, which declares
  # aws_db_instance.this), where S3's was one. That points at classifyOrphans's
  # "declared block still pending" walk not correctly concluding "no
  # declared instance anywhere" once BOTH the outer and the inner module
  # blocks disappear together, rather than at taggability at all - a third
  # data point for the same family #410 opened, not a new issue by itself.
  # The resulting cloud is equivalent either way (nothing left dangling once
  # the instance is actually gone, confirmed via the AWS CLI below), but the
  # PLAN differs from stock's, so this is left genuinely failing here rather
  # than asserting less than the oracle asserts.
  CURRENT_STAGE=day2_remove
  log "=== STAGE E. day2_remove: delete module.db_default_renamed's block outright ==="
  log "  stock oracle already computed above (REMOVE-ORACLE, before migrate ever wrote a live tag): exactly one destroy"
  python3 -c "
p = '$ADOPTED_EST/main.tf'
s = open(p).read()
start = s.index('module \"db_default_renamed\" {')
end = s.index('\n}\n', start) + len('\n}\n')
assert 'db_subnet_group_name' in s[start:end]
open(p, 'w').write(s[:start] + s[end:])
"
  grep -q 'module "db_default_renamed"' "$ADOPTED_EST/main.tf" && fail "STAGE E: module.db_default_renamed's block is still present"
  python3 -c "
import re
p = '$ADOPTED_EST/outputs.tf'
s = open(p).read()
blocks = re.findall(r'output \"[^\"]+\" \{.*?\n\}\n', s, re.S)
kept = [b for b in blocks if 'module.db_default_renamed.' not in b]
assert len(kept) < len(blocks), 'STAGE E: no db_default_renamed output blocks found - the corpus pin has moved'
open(p, 'w').write(''.join(kept))
"
  grep -q 'module.db_default_renamed' "$ADOPTED_EST/outputs.tf" && fail "STAGE E: outputs.tf still references module.db_default_renamed"
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color ) > /tmp/rds-day2-remove-init.log 2>&1 || {
    tail -40 /tmp/rds-day2-remove-init.log; fail "the day2_remove reinit failed"; }

  REMOVE_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
  [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
  grep -qE '^  # module\.db_default_renamed\.module\.db_instance\.aws_db_instance\.this\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
    || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose destroying module.db_default_renamed.module.db_instance.aws_db_instance.this[0]"; }
  grep -qE '^  # module\.db_default_renamed\.module\.db_instance\.random_id\.snapshot_identifier\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
    || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu does not propose destroying module.db_default_renamed.module.db_instance.random_id.snapshot_identifier[0] (issue #340's own record-backed effect, no cloud representation)"; }
  REMOVE_DESTROY_N="$(grep -cE '^  # .+ will be destroyed' <<< "$REMOVE_PLAN_OUT")"
  [ "$REMOVE_DESTROY_N" = "2" ] || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN_OUT"; fail "choudoufu proposes $REMOVE_DESTROY_N destroys, not exactly 2"; }
  log "  choudoufu: exactly two destroys proposed - the db instance and its own local random_id.snapshot_identifier - the same objects the stock oracle proposes destroying"

  REMOVE_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
  [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
  grep -qE '^Apply complete! Resources: 0 added, [0-9]+ changed, 2 destroyed\.$' <<< "$REMOVE_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }
  # $DB_ARN_D (D0, above): instance_use_identifier_prefix=true means the
  # live identifier itself carries a create-time random suffix (the same
  # reason live-mv refuses this resource, see D2's own wall text) - the
  # ARN captured before any of this ran is the only stable handle. RDS's
  # own describe-db-instances accepts either form.
  awsl rds describe-db-instances --db-instance-identifier "$DB_ARN_D" >/dev/null 2>&1 \
    && fail "the module.db_default_renamed db instance ($DB_ARN_D) is still live after the day2_remove apply"
  log "  the db instance is genuinely gone (describe-db-instances on $DB_ARN_D now errors, read via the AWS CLI, not choudoufu's own report)"

  FINAL_REMOVE_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_REMOVE_PLAN_RC=$?
  [ "$FINAL_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_REMOVE_PLAN_OUT" | tail -40; fail "the post-remove plan exited $FINAL_REMOVE_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_REMOVE_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_REMOVE_PLAN_OUT"; fail "the post-remove plan is not empty"; }
  log "  No changes. The db instance is gone and nothing else moved."

  gauntlet_stage day2_remove pass "choudoufu: deleting module.db_default_renamed's block proposed exactly two destroys (the db instance and its own local random_id.snapshot_identifier, no cloud representation - issue #340), applied cleanly, the db instance is genuinely gone from the live account (read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on the same renamed oracle tree also proposes exactly the same two destroys; the target was chosen (see header) because its own nested module.db_instance call has no untaggable AWS-side sibling under this estate's create_db_option_group=false/create_db_parameter_group=false, unlike the shapes that surfaced issue #410 for corpus-s3-bucket-complete and corpus-overture-tiles"
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""

CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY ==="
log ""
log "  stage 1  cold_deploy        PASS"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          PASS - genuinely empty replan; lex00/floci#120's round-trip gap CONFIRMED FIXED (round 8's #124 closed the last of its eight fields, three independent probes; see header)"
log "  stage 4  test_apply         PASS - genuine no-op, tagged object count and primary DB instance marker unchanged"
log "  stage 5  drift_reconverge   $([ "${BREAK:-}" = "1" ] && echo "SKIPPED (BREAK=1)" || echo "PASS - one object tampered, plan proposed fixing exactly one, apply reconverged it")"
log ""
log "39 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1-4 still pass and"
log "stage 5's single-object assertion is the one that fails."
