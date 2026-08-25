#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for uyuni-project/sumaform (live/corpus-manifest.json, pinned by commit -
# see that entry's own comment for why no tag), the first OpenTofu-NATIVE
# estate this goal has crossed: every other crossing tonight came from
# terraform-aws-modules/*, which is Terraform-authored and merely OpenTofu-
# compatible. sumaform describes itself as "OpenTofu configuration to
# quickly set up SUSE Multi-Linux Manager/Uyuni environments" - not
# "Terraform configuration" - and its own README leads with the OpenTofu
# binary, not terraform.
#
# THE SCOPING DECISION, and why it is not the whole example.
#
# sumaform is a libvirt/Uyuni-focused framework with AWS as one of several
# backend options (main.tf.aws.example, alongside azure/libvirt/ssh/null).
# The full example composes FOUR host roles - module.base (network + a
# bastion instance), module.mirror, module.server, module.minion - all
# built from the SAME leaf module, backend_modules/aws/host. That leaf
# module has NO root-facing toggle to disable its own Salt/SSH
# provisioning (`terraform_data.host_salt_configuration`, real
# remote-exec/file provisioners over SSH against the guest) for THREE of
# those four roles: base's bastion, mirror, and minion all launch it
# unconditionally. Only modules/server exposes `provision` as a real
# input, defaulting true, overridable to false. Floci launches a real
# Docker container per instance and CAN do real SSH (see its own
# docs/services/ec2.md - key injection, a mapped host port), but
# sumaform's own connection blocks hardcode `host = aws_instance.instance
# [...].public_dns` with no port override, which is not reachable from
# the host machine's own SSH client without the module itself routing
# through floci's mapped port - not ours to add without forking the
# module. This is exactly the "EC2 instances with real boot behavior,
# out of scope" case the task that produced this script named up front.
#
# So this crossing uses module.server ALONE (provision=false, the one
# role with a real off-switch) as the real, unmodified-sumaform-code
# centerpiece, plus this script's own plain aws_vpc/subnet/internet
# gateway/route table/NAT gateway/security group resources standing in
# for what module.base's own network submodule would otherwise create
# (create_network=true) - because create_network=true ALSO turns out to
# be unusable for a different, unrelated reason: floci does not implement
# CreateDhcpOptions (already a documented, out-of-scope-for-tonight gap -
# see live/e2e/corpus-vpc-complete/run.sh's own header) or
# ReplaceRouteTableAssociation (aws_main_route_table_association), and
# module.base's network submodule creates both unconditionally whenever
# create_network=true, with no toggle to skip just those two. Both are
# real floci API gaps, neither is this crossing's to implement (a genuine
# EC2 feature each, not a data-catalog fix). create_network=false skips
# both, and also zeroes module.base's own bastion quantity for free
# (`quantity = local.create_network ? 1 : 0`), which is what routes
# around the unreachable-provisioner problem above without touching a
# single line of sumaform's own code.
#
# create_private_network and create_additional_network are separate
# toggles from create_network and are turned off too (server attaches to
# the public subnet instead, via provider_settings.public_instance =
# true): backend_modules/aws/network's `data "aws_nat_gateway" "default"`
# (looked up by subnet_id, needed when create_network=false) is ALSO
# unconditional whenever create_network=false, and races the NAT gateway
# this script creates in the SAME apply unless module.base is ordered
# after it - which needs a real depends_on, which in turn forces
# module.base's OWN `data.aws_region.current` (feeding
# `data.aws_ami.tumbleweedo`'s count) to be unknown until apply, an
# "Invalid count argument" PLAN-time error regardless of which region is
# in use. Both problems are the identical unconditional-data-source shape
# 6a33837d (the EKS worker AMI fix, below) already names for a different
# module; sumaform's own ami.tf and network/main.tf both lean on it
# throughout. Rather than layer a second depends_on workaround on top of
# the first, stage 1 below applies in the two ordered phases Terraform's
# OWN "Invalid count argument" error suggests (-target, then a plain
# apply) - a real, common bootstrapping pattern, not a routed-around
# failure.
#
# A REAL FLOCI GAP, FIXED: sumaform's ami.tf declares one
# data "aws_ami" block PER SUPPORTED GUEST OS (~23 of them) feeding a
# single `ami_info` local every host module reads through lookup() - so
# EVERY ONE of those data sources evaluates unconditionally on every
# plan, regardless of which single image an estate's own instances
# actually pick (this crossing picks ubuntu2204 throughout). Floci's
# image catalog had zero AWS-owned SUSE, Red Hat or Rocky Linux entries,
# so ~20 of the ~23 returned "Your query returned no results" and
# module.base could not even compute its own outputs. Fixed on the fork:
# lex00/floci branch fix/sumaform-suse-ami-catalog, commit d4479d36,
# published as ghcr.io/lex00/floci:sumaform-suse-ami (digest below) -
# seeds 20 catalog entries under SUSE's, AWS Marketplace's, Rocky
# Linux's and Red Hat's real, documented AWS publisher accounts, named to
# match each data source's own name_regex. None of the 20 are ever
# actually launched (this crossing's own instances use ubuntu2204,
# already cataloged); they exist purely so the unconditional lookup
# resolves - the same shape 6a33837d ("seed AWS-owned EKS worker AMIs")
# already fixed for terraform-aws-eks, not a new pattern.
#
# THE REAL BLOCK THIS CROSSING FOUND, NOT PAPERED OVER: with the AMI
# catalog gap fixed, stage 1 (cold deploy) and stage 2 (migrate) both
# genuinely PASS - 11 real resources, 9 of them eligible and cleanly
# migrated (7 tag-stamped, 2 recorded under this estate's own `markers =
# record` selection - item 4 below). Stage 3 (test plan) is still not
# empty, for real, structural reasons baked into backend_modules/aws/host -
# the ONE leaf module EVERY AWS host role in this estate (bastion, mirror,
# server, minion) shares:
#
#   1. [RESOLVED by #353, no longer refused - kept here for the history]
#      aws_instance.instance carries an unconditional, provisioner-less
#      `connection { private_ip = self.private_ip }` block - dead code in
#      sumaform's own module (no provisioner anywhere in the same
#      resource references it). internal/live/lint's checkProvisioners
#      used to flag a connection block on its own terms regardless of
#      whether the estate had anywhere to keep a tainted-object bit.
#      GitHub issue #353 gave that bit a generic home - any instance
#      whose estate declares a record_store (this one does, see
#      write_main_tf below) now has one - and checkProvisioners's own
#      recordStoreConfigured gate (internal/live/lint/lint.go) returns
#      before ever looking at Provisioners or Connection, for every
#      resource type, not only RECORD_ADMITTED logical ones. Confirmed by
#      re-running this exact estate on the post-#353 tree: the connection-
#      block error no longer appears, only the ignore_changes refusal
#      below does. Unrelated work fixed this crossing's own finding; nice.
#   2. [RESOLVED by #365 slice 2's `markers = record`, kept here for the
#      history] Both aws_instance.instance and aws_ebs_volume.data_disk
#      carry `lifecycle { ignore_changes = [tags] }` - sumaform's own
#      WORKAROUND comment names why: "SUSE internal openbare AWS accounts
#      add special tags... After the first apply, terraform removes those
#      tags." A real, legitimate need at authoring time, but it ignores
#      the WHOLE tags argument, which is exactly where this mode's own
#      tofu-estate/tofu-address markers live, so internal/live/lint's
#      ignore-changes rule used to refuse it: HANDOFF.md's fourth row,
#      "handling it would write a wrong marker". The escape IS the record
#      rung: write_main_tf's live block below now carries `strict {
#      marker_repair = "never"; markers "record" { types = ["aws_instance",
#      "aws_ebs_volume"] } }`, and both types are recordable (their whole
#      identity is a single non-composite, non-sensitive `id` - see
#      internal/live/identity/table_generated.go's rows for both). Confirmed
#      by re-running this exact estate: "Error: Ownership markers would be
#      ignored" goes from 2 occurrences to 0, for real against floci.
#
#      One gap this surfaced at the time, RESOLVED by a later unit and kept
#      here for the history: `choudoufu live-import` (stage 2 above) did
#      not honour the selection. Its stamping path
#      (internal/live/liveimport/stamp.go) was a separate implementation
#      from the one internal/live/stamp and internal/live/lint's
#      ignore-changes check both read (identity.SelectionFor /
#      identity.SelectedLocatedType) - it never imported internal/live/stamp
#      at all - so both resources were still tag-stamped normally during
#      migrate. Only the LATER live-plan's lint check treated them as
#      record-based. See item 4 below for the fix and item 5 for the wall
#      it uncovered next.
#   3. [RESOLVED - kept here for the history, and for the shape, which is
#      the widest thing this crossing found] A static count() expression in
#      the SAME leaf module, on THREE OTHER resources - aws_eip.host_eip,
#      aws_eip_association.eip_assoc and aws_route53_record.dns_record (none
#      of them aws_instance or aws_ebs_volume). Their counts are
#      `local.host_eip ? var.quantity : 0` and
#      `local.route53_domain == null ? 0 : 1`, and every value they read is,
#      in this estate, a genuine literal - host_eip and quantity are
#      false/0/1 from literal provider_settings maps, route53_domain is null
#      because nothing sets it. Stock proceeds (stage 1 above applies this
#      exact estate cleanly with plain tofu, computing all three counts as
#      0) - HANDOFF.md's first row, choudoufu refuses where stock proceeds.
#
#      What actually blocked it, re-derived from a real run rather than
#      inherited: NOT "rebuilding a call", which is what #375's account
#      predicted. internal/live/identity's partialargs.go already RUNS a
#      function on a value whose refused leaf it substituted an unknown for
#      (composedArgument), and has since before this crossing. The two hops
#      it could not reach were both one layer further IN, inside
#      internal/configs' static scope itself:
#
#        (a) a managed resource or data source named inside a LOCAL of the
#            module being read - backend_modules/aws/base's
#            `local.configuration_output` merges
#            `aws_iam_instance_profile...[0].name` and ~23
#            `data.aws_ami.*.image_id` into a map whose other members are
#            literals - refuses THERE, and merge() of a refusal is a
#            refusal, so the whole map's KEY SET goes with it; and
#        (b) a child module's whole OUTPUT named in the middle of a larger
#            expression (`module.network.configuration` as one merge
#            argument, `module.base_backend.configuration` as another) -
#            refused by staticScopeData.StaticValidateReferences on the
#            grounds that "the module has not been evaluated yet", which is
#            true of a module's RESOURCES and false of its OUTPUTS. An
#            output is an expression written in the child module, and the
#            child module's scope is one this resolver can enter.
#
#      Both are now one seam:
#      configs.StaticEvaluator.WithUnknownForRefusedReferences (opt-in, and
#      reached only through partialargs.go's own tolerant retry, which runs
#      last and only after every strict route has failed). A refused
#      resource reference becomes cty.DynamicVal; a module call is answered
#      by evaluating that child's outputs the same tolerant way
#      (internal/live/identity/tolerantmodule.go), recursively, memoized per
#      child module instance. The substitution is an unknown and never a
#      guess, so a value that comes back KNOWN did not depend on it, and
#      every gate that turns a value into a marker still demands a known
#      one.
#
#      Measured on this exact estate, against floci, before and after:
#      live-plan went from 360 Error diagnostics (195 "Unable to compute
#      static value", 107 "Dynamic value in static context", 58 "Module
#      output not supported in static context") to ZERO, and all three
#      counts came out at the value stock computes for them - zero
#      instances, which is also what stock's own cold state holds. Stage 3a
#      below asserts both halves, plus a negative control proving its own
#      pattern is not vacuous.
#
#      The rule names no type and reaches every configuration in the corpus,
#      not this one: it is a property of static evaluation, not of a
#      provider. Pinned by value in
#      internal/live/identity/testdata/tolerant-module-output (three names
#      each spelled in a different file, one count that must come out zero,
#      and two resources reading the substituted members that must render
#      nothing) and in the 1660-row identity golden, where it changed 0 rows
#      and added 9.
#   4. [RESOLVED - live-import now honours `markers = record`; kept here
#      for the history] The wall reached once #3 was out of the way:
#      `choudoufu live-import` did not honour `markers = record`. Item 2
#      above already recorded the half of it that was visible then -
#      live-import's stamping path (internal/live/liveimport/stamp.go) was
#      a separate implementation from internal/live/stamp and never
#      consulted identity.SelectionFor, so both selected types were
#      tag-stamped normally during migrate. The half only a rendered plan
#      could show was the consequence: it also wrote no LOCATED RECORD for
#      them, and a markers=record instance's identity lives nowhere else.
#      So the replan used to read aws_instance.instance[0] and
#      aws_ebs_volume.data_disk[0] as ABSENT ("No record of which live
#      aws_instance ... owns exists yet"), and
#      aws_volume_attachment.data_disk_attachment[0], whose identity is a
#      composite of the volume's live ID, as PARENT_UNAVAILABLE. Plan used
#      to be 3 to add, 0 to change, 0 to destroy - all three that one gap.
#
#      Fixed by wiring live-import's Ratify/Approve to consult
#      identity.SelectionFor and identity.SelectedLocatedType exactly where
#      internal/live/stamp and internal/live/projection's writeBackLocated
#      already do (that function's own doc names the rule: "the set that
#      gets WRITTEN must be the set that gets READ"): a selected, taggable
#      instance now carries a new `located` carrier through Ratify instead
#      of `eligible`, and Approve writes its identity to the estate's
#      record store's located namespace (internal/live/projection's new
#      LocatedRecordFrom - factored out of writeBackLocated's own three-way
#      switch so migrate and apply derive a located identity from the
#      IDENTICAL rule - and SeedLocatedRecordForInstance, the located
#      sibling of the record-backed SeedRecordForInstance) rather than
#      stamping a tag. Confirmed at the store, by value, against floci:
#      .tofu-records/tofu-located now exists after -approve, and both
#      located records' importID equal the live object's own id, read
#      through the AWS CLI - stage 2 and stage 3b below assert this rather
#      than trusting the outcome line. aws_ebs_volume.data_disk[0], whose
#      whole identity is a non-composite id and which carries no
#      creation-only argument, now resolves with ZERO diff purely from its
#      located record.
#
#      The rule names no resource type: it is `identity.SelectionFor` (the
#      root module's own `markers "record"` block) and
#      `identity.SelectedLocatedType` (schema-derived: importable, a record
#      can hold the whole identity, no sensitive identity attribute) -
#      exactly the predicate internal/live/stamp and
#      internal/live/projection already gate on, asked again at this third
#      call site rather than re-derived. It reaches every type an operator
#      selects with `markers "record"` that passes that predicate, which
#      is Item 2's own two types here and is not bounded to them anywhere
#      in the code. It does NOT reach the OTHER door to the same
#      ClassRecordLocated (identity.LocatedType, the automatic route for a
#      markerless type with no admission-table row and no operator
#      selection at all) - that route was not wired into live-import by
#      this fix, is architecturally ready to be (the same `located` carrier
#      and the same LocatedRecordFrom/SeedLocatedRecordForInstance), and is
#      not needed by any type this repository's corpus currently reaches
#      through it at migrate time.
#   5. [RESOLVED - residue now covers list- and set-nested blocks; kept
#      here for the history] Reached only once #4 was fixed: with the
#      located-record gap closed, module.server's instance and its
#      dependent aws_volume_attachment.data_disk_attachment[0] were still
#      replaced. Plan: 2 to add, 0 to change, 2 to destroy. The cause was
#      NOT the located-record mechanism item 4 fixed - it was what that
#      mechanism's own recovery IS for an instance with no full prior
#      state: an ordinary provider `import` by bare id, the same recovery
#      a human's own `tofu import` gives. aws_instance's
#      `ephemeral_block_device` and `root_block_device` are both
#      documented (the provider's own Import section) as creation-only
#      block arguments a live read does not detect on refresh - config
#      wanted `ephemeral_block_device` blocks, the bare-import read had
#      none, and the mismatch forced the replace
#      ("+ ephemeral_block_device { # forces replacement }" in the plan).
#      The volume attachment's own identity is a composite of the
#      instance's live id, so it cascaded from the instance's replacement
#      rather than naming a second wall.
#
#      Confirmed as a real gap inside this script itself (stage 1c, right
#      after the cold apply, before anything is stamped): plain tofu,
#      given the SAME live instance and nothing but its bare id (a fresh
#      directory, no prior state at all - a record-located identity with
#      no residue), proposed the identical
#      "module.server.module.server.module.host.aws_instance.instance[0]
#      must be replaced".
#
#      Fixed by widening residue past NestingSingle. Issue #275's
#      mechanism (internal/live/projection/residue.go) already answers
#      exactly this question - "did the applied value ever come back from
#      a live read, or only ever from what we sent" - for a flat attribute
#      and, since corpus-rds-complete-postgres, for a NestingSingle block.
#      What excluded NestingList, NestingSet and NestingMap was one
#      function, residueEligibleBlock, and one stated (and, on inspection,
#      overstated) reason: "the classifier's discriminator compares a
#      whole attribute value before and after, which a collection-nested
#      block has no stable per-element form for". Verified against the
#      code rather than trusted from the comment - a brief is a lead, not
#      a fact, and so is a comment: [classifyResidue] already compares ANY
#      attribute's value, of any cty type, as one whole value via
#      cty.Value.RawEquals, and cty.Value.RawEquals on a cty.Set is
#      independently confirmed order-independent
#      (TestResidueSetOrderDoesNotAffectClassification). [carriesNoInformation]
#      already answers "does this read carry no information" for a list, a
#      set or a map the identical way it answers for a string: by asking
#      whether it is empty. Neither needed a single line changed. The real,
#      narrower question residueEligibleBlock's widened doc comment states
#      is about ABSENCE, not about elements: does the nesting mode read
#      back as something [carriesNoInformation] can tell apart from "really
#      there but empty"? NestingSingle (null), NestingList/NestingSet/
#      NestingMap (an empty collection) all answer yes; only NestingGroup
#      answers no (an absent group reads back as a block of zero-valued
#      attributes, indistinguishable from a real all-zero one), and
#      NestingGroup is the one mode still refused.
#
#      The rule names no resource type and reaches every block on every
#      admitted type: any [configschema.NestingList], [configschema.NestingSet]
#      or [configschema.NestingMap] block whose own schema carries nothing
#      sensitive or write-only anywhere inside it (the same test
#      residueEligibleBlock already applied to NestingSingle) is now a
#      structural residue candidate; whether the LIVE system happens to
#      answer for it, which is what determines whether anything is ever
#      actually recorded, is still classifyResidue's question alone and
#      unchanged by this widening - aws_default_network_acl's own
#      egress/ingress rules (real cloud data, NestingSet) are the existing
#      worked example that widening the filter does not silence real
#      drift, confirmed still true in this crossing's own regression
#      coverage (corpus-rds-complete-postgres's `ingress` is now a
#      structural candidate too, and is still correctly excluded by the
#      classifier, not by the filter).
#
#      Measured, by value, against floci: the plan moved from 2 to
#      add/0/2 to destroy (this item's own replacement) to 0 to add/1 to
#      change/0 to destroy - no replacement anywhere, and
#      ephemeral_block_device (2 elements, a NestingSet) round-trips with
#      zero diff, asserted in stage 3 below by checking the block does not
#      appear in the plan's rendering at all. New unit tests
#      (internal/live/projection/residue_test.go,
#      TestResidueCarriesListAndSetNestedBlocksByValue) pin both the
#      NestingList and the NestingSet case by value with an aws_instance-
#      shaped fixture, independent of this crossing's own floci run.
#   6. [RESOLVED by lex00/floci#103 - kept here for the history] Reached
#      only once #5 was fixed, and isolated to one line:
#      root_block_device.volume_size = 8 -> 200. Plan: 0 to add, 1 to
#      change, 0 to destroy. Never a residue question - residue exists for
#      an argument a live read never populates, and this one WAS populated,
#      just wrongly. Confirmed a FLOCI GAP, independent of any choudoufu
#      mechanism, at stage 1d: right after the cold apply, before any
#      import, replan or residue involvement at all, the root volume floci
#      itself just created from this estate's own
#      `root_block_device.volume_size = 200` reported size 8 through the
#      AWS CLI's own DescribeVolumes directly. A second volume the SAME
#      apply creates through a plain `aws_ebs_volume` resource (a real
#      CreateVolume call, not embedded in RunInstances' own
#      BlockDeviceMapping) got its requested size correctly, which is what
#      narrowed the gap to exactly one API shape: RunInstances'
#      BlockDeviceMapping.Ebs.VolumeSize was not honoured for the root
#      device. HANDOFF.md's fourth row: not something this unit's own code
#      could reach, and not attempted here - handed off instead (this
#      script's own stage 1d comment carries the standalone reproduction
#      and the sibling contrast) and fixed in lex00/floci#103, published in
#      ghcr.io/lex00/floci@sha256:e16d9007a03093b6a6edd22273dee9d8253131f18581b0fa20ae6d34178a3079.
#      See stage 1d's own comment for how much wider the real fix turned
#      out to be than this one field.
#
# Because all six live in the ONE leaf module every role shares, none of
# this is an artifact of this crossing's own reduced slice: module.mirror,
# module.minion, and module.base's own bastion would hit the identical walls
# the moment any of them is actually instantiated. A real, generalizable
# finding about this real, popular project's compatibility with choudoufu
# today, not a corner this script backed itself into.
#
# Stages 4 and 5 are unwritten, the same discipline
# live/e2e/corpus-lambda-simple/run.sh's own header uses for its own
# stage-3 block: there is nothing running yet for them to exercise.
#
#   bash live/e2e/corpus-sumaform-aws/run.sh
#
# Needs Docker and the AWS CLI. Prefers the real `tofu` binary for stage
# 1's cold deploy, since this is specifically an OpenTofu-native estate;
# falls back to `terraform` if `tofu` is not on PATH (TF_COLD_BIN
# overrides either way). .corpus is read, never written: modules/ and
# backend_modules/ - the only two directories this reduced slice uses -
# are copied out to a scratch directory first, same as every other corpus
# crossing.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   TF_COLD_BIN   the plain binary for stage 1 (default: tofu if present
#                 on PATH, else terraform).
#   FLOCI_PORT    host port for the emulator (default 4716, clear of
#                 every other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image. This crossing's own AMI-catalog fix
#                 (originally published standalone as
#                 ghcr.io/lex00/floci:sumaform-suse-ami) has since been
#                 folded into the shared pin - combine/floci-fixes-round-3
#                 branches off the SUSE-catalog merge (f4c3d5d4), so every
#                 pin from that round forward carries it. Verified live
#                 against the pin this file was last measured with:
#                 `ec2 describe-images --owners amazon --filters
#                 Name=name,Values=suse-sles-15*` returns 7, not 0.
#   BREAK         set to 1 to corrupt one expected tag string before
#                 stage 2's assertion, proving it is load-bearing rather
#                 than a grep that always matches; also day2_rename's own
#                 break control (PART D) once the estate is clear.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control (PART E)
#                 instead of the real removal: keep module.server's block
#                 in the config; the plan must propose no destroy for any
#                 of its resources - the Break text in
#                 tools/gauntlet/stages.go, verbatim.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/sumaform"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4716}"
FLOCI_NAME="choudoufu-corpus-sumaform-aws-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="eu-west-1"
AZ="eu-west-1a"
ESTATE_NAME="sumaform-aws-crossing"
KEY_NAME="sumaform-crossing-key"

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md
# #13): one namespace choudoufu applies into directly with no migration,
# and a SEPARATE namespace stock applies the identical config into as
# that stage's own oracle.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-sumaform-aws-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-sumaform-aws-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
GREEN_ESTATE_NAME="${ESTATE_NAME}-greenfield"

