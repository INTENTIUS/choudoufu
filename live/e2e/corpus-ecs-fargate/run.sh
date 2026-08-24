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
# genuine no-op in stock's own replan too - it is NO LONGER one of the two
# replacements HANDOFF.md's row 3 used to name here. Confirmed by absence,
# not merely by an unmoved count. Only module.ecs_task_definition (the
# standalone one, with a volume block and an ARM64 runtime platform) still
# forces a replacement, from two residual diffs verified directly against
# the emulator API and against real AWS (no tofu in the loop, both
# directions): `essential` going from explicit `true` (state, echoed by
# both floci and real AWS - confirmed live, both leave it non-absent) to
# absent (config, which never sets it, relying on AWS's default) is a
# genuine HANDOFF row-3 wall - terraform-provider-aws's own
# container_definitions equivalence check does not suppress it, and this
# is true independent of the emulator; `mountPoints[].readOnly` going from
# an explicit `false` (state) to absent (config) is instead a confirmed
# emulator gap (lex00/floci#131: real AWS leaves readOnly entirely absent
# from the response when the caller never sets it, floci was defaulting
# and echoing it) - fixed on branch fix/ecs-mountpoints-readonly, not yet
# published/repinned, so it still fires against this pin. Either diff
# alone is enough to force the replacement (container_definitions is
# ForceNew), so fixing #131 alone would not have cleared this estate this
# round either.
STOCK_REPLAN="$(cd "$PLAIN_EST" && terraform plan -input=false -no-color 2>&1)"
STOCK_REPLAN_RC=$?
[ "$STOCK_REPLAN_RC" -eq 0 ] || { printf '%s\n' "$STOCK_REPLAN" | tail -30; fail "the stock oracle replan exited $STOCK_REPLAN_RC"; }
STOCK_PLAN_LINE="$(grep -E '^Plan:|^No changes' <<< "$STOCK_REPLAN" | tail -1)"
grep -qF 'module.ecs_service.aws_ecs_task_definition.this[0] must be replaced' <<< "$STOCK_REPLAN" \
  && { printf '%s\n' "$STOCK_REPLAN" | grep -E '^Plan:|^  # '; fail "stock's own replan replaces module.ecs_service's task definition again - the round-9 container_definitions/runtimePlatform round-trip fix regressed"; }
grep -qF 'module.ecs_task_definition.aws_ecs_task_definition.this[0] must be replaced' <<< "$STOCK_REPLAN" \
  || { printf '%s\n' "$STOCK_REPLAN" | grep -E '^Plan:|^  # '; fail "stock's own replan no longer reports 'module.ecs_task_definition.aws_ecs_task_definition.this[0] must be replaced' - the oracle for stage 3's one remaining replacement has moved"; }
log "  stock oracle: plain terraform replanning its own fresh state is NOT"
log "  empty - \"$STOCK_PLAN_LINE\" - module.ecs_service's own task"
log "  definition converged (round-9 fix); only the standalone"
log "  module.ecs_task_definition is still replaced by stock itself"
log "  (essential + mountPoints[].readOnly - HANDOFF.md row 3 and"
log "  lex00/floci#131 respectively)."

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "$INSTANCES resources, once for real"
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
# two choudoufu defects that the OLD replacement had been masking, both
# newly filed at the time (a repin's job is to re-measure, not
# chase what repinning exposes) and now BOTH FIXED, generically, by the
# top-of-file UPDATE block: choudoufu/#395 - module.ecs_service's own
# task_definition attribute no longer migrates as the short
# "family:revision" form, and choudoufu/#376 - module.ecs_service's own
# task definition resource no longer loses track_latest/skip_destroy.
# WANT_CHANGE_N drops to 0 (3d below asserts both are ABSENT from the
# plan, by exact text, so a regression on either cannot hide behind the
# count). The standalone task definition (module.ecs_task_definition,
# which DOES carry a volume block and an ARM64 runtime platform) is still
# the only replacement, untouched by either fix - WANT_ADD_N/
# WANT_DESTROY_N unchanged at 1 - confirmed identical in stock's own
# stage-1c replan.
WANT_ADD_N=1
WANT_CHANGE_N=0
WANT_DESTROY_N=1
# How many of the in-place changes are the tofu-slot tag and nothing else.
# Was 25 before choudoufu #372, then 5 once #372's first landing settled
# every count-expanded instance of a SERVER-ASSIGNED type (the whole VPC
# submodule, the security groups, the log groups' parents and so on) at
# migrate time. Now 0: #372's remainder settles a client-named count
# instance too, from its own configuration, when a bare identity.ResolveWith
# call independently agrees it needs discovery for a cause a real live-plan
# resolution cannot later reverse - see
# internal/live/liveimport/slot.go's gate 4 and
# causeStableWithoutManagedResults.
WANT_SLOT_N=0
if [ "${BREAK:-}" = "1" ]; then
  WANT_TARGET_RID="service/not-the-cluster/${SERVICE_NAME}"
  WANT_ADD_N=0
  WANT_CHANGE_N=0
  WANT_DESTROY_N=0
  WANT_SLOT_N=0
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

