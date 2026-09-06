#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-lambda-simple recipe; run with: just demo-run corpus-lambda-simple)
# The five-stage real-estate crossing pipeline (cold deploy, migrate, test
# plan, test apply, drift and reconverge - live/corpus-crossing-manifest.json)
# for .corpus/lambda/examples/simple, terraform-aws-modules/terraform-aws-
# lambda's minimal entry point - Lambda is one of the most commonly deployed
# AWS services via Terraform, and calling a published module the way this
# example does is how essentially every real Terraform root module uses one.
# Stage 1 (plain terraform, no live block) and stage 2 (choudoufu live-import
# -approve, all three module-nested AWS resources verified by reading their
# tags with the AWS CLI, never through choudoufu's own report) both pass for
# real. Stage 3 currently fails with the real, unmodified choudoufu error:
# type admission runs per declared resource block rather than per resolved
# instance, so aws_lambda_function_url.this and
# aws_lambda_function_recursion_config.this - both count = 0 in this
# example - still refuse the whole plan. See the script's own header for the
# fix stage 2 needed (a module-scope bug in live-import, fixed on this
# branch) and the separate one stage 3 still needs. BREAK=1 corrupts one
# expected tofu-address before stage 2's AWS CLI checks; that step must be
# the only one that fails. Needs Docker, the AWS CLI, python3 and a populated
# .corpus; runs on its own port (4714) so it can run beside `just demo`.
set -uo pipefail

# The five-stage real-estate crossing (see live/corpus-crossing-manifest.json)
# for .corpus/lambda/examples/simple, from terraform-aws-modules/terraform-
# aws-lambda pinned at v8.8.1 (live/corpus-manifest.json). Lambda is one of
# the most commonly deployed AWS services via Terraform, and this module is
# the de facto standard way people provision it; "simple" is its minimal
# entry point: one module call (module.lambda_function), publishing a
# Python function whose name is derived from a random_pet.
#
# Stages:
#   1. COLD DEPLOY   plain `terraform apply`, no live block, no choudoufu
#                     anywhere - the honest proof the estate is real and
#                     buildable, and genuinely unmarked live infra to adopt.
#   2. MIGRATE        `choudoufu live-import -state=... -estate=... -approve`
#                     against that cold state.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     empty AND assert the rendered identity strings.
#   4. TEST APPLY     apply the empty plan, assert a genuine no-op.
#   5. DRIFT + RECONVERGE  mutate one live object out of band, replan,
#                     assert the diff proposes fixing exactly that object.
#
# WHAT THIS RUN ACTUALLY FOUND, first pass (before any fix, since superseded
# below): stage 2 reported "0 of 1 resource instance(s) are eligible for
# stamping" and "7 resource instance(s) in a non-root module were not
# considered (root module only, v1; see issue #59)". Every real resource
# this module creates - the IAM role, its inline log policy, the Lambda
# function, the CloudWatch log group - lives under module.lambda_function.*,
# because calling a published module is how essentially every real Terraform
# root module uses one. `internal/live/liveimport/ratify.go` skipped every
# non-root module wholesale, a restriction its own comments attributed to
# issue #59 - which is CLOSED, and whose closing scope explicitly gave the
# other four root-only walkers (identity, discovery, stamp, projection, mv)
# real module traversal. `live-import` was never updated to match; that was
# a live regression against a shipped capability, not a documented gap.
# Fixed in internal/live/liveimport/ratify.go - three real AWS resources
# stamp correctly with module-qualified tofu-address tags, verified below
# by reading the tags directly with the AWS CLI.
#
# Fixing that uncovered a SECOND blocker at stage 3, filed as #303:
# `choudoufu live-plan` refused the estate outright with "Resource type is
# outside the live-markers subset" for aws_lambda_function_url.this and
# aws_lambda_function_recursion_config.this - even though both have
# `count = ... ? 1 : 0` with the `? 1` condition statically false in this
# example (var.create_lambda_function_url defaults to false;
# var.recursive_loop defaults to null, and the config never overrides
# either), so stock OpenTofu creates zero instances of either type. Type
# admission ran once per declared resource block, not once per resolved
# instance, so a provably-zero-instance block still had to pass admission
# before ANYTHING in the estate planned - a parity gap against the standing
# bar in HANDOFF.md ("if upstream accepts a configuration we refuse, that is
# a defect"). #303 IS NOW FIXED AND MERGED (`blockHasNoInstances`,
# internal/live/lint/admission.go): re-run against current main confirms
# neither type is mentioned anywhere in live-plan's diagnostics any more -
# not as an error, not as a warning.
#
# That fix uncovered a THIRD blocker at stage 3: `local_file.archive_plan`
# (module.lambda_function's package.tf:44, `count = var.create &&
# var.create_package ? 1 : 0`, both true by default, so a real instance)
# refusing with "Logical resource is not admitted". It was called "genuinely
# LAST" here and it was not. It IS NOW FIXED AND MERGED (issue #314): a
# fourth LogicalClass, EXTERNAL_ADMITTED, admits local_file the moment a
# record_store is declared, which this estate already does. Re-run against
# current main confirms local_file appears NOWHERE in live-plan's
# diagnostics any more - not as an error, not as a warning.
#
# Two things about #314 are worth knowing before touching this estate again,
# because both refute what this header used to say. First, #237/#238's
# framing - "its identity is argument-derived, not record-backed" - was only
# half right. hashicorp/local 2.9.0 implements NO import for local_file at
# all (`tofu import local_file.f <path>` answers "Resource Import Not
# Implemented"), so the record is the only carrier that can bring its prior
# state back and it IS record-backed; what its filename argument settles is
# not where the identity lives but why lint's count.index walk must keep
# running over it. Second, this estate's own local_file could never have
# resolved an argument-derived identity anyway: its filename is
# `data.external.archive_prepare[0].result.build_plan_filename`, not a
# static value.
#
# SCOPING CONSIDERED AND REJECTED, and still the right call: could this
# estate route around local_file the way live/e2e/corpus-vpc-complete/run.sh
# and live/e2e/corpus-sumaform-aws/run.sh route around their own
# out-of-scope resources, by setting `create_package = false` +
# `local_existing_package = <a pre-built zip>`? Rejected then and moot now.
# Unlike sumaform's `provision = false` (which picks between the module's
# own equally-real deployment modes to route around an infra-emulation gap
# in floci, not around choudoufu's own admission policy), swapping to a
# pre-built zip would replace the actual thing "simple" demonstrates - the
# module's own packaging pipeline, the default path essentially every real
# minimal deployment of this module takes. #314 fixed the product instead,
# which is what the standing bar asks for.
#
# A FOURTH blocker sits underneath, newly REACHED rather than caused, and it
# is a different wall entirely - nothing to do with logical resources. Five
# errors, and every one of them traces to a single expression in the
# estate's own main.tf:
#
#     function_name = "${random_pet.this.id}-lambda-simple"
#
# `random_pet.this` is RECORD_ADMITTED, so its `id` exists only in the
# record store. Three of the module's real AWS resources take their identity
# from it, directly or through a local:
#
#   iam.tf:97    aws_iam_role.lambda.name          = local.role_name
#                  (local.role_name = coalesce(var.role_name,
#                   var.function_name, "*"))
#   iam.tf:137   aws_iam_role_policy.logs.name     = "${local.policy_name}-logs"
#   main.tf:46   aws_lambda_function.this.function_name = var.function_name
#   main.tf:279  aws_cloudwatch_log_group.lambda.name   = coalesce(
#                   var.logging_log_group, "/aws/lambda/${...}${var.function_name}")
#
# plus one cascade (aws_iam_role_policy.logs.role reads
# aws_iam_role.lambda[0].name, which failed above). The diagnostic is
# "Non-static identity argument" / "Unresolvable identity", not a logical or
# an admission refusal.
#
# What makes this worth filing rather than shrugging at: choudoufu KNOWS
# this value. The record store holds random_pet.this, internal/live/
# projection reads records to hydrate record-backed instances, and all three
# affected AWS resources are ALREADY MARKED - stage 2 below stamps them and
# verifies the tags through the AWS CLI. So this is an identity resolver
# that will not read a carrier the run already has, over resources whose
# markers already say which object they are. Whether the fix is the record
# (feed a record-backed resource attribute into static evaluation) or the
# marker (an adoption-only refusal firing on a resource that carries one) is
# a real design question and not this script's to answer.
#
# One piece of good news from the earlier investigation, still true: issue
# #275 (closed 2026-08-18) built a residue mechanism specifically for
# arguments like aws_lambda_function's own `filename`/`source_code_hash`/
# `publish` - pure configuration inputs with no API-readable counterpart -
# gated on a configured `record_store`. This estate declares one, so nothing
# here should re-hit #275's problem on the Lambda function itself once the
# identity wall above clears.
#
# THE FOURTH BLOCKER IS FIXED (issue #336, 2026-08-19) AND THE ISSUE'S OWN
# READING OF IT WAS WRONG, which is worth knowing before touching this
# estate again. It was filed as "the identity resolver declines to read the
# record store". It does not decline: internal/live/identity's parentPart
# already has a record-backed branch, and namedLeaf already carries the
# resulting formula across a module-call argument - which is why
# aws_lambda_function.this, whose function_name is a bare var.function_name,
# resolved on main before anything was fixed. What refused was coalesce().
# All three of the module's other identity chains go through one
# (iam.tf:14, iam.tf:15, main.tf:279), every argument of all three is a var
# or a local, so resolver.isSymbolic saw no managed resource in them and
# never sent them to the structural decomposition that would have found it.
# resolveCoalesceCall and resolveSelection (internal/live/identity/
# coalesce.go) close that. Re-run against current main: live-plan raises
# ZERO diagnostics for this estate and all four AWS resources resolve, three
# of them newly - aws_iam_role.lambda and aws_lambda_function.this to
# ${random_pet.this.id}-lambda-simple, aws_cloudwatch_log_group.lambda to
# /aws/lambda/${random_pet.this.id}-lambda-simple, aws_iam_role_policy.logs
# to the role:policy composite over the same.
#
# A FIFTH blocker sits underneath it, newly REACHED rather than caused, and
# it refutes the premise the fourth was filed on. live-plan now completes
# and proposes CREATING all eight resources, starting with
#
#     # random_pet.this will be created
#
# because the record store is EMPTY after a clean migrate. `live-import
# -approve` writes markers, and for every stamped entry it also records
# issue #327's residue - but a record-backed resource is not stampable, so
# it is OutcomeSkipped and Approve `continue`s before recordResidueFor is
# ever reached (internal/live/liveimport/stamp.go). random_pet.this's
# generated id is therefore lost at migration, its whole object plans as a
# create, and every identity derived from it - which, in this estate, is
# every identity there is - has a parent with no value to render from. So
# "the record store already holds random_pet.this, that's what made it
# eligible for stamping" was false on both halves: nothing wrote the record,
# and being record-backed is precisely what made it INeligible.
#
# THE FIFTH BLOCKER IS FIXED (issue #340, 2026-08-20). Approve now has a
# second write path beside the tag write: for every instance whose type
# identity.TypeIdentity.RecordBacked marks - fifteen types across four
# providers, read off the generated table, no type name in Go - it seeds the
# estate's record store from the state's own object
# (projection.SeedRecordForInstance). Step 6 below asserts the new outcome
# counts AND greps the store's own files for random_pet.this's generated id,
# and step 8 asserts by ABSENCE that not one record-backed resource is
# proposed for creation any more.
#
# Measured on this estate, 2026-08-20, against floci:
#   STAGE 1 (cold deploy)  PASS
#   STAGE 2 (migrate)      PASS - 3 stamped, 4 recorded, 0 failed, 1 skipped
#   STAGE 3 (test plan)    BLOCKED, but on a SIXTH wall and a different kind:
#                          live-plan raises ZERO diagnostics, every identity
#                          resolves, all four record-backed resources come
#                          back out of the store - and the plan is "0 to add,
#                          2 to change, 0 to destroy".
#
# The two remaining changes, from the run's own dump (step 8 prints it):
#
#   module.lambda_function.aws_lambda_function.this[0]
#       - environment {}
#       + logging_config { log_format = "Text" }
#     plus the computed re-derivation those force (version, qualified_arn,
#     qualified_invoke_arn -> known after apply). A nested-block round-trip
#     between what floci's Lambda read returns and what the module's config
#     declares, not an identity or a record question.
#
#   module.lambda_function.local_file.archive_plan[0]
#       ~ content = (sensitive value)
#     with OpenTofu's own renderer saying "The value is unchanged" - a
#     SENSITIVITY-ONLY diff. hashicorp/local marks local_file.content
#     sensitive; states.ResourceInstanceObjectSrc.Decode re-applies that mark
#     from the state's AttrSensitivePaths; and projection's recordPayload had
#     nowhere to put a sensitivity path, so the migrate unmarked before
#     ctyjson could encode it. The record therefore carried the value and not
#     the mark, and because live-plan runs with SkipRefresh nothing ever put
#     the mark back: the plan's "before" side had no marks while its "after"
#     side was re-marked from the provider schema on every run.
#
# THE SECOND OF THOSE IS FIXED (2026-08-20). projection.recordPayload gained
# SensitiveAttrs, encoded exactly the way a state file encodes
# "sensitive_attributes" - the paths travel beside the value, encodeRecordPayload
# splits them off instead of the caller unmarking, decodeRecordPayload puts them
# back, and materializeRecord re-marks after the schema conversion so
# obj.Encode derives AttrSensitivePaths from them. It is derived from the
# OBJECT's own marks, so it covers any record-backed type with any sensitive
# attribute, not local_file.content. A mark that is not sensitivity is refused
# rather than dropped. Step 8 asserts it by ABSENCE.
#
# THE FIRST WAS NOT OURS, AND IS NOW FIXED (2026-08-20, lex00/floci#83).
# Step 3b used to prove stock terraform - its own state file, its own
# refresh, immediately after its own cold apply, no choudoufu anywhere in the
# run - proposed the SAME aws_lambda_function update this estate's live-plan
# did. Two response-shape gaps in the emulator's GetFunction/
# GetFunctionConfiguration:
#
#   * Environment was emitted unconditionally ("SDK expects it even when
#     empty", LambdaController.buildFunctionConfiguration). Real AWS omits it
#     for a function that never had one - verified against a real account
#     2026-08-20, which is why terraform-provider-aws reads it under
#     `if function.Environment != nil`. The module declares zero environment
#     blocks (main.tf:90, a dynamic block over an empty map), so a
#     present-but-empty Environment read back as one block and the plan said
#     "- environment {}".
#   * LoggingConfig was never emitted and never stored. Real AWS always
#     returns one, defaulting to Text/`/aws/lambda/<name>` - also verified
#     against a real account 2026-08-20; the module always declares one
#     (main.tf:136, log_format "Text"), so the plan said
#     "+ logging_config { log_format = "Text" }".
#
# Both are fixed in floci 94ca0669 (published as sha256:f068fa6b via
# 720727be, re-pinned in live/floci-image the same day). Re-run against the
# new pin: step 3b's stock-terraform control now names ZERO resource-level
# changes at all, confirmed by reading the raw `terraform plan
# -detailed-exitcode` output directly (not through this script's own
# extraction) immediately after a real cold apply. The one line left in
# stock's own control is an OUTPUT diff (outputs.tf:49's
# `try(aws_lambda_function.this[0].kms_key_arn, "")`), not a resource
# attribute - see step 3b's own comment.
#
# A SIXTH BLOCKER SITS UNDERNEATH, newly REACHED rather than caused, and it
# is real - filed as issue #348 rather than assumed away. `live-plan` itself
# raises ZERO diagnostics, resolves every identity, and proposes changing
# ZERO resources - there is no "OpenTofu will perform the following
# actions" block at all - and the plan is STILL not empty, because ALL 23 of
# this example's own root-level `output` blocks render as
# "Changes to Outputs: + <name> = <value>" on every single run.
# internal/live/projection.Manager.GetRootOutputValues (the statemgr
# interface live-plan asks for the "prior" side of an output diff) always
# returns an empty map, because nothing evaluates the configuration's
# `output` blocks against the prior resource state live-plan reconstructs
# from markers before the plan graph asks for them - there is no carrier for
# an output value the way there is for a resource identity, and nothing
# fills the gap that leaves. Stock Terraform does not hit this because its
# refresh step recomputes outputs against a REAL persisted state file's
# prior values; choudoufu never persists one. Verified this is genuinely
# new, not a pre-existing gap this estate happened to reach first:
# corpus-mastino-dns and corpus-evoteum-modules, the two crossings closest
# to 5 of 5, both declare ZERO root-level outputs.
#
# THE SIXTH BLOCKER IS NOW DOWN TO ONE OUTPUT LINE. #348 (evaluate the root
# output blocks at all) and #349's zero-instance half took 23 down to 2, and
# #349's second half - a data-source read driven by root-output demand rather
# than by identity demand - took 2 down to 1. Measured 2026-08-21 with this
# identical script run twice against floci, only TOFU_BIN swapped:
#
#   860c29e129 (before)   + lambda_function_arn_static, + local_filename
#   with the fix          + local_filename
#
# lambda_function_arn_static reads data.aws_partition.current.partition,
# data.aws_region.current.region and data.aws_caller_identity.current.
# account_id (module.lambda_function's main.tf:1-3) - three no-count AWS data
# sources that stock OpenTofu reads synchronously on every plan and that
# choudoufu never read, because nothing demanded them: the pre-resolution
# data-read phase derives its demand by probing IDENTITY resolution, and no
# identity in this estate reads them. internal/live/dataread now has a second
# demand class for what root outputs reach, read best-effort through the same
# provider instances the projection already uses, and the values are seeded
# into the state internal/live/projection's ApplyRootOutputValues evaluates
# against. The output does not merely have a value now: it is gone from the
# diff entirely rather than rendering "~ old -> new", so the plan graph
# computed the same value independently and the two sides cancel.
#
# local_filename was the one line left, and the paragraph that used to stand
# here said it stayed. It no longer does. Keeping the reasoning, because the
# half of it that held is what shaped the fix:
#
# It reaches data.external.archive_prepare (package.tf:7), whose read RUNS
# package.py on the machine running the plan. Everything else about that
# block is readable - count = 1, static arguments, no provider configuration
# to evaluate - and what stops it is deliberately not any of those: the
# root-output read class is confined to providers this configuration manages
# live objects through (dataread.LiveProviders), and hashicorp/external
# serves no managed resource type at all, in this or any configuration. That
# confinement is still in force and is still right. A plan that ran a local
# program would stop being a pure preview, which is the same invariant that
# keeps provisioners to apply only.
#
# What was wrong was the conclusion drawn from it - that the line therefore
# had to stay. Reading the value earlier was never the only way to have it.
# Stock does not compute an output's prior value at all: it REMEMBERS the one
# the last apply settled on, out of its own state file. A migration reads
# that state file, and until #349's last rung, dropped every output value in
# it on the floor - a hole in HANDOFF.md's "migration from a stock state file
# is lossless" exactly the size of the estate's outputs.
#
# So the carrier is the record store, sixth namespace, "tofu-outputs/<estate>"
# (internal/live/projection/rootoutput.go). live-import writes what the stock
# state held; an apply's write-back keeps it current;
# projection.ApplyRootOutputValues consults it ONLY for an output it could
# not evaluate at all, which is what bounds the change - a remembered value
# can add a prior value where there was none, and can never displace one the
# projection computed. Step 6 below asserts the carried value against the
# cold stock state BY VALUE, because an empty plan is convergence and
# convergence is never evidence a value is right.
#
# With that, stage 3's output diff is empty for the first time and this
# estate's plan is genuinely empty end to end.
#
# Stages 4 and 5 remain to be written below, following
# live/e2e/corpus-mastino-dns/run.sh's shape, once stage 3 has a real empty
# plan to build them on.
#
# Measured again on this estate, 2026-08-21, against floci:
#   STAGE 1 (cold deploy)  PASS
#   STAGE 2 (migrate)      PASS - 3 stamped, 4 recorded, 0 failed, 1 skipped
#   STAGE 3 (test plan)    BLOCKED, on one output line: "+ local_filename".
#                          Zero diagnostics, zero resource-level changes.
#   STAGES 4 and 5         NOT REACHED, and not yet written.
#
# RE-CROSSED for real, 2026-08-21, against floci cdd50ec0, after the
# data-read safety audit widened the phase's provider boundary to cover the
# IDENTITY read class as well as the root-output one. Byte-identical result:
# stage 1 PASS, stage 2 PASS (3 stamped, 4 recorded), stage 3 BLOCKED on the
# same single "+ local_filename" line with zero diagnostics and zero
# resource-level changes. The widening does not touch this estate, and the
# reason is worth knowing rather than guessing at: nothing here demands
# data.external.archive_prepare for an IDENTITY. local_file.archive_plan's
# filename reads it, but local_file is record-backed and the migrate seeded
# its record, so the identity class never asks. The one output line is the
# root-output class refusing it, exactly as before.
#
# RE-CROSSED for real, 2026-08-21, against floci cdd50ec0, after #349's last
# rung (the "tofu-outputs" record namespace) landed. This is the run that
# moved the estate:
#   STAGE 1 (cold deploy)  PASS - 8 resources, genuinely cold and unmarked
#   STAGE 2 (migrate)      PASS - 3 stamped, 4 recorded, 0 failed, 1 skipped,
#                          and local_filename's remembered value asserted by
#                          value against the cold stock state:
#                          builds/b982f072d0f3e8eba2708ddda345f11fcb6ae4d4ecdfa3be269021f62bde988a.zip
#   STAGE 3 (test plan)    PASS - "No changes. Your infrastructure matches
#                          the configuration." Zero diagnostics, zero
#                          resource changes, zero output lines.
#   STAGES 4 and 5         NOT REACHED, and still not written - stage 3 only
#                          started passing in this run, which is the whole of
#                          why the estate is not yet clear.
#
# The measurement was taken twice with the identical harness and only the
# binary swapped: at 8095eba176 the plan carries "+ local_filename = ..." and
# test_plan reads fail; with the fix the "Changes to Outputs:" block is gone
# entirely and test_plan reads pass.
#
# STAGE 4 IS NOW WRITTEN, since stage 3 has a real empty plan to build it on.
# It applies that empty plan and asserts a genuine no-op: "0 added, 0 changed,
# 0 destroyed", no state file left behind, all three module-nested markers
# read back unmoved, and the record store (random_pet.this's generated id,
# the carried root output) still present - the same shape as
# reference-ec2-vpc's and corpus-mastino-dns's own test_apply blocks.
#
# One real, non-choudoufu gap surfaced while writing it: floci's
# resourcegroupstaggingapi GetResources does not index CloudWatch Logs log
# groups. Queried directly against this estate it returns only the IAM role
# and the Lambda function - 2 of the 3 tagged objects - even though the log
# group's OWN tag API (logs:list-tags-for-resource, already used at stage 2's
# marker check) reads its tags back correctly, and AWS's own GetResources
# documents "logs:loggroup" as a supported resource type. Rather than assert
# the object count through a cross-service search that gap would silently
# under-report, the object count here is the sum of each of the three
# objects' own tag reads - the same precedent as corpus-sumaform-aws routing
# around an infra-emulation gap rather than around choudoufu's own policy.
# Not filed as a floci issue by this change since it does not block the
# stage; worth one if a later estate needs the cross-service search itself.
#
#   bash live/e2e/corpus-lambda-simple/run.sh
#
# Needs Docker, the AWS CLI, and python3 (the module's package.py builds the
# deployment zip locally through a `data "external"` block - no network).
# .corpus is read, never written: the estate is copied out to a temp
# directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4714).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before the
#                stage-2 tag assertions, proving they are load-bearing
#                rather than a grep that always matches. It does not affect
#                stages 3 or 4.
#   BREAK_COUNT  set to 1 to run day2_count's own negative control (PART G)
#                instead of its real checks: after the real scale-down plan,
#                assert the WRONG instance (aws_iam_role.count_test[0], the
#                survivor) was the one destroyed. That assertion must not
#                hold, so the stage reports fail - which is what proves G2's
#                real assertion is load-bearing. Independent of BREAK,
#                BREAK_RENAME and BREAK_REMOVE.
#   BREAK_APPROVAL
#                set to 1 to run plan_approval's own negative control
#                instead of the real refusal check (PART P): after the world
#                has moved out of band, assert the saved plan file APPLIES
#                cleanly - the Break text in tools/gauntlet/stages.go for
#                plan_approval is literally "Apply the planfile after a
#                mutation and expect success; the run must refuse", so this
#                assertion has to fail. Independent of every BREAK above,
#                and the only one of them under which PART P runs at all -
#                the others deliberately leave the estate somewhere PART P
#                does not describe, and it reports no verdict there.
#
# Exit codes: 0 on a real pass of every stage this script currently
# exercises (stages 1 through 4; stage 5 is not yet written), non-zero on a
# real failure. Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_MODULE="$ROOT/.corpus/lambda"
SRC_EXAMPLE="$ROOT/.corpus/lambda/examples/simple"
SRC_FIXTURES="$ROOT/.corpus/lambda/examples/fixtures"
WORK="$(mktemp -d)"
EST="$WORK/lambda/examples/simple"
FLOCI_PORT="${FLOCI_PORT:-4714}"
FLOCI_NAME="choudoufu-corpus-lambda-simple-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# SEPARATE namespace stock applies the identical config into as that stage's
# own oracle.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-lambda-simple-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-lambda-simple-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"