if [ -n "${TF_COLD_BIN:-}" ]; then
  TF_COLD="$TF_COLD_BIN"
elif command -v tofu >/dev/null 2>&1; then
  TF_COLD="tofu"
else
  TF_COLD="terraform"
fi

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
gauntlet_begin
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v "$TF_COLD" >/dev/null 2>&1 || fail "TF_COLD_BIN=$TF_COLD is not on PATH - needed for stage 1's plain cold deploy"
command -v ssh-keygen >/dev/null 2>&1 || fail "ssh-keygen is not on PATH"
[ -d "$SRC/modules" ] && [ -d "$SRC/backend_modules" ] || fail "$SRC is missing modules/ or backend_modules/ - run 'go run ./tools/corpus-fetch' first"
log "  cold deploy binary: $TF_COLD"

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

# copy_estate <destdir>: modules/ and backend_modules/ only - the two
# directories this reduced slice reaches - plus sumaform's own documented
# "select a backend" setup step (README.md: `ln -s ../backend_modules/
# <BACKEND>/ modules/backend`, not this script's invention) and a fresh
# SSH key pair for aws_instance's required key_name/key_file inputs
# (never actually used for a real SSH session - module.server runs with
# provision=false, so the connection block inside its own
# terraform_data.host_salt_configuration, gated on that variable, is
# never evaluated; the key only has to exist for RunInstances to accept
# key_name and for Terraform's own `file(key_file)` local-side read).
copy_estate() {
  local dest="$1"
  mkdir -p "$dest"
  rsync -a --exclude '.git' "$SRC/modules" "$SRC/backend_modules" "$dest/"
  ln -sf ../backend_modules/aws/ "$dest/modules/backend"
  ssh-keygen -t ed25519 -f "$dest/crossing-key" -N '' -q -C sumaform-crossing
}