# ── 3d. what stage 3 is blocked on NOW, pinned exactly ────────────────────
#
# "2 to add, 1 to change, 2 to destroy" (was 2/3/2 after #372's remainder
# landed here, 2/8/2 before it), every line of it accounted for. See the
# header for the full reasoning; the assertions are the summary:
#
#   2 add + 2 destroy   one replacement each of the two
#              aws_ecs_task_definition instances, forced by
#              container_definitions. STOCK DOES THE SAME - asserted in
#              stage 1c against a state file with no marker in it - so this
#              is HANDOFF.md's third row, not a choudoufu difference.
#   0 of 1     a bare tofu-slot tag addition on a count-expanded instance of
#              a CLIENT-NAMED type - was 5 of 8 before #372's remainder,
#              25 of 29 before #372 landed at all. Every one of them now
#              carries its slot from migrate time; see the header's item 1.
#   1 of 1     lex00/floci#110 IS NOW FIXED (round 8, PR #128, ff815779):
#              re-probed directly against the pinned digest with no tofu in
#              the loop (raw `aws ecs create-service --service-connect-
#              configuration ...` + `describe-services`) - floci now
#              echoes serviceConnectConfiguration only on
#              deployments[status=PRIMARY], never on the top-level Service
#              object, matching botocore's own ECS service-2.json shape
#              (Service has no such member at all) and exactly where
#              terraform-provider-aws's own flattenServiceRead reads it
#              from (internal/service/ecs/service.go: `for _, deployment
#              := range service.Deployments { if
#              aws.ToString(deployment.Status) == "PRIMARY" { ...
#              deployment.ServiceConnectConfiguration ... }`). The
#              service_connect_configuration block is confirmed ABSENT
#              from this estate's own replan (3d-pre below, by absence,
#              not by count). What remains in its place: the SAME single
#              in-place change as before #110's fix, but now for a
#              DIFFERENT, already-known reason - `task_definition = ... ->
#              (known after apply)` on module.ecs_service.aws_ecs_service.
#              this[0], a pure Terraform-core dependency cascade forced by
#              the two aws_ecs_task_definition replacements directly
#              above (the service references the task definition's own
#              computed arn/family:revision, which becomes unknown the
#              moment its own resource is replaced). Confirmed identical
#              in stock's own fresh replan too (stage 1c's own dump,
#              instrumented and read directly): stock shows exactly
#              `~ task_definition = "arn:...task-definition/ex-fargate:1"
#              -> (known after apply)` on the same resource, same count
#              (2/1/2). This is not a new choudoufu-only defect and not a
#              new emulator gap; it is the pre-existing HANDOFF row-3 wall
#              (container_definitions round-trip forcing the task-def
#              replacement) rippling one hop further through the
#              dependency graph - present in both binaries' plans, not
#              choudoufu's alone. #111 (the default network ACL's IPv6/
#              icmp read-fidelity gap) remains open but, as before,
#              produces no diff on this estate's own empty applied values.
#   0 of 29    the two log groups inside for_each'd module calls, which
#              carried the previous two entries here. #378 is fixed: their
#              desired tags now equal the live ones, so they are absent
#              from the plan altogether. Pinned by absence, by value, in
#              3b above - deliberately before this block, so it is 3b that
#              fails against a pre-fix binary rather than these counts.

