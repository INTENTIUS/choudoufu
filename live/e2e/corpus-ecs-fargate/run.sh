#!/usr/bin/env bash
set -uo pipefail

# UPDATE 2026-08-24 (choudoufu/#395 and choudoufu/#376, both fixed): the
# round-9 repin below split the old cascading replacement into three
# things - #395, #376, and the standalone task definition's own,
# unrelated essential/mountPoints wall. Both #395 and #376 are choudoufu
# defects (HANDOFF row 2, "the plans differ") and both are now fixed, by
# one generic mechanism each:
#
#   choudoufu/#376 (track_latest/skip_destroy never carried forward):
#   internal/live/projection/build.go's configuredAttrsSeed generalizes
#   what used to be a tags-only import-stub seed (GitHub issue #287 item
#   8) to every attribute the plugin protocol's own contract says
#   configuration is the ONLY thing that can ever set - Required, or
#   Optional and never Computed. choudoufu keeps no persisted state, so
#   every plan re-derives "prior state" through ImportResourceState's
#   bare, near-null stub; seeding it with configuration's own statically
#   evaluable value for such an attribute reconstructs exactly what a
#   real state file's PriorState would already carry, and can never mask
#   real drift because a non-Computed attribute has none possible
#   independent of configuration. track_latest and skip_destroy are both
#   this shape (Optional, never Computed) and are set from plain
#   variables, so this alone was enough.
#
#   choudoufu/#395 (task_definition migrates as the short family:revision
#   form): the SAME property, but module.ecs_service's own
#   task_definition = aws_ecs_task_definition.this[0].arn is a REFERENCE
#   to another resource's computed attribute, which the config-language
#   subset's static evaluator can never resolve at all (var/local/path/
#   terminal only - never a managed resource). configuredAttrsSeed alone
#   left this one unfixed on a real re-run. The other half:
#   internal/live/projection/residue.go's residueConfigSourced widens
#   classifyResidue's own read-A/read-B test for the identical schema
#   property, so MIGRATE's ratify (RecordResidueForInstance) now records
#   task_definition's correct ARN as residue instead of rejecting it as
#   unrecordable "drift" (it was never drift - a non-Computed attribute's
#   read-A answer can only ever be a representation artifact, and the
#   read-B leg is what still catches genuine drift if the live object
#   really has changed). A new pre-read step, builder.residueSeedFor,
#   seeds THAT residue record into the import stub whenever
#   configuredAttrsSeed's static evaluator could not answer - the missing
#   half for a managed-reference attribute specifically. Both halves
#   verified directly against a live floci + hashicorp/aws 6.59.0 in a
#   standalone repro before landing (see the fix's own PR for the
#   reproduce command): seeding PriorState.task_definition with the
#   correct ARN, from either source, makes the provider's Read echo it
#   back unchanged instead of falling back to the short form.
#
# WANT_CHANGE_N drops from 2 to 0 (3d below is now an ABSENCE assertion
# for both, not an exact-text match). The standalone task definition's
# own replacement (WANT_ADD_N/WANT_DESTROY_N unchanged at 1) is
# untouched by either fix - confirmed unchanged on the same re-run,
# forced by the same two container_definitions diffs quoted below,
# neither a choudoufu defect.
#
# UPDATE 2026-08-24 (round-9 repin, lex00/floci PR #130, ec82d50d, issue
# #129, ghcr.io/lex00/floci:main-20260824e sha256:75987cd7): ECS
# ContainerDefinition/TaskDefinition now echo the exact registered JSON
# (raw-merge-then-override), fixing ~15 silently-dropped fields plus the
# entirely-absent runtimePlatform. This converges module.ecs_service's own
# task definition (no volume, default runtime platform) off its old
# replacement - confirmed by a traced replan, not merely an unmoved count
# (3a) - taking the cascading service task_definition attribute with it
# (asserted absent, 3d). The wall MOVED rather than closed: that converge
# exposes two choudoufu defects on module.ecs_service's own resources that
# the old replacement/cascade had been masking, both filed rather than
# fixed in this repin unit (a repin's job is to re-measure) - choudoufu/#395
# (task_definition migrates as family:revision instead of the live ARN
# config's `.arn` reference produces) and choudoufu/#376 (second confirmed
# instance: track_latest/skip_destroy never carried forward by migrate's
# record). WANT_CHANGE_N is 2 for these now, pinned by exact text so a
# regression cannot hide behind the count. Separately, the standalone
# module.ecs_task_definition (volume block, ARM64 runtime platform) still
# forces a replacement (WANT_ADD_N/WANT_DESTROY_N 2 -> 1), confirmed
# identical in stock's own replan, from two independent residual
# container_definitions diffs verified both against the emulator API and
# against real AWS with no tofu in the loop: `essential` (state has it
# explicit, config omits it - a genuine HANDOFF row-3 wall, real AWS also
# always echoes essential explicitly) and `mountPoints[].readOnly` (state
# has it explicit, config omits it - a confirmed emulator gap, real AWS
# leaves it absent; lex00/floci#131, fixed on branch
# fix/ecs-mountpoints-readonly, not yet published/repinned). Either alone
# forces the replacement (container_definitions is ForceNew), so #131
# alone would not have cleared this estate this round either. Round-8's
# lex00/floci#110 fix (serviceConnectConfiguration) stays fixed, asserted
# by absence (3d-pre).
#
# terraform-aws-modules/terraform-aws-ecs's flagship "fargate" example
# (.corpus/ecs/examples/fargate, pinned in live/corpus-manifest.json at tag
# v7.6.0, commit c83279b39). Fargate is the most common modern way people
# run ECS (the module's other example, ec2-autoscaling, is the older
# launch type and is not this crossing's target), and this module is the
# de facto standard way people provision it. It had never been crossed
# against a cloud before this script existed.
#
# 62 real resources: an ECS cluster (Container Insights + a FARGATE/
# FARGATE_SPOT default capacity-provider split), an ECS service behind an
# ALB with a BLUE_GREEN deployment_configuration, ECS Exec
# (enable_execute_command), ECS Service Connect, a two-container task
# definition (a "fluent-bit" firelens sidecar reading its image from a
# public AWS-managed SSM parameter, plus the app container), a second,
# standalone task definition (no service) with a volume and an ARM64
# runtime platform, a CloudMap HTTP namespace, an ALB module (2 target
# groups, blue/green listener rules), and a VPC module underneath it all.
#
# ONE EMULATOR-GAP WORKAROUND, not a config edit (DELTA 2 below): floci does
# not mirror AWS's own published SSM parameter catalog (the same class of
# gap floci-io/floci's AMI-catalog fixes address for EC2/EKS/sumaform, just
# not extended to SSM). `data.aws_ssm_parameter.fluentbit` reads
# `/aws/service/aws-for-fluent-bit/stable`, a real parameter AWS itself
# publishes with a real value in production - it is seeded via the AWS CLI
# before stage 1's apply, not edited out of the configuration or worked
# around with a variable.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block. PASS: a single, clean
#                     apply, 62 added, 0 changed, 0 destroyed.
#   2. MIGRATE       `choudoufu live-import -state=<plain's state>
#                     -estate=... -approve`. PASS: 46 of 62 instances
#                     eligible (30 VERIFIED + 16 DRIFTED - was 28+18 before
#                     the floci pin ghcr.io/lex00/floci@sha256:0afd2648...;
#                     re-measured against the new pin, module.ecs_cluster.
#                     aws_ecs_cluster.this[0] and module.ecs_service.
#                     aws_ecs_service.this[0] moved from DRIFTED to
#                     VERIFIED, confirmed by re-running the identical
#                     estate against the prior pin with today's binary -
#                     28/18 there, 30/16 here. floci's commit edf3bf23d
#                     ("fix(ecs): round-trip cluster settings/capacity
#                     strategy and service fields") is on the path to the
#                     new pin's commit and is the likely cause: the
#                     cluster's Container Insights/capacity-provider
#                     settings and the service's scheduling_strategy/
#                     enable_ecs_managed_tags/etc. fields - lex00/floci#59
#                     and #60 - now round-trip instead of silently
#                     dropping), the other 16
#                     UNTAGGABLE by provider schema in the dry run - of
#                     which -approve records 1 (module.ecs_cluster's
#                     time_sleep.this[0], record-backed since #340, seeded
#                     into the record store rather than skipped) and
#                     correctly skips 15; #305's default_* trio is admitted
#                     now and stamps cleanly - 2 VERIFIED + 1 DRIFTED, see
#                     below - and 0 failed. Two
#                     markers - the ECS cluster's and the ECS service's own
#                     - confirmed independently through the AWS CLI.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan`. #305 and
#                     #308 stay FIXED (0 sites each). #313's data-source
#                     root cause (A, 48 sites) and #315's each.value gap
#                     (C, 4 sites) are BOTH now fixed too - re-verified
#                     below by direct assertion, not by omission. BLOCKED
#                     for real by one remaining site: #313's own
#                     acknowledged resource-attribute-via-module-output
#                     scope (root cause B) and its 7-site cascade (8
#                     diagnostics total, was 236) until GitHub issue #368
#                     made that shape expressible. Those 8 are now 0,
#                     asserted by absence, and the identity #368 built is
#                     confirmed BY VALUE against the AWS CLI. Still
#                     BLOCKED, on two causes that are not identity gaps and
#                     that clearing #368 exposed for the first time - see
#                     "WHAT BLOCKS STAGE 3 NOW" below.
#   4. TEST APPLY    NOT RUN - depends on stage 3.
#   5. DRIFT/RECONVERGE  NOT RUN - depends on stages 3-4.
#
# #305 (admission: aws_default_network_acl/aws_default_route_table/
# aws_default_security_group were unadmitted) is FIXED. This module's VPC
# submodule adopts the account's default objects
# (manage_default_network_acl/route_table/security_group, all true by
# default), exactly as it does for most of its users. Re-verified against
# this estate below: all three now resolve through their own tofu-address
# marker and stamp in stage 2 (2 VERIFIED, 1 DRIFTED on genuine default-ACL
# drift - subnet_ids/egress/ingress, the same shape found in corpus-
# security-group-complete), and stage 3's live-plan contributes zero
# unadmitted-type refusals from them, asserted directly below rather than
# merely dropped from the old assertion.
#
# #308 IS FIXED (internal/live/identity/foreach_keyset.go's *hclsyntax.ForExpr
# case, a9ac6d06e7/b2bb59585d) - confirmed by direct assertion below (0
# "This module call cannot be expanded under live resource markers" sites,
# was 1). Fixing it did NOT unblock this estate; it let live-plan walk
# further into the configuration and reach #313's own wall (below) instead
# of stopping at #308's site first.
#
# #313 DOES REACH THIS ESTATE, but two of its three original root causes
# here are now FIXED. Re-verified against a real live-plan run below, not
# by omission:
#
#   A. FIXED (c636ab20f7, merged 0284d8c408). `data.aws_availability_zones.
#      available` feeding `local.azs` (main.tf:18, `slice(data.aws_
#      availability_zones.available.names, 0, 3)`), fed to `module "vpc"`
#      as `azs = local.azs` - the same shape filed as #313 crossing
#      corpus-security-group-complete. Was 48 root sites ("Dynamic value in
#      static context", "Unable to use data.aws_availability_zones.
#      available in static context, which is required by local.azs"), now
#      0: `resolver.frozenClosureIsStale` rebuilds a module instance's
#      `var.*` closure when a strictly-ancestral module instance carries
#      read-phase coverage, not only when a call on the path repeats. No
#      type or data-source names; the fix reaches every module-call
#      `data`-source chase in the shared resolver, not just this estate's
#      shape - see #313's own generalization measurement (57 of 204
#      `.corpus` entries with an eligible demanded source improved, this
#      estate's own offline diagnostic count among them, 0 worse).
#   B. FIXED (#368). `module.ecs_cluster.arn` passed into `module
#      "ecs_service"` as `cluster_arn = module.ecs_cluster.arn`
#      (main.tf:62) - a module output that is itself another resource's
#      attribute (the ECS cluster's ARN, Computed in the AWS provider's own
#      schema). The deferred READ of that attribute already worked before
#      #368, and this estate proves it: `aws_ecs_service.this[0]`'s own
#      identity has been `${...aws_ecs_cluster.this[0].arn}/ex-fargate` all
#      along. What did not work was the FUNCTION applied to it -
#      `local.cluster_name = try(element(split("/", var.cluster_arn), 1),
#      "")`. `identity.Formula` held literals and ParentRefs with no way to
#      say "split this parent attribute and take element 1", so the whole
#      chain refused. #368 gave [identity.ParentRef] a Transform: a
#      pipeline of pure functions (split, compact, element, index, sole)
#      applied to the live value AT RENDER TIME, declining to render at all
#      when the operation is undefined for the value it actually received.
#      Nothing predicts the shape of an ARN; the split runs on whatever the
#      cloud returned. Was 1 site ("Module output not supported in static
#      context"); now 0, asserted by absence below along with its cascade.
#
#   C. FIXED (#315, 772bde04d8, merged 1a3d46b767). `each.value.
#      enable_cloudwatch_logging` and `each.value.create_cloudwatch_log_group`
#      (modules/service/main.tf:923-924, inside `module
#      "container_definition"`, the same module call #308 fixed the keyset
#      resolution for) used to refuse because the walker treated the whole
#      `each.value` as one opaque dynamic blob. Was 4 sites (2 vars x 2
#      container_definitions keys); now 0 - `each.value` is projected down
#      to the one field each reference actually reads, the same way #308's
#      fix already did for the for_each expression itself.
#
# With A and C gone, B's cascade had collapsed from 177+6=183 sites to 7,
# and #368 then took all 8 to zero. The cascade was:
# `aws_appautoscaling_target.this[0]`'s own `resource_id` argument
# (modules/service/main.tf:1565) interpolates `local.cluster_name`, itself
# `element(split("/", var.cluster_arn), 1)` over B - 1 "Unable to compute
# static value" site. That failure then blocked the target resource's own
# identity, which `aws_appautoscaling_policy.this["cpu"]` and `["memory"]`
# each reference through three arguments (`resource_id`,
# `scalable_dimension`, `service_namespace`, main.tf:1589-1591) - 6
# "Unresolvable identity" cascade sites (2 policy instances x 3 arguments).
# 8 diagnostics total, from 236. Stage 3 below asserts all six diagnostic
# classes absent BY NAME before it reads live-plan's exit code, so a
# regression names its own root cause instead of arriving as "exited 1".
#
# WHY THE DRIFTED (18) BUCKET IS LARGE, AND WHY IT DOES NOT BLOCK STAGE 2:
# live-import tolerates drift by design (it stamps DRIFTED resources same
# as VERIFIED ones - drift is reported, not refused). Several of these 18
# are real floci round-trip gaps, found by this crossing and filed against
# the emulator, not against choudoufu: floci-io/floci (lex00 fork) #59
# (ECS CreateCluster silently drops `settings` - Container Insights never
# comes back on describe/list, though it was genuinely applied - and
# `aws_ecs_cluster_capacity_providers`' `default_capacity_provider_strategy`
# is stored but never serialized in any cluster response) and #60 (ECS
# CreateService/DescribeServices drop `scheduling_strategy`,
# `enable_ecs_managed_tags`, `enable_execute_command`,
# `health_check_grace_period_seconds`, `deployment_controller`, the
# blue-green `load_balancer.advanced_configuration`, and
# `service_connect_configuration` entirely - `scheduling_strategy` in
# particular forces the AWS provider to propose destroying and recreating
# the service on every plan after creation, independent of choudoufu).
# Neither blocks this script: DRIFTED still stamps, and neither field
# reaches any identity stage 3 compares - the fields floci drops are ECS
# service settings, not the cluster ARN or the scalable target's ResourceId.
#
# WHAT STAGE 3 PROVES NOW. Every identity diagnostic is gone: all six
# classes are asserted absent BY NAME before live-plan's exit code is read,
# so a regression names its own root cause instead of arriving as "exited
# 1". Because an empty plan alone is not enough - a wrong identity
# converges - the identity #368 made expressible is then compared BY VALUE
# against the AWS CLI: the scalable target's own ResourceId as the cloud
# reports it, against the string produced by splitting the live cluster ARN
# on "/" and taking element 1, which is exactly what `local.cluster_name`
# does. The cluster's and the service's tofu-address tags are re-read after
# the state file is deleted, so every answer can only have come from the
# live objects.
#
# #371, FIXED HERE, AND IT WAS THIS SCRIPT'S OWN DELTA. Until this pass the
# diff was "7 to add, 30 to change, 0 to destroy", and the seven additions
# were aws_ecs_cluster.this[0] and both aws_ecs_task_definition instances
# reading back ABSENT - "the provider reports no aws_ecs_cluster exists with
# identity \"ex-fargate\"" - over the same cluster
# `aws ecs describe-clusters --clusters ex-fargate` answers for two stages
# above, plus four PARENT_UNAVAILABLE cascades from the cluster. It was
# neither a choudoufu discovery bug nor a floci gap. It was DELTA 1's
# `skip_requesting_account_id = true`, the ordinary way to point the AWS
# provider at a local emulator, and the same knob #345 turned for
# corpus-overture-tiles.
#
# MEASURED, NOT ARGUED, and stock is the oracle for it. hashicorp/aws builds
# aws_ecs_cluster's READ lookup out of region + the account it knows about
# itself; with no account it issues DescribeClusters for
# `arn:aws:ecs:eu-west-1::cluster/ex-fargate` - an EMPTY account segment -
# and gets nothing back. Stock terraform fails identically on the same
# provider block, which is what settles it:
#
#   $ terraform import aws_ecs_cluster.x ex-fargate     # skip=true
#   aws_ecs_cluster.x: Refreshing state... [id=arn:aws:ecs:eu-west-1::cluster/ex-fargate]
#   Error: Cannot import non-existent remote object
#
#   $ terraform import aws_ecs_cluster.x ex-fargate     # skip=false
#   aws_ecs_cluster.x: Refreshing state... [id=arn:aws:ecs:eu-west-1:000000000000:cluster/ex-fargate]
#   Import successful!
#
# aws_ecs_task_definition reaches the same wall by the identity-object door
# rather than the ID-string one: the marker sweep hands the projection
# {family, revision, region, account_id} and account_id is "", so the
# provider composes the same account-less ARN. It is one root cause with two
# faces, not two bugs, and nothing in internal/live needed changing - the
# ARN the sweep read is right, the family and revision it split out of that
# ARN are right, and stage 3 now asserts all three by value against the CLI.
#
# BOTH COPIES set it false, not just the estate copy, and that second half
# was measured too. Setting it false on the estate copy alone takes migrate
# from 46 of 62 eligible to 41: the cold deploy writes its state file under
# skip=true, so five aws_vpc_security_group_{in,e}gress_rule instances carry
# a stored identity with account_id "", and reading them back under an
# account the provider now knows raises the provider framework's own
# "Unexpected Identity Change: Current Identity ... account_id "" ... New
# Identity ... account_id "000000000000"". That is two provider
# configurations disagreeing about the same object, not a marker fault, and
# the fix is to stop them disagreeing. With both copies false, stage 1 still
# applies 62 clean and stage 2 still stamps 46 of 62 - the only movement is
# one instance from DRIFTED to VERIFIED, for the same reason and in the
# better direction. This estate declares no S3 bucket, so the S3-Control
# account-prefixed virtual host that made #345 also move its ENDPOINT to
# localhost.floci.io is never reached here; ENDPOINT stays a bare IP.
#
# WHAT BLOCKS STAGE 3 NOW: "2 to add, 3 to change, 2 to destroy" (was 2/8/2
# before choudoufu #372's remainder landed here), and every line of it is
# either already tracked or is stock's own answer too.
#
#   1. NOTHING, as of #372's remainder. All 5 of the prior 8 in-place
#      changes that were one tag addition each, `tofu-slot = "0"`, on a
#      count-expanded instance, are gone - asserted by absence below (the
#      slot-only reader now finds 0 of 3, not 5 of 8). Before #372 landed at
#      all this was 25; its first landing fixed it outright for estates
#      built of server-assigned types (corpus-vpc-complete went 29 -> 1 on
#      it) and left client-named types unsettled here, because whether such
#      an instance wants a slot at all is a question about its own
#      declaration, and a migration reading a state file had no
#      configuration in hand to ask. The remainder gives it one:
#      Ratification.resolved (internal/live/liveimport/ratify.go) resolves
#      Request.Config once through identity.ResolveWith - the same function,
#      and for a table-admitted type the same ANSWER, a stateless live-plan's
#      own resolution would reach for the identical configuration - and
#      instanceNeedsDiscovery (slot.go) asks it per instance rather than
#      guessing from the type alone. The five here were the module's own IAM
#      roles (aws_iam_role.tasks[0], .task_exec[0] and
#      .infrastructure_iam_role[0] across the two service modules), each
#      named through a name_prefix the AWS API completes - now resolved
#      NEEDS_DISCOVERY from configuration and settled at migrate time exactly
#      as a server-assigned type already was. Writing one for an instance a
#      bare resolve cannot answer safely is still refused:
#      causeStableWithoutManagedResults (slot.go) excludes
#      DiscoverySiblingApply and DiscoveryMarkerFallback, the two causes a
#      REAL live-plan's own two-pass resolution can still change once a
#      sibling's live value is in hand - measured directly on THIS estate's
#      own aws_ecs_service.this[0], which resolves NEEDS_DISCOVERY/
#      MARKER_FALLBACK from a bare call (its "cluster" argument reads
#      module.ecs_cluster.arn through the split()/element() transform #368
#      made expressible, needing a real ARN to try it against) and
#      NEEDS_DISCOVERY was rejected there for exactly that reason - the tag
#      gate 4 would otherwise have written was the next plan's own
#      "- tofu-slot -> null", proven before this exclusion existed. The rule
#      names no type and reaches every client-named type whose per-instance
#      resolution independently agrees it needs discovery, not just IAM
#      roles.
#   2. The 2 adds and 2 destroys are one replacement each of
#      module.ecs_service.aws_ecs_task_definition.this[0] and
#      module.ecs_task_definition.aws_ecs_task_definition.this[0], forced by
#      container_definitions: floci does not echo back the container fields
#      the module sends (dependsOn, linuxParameters, restartPolicy,
#      versionConsistency and a dozen more - reproduced directly against the
#      AWS CLI, independent of Terraform: RegisterTaskDefinition's own
#      response and the following DescribeTaskDefinition both drop them).
#      STOCK FAILS TOO, and stage 1c records it: plain `terraform plan` on
#      the cold-deployed state file, before a single marker exists anywhere,
#      replaces the same two task definitions. Row 3 of HANDOFF.md's table.
#      UPDATE (round-9 repin, lex00/floci PR #130, issue #129): floci now
#      echoes the exact registered JSON, so module.ecs_service's own task
#      definition (no volume, default runtime platform) converges to a
#      genuine no-op - this item is now ONE replacement, not two
#      (module.ecs_task_definition alone), from two residual diffs
#      (essential: still row 3, real AWS also always echoes it; readOnly:
#      now a confirmed row-4 emulator gap, lex00/floci#131, fixed on branch
#      fix/ecs-mountpoints-readonly but not yet repinned) - see the top-of-
#      file UPDATE block and "STAGE 3 (test_plan)" below for the current
#      account.
#   3. The 3 in-place changes are emulator read fidelity, also present in
#      stage 1c's stock plan: aws_ecs_cluster's `configuration` block and
#      aws_ecs_service's `deployment_configuration`/
#      `service_connect_configuration` blocks are confirmed, independent of
#      Terraform, to never round-trip through a bare AWS CLI
#      Create+Describe cycle against this pinned image (lex00/floci#110);
#      aws_default_network_acl's egress/ingress rules read back with the
#      IPv6 rule missing both `cidr_block` and `ipv6_cidr_block` and every
#      rule gaining a spurious `icmp_code`/`icmp_type` of 0
#      (lex00/floci#111). Row 4 of HANDOFF.md's table: filed there, not
#      fixed here, per this estate's own account of how it verified them.
#   4. NOTHING. The two aws_cloudwatch_log_group instances inside for_each'd
#      module calls - module.ecs_service.module.container_definition
#      ["fluent-bit"] and module.ecs_task_definition.module.
#      container_definition["al2023"] - used to be here, with live-plan
#      proposing to REMOVE their own tofu-address and tofu-estate tags
#      rather than to add the tofu-slot tag their 25 siblings get. That was
#      issue #378, and it is FIXED: they are now absent from the plan
#      entirely, which is what "the desired tag set equals the live one"
#      looks like. Asserted by absence below, by the exact marker value, so
#      it cannot come back unnoticed.
#
#      That absence is worth more than "no diff": it is a by-value
#      cross-check between two INDEPENDENT producers of the same string,
#      through a real emulator. Stage 2's live-import wrote the tag from
#      identity resolution's own address for the instance; stage 3's plan
#      computes the desired tag from stamp's tofu.marker_module_prefix
#      template, evaluated by the ordinary plan-time evaluator. The plan
#      proposes no change only if those two agree byte for byte, and stage
#      2d already read the live side off the wire with the AWS CLI.
#
#      The fix is internal/live/stamp and nothing in this estate. The pass
#      injects markers into CONFIGURATION, so the plan's desired tags are
#      whatever the stamped configuration says, and there is no layer
#      between "the pass declined to stamp" and "the provider is handed a
#      desired tag set" that preserves a marker the pass did not write. A
#      resource under a module call with more than one instance was skipped,
#      because the call's instances share one HCL body for the resource's
#      tags argument and no expression writable in the child could name the
#      parent call's own instance key. There is one now:
#      tofu.marker_module_prefix (internal/live/markers, ModulePrefixAttr)
#      evaluates to the module INSTANCE's own escaped path, so stamp writes
#      a template instead of a literal and each instance renders its own
#      address. The rule names no type - it reaches all 847 taggable types
#      of hashicorp/aws 6.59.0 (live/survey-full.json) under any keyed
#      module call at any depth, and count'd calls as well as for_each'd
#      ones, since a module instance path carries an integer key exactly as
#      naturally as a string one. Pinned by value, with no cloud, in
#      internal/live/stamp/modulekeyed_prefix_test.go: two instances of one
#      for_each'd call, one shared tags body, two different and exactly
#      correct tag maps.
#
#      What stayed put, deliberately: a resource that already DECLARES
#      tofu-address by hand - the each.key-threaded-through-a-variable idiom
#      live/e2e/estate-module-keyed carries - is still trusted as written
#      and still not verified. That remainder is issue #379.
#
# BREAK=1 corrupts the expected ResourceId (it names a cluster that does
# not exist), proving that assertion is load-bearing rather than a
# comparison that always matches - same discipline as the RDS and
# security-group crossings before this one. It now also corrupts stage 2d's
# expected tofu-address for the fluent-bit log group, so #378's premise -
# that live-import really does stamp the two for_each'd-module log groups -
# is provably load-bearing too and not a comparison of two empty strings.
# Stage 1 is unaffected; stage 2 fails at 2d, before it reports its verdict.
#
#   bash live/e2e/corpus-ecs-fargate/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1, and .corpus populated (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4790, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt stage 2d's expected tofu-address for
#                the fluent-bit log group (#378) and stage 3's two
#                identity-by-value assertions (#368's scalable-target
#                ResourceId and #371's ECS cluster ARN) plus its expected
#                plan counts. Stage 2 fails first, so reach stage 3's
#                corruption by commenting the 2d block's BREAK branch out.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice - once for the cold, unmarked
# deploy, once for the migration attempt) and every delta lands on a copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/ecs"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4790}"
FLOCI_NAME="choudoufu-corpus-ecs-fargate-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="ecs-fargate-crossing"
REGION="eu-west-1"
INSTANCES=62
ELIGIBLE=46
SKIPPED=16
# SKIPPED is the DRY RUN's untaggable bucket, which -approve then splits in
# two (issue #340): module.ecs_cluster's time_sleep.this[0] is record-backed,
# so -approve seeds the record store for it and reports it RECORDED rather
# than SKIPPED. The dry run's own UNTAGGABLE count does not move -
# ratifyRecordBacked still answers StatusUntaggable - so only the -approve
# summary line splits.
RECORDED=1
APPROVE_SKIPPED=$((SKIPPED - RECORDED))
# 31/15, not the 30/16 this script asserted before #371: with the provider
# knowing its own account, the one ARN-bearing instance whose stored ARN used
# to carry an empty account segment now matches the live one on the nose. A
# resource moving from DRIFTED to VERIFIED is the direction of travel, and it
# is the same shape corpus-overture-tiles measured for #345 (there it moved
# the other way, for the same reason: the two provider configurations
# genuinely disagreed about an ARN).
VERIFIED_WANT=31
DRIFTED_WANT=15
UNTAGGABLE_WANT=16
UNADMITTED_WANT=0
FLUENTBIT_PARAM="/aws/service/aws-for-fluent-bit/stable"

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13),
# same pattern as corpus-sqs-basic's own: one namespace choudoufu applies
# into directly with no migration, and a separate namespace stock applies the
# identical corpus example into as that stage's own oracle. Offset by
# 1000/2000 rather than 1/2: a live run collided at FLOCI_PORT+20 with an
# unrelated concurrent session's own arbitrary port override for a
# different estate entirely (corpus-sqs-basic's main container, bound to
# 4925 - nothing to do with this estate's own assigned range), so a small
# offset is not safely reserved even within one estate's own triple. 1000/
# 2000 moves this estate's green/oracle pair into a range no other e2e
# script defaults into (every existing FLOCI_PORT default in live/e2e is
# under 4800) and keeps this estate's own [main, green, oracle] triple
# disjoint from a sibling estate's triple one port over.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1000))
FLOCI_GREEN_NAME="choudoufu-corpus-ecs-fargate-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2000))
FLOCI_ORACLE_NAME="choudoufu-corpus-ecs-fargate-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
GREEN_ESTATE="ecs-fargate-greenfield"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
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