# write_main_tf <destdir> <live_block>: the estate itself. $2 is either
# empty (stage 1, cold deploy) or a `live { ... }` block (stage 2+) -
# every other line is identical between the two copies, ordinary
# onboarding (#269-style version pin, emulator provider flags), never a
# per-stage behavior change.
write_main_tf() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tf" <<EOF
terraform {
  required_version = ">= 1.5.7"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
$live_block
}

locals {
  region            = "$REGION"
  availability_zone = "$AZ"
  key_file          = "\${path.module}/crossing-key"
  key_name          = "$KEY_NAME"
}

provider "aws" {
  region = local.region

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style            = true
}


# Bring-your-own network: plain resources standing in for what
# modules/base's own network submodule creates when create_network=true -
# this script's header explains why create_network=true itself is not
# usable against floci today (CreateDhcpOptions,
# ReplaceRouteTableAssociation).
resource "aws_vpc" "crossing" {
  cidr_block           = "172.16.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = "sumaform-crossing-vpc" }
}

resource "aws_internet_gateway" "crossing" {
  vpc_id = aws_vpc.crossing.id
  tags   = { Name = "sumaform-crossing-igw" }
}

resource "aws_subnet" "crossing_public" {
  vpc_id                  = aws_vpc.crossing.id
  cidr_block              = "172.16.0.0/24"
  availability_zone       = local.availability_zone
  map_public_ip_on_launch = true
  tags                    = { Name = "sumaform-crossing-public-subnet" }
}

resource "aws_route_table" "crossing_public" {
  vpc_id = aws_vpc.crossing.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.crossing.id
  }
  tags = { Name = "sumaform-crossing-public-rt" }
}

resource "aws_route_table_association" "crossing_public" {
  subnet_id      = aws_subnet.crossing_public.id
  route_table_id = aws_route_table.crossing_public.id
}

resource "aws_eip" "crossing_nat" {
  domain = "vpc"
  tags   = { Name = "sumaform-crossing-nat-eip" }
}

resource "aws_nat_gateway" "crossing" {
  allocation_id = aws_eip.crossing_nat.id
  subnet_id     = aws_subnet.crossing_public.id
  depends_on    = [aws_internet_gateway.crossing]
  tags          = { Name = "sumaform-crossing-nat" }
}

resource "aws_security_group" "crossing_public" {
  name        = "sumaform-crossing-public-sg"
  description = "sumaform crossing public sg"
  vpc_id      = aws_vpc.crossing.id
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = { Name = "sumaform-crossing-public-sg" }
}

module "base" {
  source = "./modules/base"

  cc_username = "admin"
  cc_password = "admin12345"

  name_prefix     = "sumaform-crossing-"
  product_version = "uyuni-released"

  provider_settings = {
    availability_zone = local.availability_zone
    region             = local.region
    ssh_allowed_ips    = ["0.0.0.0/0"]
    key_name           = local.key_name
    key_file           = local.key_file
    bastion_image      = "ubuntu2204"

    create_network            = false
    create_private_network    = false
    create_additional_network = false
    vpc_id                    = aws_vpc.crossing.id
    public_subnet_id          = aws_subnet.crossing_public.id
    public_security_group_id  = aws_security_group.crossing_public.id
  }
}

module "server" {
  source             = "./modules/server"
  base_configuration = module.base.configuration

  name  = "server"
  image = "ubuntu2204"

  provision            = false
  repository_disk_size = 10
  provider_settings    = { public_instance = true }
}

output "server_id" {
  value = module.server.configuration.id
}
EOF
}

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ec2"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ec2"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ec2) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

copy_estate "$PLAIN"
copy_estate "$ESTATE"
write_main_tf "$PLAIN" ""
write_main_tf "$ESTATE" '
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      marker_repair = "never"
      markers "record" {
        types = ["aws_instance", "aws_ebs_volume"]
      }
    }
  }'
log "  estate copied out of .corpus/sumaform (modules/, backend_modules/ only) into $PLAIN and $ESTATE"

awsl ec2 import-key-pair --key-name "$KEY_NAME" --public-key-material "fileb://$PLAIN/crossing-key.pub" >/dev/null \
  || fail "importing the crossing key pair into floci failed"