ESTATE="lambda-simple-crossing"
GREEN_ESTATE="lambda-simple-greenfield"
REGION="eu-west-1"
ACCOUNT="000000000000"

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
gauntlet_begin
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# changed_addrs_excluding_markers: reads a `plan -no-color` transcript on
# stdin, prints one changed resource address per line, EXCLUDING any address
# whose only proposed change is the tofu-address/tofu-estate marker tags.
# Stage 5's stock oracle plans against infra that choudoufu's own migrate
# step (stage 2) already tagged for real, through the AWS API - stock's state
# knows nothing about those tags, so its replan proposes removing them from
# every tagged object, which is marker noise, not the out-of-band mutation
# under test. This is the "marker tags normalised out of both plans" the
# stage's oracle text calls for, applied to both choudoufu's plan and
# stock's (choudoufu's plan may carry the same noise if a tagged object's
# marker were ever out of sync, though it should not be here).
FILTER_MARKERS_PY="$WORK/filter_changed_addrs.py"
cat > "$FILTER_MARKERS_PY" <<'PY'
# Reads a `plan -no-color` transcript on stdin, prints one changed resource
# address per line, EXCLUDING any address whose only proposed change is the
# tofu-address/tofu-estate marker tags. A file, not a `python3 - <<PY`
# heredoc: the latter feeds the script itself to python3's stdin, leaving
# nothing left on stdin for sys.stdin.read() below to read.
import re, sys

text = sys.stdin.read()
lines = text.split("\n")
header_re = re.compile(r'^  # (\S+) will be (.+)$')
headers = [(i, m.group(1)) for i, line in enumerate(lines) for m in [header_re.match(line)] if m]

MARKER_KEYS = ("tofu-address", "tofu-estate")
changed = []
for idx, (i, addr) in enumerate(headers):
    end = headers[idx + 1][0] if idx + 1 < len(headers) else len(lines)
    block = lines[i:end]
    real_change = False
    for line in block[1:]:
        stripped = line.strip()
        if not stripped or not re.match(r'^[~+-]', stripped):
            continue
        if any(k in stripped for k in MARKER_KEYS):
            continue
        if re.match(r'^[~+-]\s*(resource\b|tags(_all)?\s*=)', stripped):
            continue
        real_change = True
    if real_change:
        changed.append(addr)

print("\n".join(sorted(set(changed))))
PY
changed_addrs_excluding_markers() {
  python3 "$FILTER_MARKERS_PY"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH - package.py needs it to build the deployment zip"
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_EXAMPLE" ] || fail "$SRC_EXAMPLE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_FIXTURES" ] || fail "$SRC_FIXTURES is missing - run 'just corpus-fetch' first"

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

# .corpus is shared across every worktree and is NEVER written to: the
# module, the example, and the fixtures the example's source_path reaches
# are copied out, preserving the relative paths main.tf's
# `source = "../../"` and `source_path = ["${path.module}/../fixtures/..."]`
# both expect.
mkdir -p "$WORK/lambda"
rsync -a --exclude 'examples' --exclude 'tests' --exclude '.git' "$SRC_MODULE/" "$WORK/lambda/"
mkdir -p "$WORK/lambda/examples/simple" "$WORK/lambda/examples/fixtures"
cp -R "$SRC_EXAMPLE/." "$EST/"
cp -R "$SRC_FIXTURES/." "$WORK/lambda/examples/fixtures/"
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  module + example + fixtures copied out of .corpus into $WORK"

# ── 1. the one delta - emulator flags, no live block yet ───────────────────
gauntlet_begin_stage cold_deploy
log "=== 1. cold deploy: plain terraform, no live block, no choudoufu ==="
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)(.*?\n)(\}\n)/$1  access_key                  = "test"\n  secret_key                  = "test"\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n$2$3/s' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
log "  DELTA 1  emulator flags added to the provider block; no backend needed"

# DELTA 2: pin the AWS provider to the release this fork's own tables are
# derived at, the same way corpus-cloudfront and every other crossing that
# carries a pin does.
#
# This script used to say "no version pin needed" and it was wrong twice
# over. The estate asks for `>= 6.28`, so it silently followed whatever
# hashicorp/aws had published most recently - which means the crossing was
# not reproducible (a run today and a run last week measured different
# providers), and it was measuring a provider the admission table, the
# import grammar and live/survey.json were never derived against. Both are
# methodology problems, not conveniences; the speed is a side effect.
#
# 6.59.0 is live/corpus-provider-pins.json's verified release and the
# version tools/survey-gen and tools/row-gen measured. `>= 6.28` accepts it,
# in both the example and the module (.corpus/lambda/versions.tf).
perl -0pi -e 's/(aws = \{\n      source  = "hashicorp\/aws"\n)      version = ">= 6\.28"/$1      version = "= 6.59.0"/' "$EST/versions.tf"
grep -q 'version = "= 6.59.0"' "$EST/versions.tf" \
  || fail "the provider-pin delta did not match versions.tf - the corpus pin has moved"
log "  DELTA 2  hashicorp/aws pinned to = 6.59.0, the release this fork's tables are derived at"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"lambda"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"lambda"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (lambda) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

log "=== 3. cold init and apply: plain terraform, 8 resources from nothing ==="
# The shared plugin cache, the same way corpus-alb-complete's crossing uses
# it. Without it every run re-downloads hashicorp/aws 6.59.0 (several hundred
# megabytes) from the registry, which on a machine running more than one
# crossing at a time takes longer than the rest of this script put together
# and makes the estate look hung. It changes nothing about what is measured.
#
# #339: TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE closes the gap a warm
# cache alone does not - without it, init in a directory with no
# .terraform.lock.hcl re-downloads the whole provider purely to compute
# checksums, even when the cache already holds that exact version. Real
# terraform and choudoufu both honor it (see live/e2e/README.md, "The shared
# plugin cache" for the measured numbers).
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"
( cd "$EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "cold terraform init failed"; }
COLD_APPLY_OUT="$(cd "$EST" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$COLD_APPLY_OUT" | tail -40
  fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 8 added' <<< "$COLD_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_APPLY_OUT"; fail "the cold apply did not create exactly 8 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_APPLY_OUT" | head -1)"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

PET="$(python3 -c "
import json
s = json.load(open('$EST/terraform.tfstate'))
for r in s['resources']:
    if r['type'] == 'random_pet' and r['name'] == 'this':
        print(r['instances'][0]['attributes']['id'])
")"
[ -n "$PET" ] || fail "could not read random_pet.this's id back out of the cold state"
FN_NAME="${PET}-lambda-simple"
log "  function_name resolved to $FN_NAME (random_pet.this = $PET)"

# Confirmed unmarked: plain terraform never wrote a tofu-address tag.
LAMBDA_ARN="arn:aws:lambda:${REGION}:${ACCOUNT}:function:${FN_NAME}"
COLD_TAGS="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'length(Tags)' --output text 2>/dev/null || echo 0)"
[ "$COLD_TAGS" = "0" ] || fail "the cold-deployed function already carries $COLD_TAGS tag(s) before migration - this test proves nothing"
log "  confirmed unmarked: $LAMBDA_ARN carries no tags"

cp "$EST/terraform.tfstate" "$WORK/cold.tfstate"

# A snapshot of the pre-live-block config (DELTA 1 + DELTA 2, no live block),
# taken before step 4 below adds one to $EST/versions.tf. Stage 5's stock
# oracle needs a plain-terraform working directory that still points at the
# same floci endpoint with no choudoufu involvement at all, and $EST itself
# stops being that the moment migration starts.
#
# The WHOLE $WORK/lambda tree is snapshotted, not just $EST: the example's
# module block is "source = \"../../\"", relative to the example directory's
# own depth under the module root, and copying only $EST would leave that
# path resolving to nothing (or to the wrong directory) once terraform init
# runs a second time from a differently-nested copy.
cp -a "$WORK/lambda" "$WORK/stocklambda"
STOCKDRIFT="$WORK/stocklambda/examples/simple"
rm -rf "$STOCKDRIFT/.terraform" "$STOCKDRIFT/.terraform.lock.hcl" "$STOCKDRIFT/terraform.tfstate" "$STOCKDRIFT/terraform.tfstate.backup"
log "  snapshot of the pre-live-block config saved to $STOCKDRIFT, for stage 5's stock oracle"

# ── 3b. the EMULATOR's own drift, measured before choudoufu exists ─────────
# Stock terraform, its own state file, its own refresh, immediately after its
# own apply. Whatever it proposes here is the emulator disagreeing with the
# provider about what it just created, and choudoufu is not in the room.
#
# This exists because stage 3 below has to say which half of a non-empty
# replan is ours. Reasoning about it from the diff alone has been wrong here
# before; a control run is the only thing that settles it.
STOCK_REPLAN_OUT="$(cd "$EST" && terraform plan -input=false -no-color -detailed-exitcode 2>&1)"; STOCK_REPLAN_RC=$?
case "$STOCK_REPLAN_RC" in
  0) log "  control: stock terraform replans EMPTY against the emulator - no emulator drift" ;;
  2) log "  control: stock terraform's OWN replan is not clean-exit, with no choudoufu involved:" ;;
  *) printf '%s\n' "$STOCK_REPLAN_OUT" | tail -20; fail "the stock control replan failed to run at all (exit $STOCK_REPLAN_RC)" ;;
esac
STOCK_DRIFTED="$(grep -E '^  # ' <<< "$STOCK_REPLAN_OUT" | sed 's/^  # //' | sort)"
if [ -n "$STOCK_DRIFTED" ]; then
  printf '%s\n' "$STOCK_DRIFTED" | sed 's/^/    /'
fi
# lex00/floci#83 IS FIXED (2026-08-20, floci 94ca0669, published sha256:f068fa6b
# via 720727be): GetFunction/GetFunctionConfiguration used to emit an
# Environment block for a function that never had one, and never returned
# LoggingConfig at all. Re-run against the re-pinned image: stock terraform's
# own replan now names ZERO resource-level changes - no
# "aws_lambda_function.this[0] will be updated in-place", no environment, no
# logging_config. STOCK_DRIFTED (every "  # ... will be" line) is asserted
# empty below, by absence rather than by eye.
#
# The control's exit code is still 2, not 0, and that is NOT #83's carve-out
# reopening: OpenTofu's own trailer prints exactly one thing changing,
#
#     Changes to Outputs:
#       + lambda_function_kms_key_arn      = ""
#
# an OUTPUT going from absent to "", not a resource attribute. It comes from
# outputs.tf:49's `try(aws_lambda_function.this[0].kms_key_arn, "")`
# recomputing against the state the cold apply just wrote, which is a
# property of running `terraform plan` against a real state file - and
# choudoufu's live-plan has no state file to diff an output's prior value
# against in the first place (issue #73's whole premise), so this cannot
# reach stage 3 as choudoufu-attributable drift. It is asserted here by
# EXCLUSION - the grep this control's own STOCK_DRIFTED reads only ever
# matches a "  # <address> will be ..." resource line, never an output-only
# "Changes to Outputs:" trailer, so it does not need its own carve-out to
# stay silent about this.
WANT_STOCK_DRIFTED=""
if [ "$STOCK_DRIFTED" != "$WANT_STOCK_DRIFTED" ]; then
  fail "stock terraform's own replan drifts on:
$STOCK_DRIFTED
and this script now expects NO resource-level drift at all (lex00/floci#83 is
fixed). Either the emulator has grown a NEW gap that stage 3 would otherwise
blame on choudoufu, or #83 has regressed."
fi
log "  control: zero resource-level drift - lex00/floci#83 is fixed. The only"
log "  remaining stock-terraform diff is an output-only value (outputs.tf's"
log "  kms_key_arn try()), which live-plan cannot reach because it has no"
log "  prior state to diff an output against at all."

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "8 resources, genuinely cold, genuinely unmarked"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, active - live/GAUNTLET.md #13)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: greenfield means from
# nothing, so this never touches the objects stage 1's plain terraform apply
# created (those get migrated in stage 2, below). choudoufu applies the
# IDENTICAL reduced example (DELTA 1 + DELTA 2, no other change) directly,
# with a live block from the start, no migration, no state file ever
# existing; the record store must hold every record-backed instance
# (random_pet.this, local_file.archive_plan, null_resource.archive,
# terraform_data.package_filename_for_hash - the same four stage 2's own
# migrate step records); and the estate's own oracle is stock applying the
# SAME config fresh in a THIRD, independent namespace, compared structurally
# via the AWS CLI on both endpoints, never through tofu state. $WORK/
# stocklambda is already DELTA 1 + DELTA 2 with no live block and no state -
# exactly the base both fresh copies below need - taken above right after
# the cold apply.
gauntlet_begin_stage greenfield
log "=== PART GREENFIELD: 0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"lambda"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"lambda"' <<< "${GH:-}" || fail "floci did not come up healthy (lambda) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