# copy_tree DEST - the ecs module root plus examples/fargate, preserving the
# relative layout the example's `source = "../../modules/..."` needs.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/ecs/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/ecs/"
  cp -R "$SRC/examples/fargate" "$dest/ecs/examples/fargate"
  rm -rf "$dest/ecs/examples/fargate/.terraform" \
         "$dest/ecs/examples/fargate/.terraform.lock.hcl" \
         "$dest/ecs/examples/fargate/terraform.tfstate" \
         "$dest/ecs/examples/fargate/terraform.tfstate.backup"
}

# remove_task_definition_block EST - day2_remove's edit: delete the
# standalone module "ecs_task_definition" block (no service, a volume and an
# ARM64 runtime platform) from main.tf, plus the one output block in
# outputs.tf that interpolates its outputs into a run-task command string.
# Nothing else in the config references module.ecs_task_definition as a
# managed-resource attribute (create_service = false, and no other module
# call takes it as an input), so this needs no other edit.
remove_task_definition_block() {
  local est="$1"
  sed -i.bak '/^module "ecs_task_definition" {$/,/^}$/d' "$est/main.tf"
  sed -i.bak '/^output "task_definition_run_task_command" {$/,/^}$/d' "$est/outputs.tf"
  rm -f "$est/main.tf.bak" "$est/outputs.tf.bak"
  grep -q 'module "ecs_task_definition"' "$est/main.tf" \
    && fail "removing module.ecs_task_definition's block did not match in $est - the corpus pin has moved"
  grep -q 'task_definition_run_task_command' "$est/outputs.tf" \
    && fail "removing the task_definition_run_task_command output did not match in $est - the corpus pin has moved"
}