log "  key pair $KEY_NAME imported (never actually used for SSH - provision=false)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu/terraform, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
log "=== STAGE 1: cold deploy (plain $TF_COLD, two-phase - see header) ==="
( cd "$PLAIN" && "$TF_COLD" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$PLAIN" && "$TF_COLD" init -input=false -no-color 2>&1 | tail -30 ); fail "plain init failed"; }

# Phase 1: the NAT gateway and its dependents only. modules/base's own
# `data "aws_nat_gateway" "default"` (create_network=false) looks the NAT
# gateway up by subnet_id with no resource-level edge tying it to
# module.base, so it can race the NAT gateway's own creation inside a
# single apply - this is Terraform's own suggested workaround for exactly
# this shape ("use the -target argument to first apply only the resources
# that the count depends on"), not a routed-around failure.
PHASE1="$(cd "$PLAIN" && "$TF_COLD" apply -input=false -auto-approve -no-color \
  -target=aws_nat_gateway.crossing -target=aws_route_table_association.crossing_public -target=aws_security_group.crossing_public 2>&1)" || {
  printf '%s\n' "$PHASE1" | tail -40; fail "stage 1 phase 1 (network bootstrap) failed"; }
log "  phase 1: $(grep -E 'Apply complete' <<< "$PHASE1" | tail -1)"

PHASE2="$(cd "$PLAIN" && "$TF_COLD" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PHASE2" | tail -40; fail "stage 1 phase 2 (module.base + module.server) failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$PHASE2" \
  || { grep -E 'Apply complete' <<< "$PHASE2"; fail "stage 1 phase 2 did not add exactly 3 resources (module.server's instance, EBS volume, attachment) - the AMI catalog fix or the corpus pin may have moved"; }
log "  phase 2: $(grep -E 'Apply complete' <<< "$PHASE2" | tail -1)"

[ -f "$PLAIN/terraform.tfstate" ] || fail "plain $TF_COLD left no state file to migrate from"
# state list also lists every data source (module.base alone reads ~23
# data.aws_ami/data.aws_region/data.aws_vpc/data.aws_nat_gateway) - filter
# those out to count only actual managed resource instances.
TOTAL_RESOURCES="$(cd "$PLAIN" && "$TF_COLD" state list | grep -vE '(^|\.)data\.' | wc -l | tr -d ' ')"
[ "$TOTAL_RESOURCES" = "11" ] || fail "expected exactly 11 managed resource instances in state, got $TOTAL_RESOURCES"
log "  11 managed resource instances total (8 network/VPC pieces + 3 from module.server)"

INSTANCE_ID="$(cd "$PLAIN" && "$TF_COLD" output -raw server_id)"
[ -n "$INSTANCE_ID" ] || fail "could not read module.server's instance id back out of the cold state"
log "  module.server's instance: $INSTANCE_ID"

# The EBS volume's own live id, the same way VPC_ID and INSTANCE_ID are read
# below: straight out of stock's own cold state, never off anything
# choudoufu reports about itself. Needed for the located-record assertions
# in stages 2 and 3 - aws_ebs_volume.data_disk[0] is markers=record selected
# exactly like the instance (this script's header, item 2).
VOLUME_ID="$(cd "$PLAIN" && "$TF_COLD" state show 'module.server.module.server.module.host.aws_ebs_volume.data_disk[0]' | grep -E '^\s+id\s+=' | head -1 | awk -F'"' '{print $2}')"
[ -n "$VOLUME_ID" ] || fail "could not read module.server's EBS volume id back out of the cold state"
log "  module.server's EBS volume: $VOLUME_ID"

COLD_TAGS="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
[ "$COLD_TAGS" = "0" ] || fail "the cold-deployed instance already carries a tofu-address tag before migration - this test proves nothing"
log "  confirmed unmarked: $INSTANCE_ID carries no tofu-address tag"

# ── 1c. the stock oracle for a BARE import - no residue, no full state,
# nothing but the id - taken here before anything is stamped, so it is
# read from a cloud nothing has touched yet. This is what plain
# `tofu import` gives ANY resource with no state to migrate from, and it is
# strictly WEAKER than what this estate's own migrate stage gets (a full
# stock state file to read residue from), which is the point HANDOFF.md's
# fifth row makes explicit: stock has only the bare id because stock has no
# record, and choudoufu does. Kept as a control even after item 5 below
# closed most of the gap it used to show - a regression back to "no residue
# at all" would look exactly like this oracle again.
REIMPORT="$WORK/reimport"
mkdir -p "$REIMPORT"
rsync -a --exclude '.terraform' --exclude 'terraform.tfstate*' "$PLAIN/" "$REIMPORT/"
( cd "$REIMPORT" && "$TF_COLD" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$REIMPORT" && "$TF_COLD" init -input=false -no-color 2>&1 | tail -30 ); fail "the stock reimport oracle's init failed"; }
REIMPORT_OUT="$(cd "$REIMPORT" && "$TF_COLD" import -input=false -no-color 'module.server.module.server.module.host.aws_instance.instance[0]' "$INSTANCE_ID" 2>&1)" || {
  printf '%s\n' "$REIMPORT_OUT" | tail -40; fail "the stock reimport oracle's import failed"; }
STOCK_REPLAN="$(cd "$REIMPORT" && "$TF_COLD" plan -input=false -no-color -target='module.server.module.server.module.host.aws_instance.instance[0]' 2>&1)"
STOCK_REPLAN_RC=$?
[ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -40; fail "the stock reimport oracle's plan exited $STOCK_REPLAN_RC"; }
grep -qF 'module.server.module.server.module.host.aws_instance.instance[0] must be replaced' <<< "$STOCK_REPLAN" \
  || { printf '%s\n' "$STOCK_REPLAN" | grep -E '^Plan:|^  # '; fail "stock's own bare-import replan no longer reports the instance as replaced - the oracle for stage 3's remaining wall has moved"; }
STOCK_REPLAN_LINE="$(grep -E '^Plan: ' <<< "$STOCK_REPLAN" | tail -1)"
log "  stock oracle: plain $TF_COLD, importing the SAME live instance by bare id into a"
log "  fresh state (no prior state at all - a record-located identity with no residue),"
log "  also proposes \"$STOCK_REPLAN_LINE\" for it. choudoufu's own migrate keeps what"
log "  stock's bare import cannot: residue from the full state file - see item 5 below."

# ── 1d. [RESOLVED in the pinned emulator - kept here for the history and
# as a live regression guard] the floci oracle for the ONE diff item 5's
# residue fix could not reach: aws_instance's root_block_device.volume_size.
# This was never a residue question - residue exists for arguments a live
# READ never repopulates, and this one was read, just read WRONG. Checked
# here, against the volume floci itself just created from THIS estate's own
# config (root_block_device.volume_size = 200, defaulted from
# backend_modules/aws/host/variables.tf's own main_disk_size = 200, not a
# value this script hand-picked), through the AWS CLI directly - no import,
# no replan, no choudoufu involved - because the wrongness had to be shown
# independent of every mechanism this script's header discusses, or a
# future reader could have mistaken it for one more residue gap.
#
# THE FLOCI HANDOFF THAT FIXED THE WRITE SIDE, lex00/floci#103, published in
# ghcr.io/lex00/floci@sha256:e16d9007a03093b6a6edd22273dee9d8253131f18581b0fa20ae6d34178a3079
# (a combined image carrying five floci fixes). That build's write path was
# itself intermittently unreadable back: Ec2Service#matchesFilters had no
# case for attachment.instance-id/attachment.device on volumes, so
# `describe-volumes --filters attachment.instance-id=<id>` matched EVERY
# volume in the region and a multi-volume container could read back the
# wrong one's size. lex00/floci#103's REAL fix (the read-side filter,
# not the write-side parsing this comment first found) landed in
# ghcr.io/lex00/floci@sha256:78262a598550703a53da9a856099c0421307bbccd942aa53e12d3758eff2a4bb,
# verified deterministic at 20/20 cold starts - and THIS repin folded that
# digest into the shared live/floci-image pin, closing the gap between
# "fixed" and "fixed and usable by this crossing without its own
# FLOCI_IMAGE override":
#
#   Reproduction, standalone and independent of sumaform, that found it:
#
#     aws --endpoint-url http://127.0.0.1:<PORT> --region eu-west-1 \
#       ec2 run-instances --image-id ami-ubuntu2204 --instance-type t3.medium \
#       --block-device-mappings '[{"DeviceName":"/dev/xvda","Ebs":{"VolumeSize":200}}]' \
#       --query 'Instances[0].InstanceId' --output text
#     # -> i-XXXX
#     aws --endpoint-url http://127.0.0.1:<PORT> --region eu-west-1 \
#       ec2 describe-volumes \
#       --filters "Name=attachment.instance-id,Values=i-XXXX" "Name=attachment.device,Values=/dev/xvda" \
#       --query 'Volumes[0].Size' --output text
#     # -> was 8 before lex00/floci#103 (requested 200 in the mapping above)
#
#   The sibling contrast that narrowed it to ONE API shape, run against the
#   SAME floci container: a NON-root volume, created directly rather than
#   embedded in RunInstances' own launch request -
#
#     aws --endpoint-url http://127.0.0.1:<PORT> --region eu-west-1 \
#       ec2 create-volume --availability-zone eu-west-1a --size 10 --volume-type sc1 \
#       --query 'VolumeId' --output text
#     # -> vol-YYYY
#     aws --endpoint-url http://127.0.0.1:<PORT> --region eu-west-1 \
#       ec2 describe-volumes --volume-ids vol-YYYY --query 'Volumes[0].Size' --output text
#     # -> 10  (requested 10, always correct - CreateVolume was never the bug)
#
#   This crossing's own two real volumes are the same pair, values
#   confirmed live both before and after the fix: module.server's ROOT
#   volume (embedded in aws_instance.instance's own root_block_device,
#   RunInstances' BlockDeviceMapping) requested 200, used to report 8 -
#   asserted below, now the other way. module.server's DATA volume
#   (aws_ebs_volume.data_disk, module.host/main.tf's own
#   repository_disk_size = 10 -> a direct, standalone CreateVolume call)
#   requested 10, always reported 10 correctly - the sibling contrast this
#   stage keeps asserting to prove the fix reached the RIGHT path and did
#   not just move the symptom.
#
#   It turned out much bigger than this one symptom: lex00/floci#103's own
#   fix found that handleRunInstances never parsed BlockDeviceMapping.* AT
#   ALL - the root volume was hardcoded to 8 GiB/gp3 regardless of what was
#   requested, every NON-root mapping was silently dropped with no volume
#   ever created for it, and the sibling Ebs fields (VolumeType, Encrypted,
#   Iops, Throughput, DeleteOnTermination) were ignored too. This crossing's
#   own reproduction and sibling contrast are what made the whole path
#   findable, not only the one field this estate happened to set.
ROOT_VOLUME_SIZE="$(awsl ec2 describe-volumes --filters "Name=attachment.instance-id,Values=$INSTANCE_ID" "Name=attachment.device,Values=/dev/xvda" --query 'Volumes[0].Size' --output text)"
[ -n "$ROOT_VOLUME_SIZE" ] && [ "$ROOT_VOLUME_SIZE" != "None" ] || fail "could not read module.server's root volume size through the AWS CLI"
[ "$ROOT_VOLUME_SIZE" = "200" ] \
  || fail "the root volume reports size $ROOT_VOLUME_SIZE, want 200 (root_block_device.volume_size). This estate requires a floci image carrying lex00/floci#103's REAL fix (Ec2Service#matchesFilters honouring attachment.instance-id/attachment.device on volumes, not just the earlier RunInstances/BlockDeviceMapping parsing, which was necessary but not sufficient - the read-side filter is what silently matched every volume in the region and made the write look racy). Validated deterministic at 20/20 cold container starts against ghcr.io/lex00/floci@sha256:78262a598550703a53da9a856099c0421307bbccd942aa53e12d3758eff2a4bb using three differently-sized volumes per container (the earlier digest's own check requested the same size every trial and was structurally blind to the filter bug). If this fails against that digest or a later one, treat it as a real regression, not the old known race - the race was in the filter, not the write, and #103's real fix is what closed it."

# The sibling contrast, asserted rather than only narrated: the SAME apply's
# OWN data disk (a direct, standalone CreateVolume call, not embedded in
# RunInstances) must report its OWN requested size correctly too - proving
# the fix reached RunInstances' BlockDeviceMapping specifically and did not
# merely change a shared default both paths happened to read.
DATA_VOLUME_SIZE="$(awsl ec2 describe-volumes --volume-ids "$VOLUME_ID" --query 'Volumes[0].Size' --output text)"
[ "$DATA_VOLUME_SIZE" = "10" ] \
  || fail "module.server's data disk (a direct CreateVolume call) reports size $DATA_VOLUME_SIZE, want 10 (repository_disk_size)"

log "  floci oracle: the root volume floci just created from this estate's own"
log "  root_block_device.volume_size = 200 reports size $ROOT_VOLUME_SIZE through the AWS CLI directly -"
log "  RunInstances' BlockDeviceMapping.Ebs.VolumeSize is now honoured for the root device"
log "  (lex00/floci#103, fixed). Sibling contrast: the data disk's own direct CreateVolume"
log "  call (repository_disk_size = 10) still correctly reports size $DATA_VOLUME_SIZE."

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "11 managed resource instances, genuinely cold, genuinely unmarked"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, active)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: choudoufu applies the
# same estate directly, no migration ever, compared object by object
# against stock's OWN fresh two-phase apply of the identical config in a
# THIRD namespace - the same two-phase bootstrap stage 1 needs (see this
# script's header), because the underlying "Invalid count argument" shape
# is a property of the HCL, not of which binary evaluates it.
#
# Run in a subshell: fail() exits, and a real, honestly-reported FAILURE
# here (see below) must not take stage 2 onward down with it - day2_rename
# and day2_remove both operate on the separately-migrated $ESTATE and have
# nothing to do with whether this stage's own fresh-apply record-binding
# gap is fixed yet. A subshell's exit only ends the subshell; the
# gauntlet_stage line it already printed on the way out is what the
# artifact records.
(
CURRENT_STAGE=greenfield
log ""
log "=== PART GREENFIELD: 0. two more floci containers ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for ep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  H=""
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"ec2"' <<< "${H:-}" && break
    sleep 2
  done
  grep -q '"ec2"' <<< "${H:-}" || fail "floci did not come up healthy (ec2) at $ep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
GREEN="$WORK/green"
copy_estate "$GREEN"
write_main_tf "$GREEN" '
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      marker_repair = "never"
      markers "record" {
        types = ["aws_instance", "aws_ebs_volume"]
      }
    }
  }'
aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 import-key-pair --key-name "$KEY_NAME" --public-key-material "fileb://$GREEN/crossing-key.pub" >/dev/null \
  || fail "importing the crossing key pair into the greenfield floci failed"
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GPHASE1="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color \
  -target=aws_nat_gateway.crossing -target=aws_route_table_association.crossing_public -target=aws_security_group.crossing_public 2>&1)" || {
  printf '%s\n' "$GPHASE1" | tail -40; fail "the greenfield phase 1 (network bootstrap) failed"; }
log "  phase 1: $(grep -E 'Apply complete' <<< "$GPHASE1" | tail -1)"
GPHASE2="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GPHASE2" | tail -40; fail "the greenfield phase 2 (module.base + module.server) failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$GPHASE2" \
  || { grep -E 'Apply complete' <<< "$GPHASE2"; fail "the greenfield phase 2 did not add exactly 3 resources"; }
log "  phase 2: $(grep -E 'Apply complete' <<< "$GPHASE2" | tail -1)"
[ ! -f "$GREEN/terraform.tfstate" ] || fail "the greenfield apply left a state file - this estate must never keep local state"

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI and the record store directly ==="
GTAGGED="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$GREEN_ESTATE_NAME" --query 'length(ResourceTagMappingList)' --output text)"
[ "$GTAGGED" = "7" ] || fail "the greenfield estate has $GTAGGED tag-stamped objects, expected 7"
[ -d "$GREEN/.tofu-records/tofu-records" ] || fail "the greenfield apply wrote no tofu-records namespace"
# The record store now holds an envelope per instance regardless of
# markers selection (rfc/20260823-foundation-order-ruling.md: "the record
# holds the identity of every instance, written by live-import and by
# every apply") - a bare file count can no longer tell "record-selected,
# no tag" apart from "tagged, and also recorded", so this checks the two
# markers=record-selected addresses BY NAME, the same way stage 2's own
# located_import_id does, and confirms those two specifically carry no
# tag while the tagged population elsewhere is exactly 7.
GREEN_INSTANCE_ADDR_FULL="module.server.module.server.module.host.aws_instance.instance[0]"
GREEN_VOLUME_ADDR_FULL="module.server.module.server.module.host.aws_ebs_volume.data_disk[0]"
green_located_import_id() {
  local want_addr="$1" f
  f="$(grep -rlF "\"address\":\"${want_addr}\"" "$GREEN/.tofu-records/tofu-records" 2>/dev/null | head -1)"
  [ -n "$f" ] || return 1
  grep -o '"import_id":"[^"]*"' "$f" | head -1 | cut -d'"' -f4
}
GREEN_INSTANCE_ID="$(green_located_import_id "$GREEN_INSTANCE_ADDR_FULL")" \
  || fail "no record file names address '$GREEN_INSTANCE_ADDR_FULL' after the greenfield apply"
[ -n "$GREEN_INSTANCE_ID" ] || fail "the greenfield instance's record carries no import_id"
GREEN_VOLUME_ID="$(green_located_import_id "$GREEN_VOLUME_ADDR_FULL")" \
  || fail "no record file names address '$GREEN_VOLUME_ADDR_FULL' after the greenfield apply"
[ -n "$GREEN_VOLUME_ID" ] || fail "the greenfield volume's record carries no import_id"
GREEN_INSTANCE_TAGCOUNT="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=resource-id,Values=$GREEN_INSTANCE_ID" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
[ "$GREEN_INSTANCE_TAGCOUNT" = "0" ] || fail "the greenfield instance carries a tofu-address tag; it is markers=record selected and must not be"
GREEN_VOLUME_TAGCOUNT="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-tags \
  --filters "Name=resource-id,Values=$GREEN_VOLUME_ID" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
[ "$GREEN_VOLUME_TAGCOUNT" = "0" ] || fail "the greenfield EBS volume carries a tofu-address tag; it is markers=record selected and must not be"
log "  7 tag-stamped; the instance ($GREEN_INSTANCE_ID) and EBS volume ($GREEN_VOLUME_ID) located by record, by address, carrying no tag (markers = record honoured on a first apply too)"

log "=== PART GREENFIELD: 3. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -40; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD: 4. stock oracle - the identical config applied fresh in its own namespace ==="
GREEN_ORACLE="$WORK/green-oracle"
copy_estate "$GREEN_ORACLE"
write_main_tf "$GREEN_ORACLE" ""
aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 import-key-pair --key-name "$KEY_NAME" --public-key-material "fileb://$GREEN_ORACLE/crossing-key.pub" >/dev/null \
  || fail "importing the crossing key pair into the oracle floci failed"
( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" "$TF_COLD" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" "$TF_COLD" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
OPHASE1="$(cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" "$TF_COLD" apply -input=false -auto-approve -no-color \
  -target=aws_nat_gateway.crossing -target=aws_route_table_association.crossing_public -target=aws_security_group.crossing_public 2>&1)" || {
  printf '%s\n' "$OPHASE1" | tail -40; fail "the greenfield oracle's phase 1 failed"; }
OPHASE2="$(cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" "$TF_COLD" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OPHASE2" | tail -40; fail "the greenfield oracle's phase 2 failed"; }
grep -qE 'Apply complete! Resources: 3 added' <<< "$OPHASE2" \
  || { grep -E 'Apply complete' <<< "$OPHASE2"; fail "the greenfield oracle's phase 2 did not add exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$OPHASE2" | tail -1)"

log "=== PART GREENFIELD: 5. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
GVPC_CIDR="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-vpcs --filters Name=cidr-block,Values=172.16.0.0/16 --query 'Vpcs[0].CidrBlock' --output text)"
OVPC_CIDR="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-vpcs --filters Name=cidr-block,Values=172.16.0.0/16 --query 'Vpcs[0].CidrBlock' --output text)"
[ "$GVPC_CIDR" = "172.16.0.0/16" ] && [ "$OVPC_CIDR" = "172.16.0.0/16" ] \
  || fail "the crossing VPC's cidr differs: greenfield=$GVPC_CIDR oracle=$OVPC_CIDR"
GSG_RULES="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-security-groups --filters Name=group-name,Values=sumaform-crossing-public-sg \
  --query 'SecurityGroups[0].[length(IpPermissions),length(IpPermissionsEgress)]' --output text)"
OSG_RULES="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-security-groups --filters Name=group-name,Values=sumaform-crossing-public-sg \
  --query 'SecurityGroups[0].[length(IpPermissions),length(IpPermissionsEgress)]' --output text)"
[ "$GSG_RULES" = "$OSG_RULES" ] \
  || fail "the crossing security group's ingress/egress rule counts differ: greenfield=($GSG_RULES) oracle=($OSG_RULES)"
# The greenfield instance carries no tag at all (markers = record
# selected, confirmed in part 2 above), so it is looked up by the id
# part 2 already read out of its own record, not by tag.
GINST="$(aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" ec2 describe-instances --instance-ids "$GREEN_INSTANCE_ID" \
  --query 'Reservations[0].Instances[0].[ImageId,InstanceType]' --output text)"
OINST="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" ec2 describe-instances --filters "Name=instance-state-name,Values=running,pending,stopped" \
  --query 'Reservations[0].Instances[0].[ImageId,InstanceType]' --output text)"
[ "$GINST" = "$OINST" ] \
  || fail "module.server's instance ami/type differs: greenfield=($GINST) oracle=($OINST)"
log "  vpc cidr, security-group rule counts, and the instance's ami+type match between the greenfield estate and the stock oracle in its own namespace"
gauntlet_stage greenfield pass "11 resources from nothing (7 tag-stamped, 2 recorded via markers = record, 2 untaggable/derived - route_table_association and volume_attachment), replan empty, stock oracle in its own namespace matches on vpc cidr, security-group rule counts and the instance's ami+type"
CURRENT_STAGE=""
docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
) || log "  PART GREENFIELD did not clear (see the FAIL line and the greenfield stage=fail line above) - continuing to stage 2 onward, which does not depend on it"
docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
CURRENT_STAGE=""

CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/resource "aws_eip" "crossing_nat" {/resource "aws_eip" "crossing_nat_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/allocation_id = aws_eip\.crossing_nat\.id/allocation_id = aws_eip.crossing_nat_renamed.id/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/resource "aws_route_table" "crossing_public" {/resource "aws_route_table" "crossing_public_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/route_table_id = aws_route_table\.crossing_public\.id/route_table_id = aws_route_table.crossing_public_renamed.id/' "$PLAIN_ORACLE/main.tf"
rm -f "$PLAIN_ORACLE/main.tf.bak"
cat >> "$PLAIN_ORACLE/main.tf" <<'EOF'

moved {
  from = aws_eip.crossing_nat
  to   = aws_eip.crossing_nat_renamed
}

moved {
  from = aws_route_table.crossing_public
  to   = aws_route_table.crossing_public_renamed
}
EOF
( cd "$PLAIN_ORACLE" && "$TF_COLD" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && "$TF_COLD" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && "$TF_COLD" plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART E-ORACLE: REMOVE, stock (day2_remove, active - live/GAUNTLET.md #7):
# "Stock with the same block removed plans the same destroys." A SEPARATE
# copy of cold_deploy's own state, so this destroy has nothing to do with
# the rename above. Removes the WHOLE module.server call (and its matching
# output block) - its 3 resources (instance, EBS volume, volume
# attachment), NOT the bring-your-own-network resources: every one of
# those 8 is load-bearing for module.base (either a direct
# provider_settings input, or - aws_nat_gateway.crossing specifically -
# module.base's own `data "aws_nat_gateway" "default"` (create_network=
# false, this script's header) looks it up unconditionally by subnet_id,
# so deleting IT breaks that data source's own read with "no matching EC2
# NAT Gateway found", a hard plan-time error, not a clean single-object
# destroy - found and reverted after a real run reproduced exactly that.
# module.server depends ON module.base's output and nothing depends on
# module.server, so removing it cleanly destroys 3 objects and nothing
# else is left to reason about.
CURRENT_STAGE=day2_remove
log "=== E-ORACLE: stock terraform, delete module.server's whole block on cold_deploy's own state ==="
REMOVE_ORACLE="$WORK/remove-oracle"
cp -r "$PLAIN" "$REMOVE_ORACLE"
perl -0pi -e 's/\nmodule "server" \{.*\z//s' "$REMOVE_ORACLE/main.tf"
grep -q 'module "server"' "$REMOVE_ORACLE/main.tf" \
  && fail "removing module.server's block from the remove-oracle copy did not match - the corpus pin has moved"
( cd "$REMOVE_ORACLE" && "$TF_COLD" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE" && "$TF_COLD" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's init failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE" && "$TF_COLD" plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
for addr in 'aws_instance.instance[0]' 'aws_ebs_volume.data_disk[0]' 'aws_volume_attachment.data_disk_attachment[0]'; do
  grep -qF "  # module.server.module.server.module.host.$addr will be destroyed" <<< "$REMOVE_ORACLE_PLAN_OUT" \
    || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock does not propose destroying module.server.module.server.module.host.$addr when module.server's block is removed"; }
done
grep -qF 'Plan: 0 to add, 0 to change, 3 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly three destroys"; }
log "  stock: exactly three destroys (module.server's instance, EBS volume, volume attachment), nothing else, on the state cold_deploy produced"
CURRENT_STAGE=migrate

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "9 of 11 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | head -60; fail "live-import did not verify exactly 9 of 11 as eligible (the 7 network resources plus the instance and EBS volume - the route table association and volume attachment are untaggable)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  9 of 11 verified/drifted against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
# 7 stamped, not 9: live-import now honours this estate's own `markers =
# record` selection (write_main_tf's `strict { markers "record" { types =
# [...] } }` below), so aws_instance.instance[0] and
# aws_ebs_volume.data_disk[0] are the "2 newly recorded" - seeded into the
# estate's record store's located namespace instead of tag-stamped. This is
# GitHub issue #365 slice 2's own completeness catching up: this script's
# header (item 4, "THE WALL THAT IS LEFT") used to record exactly this gap
# and now records it fixed.
grep -qF "7 resource(s) newly stamped, 0 already stamped, 2 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 2 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp 7 and record 2 as expected"; }
log "  7 stamped, 2 newly recorded (markers = record selection honoured at migrate time)"

VPC_ID="$(cd "$PLAIN" && "$TF_COLD" state show aws_vpc.crossing | grep -E '^\s+id\s+=' | head -1 | awk -F'"' '{print $2}')"
[ -n "$VPC_ID" ] || fail "could not read the crossing VPC's id back out of the cold state"

# assert_tag <resource-id> <label> <expected-estate>: reads both markers
# straight off floci through the AWS CLI (never off choudoufu's own
# report of itself) and asserts BOTH the value and the estate name -
# HANDOFF.md's own standing bar, assert the rendered identity, not just a
# verdict. <expected-estate> is parameterized so BREAK=1 can hand this
# exact function a wrong value below and prove it actually discriminates,
# rather than asserting only the real value ever runs through it.
assert_tag() {
  local rid="$1" label="$2" want_estate="$3" addr est
  addr="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$rid" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text)"
  est="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$rid" "Name=key,Values=tofu-estate" --query 'Tags[0].Value' --output text)"
  [ -n "$addr" ] && [ "$addr" != "None" ] || { echo "  $label ($rid) carries no tofu-address"; return 1; }
  [ "$est" = "$want_estate" ] || { echo "  $label ($rid) carries tofu-estate=$est, not $want_estate"; return 1; }
  echo "  $label ($rid) -> tofu-address=$addr tofu-estate=$est"
  return 0
}

# no_marker_tag <resource-id> <label>: the negative half of assert_tag, for
# the two types this estate's own `strict { markers "record" }` selection
# covers (write_main_tf below). Their identity now belongs in the record
# store, not on a tag, and this fails loudly if a tag ever reappears -
# GitHub issue #365 slice 2's migrate-time half regressing back to always
# tag-stamping would pass every OTHER assertion here silently.
no_marker_tag() {
  local rid="$1" label="$2" n
  n="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$rid" "Name=key,Values=tofu-address" --query 'length(Tags)' --output text)"
  [ "$n" = "0" ] || { echo "  $label ($rid) carries a tofu-address tag; it is markers=record selected and must not be"; return 1; }
  echo "  $label ($rid) -> no tofu-address tag (expected: its identity is in the record store)"
  return 0
}

# located_import_id <address>: the record-backed instance's own rendered
# identity, read straight off the local record store's files on disk
# (never off choudoufu's own report of itself) - the record-store
# counterpart of assert_tag, for HANDOFF.md's same standing bar.
# internal/live/projection/record.go lays the store out as
# .tofu-records/tofu-records/<estate>/<type>/<base64-of-address> since
# GitHub issue #364 unit A1 collapsed the old separate tofu-located store
# into one per-instance envelope; each file is compact, one-line JSON with
# top-level "address" and a nested identity.import_id, so a literal grep on
# "address" finds the one file naming this exact instance, and a plain
# field extraction reads import_id back out of it - no jq dependency
# needed for two flat string fields.
located_import_id() {
  local want_addr="$1" f
  f="$(grep -rlF "\"address\":\"${want_addr}\"" "$ESTATE/.tofu-records/tofu-records" 2>/dev/null | head -1)"
  [ -n "$f" ] || { echo "  no record file names address '$want_addr'"; return 1; }
  grep -o '"import_id":"[^"]*"' "$f" | head -1 | cut -d'"' -f4
}

assert_tag "$VPC_ID" "the crossing VPC" "$ESTATE_NAME" || fail "the crossing VPC's markers are wrong"
no_marker_tag "$INSTANCE_ID" "module.server's instance" || fail "module.server's instance was tag-stamped; it is markers=record selected"
no_marker_tag "$VOLUME_ID" "module.server's EBS volume" || fail "module.server's EBS volume was tag-stamped; it is markers=record selected"

INSTANCE_ADDR_FULL="module.server.module.server.module.host.aws_instance.instance[0]"
VOLUME_ADDR_FULL="module.server.module.server.module.host.aws_ebs_volume.data_disk[0]"

GOT_INSTANCE_IMPORT_ID="$(located_import_id "$INSTANCE_ADDR_FULL")"
[ "$GOT_INSTANCE_IMPORT_ID" = "$INSTANCE_ID" ] \
  || fail "the located record for module.server's instance holds importID='$GOT_INSTANCE_IMPORT_ID', want '$INSTANCE_ID' - GitHub issue #365 slice 2's migrate-time gap"
log "  module.server's instance -> located record importID=$GOT_INSTANCE_IMPORT_ID (matches the live object)"

GOT_VOLUME_IMPORT_ID="$(located_import_id "$VOLUME_ADDR_FULL")"
[ "$GOT_VOLUME_IMPORT_ID" = "$VOLUME_ID" ] \
  || fail "the located record for module.server's EBS volume holds importID='$GOT_VOLUME_IMPORT_ID', want '$VOLUME_ID' - GitHub issue #365 slice 2's migrate-time gap"
log "  module.server's EBS volume -> located record importID=$GOT_VOLUME_IMPORT_ID (matches the live object)"

# Negative control: the SAME function, over the SAME live resource, with a
# deliberately wrong expected estate name - proving assert_tag actually
# discriminates rather than passing anything it is handed.
if assert_tag "$VPC_ID" "the crossing VPC" "not-the-real-estate-name" >/dev/null 2>&1; then
  fail "assert_tag PASSED with a deliberately wrong expected estate name - this stage's assertion is not load-bearing"
fi
log "  negative control: assert_tag rejects a wrong expected estate name"

if [ "${BREAK:-}" = "1" ]; then
  # BREAK=1 flips the control above from a self-check into the reported
  # failure, proving this script's own exit code responds to it.
  fail "BREAK=1: treating the negative control above as the run's own result, to prove this script's exit code is not vacuously 0"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "7 stamped, 2 recorded (markers = record honoured at migrate time, GitHub issue #365 slice 2), 0 failed, 2 skipped"
CURRENT_STAGE=test_plan

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - the static-count wall is gone; a live-import gap is
# what is left (see this script's header, items 3 and 4)
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?

# The connection-block refusal used to fire here (see this script's header,
# item 1): #353 gave every instance whose estate declares a record_store
# somewhere to keep a tainted-object bit, and this estate's own
# write_main_tf does declare one, so checkProvisioners now admits the dead
# connection block on aws_instance.instance the same as it would a real
# provisioner. Assert that stays fixed - not just absent by accident - so a
# future regression here is caught the same way the original gap was.
grep -qF 'Error: Provisioners are not available under live resource markers' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | tail -60; fail "the connection-block refusal is back - #353's record_store admission (internal/live/lint.go's recordStoreConfigured gate) has regressed"; }

# The ignore_changes refusal (header item 2) is GONE, for real, as of the
# estate's own `strict { marker_repair = "never"; markers "record" { types =
# ["aws_instance", "aws_ebs_volume"] } }` block in write_main_tf above
# (GitHub issue #365 slice 2). Assert it stays gone rather than merely
# absent by accident.
[ "$(grep -cF 'Error: Ownership markers would be ignored' <<< "$PLAN_OUT")" = "0" ] \
  || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "expected 0 ignore-changes refusals now that aws_instance and aws_ebs_volume are markers=record selected, got $(grep -cF 'Error: Ownership markers would be ignored' <<< "$PLAN_OUT")"; }
log "  confirmed: the ignore_changes[tags] refusal on aws_instance.instance and aws_ebs_volume.data_disk"
log "  is gone - the markers = record selection above clears it for real against floci."

# --- 3a: the static-count wall (header item 3) is gone, asserted three ways
#
# Not "no error appeared": an error that stopped appearing because an
# earlier one now short-circuits would read the same. So this asserts the
# three counts came out at the VALUE stock computes for them - zero - and
# that the whole run refuses nothing at all.
[ "$PLAN_RC" = "0" ] || {
  printf '%s\n' "$PLAN_OUT" | grep '^Error:' | sort | uniq -c | sort -rn | head -20
  fail "live-plan exited $PLAN_RC; the static-count wall (or something new) is still refusing this estate"
}
REFUSALS="$(grep -c '^Error:' <<< "$PLAN_OUT" || true)"
[ "$REFUSALS" = "0" ] || {
  printf '%s\n' "$PLAN_OUT" | grep '^Error:' | sort | uniq -c | sort -rn | head -20
  fail "expected 0 refusals of any kind from live-plan, got $REFUSALS (was 360 before the tolerant static scope landed)"
}
log "  0 refusals of any kind (was 360: 195 'Unable to compute static value', 107 'Dynamic value in"
log "  static context', 58 'Module output not supported in static context')"