cp -a "$WORK/stocklambda" "$WORK/lambda-greenfield"
GREEN_EST="$WORK/lambda-greenfield/examples/simple"
perl -0pi -e 's/(random = \{\n      source  = "hashicorp\/random"\n      version = ">= 2.0"\n    \}\n  \}\n)\}/$1\n\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$GREEN_EST/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 8 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 8 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

# The greenfield namespace holds exactly one function - a fresh account with
# nothing else in it - so its own name, read straight off Lambda's own
# ListFunctions, is what function_name actually resolved to; no need to
# reconstruct random_pet.this's generated id from the record store.
GREEN_FN_NAME="$(awsg lambda list-functions --query 'Functions[0].FunctionName' --output text)"
[ -n "$GREEN_FN_NAME" ] && [ "$GREEN_FN_NAME" != "None" ] || fail "no live function found in the greenfield namespace through the AWS CLI"
log "  function_name resolved to $GREEN_FN_NAME"

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_LAMBDA_ARN="arn:aws:lambda:${REGION}:${ACCOUNT}:function:${GREEN_FN_NAME}"
GREEN_LAMBDA_ADDR="$(awsg lambda list-tags --resource "$GREEN_LAMBDA_ARN" --query 'Tags."tofu-address"' --output text)"
[ "$GREEN_LAMBDA_ADDR" = "module.lambda_function.aws_lambda_function.this:0" ] || fail "the greenfield function carries tofu-address=$GREEN_LAMBDA_ADDR, not module.lambda_function.aws_lambda_function.this:0"
GREEN_ROLE_ADDR="$(awsg iam list-role-tags --role-name "$GREEN_FN_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_ROLE_ADDR" = "module.lambda_function.aws_iam_role.lambda:0" ] || fail "the greenfield role carries tofu-address=$GREEN_ROLE_ADDR, not module.lambda_function.aws_iam_role.lambda:0"
GREEN_LOGGROUP_ARN="arn:aws:logs:${REGION}:${ACCOUNT}:log-group:/aws/lambda/${GREEN_FN_NAME}"
GREEN_LOGGROUP_ADDR="$(awsg logs list-tags-for-resource --resource-arn "$GREEN_LOGGROUP_ARN" --query 'tags."tofu-address"' --output text 2>/dev/null \
  || awsg logs list-tags-log-group --log-group-name "/aws/lambda/${GREEN_FN_NAME}" --query 'tags."tofu-address"' --output text)"
[ "$GREEN_LOGGROUP_ADDR" = "module.lambda_function.aws_cloudwatch_log_group.lambda:0" ] || fail "the greenfield log group carries tofu-address=$GREEN_LOGGROUP_ADDR, not module.lambda_function.aws_cloudwatch_log_group.lambda:0"
log "  all three module-nested markers verified via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds every record-backed instance (#364 A2) ==="
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN_EST/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" = "8" ] || fail "expected 8 records under the local record store after the greenfield apply (3 taggable + random_pet + local_file + null_resource + terraform_data + one for the config-derived aws_iam_role_policy.logs), found $GREEN_RECORD_FILES"
log "  $GREEN_RECORD_FILES records persisted, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qE '^  # .+ will be' <<< "$GREEN_PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan proposes a resource change"; }
log "  no resource change proposed"