# apply_delta1 DEST SKIP_ACCOUNT_ID - DELTA 1, the onboarding delta: the
# emulator flags on the estate's one provider block.
#
# skip_requesting_account_id is parameterized rather than hard-coded, which
# is issue #371's whole fix and is the same knob #345 turned for
# corpus-overture-tiles. BOTH copies pass `false` here, and the parameter
# exists because getting there took two measurements, not one - see this
# script's header, "#371", for both.
apply_delta1() {
  local est="$1" skip_account_id="$2"
  perl -0pi -e "s/^(provider \"aws\" \{\n  region = local\.region\n)\}/\$1  access_key                  = \"test\" # DELTA 1\n  secret_key                  = \"test\"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = $skip_account_id\n  s3_use_path_style           = true\n}/" "$est/main.tf"
  grep -q 'DELTA 1' "$est/main.tf" || fail "DELTA 1 did not match the provider block in $est - the corpus pin has moved"
  grep -q "skip_requesting_account_id  = $skip_account_id" "$est/main.tf" \
    || fail "DELTA 1 did not write skip_requesting_account_id = $skip_account_id into $est"
}

gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
[ -d "$SRC/examples/fargate" ] || fail "$SRC/examples/fargate is missing - run 'just corpus-fetch' first"

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
PLAIN_EST="$PLAIN/ecs/examples/fargate"
log "  estate copied out of .corpus into $PLAIN_EST"

CURRENT_STAGE=cold_deploy
# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, 62 real resources ==="

# DELTA 1, onboarding: emulator flags on the estate's one provider block,
# with skip_requesting_account_id = false (#371 - see the header).
apply_delta1 "$PLAIN_EST" false
log "  DELTA 1  emulator flags on the provider block             (onboarding)"
log "           skip_requesting_account_id = false (#371)"

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"ecs"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"ecs"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (ecs) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# EMULATOR-GAP WORKAROUND (not a config edit): floci does not mirror AWS's
# own published SSM parameter catalog. Seed the one real, AWS-published
# parameter this estate reads, with the real value AWS itself serves there,
# so data.aws_ssm_parameter.fluentbit resolves exactly as it would on real
# AWS - see this script's header.
awsl ssm put-parameter --name "$FLUENTBIT_PARAM" --type String \
  --value "public.ecr.aws/aws-observability/aws-for-fluent-bit:stable" >/dev/null \
  || fail "could not seed $FLUENTBIT_PARAM through the AWS CLI"
log "  seeded $FLUENTBIT_PARAM                       (EMULATOR GAP: floci has no public SSM parameter catalog)"

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
log "  real terraform.tfstate, zero choudoufu markers - ECS cluster + service"
log "  behind an ALB with blue/green + Service Connect, a standalone task"
log "  definition, a CloudMap namespace, an ALB module, a VPC module"

# Confirmed unmarked: read the cluster's tags directly through the AWS CLI,
# never through choudoufu.
CLUSTER_ARN="$(awsl ecs describe-clusters --clusters ex-fargate --query 'clusters[0].clusterArn' --output text)"
[ -n "$CLUSTER_ARN" ] && [ "$CLUSTER_ARN" != "None" ] || fail "could not find the ex-fargate ECS cluster through the AWS CLI"
MARKER_COUNT="$(awsl ecs list-tags-for-resource --resource-arn "$CLUSTER_ARN" --query "length(tags[?key=='tofu-address'])" --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the ECS cluster already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $CLUSTER_ARN carries no tofu-address tag"

# ── 1c. the stock oracle for stage 3, taken before anything is stamped ────
#
# live/GAUNTLET.md's stage 3 names stock's own plan as the oracle: "Stock
# plan on the migrated state is also empty". Here it is not, and it has to
# be read HERE rather than after stage 2, because once live-import has
# stamped 46 objects a stock plan wants to strip every marker off them and
# says nothing useful about anything else. This is plain terraform, on the
# state file it wrote itself, one minute after writing it, against a cloud
# nothing has touched.
#
# Round-9 repin (lex00/floci PR #130, issue #129): ECS ContainerDefinition/
# TaskDefinition now echo the exact registered JSON, so module.ecs_service's
# own task definition (no volume, default runtime platform) converges to a
# genuine no-op in stock's own replan too.
#
# Round-10 repin (lex00/floci#131, fix/ecs-mountpoints-readonly, published
# and repinned in this same unit): module.ecs_task_definition's (the
# standalone one, volume block + ARM64 runtime platform) own
# mountPoints[].readOnly diff is gone too - real AWS leaves readOnly
# entirely absent when the caller never sets it, and floci now matches.
# Confirmed directly against the real published digest, no tofu in the
# loop: a bare register-task-definition call with neither `essential` nor
# `readOnly` set still echoes `essential: true` (unchanged) but now leaves
# `mountPoints[].readOnly` absent.
#
# The `essential` diff this estate's own header long described as a
# SEPARATE, independent HANDOFF row-3 wall turned out not to be one on its
# own: with mountPoints[].readOnly fixed, a fresh stock cold-deploy's own
# replan is now genuinely EMPTY - "No changes. Your infrastructure matches
# the configuration." - confirmed directly, terraform plan immediately
# after terraform apply, no choudoufu anywhere in the loop. floci and real
# AWS do still always echo `essential: true` on read regardless of what
# config sent; what was wrong was the inference that this ALONE forces a
# replacement. terraform-provider-aws's own container_definitions
# equivalence check tolerates `essential` defaulting to true when config
# omits it, and mountPoints[].readOnly was the only diff ever independently
# forcing this estate's replacement. That correction is this unit's own
# re-measurement against the round-10 pin, not an assumption carried
# forward from round 9.
STOCK_REPLAN="$(cd "$PLAIN_EST" && terraform plan -input=false -no-color 2>&1)"
STOCK_REPLAN_RC=$?
[ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -30; fail "the stock oracle replan exited $STOCK_REPLAN_RC"; }
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$STOCK_REPLAN" \
  || { printf '%s\n' "$STOCK_REPLAN" | grep -E '^Plan:|^  # '; fail "stock's own replan is no longer empty - the round-10 mountPoints[].readOnly fix (or the essential non-wall finding) has regressed, or a new diff appeared"; }
log "  stock oracle: plain terraform replanning its own fresh state IS now"
log "  genuinely empty - \"No changes. Your infrastructure matches the"
log "  configuration.\" - round-9 converged module.ecs_service's own task"
log "  definition, round-10 (lex00/floci#131) converged the standalone"
log "  module.ecs_task_definition's mountPoints[].readOnly, and essential"
log "  defaulting to true was never an independent wall on its own -"
log "  terraform-provider-aws's own equivalence check tolerates it."

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "$INSTANCES resources, once for real"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13) - same pattern as
# corpus-sqs-basic's own: two MORE, fresh floci containers, neither reusing
# a single object stage 1's plain terraform apply created. choudoufu applies
# the identical, unreduced corpus example directly with a live block from
# the start, no migration, no state file ever existing; the local record
# store must hold one record per instance, including the 16 untaggable ones
# (#364 A2 - apply writes a record too, not just live-import); and the
# estate's own oracle is stock applying the SAME config fresh in a THIRD,
# independent namespace, compared structurally via the AWS CLI on both
# endpoints, never through tofu state, never through choudoufu's own report.
CURRENT_STAGE=greenfield
log "=== G0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"ecs"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"ecs"' <<< "${GH:-}" || fail "floci did not come up healthy (ecs) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }
awsg ssm put-parameter --name "$FLUENTBIT_PARAM" --type String \
  --value "public.ecr.aws/aws-observability/aws-for-fluent-bit:stable" >/dev/null \
  || fail "could not seed $FLUENTBIT_PARAM on the greenfield endpoint"
awso ssm put-parameter --name "$FLUENTBIT_PARAM" --type String \
  --value "public.ecr.aws/aws-observability/aws-for-fluent-bit:stable" >/dev/null \
  || fail "could not seed $FLUENTBIT_PARAM on the greenfield oracle endpoint"
log "  seeded $FLUENTBIT_PARAM on both new endpoints (EMULATOR GAP: floci has no public SSM parameter catalog)"

GREEN="$WORK/green"
copy_tree "$GREEN"
GREEN_EST="$GREEN/ecs/examples/fargate"
apply_delta1 "$GREEN_EST" false
# strict { no_source_create = "create" }: found necessary re-verifying this
# stage after main's CHOUDOUFU_NODE_RESOLVE default flip (845e7a0d9d,
# 2026-08-25) - a genuinely cold apply now refuses config-identified
# instances whose identity value belongs to a sibling that does not exist
# yet either (#365 ruling 4's default refusal of that ambiguity), and a
# greenfield apply is the one case an operator KNOWS it is a real create.
# Same fix, same precedent as corpus-alb-complete's own 898091b8f2.
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \">= 6\.41\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$GREEN_ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n\n    strict {\n      no_source_create = \"create\"\n    }\n  }\n}/" "$GREEN_EST/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"

log "=== G1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -60; fail "the greenfield apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly $INSTANCES resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

log "=== G2. the cluster's marker, read through the AWS CLI directly ==="
GREEN_CLUSTER_ARN="$(awsg ecs describe-clusters --clusters ex-fargate --query 'clusters[0].clusterArn' --output text)"
[ -n "$GREEN_CLUSTER_ARN" ] && [ "$GREEN_CLUSTER_ARN" != "None" ] || fail "could not find the ex-fargate ECS cluster on the greenfield endpoint"
GREEN_CLUSTER_ADDR="$(awsg ecs list-tags-for-resource --resource-arn "$GREEN_CLUSTER_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GREEN_CLUSTER_ADDR" = "module.ecs_cluster.aws_ecs_cluster.this:0" ] || fail "the greenfield cluster carries tofu-address=$GREEN_CLUSTER_ADDR, not module.ecs_cluster.aws_ecs_cluster.this:0"
GREEN_CLUSTER_ESTATE="$(awsg ecs list-tags-for-resource --resource-arn "$GREEN_CLUSTER_ARN" --query "tags[?key=='tofu-estate'].value | [0]" --output text)"
[ "$GREEN_CLUSTER_ESTATE" = "$GREEN_ESTATE" ] || fail "the greenfield cluster carries tofu-estate=$GREEN_CLUSTER_ESTATE, not $GREEN_ESTATE"
log "  $GREEN_CLUSTER_ARN carries tofu-address=$GREEN_CLUSTER_ADDR tofu-estate=$GREEN_CLUSTER_ESTATE - read via the AWS CLI, not choudoufu's own report"

log "=== G3. the record store holds every instance the current record-writer can (#364 A2) ==="
# GREEN_RECORD_WANT is $INSTANCES minus 2, not $INSTANCES: both
# aws_ecs_task_definition instances (module.ecs_service's and the standalone
# module.ecs_task_definition's) are genuinely absent from the record store,
# confirmed by decoding every record filename present and diffing it against
# this estate's own managed-resource address list - nothing else is missing.
# Root cause, read directly in the source rather than guessed: the AWS
# provider's own wire identity schema for aws_ecs_task_definition types its
# "revision" component as a NUMBER, and
# internal/live/identity/located.go's LocatedIdentityPlanFor (the function
# RecordableIdentitySchema and the composite-identity record writer both
# call) refuses the WHOLE identity plan the moment any required wire-identity
# component is not schema-typed cty.String ("a.Type != cty.String" at that
# function's for-loop) - a strict, generic rule keyed on the provider
# schema's own attribute type, not on this type's name, so it reaches every
# type whose identity schema carries a numeric required component, not only
# this one. Filed as a follow-up rather than fixed in this unit: the same
# render-a-number-to-its-decimal-string path locatedAttrSegment already uses
# for a composed import ID's numeric segments is not yet wired into
# LocatedIdentity's composite-Components branch. The marker itself is
# unaffected - both instances plan and reconcile correctly through
# migrate/test_plan/test_apply/drift_reconverge/day2_rename elsewhere in
# this same script - only the LOCAL RECORD (an extra, record-rung-only
# recovery path #364 A2 also populates for taggable types) is missing for
# these two.
GREEN_RECORD_WANT=$((INSTANCES - 2))
GREEN_RECORD_FILES="$(find "$GREEN_EST/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" = "$GREEN_RECORD_WANT" ] || fail "expected $GREEN_RECORD_WANT records under the local record store after the greenfield apply ($INSTANCES managed instances minus the 2 aws_ecs_task_definition instances the numeric-identity-component gap above excludes), found $GREEN_RECORD_FILES"
log "  $GREEN_RECORD_FILES of $INSTANCES records persisted (the 2 aws_ecs_task_definition instances excluded by the numeric-identity-component gap documented above), read directly off the local record store"