# The three counts, by value. `local.host_eip ? var.quantity : 0` (twice)
# and `local.route53_domain == null ? 0 : 1` all come out zero, so not one
# instance of the three resources may appear in the plan - and stock's own
# cold state, the oracle for this stage, holds none of them either. Both
# halves are asserted, because "choudoufu proposes none" is only evidence
# when it is the same answer stock gave.
for BLOCK in aws_eip.host_eip aws_eip_association.eip_assoc aws_route53_record.dns_record; do
  IN_PLAN="$(grep -cE "^  # .*\\.${BLOCK//./\\.}\\[" <<< "$PLAN_OUT" || true)"
  [ "$IN_PLAN" = "0" ] || {
    grep -E "^  # .*${BLOCK}" <<< "$PLAN_OUT" | head -5
    fail "$BLOCK expanded to $IN_PLAN instance(s) in the live plan; its count is a literal zero in this estate and stock computed zero"
  }
  IN_STOCK="$(cd "$PLAIN" && "$TF_COLD" state list | grep -cE "\\.${BLOCK//./\\.}\\[" || true)"
  [ "$IN_STOCK" = "0" ] || fail "stock's own cold state holds $IN_STOCK instance(s) of $BLOCK, so zero is not the oracle's answer and this assertion is wrong"
done
log "  aws_eip.host_eip, aws_eip_association.eip_assoc and aws_route53_record.dns_record all"
log "  expanded to ZERO instances, which is what stock's own cold state holds for each."

# The negative control for the loop above: the same test, on a block this
# estate really does declare, must find it somewhere in a NON-empty plan -
# without this, a typo in the pattern would make all three assertions pass
# vacuously. Once items 4-6 are ALL fixed the plan can be genuinely empty
# (asserted its own way at 3d below), and an empty plan has no "# addr will
# be ..." lines for ANYTHING, aws_instance.instance included - that is not
# the vacuous-pattern failure this control exists to catch, so it is
# skipped rather than tripped by success.
if grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT"; then
  log "  negative control skipped: the plan is fully empty (see 3d below), so aws_instance.instance"
  log "  correctly does not appear in ANY diff line - that is convergence, not a vacuous pattern."
else
  CONTROL="$(grep -cE '^  # .*\.aws_instance\.instance\[' <<< "$PLAN_OUT" || true)"
  [ "$CONTROL" != "0" ] || fail "the count assertion's own pattern matches nothing even for aws_instance.instance, so the three zeroes above prove nothing"
  log "  negative control: the same pattern finds aws_instance.instance in the plan, so the zeroes are real"
fi

# --- 3b: identities by value, against the AWS CLI
#
# The stage's own bar (live/GAUNTLET.md): an empty plan alone is not enough,
# because a wrong identity can converge. These read the marker straight off
# floci and compare it to the address the plan is written against.
assert_tag "$VPC_ID" "the crossing VPC" "$ESTATE_NAME" || fail "the crossing VPC's markers moved during the replan"
no_marker_tag "$INSTANCE_ID" "module.server's instance" || fail "module.server's instance was tag-stamped during the replan; it is markers=record selected"
VPC_ADDR="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text)"
[ "$VPC_ADDR" = "aws_vpc.crossing" ] || fail "the crossing VPC's tofu-address is '$VPC_ADDR', not 'aws_vpc.crossing'"
# module.server's instance and EBS volume carry no tag at all (asserted
# above): their identity is the located record written at migrate time, and
# a live-plan must not have moved it. Re-read by value rather than assumed
# unchanged.
GOT_INSTANCE_IMPORT_ID_REPLAN="$(located_import_id "$INSTANCE_ADDR_FULL")"
[ "$GOT_INSTANCE_IMPORT_ID_REPLAN" = "$INSTANCE_ID" ] \
  || fail "the located record for module.server's instance holds importID='$GOT_INSTANCE_IMPORT_ID_REPLAN' after the replan, want '$INSTANCE_ID'"
log "  identities by value: aws_vpc.crossing (tag) and"
log "  module.server.module.server.module.host.aws_instance.instance[0] (located record, importID=$GOT_INSTANCE_IMPORT_ID_REPLAN)"

# --- 3c: item 4's fix, confirmed at the store rather than only in the
# plan's prose. GitHub issue #364 unit A1 collapsed the once-separate
# tofu-residue and tofu-located namespaces into one per-instance envelope
# under tofu-records/tofu-records, so item 4 (identity) and item 5
# (residue) now live in the SAME file rather than in two directories -
# confirm both halves of that one file rather than two directories that no
# longer exist.
[ -d "$ESTATE/.tofu-records/tofu-records" ] || fail "live-import wrote no tofu-records namespace, so this estate's record store is not what stage 2 reported"
RECORD_FILE_INSTANCE="$(grep -rlF "\"address\":\"${INSTANCE_ADDR_FULL}\"" "$ESTATE/.tofu-records/tofu-records" 2>/dev/null | head -1)"
[ -n "$RECORD_FILE_INSTANCE" ] || fail "no record file names module.server's instance - live-import no longer honours markers=record; this script's header, item 4, has regressed"
grep -qF '"residue":{' "$RECORD_FILE_INSTANCE" \
  || fail "module.server's instance record carries an identity but no residue - item 5 has regressed"
log "  confirmed at the store: module.server's instance record carries BOTH its identity AND residue, unified -"
log "  live-import now honours markers = record at migrate time (items 4 and 5, FIXED)."

# --- 3d: items 4, 5 AND 6 are all fixed now (this script's header) - the
# plan is genuinely EMPTY, the strongest form of "no diff": not "the
# expected diffs are the ones we already named", but nothing at all.
PLAN_LINE="$(grep -E '^Plan: |^No changes\.' <<< "$PLAN_OUT" | tail -1)"
[ -n "$PLAN_LINE" ] || fail "live-plan printed no 'Plan:' or 'No changes.' line at all"
log "  $PLAN_LINE"
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { printf '%s\n' "$PLAN_OUT" | grep -E '^  #|^Plan:'; fail "expected an EMPTY plan now that items 4, 5 and 6 are all fixed; got '$PLAN_LINE'"; }
log "  the plan is EMPTY: items 4 (located records), 5 (residue for list/set-nested blocks)"
log "  and 6 (lex00/floci#103's RunInstances BlockDeviceMapping fix) are ALL fixed together."

# Both of module.server's record-based identities re-checked by value after
# the empty replan, the same way stage 2 checked them right after -approve -
# an empty plan is not evidence an identity held; only reading it again is.
GOT_INSTANCE_IMPORT_ID_EMPTY="$(located_import_id "$INSTANCE_ADDR_FULL")"
[ "$GOT_INSTANCE_IMPORT_ID_EMPTY" = "$INSTANCE_ID" ] \
  || fail "the located record for module.server's instance holds importID='$GOT_INSTANCE_IMPORT_ID_EMPTY' after the empty replan, want '$INSTANCE_ID'"
GOT_VOLUME_IMPORT_ID_EMPTY="$(located_import_id "$VOLUME_ADDR_FULL")"
[ "$GOT_VOLUME_IMPORT_ID_EMPTY" = "$VOLUME_ID" ] \
  || fail "the located record for module.server's EBS volume holds importID='$GOT_VOLUME_IMPORT_ID_EMPTY' after the empty replan, want '$VOLUME_ID'"
log "  identities re-checked after the empty plan: module.server's instance and EBS volume"
log "  still hold their own live ids at the record store, module.server.module.server.module.host."

log ""
log "STAGE 3 (test plan): EMPTY. Items 4 (live-import honouring markers = record), 5 (residue"
log "for list- and set-nested blocks) and 6 (lex00/floci#103) are all fixed - confirmed at the"
log "store, at the exact attribute, and now by an empty plan itself."
log ""
gauntlet_stage test_plan pass "Items 4, 5 and 6 (this script's header) are all FIXED and the plan is genuinely empty (\"No changes. Your infrastructure matches the configuration.\"): live-import honours markers = record (located records for aws_instance.instance[0] and aws_ebs_volume.data_disk[0], confirmed at the store and by value against the AWS CLI both right after migrate and again after this empty replan), residue now covers NestingList/NestingSet/NestingMap blocks (internal/live/projection's residueEligibleBlock, widened from the block's SHAPE - whether carriesNoInformation can tell its absence from a real empty answer - never from a type name), and lex00/floci#103 (published in ghcr.io/lex00/floci@sha256:e16d9007a03093b6a6edd22273dee9d8253131f18581b0fa20ae6d34178a3079) now honours RunInstances' BlockDeviceMapping.Ebs.VolumeSize for the root device, closing the one line (root_block_device.volume_size = 8 -> 200) that was this crossing's own last wall. Plan moved 3 to add/0/0 (the original ABSENT gap) -> 2 to add/0/2 to destroy (item 4 fixed, item 5's replacement exposed) -> 0 to add/1 to change/0 to destroy (item 5 fixed) -> empty (item 6 fixed by the emulator)."
CURRENT_STAGE=test_apply

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="

# BREAK=1 negative control, load-bearing on its own and independent of
# stage 5's: tamper a DIFFERENT tag-governed object's Name tag - the
# crossing internet gateway, never touched by stage 5's own VPC-targeted
# drift below - before this stage's own apply, so the "genuine no-op"
# assertion has something real to catch. Without this, a no-op check that
# reported success regardless of the live system's actual state would
# read as evidence rather than as the untested code it would be.
if [ "${BREAK:-}" = "1" ]; then
  IGW_ID="$(awsl ec2 describe-internet-gateways --filters "Name=tag:Name,Values=sumaform-crossing-igw" --query 'InternetGateways[0].InternetGatewayId' --output text)"
  [ -n "$IGW_ID" ] && [ "$IGW_ID" != "None" ] || fail "BREAK=1: could not find the crossing internet gateway to tamper"
  awsl ec2 create-tags --resources "$IGW_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: tampered $IGW_ID's Name tag before the no-op apply - the apply below must NOT report a genuine no-op"
fi

BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }

if [ "${BREAK:-}" = "1" ]; then
  grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
    && fail "BREAK=1: tampered the internet gateway's Name tag but the apply still reports a genuine no-op - this stage's own no-op check is not load-bearing"
  log "  BREAK=1: the apply correctly did NOT report a no-op ($(grep -E 'Apply complete' <<< "$APPLY2_OUT")) -"
  log "           this stage's own no-op assertion is load-bearing on its own, independent of stage 5"
  fail "BREAK=1: treating the negative control above as this stage's own result, to prove stage 4's exit code is not vacuously a pass"
fi

grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply - choudoufu apply under a live block must leave none"

# module.server's instance and EBS volume carry no tag at all, by design
# (markers=record), so BEFORE_N/AFTER_N above - the tofu-estate tag count -
# never counted them and cannot be the whole no-op claim for this estate.
# Their own identities are re-checked here too, the located-record
# equivalent of the tag count staying put.
GOT_INSTANCE_IMPORT_ID_NOOP="$(located_import_id "$INSTANCE_ADDR_FULL")"
[ "$GOT_INSTANCE_IMPORT_ID_NOOP" = "$INSTANCE_ID" ] \
  || fail "the located record for module.server's instance holds importID='$GOT_INSTANCE_IMPORT_ID_NOOP' after the no-op apply, want '$INSTANCE_ID'"