log "=== PART GREENFIELD: 5. stock oracle - the identical config applied fresh in its own namespace ==="
cp -a "$WORK/stocklambda" "$WORK/lambda-greenfield-oracle"
ORACLE_EST="$WORK/lambda-greenfield-oracle/examples/simple"
( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 8 added' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 8 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

ORACLE_PET="$(python3 -c "
import json
s = json.load(open('$ORACLE_EST/terraform.tfstate'))
for r in s['resources']:
    if r['type'] == 'random_pet' and r['name'] == 'this':
        print(r['instances'][0]['attributes']['id'])
")"
ORACLE_FN_NAME="${ORACLE_PET}-lambda-simple"

log "=== PART GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
lambda_shape() { # $1=endpoint $2=function-name - a normalised structural
                  # fact sheet, read via the AWS CLI, never through tofu state.
  aws --endpoint-url "$1" --region "$REGION" lambda get-function-configuration --function-name "$2" \
    --query '[Runtime,Handler,MemorySize,Timeout]' --output json 2>/dev/null \
  | jq -S .
}
loggroup_shape() { # $1=endpoint $2=log-group-name
  aws --endpoint-url "$1" --region "$REGION" logs describe-log-groups --log-group-name-prefix "$2" \
    --query 'logGroups[0].retentionInDays' --output json 2>/dev/null
}
GREEN_LAMBDA_SHAPE="$(lambda_shape "$GREEN_ENDPOINT" "$GREEN_FN_NAME")"
ORACLE_LAMBDA_SHAPE="$(lambda_shape "$ORACLE_ENDPOINT" "$ORACLE_FN_NAME")"
if [ "${BREAK:-}" = "7" ]; then
  GREEN_LAMBDA_SHAPE='"tampered-by-BREAK"'
  log "  BREAK=7: tampered the expected greenfield function shape - the comparison below must fail"
fi
[ "$GREEN_LAMBDA_SHAPE" = "$ORACLE_LAMBDA_SHAPE" ] || { printf 'greenfield: %s\noracle:     %s\n' "$GREEN_LAMBDA_SHAPE" "$ORACLE_LAMBDA_SHAPE"; fail "the greenfield function differs structurally (runtime/handler/memory/timeout) from the stock oracle"; }
GREEN_LOGGROUP_SHAPE="$(loggroup_shape "$GREEN_ENDPOINT" "/aws/lambda/${GREEN_FN_NAME}")"
ORACLE_LOGGROUP_SHAPE="$(loggroup_shape "$ORACLE_ENDPOINT" "/aws/lambda/${ORACLE_FN_NAME}")"
[ "$GREEN_LOGGROUP_SHAPE" = "$ORACLE_LOGGROUP_SHAPE" ] || { printf 'greenfield: %s\noracle:     %s\n' "$GREEN_LOGGROUP_SHAPE" "$ORACLE_LOGGROUP_SHAPE"; fail "the greenfield log group's retention differs from the stock oracle"; }
log "  runtime, handler, memory, timeout and log-group retention match structurally between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "8 resources from nothing (3 taggable + 5 record-backed/config-derived), all three module-nested markers verified via the AWS CLI, 8 records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally (runtime, handler, memory, timeout, log-group retention)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# module.lambda_function is this estate's only real module and carries all
# 3 of its taggable resources (aws_lambda_function.this[0], aws_iam_role.
# lambda[0], aws_cloudwatch_log_group.lambda[0] - see the header's own
# accounting: "3 stamped"); random_pet.this is the estate's only other real
# resource and is record-located (RECORD_ADMITTED per the header above -
# #340), which live-mv explicitly does not support renaming yet (issue
# #270: "renaming it means moving a record store key, which live-mv does
# not do"). So both day2_rename mechanisms run on the SAME module, one
# after the other, rather than on two different objects: a `moved` block
# first (module.lambda_function -> module.lambda_function_moved), then
# "choudoufu live-mv" second (module.lambda_function_moved ->
# module.lambda_function_final, no moved block for that hop at all, one
# live-mv call per taggable resource since live-mv moves one resource
# instance at a time and none of the three are ParentRef children of
# another). The stock oracle below plans the NET rename (original name
# straight to the final name) on a copy of cold_deploy's own state, before
# choudoufu or live-import ever touch it.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE. stock: the net module rename, through one moved block, on cold_deploy's own state ==="
ORACLE_ROOT="$WORK/oracle"
cp -r "$WORK/lambda" "$ORACLE_ROOT"
ORACLE="$ORACLE_ROOT/examples/simple"
rm -rf "$ORACLE/.terraform" "$ORACLE/.terraform.lock.hcl"
( cd "$ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's init failed"; }

# BASELINE, no rename at all: this module's own null_resource.archive[0]
# (package.tf) triggers on var.trigger_on_package_timestamp (default true)
# re-reading data.external.archive_prepare's own fresh timestamp on every
# single plan, so a genuinely unrelated replace of that ONE resource is
# expected on ANY replan of this estate, renamed or not - proven here
# before the rename is even applied, so the assertion below is checking
# what the rename changes, not masquerading as a rename defect (HANDOFF's
# "check the masquerade first").
BASELINE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; BASELINE_PLAN_RC=$?
[ "$BASELINE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BASELINE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle's baseline (no-rename) plan exited $BASELINE_PLAN_RC"; }
grep -qE '^  # module\.lambda_function\.null_resource\.archive\[0\] must be replaced' <<< "$BASELINE_PLAN_OUT" \
  || { printf '%s\n' "$BASELINE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "the baseline (no-rename) plan does not show null_resource.archive[0]'s own always-replace churn any more - re-check whether this estate's rename assertion below still needs to exclude it"; }
BASELINE_OTHER="$(grep -E '^  # .+ (will be (destroyed|created)|must be replaced)' <<< "$BASELINE_PLAN_OUT" | grep -v 'null_resource\.archive\[0\]' || true)"
[ -z "$BASELINE_OTHER" ] || { printf '%s\n' "$BASELINE_OTHER"; fail "the baseline (no-rename) plan shows create/destroy/replace churn beyond the known null_resource.archive[0] noise - this estate has drifted since the baseline was last measured"; }
log "  baseline (no rename): the module's own null_resource.archive[0] always replaces on any plan (its package-timestamp trigger), nothing else - confirmed BEFORE the rename below"

sed -i.bak 's/module "lambda_function" {/module "lambda_function_final" {/' "$ORACLE/main.tf"
sed -i.bak 's/module\.lambda_function\./module.lambda_function_final./g' "$ORACLE/outputs.tf"
rm -f "$ORACLE/main.tf.bak" "$ORACLE/outputs.tf.bak"
cat >> "$ORACLE/main.tf" <<'EOF'

moved {
  from = module.lambda_function.aws_lambda_function.this[0]
  to   = module.lambda_function_final.aws_lambda_function.this[0]
}

moved {
  from = module.lambda_function.aws_iam_role.lambda[0]
  to   = module.lambda_function_final.aws_iam_role.lambda[0]
}

moved {
  from = module.lambda_function.aws_cloudwatch_log_group.lambda[0]
  to   = module.lambda_function_final.aws_cloudwatch_log_group.lambda[0]
}

moved {
  from = module.lambda_function.aws_iam_role_policy.logs[0]
  to   = module.lambda_function_final.aws_iam_role_policy.logs[0]
}

moved {
  from = module.lambda_function.local_file.archive_plan[0]
  to   = module.lambda_function_final.local_file.archive_plan[0]
}

moved {
  from = module.lambda_function.null_resource.archive[0]
  to   = module.lambda_function_final.null_resource.archive[0]
}

moved {
  from = module.lambda_function.terraform_data.package_filename_for_hash[0]
  to   = module.lambda_function_final.terraform_data.package_filename_for_hash[0]
}
EOF
( cd "$ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
ORACLE_OTHER="$(grep -E '^  # .+ (will be (destroyed|created)|must be replaced)' <<< "$ORACLE_PLAN_OUT" | grep -v 'null_resource\.archive\[0\]' || true)"
[ -z "$ORACLE_OTHER" ] || { printf '%s\n' "$ORACLE_OTHER"; fail "stock proposes a destroy, create or replace beyond the known null_resource.archive[0] baseline noise for a rename carried entirely by a moved block - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan shows different churn than the baseline's own null_resource.archive[0] replace - the rename is not a true no-op beyond that known noise"; }
log "  stock: zero churn on cold_deploy's own state beyond the pre-existing null_resource.archive[0] noise (confirmed identical to the baseline above) - the module move reports only its move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# "Stock's replace of the same resource leaves the same single object."
# module.lambda_function.aws_cloudwatch_log_group.lambda[0] is forced to
# replace via the module's own `logging_log_group` argument, currently
# unset (so the module derives the name from function_name): setting it to
# a different literal forces aws_cloudwatch_log_group's `name` argument to
# change, and CloudWatch Logs has no RenameLogGroup API - only
# CreateLogGroup/DeleteLogGroup - so name is ForceNew in the provider's own
# schema, confirmed empirically below by the plan's own "must be replaced"
# annotation on that one resource, not assumed. The SAME literal also
# reaches aws_lambda_function.this[0]'s logging_config.log_group argument
# (main.tf:142, `log_group = var.logging_log_group`) and, through the
# recomposed policy document, aws_iam_role_policy.logs[0]'s policy JSON -
# both real, expected in-place updates cascading from the one ForceNew
# change, the same shape corpus-ec2-instance-complete's own F-ORACLE
# documents for its ami/eip/volume-attachment cascade. A fresh copy of
# cold_deploy's own state (cp -r, same as D-ORACLE above, preserving the
# module's relative source path), so this oracle runs on the ORIGINAL
# module name before the real script's own rename ever touches $EST.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace module.lambda_function's log group via its ForceNew logging_log_group-derived name, on cold_deploy's own state ==="
REPLACE_ORACLE_ROOT="$WORK/replace-oracle"
cp -r "$WORK/lambda" "$REPLACE_ORACLE_ROOT"
REPLACE_ORACLE="$REPLACE_ORACLE_ROOT/examples/simple"
rm -rf "$REPLACE_ORACLE/.terraform" "$REPLACE_ORACLE/.terraform.lock.hcl"
( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's init failed"; }
sed -i.bak 's|function_name = "${random_pet.this.id}-lambda-simple"|function_name = "${random_pet.this.id}-lambda-simple"\n  logging_log_group = "/aws/lambda/${random_pet.this.id}-lambda-simple-v2"|' "$REPLACE_ORACLE/main.tf"
rm -f "$REPLACE_ORACLE/main.tf.bak"
grep -q 'lambda-simple-v2' "$REPLACE_ORACLE/main.tf" \
  || fail "adding module.lambda_function's logging_log_group argument in the replace-oracle copy did not match - the corpus pin has moved"
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.lambda_function\.aws_cloudwatch_log_group\.lambda\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose replacing module.lambda_function's log group when its derived name changes"; }
grep -qE '~ +name +=.+forces replacement' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock's plan does not mark the log group's name as forcing replacement - it may not be ForceNew after all"; }
grep -qE '^  # module\.lambda_function\.aws_lambda_function\.this\[0\] will be updated in-place' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose updating the lambda function in-place when the log group name changes"; }
REPLACE_ORACLE_OTHER="$(grep -E '^  # .+ (will be (destroyed|created)|must be replaced)' <<< "$REPLACE_ORACLE_PLAN_OUT" | grep -v 'null_resource\.archive\[0\]' | grep -v 'aws_cloudwatch_log_group\.lambda\[0\]' || true)"
[ -z "$REPLACE_ORACLE_OTHER" ] \
  || { printf '%s\n' "$REPLACE_ORACLE_OTHER"; fail "stock proposes a destroy, create or replace beyond the log group's own forced replace and the known null_resource.archive[0] baseline noise"; }
log "  stock: exactly one replace (the log group) plus its expected in-place cascade (function, inline log policy), beyond the known null_resource.archive[0] baseline noise; plan only, never applied"

gauntlet_begin_stage migrate

# ── STAGE 2: MIGRATE ─────────────────────────────────────────────────────
log "=== 4. add the live block (record_store, for the estate's random_pet/"
log "        null_resource/terraform_data residue) ==="
perl -0pi -e 's/(random = \{\n      source  = "hashicorp\/random"\n      version = ">= 2.0"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

log "=== 5. choudoufu live-import against the cold state, read-only first ==="
IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "3 of 8 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 3 of 8 resources as eligible - the module-scope fix or the module's own resource shape has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" \
  || fail "the dry run wrote a tag - it must not"
# The three real AWS resources live under module.lambda_function - this is
# the module-scope fix under test. random_pet, local_file, null_resource and
# terraform_data all correctly report UNTAGGABLE or UNADMITTED_TYPE instead:
# none of them has an AWS tags argument, and this run never claims to stamp
# what it cannot.
grep -qF "module.lambda_function.aws_lambda_function.this[0]" <<< "$IMPORT_OUT" \
  || fail "live-import's report does not name the module-nested Lambda function at all - the module-scope fix regressed"
log "  3 of 8 verified against the live system (the module-nested IAM role, log group and function); nothing written yet"

log "=== 6. -approve: stamp the three module-nested AWS resources, and seed the"
log "        record store for the four record-backed ones (#340) ==="
APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
# 3 stamped: the module-nested IAM role, log group and function.
# 4 recorded: random_pet.this, local_file.archive_plan, null_resource.archive
#   and terraform_data.package_filename_for_hash - every RECORD_BACKED type in
#   this estate. Before #340 all four were reported SKIPPED and their values
#   went nowhere, which is what made stage 3 propose creating them.
# 1 skipped: aws_iam_role_policy.logs, genuinely untaggable and genuinely
#   derived from its tagged parent - the one resource here that needs neither
#   carrier.
grep -qF "3 resource(s) newly stamped, 0 already stamped, 4 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp 3 and record 4 of 8 resources cleanly"; }
log "  3 stamped, 4 recorded"

# The record store is asserted by CONTENT, not by "a file exists": the value
# a migration has to carry for this estate is random_pet.this's generated
# name, and every identity in the estate is derived from it.
RECORDS_DIR="$EST/.tofu-records"
[ -d "$RECORDS_DIR" ] || fail "live-import -approve created no record store at $RECORDS_DIR"
grep -Rqs -- "$PET" "$RECORDS_DIR" \
  || { find "$RECORDS_DIR" -type f | head -20; fail "the record store does not contain random_pet.this's generated id ($PET) anywhere - #340 has regressed"; }
log "  record store carries random_pet.this = $PET"

# Issue #349's remaining half, asserted BY VALUE against stock's own answer.
#
# A stock state file holds every root output's value; until this existed, a
# migration dropped all of them, and stage 3 below then rendered every output
# choudoufu could not recompute as newly created. The last one it could not
# recompute was local_filename, whose value is
# try(data.external.archive_prepare[0].result.filename, null) - the name of a
# deployment package, derived from a hash package.py computes by running.
# Nothing evaluates that offline, so the only honest source for it is the
# value the last apply settled on, which is exactly what stock reads out of
# its state file.
#
# The assertion is deliberately not "a record exists" and not "the plan is
# empty". An empty plan is convergence, and HANDOFF.md says convergence is
# never evidence a value is right: a prior output value that happened to
# equal what the plan recomputes would cancel whether it was read across
# correctly or invented. So the recorded value is compared, by value, against
# what the COLD STOCK STATE holds for the same output - the oracle, taken
# before choudoufu touched anything.
OUTPUTS_DIR="$RECORDS_DIR/tofu-outputs/$ESTATE"
[ -d "$OUTPUTS_DIR" ] || { find "$RECORDS_DIR" -type d | head -20; fail "live-import -approve carried no root output values across (#349): $OUTPUTS_DIR does not exist"; }
# bG9jYWxfZmlsZW5hbWU is base64url("local_filename"), the key scheme in
# internal/live/projection/rootoutput.go's RootOutputKey.
LOCAL_FILENAME_RECORD="$OUTPUTS_DIR/bG9jYWxfZmlsZW5hbWU"
[ -f "$LOCAL_FILENAME_RECORD" ] || { find "$OUTPUTS_DIR" -type f | head -30; fail "no record was written for the root output local_filename"; }
STOCK_LOCAL_FILENAME="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["outputs"]["local_filename"]["value"])' "$WORK/cold.tfstate")"
# The payload's "value" is the cty value ctyjson-encoded, which for a string
# output is a JSON string - so json.load decodes it in one step. "type" beside
# it is the value's own cty type, which is what lets the record be read back
# with no schema and no configuration in hand.
RECORDED_LOCAL_FILENAME="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["value"])' "$LOCAL_FILENAME_RECORD")"
[ -n "$STOCK_LOCAL_FILENAME" ] || fail "the cold stock state holds no local_filename output - the corpus pin has moved"
[ "$RECORDED_LOCAL_FILENAME" = "$STOCK_LOCAL_FILENAME" ] \
  || fail "the migrated record for the root output local_filename is $RECORDED_LOCAL_FILENAME, but stock's own state says $STOCK_LOCAL_FILENAME - the value carried across is WRONG, which is worse than not carrying it"
log "  root output local_filename carried across by value: $RECORDED_LOCAL_FILENAME (identical to stock's own state)"
# The sensitive half of the same rule, asserted by ABSENCE. A sensitive
# output's value is deliberately NOT written - see WriteRootOutputValues for
# why the strict answer is taken until HANDOFF's "no secrets stored by the
# tool" toggle reaches that namespace. This example declares no sensitive
# output today, so the check reads zero of them and passes trivially; it is
# here as a standing guard, driven off whatever the STOCK STATE flags rather
# than off a name written down here, so the day the module or this example
# grows one it is already covered.
python3 - "$WORK/cold.tfstate" "$OUTPUTS_DIR" <<'PY' || fail "a sensitive root output's value was written into the record store"
import base64, json, os, sys
state = json.load(open(sys.argv[1]))
outdir = sys.argv[2]
bad = []
for name, ov in state.get("outputs", {}).items():
    if not ov.get("sensitive"):
        continue
    key = base64.urlsafe_b64encode(name.encode()).decode().rstrip("=")
    if os.path.exists(os.path.join(outdir, key)):
        bad.append(name)
if bad:
    print("sensitive outputs with a record:", bad)
    sys.exit(1)
PY
log "  no record written for any output the stock state flags sensitive"

log "=== 7. the markers, read through the AWS CLI directly - never through choudoufu ==="
WANT_LAMBDA_ADDR="module.lambda_function.aws_lambda_function.this:0"
WANT_ROLE_ADDR="module.lambda_function.aws_iam_role.lambda:0"
WANT_LOGGROUP_ADDR="module.lambda_function.aws_cloudwatch_log_group.lambda:0"
if [ "${BREAK:-}" = "1" ]; then
  WANT_LAMBDA_ADDR="module.lambda_alias.aws_lambda_function.this:0"
  log "  BREAK=1: expecting tofu-address=$WANT_LAMBDA_ADDR on the function - the"
  log "           SAME shape and the SAME resource type, just the wrong module"
  log "           name. This step must fail."
fi

GOT_LAMBDA_ADDR="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-address"' --output text)"
[ "$GOT_LAMBDA_ADDR" = "$WANT_LAMBDA_ADDR" ] || fail "aws_lambda_function.this carries tofu-address=$GOT_LAMBDA_ADDR, not $WANT_LAMBDA_ADDR"
GOT_LAMBDA_ESTATE="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-estate"' --output text)"
[ "$GOT_LAMBDA_ESTATE" = "$ESTATE" ] || fail "aws_lambda_function.this carries tofu-estate=$GOT_LAMBDA_ESTATE, not $ESTATE"

GOT_ROLE_ADDR="$(awsl iam list-role-tags --role-name "$FN_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ROLE_ADDR" = "$WANT_ROLE_ADDR" ] || fail "aws_iam_role.lambda carries tofu-address=$GOT_ROLE_ADDR, not $WANT_ROLE_ADDR"

LOGGROUP_ARN="arn:aws:logs:${REGION}:${ACCOUNT}:log-group:/aws/lambda/${FN_NAME}"
GOT_LOGGROUP_ADDR="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-address"' --output text 2>/dev/null \
  || awsl logs list-tags-log-group --log-group-name "/aws/lambda/${FN_NAME}" --query 'tags."tofu-address"' --output text)"
[ "$GOT_LOGGROUP_ADDR" = "$WANT_LOGGROUP_ADDR" ] || fail "aws_cloudwatch_log_group.lambda carries tofu-address=$GOT_LOGGROUP_ADDR, not $WANT_LOGGROUP_ADDR"

log "  function:   tofu-address=$GOT_LAMBDA_ADDR tofu-estate=$GOT_LAMBDA_ESTATE"
log "  iam role:   tofu-address=$GOT_ROLE_ADDR"
log "  log group:  tofu-address=$GOT_LOGGROUP_ADDR"
log "  all three module-nested markers verified directly against IAM/Lambda/Logs, not through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "3 stamped, 4 recorded, 0 failed, 1 skipped"
gauntlet_begin_stage test_plan

# ── STAGE 3: TEST PLAN ──────────────────────────────────────────────────────
log "=== 8. delete the state file, choudoufu live-plan ==="
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"

PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ "$PLAN_RC" -ne 0 ]; then
  # Both previously-reported blockers are asserted CLEARED by absence, which
  # is the only way a "this is fixed" claim survives the next reader. A grep
  # that finds nothing proves more here than one that finds something: if
  # #303 or #314 ever regresses, this stops being a report about the fourth
  # wall and says so.
  for gone in aws_lambda_function_url aws_lambda_function_recursion_config; do
    grep -qF "$gone" <<< "$PLAN_OUT" \
      && fail "$gone is back in live-plan's diagnostics - issue #303's per-instance admission fix has regressed"
  done
  grep -qF "local_file" <<< "$PLAN_OUT" \
    && fail "local_file is back in live-plan's diagnostics - issue #314's EXTERNAL_ADMITTED class has regressed"
  grep -qF "Logical resource is not admitted" <<< "$PLAN_OUT" \
    && fail "a logical resource is refused again - this estate's random_pet/null_resource/terraform_data/local_file are all admitted under the record_store declared above"

  BLOCKERS="$(grep -c "^Error:" <<< "$PLAN_OUT")"
  log ""
  log "STAGE 3 (test plan): BLOCKED for real, at $BLOCKERS diagnostics."
  log ""
  log "  Two previously-reported blockers are CONFIRMED FIXED, each asserted"
  log "  by ABSENCE just above rather than by reading a fresh log:"
  log "    #303  zero-instance blocks no longer have to pass type admission"
  log "          (aws_lambda_function_url, aws_lambda_function_recursion_config)"
  log "    #314  local_file.archive_plan is admitted through the fourth"
  log "          LogicalClass, EXTERNAL_ADMITTED, against the record_store"
  log "          this estate declares at step 4"
  log ""
  log "  What remains is a FOURTH wall, newly reached rather than caused, and"
  log "  a different kind entirely: every diagnostic below traces to one"
  log "  expression in the estate's own main.tf,"
  log ""
  log "      function_name = \"\${random_pet.this.id}-lambda-simple\""
  log ""
  log "  random_pet.this is RECORD_ADMITTED, so its id lives in the record"
  log "  store and nowhere else, and three of the module's real AWS resources"
  log "  take their identity from it. See this script's header for the full"
  log "  chain and for why this is worth filing: choudoufu holds that value"
  log "  already, and all three resources are ALREADY MARKED - stage 2 above"
  log "  stamped them and verified the tags through the AWS CLI."
  log ""
  printf '%s\n' "$PLAN_OUT" | grep -B1 -A6 "^Error:" | head -60
  log ""
  log "STAGE 4 (test apply): NOT REACHED"
  log "STAGE 5 (drift and reconverge): NOT REACHED"
  log ""
  log "Stages 1 and 2 are real, verified passes - see above."
  gauntlet_stage test_plan fail "BLOCKED for real, at $BLOCKERS diagnostics"
  gauntlet_stage test_apply not_run "STAGE 4 NOT REACHED"
  gauntlet_stage drift_reconverge not_run "STAGE 5 NOT REACHED"
  gauntlet_end_stage
  exit 1
fi

[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"

# Issue #340, asserted by ABSENCE. Before the migrate seeded the record
# store, live-plan raised no diagnostics at all and then proposed CREATING
# every record-backed resource in the estate from nothing, led by
# "# random_pet.this will be created" - which also broke every identity
# derived from it. A grep that finds nothing proves more here than one that
# finds something: if the seeding regresses, this says so by name instead of
# leaving a reader to work out why the plan grew.
for gone in "random_pet.this will be created" \
            "local_file.archive_plan[0] will be created" \
            "null_resource.archive[0] will be created" \
            "terraform_data.package_filename_for_hash will be created"; do
  grep -qF "$gone" <<< "$PLAN_OUT" \
    && fail "the plan proposes creating a record-backed resource ($gone) - issue #340's migrate-time record seeding has regressed"
done
log "  no record-backed resource is proposed for creation: the migrate seeded all four"

# The sixth wall's choudoufu half, asserted by ABSENCE. local_file.content is
# marked sensitive by hashicorp/local, and a record store had nowhere to put
# a sensitivity path - so the migrate stored the value and not the mark, and
# because live-plan runs with SkipRefresh the plan's "before" side had no
# marks at all while its "after" side was re-marked from the schema every
# run. The result was a permanent "~ content = (sensitive value)" that
# OpenTofu's own renderer annotated "The value is unchanged". A grep that
# finds nothing proves more here than one that finds something.
grep -qF "local_file.archive_plan" <<< "$PLAN_OUT" \
  && fail "local_file.archive_plan is back in the plan - projection's record sensitivity (recordPayload.SensitiveAttrs) has regressed"
log "  no sensitivity-only diff on local_file.archive_plan: the record carries its marks"

# Whatever remains, split against the STOCK control taken at step 3b. Only
# the difference is choudoufu's; the rest is the emulator disagreeing with
# the provider about what it created, which stock terraform sees too.
LIVE_DRIFTED="$(grep -E '^  # ' <<< "$PLAN_OUT" | sed 's/^  # //' | sort)"
CHOUDOUFU_DRIFTED="$(comm -23 <(printf '%s\n' "$LIVE_DRIFTED" | grep -v '^$' | sort -u) <(printf '%s\n' "$STOCK_DRIFTED" | grep -v '^$' | sort -u))"

grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" || {
  log ""
  log "STAGE 3 (test plan): BLOCKED for real - live-plan raises NO diagnostics,"
  log "  every identity resolves, and ZERO resources are proposed for change,"
  log "  but the plan is not empty."
  log ""
  OUTPUT_ONLY=0
  grep -qF "OpenTofu will perform the following actions" <<< "$PLAN_OUT" || {
    grep -qF "Changes to Outputs:" <<< "$PLAN_OUT" && OUTPUT_ONLY=1
  }
  if [ "$OUTPUT_ONLY" = "1" ]; then
    log "  lex00/floci#83 IS FIXED: there is no resource-level action block at"
    log "  all (no "OpenTofu will perform the following actions"), which is"
    log "  what step 3b's control already showed against stock terraform. What"
    log "  remains is an OUTPUT-only diff. This estate went 23 output lines to"
    log "  2 to 1 to 0 across three rungs: #348 evaluated the root outputs at"
    log "  all; #349's first two rungs saw through zero-instance blocks and"
    log "  read the data sources the outputs reach; #349's last rung carried"
    log "  the stock state file's own output values across at migrate time and"
    log "  keeps them current at write-back, which is the only honest source"
    log "  for an output like local_filename whose value exists only because"
    log "  package.py was run. If a line is showing here again, the question"
    log "  to ask first is which of those four is not doing its job for it."
  elif [ -z "$CHOUDOUFU_DRIFTED" ]; then
    log "  Every resource in this plan is one stock terraform's OWN replan proposes"
    log "  too (step 3b's control), so nothing here is choudoufu's."
  else
    log "  Beyond the emulator's own drift (step 3b), these are choudoufu's:"
    printf '%s\n' "$CHOUDOUFU_DRIFTED" | sed 's/^/    /'
  fi
  log ""
  log "  What is CONFIRMED FIXED and asserted by absence just above: #340's"
  log "  migrate-time record seeding. random_pet.this, local_file.archive_plan,"
  log "  null_resource.archive and terraform_data.package_filename_for_hash are"
  log "  all read back out of the record store this migration wrote, so none of"
  log "  them is proposed for creation and every identity derived from"
  log "  random_pet.this renders."
  log ""
  log "  What remains is whatever the diff below says."
  log ""
  # Bounded by sed's own range end rather than piped into head: head closes
  # the pipe early and printf then reports a broken pipe into the middle of
  # the evidence this block exists to print. An output-only diff (issue
  # #348) has no "OpenTofu will perform" header at all, so both possible
  # shapes are dumped - whichever is present prints, the other prints
  # nothing.
  printf '%s\n' "$PLAN_OUT" | sed -n '/^OpenTofu will perform/,/^Plan: /p'
  printf '%s\n' "$PLAN_OUT" | sed -n '/^Changes to Outputs:/,/^$/p'
  log ""
  log "STAGE 4 (test apply): NOT REACHED"
  log "STAGE 5 (drift and reconverge): NOT REACHED"
  log ""
  gauntlet_stage test_plan fail "live-plan raises no diagnostics and proposes zero resource changes, but the plan is not empty"
  gauntlet_stage test_apply not_run "STAGE 4 NOT REACHED"
  gauntlet_stage drift_reconverge not_run "STAGE 5 NOT REACHED"
  gauntlet_end_stage
  fail "live-plan is not empty"
}

for id in "$FN_NAME" "${FN_NAME}-logs"; do
  grep -qF "$id" <<< "$PLAN_OUT" || true
done
log "  no resource change proposed"

log ""
log "STAGE 3 (test plan): PASS"
log ""
gauntlet_stage test_plan pass "no resource change proposed"
gauntlet_begin_stage test_apply

# ── STAGE 4: TEST APPLY ──────────────────────────────────────────────────────
log "=== 9. test apply: apply the empty plan; tagged object count and markers unchanged ==="

# floci's resourcegroupstaggingapi GetResources does not index CloudWatch Logs
# log groups: queried directly against this same estate it returns only the
# IAM role and the Lambda function, 2 of the 3 tagged objects, even though
# each object's OWN service (logs:list-tags-for-resource, used at step 7
# above and again below) reads its tags back correctly, and AWS's own
# GetResources documents "logs:loggroup" as a supported resource type - an
# emulator gap, not a real-AWS or choudoufu one. Rather than assert the
# object count through a cross-service search that gap would silently
# under-report (a wrong count is worse than an inconvenient one), this counts
# the three module-nested resources stage 2 already verified are tagged, by
# reading each object's own tag API directly - the same precedent as
# corpus-sumaform-aws's routing around an infra-emulation gap rather than
# around choudoufu's own policy.
tagged_object_count() {
  local n=0
  [ "$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-estate"' --output text 2>/dev/null)" = "$ESTATE" ] && n=$((n + 1))
  [ "$(awsl iam list-role-tags --role-name "$FN_NAME" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null)" = "$ESTATE" ] && n=$((n + 1))
  local lg_estate
  lg_estate="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-estate"' --output text 2>/dev/null \
    || awsl logs list-tags-log-group --log-group-name "/aws/lambda/${FN_NAME}" --query 'tags."tofu-estate"' --output text 2>/dev/null)"
  [ "$lg_estate" = "$ESTATE" ] && n=$((n + 1))
  printf '%s\n' "$n"
}

BEFORE_N="$(tagged_object_count)"
[ "$BEFORE_N" = "3" ] || fail "expected 3 tagged objects (the module-nested IAM role, log group and function - aws_iam_role_policy.logs is untaggable and carries no tag) before the no-op apply, got $BEFORE_N"

NOOP_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_APPLY_RC=$?
[ "$NOOP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$NOOP_APPLY_OUT" | tail -40; fail "the no-op apply exited $NOOP_APPLY_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes' <<< "$NOOP_APPLY_OUT" \
  || { grep -E 'Apply complete|Plan: ' <<< "$NOOP_APPLY_OUT"; fail "the no-op apply was not a genuine no-op"; }

AFTER_N="$(tagged_object_count)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "the tagged object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$EST/terraform.tfstate" ] || fail "the no-op apply left a state file behind"

# The markers did not move either - read directly through the AWS CLI, the
# same three services stage 2 verified against, not through choudoufu's own
# report of itself.
GOT_LAMBDA_ADDR2="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-address"' --output text)"
[ "$GOT_LAMBDA_ADDR2" = "module.lambda_function.aws_lambda_function.this:0" ] \
  || fail "after the no-op apply, aws_lambda_function.this carries tofu-address=$GOT_LAMBDA_ADDR2, not module.lambda_function.aws_lambda_function.this:0"
GOT_ROLE_ADDR2="$(awsl iam list-role-tags --role-name "$FN_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_ROLE_ADDR2" = "module.lambda_function.aws_iam_role.lambda:0" ] \
  || fail "after the no-op apply, aws_iam_role.lambda carries tofu-address=$GOT_ROLE_ADDR2, not module.lambda_function.aws_iam_role.lambda:0"
GOT_LOGGROUP_ADDR2="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-address"' --output text 2>/dev/null \
  || awsl logs list-tags-log-group --log-group-name "/aws/lambda/${FN_NAME}" --query 'tags."tofu-address"' --output text)"
[ "$GOT_LOGGROUP_ADDR2" = "module.lambda_function.aws_cloudwatch_log_group.lambda:0" ] \
  || fail "after the no-op apply, aws_cloudwatch_log_group.lambda carries tofu-address=$GOT_LOGGROUP_ADDR2, not module.lambda_function.aws_cloudwatch_log_group.lambda:0"

# And the record store survived the apply, by value: random_pet.this's
# generated id (every identity in this estate derives from it) and the
# carried root output local_filename (#349) are both still there, not
# dropped by write-back after a run with nothing to change.
grep -Rqs -- "$PET" "$RECORDS_DIR" \
  || fail "random_pet.this's generated id ($PET) is gone from the record store after the no-op apply"
[ -f "$LOCAL_FILENAME_RECORD" ] || fail "the carried root output local_filename ($OUTPUTS_DIR) is gone from the record store after the no-op apply"

log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file, all 3 markers unmoved, record store intact"
log ""
log "STAGE 4 (test apply): PASS"
log ""
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N; markers and record store intact"
gauntlet_begin_stage drift_reconverge

# ── STAGE 5: DRIFT AND RECONVERGE ───────────────────────────────────────────
#
# The same $EST estate, already stamped and already proven to plan and apply
# empty (stages 2-4), is the natural place to prove the OTHER direction: one
# live object changed out of band, directly through the AWS CLI, is detected
# and the fix is scoped to exactly that object - not "the whole estate looks
# different." The mutated attribute is memory_size on the module-nested
# Lambda function (module.lambda_function.aws_lambda_function.this[0]): 128
# in the config (var.memory_size's own default), changed live to 256 via
# `aws lambda update-function-configuration` - never through choudoufu.

log "=== 10. mutate one live object out of band, directly via the AWS CLI ==="
if [ "${BREAK:-}" = "1" ]; then
  # aws_iam_role.lambda's own mutable arguments (max_session_duration,
  # description) go through IAM's UpdateRole/UpdateRoleDescription actions,
  # and floci's UpdateRole response is missing the (empty but expected)
  # <UpdateRoleResult/> element the AWS CLI's botocore deserializer requires
  # - the live-side mutation succeeds (confirmed manually: MaxSessionDuration
  # really moves) but the CLI call this script would make exits non-zero
  # with "'UpdateRoleResult'", which would make this BREAK-only negative
  # control fail on an emulator bug rather than on the assertion under test.
  # A floci gap, not a choudoufu one (HANDOFF's "the emulator is wrong" row);
  # not filed as its own issue since nothing else in this script depends on
  # UpdateRole. aws_cloudwatch_log_group.lambda's retention_in_days
  # (PutRetentionPolicy) has no such gap and is just as real a second object.
  awsl logs put-retention-policy --log-group-name "/aws/lambda/${FN_NAME}" --retention-in-days 14 >/dev/null \
    || fail "BREAK=1: could not tamper the log group's retention_in_days"
  GOT_RETENTION="$(awsl logs describe-log-groups --log-group-name-prefix "/aws/lambda/${FN_NAME}" --query 'logGroups[0].retentionInDays' --output text)"
  [ "$GOT_RETENTION" = "14" ] || fail "BREAK=1: the log group tamper did not take (read back $GOT_RETENTION)"
  log "  BREAK=1: also tampered aws_cloudwatch_log_group.lambda's retention_in_days"
  log "           (unset in config -> 14) out of band - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

awsl lambda update-function-configuration --function-name "$FN_NAME" --memory-size 256 >/dev/null \
  || fail "could not tamper aws_lambda_function.this's memory_size via the AWS CLI"
STATUS=""
for _ in $(seq 1 30); do
  STATUS="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query LastUpdateStatus --output text 2>/dev/null)"
  [ "$STATUS" = "Successful" ] && break
  sleep 1
done
[ "$STATUS" = "Successful" ] || fail "the out-of-band memory_size update never reached LastUpdateStatus=Successful (last seen: $STATUS)"
DRIFTED_MEMORY="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query MemorySize --output text)"
[ "$DRIFTED_MEMORY" = "256" ] || fail "the out-of-band memory_size mutation did not take (read back $DRIFTED_MEMORY)"
log "  mutated $FN_NAME's memory_size to 256 (config says 128) directly via the AWS CLI - never through choudoufu"

log "=== 11. choudoufu plan proposes fixing exactly that one object ==="
DRIFT_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -40; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(changed_addrs_excluding_markers <<< "$DRIFT_PLAN_OUT")"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"

if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK=1 set (two objects tampered), but choudoufu's plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more"
  log "           than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "module.lambda_function.aws_lambda_function.this[0]" ] \
    || fail "choudoufu's plan proposes fixing $CHANGED_ADDRS, not module.lambda_function.aws_lambda_function.this[0]"
  log "  choudoufu's plan proposes fixing exactly one object: $CHANGED_ADDRS"

  log "=== 12. the stock oracle: the identical mutation, plain terraform ==="
  # $STOCKDRIFT is the pre-live-block snapshot saved right after the cold
  # apply (before step 4 added a live block to $EST/versions.tf) - a plain
  # terraform working directory pointed at the same floci endpoint, zero
  # choudoufu involvement. cold.tfstate is the state the cold apply itself
  # wrote, before this stage's mutation.
  cp "$WORK/cold.tfstate" "$STOCKDRIFT/terraform.tfstate"
  ( cd "$STOCKDRIFT" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$STOCKDRIFT" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the stock oracle's init failed"; }
  STOCK_DRIFT_PLAN_OUT="$(cd "$STOCKDRIFT" && terraform plan -input=false -no-color -detailed-exitcode 2>&1)"; STOCK_DRIFT_PLAN_RC=$?
  case "$STOCK_DRIFT_PLAN_RC" in
    0) fail "the stock oracle replans EMPTY after the same mutation - this control is not load-bearing" ;;
    2) ;;
    *) printf '%s\n' "$STOCK_DRIFT_PLAN_OUT" | tail -40; fail "the stock oracle's plan failed to run at all (exit $STOCK_DRIFT_PLAN_RC)" ;;
  esac
  STOCK_CHANGED_ADDRS="$(changed_addrs_excluding_markers <<< "$STOCK_DRIFT_PLAN_OUT")"
  STOCK_N_CHANGED="$(printf '%s\n' "$STOCK_CHANGED_ADDRS" | grep -c . || true)"
  [ "$STOCK_N_CHANGED" = "1" ] \
    || { printf '%s\n' "$STOCK_DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected stock terraform's own plan to propose fixing exactly 1 object too, got $STOCK_N_CHANGED"; }
  [ "$STOCK_CHANGED_ADDRS" = "module.lambda_function.aws_lambda_function.this[0]" ] \
    || fail "stock terraform's plan proposes fixing $STOCK_CHANGED_ADDRS, not module.lambda_function.aws_lambda_function.this[0] - choudoufu and stock disagree about which object drifted"

  # The oracle comparison itself: the memory_size diff line, choudoufu's
  # against stock's. Filtering to that one attribute is how marker tags get
  # normalised out of the comparison - stock's plan carries none (it never
  # wrote tofu-address/tofu-estate tags to begin with) and choudoufu's would
  # only show tag churn if the tags themselves had drifted, which they have
  # not here, so comparing the memory_size line alone is comparing the same
  # thing either way: the actual change, not incidental formatting.
  CHOUDOUFU_MEMORY_DIFF="$(grep -E 'memory_size' <<< "$DRIFT_PLAN_OUT" | sed -E 's/^[[:space:]]*[~+-]?[[:space:]]*//; s/[[:space:]]+/ /g' | sort -u)"
  STOCK_MEMORY_DIFF="$(grep -E 'memory_size' <<< "$STOCK_DRIFT_PLAN_OUT" | sed -E 's/^[[:space:]]*[~+-]?[[:space:]]*//; s/[[:space:]]+/ /g' | sort -u)"
  [ -n "$CHOUDOUFU_MEMORY_DIFF" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -B2 -A10 'will be updated'; fail "choudoufu's plan proposes fixing the object but names no memory_size diff line"; }
  [ "$CHOUDOUFU_MEMORY_DIFF" = "$STOCK_MEMORY_DIFF" ] || fail "choudoufu says \"$CHOUDOUFU_MEMORY_DIFF\", stock says \"$STOCK_MEMORY_DIFF\" - same object, different proposed change"
  log "  the stock oracle proposes fixing the identical object with the identical change: $CHOUDOUFU_MEMORY_DIFF"

  log "=== 13. apply the reconverging plan; the drift is gone ==="
  RECONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_MEMORY="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query MemorySize --output text)"
  [ "$FIXED_MEMORY" = "128" ] \
    || fail "the function's memory_size is $FIXED_MEMORY after reconverging, not 128"
  [ ! -f "$EST/terraform.tfstate" ] || fail "the reconverge apply left a state file behind"
  log "  reconverged: $FN_NAME's memory_size is back to 128, read via the AWS CLI"

  log ""
  log "STAGE 5 (drift and reconverge): PASS"
  log ""
  gauntlet_stage drift_reconverge pass "one object tampered (memory_size 128->256), exactly module.lambda_function.aws_lambda_function.this[0] proposed by both choudoufu and stock with the identical change, apply changed 1 and memory_size reads back as 128"
fi

# ══════════════════════════════════════════════════════════════════════════
# PART P: PLAN, REVIEW, APPLY (plan_approval, live/GAUNTLET.md #12, issue #903)
# ══════════════════════════════════════════════════════════════════════════
#
# The pipeline shape CI has always run: plan on the pull request, a human
# approves, apply exactly what was approved. The artifact that crosses that
# gate is the plan file, and under live markers it is an APPROVAL rather
# than an instruction - "apply <planfile>" re-reads the live system, plans
# against what it finds now, and compares that fresh plan with the file's,
# refusing by name and with exit 3 when the two disagree (issue #878,
# internal/command/live_approval.go).
#
# Both arms run on every real run, because only the pair is evidence:
#
#   P2/P3  the world MOVES between the approval and the apply - the Lambda
#          function's memory_size is changed out of band through the AWS
#          CLI, the SAME mutation STAGE 5 above already proves this estate's
#          plan notices and scopes to one object - and the apply must
#          refuse: exit 3, the named summary, the unapproved row printed by
#          address AND by the live identity it was computed against, and the
#          reviewed change still not landed when the live log group is read
#          back through the CLI.
#   P4     nothing has moved (memory_size is put back first) and the SAME
#          file must APPLY. This is the inverted control that
#          live/smoke/scenarios/apply-what-was-approved.sh reasons out: a
#          comparison which refuses unconditionally is not a check, so P3's
#          refusal is only worth something if the identical artifact goes
#          through when the world is where the approval left it.
#
# The two objects are deliberately disjoint - the change under review is one
# in-place retention_in_days update on module.lambda_function's log group,
# the out-of-band move is on the FUNCTION - so the refusal has an EXTRA row
# to name rather than a values-only disagreement about the same row
# (approvalMismatchDetail's Drifted branch). The reviewed argument is chosen
# to be in-place and to reach exactly one instance: the module call carries
# no tags argument at all, and every tags-shaped knob it does have
# propagates to three children at once, while cloudwatch_logs_retention_in_days
# lands on aws_cloudwatch_log_group.lambda[0] and nowhere else. It is not a
# create or a destroy, so no later part's captured id moves under it - PART
# F below replaces the same log group by NAME and captures its own ids
# after this part has already put the retention back.
#
# Runs only on a real run. Under any of this script's other BREAK controls
# the estate is deliberately left somewhere this part does not describe, so
# it reports no verdict at all and the runner records the stage as not_run,
# never as a pass.
if [ -z "${BREAK:-}" ] && [ -z "${BREAK_RENAME:-}" ] && [ -z "${BREAK_REMOVE:-}" ] \
   && [ -z "${BREAK_COUNT:-}" ] && [ -z "${BREAK_COLLISION:-}" ] \
   && [ -z "${BREAK_DEPENDENCY:-}" ] && [ -z "${BREAK_PLAN:-}" ]; then
  gauntlet_begin_stage plan_approval
  log "=== PART P: plan, review, apply (the approval gate, live/GAUNTLET.md #12) ==="

  P_REVIEWED_ADDR="module.lambda_function.aws_cloudwatch_log_group.lambda[0]"
  P_MOVED_ADDR="module.lambda_function.aws_lambda_function.this[0]"

  log "=== P1. the change under review: one argument, reaching one instance ==="
  [ "$(grep -c '^  publish = true$' "$EST/main.tf")" = "1" ] \
    || fail "main.tf no longer carries exactly one \"publish = true\" module argument - the corpus pin has moved"
  perl -0pi -e 's/^  publish = true$/  publish = true\n  cloudwatch_logs_retention_in_days = 14/m' "$EST/main.tf"
  [ "$(grep -c '^  cloudwatch_logs_retention_in_days = 14$' "$EST/main.tf")" = "1" ] \
    || fail "the reviewed edit did not write exactly one cloudwatch_logs_retention_in_days argument"
  log "  edited one argument: module.lambda_function's cloudwatch_logs_retention_in_days is now 14 (was unset)"

  P_PLAN_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color -out=approved.tfplan 2>&1)"; P_PLAN_RC=$?
  [ "$P_PLAN_RC" -eq 0 ] || { printf '%s\n' "$P_PLAN_OUT" | tail -40; fail "plan -out exited $P_PLAN_RC"; }
  [ -f "$EST/approved.tfplan" ] || { printf '%s\n' "$P_PLAN_OUT" | tail -20; fail "plan -out wrote no file"; }
  P_APPROVED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$P_PLAN_OUT" | awk '{print $2}' | sort -u)"
  [ "$P_APPROVED_ADDRS" = "$P_REVIEWED_ADDR" ] \
    || { grep -E '^  # .+ (will be|must be)' <<< "$P_PLAN_OUT"; fail "the approved plan is about [$P_APPROVED_ADDRS], not $P_REVIEWED_ADDR alone"; }
  if grep -qE '^  # .+ (will be (created|destroyed)|must be replaced)' <<< "$P_PLAN_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$P_PLAN_OUT"; fail "the approved plan proposes a create, a destroy or a replace; this review is one in-place update"
  fi
  P_PLAN_BYTES="$(wc -c < "$EST/approved.tfplan" | tr -d ' ')"
  log "  approved.tfplan written ($P_PLAN_BYTES bytes of stock-format plan file); the approval is exactly one update, on $P_REVIEWED_ADDR"

  log "=== P2. the world moves between the approval and the apply ==="
  awsl lambda update-function-configuration --function-name "$FN_NAME" --memory-size 256 >/dev/null \
    || fail "the out-of-band memory_size move could not be made through the AWS CLI"
  P_MOVE_STATUS=""
  for _ in $(seq 1 30); do
    P_MOVE_STATUS="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query LastUpdateStatus --output text 2>/dev/null)"
    [ "$P_MOVE_STATUS" = "Successful" ] && break
    sleep 1
  done
  [ "$P_MOVE_STATUS" = "Successful" ] || fail "the out-of-band memory_size update never reached LastUpdateStatus=Successful (last seen: $P_MOVE_STATUS)"
  P_MOVED_VALUE="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query MemorySize --output text)"
  [ "$P_MOVED_VALUE" = "256" ] || fail "the out-of-band move did not take: $FN_NAME's memory_size reads $P_MOVED_VALUE"
  log "  $FN_NAME's memory_size changed out of band to 256 (config says 128) - after the approval, before the apply, through the AWS CLI"

  log "=== P3. apply the approved plan against a world that moved ==="
  P_GATE_RC=0
  P_GATE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_GATE_RC=$?
  if [ "${BREAK_APPROVAL:-}" = "1" ]; then
    # stages.go's own Break line for plan_approval, executed literally:
    # "Apply the planfile after a mutation and expect success; the run must
    # refuse." Expecting success here is the defect this stage exists to
    # catch, so this assertion has to fail.
    [ "$P_GATE_RC" = "0" ] \
      || fail "BREAK_APPROVAL=1: the apply of a plan file approved before the world moved exited $P_GATE_RC, not 0 - the refusal is load-bearing and this expectation is the defect stage 12 catches"
    log "  BREAK_APPROVAL=1: the apply exited 0 with the world moved - stage 12 is NOT load-bearing"
  fi
  [ "$P_GATE_RC" = "3" ] \
    || { printf '%s\n' "$P_GATE_OUT" | tail -40; fail "the apply exited $P_GATE_RC, want 3 - a plan file whose approval no longer covers the run must refuse with its own status"; }
  grep -q "The approved plan no longer matches the live system" <<< "$P_GATE_OUT" \
    || { printf '%s\n' "$P_GATE_OUT" | tail -40; fail "the apply stopped, but not with the named refusal"; }
  # Everything from the refusal's own summary line onward. The fresh plan
  # printed above it also names the moved function, so asserting over the
  # whole output would pass on a refusal that named nothing at all.
  P_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$P_GATE_OUT")"
  grep -qF "This apply would do, and the approved plan does not include:" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not classify the difference as a change nobody approved"; }
  P_EXTRA_ROW="$(grep -F "$P_MOVED_ADDR" <<< "$P_REFUSAL" | head -1)"
  [ -n "$P_EXTRA_ROW" ] \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not name $P_MOVED_ADDR, the change nobody approved"; }
  grep -qF "$FN_NAME" <<< "$P_EXTRA_ROW" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal names the address but not the live identity it was computed against: the row reads \"$P_EXTRA_ROW\", which does not carry $FN_NAME"; }
  grep -qF "Exit status 3" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not tell a pipeline what its exit status means"; }
  if grep -q "Apply complete!" <<< "$P_GATE_OUT"; then
    printf '%s\n' "$P_GATE_OUT" | tail -20; fail "the apply ran anyway after refusing"
  fi
  # Not "no Apply complete line" alone: read the live object the approval
  # was about and confirm the reviewed change did not land.
  P_REVIEWED_RETENTION="$(awsl logs describe-log-groups --log-group-name-prefix "/aws/lambda/${FN_NAME}" --query 'logGroups[0].retentionInDays' --output text)"
  [ "$P_REVIEWED_RETENTION" = "None" ] || [ -z "$P_REVIEWED_RETENTION" ] \
    || fail "the refused apply still wrote the reviewed change: /aws/lambda/${FN_NAME} carries retentionInDays=$P_REVIEWED_RETENTION"
  printf '%s\n' "$P_REFUSAL" | head -12
  log "  refused by name, exit $P_GATE_RC, nothing applied - the row it names is \"$P_EXTRA_ROW\", exactly the change that appeared after the approval"

  log "=== P4. the inverted control: put the world back, apply the SAME file ==="
  awsl lambda update-function-configuration --function-name "$FN_NAME" --memory-size 128 >/dev/null \
    || fail "the out-of-band move could not be undone through the AWS CLI"
  P_BACK_STATUS=""
  for _ in $(seq 1 30); do
    P_BACK_STATUS="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query LastUpdateStatus --output text 2>/dev/null)"
    [ "$P_BACK_STATUS" = "Successful" ] && break
    sleep 1
  done
  [ "$P_BACK_STATUS" = "Successful" ] || fail "putting memory_size back never reached LastUpdateStatus=Successful (last seen: $P_BACK_STATUS)"
  P_RESTORED="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query MemorySize --output text)"
  [ "$P_RESTORED" = "128" ] || fail "the out-of-band move was not undone: $FN_NAME's memory_size reads $P_RESTORED"
  P_OK_RC=0
  P_OK_OUT="$(cd "$EST" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_OK_RC=$?
  [ "$P_OK_RC" = "0" ] \
    || { printf '%s\n' "$P_OK_OUT" | tail -40; fail "the same plan file was refused (exit $P_OK_RC) over a world that had not moved - a comparison that refuses unconditionally is not a check"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_OK_OUT" \
    || { grep -E 'Apply complete' <<< "$P_OK_OUT"; fail "the approved apply did not change exactly the one reviewed resource"; }
  P_LANDED="$(awsl logs describe-log-groups --log-group-name-prefix "/aws/lambda/${FN_NAME}" --query 'logGroups[0].retentionInDays' --output text)"
  [ "$P_LANDED" = "14" ] \
    || fail "the approved change did not land: /aws/lambda/${FN_NAME} carries retentionInDays=$P_LANDED, want 14"
  log "  the identical artifact applied (0 added, 1 changed, 0 destroyed) and /aws/lambda/${FN_NAME} now carries retentionInDays=14, read via the AWS CLI"

  log "=== P5. put the estate back where the rest of this script expects it ==="
  rm -f "$EST/approved.tfplan"
  perl -0pi -e 's/^  cloudwatch_logs_retention_in_days = 14\n//m' "$EST/main.tf"
  [ "$(grep -c 'cloudwatch_logs_retention_in_days' "$EST/main.tf")" = "0" ] \
    || fail "reverting the reviewed edit did not remove the cloudwatch_logs_retention_in_days argument"
  P_REVERT_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; P_REVERT_RC=$?
  [ "$P_REVERT_RC" -eq 0 ] || { printf '%s\n' "$P_REVERT_OUT" | tail -40; fail "the revert apply failed"; }
  P_GONE="$(awsl logs describe-log-groups --log-group-name-prefix "/aws/lambda/${FN_NAME}" --query 'logGroups[0].retentionInDays' --output text)"
  [ "$P_GONE" = "None" ] || [ -z "$P_GONE" ] \
    || fail "the reviewed retention is still set on /aws/lambda/${FN_NAME} after the revert: $P_GONE"
  P_KEPT_MEMORY="$(awsl lambda get-function-configuration --function-name "$FN_NAME" --query MemorySize --output text)"
  [ "$P_KEPT_MEMORY" = "128" ] || fail "$FN_NAME's memory_size is $P_KEPT_MEMORY after PART P, not the configured 128"
  P_FINAL_OUT="$(cd "$EST" && "$TOFU" plan -input=false -no-color 2>&1)"; P_FINAL_RC=$?
  [ "$P_FINAL_RC" -eq 0 ] || { printf '%s\n' "$P_FINAL_OUT" | tail -40; fail "the post-revert plan exited $P_FINAL_RC"; }
  if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$P_FINAL_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$P_FINAL_OUT"; fail "the estate is not converged again after PART P"
  fi
  [ ! -f "$EST/terraform.tfstate" ] || fail "PART P left a state file behind"
  log "  reverted; the estate is converged again and PART D starts from where it would have"

  log ""
  log "PART P (plan, review, apply): PASS"
  gauntlet_stage plan_approval pass "one argument edited (module.lambda_function's cloudwatch_logs_retention_in_days, unset -> 14, which reaches $P_REVIEWED_ADDR and nothing else - the module call carries no tags argument and every tags-shaped knob it has would reach three children at once), \"plan -out=approved.tfplan\" wrote a $P_PLAN_BYTES-byte stock-format plan file whose whole change set is that one in-place update; the world then moved out of band ($FN_NAME's memory_size 128->256, this estate's own STAGE 5 mutation lifted, through the AWS CLI and never through choudoufu) and \"apply approved.tfplan\" refused with \"The approved plan no longer matches the live system\" at exit 3, classifying the drift under \"This apply would do, and the approved plan does not include:\" and naming the extra row as \"$P_EXTRA_ROW\" - both $P_MOVED_ADDR and the live identity it was computed against - with \"Exit status 3\" spelled out for a pipeline; nothing was applied - /aws/lambda/${FN_NAME} still carried no retentionInDays, read back through the AWS CLI rather than from the absence of an \"Apply complete!\" line. Inverted control on the same run (the shape live/smoke/scenarios/apply-what-was-approved.sh reasons out): with memory_size put back and nothing else changed, the IDENTICAL file applied - 0 added, 1 changed, 0 destroyed - and the log group read back with retentionInDays=14, so the refusal is earned by the drift and not handed out to every plan file. Reverted and reconverged in P5 (retention unset again, memory_size still 128, next plan proposes no resource action, no state file left behind) so PART D starts where it would have. BREAK_APPROVAL=1 asserts stage 12's own recorded Break line (apply the planfile after a mutation and expect success) and correctly fails"
  log ""
fi

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, active - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# See the D-ORACLE comment above stage 2 for why both mechanisms run on the
# SAME module (module.lambda_function is the estate's only real module and
# random_pet.this, its only other real resource, is record-located - live-mv
# does not support renaming that class yet, issue #270). live-mv moves one
# resource instance at a time, so the live-mv leg below issues one call per
# taggable resource under the module (the function, the role, the log
# group) rather than the single call another estate's module leg needs when
# its module holds exactly one taggable child.
#
# BREAK=6 (not 1: this script's own stage 3 identity check and stage 5
# drift check already corrupt their assertions and exit through fail()
# under BREAK=1 before this point, the same collision corpus-eks-basic's
# header documents between its own stage 2 and stage 3) exercises this
# stage's own break control instead of the real checks: renaming module.
# lambda_function WITHOUT a moved block, which must make choudoufu propose
# destroying the old address's function and creating the new one - the
# opposite of every other assertion in this part.
gauntlet_begin_stage day2_rename
log "=== D0. capture the live objects this rename must not disturb ==="
log "  $LAMBDA_ARN (aws_lambda_function), role $FN_NAME (aws_iam_role), $LOGGROUP_ARN (aws_cloudwatch_log_group)"

if [ "${BREAK:-}" = "6" ]; then
  log "=== D1 (BREAK=6). rename module.lambda_function -> module.lambda_function_final WITHOUT a moved block ==="
  sed -i.bak 's/module "lambda_function" {/module "lambda_function_final" {/' "$EST/main.tf"
  sed -i.bak 's/module\.lambda_function\./module.lambda_function_final./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the BREAK=6 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=6 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.lambda_function\.aws_lambda_function\.this\[0\] will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=6: renaming without a moved block did not propose destroying module.lambda_function.aws_lambda_function.this[0] - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.lambda_function_final\.aws_lambda_function\.this\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=6: renaming without a moved block did not propose creating module.lambda_function_final.aws_lambda_function.this[0] - this stage's check is not load-bearing"; }
  log "  BREAK=6: correctly proposes destroying module.lambda_function.aws_lambda_function.this[0] and creating module.lambda_function_final.aws_lambda_function.this[0] - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module.lambda_function -> module.lambda_function_moved ==="
  sed -i.bak 's/module "lambda_function" {/module "lambda_function_moved" {/' "$EST/main.tf"
  sed -i.bak 's/module\.lambda_function\./module.lambda_function_moved./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  # Per-resource moved blocks, not one module-level block: a module-level
  # `moved { from = module.lambda_function to = module.lambda_function_moved
  # }` alongside explicit per-resource blocks for the record-located
  # children is a cycle stock terraform itself refuses ("A chain of move
  # statements must end with an address that doesn't appear in any other
  # statements"), so every one of the module's 6 stateful children
  # (3 taggable, 3 record-located - aws_iam_role_policy.logs[0] is neither:
  # its identity is fully config-derived, role name + policy name, and
  # needs no moved block at all, confirmed absent from every churn list
  # below) gets its own.
  cat >> "$EST/main.tf" <<'EOF'

moved {
  from = module.lambda_function.aws_lambda_function.this[0]
  to   = module.lambda_function_moved.aws_lambda_function.this[0]
}

moved {
  from = module.lambda_function.aws_iam_role.lambda[0]
  to   = module.lambda_function_moved.aws_iam_role.lambda[0]
}

moved {
  from = module.lambda_function.aws_cloudwatch_log_group.lambda[0]
  to   = module.lambda_function_moved.aws_cloudwatch_log_group.lambda[0]
}

moved {
  from = module.lambda_function.aws_iam_role_policy.logs[0]
  to   = module.lambda_function_moved.aws_iam_role_policy.logs[0]
}

moved {
  from = module.lambda_function.local_file.archive_plan[0]
  to   = module.lambda_function_moved.local_file.archive_plan[0]
}

moved {
  from = module.lambda_function.null_resource.archive[0]
  to   = module.lambda_function_moved.null_resource.archive[0]
}

moved {
  from = module.lambda_function.terraform_data.package_filename_for_hash[0]
  to   = module.lambda_function_moved.terraform_data.package_filename_for_hash[0]
}
EOF
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  for addr in aws_lambda_function.this aws_iam_role.lambda aws_cloudwatch_log_group.lambda; do
    grep -qE "^  # module\\.lambda_function_moved\\.${addr}\\[0\\] will be updated in-place" <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to module.lambda_function_moved.$addr[0]"; }
  done
  grep -qF 'Plan: 0 to add, 3 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly three in-place changes - the three taggable resources under the module"; }
  log "  choudoufu: zero churn, three in-place tags updates - the marker rewrite the moved block completes on all three taggable resources"

  MOVED_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 3 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly three in-place changes"; }

  LAMBDA_ARN_D1_AFTER="$(awsl lambda get-function --function-name "$FN_NAME" --query 'Configuration.FunctionArn' --output text)"
  [ "$LAMBDA_ARN_D1_AFTER" = "$LAMBDA_ARN" ] || fail "the function's ARN changed across the rename ($LAMBDA_ARN -> $LAMBDA_ARN_D1_AFTER) - it was destroyed and recreated, not renamed"
  ADDR_D1_LAMBDA="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-address"' --output text)"
  [ "$ADDR_D1_LAMBDA" = "module.lambda_function_moved.aws_lambda_function.this:0" ] \
    || fail "the function carries tofu-address=$ADDR_D1_LAMBDA after the rename, not module.lambda_function_moved.aws_lambda_function.this:0"
  ADDR_D1_ROLE="$(awsl iam list-role-tags --role-name "$FN_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ADDR_D1_ROLE" = "module.lambda_function_moved.aws_iam_role.lambda:0" ] \
    || fail "the role carries tofu-address=$ADDR_D1_ROLE after the rename, not module.lambda_function_moved.aws_iam_role.lambda:0"
  ADDR_D1_LOGGROUP="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-address"' --output text 2>/dev/null \
    || awsl logs list-tags-log-group --log-group-name "/aws/lambda/${FN_NAME}" --query 'tags."tofu-address"' --output text)"
  [ "$ADDR_D1_LOGGROUP" = "module.lambda_function_moved.aws_cloudwatch_log_group.lambda:0" ] \
    || fail "the log group carries tofu-address=$ADDR_D1_LOGGROUP after the rename, not module.lambda_function_moved.aws_cloudwatch_log_group.lambda:0"
  log "  all three live objects unchanged, tofu-address now under module.lambda_function_moved - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module.lambda_function_moved -> module.lambda_function_final, no moved block at all ==="
  sed -i.bak 's/module "lambda_function_moved" {/module "lambda_function_final" {/' "$EST/main.tf"
  sed -i.bak 's/module\.lambda_function_moved\./module.lambda_function_final./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the live-mv rename's reinit failed"; }

  # This leg used to crash outright: live-mv on ANY resource in this
  # estate - taggable or not, the function itself included - failed with
  # "Record-backed instance with no record store" (internal/live/
  # projection/build.go:1676) the moment it walked this configuration's
  # OTHER record-located resources (random_pet.this, local_file.
  # archive_plan[0], null_resource.archive[0], terraform_data.package_
  # filename_for_hash[0]). Root cause: internal/live/mv/mv.go's
  # materialize() called projection.BuildFrom(ctx, m.req.Config, list,
  # m.req.Providers) - the record-store-less convenience wrapper - instead
  # of projection.BuildWith(..., projection.Options{RecordStore: ...}), the
  # one live-plan's own path uses (internal/command/live_plan.go).
  # aws_lambda_function.this is a ClassParentDerived resolution (its
  # function_name reads random_pet.this.id), so materialize's own "the
  # whole resolution list goes in for that case" branch handed the full
  # per-estate resolution list to BuildFrom, which then reached the
  # record-backed siblings with no store to read them through. Fixed:
  # materialize() now calls BuildWith with Options{RecordStore:
  # m.req.RecordStore}, exactly what live-plan already passed - see the
  # gauntlet:corpus-lambda-simple/day2_rename fix commit. Three calls
  # below, one per taggable child under the module (the function, the
  # role, the log group) - live-mv moves one resource instance at a time.
  MV_FN_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.lambda_function_moved.aws_lambda_function.this[0]' 'module.lambda_function_final.aws_lambda_function.this[0]' 2>&1)"; MV_FN_RC=$?
  [ "$MV_FN_RC" -eq 0 ] || { printf '%s\n' "$MV_FN_OUT" | tail -30; fail "choudoufu live-mv on aws_lambda_function.this exited $MV_FN_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_FN_OUT" \
    || { printf '%s\n' "$MV_FN_OUT"; fail "live-mv on aws_lambda_function.this did not report a real write"; }
  grep -qF '"module.lambda_function_moved.aws_lambda_function.this:0" -> "module.lambda_function_final.aws_lambda_function.this:0"' <<< "$MV_FN_OUT" \
    || { printf '%s\n' "$MV_FN_OUT"; fail "live-mv on aws_lambda_function.this did not report rewriting the tofu-address marker from the old address to the new one"; }

  MV_ROLE_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.lambda_function_moved.aws_iam_role.lambda[0]' 'module.lambda_function_final.aws_iam_role.lambda[0]' 2>&1)"; MV_ROLE_RC=$?
  [ "$MV_ROLE_RC" -eq 0 ] || { printf '%s\n' "$MV_ROLE_OUT" | tail -30; fail "choudoufu live-mv on aws_iam_role.lambda exited $MV_ROLE_RC"; }
  grep -qF '"module.lambda_function_moved.aws_iam_role.lambda:0" -> "module.lambda_function_final.aws_iam_role.lambda:0"' <<< "$MV_ROLE_OUT" \
    || { printf '%s\n' "$MV_ROLE_OUT"; fail "live-mv on aws_iam_role.lambda did not report rewriting the tofu-address marker from the old address to the new one"; }

  MV_LOG_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.lambda_function_moved.aws_cloudwatch_log_group.lambda[0]' 'module.lambda_function_final.aws_cloudwatch_log_group.lambda[0]' 2>&1)"; MV_LOG_RC=$?
  [ "$MV_LOG_RC" -eq 0 ] || { printf '%s\n' "$MV_LOG_OUT" | tail -30; fail "choudoufu live-mv on aws_cloudwatch_log_group.lambda exited $MV_LOG_RC"; }
  grep -qF '"module.lambda_function_moved.aws_cloudwatch_log_group.lambda:0" -> "module.lambda_function_final.aws_cloudwatch_log_group.lambda:0"' <<< "$MV_LOG_OUT" \
    || { printf '%s\n' "$MV_LOG_OUT"; fail "live-mv on aws_cloudwatch_log_group.lambda did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: all three taggable children renamed, one call each, zero churn - the mv.go RecordStore wiring gap is fixed"

  LAMBDA_ARN_D2_AFTER="$(awsl lambda get-function --function-name "$FN_NAME" --query 'Configuration.FunctionArn' --output text)"
  [ "$LAMBDA_ARN_D2_AFTER" = "$LAMBDA_ARN" ] || fail "the function's ARN changed across live-mv ($LAMBDA_ARN -> $LAMBDA_ARN_D2_AFTER) - it was destroyed and recreated, not renamed"
  ADDR_D2_LAMBDA="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-address"' --output text)"
  [ "$ADDR_D2_LAMBDA" = "module.lambda_function_final.aws_lambda_function.this:0" ] \
    || fail "the function carries tofu-address=$ADDR_D2_LAMBDA after live-mv, not module.lambda_function_final.aws_lambda_function.this:0"
  ADDR_D2_ROLE="$(awsl iam list-role-tags --role-name "$FN_NAME" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$ADDR_D2_ROLE" = "module.lambda_function_final.aws_iam_role.lambda:0" ] \
    || fail "the role carries tofu-address=$ADDR_D2_ROLE after live-mv, not module.lambda_function_final.aws_iam_role.lambda:0"
  ADDR_D2_LOGGROUP="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-address"' --output text 2>/dev/null \
    || awsl logs list-tags-log-group --log-group-name "/aws/lambda/${FN_NAME}" --query 'tags."tofu-address"' --output text)"
  [ "$ADDR_D2_LOGGROUP" = "module.lambda_function_final.aws_cloudwatch_log_group.lambda:0" ] \
    || fail "the log group carries tofu-address=$ADDR_D2_LOGGROUP after live-mv, not module.lambda_function_final.aws_cloudwatch_log_group.lambda:0"
  log "  all three live objects unchanged, tofu-address now under module.lambda_function_final - read via the AWS CLI"

  log "=== D3. one more plan: config and marker agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qE '^  # .+ will be' <<< "$FINAL_PLAN_OUT" \
    && { grep -E '^  # .+ will be' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  no resource change proposed. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.lambda_function renamed to module.lambda_function_moved with zero churn (0 add, 3 change, 0 destroy) across all seven of its stateful children, three taggable markers rewritten in place, three record-located children moved via their own per-resource moved blocks with zero diff, one config-derived child (aws_iam_role_policy.logs) needing none; stock oracle over the identical seven-resource move on cold_deploy's own state also shows zero churn beyond the module's own pre-existing null_resource.archive[0] package-timestamp noise (confirmed present on an unrelated baseline replan too); live-mv: module.lambda_function_moved renamed to module.lambda_function_final across all three taggable children (the function, the role, the log group), one call each, zero churn, markers rewritten in place - the internal/live/mv/mv.go materialize() RecordStore wiring gap (build.go:1676's \"Record-backed instance with no record store\") is fixed; all three live objects unchanged throughout, read via the AWS CLI; final replan is empty"

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed rename: module.lambda_function_final
  # (the function, the role and the log group all bound and converged under
  # it) is where this stage forces a replace. The log group's own
  # `logging_log_group`-derived `name` argument changes to a new literal - a
  # real, upstream-immutable argument on aws_cloudwatch_log_group (CloudWatch
  # Logs has no RenameLogGroup API, only CreateLogGroup/DeleteLogGroup) -
  # forcing a replace at the SAME declared address. F-ORACLE above already
  # confirmed, empirically, that stock marks the log group's name as forcing
  # replacement on cold_deploy's own state, cascading into two real in-place
  # updates: the function's own logging_config.log_group argument (main.tf:
  # 142 reads the same var directly) and the inline log policy's document
  # (it references the log group's ARN). Neither the function nor the
  # policy is destroyed or created - only the log group is.
  #
  # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
  # header for the fuller discussion). OpenTofu core rejects a `lifecycle`
  # block written directly on a `module` call, and this corpus's established
  # convention only ever removes real upstream module content, never adds
  # library-internal lifecycle blocks to it - so this exercises OpenTofu's
  # DEFAULT replace ordering (destroy-then-create) rather than the
  # create_before_destroy variant the stage's Title names. The
  # marker-on-new-object and clean-old-object outcomes this stage's Proves
  # text cares about are identical either way; BREAK=replace below
  # manufactures the coexistence a skipped destroy half would leave, the
  # same way corpus-sqs-basic's own BREAK=replace does.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.lambda_function_final.aws_cloudwatch_log_group.lambda[0]"
  F_RECORD="$EST/.tofu-records/tofu-records/$ESTATE/aws_cloudwatch_log_group/$(record_key "$F_ADDR")"

  log "=== F0. capture the live log group and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_LOGGROUP_NAME="/aws/lambda/${FN_NAME}"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  # aws_cloudwatch_log_group's import ID is the log group NAME, not its ARN
  # (terraform import aws_cloudwatch_log_group.x /aws/lambda/my-func) -
  # confirmed by this same assertion against the record file, not assumed.
  [ "$F_OLD_IMPORT_ID" = "$F_OLD_LOGGROUP_NAME" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $F_OLD_LOGGROUP_NAME"
  F_OLD_ADDR_TAG="$(awsl logs list-tags-for-resource --resource-arn "$LOGGROUP_ARN" --query 'tags."tofu-address"' --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.lambda_function_final.aws_cloudwatch_log_group.lambda:0" ] \
    || fail "$LOGGROUP_ARN does not carry tofu-address=module.lambda_function_final.aws_cloudwatch_log_group.lambda:0 ahead of day2_replace"
  log "  $LOGGROUP_ARN, record import_id=$F_OLD_IMPORT_ID (the log group's name, not its ARN), tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy half would leave behind ==="
    # A second, distinct live log group carrying the SAME tofu-address as
    # the one a genuine replace would destroy - the state "skip the destroy
    # half" of a create-before-destroy replace would leave, produced
    # directly via the AWS CLI rather than by actually interrupting an
    # apply (day2_crash, stage 10, owns testing a real interruption).
    BREAK_COLLISION_NAME="/aws/lambda/${FN_NAME}-collision"
    awsl logs create-log-group --log-group-name "$BREAK_COLLISION_NAME" \
      --tags "tofu-estate=$ESTATE,tofu-address=module.lambda_function_final.aws_cloudwatch_log_group.lambda:0" \
      >/dev/null || fail "BREAK=replace: could not create the collision log group"
    BREAK_COLLISION_ARN="arn:aws:logs:${REGION}:${ACCOUNT}:log-group:${BREAK_COLLISION_NAME}"
    BREAK_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
    awsl logs delete-log-group --log-group-name "$BREAK_COLLISION_NAME" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one address' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the address collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one address) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew logging_log_group-derived name, forcing a replace at the same declared address ==="
    sed -i.bak 's|function_name = "${random_pet.this.id}-lambda-simple"|function_name = "${random_pet.this.id}-lambda-simple"\n  logging_log_group = "/aws/lambda/${random_pet.this.id}-lambda-simple-v2"|' "$EST/main.tf"
    rm -f "$EST/main.tf.bak"
    grep -q 'lambda-simple-v2' "$EST/main.tf" || fail "adding module.lambda_function_final's logging_log_group argument did not match - the corpus pin has moved"
    F_NEW_NAME="/aws/lambda/${FN_NAME}-v2"
    F_NEW_ARN="arn:aws:logs:${REGION}:${ACCOUNT}:log-group:${F_NEW_NAME}"

    F_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.lambda_function_final\.aws_cloudwatch_log_group\.lambda\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.lambda_function_final's log group when its derived name changes"; }
    grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark the log group's name as forcing replacement"; }
    grep -qE '^  # module\.lambda_function_final\.aws_lambda_function\.this\[0\] will be updated in-place' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose updating the lambda function in-place when the log group name changes"; }
    F_OTHER="$(grep -E '^  # .+ (will be (destroyed|created)|must be replaced)' <<< "$F_PLAN_OUT" | grep -v 'null_resource\.archive\[0\]' | grep -v 'aws_cloudwatch_log_group\.lambda\[0\]' || true)"
    [ -z "$F_OTHER" ] \
      || { printf '%s\n' "$F_OTHER"; fail "choudoufu proposes a destroy, create or replace beyond the log group's own forced replace and the known null_resource.archive[0] baseline noise"; }
    log "  choudoufu: exactly one forced replace at the same declared address (module.lambda_function_final.aws_cloudwatch_log_group.lambda[0]), name forces replacement, cascading into the expected in-place updates (function, inline log policy), beyond the known null_resource.archive[0] baseline noise"

    F_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Apply complete' <<< "$F_APPLY_OUT" \
      || { printf '%s\n' "$F_APPLY_OUT" | tail -20; fail "the day2_replace apply did not complete"; }

    F_OLD_STILL="$(awsl logs describe-log-groups --log-group-name-prefix "$F_OLD_LOGGROUP_NAME" --query "logGroups[?logGroupName=='$F_OLD_LOGGROUP_NAME']" --output text 2>&1)"
    [ -z "$F_OLD_STILL" ] || { echo "$F_OLD_STILL"; fail "$F_OLD_LOGGROUP_NAME still exists after the replace - the old object was orphaned, not destroyed"; }
    log "  $F_OLD_LOGGROUP_NAME no longer exists (confirmed via the AWS CLI, not through choudoufu's own report)"

    F_NEW_ADDR_TAG="$(awsl logs list-tags-for-resource --resource-arn "$F_NEW_ARN" --query 'tags."tofu-address"' --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.lambda_function_final.aws_cloudwatch_log_group.lambda:0" ] \
      || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.lambda_function_final.aws_cloudwatch_log_group.lambda:0 - the marker did not move onto the new object"
    log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW object's import_id, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_NAME" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object's name $F_NEW_NAME - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(cd "$EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    F_FINAL_OTHER="$(grep -E '^  # .+ (will be (destroyed|created|updated)|must be replaced)' <<< "$F_FINAL_PLAN_OUT" | grep -v 'null_resource\.archive\[0\]' || true)"
    [ -z "$F_FINAL_OTHER" ] \
      || { printf '%s\n' "$F_FINAL_OTHER"; fail "the post-replace plan proposes a resource change beyond the known null_resource.archive[0] baseline noise"; }
    log "  no resource action proposed beyond the known null_resource.archive[0] baseline noise, no marker collision. The replace is complete and invisible to the next plan."

    gauntlet_stage day2_replace pass "choudoufu: changing module.lambda_function_final's ForceNew logging_log_group-derived name proposed exactly one replace at the same declared address (the log group; -/+ destroy and then create) cascading into two expected in-place updates (the function's logging_config, the inline log policy's document) and nothing else beyond the module's own pre-existing null_resource.archive[0] package-timestamp noise; applied cleanly; the old object ($LOGGROUP_ARN) is confirmed gone and the new object ($F_NEW_ARN) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new object's import_id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action beyond the same known noise; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace plus the same in-place cascade (plan only, not applied); BREAK=replace confirms a manufactured marker collision is reported loudly rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.lambda_function_final
  # is bound and converged, and it is the estate's ONLY module call - the
  # ONLY thing this estate ever creates besides random_pet.this (record-
  # located, unaffected by this removal, live-mv does not touch it either -
  # see Part D's own header). Deleting the module block outright removes
  # all seven of its stateful children at once: the three taggable ones
  # (the function, the role, the log group) plus the three record-located
  # ones and the one config-derived child. outputs.tf's 23 root outputs all
  # read module.lambda_function_final.* - the only module that ever
  # produced these values - so they are removed along with the block, the
  # same edit a person deleting this module call would make.
  #
  # Known, non-fabricated gap this stage's own removal-detection sweep runs
  # into and does not paper over: this script's header already documents
  # that floci's resourcegroupstaggingapi GetResources does not index
  # CloudWatch Logs log groups (found building stage 4's own object count,
  # confirmed by reading logs:list-tags-for-resource directly against the
  # SAME live object, which answers correctly). The estate-wide removal
  # sweep (internal/live/discovery/tagging.go's sweepViaTagging) is exactly
  # that same GetResources call, so the log group's orphan is invisible to
  # it the identical way. Asserted below by NAME, not glossed over: the
  # function and the role must each get their own destroy line; the log
  # group's is allowed to be missing, and the assertion says so rather than
  # silently accepting "fewer destroys than expected".
  #
  # BREAK_REMOVE=1 exercises this stage's own Break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go, verbatim.
  #
  # TOFU_DISABLE_GUIDED_DISCOVERY=1 on every plan/apply below: this estate
  # declares a record_store, so guided discovery (internal/live/discovery/
  # guided.go) turns on by default, and by the time this part runs, stage
  # 2's migrate, stage 4's test_apply, stage 5's drift replan and Part D's
  # own three plans have all already written a fresh hint recording
  # aws_lambda_function/aws_iam_role/aws_cloudwatch_log_group as "checked,
  # nothing to see" - guided.go's own doc comment names exactly this trade
  # ("a standing orphan of a hinted type may not resurface on every single
  # routine plan, only at the next full or verification sweep"), and this
  # part's whole job is proving the type IS a genuine orphan the moment its
  # block disappears, not waiting out GuidedVerifyAge's window. The env var
  # is the documented operational escape hatch (live_plan.go's own comment:
  # "an operational lever for a run that is misbehaving, not a decision a
  # team checks in") - forcing the full, unguided sweep this stage needs to
  # mean anything.

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live objects one more time ==="
  E_LAMBDA_ADDR="$(awsl lambda list-tags --resource "$LAMBDA_ARN" --query 'Tags."tofu-address"' --output text 2>/dev/null || true)"
  [ "$E_LAMBDA_ADDR" = "module.lambda_function_final.aws_lambda_function.this:0" ] \
    || fail "$LAMBDA_ARN does not carry tofu-address=module.lambda_function_final.aws_lambda_function.this:0 before day2_remove even starts (got $E_LAMBDA_ADDR)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.lambda_function_final's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.lambda_function_final's block ==="
    python3 - "$EST/main.tf" <<'PY' || fail "removing module.lambda_function_final's block failed"
import re, sys
path = sys.argv[1]
text = open(path).read()
m = re.search(r'\nmodule "lambda_function_final" \{\n', text)
if not m:
    sys.exit("module.lambda_function_final's block was not found - the config has moved")
start = m.start()
i = m.end()
depth = 1
while depth > 0:
    if text[i] == "{":
        depth += 1
    elif text[i] == "}":
        depth -= 1
    i += 1
# i is now just past the matching closing brace; consume the trailing newline too.
end = i
if text[end:end + 1] == "\n":
    end += 1
open(path, "w").write(text[:start] + text[end:])
PY
    grep -q 'module "lambda_function_final"' "$EST/main.tf" \
      && fail "removing module.lambda_function_final's block did not match - the config has moved"
    cat > "$EST/outputs.tf" <<'EOF'
# All root outputs removed along with module.lambda_function_final's block
# (day2_remove) - every one of them read module.lambda_function_final.*.
EOF
    # module.lambda_function_final was the only caller of hashicorp/local and
    # hashicorp/null anywhere in this config - the root's own versions.tf
    # never named either provider directly, only ever picking them up
    # through the module call's own required_providers. Removing the call
    # removes that requirement too, and without it a plan cannot instantiate
    # either provider to reason about the local_file/null_resource records
    # the module's own three record-located children left behind (the
    # record store still holds them - live-import's #340 residue write never
    # goes away just because the block that produced it did). A real
    # operator tearing this module down keeps the provider requirement until
    # its own leftovers are cleaned up for exactly the same reason; this is
    # that, not a special case for the test.
    perl -0pi -e 's/(random = \{\n      source  = "hashicorp\/random"\n      version = ">= 2\.0"\n    \}\n)(  \}\n)/$1    local = {\n      source  = "hashicorp\/local"\n      version = ">= 2.0"\n    }\n    null = {\n      source  = "hashicorp\/null"\n      version = ">= 3.0"\n    }\n$2/' "$EST/versions.tf"
    grep -q 'source  = "hashicorp/local"' "$EST/versions.tf" \
      || fail "the day2_remove required_providers delta did not match versions.tf - the corpus pin has moved"
    ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld a destroy as a possible rename (discovery.go's classifyOrphans) even though no other block anywhere in this config declares an instance of any of these types - this is the honest wall issue #358 names, not a pass"
    fi
    for addr in 'module\.lambda_function_final\.aws_lambda_function\.this\[0\]' \
                'module\.lambda_function_final\.aws_iam_role\.lambda\[0\]' \
                'module\.lambda_function_final\.aws_iam_role_policy\.logs\[0\]' \
                'module\.lambda_function_final\.local_file\.archive_plan\[0\]' \
                'module\.lambda_function_final\.null_resource\.archive\[0\]' \
                'module\.lambda_function_final\.terraform_data\.package_filename_for_hash\[0\]'; do
      grep -qE "^  # ${addr} will be destroyed" <<< "$REMOVE_PLAN_OUT" \
        || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying $addr when its block is deleted"; }
    done
    log "  choudoufu: destroys proposed for the function, the role, its inline log policy, and all three record-located children"
    # aws_iam_role_policy.logs[0] (the role's inline CloudWatch Logs policy)
    # used to be missing from this checklist entirely - a genuine leak: an
    # untaggable child (inline policies carry no tags attribute at all) whose
    # parent role IS being destroyed in the same plan, exactly the shape
    # day2_remove's own Proves text names ("including blocks for untaggable
    # children whose parents stay" - here the parent doesn't even stay, so
    # leaving the inline policy behind would have been strictly worse). Fixed
    # by day2_replace's own unit: WANT_DESTROY_COUNT moves from 5/6 to 6/7,
    # confirmed against the AWS CLI below (the#398-guard shape in reverse -
    # a MISSING destroy is exactly as dangerous as a wrong marker when the
    # thing left behind is an orphaned IAM permission).
    if grep -qE '^  # module\.lambda_function_final\.aws_cloudwatch_log_group\.lambda\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT"; then
      log "  choudoufu ALSO proposed destroying the log group - stronger than expected (floci#? may have closed the GetResources log-group gap); not a failure"
      WANT_DESTROY_COUNT=7
    else
      log "  the log group's destroy is correctly absent - the documented floci gap (GetResources does not index CloudWatch Logs), not a choudoufu defect"
      WANT_DESTROY_COUNT=6
    fi
    N_DESTROY="$(grep -cE '^  # .+ will be destroyed' <<< "$REMOVE_PLAN_OUT")"
    [ "$N_DESTROY" = "$WANT_DESTROY_COUNT" ] \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's remove plan proposes $N_DESTROY destroys, expected exactly $WANT_DESTROY_COUNT"; }
    grep -qE '^  # .+ will be created' <<< "$REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's remove plan proposes a create - it should propose only destroys"; }

    REMOVE_APPLY_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE "Resources: 0 added, 0 changed, $WANT_DESTROY_COUNT destroyed" <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly $WANT_DESTROY_COUNT destroys"; }

    if E_LAMBDA_STILL="$(awsl lambda get-function --function-name "$FN_NAME" 2>&1)"; then
      echo "$E_LAMBDA_STILL"; fail "$LAMBDA_ARN still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    if E_ROLE_STILL="$(awsl iam get-role --role-name "$FN_NAME" 2>&1)"; then
      echo "$E_ROLE_STILL"; fail "the role $FN_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    if E_LOGPOLICY_STILL="$(awsl iam get-role-policy --role-name "$FN_NAME" --policy-name "${FN_NAME}-logs" 2>&1)"; then
      echo "$E_LOGPOLICY_STILL"; fail "the inline policy ${FN_NAME}-logs still exists in the live account after the destroy - it was orphaned, not destroyed (the leak WANT_DESTROY_COUNT=6/7 fixes)"
    fi
    log "  the function, the role and its inline CloudWatch Logs policy no longer exist (confirmed via the AWS CLI, not through choudoufu's own report - get-role-policy on the deleted policy name now returns NoSuchEntity the same way get-role does for the deleted role)"

    log "=== E2. one more plan: config and reality agree on what could be swept, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|updated)' <<< "$E_FINAL_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan proposes a create or update"; }
    if [ "$WANT_DESTROY_COUNT" = 7 ]; then
      grep -qE '^  # .+ will be destroyed' <<< "$E_FINAL_PLAN_OUT" \
        && { grep -E '^  # .+ will be' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan still proposes a destroy"; }
    fi
    log "  no further resource action proposed. The removal is complete."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.lambda_function_final's block proposed $WANT_DESTROY_COUNT destroys (the function, the role, its inline aws_iam_role_policy.logs[0] CloudWatch Logs policy, and all three record-located children always; the log group's only when floci's GetResources happens to index it - a documented emulator gap, confirmed by reading logs:list-tags-for-resource directly against the same live object), applied cleanly, the function, the role and the inline log policy genuinely gone from the live account (read via the AWS CLI, not choudoufu's own report), and the next plan proposes no further resource action; classifyOrphans did not withhold any destroy as a possible rename. WANT_DESTROY_COUNT moved from 5/6 to 6/7 in this same commit: the inline log policy was previously missing from this stage's own checklist entirely - a genuine leak (an untaggable IAM permission left behind on every destroy of this estate), not a stale assertion, fixed as part of the day2_replace unit that re-measured this stage"

    # ══════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8,
    # issue #643's board-repair sweep)
    # ══════════════════════════════════════════════════════════════════════
    #
    # THERE IS NO REAL SCALABLE KNOB IN THIS ESTATE, checked against
    # .corpus/lambda's own declarations before falling back to anything.
    # The module declares 38 count/for_each expressions and every one of
    # them is either a boolean create toggle (`count = local.create && ...
    # ? 1 : 0`, which is 0 or 1 and never a set) or a for_each over a map
    # that is empty in this example (local.qualifiers, var.allowed_triggers,
    # var.event_source_mapping). The only two that take a NUMBER -
    # iam.tf:255's `var.attach_policy_jsons ? var.number_of_policy_jsons : 0`
    # and iam.tf:278's `var.attach_policies ? var.number_of_policies : 0` -
    # are gated behind attach_policy_jsons/attach_policies, both false in
    # this example, and both drive aws_iam_role_policy_attachment, a type
    # with no tags argument at all and so no marker to read back through the
    # AWS CLI at all. Turning either on would also change the estate every
    # earlier stage in this script already measured. The example's own root
    # module (.corpus/lambda/examples/simple/main.tf) declares no count and
    # no for_each anywhere.
    #
    # So this section uses the sanctioned fallback that live/GAUNTLET.md #8
    # allows and reference-ec2-vpc's Part F and corpus-iam-policy's Part G
    # set the precedent for: a NEW, self-contained synthetic count block -
    # aws_iam_role.count_test, count = 2, scaled 2 -> 1 -> 2 - of a type
    # this estate already exercises (module.lambda_function.aws_iam_role.
    # lambda[0] is stamped at stage 2 and re-read through the AWS CLI at
    # Part D). Nothing else in this config names it, and it is added only
    # after Part E's real, completed removal, so day2_count's own history
    # never touches a resource any earlier part depends on.
    #
    # WHAT WITNESSES THE DESTROY, established by probing floci directly with
    # no tofu anywhere in the loop before this section was written (create a
    # role, read it, delete it, create it again under the SAME name, read it
    # again): aws_iam_role carries a server-minted RoleId that the emulator
    # mints fresh on every CreateRole - AROA1NFPSKTQARHO46JT then
    # AROA0QTBL681ZNKGIUYN across two successive creates of one name, with
    # CreateDate moving too - and GetRole answers NoSuchEntity in between.
    # An IAM role's ARN cannot witness anything here, because
    # arn:aws:iam::<account>:role/<name> is rebuilt from the account and the
    # name; a Lambda function's ARN has the same problem (region + account +
    # name), which is why the scalable type here is the role and not the
    # function this estate is named for. RoleId is this estate's equivalent
    # of the security group GroupId reference-ec2-vpc's Part F reads. BOTH
    # witnesses are asserted below: verified absence while the instance is
    # gone, and a genuinely NEW RoleId when it comes back.
    #
    # G0 is this stage's stock oracle (live/GAUNTLET.md #8: "Stock's plan
    # for the same count change, normalised"). Stock never had this count
    # block, so there is nothing in cold_deploy's own state to reuse the way
    # day2_remove/day2_replace reuse theirs: G0 stands the IDENTICAL block
    # up for real with plain terraform, in its own working directory, on
    # $ORACLE_ENDPOINT - the third container the greenfield stage already
    # starts, idle since that stage finished and never written to again.
    # A separate endpoint rather than corpus-iam-policy's tear-the-oracle-
    # down-first arrangement, because this block's role NAME is
    # deterministic rather than name_prefix-suffixed: two live roles called
    # lambda-simple-count-test-0 cannot coexist in one account, so sharing
    # $ENDPOINT would collide outright instead of merely confusing a lookup.
    # G1-G4 are the real choudoufu side, against the adopted estate.
    #
    # TOFU_DISABLE_GUIDED_DISCOVERY=1 on every plan and apply below, for the
    # reason Part E's own header gives: this estate declares a record_store,
    # so guided discovery is on by default and by this point every plan in
    # the script has written hints. The full, unguided sweep is the stronger
    # check and the one Part E just used, so day2_count keeps it rather than
    # silently changing the discovery mode mid-script.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG instance
    # (count_test[0], the survivor, rather than count_test[1]) was the one
    # destroyed - the Break text in tools/gauntlet/stages.go for day2_count,
    # verbatim: "Expect a different instance to be destroyed; the assertion
    # must fail." It is independent of BREAK, BREAK_RENAME and BREAK_REMOVE,
    # and only reachable on the real, non-BREAK_REMOVE path, since
    # day2_count starts from Part E's real, completed removal.

    gauntlet_begin_stage day2_count

    # count_test_block($1 = count) is day2_count's own resource. The SAME
    # text feeds the stock oracle (G0) and choudoufu (G1-G4), so the two
    # sides cannot drift apart into different shapes. Unquoted heredoc so $1
    # interpolates; ${count.index} is escaped so bash never expands it.
    count_test_block() {
      local n="$1"
      cat <<COUNTEOF
resource "aws_iam_role" "count_test" {
  count = $n
  name  = "lambda-simple-count-test-\${count.index}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })

  tags = {
    Name = "day2_count evidence (issue #643)"
  }
}
COUNTEOF
    }

    # Both sides are read through the AWS CLI, never through choudoufu's or
    # terraform's own report of itself. role_id() prints the server-minted
    # RoleId (empty if the role does not exist - that emptiness is itself
    # one of the two destroy witnesses); role_addr() prints the tofu-address
    # marker, the same list-role-tags read Part D already uses on the
    # module's own role.
    awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }
    role_id()   { awsl iam get-role --role-name "$1" --query 'Role.RoleId' --output text 2>/dev/null; }
    role_addr() { awsl iam list-role-tags --role-name "$1" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null; }
    oracle_role_id() { awso iam get-role --role-name "$1" --query 'Role.RoleId' --output text 2>/dev/null; }

    CT0="lambda-simple-count-test-0"
    CT1="lambda-simple-count-test-1"

    log "=== G0. day2_count stock oracle: the identical 2-instance count block, plain terraform, on the idle greenfield-oracle endpoint ==="
    ORACLE_COUNT_DIR="$WORK/oracle-count"
    mkdir -p "$ORACLE_COUNT_DIR"
    oracle_count_config() {
      cat <<'OHDR'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.28"
    }
  }
}