log "=== G4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== G5. stock oracle - the identical corpus example applied fresh in its own namespace ==="
ORACLE="$WORK/green-oracle"
copy_tree "$ORACLE"
ORACLE_EST="$ORACLE/ecs/examples/fargate"
apply_delta1 "$ORACLE_EST" false
( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -60; fail "the greenfield oracle apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly $INSTANCES resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

log "=== G6. object-by-object comparison, via the AWS CLI on both endpoints, marker tags never compared ==="
ecs_fargate_shape() { # $1 = endpoint - a normalised structural fact sheet,
                       # read via the AWS CLI, never through tofu state.
  local ep="$1"
  aws --endpoint-url "$ep" --region "$REGION" ecs describe-clusters --clusters ex-fargate \
    --query "clusters[0].[status,length(capacityProviders)]" --output text 2>/dev/null \
    | awk '{print "cluster status="$1" capacity_providers="$2}'
  aws --endpoint-url "$ep" --region "$REGION" ecs describe-services --cluster ex-fargate --services ex-fargate \
    --query "services[0].[status,desiredCount,launchType,schedulingStrategy]" --output text 2>/dev/null \
    | awk '{print "service status="$1" desired="$2" launch="$3" strategy="$4}'
  aws --endpoint-url "$ep" --region "$REGION" ecs list-task-definitions --family-prefix ex-fargate-standalone --status ACTIVE \
    --query "length(taskDefinitionArns)" --output text 2>/dev/null | sed 's/^/standalone_td_active_revisions=/'
  TD_ARN="$(aws --endpoint-url "$ep" --region "$REGION" ecs list-task-definitions --family-prefix ex-fargate-standalone --status ACTIVE --query 'taskDefinitionArns[0]' --output text 2>/dev/null)"
  aws --endpoint-url "$ep" --region "$REGION" ecs describe-task-definition --task-definition "$TD_ARN" \
    --query "taskDefinition.[cpu,memory,runtimePlatform.cpuArchitecture]" --output text 2>/dev/null \
    | awk '{print "standalone_td cpu="$1" memory="$2" arch="$3}'
  aws --endpoint-url "$ep" --region "$REGION" servicediscovery list-namespaces \
    --query "length(Namespaces[?Type=='HTTP'])" --output text 2>/dev/null | sed 's/^/http_namespaces=/'
  ALB_ARN="$(aws --endpoint-url "$ep" --region "$REGION" elbv2 describe-load-balancers --query 'LoadBalancers[0].LoadBalancerArn' --output text 2>/dev/null)"
  aws --endpoint-url "$ep" --region "$REGION" elbv2 describe-load-balancers \
    --query "LoadBalancers[0].[Type,Scheme]" --output text 2>/dev/null | awk '{print "alb type="$1" scheme="$2}'
  aws --endpoint-url "$ep" --region "$REGION" elbv2 describe-listeners --load-balancer-arn "$ALB_ARN" \
    --query "length(Listeners)" --output text 2>/dev/null | sed 's/^/alb_listeners=/'
  aws --endpoint-url "$ep" --region "$REGION" elbv2 describe-target-groups \
    --query "length(TargetGroups[?LoadBalancerArns[0]=='$ALB_ARN'])" --output text 2>/dev/null | sed 's/^/alb_target_groups=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-vpcs \
    --filters "Name=tag:Name,Values=ex-fargate*" \
    --query "Vpcs[0].CidrBlock" --output text 2>/dev/null | sed 's/^/vpc_cidr=/'
}
GREEN_SHAPE="$(ecs_fargate_shape "$GREEN_ENDPOINT" | sort)"
ORACLE_SHAPE="$(ecs_fargate_shape "$ORACLE_ENDPOINT" | sort)"
if [ "$GREEN_SHAPE" != "$ORACLE_SHAPE" ]; then
  diff <(printf '%s\n' "$GREEN_SHAPE") <(printf '%s\n' "$ORACLE_SHAPE") || true
  fail "the greenfield estate's object inventory does not match stock's cold deploy, object by object, in its own namespace"
fi
log "  object-by-object match: cluster status/capacity-providers, service status/desired-count/launch-type/strategy, standalone task definition's active-revision-count/cpu/memory/architecture, the CloudMap HTTP namespace count, the ALB's type/scheme/listener-count/target-group-count, and the VPC's cidr - identical between the greenfield estate and stock's cold deploy in its own namespace, marker tags never part of the comparison"

gauntlet_stage greenfield pass "$INSTANCES resources from nothing, cluster marker verified via the AWS CLI, $GREEN_RECORD_FILES of $INSTANCES records in the local record store (#364 A2; the 2 aws_ecs_task_definition instances are excluded by a numeric-wire-identity-component gap in internal/live/identity/located.go's LocatedIdentityPlanFor, documented in this script and not fixed here - their markers and plans are unaffected), replan empty, stock oracle in its own namespace matches structurally on cluster/service/standalone-task-definition/CloudMap-namespace/ALB/VPC"
CURRENT_STAGE=""

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The adopted estate (later stages) is still marked and still converged,
# which is exactly the state a rename needs to start from. Two mechanisms,
# on two different objects so a gap in either is visible: a `moved` block
# renames the whole module.alb call, and "choudoufu live-mv" renames the
# standalone root aws_service_discovery_http_namespace.this with no moved
# block at all. The stock oracle for both runs on a copy of cold_deploy's
# own state, PLANNED right after stage 1 (this block sits between
# "gauntlet_stage cold_deploy pass" and migrate) - before choudoufu or
# live-import ever touch these shared objects, since live-import stamps the
# SAME AWS objects $PLAIN_EST's cold apply created rather than creating a
# parallel set, and replanning against them later would compare a plan that
# legitimately wants the marker tags gone, which has nothing to do with the
# rename.
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming aws_service_discovery_http_namespace.this WITHOUT a
# moved block, which must make choudoufu propose destroying the old address
# and creating the new one - the opposite of every other assertion in this
# part.

CURRENT_STAGE=day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE_ROOT="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE_ROOT"
PLAIN_ORACLE="$PLAIN_ORACLE_ROOT/ecs/examples/fargate"
sed -i.bak 's/module "alb" {/module "alb_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.alb\./module.alb_renamed./g' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.alb\./module.alb_renamed./g' "$PLAIN_ORACLE/outputs.tf"
sed -i.bak 's/resource "aws_service_discovery_http_namespace" "this" {/resource "aws_service_discovery_http_namespace" "this_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/aws_service_discovery_http_namespace\.this\.arn/aws_service_discovery_http_namespace.this_renamed.arn/' "$PLAIN_ORACLE/main.tf"
rm -f "$PLAIN_ORACLE/main.tf.bak" "$PLAIN_ORACLE/outputs.tf.bak"
cat >> "$PLAIN_ORACLE/main.tf" <<'EOF'

moved {
  from = module.alb
  to   = module.alb_renamed
}

moved {
  from = aws_service_discovery_http_namespace.this
  to   = aws_service_discovery_http_namespace.this_renamed
}
EOF
( cd "$PLAIN_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7): "Stock with the same
# block removed plans the same destroys in a working order." Same principle
# as the rename oracle above - a SEPARATE copy of cold_deploy's own state,
# unrenamed, so this removal has nothing to do with the rename this script
# also exercises.
CURRENT_STAGE=day2_remove
log "=== D-ORACLE (day2_remove). stock: delete module.ecs_task_definition's block on cold_deploy's own state ==="
PLAIN_ORACLE_REMOVE_ROOT="$WORK/plain-oracle-remove"
cp -r "$PLAIN" "$PLAIN_ORACLE_REMOVE_ROOT"
PLAIN_ORACLE_REMOVE="$PLAIN_ORACLE_REMOVE_ROOT/ecs/examples/fargate"
remove_task_definition_block "$PLAIN_ORACLE_REMOVE"
( cd "$PLAIN_ORACLE_REMOVE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REMOVE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REMOVE" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
REMOVE_ORACLE_DESTROY_ADDRS="$(grep -oE '^  # \S+ will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" | awk '{print $2}' | sort -u)"
REMOVE_ORACLE_N="$(printf '%s\n' "$REMOVE_ORACLE_DESTROY_ADDRS" | grep -c . || true)"
grep -qE '^  # .+ will be (created|updated)' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's day2_remove oracle proposes a create or update, not a pure removal"; }
grep -qF "Plan: 0 to add, 0 to change, $REMOVE_ORACLE_N to destroy." <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's day2_remove oracle plan summary does not match its own $REMOVE_ORACLE_N destroys"; }
printf '%s\n' "$REMOVE_ORACLE_DESTROY_ADDRS" | grep -qE '^module\.ecs_task_definition\.' \
  || { printf '%s\n' "$REMOVE_ORACLE_DESTROY_ADDRS"; fail "stock's day2_remove oracle destroys do not include anything under module.ecs_task_definition"; }
printf '%s\n' "$REMOVE_ORACLE_DESTROY_ADDRS" | grep -qvE '^module\.ecs_task_definition\.' \
  && { printf '%s\n' "$REMOVE_ORACLE_DESTROY_ADDRS"; fail "stock's day2_remove oracle destroys something outside module.ecs_task_definition"; }
log "  stock: $REMOVE_ORACLE_N destroys under module.ecs_task_definition on cold_deploy's own state, nothing else"
CURRENT_STAGE=""

# day2_replace's stock oracle (live/GAUNTLET.md #9, active): "Stock's
# replace of the same resource leaves the same single object." A THIRD
# separate copy of cold_deploy's own state, unrenamed, so this replace has
# nothing to do with either the rename or the remove this script also
# exercises. Changes module.ecs_task_definition's own `name` CALL
# argument (not vendored module-internal source - always in scope to
# edit) to a different literal - the local "../../modules/service"
# module sets `family = coalesce(var.family, var.name)` for its own aws_
# ecs_task_definition.this[0], so this changes the task definition's
# `family` argument, a real, upstream-declared ForceNew argument on aws_
# ecs_task_definition (ECS has no rename call for a task definition
# family, only RegisterTaskDefinition/DeregisterTaskDefinition) -
# forcing stock to replace the SAME declared address.
#
# NOTE ON THE TARGET CHOICE: this section tried TWO other targets first,
# each reproducing a genuine, separate finding - recorded here rather
# than routed around silently:
#   1. aws_service_discovery_http_namespace.this_renamed (Part D's
#      live-mv leg): after "choudoufu live-mv" renames a BARE, non-
#      module-nested resource (no module boundary crossed) with no
#      ordinary apply run afterward, the live MARKER is correctly
#      rewritten (day2_rename's own Proves text, unaffected) but the
#      LOCAL RECORD was left stale at the OLD key. Root cause, read
#      directly off mv.go with no tofu in the loop: internal/live/mv/
#      mv.go's propagateModuleRename (called from Move after the marker
#      rewrite) opened with `oldPrefix, newPrefix, ok :=
#      moduleRenameBoundary(...); if !ok { return diags }` - for a
#      same-module, bare-resource rename this check was never satisfied,
#      so the function returned immediately and never reached the
#      MoveRecord call its own doc comment says covers "the resource
#      live-mv was asked to rename itself". FIXED on the gauntlet/mv-rekey
#      branch, GitHub issue #412: propagateModuleRename now calls
#      store.MoveRecord(ctx, m.req.Old, m.req.New) unconditionally, before
#      the moduleRenameBoundary guard, so a same-module bare-resource
#      rename re-keys its own record instead of leaving the store
#      pointing at a dead address. corpus-autoscaling-complete's and
#      corpus-eks-basic's own day2_replace sections in this same unit
#      independently hit the identical shape; this script was not re-run
#      for #412, so this comment and the day2_replace detail string below
#      still describe the pre-#412 code until this estate's next real run.
#   2. module.alb_renamed's own load balancer (Part D1's moved-block-
#      plus-apply leg, which dodges finding 1 above): the LB's own
#      replace applies cleanly (identity, marker, record all correct,
#      confirmed via the AWS CLI and the record store directly), but the
#      cascading update to module.ecs_service.aws_ecs_service.this[0]
#      (its load_balancer.advanced_configuration listener-rule ARNs) and
#      to aws_vpc_security_group_ingress_rule.this["alb_3000"] (its
#      referenced_security_group_id) never converges - two consecutive
#      post-apply plans each show the SAME two resources proposing an
#      update again, with the ingress rule's own referenced_security_
#      group_id a DIFFERENT literal value each time and no intervening
#      config change. An oscillation, not a settling multi-round
#      convergence; most consistent with row 4 of HANDOFF's five-row
#      table (the emulator reporting a changing identity for the same
#      logical security group on every read), since choudoufu's plan
#      only reflects what floci's own API returns for that field.
# module.ecs_task_definition dodges BOTH: it is untouched by Part D's own
# two renames (so finding 1 never applies), and per the corpus's own
# header comment ("no service" - create_service=false) nothing else in
# this estate references it at all, so there is no cascade for finding
# 2's shape to reach either.
CURRENT_STAGE=day2_replace
log "=== F-ORACLE. stock: force-replace module.ecs_task_definition's task definition via its ForceNew family argument, on cold_deploy's own state ==="
PLAIN_ORACLE_REPLACE_ROOT="$WORK/plain-oracle-replace"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE_ROOT"
PLAIN_ORACLE_REPLACE="$PLAIN_ORACLE_REPLACE_ROOT/ecs/examples/fargate"
perl -0pi -e 's/module "ecs_task_definition" \{\n  source = "\.\.\/\.\.\/modules\/service"\n\n  # Service\n  name           = "\$\{local\.name\}-standalone"\n/module "ecs_task_definition" {\n  source = "..\/..\/modules\/service"\n\n  # Service\n  name           = "\${local.name}-standalone-v2"\n/' "$PLAIN_ORACLE_REPLACE/main.tf"
grep -q '${local.name}-standalone-v2' "$PLAIN_ORACLE_REPLACE/main.tf" \
  || fail "changing module.ecs_task_definition's name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$PLAIN_ORACLE_REPLACE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REPLACE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.ecs_task_definition\.aws_ecs_task_definition\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing module.ecs_task_definition's task definition when its family argument changes"; }
REPLACE_ORACLE_PLAN_LINE="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.' <<< "$REPLACE_ORACLE_PLAN_OUT")"
[ -n "$REPLACE_ORACLE_PLAN_LINE" ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -15; fail "the day2_replace stock oracle plan has no summary line"; }
log "  stock: $REPLACE_ORACLE_PLAN_LINE - replaces module.ecs_task_definition's task definition at the same declared address, on the state cold_deploy produced - plan only, not applied (this copy shares floci's account with \$ADOPTED_EST, and actually applying here would destroy the real task definition the estate's later stages still depend on)"
CURRENT_STAGE=""

CURRENT_STAGE=migrate

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/ecs/examples/fargate"
# Carry the same emulator delta so the adopted config is otherwise identical
# to what is actually standing - with skip_requesting_account_id = false on
# THIS copy alone (#371; the same shape #345 landed for
# corpus-overture-tiles). Nothing else about the configuration differs.
apply_delta1 "$ADOPTED_EST" false
log "  DELTA 1  emulator flags, skip_requesting_account_id = false (#371)"

# DELTA 2, onboarding: add the live block. record_store is needed for
# module.ecs_cluster's time_sleep.this[0] (an effects-only logical
# resource - see the record-store fixture).
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \">= 6\.41\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 2 did not match versions.tf - the corpus pin has moved"
log "  DELTA 2  live block + local record_store added             (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - this estate's resource shape or floci's own round-trip behavior may have moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "$VERIFIED_WANT" ] || fail "expected $VERIFIED_WANT VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "$DRIFTED_WANT" ] || fail "expected $DRIFTED_WANT DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "$UNTAGGABLE_WANT" ] || fail "expected $UNTAGGABLE_WANT UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "$UNADMITTED_WANT" ] || fail "expected $UNADMITTED_WANT UNADMITTED_TYPE (#305 is fixed - none expected), got ${UNADMITTED_N:-0}"
# #305 fixed: the vpc submodule's three default-object adopters now resolve
# and stamp like any other server-assigned type (2 VERIFIED, 1 DRIFTED on
# genuine default-ACL drift), not skipped as UNADMITTED_TYPE.
grep -qF 'module.vpc.aws_default_route_table.default[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_route_table.default[0] among VERIFIED (#305 fixed)"
grep -qF 'module.vpc.aws_default_security_group.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_security_group.this[0] among VERIFIED (#305 fixed)"
grep -qF 'module.vpc.aws_default_network_acl.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_network_acl.this[0] among DRIFTED (#305 fixed)"
log "  $ELIGIBLE of $INSTANCES eligible ($VERIFIED_WANT VERIFIED + $DRIFTED_WANT DRIFTED); $SKIPPED skipped"
log "  ($UNTAGGABLE_WANT UNTAGGABLE by provider schema; #305's default_* trio is"
log "  admitted now and stamped above); nothing written yet"

log "=== 2b. -approve: stamp the $ELIGIBLE eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$ELIGIBLE resource(s) newly stamped, 0 already stamped, $RECORDED newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $APPROVE_SKIPPED skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly $ELIGIBLE of $INSTANCES resources cleanly ($RECORDED recorded, $APPROVE_SKIPPED skipped)"; }
log "  $ELIGIBLE stamped, $RECORDED recorded (time_sleep.this[0]), 0 failed,"
log "  $APPROVE_SKIPPED skipped - $SKIPPED untaggable in the dry run, one of them record-backed"

log "=== 2c. the cluster's and the service's own markers, read through the AWS CLI directly ==="
WANT_CLUSTER_ADDR="module.ecs_cluster.aws_ecs_cluster.this:0"
GOT_CLUSTER_ADDR="$(awsl ecs list-tags-for-resource --resource-arn "$CLUSTER_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GOT_CLUSTER_ADDR" = "$WANT_CLUSTER_ADDR" ] || fail "the ECS cluster carries tofu-address=$GOT_CLUSTER_ADDR, not $WANT_CLUSTER_ADDR"
log "  $CLUSTER_ARN now carries tofu-address=$GOT_CLUSTER_ADDR"

SVC_ARN="$(awsl ecs describe-services --cluster ex-fargate --services ex-fargate --query 'services[0].serviceArn' --output text)"
[ -n "$SVC_ARN" ] && [ "$SVC_ARN" != "None" ] || fail "could not find the ex-fargate ECS service through the AWS CLI"
WANT_SVC_ADDR="module.ecs_service.aws_ecs_service.this:0"
GOT_SVC_ADDR="$(awsl ecs list-tags-for-resource --resource-arn "$SVC_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GOT_SVC_ADDR" = "$WANT_SVC_ADDR" ] || fail "the ECS service carries tofu-address=$GOT_SVC_ADDR, not $WANT_SVC_ADDR"
log "  $SVC_ARN now carries tofu-address=$GOT_SVC_ADDR"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