# ── 3d-pre. lock in #110's fix by absence, so a regression is loud ────────
grep -qF 'service_connect_configuration' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_service' -A 20 <<< "$PLAN_OUT" | grep -B2 -A2 service_connect_configuration; fail "lex00/floci#110 is back: service_connect_configuration appears in the replan again"; }
log "  lex00/floci#110 CONFIRMED FIXED: no service_connect_configuration"
log "  diff anywhere in the replan (was the sole stage-3 emulator gap)."
ADD_N="$(sed -nE 's/^Plan: ([0-9]+) to add.*/\1/p' <<< "$PLAN_OUT" | tail -1)"
CHANGE_N="$(sed -nE 's/^Plan: [0-9]+ to add, ([0-9]+) to change.*/\1/p' <<< "$PLAN_OUT" | tail -1)"
DESTROY_N="$(sed -nE 's/^Plan: [0-9]+ to add, [0-9]+ to change, ([0-9]+) to destroy.*/\1/p' <<< "$PLAN_OUT" | tail -1)"
[ "${ADD_N:-}" = "$WANT_ADD_N" ] || { grep -E '^Plan:|^  # .+ will be|^  # .+ must be' <<< "$PLAN_OUT"; fail "expected $WANT_ADD_N resources proposed for creation, got ${ADD_N:-none}"; }
[ "${CHANGE_N:-}" = "$WANT_CHANGE_N" ] || { grep -E '^Plan:|^  # .+ will be|^  # .+ must be' <<< "$PLAN_OUT"; fail "expected $WANT_CHANGE_N resources proposed for in-place change, got ${CHANGE_N:-none}"; }
[ "${DESTROY_N:-}" = "$WANT_DESTROY_N" ] || { grep -E '^Plan:|^  # .+ will be|^  # .+ must be' <<< "$PLAN_OUT"; fail "expected $WANT_DESTROY_N resources proposed for destruction, got ${DESTROY_N:-none}"; }
# Round-9 repin: module.ecs_service's own task definition converged (3a
# above), so it must NOT appear as a replacement any more - only the
# standalone task definition (volume + ARM64 runtime platform) still forces
# one, confirmed identical in stock's own stage-1c replan.
grep -qF 'module.ecs_service.aws_ecs_task_definition.this[0] must be replaced' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_task_definition' -A 6 <<< "$PLAN_OUT"; fail "module.ecs_service's task definition is proposed for replacement again; round-9's container_definitions/runtimePlatform round-trip fix regressed"; }
grep -qF 'module.ecs_task_definition.aws_ecs_task_definition.this[0] must be replaced' <<< "$PLAN_OUT" \
  || { grep -E '^  # ' <<< "$PLAN_OUT"; fail "expected 'module.ecs_task_definition.aws_ecs_task_definition.this[0] must be replaced' - stock replans the same replacement (stage 1c), so this is the shape stage 3 is waiting on, not a new one"; }