provider "aws" {
  region                      = "eu-west-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_requesting_account_id  = true
  s3_use_path_style           = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
  skip_credentials_validation = true
}

OHDR
      count_test_block "$1"
    }
    oracle_count_config 2 > "$ORACLE_COUNT_DIR/main.tf"
    ( cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's terraform init failed"; }
    O_UP_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)"; O_UP_APPLY_RC=$?
    [ "$O_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$O_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
    grep -qE 'Apply complete! Resources: 2 added, 0 changed, 0 destroyed' <<< "$O_UP_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$O_UP_APPLY_OUT"; fail "stock did not create exactly 2 count-test roles for the day2_count oracle"; }
    O_CT0_ID="$(oracle_role_id "$CT0")"
    O_CT1_ID="$(oracle_role_id "$CT1")"
    [ -n "$O_CT0_ID" ] && [ "$O_CT0_ID" != "None" ] || fail "the day2_count oracle's count_test[0] role has no RoleId - it was not created"
    [ -n "$O_CT1_ID" ] && [ "$O_CT1_ID" != "None" ] || fail "the day2_count oracle's count_test[1] role has no RoleId - it was not created"
    [ "$O_CT0_ID" != "$O_CT1_ID" ] || fail "the day2_count oracle's two roles share one RoleId ($O_CT0_ID) - the emulator is not minting per-role ids, so RoleId cannot witness anything"
    log "  stock: 2 instances created, count_test[0] RoleId=$O_CT0_ID count_test[1] RoleId=$O_CT1_ID"

    oracle_count_config 1 > "$ORACLE_COUNT_DIR/main.tf"
    O_DOWN_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_DOWN_PLAN_RC=$?
    [ "$O_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$O_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $O_DOWN_PLAN_RC"; }
    grep -qE '^  # aws_iam_role\.count_test\[1\] will be destroyed' <<< "$O_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$O_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
    grep -qE '^  # aws_iam_role\.count_test\[0\] will be' <<< "$O_DOWN_PLAN_OUT" \
      && { printf '%s\n' "$O_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$O_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$O_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
    O_DOWN_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)"; O_DOWN_APPLY_RC=$?
    [ "$O_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$O_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$O_DOWN_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$O_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
    [ "$(oracle_role_id "$CT0")" = "$O_CT0_ID" ] || fail "stock's surviving count_test[0] changed RoleId across the scale-down"
    O_CT1_GONE="$(oracle_role_id "$CT1")"
    [ -z "$O_CT1_GONE" ] || fail "stock's count_test[1] still answers GetRole (RoleId=$O_CT1_GONE) after the scale-down destroy"
    log "  stock: exactly one destroy (count_test[1]), it no longer answers GetRole, count_test[0] RoleId=$O_CT0_ID unchanged"

    oracle_count_config 2 > "$ORACLE_COUNT_DIR/main.tf"
    O_UP2_PLAN_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_UP2_PLAN_RC=$?
    [ "$O_UP2_PLAN_RC" -eq 0 ] || { printf '%s\n' "$O_UP2_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $O_UP2_PLAN_RC"; }
    grep -qE '^  # aws_iam_role\.count_test\[1\] will be created' <<< "$O_UP2_PLAN_OUT" \
      || { printf '%s\n' "$O_UP2_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
    grep -qE '^  # aws_iam_role\.count_test\[0\] will be' <<< "$O_UP2_PLAN_OUT" \
      && { printf '%s\n' "$O_UP2_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$O_UP2_PLAN_OUT" \
      || { printf '%s\n' "$O_UP2_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
    O_UP2_APPLY_OUT="$(cd "$ORACLE_COUNT_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)"; O_UP2_APPLY_RC=$?
    [ "$O_UP2_APPLY_RC" -eq 0 ] || { printf '%s\n' "$O_UP2_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$O_UP2_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$O_UP2_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
    O_CT1_NEW_ID="$(oracle_role_id "$CT1")"
    [ -n "$O_CT1_NEW_ID" ] && [ "$O_CT1_NEW_ID" != "None" ] || fail "stock's recreated count_test[1] has no RoleId"
    [ "$O_CT1_NEW_ID" != "$O_CT1_ID" ] || fail "stock's recreated count_test[1] came back with the SAME RoleId ($O_CT1_ID) - the destroy oracle is not real"
    [ "$(oracle_role_id "$CT0")" = "$O_CT0_ID" ] || fail "stock's count_test[0] changed RoleId across the scale-up"
    log "  stock: exactly one create (count_test[1] back under a NEW RoleId $O_CT1_NEW_ID, was $O_CT1_ID), count_test[0] RoleId=$O_CT0_ID unchanged throughout"
    ORACLE_SHAPE="destroy the higher index only (0 add, 0 change, 1 destroy), create the higher index back under a new RoleId ($O_CT1_ID -> $O_CT1_NEW_ID), the lower index's RoleId ($O_CT0_ID) unchanged both times"

    log "=== G1. choudoufu: add aws_iam_role.count_test, count = 2 ==="
    count_test_block 2 > "$EST/count_test.tf"
    CT_ADD_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; CT_ADD_PLAN_RC=$?
    [ "$CT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CT_ADD_PLAN_OUT" | tail -40; fail "the count-block-add plan exited $CT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$CT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$CT_ADD_PLAN_OUT" | grep -E '^  # .+ will be|^Plan:'; fail "adding the count block did not plan exactly 2 creates"; }
    CT_ADD_APPLY_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CT_ADD_APPLY_RC=$?
    [ "$CT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$CT_ADD_APPLY_OUT" | tail -40; fail "the count-block-add apply exited $CT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$CT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$CT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    CT0_ID="$(role_id "$CT0")"
    CT1_ID="$(role_id "$CT1")"
    [ -n "$CT0_ID" ] && [ "$CT0_ID" != "None" ] || fail "count_test[0] ($CT0) has no live RoleId after the create"
    [ -n "$CT1_ID" ] && [ "$CT1_ID" != "None" ] || fail "count_test[1] ($CT1) has no live RoleId after the create"
    [ "$CT0_ID" != "$CT1_ID" ] || fail "both count_test instances report the same RoleId ($CT0_ID)"
    # live/MARKERS.md: an indexed instance's tofu-address is colon-escaped,
    # aws_x.count_test[0] -> aws_x.count_test:0. Asserted BY VALUE, not by
    # "a tag exists" - a wrong marker outranks a missing one.
    CT0_ADDR="$(role_addr "$CT0")"
    CT1_ADDR="$(role_addr "$CT1")"
    [ "$CT0_ADDR" = 'aws_iam_role.count_test:0' ] || fail "count_test[0]'s live tofu-address tag is $CT0_ADDR, not aws_iam_role.count_test:0"
    [ "$CT1_ADDR" = 'aws_iam_role.count_test:1' ] || fail "count_test[1]'s live tofu-address tag is $CT1_ADDR, not aws_iam_role.count_test:1"
    log "  2 instances created: index 0 RoleId=$CT0_ID (tofu-address=$CT0_ADDR), index 1 RoleId=$CT1_ID (tofu-address=$CT1_ADDR) - read via the AWS CLI"

    CT_NOOP_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; CT_NOOP_PLAN_RC=$?
    [ "$CT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CT_NOOP_PLAN_OUT" | tail -40; fail "the post-add plan exited $CT_NOOP_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$CT_NOOP_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$CT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  the plan right after the create is empty - both new instances bound their own markers"

    log "=== G2. choudoufu: scale count down, 2 -> 1 ==="
    count_test_block 1 > "$EST/count_test.tf"
    CT_DOWN_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; CT_DOWN_PLAN_RC=$?
    [ "$CT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CT_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $CT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      # The Break control, verbatim from tools/gauntlet/stages.go's
      # day2_count entry: "Expect a different instance to be destroyed; the
      # assertion must fail." So this asserts count_test[0] - the SURVIVOR -
      # is the one destroyed. It must not hold, and this stage must report
      # fail rather than pass, which is what fail() below does.
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) is the one destroyed"
      grep -qE '^  # aws_iam_role\.count_test\[0\] will be destroyed' <<< "$CT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$CT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_COUNT=1 (the Break control): the scale-down plan does NOT destroy count_test[0], so asserting a different instance was destroyed fails - which is exactly what this control has to prove about G2's real assertion"; }
      fail "BREAK_COUNT=1: the scale-down plan destroys count_test[0], the instance that must survive - day2_count's real assertion is not load-bearing"
    fi

    grep -qE '^  # aws_iam_role\.count_test\[1\] will be destroyed' <<< "$CT_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$CT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
    grep -qE '^  # aws_iam_role\.count_test\[0\] will be' <<< "$CT_DOWN_PLAN_OUT" \
      && { printf '%s\n' "$CT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$CT_DOWN_PLAN_OUT" \
      || { printf '%s\n' "$CT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be|^Plan:'; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched - the same shape stock showed at G0"

    CT_DOWN_APPLY_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CT_DOWN_APPLY_RC=$?
    [ "$CT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$CT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $CT_DOWN_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$CT_DOWN_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$CT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

    # Destroy witness 1 of 2: verified ABSENCE. GetRole on the destroyed
    # instance's name must answer NoSuchEntity, so role_id() prints nothing.
    CT1_AFTER_DOWN="$(role_id "$CT1")"
    [ -z "$CT1_AFTER_DOWN" ] || fail "count_test[1] ($CT1) still answers GetRole with RoleId=$CT1_AFTER_DOWN after the scale-down destroy - it was orphaned, not destroyed"
    CT0_AFTER_DOWN="$(role_id "$CT0")"
    [ "$CT0_AFTER_DOWN" = "$CT0_ID" ] || fail "count_test[0]'s live RoleId changed across the scale-down ($CT0_ID -> $CT0_AFTER_DOWN) - it was destroyed and recreated, not left alone"
    CT0_ADDR_AFTER_DOWN="$(role_addr "$CT0")"
    [ "$CT0_ADDR_AFTER_DOWN" = 'aws_iam_role.count_test:0' ] || fail "count_test[0]'s tofu-address marker changed across the scale-down: $CT0_ADDR_AFTER_DOWN"
    log "  $CT1 no longer answers GetRole; $CT0 keeps RoleId=$CT0_ID and tofu-address=$CT0_ADDR_AFTER_DOWN - all read via the AWS CLI"

    log "=== G3. choudoufu: scale count back up, 1 -> 2 ==="
    count_test_block 2 > "$EST/count_test.tf"
    CT_UP_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; CT_UP_PLAN_RC=$?
    [ "$CT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CT_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $CT_UP_PLAN_RC"; }
    grep -qE '^  # aws_iam_role\.count_test\[1\] will be created' <<< "$CT_UP_PLAN_OUT" \
      || { printf '%s\n' "$CT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
    grep -qE '^  # aws_iam_role\.count_test\[0\] will be' <<< "$CT_UP_PLAN_OUT" \
      && { printf '%s\n' "$CT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$CT_UP_PLAN_OUT" \
      || { printf '%s\n' "$CT_UP_PLAN_OUT" | grep -E '^  # .+ will be|^Plan:'; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
    log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched - the same shape stock showed at G0"

    CT_UP_APPLY_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CT_UP_APPLY_RC=$?
    [ "$CT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$CT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $CT_UP_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$CT_UP_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$CT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

    # Destroy witness 2 of 2: a genuinely NEW server-minted RoleId under the
    # same deterministic name. The ARN is identical either way (it is built
    # from the account and the name), so it is the RoleId that says the
    # object is new rather than the old one having survived.
    CT1_NEW_ID="$(role_id "$CT1")"
    [ -n "$CT1_NEW_ID" ] && [ "$CT1_NEW_ID" != "None" ] || fail "count_test[1] ($CT1) has no live RoleId after the scale-up"
    [ "$CT1_NEW_ID" != "$CT1_ID" ] || fail "count_test[1] came back with the SAME RoleId ($CT1_ID) it had before being destroyed - the destroy in G2 was not real"
    CT1_NEW_ADDR="$(role_addr "$CT1")"
    [ "$CT1_NEW_ADDR" = 'aws_iam_role.count_test:1' ] || fail "the recreated count_test[1] carries tofu-address=$CT1_NEW_ADDR, not aws_iam_role.count_test:1"
    CT0_AFTER_UP="$(role_id "$CT0")"
    [ "$CT0_AFTER_UP" = "$CT0_ID" ] || fail "count_test[0]'s live RoleId changed across the scale-up ($CT0_ID -> $CT0_AFTER_UP)"
    CT0_ADDR_AFTER_UP="$(role_addr "$CT0")"
    [ "$CT0_ADDR_AFTER_UP" = 'aws_iam_role.count_test:0' ] || fail "count_test[0]'s tofu-address marker changed across the scale-up: $CT0_ADDR_AFTER_UP"
    log "  count_test[1] recreated under a NEW RoleId ($CT1_NEW_ID, was $CT1_ID), tofu-address=$CT1_NEW_ADDR; count_test[0] kept RoleId=$CT0_ID and its marker throughout the down-then-up cycle - all read via the AWS CLI"

    log "=== G4. one more plan: config and reality agree, nothing left to propose ==="
    CT_FINAL_PLAN_OUT="$(cd "$EST" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; CT_FINAL_PLAN_RC=$?
    [ "$CT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $CT_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$CT_FINAL_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$CT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
    log "  no resource change proposed. The scale-down-then-up cycle is complete and invisible to the next plan."

    gauntlet_stage day2_count pass "synthetic count block, and the header says why: .corpus/lambda declares 38 count/for_each expressions and not one is scalable - 36 are boolean create toggles (count = ... ? 1 : 0), the for_each maps are empty in this example, and the only two numeric ones (number_of_policies, number_of_policy_jsons) are gated off by default and drive the untaggable aws_iam_role_policy_attachment, so turning either on would both change the estate every earlier stage measured and leave no marker to read back. So aws_iam_role.count_test (count = 2), a self-contained block of a type this estate already exercises, added after Part E's completed removal. choudoufu: scaling 2 -> 1 proposed exactly 0 add, 0 change, 1 destroy, destroying count_test[1] and never naming count_test[0]; after the apply $CT1 no longer answers GetRole at all (verified absence) while count_test[0] kept its server-minted RoleId $CT0_ID and its tofu-address=aws_iam_role.count_test:0 marker, both read through the AWS CLI. Scaling 1 -> 2 proposed exactly 1 add, 0 change, 0 destroy and count_test[1] came back as a genuinely NEW object - RoleId $CT1_ID -> $CT1_NEW_ID under the same deterministic name, the witness an IAM role's ARN cannot provide since it is rebuilt from account plus name (the same reason this estate's own Lambda function ARN could not have witnessed it) - carrying tofu-address=aws_iam_role.count_test:1, with count_test[0] untouched throughout; the next plan is empty. Stock oracle (G0): the identical block stood up for real with plain terraform in its own working directory on the idle greenfield-oracle endpoint shows the identical shape - $ORACLE_SHAPE. BREAK_COUNT=1 asserts the WRONG instance (count_test[0]) was destroyed and reports this stage fail, so the assertion is load-bearing"
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end