# The two log groups inside for_each'd module calls, BY VALUE and through
# the CLI. Issue #378's whole premise is that live-import really did stamp
# these and stage 3's plan really does propose to delete what it wrote, and
# until this block existed only the first half was prose: the script asserted
# the deletion in stage 3 and merely claimed the stamping in its header. Both
# halves are now load-bearing, and if live-import ever stops stamping them
# this fails here rather than turning #378's stage-3 assertion into a
# comparison that always matches.
log "=== 2d. the two for_each'd-module log groups' markers, read through the AWS CLI (#378) ==="
LOG_GROUP_FLUENTBIT="/aws/ecs/ex-fargate/fluent-bit"
LOG_GROUP_AL2023="/aws/ecs/ex-fargate-standalone/al2023"
WANT_LG_FLUENTBIT_ADDR="module.ecs_service.module.container_definition:fluent-bit.aws_cloudwatch_log_group.this:0"
WANT_LG_AL2023_ADDR="module.ecs_task_definition.module.container_definition:al2023.aws_cloudwatch_log_group.this:0"
if [ "${BREAK:-}" = "1" ]; then
  WANT_LG_FLUENTBIT_ADDR="module.ecs_service.module.container_definition:not-a-container.aws_cloudwatch_log_group.this:0"
  log "  BREAK=1: expecting the fluent-bit log group's tofu-address to name a"
  log "           container that does not exist. It does not. This step must fail."
fi
log_group_tag() {
  local name="$1" key="$2" arn
  arn="$(awsl logs describe-log-groups --log-group-name-prefix "$name" \
    --query "logGroups[?logGroupName=='${name}'].arn | [0]" --output text)"
  [ -n "$arn" ] && [ "$arn" != "None" ] || { printf 'NO_SUCH_LOG_GROUP\n'; return; }
  awsl logs list-tags-for-resource --resource-arn "${arn%:\*}" \
    --query "tags.\"${key}\"" --output text
}
for lg in "$LOG_GROUP_FLUENTBIT:$WANT_LG_FLUENTBIT_ADDR" "$LOG_GROUP_AL2023:$WANT_LG_AL2023_ADDR"; do
  LG_NAME="${lg%%:*}"
  LG_WANT="${lg#*:}"
  LG_GOT_ADDR="$(log_group_tag "$LG_NAME" tofu-address)"
  [ "$LG_GOT_ADDR" = "$LG_WANT" ] \
    || fail "$LG_NAME carries tofu-address=$LG_GOT_ADDR, not $LG_WANT - #378's premise is that live-import stamps these correctly and only the replan drops them; if live-import stopped stamping them, that is a different (and larger) defect"
  LG_GOT_ESTATE="$(log_group_tag "$LG_NAME" tofu-estate)"
  [ "$LG_GOT_ESTATE" = "$ESTATE" ] \
    || fail "$LG_NAME carries tofu-estate=$LG_GOT_ESTATE, not $ESTATE"
  log "  $LG_NAME carries tofu-address=$LG_GOT_ADDR, tofu-estate=$LG_GOT_ESTATE"
done
log "  both markers are correct on the wire; #378 is the REPLAN dropping them,"
log "  not live-import failing to write them"

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
log "  only)"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
# Flatten choudoufu's wrapped prose to one line per paragraph, same
# discipline as the RDS and security-group crossings, so a substring match
# is not at the mercy of where the wrap happened to land.
PLAN_FLAT="$(awk 'BEGIN{RS=""} {gsub(/\n/," "); print; print "@@CLAUSE@@"}' <<< "$PLAN_OUT")"

# Every diagnostic this estate used to fail on is asserted ABSENT by name
# before the exit code is read, so a regression names its own root cause
# instead of arriving as "live-plan exited 1".
#
#   #305  Resource type is outside the live-markers subset
#   #308  This module call cannot be expanded under live resource markers
#   #313 root cause A / #315 root cause C  Dynamic value in static context
#   #368  Module output not supported in static context, and its cascade
#         (Unable to compute static value, Unresolvable identity), which is
#         what the module.ecs_cluster.arn -> element(split("/", ...), 1)
#         chain used to produce
for want_absent in \
  'Resource type is outside the live-markers subset' \
  'This module call cannot be expanded under live resource markers' \
  'Dynamic value in static context' \
  'Module output not supported in static context' \
  'Unable to compute static value' \
  'Unresolvable identity'
do
  N="$(grep -c "^Error: ${want_absent}\$" <<< "$PLAN_OUT")"
  [ "$N" = "0" ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:' | sort | uniq -c; fail "expected 0 '${want_absent}' sites, got $N"; }
done
grep -qF 'Unable to use module.ecs_cluster.arn in static context' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "#368's own root cause (module.ecs_cluster.arn) is back"; }
grep -qF 'Unable to use data.aws_availability_zones.available in static context' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "#313's root cause A (data.aws_availability_zones) is back"; }
grep -qF 'Unable to use each.value in static context, which is required by' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|^In module'; fail "#315's root cause C (each.value) is back"; }

[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -80; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
# ── 3a. the identity #368 made expressible, by value, against the AWS CLI ──
#
# An empty plan is not enough (live/GAUNTLET.md, stage 3): a wrong identity
# converges. The identity this estate turns on is
# aws_appautoscaling_target.this[0]'s, because it is the one #368 made
# expressible - `resource_id = "service/${local.cluster_name}/${...name}"`
# where `local.cluster_name = try(element(split("/", var.cluster_arn), 1),
# "")` and var.cluster_arn is module.ecs_cluster.arn, the ECS cluster's own
# ARN. choudoufu derives the cluster name by SPLITTING the live ARN; the CLI
# is asked for the same object's own ResourceId; the two are compared as
# strings. Three other answers would satisfy a class check and be wrong in a
# marker: the whole ARN, the configuration's own `name` (a prediction of the
# ARN's tail rather than a reading of it), and try()'s "" fallback.
CLUSTER_NAME_FROM_ARN="$(awk -F/ '{print $2}' <<< "$CLUSTER_ARN")"
[ -n "$CLUSTER_NAME_FROM_ARN" ] || fail "could not split a cluster name out of $CLUSTER_ARN"
SERVICE_NAME="$(awsl ecs describe-services --cluster ex-fargate --services ex-fargate --query 'services[0].serviceName' --output text)"
[ -n "$SERVICE_NAME" ] && [ "$SERVICE_NAME" != "None" ] || fail "could not read the ECS service name through the AWS CLI"
WANT_TARGET_RID="service/${CLUSTER_NAME_FROM_ARN}/${SERVICE_NAME}"
# Round-9 repin (lex00/floci PR #130, issue #129): ECS ContainerDefinition/
# TaskDefinition now echo the exact registered JSON, so module.ecs_service's
# own task definition (no volume, default x86_64 runtime platform) is no
# longer replaced on every plan - the OLD cascade this estate tracked
# through round 8 (WANT_CHANGE_N=1, the service's task_definition going
# "known after apply" because the referenced task-def was being replaced)
# is gone (3d-pre below, and the traced fallback above). Round 9 did NOT
# converge this estate to an empty plan, though - it moved the wall onto
# two choudoufu defects that the OLD replacement had been masking:
# choudoufu/#395 (module.ecs_service's own task_definition attribute
# migrating as the short "family:revision" form) and choudoufu/#376
# (module.ecs_service's own task definition resource losing
# track_latest/skip_destroy). Both are FIXED, generically, by the
# top-of-file UPDATE block.
#
# Round-10 repin (lex00/floci#131, this same unit): the standalone task
# definition's (module.ecs_task_definition, volume block + ARM64 runtime
# platform) own mountPoints[].readOnly diff is fixed too, and - the
# re-measurement this unit made - `essential` defaulting to true was never
# an independent wall of its own (see the stage-1 stock-oracle comment
# above). With #395, #376 and #131 all fixed and `essential` never having
# been a genuine forcing factor by itself, this estate now converges to a
# COMPLETELY EMPTY plan: WANT_ADD_N and WANT_DESTROY_N both drop from 1 to
# 0, alongside WANT_CHANGE_N's own drop to 0 two rounds ago.
WANT_ADD_N=0
WANT_CHANGE_N=0
WANT_DESTROY_N=0
# The tofu-slot-only in-place-change fraction #372 used to be tracked here
# is moot now that the estate has no in-place changes at all (WANT_CHANGE_N
# is 0): #372's remainder settles every client-named count instance from
# its own configuration at migrate time - see
# internal/live/liveimport/slot.go's gate 4 and
# causeStableWithoutManagedResults - and there is nothing left for a slot
# tag to be the ONLY change on.
if [ "${BREAK:-}" = "1" ]; then
  WANT_TARGET_RID="service/not-the-cluster/${SERVICE_NAME}"
  WANT_ADD_N=0
  WANT_CHANGE_N=0
  WANT_DESTROY_N=0
  log "  BREAK=1: expecting the scalable target's ResourceId to name a"
  log "           cluster that does not exist, and the plan to be empty."
  log "           Neither is true. This step must fail."
fi
GOT_TARGET_RID="$(awsl application-autoscaling describe-scalable-targets --service-namespace ecs \
  --query "ScalableTargets[?ScalableDimension=='ecs:service:DesiredCount'].ResourceId | [0]" --output text)"
[ -n "$GOT_TARGET_RID" ] && [ "$GOT_TARGET_RID" != "None" ] \
  || fail "could not read the ECS service's scalable target through the AWS CLI"
[ "$GOT_TARGET_RID" = "$WANT_TARGET_RID" ] \
  || fail "the scalable target's ResourceId is $GOT_TARGET_RID, but splitting the live cluster ARN the way this configuration does gives $WANT_TARGET_RID"
log "  identity by value (#368): the scalable target the cloud reports is"
log "  $GOT_TARGET_RID, and splitting"
log "  $CLUSTER_ARN"
log "  on \"/\" and taking element 1 - exactly what local.cluster_name does,"
log "  and what identity.Formula could not express before #368 - reproduces"
log "  it. The formula the resolver now builds for it is"
log "  ecs/service/\${element(split(\"/\", <cluster>.arn), 1)}/<service>/ecs:service:DesiredCount."

# The cluster's and the service's own markers, re-read after the state file
# was deleted, so nothing above can have come from local memory.
GOT_CLUSTER_ADDR2="$(awsl ecs list-tags-for-resource --resource-arn "$CLUSTER_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GOT_CLUSTER_ADDR2" = "$WANT_CLUSTER_ADDR" ] \
  || fail "the ECS cluster's tofu-address changed across the plan: $WANT_CLUSTER_ADDR -> $GOT_CLUSTER_ADDR2"
GOT_SVC_ADDR2="$(awsl ecs list-tags-for-resource --resource-arn "$SVC_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
[ "$GOT_SVC_ADDR2" = "$WANT_SVC_ADDR" ] \
  || fail "the ECS service's tofu-address changed across the plan: $WANT_SVC_ADDR -> $GOT_SVC_ADDR2"
log "  identity re-check: the cluster and the service still carry"
log "  $GOT_CLUSTER_ADDR2 and $GOT_SVC_ADDR2, re-read through the AWS CLI"
log "  after the state file was deleted."

# ── 3a2. #371's ABSENT class is gone, and the three identities it was about
#        are confirmed BY VALUE against the AWS CLI ─────────────────────────
#
# Absence by name first, the same discipline #368's six diagnostics get
# above: three ABSENT readings and the four PARENT_UNAVAILABLE cascades they
# fed. A count, not a "the plan got smaller".
for want_gone in \
  'The provider reports no aws_ecs_cluster exists with identity' \
  'The provider reports no aws_ecs_task_definition exists with identity' \
  'is not in the projection: the provider reports no aws_ecs_cluster exists'
do
  N="$(grep -cF "$want_gone" <<< "$PLAN_FLAT")"
  [ "$N" = "0" ] || { printf '%s\n' "$PLAN_OUT" | sed -n '1,80p'; fail "#371 is back: expected 0 '$want_gone' readings, got $N"; }
done
grep -qF '[ABSENT]' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | sed -n '1,80p'; fail "#371 is back: live-plan reports an ABSENT instance"; }
grep -qF '[PARENT_UNAVAILABLE]' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | sed -n '1,80p'; fail "#371 is back: live-plan reports a PARENT_UNAVAILABLE instance"; }

# An empty ABSENT class is not enough either: a projection can read the WRONG
# object and report nothing missing. So the three identities #371 was about
# are compared BY VALUE, and the value compared is the one the plan itself
# printed as PRIOR state - the ARN the provider handed back for the object
# choudoufu actually bound - against the ARN the AWS CLI reports for the same
# object. Character for character, three objects, both directions read
# independently.
TD_SVC_ARN="$(awsl ecs describe-task-definition --task-definition ex-fargate --query 'taskDefinition.taskDefinitionArn' --output text)"
TD_STANDALONE_ARN="$(awsl ecs describe-task-definition --task-definition ex-fargate-standalone --query 'taskDefinition.taskDefinitionArn' --output text)"
for arn in "$CLUSTER_ARN" "$TD_SVC_ARN" "$TD_STANDALONE_ARN"; do
  [ -n "$arn" ] && [ "$arn" != "None" ] || fail "could not read one of the three #371 ARNs through the AWS CLI"
done
CLUSTER_ARN_ASSERT="$CLUSTER_ARN"
if [ "${BREAK:-}" = "1" ]; then
  CLUSTER_ARN_ASSERT="${CLUSTER_ARN}-not-the-cluster"
  log "  BREAK=1: expecting the plan's prior ECS cluster id to be"
  log "           $CLUSTER_ARN_ASSERT. It is not. This step must fail."
fi
# plan_attr BLOCK_HEADER ATTR - the value the plan printed for one attribute
# of one resource block, read out of the plan's own rendering. Column
# alignment inside a block depends on the longest attribute name in it, so
# the whitespace is matched, never counted.
plan_attr() {
  awk -v hdr="$1" -v attr="$2" '
    index($0, hdr) { inblk = 1; next }
    inblk && /^  # / { inblk = 0 }
    inblk {
      line = $0
      sub(/^[ \t]*[~+-]?[ \t]*/, "", line)
      split(line, f, /[ \t]*=[ \t]*/)
      if (f[1] == attr) {
        v = f[2]
        sub(/^"/, "", v); sub(/".*$/, "", v)
        print v; exit
      }
    }' <<< "$PLAN_OUT"
}
GOT_CLUSTER_PRIOR="$(plan_attr '# module.ecs_cluster.aws_ecs_cluster.this[0] ' 'id')"
if [ -z "$GOT_CLUSTER_PRIOR" ] && ! grep -qF '# module.ecs_cluster.aws_ecs_cluster.this[0] ' <<< "$PLAN_OUT"; then
  # The cluster's own block prints NO diff at all now - not merely one
  # plan_attr() failed to parse. GitHub issue #365 slice 2's residue
  # widening (internal/live/projection's residueEligibleBlock, landed
  # between this estate's own previous unit and this one) resolves what
  # used to be a spurious in-place change here, converging the cluster to a
  # genuine no-op; a fully unchanged resource is never printed by tofu's
  # plan renderer, in any version of it, by design. plan_attr() can only
  # read a value a block prints, so a converged resource is structurally
  # invisible to it - that is a stale ASSERTION (it assumed this block would
  # always render), not evidence the binding moved. See HANDOFF.md: "the
  # estate usually got better and the script did not."
  #
  # The stage's own bar still applies (live/GAUNTLET.md: "An empty plan
  # alone is not enough - a wrong identity can converge"), from a source
  # that is not the rendered diff. This is the same fallback
  # corpus-mastino-dns's own stage 3 already established for a plan with
  # nothing to render at all: TF_LOG=trace's own "[TRACE] projection:
  # materialized ADDR from import identity ID" line
  # (internal/live/projection/build.go), printed unconditionally by
  # materialize regardless of whether the resulting object matched the
  # desired configuration, plus the AWS SDK's own request/response log
  # underneath it - read directly, never through choudoufu's report.
  log "  the cluster's own block carries no diff at all - genuinely converged,"
  log "  not merely omitted - so its prior id is confirmed from a traced replan"
  log "  instead of the (empty) rendered plan."
  TRACE_OUT="$(cd "$ADOPTED_EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
  MATERIALIZE_N="$(grep -cF 'materialized module.ecs_cluster.aws_ecs_cluster.this[0] from import identity' <<< "$TRACE_OUT")"
  [ "$MATERIALIZE_N" = "1" ] \
    || fail "expected exactly one materialize for module.ecs_cluster.aws_ecs_cluster.this[0] in a traced replan, found $MATERIALIZE_N"
  grep -qF "\"clusterArn\":\"${CLUSTER_ARN_ASSERT}\"" <<< "$TRACE_OUT" \
    || fail "the plan's prior state for the ECS cluster is \"\", not the live cluster ARN \"$CLUSTER_ARN_ASSERT\" - the projection bound something else, or nothing"
  # And the create side, by absence: a wrongly-bound (or unbound) cluster
  # would show up as its own "+ resource" block, among the adds - which are
  # fully accounted for by the standalone task definition's own replacement
  # (3d, below; round-9 repin: was two task-definition replacements, now
  # one - see the top-of-file UPDATE block), never by the cluster.
  grep -qF 'module.ecs_cluster.aws_ecs_cluster.this[0] will be created' <<< "$PLAN_OUT" \
    && { grep -E '^  # module\.ecs_cluster\.aws_ecs_cluster' -A 4 <<< "$PLAN_OUT"; fail "#371 (or a fresh regression) is back: the cluster is proposed for creation, not read as an existing object"; }
  GOT_CLUSTER_PRIOR="$CLUSTER_ARN_ASSERT"
  log "  confirmed by a traced replan: exactly one materialize for the cluster,"
  log "  its DescribeClusters response carries clusterArn=$CLUSTER_ARN_ASSERT,"
  log "  and it is not among the 2 adds."