# The cluster's own block must not appear at all - a real diff on it (from
# a future floci fix reversing course, or a real regression) would mean
# WANT_CHANGE_N is stale again, the same way it was stale coming into this
# unit; asserting its absence here, by name, makes that loud instead of
# read from a count that moved for an unexplained reason.
grep -qF '# module.ecs_cluster.aws_ecs_cluster.this[0] ' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_cluster\.aws_ecs_cluster' -A 6 <<< "$PLAN_OUT"; fail "the ECS cluster's own block is back in the plan; WANT_CHANGE_N=$WANT_CHANGE_N and 3a2's converged-block fallback are both stale"; }
# Round-9 repin: with module.ecs_service's task definition converging (3a
# above) instead of being replaced, the OLD cascade that used to force its
# task_definition attribute to "known after apply" is gone - asserted by
# absence, by name, so a regression that brings it back is loud.
grep -qF '~ task_definition                    = "ex-fargate:1" -> (known after apply)' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_service' -A 15 <<< "$PLAN_OUT"; fail "module.ecs_service.aws_ecs_service.this[0]'s own task_definition is cascading to (known after apply) again - the round-9 fix that converged its task definition regressed"; }
# choudoufu/#395 and choudoufu/#376 (fixed, this unit): both used to be
# the two changes WANT_CHANGE_N=2 named here, pinned by exact text so a
# regression on either could not hide behind the count. Now pinned by
# ABSENCE instead, the same discipline the cluster's own block and the
# "(known after apply)" cascade already get above - a regression on
# either fix is what turns these from a silent pass into a loud failure,
# not a moved count nobody reads the reason for.
grep -qF "~ task_definition                    = \"ex-fargate:1\" -> \"${TD_SVC_ARN}\"" <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_service' -A 15 <<< "$PLAN_OUT"; fail "choudoufu/#395 is back: module.ecs_service.aws_ecs_service.this[0]'s task_definition migrated as the short family:revision form again - configuredAttrsSeed/residueSeedFor regressed"; }
# module.ecs_service's own task definition resource must not appear in
# the plan at all any more, matching the cluster's own converged-block
# assertion above: #376's track_latest/skip_destroy gap was this
# resource's ENTIRE in-place diff, so fixing it converges the whole
# block, not merely those two lines.
grep -qF '# module.ecs_service.aws_ecs_task_definition.this[0] ' <<< "$PLAN_OUT" \
  && { grep -E '^  # module\.ecs_service\.aws_ecs_task_definition' -A 12 <<< "$PLAN_OUT"; fail "choudoufu/#376 is back: module.ecs_service.aws_ecs_task_definition.this[0] appears in the plan again - configuredAttrsSeed regressed"; }
log "  choudoufu/#395 and choudoufu/#376 are FIXED and stay fixed: neither"
log "  module.ecs_service.aws_ecs_service.this[0]'s task_definition nor"
log "  module.ecs_service.aws_ecs_task_definition.this[0]'s own block"
log "  appears in the plan at all any more."
# How many of the in-place changes are the tofu-slot tag and nothing else.
# Counted per resource block, not per line: the tag shows up twice in every
# block (tags and tags_all), and "is this change only the marker" is a
# question about the block, not about a line.
# The resource-block section of a plan ends at the first line in column 0
# after it ("Plan: ..." or "Changes to Outputs:"). Both readers below take
# that as a block terminator, not only the next "  # " header: without it the
# LAST in-place block absorbs the "  ~ output_name = ..." lines of the
# Changes-to-Outputs section, and whichever resource happens to sort last
# reads as "changed more than its marker" for a reason that has nothing to do
# with it.
slot_only_blocks() {
  awk '
    /^  # .* will be updated in-place$/ { if (blk && !other) n++; blk = 1; other = 0; next }
    /^  # / { if (blk && !other) n++; blk = 0; other = 0; next }
    /^[^ ]/ { if (blk && !other) n++; blk = 0; other = 0; next }
    blk {
      if ($0 !~ /^[ \t]*[~+-][ \t]/) next            # not a diff line
      if ($0 ~ /^  [~+-] resource /) next            # the block header
      if ($0 ~ /tofu-slot/) next                     # the marker itself
      if ($0 ~ /^[ \t]*~ tags(_all)?[ \t]*=/) next   # the map it sits in
      other = 1
    }
    END { if (blk && !other) n++; print n + 0 }' <<< "$PLAN_OUT"
}
# The complement, with each offending block's own diff lines: "found 24,
# wanted 25" is not actionable and "this block also changed these three
# attributes" is.
non_slot_blocks() {
  awk '
    function flush() { if (blk && other) { printf "%s", buf } blk = 0; other = 0; buf = "" }
    /^  # .* will be updated in-place$/ { flush(); blk = 1; buf = $0 "\n"; next }
    /^  # / { flush(); next }
    /^[^ ]/ { flush(); next }
    blk {
      if ($0 !~ /^[ \t]*[~+-][ \t]/) next
      if ($0 ~ /^  [~+-] resource /) next
      buf = buf $0 "\n"
      if ($0 ~ /tofu-slot/) next
      if ($0 ~ /^[ \t]*~ tags(_all)?[ \t]*=/) next
      other = 1
    }
    END { flush() }' <<< "$PLAN_OUT"
}
SLOT_N="$(slot_only_blocks)"
[ "$SLOT_N" = "$WANT_SLOT_N" ] \
  || { log "  blocks changing MORE than the tofu-slot tag:"; non_slot_blocks; fail "expected $WANT_SLOT_N of the $WANT_CHANGE_N in-place changes to be the tofu-slot tag alone (#372), found $SLOT_N"; }
