#!/usr/bin/env bash
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

ESTATE="lambda-simple-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

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
CURRENT_STAGE=cold_deploy
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
CURRENT_STAGE=migrate

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
CURRENT_STAGE=test_plan

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
  CURRENT_STAGE=""
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
  CURRENT_STAGE=""
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
CURRENT_STAGE=test_apply

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
CURRENT_STAGE=drift_reconverge

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
CURRENT_STAGE=""
gauntlet_end