fi
[ "$GOT_CLUSTER_PRIOR" = "$CLUSTER_ARN_ASSERT" ] \
  || { grep -E '^  # module\.ecs_cluster\.aws_ecs_cluster' -A 4 <<< "$PLAN_OUT"; fail "the plan's prior state for the ECS cluster is \"$GOT_CLUSTER_PRIOR\", not the live cluster ARN \"$CLUSTER_ARN_ASSERT\" - the projection bound something else, or nothing"; }
GOT_TD_SVC_PRIOR="$(plan_attr '# module.ecs_service.aws_ecs_task_definition.this[0] ' 'arn')"
if [ -z "$GOT_TD_SVC_PRIOR" ]; then
  # Round-9 repin (lex00/floci PR #130, issue #129): ECS
  # ContainerDefinition/TaskDefinition now echo the exact registered JSON,
  # fixing ~15 silently-dropped fields plus the entirely-absent
  # runtimePlatform - so module.ecs_service's own task definition (no
  # volume, default runtime platform) no longer forces a replacement.
  # `arn` itself is now stable enough that tofu's renderer either omits the
  # block entirely (genuinely converged - the same shape #365 slice 2
  # already produced for the ECS cluster's own block above) or omits `arn`
  # alone as one of the block's own unchanged, hidden attributes (round-9
  # ALSO surfaced two newly-visible in-place changes on this same resource,
  # `track_latest`/`skip_destroy` - see 3d below; `arn` is not one of
  # them). Either way plan_attr() cannot read `arn`'s value from text that
  # never prints it, so it is confirmed instead from a traced replan - the
  # same fallback as the ECS cluster's, just triggered by "could not
  # extract" rather than "block absent", because a block that stays present
  # for its OWN new reasons is exactly what round 9 produced here.
  log "  module.ecs_service's own task definition does not print its arn in"
  log "  the rendered plan (converged, or arn is one of its own hidden"
  log "  unchanged attributes) - confirmed from a traced replan instead."
  TRACE_OUT="$(cd "$ADOPTED_EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
  MATERIALIZE_N="$(grep -cF 'materialized module.ecs_service.aws_ecs_task_definition.this[0] from import identity' <<< "$TRACE_OUT")"
  [ "$MATERIALIZE_N" = "1" ] \
    || fail "expected exactly one materialize for module.ecs_service.aws_ecs_task_definition.this[0] in a traced replan, found $MATERIALIZE_N"
  grep -qF "\"taskDefinitionArn\":\"${TD_SVC_ARN}\"" <<< "$TRACE_OUT" \
    || fail "the plan's prior state for module.ecs_service's task definition is \"\", not the live ARN \"$TD_SVC_ARN\" - the projection bound something else, or nothing"
  grep -qF 'module.ecs_service.aws_ecs_task_definition.this[0] will be created' <<< "$PLAN_OUT" \
    && { grep -E '^  # module\.ecs_service\.aws_ecs_task_definition' -A 4 <<< "$PLAN_OUT"; fail "#371 (or a fresh regression) is back: module.ecs_service's task definition is proposed for creation, not read as an existing object"; }
  GOT_TD_SVC_PRIOR="$TD_SVC_ARN"
  log "  confirmed by a traced replan: exactly one materialize for module.ecs_service's"
  log "  task definition, its DescribeTaskDefinition response carries"
  log "  taskDefinitionArn=$TD_SVC_ARN, and it is not among the adds."
fi
[ "$GOT_TD_SVC_PRIOR" = "$TD_SVC_ARN" ] \
  || { grep -E '^  # module\.ecs_service\.aws_ecs_task_definition' -A 4 <<< "$PLAN_OUT"; fail "the plan's prior state for module.ecs_service's task definition is \"$GOT_TD_SVC_PRIOR\", not the live ARN \"$TD_SVC_ARN\""; }
GOT_TD_STANDALONE_PRIOR="$(plan_attr '# module.ecs_task_definition.aws_ecs_task_definition.this[0] ' 'arn')"
if [ -z "$GOT_TD_STANDALONE_PRIOR" ]; then
  # Round-10 repin (lex00/floci#131, this same unit): the standalone task
  # definition's own mountPoints[].readOnly diff is fixed, and essential
  # defaulting to true was never an independent wall of its own (see the
  # stage-1 stock-oracle comment) - so this resource now converges
  # completely too, the same shape the cluster's and module.ecs_service's
  # own task definition's blocks already converged to above. Confirmed
  # from a traced replan, the identical fallback.
  log "  module.ecs_task_definition's own block carries no diff at all"
  log "  either - genuinely converged - so its prior arn is confirmed from"
  log "  a traced replan instead of the (empty) rendered plan."
  TRACE_OUT="$(cd "$ADOPTED_EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
  MATERIALIZE_N="$(grep -cF 'materialized module.ecs_task_definition.aws_ecs_task_definition.this[0] from import identity' <<< "$TRACE_OUT")"
  [ "$MATERIALIZE_N" = "1" ] \
    || fail "expected exactly one materialize for module.ecs_task_definition.aws_ecs_task_definition.this[0] in a traced replan, found $MATERIALIZE_N"
  grep -qF "\"taskDefinitionArn\":\"${TD_STANDALONE_ARN}\"" <<< "$TRACE_OUT" \
    || fail "the plan's prior state for module.ecs_task_definition's task definition is \"\", not the live ARN \"$TD_STANDALONE_ARN\" - the projection bound something else, or nothing"
  grep -qF 'module.ecs_task_definition.aws_ecs_task_definition.this[0] will be created' <<< "$PLAN_OUT" \
    && { grep -E '^  # module\.ecs_task_definition\.aws_ecs_task_definition' -A 4 <<< "$PLAN_OUT"; fail "#371 (or a fresh regression) is back: module.ecs_task_definition's task definition is proposed for creation, not read as an existing object"; }
  GOT_TD_STANDALONE_PRIOR="$TD_STANDALONE_ARN"
  log "  confirmed by a traced replan: exactly one materialize for the"
  log "  standalone task definition, its DescribeTaskDefinition response"
  log "  carries taskDefinitionArn=$TD_STANDALONE_ARN, and it is not"
  log "  among the (zero) adds."
fi
[ "$GOT_TD_STANDALONE_PRIOR" = "$TD_STANDALONE_ARN" ] \
  || { grep -E '^  # module\.ecs_task_definition\.aws_ecs_task_definition' -A 4 <<< "$PLAN_OUT"; fail "the plan's prior state for module.ecs_task_definition's task definition is \"$GOT_TD_STANDALONE_PRIOR\", not the live ARN \"$TD_STANDALONE_ARN\""; }
log "  #371 FIXED, and by value, not by an empty plan: the projection's own"
log "  prior state for the three types #371 was about is"
log "    $CLUSTER_ARN"
log "    $TD_SVC_ARN"
log "    $TD_STANDALONE_ARN"
log "  each of them read back independently through the AWS CLI. 0 ABSENT,"
log "  0 PARENT_UNAVAILABLE, where there were 3 and 4."

# ── 3b. issue #378, asserted by absence and by value ──────────────────────
#
# This block used to assert the DEFECT: live-plan proposing to remove each
# log group's own tofu-address, and it sat after the plan-count assertions.
# It now asserts the fix, and it runs BEFORE them, so that it is provably
# load-bearing: run this same script against a binary built before the fix
# and it is this block that fails, not the "8 to change" count downstream of
# it. Verified that way, against 44d8d573e5.
#
# Three ways rather than one, because "the two removal lines are gone" is
# also satisfiable by the resource vanishing from the projection entirely,
# which would be a different and worse bug:
#
#   1. no removal is proposed for either marker value, by exact string;
#   2. neither log group appears in the plan's change list at all, which is
#      what a desired tag set equal to the live one looks like;
#   3. the markers are still on the live objects afterwards, re-read through
#      the AWS CLI below (3c), so nothing was silently untagged.
#
# The values are stage 2d's own, which came off the wire after live-import.
for want_kept in "$WANT_LG_FLUENTBIT_ADDR" "$WANT_LG_AL2023_ADDR"
do
  grep -qF -- "- \"tofu-address\" = \"$want_kept\" -> null" <<< "$PLAN_OUT" \
    && { grep -E 'container_definition.*aws_cloudwatch_log_group' -A 12 <<< "$PLAN_OUT"; fail "#378 is back: live-plan proposes removing the marker $want_kept"; }
done
for lg_block in \
  'module.ecs_service.module.container_definition["fluent-bit"].aws_cloudwatch_log_group.this[0]' \
  'module.ecs_task_definition.module.container_definition["al2023"].aws_cloudwatch_log_group.this[0]'
do
  grep -qF -- "  # $lg_block " <<< "$PLAN_OUT" \
    && { grep -F -A 12 -e "$lg_block" <<< "$PLAN_OUT"; fail "#378: $lg_block is proposed for a change again - the marker removal was the only thing it was ever in the plan for"; }
done
log "  #378 FIXED: neither for_each'd-module log group is in the plan at all,"
log "  and no removal is proposed for either marker -"
log "  $WANT_LG_FLUENTBIT_ADDR"
log "  $WANT_LG_AL2023_ADDR"
log "  are the values stage 2d read off the wire, and the replan's desired"
log "  tag set now carries exactly them."

# ── 3c. and they are still on the live objects after the replan ───────────
#
# "The plan proposes nothing" and "the marker is still there" are different
# claims, and only the second one is about the cloud. log_group_tag is stage
# 2d's own reader, so the two stages compare the same bytes from the same
# source.
for lg in "$LOG_GROUP_FLUENTBIT:$WANT_LG_FLUENTBIT_ADDR" "$LOG_GROUP_AL2023:$WANT_LG_AL2023_ADDR"; do
  LG_NAME="${lg%%:*}"
  LG_WANT="${lg#*:}"
  LG_AFTER="$(log_group_tag "$LG_NAME" tofu-address)"
  [ "$LG_AFTER" = "$LG_WANT" ] \
    || fail "after the replan, $LG_NAME carries tofu-address=$LG_AFTER, not $LG_WANT - the plan proposed no removal, but the marker moved anyway"
done
log "  and both markers are still on the live objects, re-read through the"
log "  AWS CLI after the replan."

# ── 3d. genuinely empty, and pinned by exact text so a partial regression
#        cannot slide back into "close enough" ────────────────────────────
#
# Round-10 repin (lex00/floci#131) plus this unit's two fixes
# (configuredAttrsSeed, residueSeedFor) converge this estate completely.
# "No changes." is the whole assertion; every finer-grained thing this
# script used to check by count (ADD_N/CHANGE_N/DESTROY_N, the tofu-slot
# fraction) is now checked by ABSENCE instead, name by name, so a
# regression on any one of them fails on the thing that regressed rather
# than on a moved number nobody reads the reason for.

# ── 3d-pre. lock in #110's fix by absence, so a regression is loud ────────
grep -qF 'service_connect_configuration' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_service' -A 20 <<< "$PLAN_OUT" | grep -B2 -A2 service_connect_configuration; fail "lex00/floci#110 is back: service_connect_configuration appears in the replan again"; }
log "  lex00/floci#110 CONFIRMED FIXED: no service_connect_configuration"
log "  diff anywhere in the replan (was the sole stage-3 emulator gap)."

# Round-9 repin: module.ecs_service's own task definition converged (3a
# above), so it must NOT appear as a replacement any more.
grep -qF 'module.ecs_service.aws_ecs_task_definition.this[0] must be replaced' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_task_definition' -A 6 <<< "$PLAN_OUT"; fail "module.ecs_service's task definition is proposed for replacement again; round-9's container_definitions/runtimePlatform round-trip fix regressed"; }
# Round-10 repin (lex00/floci#131, this unit): the standalone task
# definition must NOT appear as a replacement any more either - the
# essential/mountPoints[].readOnly wall that used to force it is gone.
grep -qF 'module.ecs_task_definition.aws_ecs_task_definition.this[0] must be replaced' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_task_definition\.aws_ecs_task_definition' -A 6 <<< "$PLAN_OUT"; fail "module.ecs_task_definition is proposed for replacement again; the round-10 mountPoints[].readOnly fix (lex00/floci#131), or the essential non-wall finding, has regressed"; }
# The cluster's own block must not appear at all.
grep -qF '# module.ecs_cluster.aws_ecs_cluster.this[0] ' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_cluster\.aws_ecs_cluster' -A 6 <<< "$PLAN_OUT"; fail "the ECS cluster's own block is back in the plan; 3a2's converged-block fallback is stale"; }
# Round-9 repin: the OLD cascade that used to force module.ecs_service's
# own task_definition attribute to "known after apply" must stay gone.
grep -qF '~ task_definition                    = "ex-fargate:1" -> (known after apply)' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_service' -A 15 <<< "$PLAN_OUT"; fail "module.ecs_service.aws_ecs_service.this[0]'s own task_definition is cascading to (known after apply) again - the round-9 fix that converged its task definition regressed"; }
# choudoufu/#395 (fixed, this unit): the short family:revision form must
# never appear on module.ecs_service's own task_definition again.
grep -qF "~ task_definition                    = \"ex-fargate:1\" -> \"${TD_SVC_ARN}\"" <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_service' -A 15 <<< "$PLAN_OUT"; fail "choudoufu/#395 is back: module.ecs_service.aws_ecs_service.this[0]'s task_definition migrated as the short family:revision form again - configuredAttrsSeed/residueSeedFor regressed"; }
# choudoufu/#376 (fixed, this unit): module.ecs_service's own task
# definition resource must never appear in the plan at all - the
# track_latest/skip_destroy gap was this resource's ENTIRE in-place diff,
# so fixing it converges the whole block, not merely those two lines.
grep -qF '# module.ecs_service.aws_ecs_task_definition.this[0] ' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_task_definition' -A 12 <<< "$PLAN_OUT"; fail "choudoufu/#376 is back: module.ecs_service.aws_ecs_task_definition.this[0] appears in the plan again - configuredAttrsSeed regressed"; }
log "  choudoufu/#395 and choudoufu/#376 are FIXED and stay fixed: neither"
log "  module.ecs_service.aws_ecs_service.this[0]'s task_definition nor"
log "  module.ecs_service.aws_ecs_task_definition.this[0]'s own block"
log "  appears in the plan at all any more."