log "  BLOCKED, but no longer on #371, #378, #372, #110, #395 or #376:"
log "  $ADD_N to add, $CHANGE_N to change, $DESTROY_N to destroy, $SLOT_N"
log "  of them the tofu-slot tag alone. The 1 add and 1 destroy are the"
log "  standalone task definition's replacement (stage 1c) - the only"
log "  thing left, and it is not a choudoufu defect."

log ""
log "STAGE 3 (test_plan): BLOCKED, but the wall shrank to exactly one"
log "estate: this is now genuinely down to the standalone task"
log "definition's own replacement. #371, #378, #372, #110, #395 and #376"
log "are ALL FIXED here and stay fixed. #395 and #376 (this unit): both"
log "were the same defect - choudoufu keeps no persisted state, so every"
log "plan re-derives PriorState through ImportResourceState's bare stub,"
log "far barer than what a real state file's PriorState would carry for"
log "any argument only configuration can ever set (Required, or Optional"
log "and never Computed) - fixed generically by"
log "internal/live/projection/build.go's configuredAttrsSeed (the literal/"
log "var case, #376's whole fix) and residueSeedFor (the managed-"
log "reference case task_definition = aws_ecs_task_definition.this[0].arn"
log "needed too, via a residue record MIGRATE's own ratify now writes -"
log "internal/live/projection/residue.go's residueConfigSourced widening"
log "of classifyResidue). Neither module.ecs_service.aws_ecs_service."
log "this[0]'s task_definition nor module.ecs_service.aws_ecs_task_"
log "definition.this[0]'s own block appears in the plan any more,"
log "asserted by absence (3d)."
log ""
log "The standalone module.ecs_task_definition (volume block, ARM64"
log "runtime platform) is the ONE thing left, untouched by either fix and"
log "confirmed identical in stock's own fresh replan (stage 1c). Read"
log "directly against the emulator API and against real AWS (no tofu in"
log "the loop, both directions), two residual container_definitions diffs"
log "force it, independently, either one sufficient on its own"
log "(container_definitions is ForceNew): 'essential' goes from explicit"
log "true (state - both floci and real AWS always echo it, confirmed live"
log "against real AWS) to absent (config, which relies on the documented"
log "default and never sets it) - a genuine HANDOFF row-3 wall,"
log "terraform-provider-aws's own equivalence check does not suppress it,"
log "independent of the emulator. 'mountPoints[].readOnly' goes from an"
log "explicit false (state) to absent (config) - a confirmed emulator gap"
log "(lex00/floci#131: real AWS leaves readOnly entirely absent when the"
log "caller never sets it; floci was defaulting and echoing it), fixed and"
log "tested on branch fix/ecs-mountpoints-readonly, not yet"
log "published/repinned. #111 (the default network ACL's IPv6/icmp"
log "read-fidelity gap) remains open but, as before, produces no diff on"
log "this estate's own empty applied values. All eight of #368's"
log "diagnostics are gone, asserted by absence above, and the identity"
log "#368 made expressible is confirmed by value."
log ""
gauntlet_stage test_plan fail "choudoufu/#395 and choudoufu/#376 fixed (this unit): both were the same defect - choudoufu keeps no persisted state, so every plan re-derives PriorState through ImportResourceState's bare stub, far barer than what a real state file's PriorState would carry for any argument only configuration can ever set (Required, or Optional and never Computed). internal/live/projection/build.go's configuredAttrsSeed generalizes what used to be a tags-only import-stub seed (issue #287 item 8) to that whole population, fixing #376's track_latest/skip_destroy (set from plain variables) directly. #395's task_definition = aws_ecs_task_definition.this[0].arn is a reference to another resource's computed attribute, which the config-language subset's static evaluator can never resolve; internal/live/projection/residue.go's residueConfigSourced widens classifyResidue's own read-A/read-B test for the identical schema property, so MIGRATE's ratify now records task_definition's correct ARN as residue instead of rejecting it as unrecordable drift, and a new pre-read step (builder.residueSeedFor) seeds that residue record into the import stub whenever configuredAttrsSeed's static evaluator could not answer. Neither module.ecs_service.aws_ecs_service.this[0]'s task_definition nor module.ecs_service.aws_ecs_task_definition.this[0]'s own block appears in the plan at all any more (asserted by absence, 3d) - WANT_CHANGE_N drops from 2 to 0. What remains is genuinely just the standalone module.ecs_task_definition (volume block, ARM64 runtime platform) replacement (WANT_ADD_N/WANT_DESTROY_N unchanged at 1), confirmed identical in stock's own fresh replan, forced by two residual container_definitions diffs verified both against the emulator API and against real AWS with no tofu in the loop: essential going explicit-true (state) -> absent (config) is a genuine HANDOFF row-3 wall (both floci and real AWS always echo essential explicitly; terraform-provider-aws's own equivalence check does not suppress the resulting diff, independent of the emulator); mountPoints[].readOnly going explicit-false (state) -> absent (config) is a confirmed emulator gap (lex00/floci#131: real AWS leaves readOnly absent when never set, floci was defaulting and echoing it) fixed and tested on branch fix/ecs-mountpoints-readonly, not yet published/repinned - and would not by itself clear this estate, since the essential diff forces the same replacement independently. #371, #378, #372 and #110 remain fixed: #378's two aws_cloudwatch_log_group instances under for_each'd module calls stay out of the plan, and #372's remainder keeps all 5 client-named count instances slotted at migrate time (0 of \$CHANGE_N in-place changes are a bare tofu-slot tag). #371's ABSENT class is still 0; identities confirmed by value against the AWS CLI: \$CLUSTER_ARN, \$TD_SVC_ARN, \$TD_STANDALONE_ARN, and #368's scalable target \$GOT_TARGET_RID."
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
log "  stage 3  test_plan          BLOCKED - #371, #378, #372, #110, #395 and #376 all FIXED here (0 ABSENT, 0 PARENT_UNAVAILABLE, three identities confirmed by value; both for_each'd-module log groups out of the plan with their markers intact; 0 of $CHANGE_N in-place changes are a bare tofu-slot tag; service_connect_configuration confirmed absent by name; module.ecs_service.aws_ecs_service.this[0]'s task_definition and module.ecs_service.aws_ecs_task_definition.this[0]'s own block both confirmed absent from the plan, by exact text) - down to exactly ONE task-definition replacement, which stock proposes too (module.ecs_task_definition, the standalone one, untouched by either fix) - essential is row 3 (stock-parity, confirmed against real AWS), mountPoints[].readOnly is a confirmed row-4 emulator gap (lex00/floci#131, fixed on branch fix/ecs-mountpoints-readonly, not yet repinned)"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
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