log "  genuine no-op: $BEFORE_N tagged objects before, $AFTER_N after, no state file either time,"
log "  and module.server's instance still holds its own located identity unchanged."
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N tagged objects before, $AFTER_N after, no state file either time; module.server's record-based instance and volume identities unchanged"
CURRENT_STAGE=drift_reconverge

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one tag-governed object, replan,
# assert exactly one fix
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 5: drift and reconverge (mutate the crossing VPC's Name tag out of band) ==="
# The crossing VPC is this estate's simplest tag-governed object
# (write_main_tf's own aws_vpc.crossing, tags = { Name = "sumaform-crossing-vpc" }) -
# module.server's instance and volume are markers=record selected and have
# no ownership tag for an out-of-band mutation to land on, which is why the
# VPC is the target here rather than either of them.
#
# BREAK=1's second object is the crossing security group, not the data
# volume - verified directly against a converged estate rather than assumed:
# module.host/main.tf's own aws_ebs_volume.data_disk carries
# `lifecycle { ignore_changes = [tags] }` (this script's header, item 2's
# own WORKAROUND comment), so a tampered Name tag on it produces "No
# changes" - config itself tells choudoufu to ignore that drift, the same
# way it tells stock to. Tampering it here would leave N_CHANGED at 1
# (the VPC alone) and make the assertion below wrongly conclude BREAK=1
# was not load-bearing, when the real defect would have been this choice of
# target. The security group carries no such lifecycle block, so its own
# Name tag drifting is real and visible, confirmed the same way.
if [ "${BREAK:-}" = "1" ]; then
  SG_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:Name,Values=sumaform-crossing-public-sg" --query 'SecurityGroups[0].GroupId' --output text)"
  [ -n "$SG_ID" ] && [ "$SG_ID" != "None" ] || fail "BREAK=1: could not find the crossing security group to tamper"
  awsl ec2 create-tags --resources "$SG_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered $SG_ID's Name tag - stage 5 must now see TWO drifted objects"
  log "           and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" --query "Tags[0].Value" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $VPC_ID's Name tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

DRIFT_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "           one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "aws_vpc.crossing" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not aws_vpc.crossing"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" --query "Tags[0].Value" --output text)"
  [ "$FIXED_VALUE" = "sumaform-crossing-vpc" ] \
    || fail "the VPC's Name tag is \"$FIXED_VALUE\" after reconverging, not \"sumaform-crossing-vpc\""
  log "  reconverged: $VPC_ID's Name tag is back to \"sumaform-crossing-vpc\", read via the AWS CLI"

  # Nothing else moved: module.server's record-based identities, untouched
  # by either the drift or the reconverge apply, still hold.
  GOT_INSTANCE_IMPORT_ID_DRIFT="$(located_import_id "$INSTANCE_ADDR_FULL")"
  [ "$GOT_INSTANCE_IMPORT_ID_DRIFT" = "$INSTANCE_ID" ] \
    || fail "the located record for module.server's instance holds importID='$GOT_INSTANCE_IMPORT_ID_DRIFT' after drift+reconverge, want '$INSTANCE_ID'"
  log "  module.server's instance identity unchanged throughout drift and reconverge."
  gauntlet_stage drift_reconverge pass "the crossing VPC's Name tag tampered out of band, plan proposed fixing exactly aws_vpc.crossing, apply changed 1 and reconverged the tag to sumaform-crossing-vpc; module.server's record-based identities unaffected"
fi

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (stages 2-5) is still marked and still converged, which
# is exactly the state a rename needs to start from. Two mechanisms, on two
# of the bring-your-own-network resources write_main_tf declares (module.base
# and module.server's own record-based identities are untouched by either):
# a `moved` block renames aws_eip.crossing_nat, and "choudoufu live-mv"
# renames aws_route_table.crossing_public with no moved block at all. The
# stock oracle for both runs on a copy of cold_deploy's own state, before
# choudoufu or live-import ever touched these objects.
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming aws_route_table.crossing_public WITHOUT a moved block,
# which must make choudoufu propose destroying the old address and creating
# the new one - the opposite of every other assertion in this part.

CURRENT_STAGE=day2_rename
log "=== D0. capture the two live ids a rename must not disturb ==="
EIP_ALLOC_ID="$(awsl ec2 describe-tags --filters "Name=resource-type,Values=elastic-ip" "Name=key,Values=tofu-address" "Name=value,Values=aws_eip.crossing_nat" --query "Tags[0].ResourceId" --output text)"
[ -n "$EIP_ALLOC_ID" ] && [ "$EIP_ALLOC_ID" != "None" ] || fail "no live eip found by its tofu-address marker"
RT_ID="$(awsl ec2 describe-tags --filters "Name=resource-type,Values=route-table" "Name=key,Values=tofu-address" "Name=value,Values=aws_route_table.crossing_public" --query "Tags[0].ResourceId" --output text)"
[ -n "$RT_ID" ] && [ "$RT_ID" != "None" ] || fail "no live route table found by its tofu-address marker"
log "  $EIP_ALLOC_ID (aws_eip.crossing_nat), $RT_ID (aws_route_table.crossing_public)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename aws_route_table.crossing_public -> .crossing_public_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_route_table" "crossing_public" {/resource "aws_route_table" "crossing_public_renamed" {/' "$ESTATE/main.tf"
  sed -i.bak 's/route_table_id = aws_route_table\.crossing_public\.id/route_table_id = aws_route_table.crossing_public_renamed.id/' "$ESTATE/main.tf"
  rm -f "$ESTATE/main.tf.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # aws_route_table\.crossing_public will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_route_table.crossing_public - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_route_table\.crossing_public_renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_route_table.crossing_public_renamed - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying aws_route_table.crossing_public and creating aws_route_table.crossing_public_renamed - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: aws_eip.crossing_nat -> .crossing_nat_renamed ==="
  sed -i.bak 's/resource "aws_eip" "crossing_nat" {/resource "aws_eip" "crossing_nat_renamed" {/' "$ESTATE/main.tf"
  sed -i.bak 's/allocation_id = aws_eip\.crossing_nat\.id/allocation_id = aws_eip.crossing_nat_renamed.id/' "$ESTATE/main.tf"
  rm -f "$ESTATE/main.tf.bak"
  cat >> "$ESTATE/main.tf" <<'EOF'

moved {
  from = aws_eip.crossing_nat
  to   = aws_eip.crossing_nat_renamed
}
EOF
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # aws_eip\.crossing_nat_renamed will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to aws_eip.crossing_nat_renamed"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" = "aws_eip\.crossing_nat" -> "aws_eip\.crossing_nat_renamed"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the eip's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  EIP_ALLOC_ID_AFTER="$(awsl ec2 describe-addresses --allocation-ids "$EIP_ALLOC_ID" --query "Addresses[0].AllocationId" --output text 2>/dev/null || true)"
  [ "$EIP_ALLOC_ID_AFTER" = "$EIP_ALLOC_ID" ] || fail "the eip's allocation id changed across the rename ($EIP_ALLOC_ID -> $EIP_ALLOC_ID_AFTER) - it was destroyed and recreated, not renamed"
  EIP_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$EIP_ALLOC_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$EIP_ADDR_AFTER" = "aws_eip.crossing_nat_renamed" ] || fail "the eip carries tofu-address=$EIP_ADDR_AFTER after the rename, not aws_eip.crossing_nat_renamed"
  log "  $EIP_ALLOC_ID unchanged, tofu-address now aws_eip.crossing_nat_renamed - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_route_table.crossing_public -> .crossing_public_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_route_table" "crossing_public" {/resource "aws_route_table" "crossing_public_renamed" {/' "$ESTATE/main.tf"
  sed -i.bak 's/route_table_id = aws_route_table\.crossing_public\.id/route_table_id = aws_route_table.crossing_public_renamed.id/' "$ESTATE/main.tf"
  rm -f "$ESTATE/main.tf.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" aws_route_table.crossing_public aws_route_table.crossing_public_renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"aws_route_table.crossing_public" -> "aws_route_table.crossing_public_renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  RT_ID_AFTER="$(awsl ec2 describe-route-tables --route-table-ids "$RT_ID" --query "RouteTables[0].RouteTableId" --output text 2>/dev/null || true)"
  [ "$RT_ID_AFTER" = "$RT_ID" ] || fail "the route table's id changed across live-mv ($RT_ID -> $RT_ID_AFTER) - it was destroyed and recreated, not renamed"
  RT_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$RT_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$RT_ADDR_AFTER" = "aws_route_table.crossing_public_renamed" ] || fail "the route table carries tofu-address=$RT_ADDR_AFTER after live-mv, not aws_route_table.crossing_public_renamed"
  log "  $RT_ID unchanged, tofu-address now aws_route_table.crossing_public_renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: aws_eip.crossing_nat renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: aws_route_table.crossing_public renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state (the EIP and route table
  # renamed; module.server untouched by either). Its whole block - and
  # the matching output block - are removed here; see E-ORACLE's own
  # comment above for why module.server, not one of the bring-your-own-
  # network resources, is this crossing's day2_remove target. E-ORACLE
  # already proved stock destroys all three of module.server's resources
  # cleanly on cold_deploy's own state.
  CURRENT_STAGE=day2_remove
  log ""
  log "=== E0. capture module.server's own record-based identities one more time ==="
  E_INSTANCE_ID="$(located_import_id "$INSTANCE_ADDR_FULL")" \
    || fail "no record file names address '$INSTANCE_ADDR_FULL' before day2_remove even starts"
  E_VOLUME_ID="$(located_import_id "$VOLUME_ADDR_FULL")" \
    || fail "no record file names address '$VOLUME_ADDR_FULL' before day2_remove even starts"
  [ -n "$E_INSTANCE_ID" ] && [ -n "$E_VOLUME_ID" ] || fail "module.server's own records carry no import_id before day2_remove even starts"
  log "  $E_INSTANCE_ID (the instance), $E_VOLUME_ID (the EBS volume)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.server's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.server\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: a destroy was proposed for one of module.server's resources even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.server's whole block ==="
    perl -0pi -e 's/\nmodule "server" \{.*\z//s' "$ESTATE/main.tf"
    grep -q 'module "server"' "$ESTATE/main.tf" \
      && fail "removing module.server's block did not match - the config has moved"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    for addr in 'aws_instance.instance[0]' 'aws_ebs_volume.data_disk[0]' 'aws_volume_attachment.data_disk_attachment[0]'; do
      grep -qF "  # module.server.module.server.module.host.$addr will be destroyed" <<< "$REMOVE_PLAN_OUT" \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.server.module.server.module.host.$addr when module.server's block is deleted"; }
    done
    grep -qE '^  # .+ will be (created|updated)' <<< "$REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's remove plan proposes something other than the three destroys"; }
    grep -qF 'Plan: 0 to add, 0 to change, 3 to destroy' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly three destroys"; }
    log "  choudoufu: exactly three destroys (module.server's instance, EBS volume, volume attachment), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 3 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly three destroys"; }

    # Confirmed directly against floci, not through choudoufu's own
    # report: a terminated instance's own describe-instances still lists
    # it (State=terminated, unlike a deleted NAT gateway's own listing
    # gap), and a deleted EBS volume is simply absent.
    INST_STATE="$(awsl ec2 describe-instances --instance-ids "$E_INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "absent")"
    [ "$INST_STATE" = "terminated" ] || [ "$INST_STATE" = "absent" ] \
      || fail "instance $E_INSTANCE_ID is still in state \"$INST_STATE\" after the destroy - it was orphaned, not destroyed"
    if VOL_STILL="$(awsl ec2 describe-volumes --volume-ids "$E_VOLUME_ID" 2>&1)"; then
      echo "$VOL_STILL"; fail "EBS volume $E_VOLUME_ID still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  instance $E_INSTANCE_ID state=\"$INST_STATE\", EBS volume $E_VOLUME_ID gone - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting module.server's block proposed exactly three destroys (0 add, 0 change, 3 destroy: the record-based instance and EBS volume, plus the untaggable/derived volume attachment), applied cleanly (0 added, 0 changed, 3 destroyed), the instance and volume are genuinely gone from the live account (instance State=$INST_STATE, volume absent, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes the same three destroys"
    log ""
  fi
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""

log ""
log "=== ESTATE CLEAR: cold_deploy, migrate, test_plan, test_apply, drift_reconverge and day2_rename all pass ==="
gauntlet_end