# The estate-wide bottom line: nothing at all is proposed.
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$PLAN_OUT" \
  || { grep -E '^Plan:|^  # .+ will be|^  # .+ must be' <<< "$PLAN_OUT"; fail "live-plan is not reporting \"No changes\" - the plan may not be genuinely empty"; }
log "  the replan is genuinely EMPTY - \"No changes. Your infrastructure"
log "  matches the configuration.\" - round-10 (lex00/floci#131) plus this"
log "  unit's own two fixes clear every wall this estate has ever had."

log ""
log "STAGE 3 (test_plan): PASS. The estate is fully converged: #371, #378,"
log "#372, #110, #395 and #376 are ALL FIXED here and stay fixed, and the"
log "standalone task definition's essential/mountPoints[].readOnly wall"
log "(lex00/floci#131, published and repinned in this same unit) is gone"
log "too - along with the discovery, made re-measuring this round, that"
log "essential defaulting to true was never an independent wall on its"
log "own."
log ""
log "#395 and #376 were the same defect: choudoufu keeps no persisted"
log "state, so every plan re-derives PriorState through"
log "ImportResourceState's bare stub, far barer than what a real state"
log "file's PriorState would carry for any argument only configuration"
log "can ever set (Required, or Optional and never Computed)."
log "internal/live/projection/build.go's configuredAttrsSeed generalizes"
log "what used to be a tags-only import-stub seed (issue #287 item 8) to"
log "that whole population, fixing #376's track_latest/skip_destroy (set"
log "from plain variables) directly. #395's task_definition ="
log "aws_ecs_task_definition.this[0].arn is a reference to another"
log "resource's computed attribute, which the config-language subset's"
log "static evaluator can never resolve; internal/live/projection/"
log "residue.go's residueConfigSourced widens classifyResidue's own"
log "read-A/read-B test for the identical schema property, so MIGRATE's"
log "ratify now records task_definition's correct ARN as residue instead"
log "of rejecting it as unrecordable drift, and a new pre-read step"
log "(builder.residueSeedFor) seeds that residue record into the import"
log "stub whenever configuredAttrsSeed's static evaluator could not"
log "answer."
log ""
log "#371's ABSENT class is still 0; identities confirmed by value"
log "against the AWS CLI: \$CLUSTER_ARN, \$TD_SVC_ARN, \$TD_STANDALONE_ARN,"
log "and #368's scalable target \$GOT_TARGET_RID. #378's two"
log "aws_cloudwatch_log_group instances under for_each'd module calls"
log "stay out of the plan, and #372's remainder keeps all 5 client-named"
log "count instances slotted at migrate time."
gauntlet_stage test_plan pass "genuinely empty replan (\"No changes. Your infrastructure matches the configuration.\") - #371, #378, #372, #110, #395 and #376 all fixed and stay fixed; the standalone task definition's essential/mountPoints[].readOnly wall is gone (lex00/floci#131, published and repinned this unit) and essential defaulting to true was never an independent wall on its own (this unit's own re-measurement). #395/#376: choudoufu keeps no persisted state, so every plan re-derives PriorState through ImportResourceState's bare stub; internal/live/projection/build.go's configuredAttrsSeed generalizes the tags-only import-stub seed (issue #287 item 8) to every Required-or-Optional-non-Computed attribute (fixing #376's track_latest/skip_destroy directly), and internal/live/projection/residue.go's residueConfigSourced widening of classifyResidue plus the new builder.residueSeedFor pre-read seed close #395's managed-reference case (task_definition = aws_ecs_task_definition.this[0].arn) that configuredAttrsSeed's static evaluator alone could not reach. Identities confirmed by value against the AWS CLI: \$CLUSTER_ARN, \$TD_SVC_ARN, \$TD_STANDALONE_ARN, and #368's scalable target \$GOT_TARGET_RID."

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== 4. test apply: apply the empty plan, assert a genuine no-op ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -60; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "a state file exists after the apply"

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
log "  genuine no-op: $BEFORE_N tofu-estate-tagged objects before, $AFTER_N after,"
log "  no state file either time"
gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); $BEFORE_N tofu-estate-tagged objects before, $AFTER_N after, no state file either time"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
CURRENT_STAGE=drift_reconverge
log "=== 5. drift and reconverge: mutate one object out of band ==="
VPC_ID="$(awsl ec2 describe-vpcs \
  --query "Vpcs[?Tags[?Key=='tofu-estate' && Value=='$ESTATE']].VpcId | [0]" --output text 2>/dev/null)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "no live VPC found for estate $ESTATE"

if [ "${BREAK:-}" = "1" ]; then
  # A second, unrelated object is mutated too - the assertion below must
  # catch this as MORE than one object proposed, not silently pass.
  awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Environment,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered $VPC_ID's Environment tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
  --query 'Tags[0].Value' --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated VPC $VPC_ID's Name tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  printf '%s\n' "$CHANGED_ADDRS" | grep -qE 'aws_vpc\.this' \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the VPC that was actually tampered"
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -60; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
    --query 'Tags[0].Value' --output text)"
  [ "$FIXED_VALUE" != "tampered-out-of-band" ] || fail "the VPC's Name tag is still \"tampered-out-of-band\" after reconverging"
  log "  reconverged: VPC $VPC_ID's Name tag is back to its configured value ($FIXED_VALUE)"
  gauntlet_stage drift_reconverge pass "one object tampered (VPC's Name tag), plan proposed fixing exactly $CHANGED_ADDRS, apply changed 1 and the Name tag reconverged"
fi

CURRENT_STAGE=day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
NS_ARN_D="$(awsl servicediscovery list-namespaces --query "Namespaces[0].Arn" --output text)"
[ -n "$NS_ARN_D" ] && [ "$NS_ARN_D" != "None" ] || fail "no live service-discovery namespace found"
ALB_ARN_D="$(awsl elbv2 describe-load-balancers --query "LoadBalancers[0].LoadBalancerArn" --output text)"
[ -n "$ALB_ARN_D" ] && [ "$ALB_ARN_D" != "None" ] || fail "no live load balancer found"
log "  $NS_ARN_D (aws_service_discovery_http_namespace.this), $ALB_ARN_D (module.alb)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename aws_service_discovery_http_namespace.this -> .this_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_service_discovery_http_namespace" "this" {/resource "aws_service_discovery_http_namespace" "this_renamed" {/' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/aws_service_discovery_http_namespace\.this\.arn/aws_service_discovery_http_namespace.this_renamed.arn/' "$ADOPTED_EST/main.tf"
  rm -f "$ADOPTED_EST/main.tf.bak"
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # aws_service_discovery_http_namespace\.this will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_service_discovery_http_namespace.this - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_service_discovery_http_namespace\.this_renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_service_discovery_http_namespace.this_renamed - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying the old namespace address and creating the new one - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.alb -> module.alb_renamed ==="
  sed -i.bak 's/module "alb" {/module "alb_renamed" {/' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/module\.alb\./module.alb_renamed./g' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/module\.alb\./module.alb_renamed./g' "$ADOPTED_EST/outputs.tf"
  rm -f "$ADOPTED_EST/main.tf.bak" "$ADOPTED_EST/outputs.tf.bak"
  cat >> "$ADOPTED_EST/main.tf" <<'EOF'

moved {
  from = module.alb
  to   = module.alb_renamed
}
EOF
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu defect: the moved-block rename of module.alb proposes a create/destroy for one of its children instead of matching them structurally under the parent's new address - not zero churn. Stock's native moved-block handling relocates every child cleanly (confirmed by the oracle above). Not fixed in this unit, scope is the day2_rename stage activation itself (see corpus-vpc-complete's own day2_rename detail for the first occurrence of this class of wall)."; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" -ge 1 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -20; fail "the moved-block rename plan proposes no in-place changes at all - nothing to rewrite the markers"; }
  grep -qF "Plan: 0 to add, $N_CHANGED_D1 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary does not match its own $N_CHANGED_D1 in-place changes"; }
  log "  choudoufu: zero churn, $N_CHANGED_D1 in-place tags update(s) - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE "Resources: 0 added, $N_CHANGED_D1 changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_CHANGED_D1 resources"; }

  ALB_ARN_D_AFTER="$(awsl elbv2 describe-load-balancers --load-balancer-arns "$ALB_ARN_D" --query "LoadBalancers[0].LoadBalancerArn" --output text 2>/dev/null || true)"
  [ "$ALB_ARN_D_AFTER" = "$ALB_ARN_D" ] || fail "the load balancer's arn changed across the rename ($ALB_ARN_D -> $ALB_ARN_D_AFTER) - it was destroyed and recreated, not renamed"
  ALB_ADDR_D_AFTER="$(awsl elbv2 describe-tags --resource-arns "$ALB_ARN_D" --query "TagDescriptions[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ALB_ADDR_D_AFTER" = "module.alb_renamed.aws_lb.this[0]" ] \
    || log "  note: the load balancer's tofu-address now reads $ALB_ADDR_D_AFTER (module renamed, live id unchanged)"
  log "  $ALB_ARN_D unchanged - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_service_discovery_http_namespace.this -> .this_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_service_discovery_http_namespace" "this" {/resource "aws_service_discovery_http_namespace" "this_renamed" {/' "$ADOPTED_EST/main.tf"
  sed -i.bak 's/aws_service_discovery_http_namespace\.this\.arn/aws_service_discovery_http_namespace.this_renamed.arn/' "$ADOPTED_EST/main.tf"
  rm -f "$ADOPTED_EST/main.tf.bak"
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-mv -estate="$ESTATE" aws_service_discovery_http_namespace.this aws_service_discovery_http_namespace.this_renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "wall: aws_service_discovery_http_namespace has no marker search path for live-mv. Its identity is provider-assigned (the ServiceDiscovery service assigns it at create time; no argument reconstructs it), so live-mv can only find it by listing the type, and this provider version has no List support for it. choudoufu correctly refuses rather than guess (HANDOFF's 'a wrong marker outranks a missing one') - the SAME class of refusal corpus-rds-complete-postgres's day2_rename hit for aws_db_instance. The moved-block leg (module.alb, above) already proved zero churn; this is the live-mv leg's own, separate wall. live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"aws_service_discovery_http_namespace.this" -> "aws_service_discovery_http_namespace.this_renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  NS_ARN_D_AFTER="$(awsl servicediscovery list-namespaces --query "Namespaces[0].Arn" --output text)"
  [ "$NS_ARN_D_AFTER" = "$NS_ARN_D" ] || fail "the namespace's arn changed across live-mv ($NS_ARN_D -> $NS_ARN_D_AFTER) - it was destroyed and recreated, not renamed"
  NS_ADDR_D_AFTER="$(awsl servicediscovery list-tags-for-resource --resource-arn "$NS_ARN_D" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$NS_ADDR_D_AFTER" = "aws_service_discovery_http_namespace.this_renamed" ] \
    || fail "the namespace carries tofu-address=$NS_ADDR_D_AFTER after live-mv, not aws_service_discovery_http_namespace.this_renamed"
  log "  $NS_ARN_D unchanged, tofu-address now aws_service_discovery_http_namespace.this_renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.alb renamed with zero churn (0 add, $N_CHANGED_D1 change, 0 destroy), marker rewritten in place; live-mv: aws_service_discovery_http_namespace.this renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.ecs_task_definition
  # (never touched by Part D's own two renames) is bound and converged.
  # Its module CALL's `name` argument (not vendored module-internal
  # source - always in scope to edit) changes to a new literal, which the
  # local "../../modules/service" module sets as `family = coalesce(var.
  # family, var.name)` for its own aws_ecs_task_definition.this[0] - a
  # real, upstream-declared ForceNew argument (ECS has no rename call for
  # a task definition family, only Register/DeregisterTaskDefinition) -
  # forcing a replacement at the SAME declared address while the physical
  # live task definition behind it is destroyed and a new one created -
  # the marker moving onto the new object is this stage's own Proves text.
  #
  # THE TARGET CHOICE, and the two findings that produced it: F-ORACLE's
  # own header comment above records two genuine, separate defects this
  # section reproduced on its first two target choices before landing
  # here - see that comment for the full detail (a stale local record
  # after a bare, same-module live-mv rename with no apply, on aws_
  # service_discovery_http_namespace.this_renamed; and a non-converging
  # cascade into module.ecs_service and its security-group ingress rule,
  # on module.alb_renamed's own load balancer). module.ecs_task_
  # definition dodges both: untouched by Part D's renames, and per this
  # corpus's own header ("no service" - create_service=false) referenced
  # by nothing else in the estate, so there is no cascade at all.
  #
  # THE create_before_destroy SCOPE NOTE (same shape as corpus-ec2-
  # instance-complete's and corpus-sqs-basic's own Part F): the task
  # definition lives inside a local, vendored-shape module (modules/
  # service, mirroring terraform-aws-modules/terraform-aws-ecs's own
  # submodule structure), whose own source this corpus's established
  # convention never patches to add a library-internal lifecycle block.
  # This evidence pass exercises OpenTofu's DEFAULT replace ordering
  # instead. BREAK=replace manufactures the coexistence a skipped destroy
  # would leave behind directly via the AWS CLI.
  CURRENT_STAGE=day2_replace
  F_ADDR="module.ecs_task_definition.aws_ecs_task_definition.this[0]"
  F_OLD_FAMILY="ex-fargate-standalone"
  F_TD_ARN_D="$(awsl ecs list-task-definitions --family-prefix "$F_OLD_FAMILY" --query "taskDefinitionArns[0]" --output text)"
  [ -n "$F_TD_ARN_D" ] && [ "$F_TD_ARN_D" != "None" ] || fail "no live task definition found by family prefix $F_OLD_FAMILY ahead of day2_replace"

  # NO RECORD-STORE CHECK for this type, unlike this unit's other day2_
  # replace sections: aws_ecs_task_definition's own identity attrs are
  # `family` AND `revision` (live/identity/table_generated.go) - revision
  # changes on every apply that touches the container spec at all, not
  # only ones that change family, so a cached import_id would already be
  # stale after ordinary day-2 use, not just a rename. Confirmed directly
  # against the actual record store, no tofu in the loop: cat-ing every
  # key this estate's store holds after Part D found no aws_ecs_task_
  # definition entry at all, while every other taggable type in the same
  # estate (aws_ecs_cluster, aws_ecs_service, aws_lb, ...) has one. Not
  # investigated further in this script-only unit; the marker checks
  # below (tofu-address tag, moved onto the new object) are this
  # section's own identity verification for this resource instead.
  log "=== F0. capture the live task definition ahead of the forced replace ==="
  F_OLD_ADDR_TAG="$(awsl ecs list-tags-for-resource --resource-arn "$F_TD_ARN_D" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.ecs_task_definition.aws_ecs_task_definition.this:0" ] \
    || fail "$F_TD_ARN_D does not carry tofu-address=module.ecs_task_definition.aws_ecs_task_definition.this:0 ahead of day2_replace"
  log "  $F_TD_ARN_D, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live task definition carrying the SAME tofu-
    # address and tofu-slot as the one a genuine replace would destroy -
    # the state "skip the destroy half" of a create-before-destroy
    # replace would leave, produced directly via the AWS CLI rather than
    # by actually interrupting an apply (day2_crash's own job).
    BREAK_COLLISION_ARN="$(awsl ecs register-task-definition --family "${ESTATE}-td-collision" \
      --container-definitions '[{"name":"c","image":"public.ecr.aws/amazonlinux/amazonlinux:2023-minimal","essential":true}]' \
      --requires-compatibilities FARGATE --network-mode awsvpc --cpu 256 --memory 512 \
      --tags "key=tofu-estate,value=$ESTATE" "key=tofu-address,value=module.ecs_task_definition.aws_ecs_task_definition.this:0" "key=tofu-slot,value=0" \
      --query "taskDefinition.taskDefinitionArn" --output text)"
    BREAK_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
    awsl ecs deregister-task-definition --task-definition "$BREAK_COLLISION_ARN" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one slot' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the slot collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one slot) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew name argument, forcing a replace at the same declared address ==="
    perl -0pi -e 's/module "ecs_task_definition" \{\n  source = "\.\.\/\.\.\/modules\/service"\n\n  # Service\n  name           = "\$\{local\.name\}-standalone"\n/module "ecs_task_definition" {\n  source = "..\/..\/modules\/service"\n\n  # Service\n  name           = "\${local.name}-standalone-v2"\n/' "$ADOPTED_EST/main.tf"
    grep -q '${local.name}-standalone-v2' "$ADOPTED_EST/main.tf" || fail "changing module.ecs_task_definition's name argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.ecs_task_definition\.aws_ecs_task_definition\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.ecs_task_definition's task definition when its ForceNew family argument changes"; }
    F_PLAN_LINE="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.' <<< "$F_PLAN_OUT")"
    [ -n "$F_PLAN_LINE" ] || { printf '%s\n' "$F_PLAN_OUT" | tail -15; fail "the day2_replace plan has no summary line"; }
    log "  choudoufu: $F_PLAN_LINE - the task definition forced to replace at the same declared address"

    F_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Apply complete! Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$F_APPLY_OUT" \
      || { printf '%s\n' "$F_APPLY_OUT" | tail -20; fail "the day2_replace apply did not report a clean apply"; }
    log "  $(grep -E 'Apply complete' <<< "$F_APPLY_OUT")"

    # A deregistered ECS task definition is not deleted outright - it
    # moves to status INACTIVE and stays list-able (real AWS behavior,
    # confirmed in floci's own docs: DeregisterTaskDefinition marks
    # INACTIVE, never removes). "gone" here means INACTIVE, not absent.
    F_OLD_STATUS="$(awsl ecs describe-task-definition --task-definition "$F_TD_ARN_D" --query 'taskDefinition.status' --output text 2>/dev/null || echo "GONE")"
    [ "$F_OLD_STATUS" = "INACTIVE" ] || [ "$F_OLD_STATUS" = "GONE" ] \
      || fail "$F_TD_ARN_D is not INACTIVE after the replace (status=$F_OLD_STATUS) - the old object was orphaned, not deregistered"
    log "  $F_TD_ARN_D is $F_OLD_STATUS - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ARN="$(awsl ecs list-task-definitions --family-prefix "${F_OLD_FAMILY}-v2" --query "taskDefinitionArns[0]" --output text)"
    [ -n "$F_NEW_ARN" ] && [ "$F_NEW_ARN" != "None" ] || fail "no live task definition found by family prefix ${F_OLD_FAMILY}-v2 after the replace"
    F_NEW_ADDR_TAG="$(awsl ecs list-tags-for-resource --resource-arn "$F_NEW_ARN" --query "tags[?key=='tofu-address'].value | [0]" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.ecs_task_definition.aws_ecs_task_definition.this:0" ] \
      || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.ecs_task_definition.aws_ecs_task_definition.this:0 - the marker did not move onto the new object"
    log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"
    [ "$F_NEW_ARN" != "$F_TD_ARN_D" ] \
      || fail "sanity: the task definition ARN did not change at all across the replace"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { printf '%s\n' "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

    gauntlet_stage day2_replace pass "choudoufu: changing module.ecs_task_definition's ForceNew name argument (module CALL, passed through to the local module's own family = coalesce(var.family, var.name)) proposed a forced replace at the same declared address ($F_PLAN_LINE), applied cleanly; the old task definition is confirmed INACTIVE via the AWS CLI (ECS deregisters rather than deletes) and the new one ($F_NEW_ARN) carries the marker, moved via the tofu-address tag ($F_TD_ARN_D -> $F_NEW_ARN); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes replacing the task definition at the same address ($REPLACE_ORACLE_PLAN_LINE, plan only, not applied - it shares floci's account with \$ADOPTED_EST); BREAK=replace confirms a manufactured marker collision is reported loudly (\"Two live resources claiming one slot\") rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names; no local record store check for this type - see this section's own header comment (aws_ecs_task_definition's identity attrs include \`revision\`, which changes on every apply, and no record was ever found for it in this estate's store); two earlier target choices each found a genuine, separate defect: aws_service_discovery_http_namespace.this_renamed's (mv.go's propagateModuleRename skipping MoveRecord for a same-module rename) is FIXED on the gauntlet/mv-rekey branch, GitHub issue #412 - see F-ORACLE's own header comment and corpus-autoscaling-complete's/corpus-eks-basic's matching mv.go finding in this same unit, neither of which was re-run for #412; module.alb_renamed's (the non-converging cascade, F-ORACLE's own header comment, finding 2) remains a separate, open finding, not fixed here."
  fi
  CURRENT_STAGE=""

  # ══════════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active stage - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed rename: module.alb_renamed and
  # aws_service_discovery_http_namespace.this_renamed are both live and the
  # config plans empty (D3). module.ecs_task_definition (the standalone
  # task definition - no service, a volume, an ARM64 runtime platform) is
  # untouched by the rename and is the object removed here: it contributes
  # no managed-resource attribute to anything else in the config
  # (create_service = false, and outputs.tf's two interpolations of it are
  # a plain string, not a resource reference), so its removal needs no
  # other config edit - the same "no other dependents" shape reference-
  # ec2-vpc's Part E and corpus-iam-policy's Part E both use, just against
  # a whole module's worth of children (8: the task definition, its
  # execution and task IAM roles/policy/attachment, its security group and
  # egress rule, and the al2023 container's own CloudWatch log group)
  # instead of a single resource.
  #
  # BREAK_REMOVE=1 exercises this stage's own break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go for day2_remove is literally
  # "keep the block; no destroy may be proposed".

  CURRENT_STAGE=day2_remove
  log "=== E0. capture the live standalone task definition's family before day2_remove ==="
  TD_FAMILY="ex-fargate-standalone"
  TD_ARN_BEFORE="$(awsl ecs list-task-definitions --family-prefix "$TD_FAMILY" --status ACTIVE --query 'taskDefinitionArns[0]' --output text)"
  [ -n "$TD_ARN_BEFORE" ] && [ "$TD_ARN_BEFORE" != "None" ] || fail "no live standalone task definition found by family prefix $TD_FAMILY before day2_remove even starts"
  log "  $TD_ARN_BEFORE"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.ecs_task_definition's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -30; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.ecs_task_definition\..+ will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_REMOVE=1: a destroy was proposed under module.ecs_task_definition even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: the kept-block plan is not empty"; }
    log "  BREAK_REMOVE=1: correctly proposes nothing - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.ecs_task_definition's block ==="
    remove_task_definition_block "$ADOPTED_EST"
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -30
      fail "choudoufu withheld a destroy under module.ecs_task_definition as a possible rename (discovery.go's classifyOrphans) even though no other module.ecs_task_definition block exists anywhere in this config - this is an honest wall, not a pass"
    fi
    REMOVE_DESTROY_ADDRS="$(grep -oE '^  # \S+ will be destroyed' <<< "$REMOVE_PLAN_OUT" | awk '{print $2}' | sort -u)"
    REMOVE_N="$(printf '%s\n' "$REMOVE_DESTROY_ADDRS" | grep -c . || true)"
    grep -qE '^  # .+ will be (created|updated)' <<< "$REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's day2_remove plan proposes a create or update, not a pure removal"; }
    grep -qF "Plan: 0 to add, 0 to change, $REMOVE_N to destroy." <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's day2_remove plan summary does not match its own $REMOVE_N destroys"; }
    # A real, named wall (HANDOFF row 2: the plans differ), confirmed on a
    # live run rather than assumed: choudoufu proposes 7 of stock's 8
    # destroys, missing exactly
    # module.ecs_task_definition.aws_iam_role_policy_attachment.task_exec[0].
    # Root cause read directly in the schema, not guessed:
    # internal/live/identity/table_generated.go's aws_iam_role_policy_attachment
    # row builds its identity from Components requiring the LIVE
    # CONFIGURATION's own "role" and "policy_arn" argument values (composed-
    # of-arguments, ImportSyntax ROLENAME/POLICYARN) - there is no marker
    # (the type carries no tags argument at all) and no ServerAssigned
    # cloud-listing route (contrast aws_vpc_security_group_egress_rule two
    # lines above, ServerAssigned:true, found by listing the security
    # group's own live rules with no configuration involved). Once the
    # declaring block - and here, the whole module - is removed, there is
    # no configuration left to derive role/policy_arn from and no marker or
    # record to recover them from either, so orphan discovery has no way to
    # know the attachment ever existed. This is not specific to this one
    # instance: it reaches every composed-of-arguments, untaggable,
    # non-record-backed type whenever its declaring block (or an ancestor
    # module) is removed outright rather than merely renamed - a generic,
    # schema-derived limitation of the current discovery mechanism, not a
    # per-type gap. Not fixed in this script-only unit.
    [ "$REMOVE_N" = "$REMOVE_ORACLE_N" ] \
      || { printf 'choudoufu destroys (%s):\n%s\nstock destroys (%s):\n%s\n' "$REMOVE_N" "$REMOVE_DESTROY_ADDRS" "$REMOVE_ORACLE_N" "$REMOVE_ORACLE_DESTROY_ADDRS"; fail "choudoufu proposes $REMOVE_N destroys under module.ecs_task_definition, stock's oracle proposed $REMOVE_ORACLE_N for the same block removal - see the comment immediately above this assertion for the named, generic root cause (a composed-of-arguments untaggable type left orphaned with no configuration, marker or record left to derive its identity from)"; }
    [ "$REMOVE_DESTROY_ADDRS" = "$REMOVE_ORACLE_DESTROY_ADDRS" ] \
      || { printf 'choudoufu:\n%s\nstock:\n%s\n' "$REMOVE_DESTROY_ADDRS" "$REMOVE_ORACLE_DESTROY_ADDRS"; fail "choudoufu's destroy address set differs from stock's oracle"; }
    log "  choudoufu: exactly $REMOVE_N destroys under module.ecs_task_definition, address-for-address identical to stock's oracle on cold_deploy's own state, nothing else"

    BEFORE_REMOVE_N="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
    REMOVE_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE "Resources: 0 added, 0 changed, $REMOVE_N destroyed" <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly $REMOVE_N destroys"; }

    TD_COUNT_AFTER="$(awsl ecs list-task-definitions --family-prefix "$TD_FAMILY" --status ACTIVE --query 'length(taskDefinitionArns)' --output text 2>/dev/null || echo 0)"
    [ "$TD_COUNT_AFTER" = "0" ] || fail "the standalone task definition family $TD_FAMILY still has $TD_COUNT_AFTER active revision(s) after the destroy - it was orphaned, not destroyed"
    log "  $TD_FAMILY has 0 active revisions - confirmed via the AWS CLI, not through choudoufu's own report"

    AFTER_REMOVE_N="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
    log "  tofu-estate-tagged objects: $BEFORE_REMOVE_N before, $AFTER_REMOVE_N after"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    # RE-VERIFIED against current main, with #405's fix in (re-verify-
    # day2_remove unit, 2026-08): E1's own INITIAL destroy plan is now a
    # clean, exact 8-of-8 match with stock's oracle (asserted above,
    # REMOVE_N = REMOVE_ORACLE_N and the address sets equal) - #405's fix
    # DID close this estate's originally-recorded wall
    # (aws_iam_role_policy_attachment.task_exec[0], the composed-of-
    # arguments type this comment block's header names). This is a
    # narrower, DIFFERENT residual, found only by re-verifying past the
    # first plan: after E1's 8 destroys apply cleanly, this second plan -
    # taken with nothing else touched in between - still proposes
    # destroying module.ecs_task_definition.aws_vpc_security_group_egress_
    # rule.this["all"], one MORE untaggable child of the same module,
    # newly discovered as orphaned only on THIS pass. Stock's own day2_remove
    # oracle (D-ORACLE above) needed only one pass for its 8 destroys and
    # implies no comparable ninth object, since a real state-backed apply
    # destroys everything belonging to a removed block in the SAME apply -
    # this two-pass shape is choudoufu-specific. Not fixed here - a Go
    # change (likely a discovery-ordering or dependency gap between a
    # security group and its own split-out ingress/egress rule resources
    # under the record-orphan-read path), out of scope for this
    # script-only re-verification unit; named precisely as this estate's
    # true residual scope for #405/#410 rather than left as the old,
    # already-closed wall.
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "#405's fix closed this estate's original wall (E1's initial 8-destroy plan now matches stock's oracle exactly, address-for-address) but a SECOND plan taken right after the apply still proposes destroying module.ecs_task_definition.aws_vpc_security_group_egress_rule.this[\"all\"] - one more untaggable child discovered orphaned only on this later pass, which stock's single-pass oracle needed no equivalent second look for. New residual, not the old one: $(grep -E '^  #' <<< "$E_FINAL_PLAN_OUT" | tr '\n' ' ')"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.ecs_task_definition's block proposed exactly $REMOVE_N destroys (0 add, 0 change, $REMOVE_N destroy), address-for-address identical to stock's oracle on cold_deploy's own state; applied cleanly (0 added, 0 changed, $REMOVE_N destroyed); the standalone task definition family ($TD_FAMILY) genuinely has 0 active revisions afterward, read via the AWS CLI, not choudoufu's own report; classifyOrphans did not withhold any destroy because no other module.ecs_task_definition block is declared anywhere in this config; the next plan is empty"
  fi
  CURRENT_STAGE=""
fi
CURRENT_STAGE=""

CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY (all five stages, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS"
log "  stage 2  migrate            PASS (real: $ELIGIBLE of $INSTANCES stamped, see header)"
log "  stage 3  test_plan          PASS - genuinely empty replan (0 ABSENT, 0 PARENT_UNAVAILABLE, three identities confirmed by value; both for_each'd-module log groups out of the plan with their markers intact; service_connect_configuration confirmed absent by name). #371, #378, #372 and #110 all FIXED and stay fixed. #395/#376 (this unit): fixed generically by configuredAttrsSeed and residueSeedFor. Round-10 (lex00/floci#131, this unit): the standalone task definition's mountPoints[].readOnly wall is gone, and essential defaulting to true was never an independent wall of its own (this unit's own re-measurement) - the estate has no remaining diff at all."
log "  stage 4  test_apply         PASS - genuine no-op apply, $BEFORE_N tofu-estate-tagged objects before and after"
log "  stage 5  drift_reconverge   PASS - one object tampered (VPC Name tag), plan proposed fixing exactly it, apply reconverged"
log ""
log "62 real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Three earlier floci gaps found and fixed along the way"
log "(lex00/floci#59, #60, #110) no longer block this script. #110 (ECS"
log "service_connect_configuration echoed at an unmodeled top-level Service"
log "location instead of deployments[].serviceConnectConfiguration) was"
log "CONFIRMED FIXED this round (PR #128, ff815779) by re-probing the pinned"
log "digest directly, with no tofu in the loop. One remains open and filed,"
log "not fixed here: #111 (the default network ACL's IPv6 rule loses its"
log "CIDR type on read, and non-ICMP rules gain a spurious icmp_code/"
log "icmp_type) - it produces no diff on this estate's own empty applied"
log "values, so it does not block stage 3 here even though it is still open."
log "Run again with BREAK=1: stage 1 still passes and stage 2's own"
log "identity-by-value assertion, 2d's marker on the fluent-bit log group"
log "(#378), is the first one that fails."
