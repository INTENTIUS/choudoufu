#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-sqs-basic recipe; run with: just demo-run corpus-sqs-basic)
# The five-stage real-estate crossing pipeline (cold deploy, migrate, test
# plan, test apply, drift and reconverge - live/corpus-crossing-manifest.json)
# for .corpus/sqs/examples/complete, terraform-aws-modules/terraform-aws-sqs's
# own showcase example, reduced to its four self-contained module calls (the
# script's header states the trim and grep-verifies it, so a moved pin fails
# loudly). SQS queueing is a service surface no other crossing here reaches.
# All five stages pass for real. What makes this estate worth its slot is the
# two resources hanging off the FIFO queue: aws_sqs_queue_redrive_policy and
# aws_sqs_queue_redrive_allow_policy are untaggable, have no row in the
# generated identity table, and resolve entirely through the provider's own
# identity schema (queue_url) - and they resolve to two DIFFERENT queues, the
# source and the DLQ, which stage 2 asserts by value rather than by "did it
# error". Four tagged queues plus two derived from tagged parents: the
# invariant, with nothing in a third bucket. BREAK takes three values -
# schema, identity, drift - each corrupting a different assertion and each
# required to exit non-zero at its own stage. Needs Docker, the AWS CLI, the
# real terraform binary and a populated .corpus; runs on its own port (4690)
# so it can run beside `just demo`.
set -uo pipefail

# STATUS (2026-08-20): VERIFIED. All five stages were run for real against
# floci ghcr.io/lex00/floci@sha256:8a882bcc (live/floci-image's pin) and every
# count, queue URL and tofu-address string below is now read off that run's
# output rather than derived from the module source.
#
# STATUS (2026-08-31, issue #643): day2_count added (PART G, and its own real
# stock oracle at G-ORACLE) and run for real against floci
# ghcr.io/lex00/floci@sha256:c55d74e1, the current live/floci-image pin. All
# ten stages this script reports came back pass in that run - cold_deploy,
# greenfield, migrate, test_plan, test_apply, drift_reconverge, day2_rename,
# day2_replace, day2_remove, day2_count - and every timestamp and marker
# string quoted in PART G's comments below is read off that run rather than
# assumed. BREAK_COUNT=1 was then run end to end on the same emulator and
# took the stage red - "GAUNTLET stage=day2_count verdict=fail ... detail=
# choudoufu's scale-down plan does not destroy count_test[0]", exit 1, with
# every earlier stage still passing - so the scale-down assertion is
# load-bearing rather than a grep that always matches.
#
# WHAT THE FIRST REAL RUN CORRECTED. The version committed on 2026-08-20 had
# every assertion past stage 1's `terraform apply` DERIVED from reading
# terraform-aws-sqs's naming locals. Three of those derivations were right and
# one was wrong:
#   RIGHT  all four queue names and URLs, the FIFO DLQ's "-dlq.fifo" suffix
#          included, and all four rendered tofu-address strings.
#   WRONG  the resource count. The estate creates SIX managed resources, not
#          five: `create_dlq = true` makes the module emit an
#          aws_sqs_queue_redrive_ALLOW_policy on the DLQ alongside the
#          aws_sqs_queue_redrive_policy on the source queue. Reading the
#          naming locals found the second resource and missed the first.
#          Every count in this script was corrected from that run: 6 added at
#          stage 1, "4 of 6 eligible" at stage 2 (the two redrive resources
#          are untaggable), 4 stamped and 2 skipped.
#
# terraform-aws-modules/terraform-aws-sqs's "complete" example
# (.corpus/sqs/examples/complete, pinned to v5.2.2), crossed through
# choudoufu against floci via the real, five-stage pipeline (cold deploy,
# migrate, test plan, test apply, drift and reconverge) that
# live/corpus-crossing-manifest.json tracks. terraform-aws-sqs is one of the
# most-downloaded terraform-aws-modules repos (52M+ Registry downloads at
# sourcing time, 2026-08-20) and SQS is a new AWS service surface for this
# corpus - queues, not DNS/IAM/S3/VPC/compute.
#
# THE REDUCTION (documented per the sumaform/corpus-sumaform-aws precedent -
# a corpus crossing script may trim a real example down to a materially
# simpler shape, as long as the trim is a real subset of the real file and
# the rationale is stated here). The upstream "complete" example wires up
# SEVEN queue configurations: default, fifo, unencrypted, a CMK-encrypted
# queue depending on the `terraform-aws-modules/kms/aws` registry module, an
# SSE-encrypted queue and a matching standalone DLQ that reference each
# OTHER's queue_arn (a genuine cross-resource Computed-attribute dependency -
# exactly the shape the campaign's own sourcing guidance flags as unlikely
# to reach a clean plan), and a queue-with-inline-DLQ using
# `data.aws_partition`/`data.aws_caller_identity` in a queue policy. This
# script keeps only the four self-contained module calls with no external
# registry module and no queue-to-queue Computed reference: default_sqs
# (one static-named standard queue), fifo_sqs (one FIFO queue, its own DLQ,
# and the redrive policy binding them - the DLQ and redrive policy are
# created BY THE SAME module instance, not wired to a sibling module's
# output), unencrypted_sqs (one static-named queue with SSE disabled), and
# disabled_sqs (create = false; contributes no resources, same shape
# corpus-iam-policy's third module instantiation exercises). The trim drops
# the "kms" supporting module and its "Supporting resources" header, and the
# four dropped modules' matching output blocks in outputs.tf. Nothing here
# was hand-authored: main.tf and outputs.tf are the real upstream files with
# a real subset of their content removed by exact text match (grep-verified
# below, so a moved corpus pin fails loudly rather than silently deploying
# a different shape than this script documents).
#
# THE RESOURCE SHAPE (6 resources, exercising THREE AWS resource types -
# measured off `terraform state list` on the first real run, not derived):
#   aws_sqs_queue x4          default_sqs, fifo_sqs (standard), fifo_sqs's
#                              own DLQ, unencrypted_sqs - all taggable,
#                              server-assigned-if-absent identity
#                              (live/identity/table_generated.go's
#                              aws_sqs_queue row: the queue URL rebuilt from
#                              region + account + name).
#   aws_sqs_queue_redrive_policy x1        on the SOURCE queue (ex-complete
#                              .fifo), naming the DLQ as its target.
#   aws_sqs_queue_redrive_allow_policy x1  on the DLQ (ex-complete-dlq.fifo),
#                              naming the source queue as permitted. The
#                              module emits this one whenever create_dlq is
#                              true; the derived sketch missed it.
#
# Both redrive types are UNTAGGABLE (neither has a tags argument in the
# provider schema) and NEITHER has a row in the generated identity table.
# live/survey-full.json classifies both identically - path "client-named",
# admission "schema", `required_for_import: [queue_url]` - because their one
# required-for-import identity attribute is a required argument, so the
# provider's own identity schema settles them (HANDOFF's "Provider identity
# schemas are plumbed and load-bearing"). This script is a real, live test of
# that schema-fallback path on two popular types this repo had not crossed
# live before, and stage 2 asserts BOTH resolved live ids by value rather
# than trusting that the run merely did not error.
#
# THE INVARIANT THIS ESTATE EXERCISES. "A migrated estate is tagged, plus
# derived-from-tagged. There is no third bucket." Four tagged queues, and two
# redrive resources whose entire identity IS a tagged queue's URL. That is
# why 4 of 6 eligible is the right answer here and not a shortfall: the two
# skipped resources are derivable from parents that carry markers, and stage
# 3's empty plan is what proves the derivation holds with no state file.
#
# THE IMPORT-TIME DRIFT, expected and not a defect. live-import reports the
# two FIFO queues as DRIFTED rather than VERIFIED, on redrive_policy and
# redrive_allow_policy respectively (cold state has "", live has the JSON).
# That is the module's own design: the queue resource does not manage those
# attributes, the separate redrive resources do, so the live object carries a
# value the queue's state row never recorded. DRIFTED is still eligible for
# stamping, the convergence apply reconciles it, and stage 3's plan is empty.
#
# All four aws_sqs_queue resources declare `count = var.create ? 1 : 0`
# (module default), the same shape corpus-iam-policy's "THE TOFU-SLOT
# FINDING" documents - but unlike an IAM policy (ServerAssigned, so gate 4's
# unconditional half already covered it), a queue is client-named and used
# to be the OTHER half: whether it needs discovery is a question about its
# own declaration, which a migration reading a bare state file could not
# ask. GitHub issue #372's remainder (gauntlet/sumaform-record) closed that
# gap by threading Request.Config through to Ratify, so as of this crossing
# live-import -approve writes tofu-slot in the SAME stamp as tofu-estate and
# tofu-address - no separate convergence apply is needed any more. STAGE 2
# below asserts tofu-slot by value right after the stamp, then runs one more
# ordinary apply as a POSITIVE no-op check that nothing was left to converge.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline; see
# live/corpus-crossing-manifest.json):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block anywhere.
#   2. MIGRATE        `choudoufu live-import -approve` against the cold
#                     state (4 of 6 eligible; the two redrive resources are
#                     untaggable and resolve by provider identity schema);
#                     tofu-slot is written on all four count-based queues by
#                     this same stamp (issue #372's remainder), confirmed by
#                     value and by one more apply staying a genuine no-op.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan proposes no resource action and re-assert the
#                     rendered queue identities against the AWS CLI's own
#                     answer.
#   4. TEST APPLY     apply that empty plan; assert a genuine no-op and that
#                     the tofu-estate-tagged object count is unchanged.
#   5. DRIFT AND      mutate one queue's tag out of band via the AWS CLI
#      RECONVERGE     directly against floci, replan, assert the diff
#                     proposes fixing exactly that one object and nothing
#                     else, then apply and confirm it reconverged.
#
# THE ONE ONBOARDING DELTA (stage 1). Same shape as every other
# terraform-aws-modules crossing: the example's own
# `version = ">= 6.28"` resolves straight to a current provider, and the
# only edit needed ahead of a real `terraform apply` against floci is the
# emulator connection flags on the provider block.
#
# WHAT A GENUINE SQS DELETE/RECREATE ACTUALLY CHANGES (established directly
# against floci ghcr.io/lex00/floci@sha256:c55d74e1 before any day2_count
# assertion below was written, with NO tofu in the loop - HANDOFF's
# identity-semantics rule). Created one queue, read every attribute, deleted
# it, re-created it under the SAME name, read them again:
#   SAME     QueueUrl and QueueArn. Both are rebuilt from region + account +
#            name (live/identity/table_generated.go's aws_sqs_queue row says
#            exactly this), so neither can witness a destroy: the recreated
#            queue is reachable at the identical URL the destroyed one had.
#   GONE     in between: get-queue-url returns
#            AWS.SimpleQueueService.NonExistentQueue and list-queues returns
#            an empty list. Verified absence is therefore available as a
#            discriminator, the corpus-simpleinfra-dns shape.
#   CHANGED  CreatedTimestamp (1788159242 -> 1788159243 -> 1788159246 across
#            two recreates), in epoch SECONDS - one-second granularity, so a
#            destroy and a create inside the same wall-clock second would
#            read identical. PART G below sleeps 2s between the scale-down
#            apply and the scale-up plan and then asserts strictly GREATER,
#            not merely different.
#   CHANGED  tags: the recreated queue came back with an empty tag set, none
#            carried over.
# One divergence from documented AWS worth stating rather than relying on:
# real SQS refuses to create a queue with a recently-deleted name for up to
# 60 seconds (DeleteQueue's own documentation); floci accepted an immediate
# recreate. That is permissiveness both legs of this stage see equally -
# stock's oracle recreates through the same emulator - so it changes nothing
# about what is compared here, and PART G's own 2-second gap is well inside
# the window either way. Not filed as a floci defect: nothing in this stage
# depends on the refusal.
#
# THE OUTPUTS QUIRK, same as corpus-iam-policy: this estate declares root
# `output` blocks and live-plan carries no state to diff them against, so
# OpenTofu's renderer never prints a "Plan: N to add..." summary line, empty
# or not - it prints nothing when 0 resources change and a "Changes to
# Outputs" section is non-empty. Every empty-plan assertion below checks for
# the absence of a resource-action header instead of a summary line.
#
#   bash live/e2e/corpus-sqs-basic/run.sh
#
# Needs Docker, the AWS CLI, and the real `terraform` binary on PATH for
# stage 1. .corpus is read, never written: the module (repo root) and the
# example are copied out to a temp directory first, same as every other
# corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4690, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        corrupt one assertion on purpose, to prove it is load-bearing
#                rather than a grep that always matches. Each value corrupts a
#                DIFFERENT assertion, and each must make this script exit
#                non-zero at that assertion:
#                  schema    swap the two expected redrive queue URLs at stage
#                            2, so each untabled type is expected to resolve
#                            to the OTHER queue. Same two types, same two real
#                            URLs, wrong pairing - the one thing a "did it
#                            error?" check cannot catch.
#                  identity  expect the fifo queue's tofu-address to name a
#                            different module at stage 3. Same shape, same
#                            resource type, a module that was never created.
#                  drift     tamper a second, unrelated queue's tag before
#                            stage 5's mutation, so the plan must propose
#                            fixing two objects where the assertion demands
#                            exactly one.
#                  rename    rename module unencrypted_sqs (day2_rename's D1)
#                            WITHOUT a moved block, so the plan must propose
#                            ONLY a create for the renamed address, no
#                            destroy of the old one - a genuinely stateless
#                            live-plan walks the CURRENT config's addresses
#                            alone, so an address no longer declared is
#                            never visited, instead of the zero-churn plan
#                            a moved block or live-mv produces.
#                  greenfield  the greenfield stage's own oracle (below,
#                            right after stage 1): expect one fewer queue
#                            than the greenfield estate actually created, so
#                            the queue-count comparison against the stock
#                            oracle namespace must fail.
#                  remove    day2_remove's own break control (PART E, after
#                            the real rename): keep module.unencrypted_sqs_
#                            renamed's block in the config; the plan below
#                            must propose no destroy for it at all - the
#                            Break text in tools/gauntlet/stages.go,
#                            verbatim.
#                  replace   day2_replace's own break control (PART F, after
#                            the real rename, before the real remove):
#                            manufacture the exact coexistence "skip the
#                            destroy half" describes directly - a second
#                            live queue is created via the AWS CLI, carrying
#                            the SAME tofu-address/tofu-slot as the queue a
#                            genuine replace would have destroyed - and the
#                            next plan must report the collision loudly, not
#                            propose nothing. The Break text in
#                            tools/gauntlet/stages.go, verbatim.
#                  1         alias for `schema`.
#                They are separate values rather than one flag because the
#                first corruption reached exits the script: a single BREAK=1
#                that set all three would leave the later two unreachable, and
#                an unreachable check proves nothing. Run all three.
#   BREAK_APPROVAL
#                plan_approval's own break control (PART P, between STAGE 5
#                and PART D), a SEPARATE variable from BREAK for the same
#                reason BREAK_COUNT below is: after the world has moved out
#                of band, assert the saved plan file APPLIES cleanly - the
#                Break text in tools/gauntlet/stages.go for plan_approval is
#                literally "Apply the planfile after a mutation and expect
#                success; the run must refuse", so this assertion has to
#                fail. PART P runs only when BREAK and BREAK_COUNT are both
#                unset - the other controls deliberately leave the estate
#                somewhere PART P does not describe, and it reports no
#                verdict there.
#   BREAK_COUNT  day2_count's own break control (PART G), a SEPARATE variable
#                from BREAK for the same reason the values above are separate
#                from each other: PART G runs after PART E's real removal, so
#                it is unreachable under BREAK=rename or BREAK=remove. Set it
#                to 1 and the scale-down assertion expects the WRONG instance
#                to have been destroyed - count_test[0], the survivor, rather
#                than count_test[1] - which is the Break text in
#                tools/gauntlet/stages.go for day2_count, verbatim ("Expect a
#                different instance to be destroyed; the assertion must
#                fail"). The plan really does destroy count_test[1], so the
#                by-value assertion below fails and the stage reports
#                verdict=fail. That is the demonstration: not a branch that
#                congratulates itself for the corruption not taking, but the
#                same assertion pointed at the wrong index, going red.
#
# Exit codes: 0 on a real pass of all five stages, non-zero on a real
# failure. Every assertion reads command output, an exit code, or the
# emulator's own answer through the AWS CLI, never choudoufu's own report of
# itself.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_MODULE="$ROOT/.corpus/sqs"
WORK="$(mktemp -d)"
EST="$WORK/sqs/examples/complete"
FLOCI_PORT="${FLOCI_PORT:-4690}"
FLOCI_NAME="choudoufu-corpus-sqs-basic-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# SEPARATE namespace stock applies the identical config into as that stage's
# own oracle. Neither reuses the main container's objects above - greenfield
# means from nothing, and the oracle needs its own independent apply.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-sqs-basic-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-sqs-basic-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"

ESTATE="sqs-basic-crossing"
GREEN_ESTATE="sqs-basic-greenfield"
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
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

# Which single assertion BREAK corrupts (see the header's env-override notes).
BREAK_AT="${BREAK:-}"
[ "$BREAK_AT" = "1" ] && BREAK_AT="schema"
case "$BREAK_AT" in
  ""|schema|identity|drift|rename|greenfield|remove|replace) ;;
  *) fail "BREAK must be one of: schema, identity, drift, rename, greenfield, remove, replace (1 is an alias for schema)" ;;
esac
# day2_count's own break control, a separate variable (see the header). A
# typo'd value must not read as "unset" and silently run the real path, which
# would report a pass for a run the operator asked to corrupt.
case "${BREAK_COUNT:-}" in
  ""|1) ;;
  *) fail "BREAK_COUNT must be unset or 1 (got '${BREAK_COUNT:-}')" ;;
esac

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed to build unmarked reference infra"
[ -d "$SRC_MODULE" ] || fail "$SRC_MODULE is missing - run 'just corpus-fetch' first"
[ -d "$SRC_MODULE/examples/complete" ] || fail "$SRC_MODULE/examples/complete is missing - run 'just corpus-fetch' first"

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

# .corpus is shared across every worktree and is NEVER written to: the whole
# module (repo root, which examples/complete's `source = "../../"` needs) is
# copied out first.
mkdir -p "$WORK/sqs"
cp -R "$SRC_MODULE"/. "$WORK/sqs"
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
log "  module + example copied out of .corpus into $WORK"

# ── 1. the reduction (see header) + the one onboarding delta ───────────────
log "=== 1. the reduction and the one onboarding delta ==="
perl -0777 -pi -e 's/\nmodule "cmk_encrypted_sqs" \{.*?\nmodule "disabled_sqs"/\nmodule "disabled_sqs"/s' "$EST/main.tf"
grep -q 'module "cmk_encrypted_sqs"' "$EST/main.tf" && fail "the module-block reduction did not remove cmk_encrypted_sqs - the corpus pin has moved"
perl -0777 -pi -e 's/\n################################################################################\n# Supporting resources\n################################################################################\n\nmodule "kms" \{.*\z//s' "$EST/main.tf"
grep -q 'module "kms"' "$EST/main.tf" && fail "the module-block reduction did not remove the kms supporting module - the corpus pin has moved"
grep -q 'module "default_sqs"' "$EST/main.tf" || fail "default_sqs did not survive the reduction - the corpus pin has moved"
grep -q 'module "fifo_sqs"' "$EST/main.tf" || fail "fifo_sqs did not survive the reduction - the corpus pin has moved"
grep -q 'module "unencrypted_sqs"' "$EST/main.tf" || fail "unencrypted_sqs did not survive the reduction - the corpus pin has moved"
grep -q 'module "disabled_sqs"' "$EST/main.tf" || fail "disabled_sqs did not survive the reduction - the corpus pin has moved"
log "  DELTA  main.tf reduced to default_sqs/fifo_sqs/unencrypted_sqs/disabled_sqs (see header)"

perl -0777 -pi -e 's/\n# CMK Encrypted\n.*?\n# Disabled/\n# Disabled/s' "$EST/outputs.tf"
grep -q 'module.cmk_encrypted_sqs' "$EST/outputs.tf" && fail "the outputs reduction did not remove the cmk_encrypted_sqs outputs - the corpus pin has moved"
log "  DELTA  outputs.tf reduced to match"

perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                   = "test"\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/main.tf"
grep -q 's3_use_path_style' "$EST/main.tf" || fail "the emulator delta did not match main.tf - the corpus pin has moved"
log "  DELTA  emulator flags added to the provider block; no backend, no version pin, no live block yet"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"sqs"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"sqs"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (sqs) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform, no choudoufu, no live block
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
log "=== STAGE 1: cold deploy (terraform apply, the real reduced example + delta) ==="
# The shared plugin cache, the same way corpus-lambda-simple's and
# corpus-alb-complete's crossings use it. Without it every run re-downloads
# hashicorp/aws (several hundred megabytes) from the registry twice - once for
# terraform, once for choudoufu's own registry.opentofu.org copy - which on a
# machine running more than one crossing at a time takes longer than the rest
# of this script put together and makes the estate look hung. Measured on the
# first real run of this script: 21 minutes into `terraform init` only 48MB of
# the provider had arrived, and the init then died on a transient DNS failure
# before ever reaching `terraform apply`. It changes nothing about what is
# measured; an operator who already exports TF_PLUGIN_CACHE_DIR keeps theirs.
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
  ( cd "$EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$EST" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 6 added' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "the cold apply did not create exactly 6 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

# The exact managed shape, read off terraform's own state rather than off this
# script's reading of the module. A corpus pin that adds or drops a resource
# fails here, by name, instead of silently crossing a different estate - which
# is precisely how the derived version of this script came to expect five
# resources when the module builds six.
# Both sides go through the same LC_ALL=C sort: '.' and '_' collate
# differently under a UTF-8 locale, so comparing a hand-ordered list against a
# locale-sorted one fails on ordering alone even when the shape is identical.
WANT_SHAPE="$(LC_ALL=C sort <<'EOF'
module.default_sqs.aws_sqs_queue.this[0]
module.fifo_sqs.aws_sqs_queue.dlq[0]
module.fifo_sqs.aws_sqs_queue.this[0]
module.fifo_sqs.aws_sqs_queue_redrive_allow_policy.dlq[0]
module.fifo_sqs.aws_sqs_queue_redrive_policy.dlq[0]
module.unencrypted_sqs.aws_sqs_queue.this[0]
EOF
)"
GOT_SHAPE="$(cd "$EST" && terraform state list 2>/dev/null | grep -v '\.data\.\|^data\.' | LC_ALL=C sort)"
[ "$GOT_SHAPE" = "$WANT_SHAPE" ] || {
  printf 'want:\n%s\ngot:\n%s\n' "$WANT_SHAPE" "$GOT_SHAPE"
  fail "the cold estate's managed resource shape is not the six resources this script documents - the corpus pin has moved"; }
log "  managed shape confirmed: 4 aws_sqs_queue + 1 redrive_policy + 1 redrive_allow_policy"

# local.name = "ex-${basename(path.cwd)}" - path.cwd is $EST, whose basename
# is "complete" (the upstream directory name, preserved by the copy above).
BASENAME="$(basename "$EST")"
NAME="ex-${BASENAME}"
DEFAULT_QUEUE_NAME="${NAME}-default"
FIFO_QUEUE_NAME="${NAME}.fifo"
FIFO_DLQ_NAME="${NAME}-dlq.fifo"
UNENCRYPTED_QUEUE_NAME="${NAME}-unencrypted"

DEFAULT_QUEUE_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${DEFAULT_QUEUE_NAME}"
FIFO_QUEUE_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${FIFO_QUEUE_NAME}"
FIFO_DLQ_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${FIFO_DLQ_NAME}"
UNENCRYPTED_QUEUE_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${UNENCRYPTED_QUEUE_NAME}"

GOT_DEFAULT_URL="$(awsl sqs get-queue-url --queue-name "$DEFAULT_QUEUE_NAME" --query QueueUrl --output text 2>/dev/null || true)"
[ "$GOT_DEFAULT_URL" = "$DEFAULT_QUEUE_URL" ] || fail "default queue not found at the expected URL: got '$GOT_DEFAULT_URL', want '$DEFAULT_QUEUE_URL'"
GOT_FIFO_URL="$(awsl sqs get-queue-url --queue-name "$FIFO_QUEUE_NAME" --query QueueUrl --output text 2>/dev/null || true)"
[ "$GOT_FIFO_URL" = "$FIFO_QUEUE_URL" ] || fail "fifo queue not found at the expected URL: got '$GOT_FIFO_URL', want '$FIFO_QUEUE_URL'"
GOT_FIFO_DLQ_URL="$(awsl sqs get-queue-url --queue-name "$FIFO_DLQ_NAME" --query QueueUrl --output text 2>/dev/null || true)"
[ "$GOT_FIFO_DLQ_URL" = "$FIFO_DLQ_URL" ] || fail "fifo DLQ not found at the expected URL: got '$GOT_FIFO_DLQ_URL', want '$FIFO_DLQ_URL'"
GOT_UNENCRYPTED_URL="$(awsl sqs get-queue-url --queue-name "$UNENCRYPTED_QUEUE_NAME" --query QueueUrl --output text 2>/dev/null || true)"
[ "$GOT_UNENCRYPTED_URL" = "$UNENCRYPTED_QUEUE_URL" ] || fail "unencrypted queue not found at the expected URL: got '$GOT_UNENCRYPTED_URL', want '$UNENCRYPTED_QUEUE_URL'"
log "  all four queues live at their expected URLs"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain terraform's own objects already carry tofu-estate=$ESTATE before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE before migration"

cp "$EST/terraform.tfstate" "$WORK/cold.tfstate"

log ""
gauntlet_stage cold_deploy pass "6 resources added by plain terraform (4 queues + redrive_policy + redrive_allow_policy), 0 objects carry tofu-estate before migration"
log "STAGE 1 (cold deploy): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13, planned stage - this
# crossing wires the evidence for it)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: greenfield means from
# nothing, so this never touches the objects stage 1's plain terraform apply
# created (those get migrated in STAGE 2, below). choudoufu applies the
# identical reduced example directly, with a live block from the start, no
# migration, no state file ever existing; the record store must hold one
# record per instance, including the two untaggable redrive types (#364 A2
# - apply writes a record too, not just live-import); and the estate's own
# oracle is stock applying the SAME config fresh in a THIRD, independent
# namespace, compared structurally via the AWS CLI on both endpoints, never
# through tofu state.
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
    grep -q '"sqs"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"sqs"' <<< "${GH:-}" || fail "floci did not come up healthy (sqs) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

# Copy the WHOLE module tree, not just the example directory, preserving the
# same nesting depth - a shallow copy silently breaks every module's
# "../../" relative source path (see the day2_rename stock oracle's own
# comment on this, below, for the failure mode this avoids).
cp -R "$WORK/sqs" "$WORK/sqs-greenfield"
rm -rf "$WORK/sqs-greenfield/examples/complete/.terraform" \
       "$WORK/sqs-greenfield/examples/complete/terraform.tfstate" \
       "$WORK/sqs-greenfield/examples/complete/terraform.tfstate.backup" \
       "$WORK/sqs-greenfield/examples/complete/.terraform.lock.hcl"
GREEN_EST="$WORK/sqs-greenfield/examples/complete"
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$GREEN_EST/versions.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/versions.tf" || fail "the greenfield live-block delta did not match versions.tf - the corpus pin has moved"

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 6 added' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 6 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_DEFAULT_ADDR="$(awsg sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GREEN_DEFAULT_ADDR" = "module.default_sqs.aws_sqs_queue.this:0" ] || fail "the greenfield default queue carries tofu-address=$GREEN_DEFAULT_ADDR, not module.default_sqs.aws_sqs_queue.this:0"
GREEN_FIFO_ADDR="$(awsg sqs list-queue-tags --queue-url "$FIFO_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GREEN_FIFO_ADDR" = "module.fifo_sqs.aws_sqs_queue.this:0" ] || fail "the greenfield fifo queue carries tofu-address=$GREEN_FIFO_ADDR, not module.fifo_sqs.aws_sqs_queue.this:0"
GREEN_FIFO_DLQ_ADDR="$(awsg sqs list-queue-tags --queue-url "$FIFO_DLQ_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GREEN_FIFO_DLQ_ADDR" = "module.fifo_sqs.aws_sqs_queue.dlq:0" ] || fail "the greenfield fifo dlq carries tofu-address=$GREEN_FIFO_DLQ_ADDR, not module.fifo_sqs.aws_sqs_queue.dlq:0"
GREEN_UNENCRYPTED_ADDR="$(awsg sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GREEN_UNENCRYPTED_ADDR" = "module.unencrypted_sqs.aws_sqs_queue.this:0" ] || fail "the greenfield unencrypted queue carries tofu-address=$GREEN_UNENCRYPTED_ADDR, not module.unencrypted_sqs.aws_sqs_queue.this:0"
GREEN_ESTATE_TAG="$(awsg sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-estate\"" --output text)"
[ "$GREEN_ESTATE_TAG" = "$GREEN_ESTATE" ] || fail "the greenfield default queue carries tofu-estate=$GREEN_ESTATE_TAG, not $GREEN_ESTATE"
log "  all four queues carry their expected tofu-address markers and tofu-estate=$GREEN_ESTATE_TAG - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds every instance, including the two untaggable redrive types (#364 A2) ==="
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN_EST/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" = "6" ] || fail "expected 6 records under the local record store after the greenfield apply (4 tagged queues + 2 untaggable redrive types), found $GREEN_RECORD_FILES"
log "  6 records persisted, one per managed instance, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN_EST" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD: 5. stock oracle - the identical config applied fresh in its own namespace ==="
cp -R "$WORK/sqs" "$WORK/sqs-greenfield-oracle"
rm -rf "$WORK/sqs-greenfield-oracle/examples/complete/.terraform"
ORACLE_EST="$WORK/sqs-greenfield-oracle/examples/complete"
( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$ORACLE_EST" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 6 added' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 6 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

queue_shape() { # $1=endpoint $2=queue-url - a normalised structural fact
                 # sheet, read via the AWS CLI, never through tofu state.
  aws --endpoint-url "$1" --region "$REGION" sqs get-queue-attributes \
    --queue-url "$2" \
    --attribute-names FifoQueue ContentBasedDeduplication KmsMasterKeyId SqsManagedSseEnabled RedrivePolicy RedriveAllowPolicy \
    --output json 2>/dev/null \
  | jq -S '(.Attributes // {}) as $a | {
      FifoQueue:         ($a.FifoQueue // "false"),
      Sse:               ($a.SqsManagedSseEnabled // "false"),
      Kms:               ($a | has("KmsMasterKeyId")),
      RedriveMaxReceive:  (if ($a | has("RedrivePolicy")) then ($a.RedrivePolicy | fromjson | .maxReceiveCount) else null end),
      RedriveAllow:      ($a | has("RedriveAllowPolicy"))
    }'
}

log "=== PART GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
QUEUE_COUNT_GREEN="$(awsg sqs list-queues --query 'length(QueueUrls)' --output text)"
EXPECTED_QUEUES="default:$DEFAULT_QUEUE_URL fifo:$FIFO_QUEUE_URL dlq:$FIFO_DLQ_URL unencrypted:$UNENCRYPTED_QUEUE_URL"
if [ "$BREAK_AT" = "greenfield" ]; then
  EXPECTED_QUEUES="default:$DEFAULT_QUEUE_URL fifo:$FIFO_QUEUE_URL unencrypted:$UNENCRYPTED_QUEUE_URL"
  log "  BREAK=greenfield: dropped the dlq queue from the expected inventory - the count comparison below must fail"
fi
N_EXPECTED="$(wc -w <<< "$EXPECTED_QUEUES" | tr -d ' ')"
[ "$QUEUE_COUNT_GREEN" = "$N_EXPECTED" ] || fail "the greenfield estate has $QUEUE_COUNT_GREEN queues, expected $N_EXPECTED"

for pair in $EXPECTED_QUEUES; do
  label="${pair%%:*}"; url="${pair#*:}"
  G="$(queue_shape "$GREEN_ENDPOINT" "$url")"
  O="$(queue_shape "$ORACLE_ENDPOINT" "$url")"
  [ "$G" = "$O" ] || { printf 'greenfield %s: %s\noracle    %s: %s\n' "$label" "$G" "$label" "$O"; fail "the $label queue differs structurally between the greenfield estate and the stock oracle"; }
done
log "  all $N_EXPECTED queues match structurally (fifo flag, sse, kms, redrive max-receive-count, redrive-allow presence) between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "6 resources from nothing (4 tagged queues + 2 untaggable redrive types), all markers verified via the AWS CLI, 6 records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally on all 4 queues"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock oracle (day2_count, live/GAUNTLET.md #8,
# issue #643)
# ══════════════════════════════════════════════════════════════════════════
#
# WHY A SYNTHETIC BLOCK. All four of this estate's module calls declare
# `count = var.create ? 1 : 0` (the terraform-aws-sqs module default, see the
# header's TOFU-SLOT FINDING), which is a boolean create toggle, not a knob
# that scales: it can only ever be 0 or 1, so it can never carry the two
# instances this stage needs, and turning it off is day2_remove's shape
# rather than day2_count's. Neither the example nor the module offers a
# numeric count or a for_each anywhere else. This is issue #488's sanctioned
# synthetic-count fallback, following live/e2e/reference-ec2-vpc/run.sh's
# PART F and corpus-hongbomiao-storage's PART G: a NEW, entirely
# self-contained resource, aws_sqs_queue.count_test, of a type this estate
# already exercises four times over, that nothing else in the estate
# references and no other stage's address space ever touches.
#
# WHY THE ORACLE IS APPLIED FOR REAL AND HERE. Stock never had this count
# block, so unlike day2_remove's and day2_replace's oracles there is no
# cold_deploy state to reuse - the shape has to be stood up from nothing with
# the stock binary. It runs in the greenfield stock oracle's own namespace
# ($ORACLE_ENDPOINT), which stock itself just applied into and which is idle
# from here on; "ex-complete-count-test-N" collides with none of the four
# "ex-complete*" queues already in it. It MUST run before the
# `docker rm -f "$FLOCI_ORACLE_NAME"` line below, or the account it needs is
# already gone - the same placement corpus-hongbomiao-storage's own G-ORACLE
# uses and the same reason.
#
# The discriminator for "genuinely a new object" is CreatedTimestamp, and the
# reasoning behind that choice is the header's own WHAT A GENUINE SQS
# DELETE/RECREATE ACTUALLY CHANGES paragraph, measured against floci with no
# tofu in the loop before any of this was written: a queue's URL and ARN are
# both rebuilt from region + account + name, so the recreated queue is
# reachable at the identical URL and neither can witness the destroy.
gauntlet_begin_stage day2_count
count_test_block() { # $1 = count. Byte-for-byte the same block both legs get.
  local n="$1"
  cat <<COUNTEOF
resource "aws_sqs_queue" "count_test" {
  count = $n
  name  = "${NAME}-count-test-\${count.index}"

  tags = {
    Example = "${NAME}"
  }
}
COUNTEOF
}
count_oracle_provider() {
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.28"
    }
  }
}

provider "aws" {
  region = "$REGION"

  access_key                   = "test"
  secret_key                   = "test"
  skip_credentials_validation  = true
  skip_metadata_api_check      = true
  s3_use_path_style            = true
}

EOF
}
# CreatedTimestamp is epoch seconds; a missing queue makes the CLI error, so
# stdout comes back empty rather than stale.
queue_created_ts() { # $1 = endpoint, $2 = queue url
  aws --endpoint-url "$1" --region "$REGION" sqs get-queue-attributes \
    --queue-url "$2" --attribute-names CreatedTimestamp \
    --query 'Attributes.CreatedTimestamp' --output text 2>/dev/null
}

CT0_NAME="${NAME}-count-test-0"
CT1_NAME="${NAME}-count-test-1"
CT0_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${CT0_NAME}"
CT1_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${CT1_NAME}"

log "=== G-ORACLE: stock terraform, a 2-instance count block scaled to 1 and back, in the (idle) greenfield oracle account ==="
COUNT_ORACLE="$WORK/plain-oracle-count"
mkdir -p "$COUNT_ORACLE"
{ count_oracle_provider; count_test_block 2; } > "$COUNT_ORACLE/main.tf"
( cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
CO_ADD_OUT="$(cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$CO_ADD_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$CO_ADD_OUT" \
  || { grep -E 'Apply complete' <<< "$CO_ADD_OUT"; fail "stock did not create exactly 2 count_test queues for the day2_count oracle"; }
CO_CT0_TS="$(queue_created_ts "$ORACLE_ENDPOINT" "$CT0_URL")"
CO_CT1_TS="$(queue_created_ts "$ORACLE_ENDPOINT" "$CT1_URL")"
[ -n "$CO_CT0_TS" ] && [ "$CO_CT0_TS" != "None" ] || fail "the oracle's count_test[0] queue ($CT0_NAME) has no CreatedTimestamp"
[ -n "$CO_CT1_TS" ] && [ "$CO_CT1_TS" != "None" ] || fail "the oracle's count_test[1] queue ($CT1_NAME) has no CreatedTimestamp"
log "  stock: 2 instances created, count_test[0]=$CT0_NAME (created=$CO_CT0_TS), count_test[1]=$CT1_NAME (created=$CO_CT1_TS)"

{ count_oracle_provider; count_test_block 1; } > "$COUNT_ORACLE/main.tf"
CO_DOWN_PLAN="$(cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; CO_DOWN_PLAN_RC=$?
[ "$CO_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CO_DOWN_PLAN" | tail -40; fail "the day2_count stock oracle's scale-down plan exited $CO_DOWN_PLAN_RC"; }
grep -qE '^  # aws_sqs_queue\.count_test\[1\] will be destroyed' <<< "$CO_DOWN_PLAN" \
  || { printf '%s\n' "$CO_DOWN_PLAN" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_sqs_queue\.count_test\[0\] will be' <<< "$CO_DOWN_PLAN" \
  && { printf '%s\n' "$CO_DOWN_PLAN" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$CO_DOWN_PLAN" \
  || { printf '%s\n' "$CO_DOWN_PLAN" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
CO_DOWN_APPLY="$(cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$CO_DOWN_APPLY" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$CO_DOWN_APPLY" \
  || { grep -E 'Apply complete' <<< "$CO_DOWN_APPLY"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
if CO_CT1_STILL="$(aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" sqs get-queue-url --queue-name "$CT1_NAME" 2>&1)"; then
  echo "$CO_CT1_STILL"; fail "stock's count_test[1] queue ($CT1_NAME) still exists after the scale-down destroy"
fi
CO_CT0_TS_DOWN="$(queue_created_ts "$ORACLE_ENDPOINT" "$CT0_URL")"
[ "$CO_CT0_TS_DOWN" = "$CO_CT0_TS" ] || fail "stock's surviving count_test[0] changed CreatedTimestamp across the scale-down ($CO_CT0_TS -> $CO_CT0_TS_DOWN)"
log "  stock: exactly one destroy (count_test[1]=$CT1_NAME, now NonExistentQueue), count_test[0] created=$CO_CT0_TS unchanged"

# CreatedTimestamp has one-second granularity (header), so the gap between
# the destroy and the recreate has to be wider than the resolution of the
# thing being compared, or a true recreate could read as no change at all.
sleep 2
{ count_oracle_provider; count_test_block 2; } > "$COUNT_ORACLE/main.tf"
CO_UP_PLAN="$(cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; CO_UP_PLAN_RC=$?
[ "$CO_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$CO_UP_PLAN" | tail -40; fail "the day2_count stock oracle's scale-up plan exited $CO_UP_PLAN_RC"; }
grep -qE '^  # aws_sqs_queue\.count_test\[1\] will be created' <<< "$CO_UP_PLAN" \
  || { printf '%s\n' "$CO_UP_PLAN" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_sqs_queue\.count_test\[0\] will be' <<< "$CO_UP_PLAN" \
  && { printf '%s\n' "$CO_UP_PLAN" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$CO_UP_PLAN" \
  || { printf '%s\n' "$CO_UP_PLAN" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
CO_UP_APPLY="$(cd "$COUNT_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$CO_UP_APPLY" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$CO_UP_APPLY" \
  || { grep -E 'Apply complete' <<< "$CO_UP_APPLY"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
CO_CT1_TS_UP="$(queue_created_ts "$ORACLE_ENDPOINT" "$CT1_URL")"
[ -n "$CO_CT1_TS_UP" ] && [ "$CO_CT1_TS_UP" != "None" ] || fail "no oracle count_test[1] queue found after the scale-up"
[ "$CO_CT1_TS_UP" -gt "$CO_CT1_TS" ] \
  || fail "stock's recreated count_test[1] came back with CreatedTimestamp $CO_CT1_TS_UP, not later than the destroyed queue's $CO_CT1_TS - the destroy was not real"
CO_CT0_TS_UP="$(queue_created_ts "$ORACLE_ENDPOINT" "$CT0_URL")"
[ "$CO_CT0_TS_UP" = "$CO_CT0_TS" ] || fail "stock's count_test[0] changed CreatedTimestamp across the scale-up ($CO_CT0_TS -> $CO_CT0_TS_UP)"
log "  stock: exactly one create (count_test[1], back at the SAME url $CT1_URL - deterministic - but created=$CO_CT1_TS_UP, was $CO_CT1_TS), count_test[0] created=$CO_CT0_TS unchanged throughout"
gauntlet_end_stage

docker rm -f "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two of the estate's four taggable queues, both standalone module calls
# with no cross-references from any sibling (unlike fifo_sqs's DLQ pair):
# a `moved` block renames the WHOLE module call "default_sqs", and
# "choudoufu live-mv" (below, after drift_reconverge) renames "unencrypted_
# sqs" with no moved block at all. The stock oracle (real terraform, the
# same binary stage 1 used) runs the same two renames, through moved
# blocks only, on a copy of $EST right after cold_deploy's own state -
# before choudoufu or live-import ever touch these objects.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE: stock terraform, the same two renames through moved blocks, on cold_deploy's own state ==="
# $EST's module calls use `source = "../../"`, which resolves relative to
# $EST's own path ($WORK/sqs/examples/complete, two levels under $WORK/sqs,
# the copied repo root) - copying $EST alone to a new, shallower directory
# breaks that relative path silently and makes every module's schema
# resolve empty (every argument then reports "not expected here", even on
# modules this rename never touches). Copy the whole $WORK/sqs tree instead,
# preserving the same nesting depth, and also drop .terraform: a copy of
# it keys module addresses by the OLD module-call names, so a re-init
# would only partially refresh the manifest instead of genuinely
# re-resolving it.
cp -R "$WORK/sqs" "$WORK/sqs-oracle"
rm -rf "$WORK/sqs-oracle/examples/complete/.terraform"
PLAIN_ORACLE="$WORK/sqs-oracle/examples/complete"
sed -i.bak 's/module "default_sqs" {/module "default_sqs_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module "unencrypted_sqs" {/module "unencrypted_sqs_renamed" {/' "$PLAIN_ORACLE/main.tf"
sed -i.bak 's/module\.default_sqs\./module.default_sqs_renamed./g' "$PLAIN_ORACLE/outputs.tf"
sed -i.bak 's/module\.unencrypted_sqs\./module.unencrypted_sqs_renamed./g' "$PLAIN_ORACLE/outputs.tf"
rm -f "$PLAIN_ORACLE/main.tf.bak" "$PLAIN_ORACLE/outputs.tf.bak"
cat >> "$PLAIN_ORACLE/main.tf" <<'EOF'

moved {
  from = module.default_sqs
  to   = module.default_sqs_renamed
}

moved {
  from = module.unencrypted_sqs
  to   = module.unencrypted_sqs_renamed
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

# ══════════════════════════════════════════════════════════════════════════
# PART E-ORACLE: REMOVE, stock oracle (day2_remove, live/GAUNTLET.md #7):
# "Stock with the same block removed plans the same destroys." A SEPARATE
# fresh copy of $WORK/sqs, taken here (before PART D below ever mutates
# $EST in place) so this oracle runs on cold_deploy's own state, with
# nothing else about the config touched - same shape as the D-ORACLE copy
# just above and iam-policy's/reference-ec2-vpc's own remove oracles.
# Removes module.unencrypted_sqs's block entirely (the same standalone,
# single-queue module PART E below removes after the real rename), plus
# its six "# Unencrypted" output blocks in outputs.tf - unlike
# iam-policy's oracle, this estate's outputs.tf DOES reference the removed
# module (see THE OUTPUTS QUIRK in the header), so leaving them in place
# would fail re-init with an undefined-module reference, not a clean
# destroy plan.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_remove
log "=== E-ORACLE: stock terraform, delete module.unencrypted_sqs's block on cold_deploy's own state ==="
cp -R "$WORK/sqs" "$WORK/sqs-remove-oracle"
rm -rf "$WORK/sqs-remove-oracle/examples/complete/.terraform"
REMOVE_ORACLE_EST="$WORK/sqs-remove-oracle/examples/complete"
perl -0pi -e 's/module "unencrypted_sqs" \{.*?\n\}\n\n//s' "$REMOVE_ORACLE_EST/main.tf"
grep -q 'module "unencrypted_sqs"' "$REMOVE_ORACLE_EST/main.tf" \
  && fail "removing module.unencrypted_sqs's block from the remove-oracle copy did not match - the corpus pin has moved"
perl -0777 -pi -e 's/\n# Unencrypted\n.*?\n# Disabled/\n# Disabled/s' "$REMOVE_ORACLE_EST/outputs.tf"
grep -q 'module.unencrypted_sqs' "$REMOVE_ORACLE_EST/outputs.tf" \
  && fail "removing module.unencrypted_sqs's outputs from the remove-oracle copy did not match - the corpus pin has moved"
( cd "$REMOVE_ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REMOVE_ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$REMOVE_ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.unencrypted_sqs\.aws_sqs_queue\.this\[0\] will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose destroying module.unencrypted_sqs's queue when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly one destroy"; }
log "  stock: exactly one destroy (module.unencrypted_sqs's queue), nothing else, on the state cold_deploy produced"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock oracle (day2_replace, live/GAUNTLET.md #9):
# "Stock's replace of the same resource leaves the same single object." A
# THIRD separate fresh copy of $WORK/sqs (same nesting-depth requirement as
# D-ORACLE/E-ORACLE above), so this oracle also runs on cold_deploy's own
# state, before any rename or migration ever touches $EST. Changes
# module.default_sqs's `name` argument (a real, upstream-declared
# ForceNew argument on aws_sqs_queue - the provider does not allow
# renaming a live queue in place) to a different literal name, which
# forces stock to replace the SAME declared address rather than propose a
# destroy-and-create pair at two different addresses (that shape is
# day2_rename's own BREAK=rename finding, a genuinely different thing).
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_replace
log "=== F-ORACLE: stock terraform, force-replace module.default_sqs's queue via its ForceNew name argument, on cold_deploy's own state ==="
cp -R "$WORK/sqs" "$WORK/sqs-replace-oracle"
rm -rf "$WORK/sqs-replace-oracle/examples/complete/.terraform"
REPLACE_ORACLE_EST="$WORK/sqs-replace-oracle/examples/complete"
sed -i.bak 's/name = "\${local\.name}-default"/name = "${local.name}-default-v2"/' "$REPLACE_ORACLE_EST/main.tf"
rm -f "$REPLACE_ORACLE_EST/main.tf.bak"
grep -q 'default-v2' "$REPLACE_ORACLE_EST/main.tf" \
  || fail "changing module.default_sqs's name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$REPLACE_ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.default_sqs\.aws_sqs_queue\.this\[0\] must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose replacing module.default_sqs's queue when its name argument changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add and one destroy at the same address"; }
# PLAN ONLY, never applied - same convention as D-ORACLE and E-ORACLE
# above. This oracle's copy shares floci's ACCOUNT with $EST (only the
# working directory and state are separate; there is no per-oracle
# namespace the way PART GREENFIELD's stock oracle gets its own
# container), so actually applying here would destroy and recreate the
# real ex-complete-default queue $EST's own later stages still depend on.
# An earlier version of this section did apply, and a later real run
# caught it immediately: STAGE 2 (migrate) failed with "module.
# default_sqs.aws_sqs_queue.this[0] ... The live system reports that this
# identity no longer exists" - the oracle's own apply had destroyed it out
# from under the estate. The plan's own "-/+ destroy and then create
# replacement" legend and its "must be replaced"/"forces replacement"
# lines are the oracle's proof; no apply is needed to make it load-bearing.
log "  stock: exactly one replace proposed (destroy the old ex-complete-default, create ex-complete-default-v2) at the same declared address, on the state cold_deploy produced - plan only, not applied (see above)"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot (see the header's TOFU-SLOT note)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
log "=== STAGE 2: migrate (choudoufu live-import -approve, then converge) ==="
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"

IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "4 of 6 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly 4 of 6 resources as eligible - the corpus pin or the fix under test has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"

# THE SCHEMA-FALLBACK ASSERTION, the reason this estate was sourced. Neither
# redrive type has a row in live/identity/table_generated.go. Both must still
# resolve a live id, from the provider's own identity schema (queue_url), and
# both must resolve to the RIGHT queue - which is not the same queue for the
# two of them: the redrive policy hangs off the SOURCE queue and the redrive
# allow policy off the DLQ. Asserting the rendered id by value is the whole
# point; a run that merely did not error would pass with them swapped.
WANT_REDRIVE_URL="$FIFO_QUEUE_URL"
WANT_ALLOW_URL="$FIFO_DLQ_URL"
if [ "$BREAK_AT" = "schema" ]; then
  WANT_REDRIVE_URL="$FIFO_DLQ_URL"
  WANT_ALLOW_URL="$FIFO_QUEUE_URL"
  log "  BREAK=schema: the two expected redrive URLs are swapped - both are real"
  log "                queues in this estate and both types really do resolve,"
  log "                so only a by-value check catches the wrong pairing. This"
  log "                step must fail."
fi
REDRIVE_LINE="$(grep -F 'module.fifo_sqs.aws_sqs_queue_redrive_policy.dlq[0]' <<< "$IMPORT_OUT" | grep -F 'live id:' | head -1)"
grep -qF "live id: $WANT_REDRIVE_URL" <<< "$REDRIVE_LINE" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "aws_sqs_queue_redrive_policy did not resolve to $WANT_REDRIVE_URL via the provider identity schema; got: ${REDRIVE_LINE:-<no live id line at all>}"; }
ALLOW_LINE="$(grep -F 'module.fifo_sqs.aws_sqs_queue_redrive_allow_policy.dlq[0]' <<< "$IMPORT_OUT" | grep -F 'live id:' | head -1)"
grep -qF "live id: $WANT_ALLOW_URL" <<< "$ALLOW_LINE" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "aws_sqs_queue_redrive_allow_policy did not resolve to $WANT_ALLOW_URL via the provider identity schema; got: ${ALLOW_LINE:-<no live id line at all>}"; }
log "  schema fallback resolved both untabled types by value:"
log "    aws_sqs_queue_redrive_policy       -> $FIFO_QUEUE_URL (the source queue)"
log "    aws_sqs_queue_redrive_allow_policy -> $FIFO_DLQ_URL (the DLQ)"
log "  dry run: 4 of 6 eligible (2 untaggable, derived from tagged parents); nothing written yet"

APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "4 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 2 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 4 of 6 resources cleanly with 2 skipped"; }
log "  4 stamped, 2 skipped as untaggable"

WANT_DEFAULT_ADDR="module.default_sqs.aws_sqs_queue.this:0"
WANT_FIFO_ADDR="module.fifo_sqs.aws_sqs_queue.this:0"
WANT_FIFO_DLQ_ADDR="module.fifo_sqs.aws_sqs_queue.dlq:0"
WANT_UNENCRYPTED_ADDR="module.unencrypted_sqs.aws_sqs_queue.this:0"
WANT_FIFO_ADDR_BRACKET="module.fifo_sqs.aws_sqs_queue.this[0]"

GOT_DEFAULT_ADDR="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_DEFAULT_ADDR" = "$WANT_DEFAULT_ADDR" ] || fail "$DEFAULT_QUEUE_URL carries tofu-address=$GOT_DEFAULT_ADDR, not $WANT_DEFAULT_ADDR"
GOT_FIFO_ADDR="$(awsl sqs list-queue-tags --queue-url "$FIFO_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_FIFO_ADDR" = "$WANT_FIFO_ADDR" ] || fail "$FIFO_QUEUE_URL carries tofu-address=$GOT_FIFO_ADDR, not $WANT_FIFO_ADDR"
GOT_FIFO_DLQ_ADDR="$(awsl sqs list-queue-tags --queue-url "$FIFO_DLQ_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_FIFO_DLQ_ADDR" = "$WANT_FIFO_DLQ_ADDR" ] || fail "$FIFO_DLQ_URL carries tofu-address=$GOT_FIFO_DLQ_ADDR, not $WANT_FIFO_DLQ_ADDR"
GOT_UNENCRYPTED_ADDR="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_UNENCRYPTED_ADDR" = "$WANT_UNENCRYPTED_ADDR" ] || fail "$UNENCRYPTED_QUEUE_URL carries tofu-address=$GOT_UNENCRYPTED_ADDR, not $WANT_UNENCRYPTED_ADDR"
log "  markers verified directly against SQS, not through choudoufu's own report:"
log "    $DEFAULT_QUEUE_URL -> tofu-address=$GOT_DEFAULT_ADDR"
log "    $FIFO_QUEUE_URL -> tofu-address=$GOT_FIFO_ADDR"
log "    $FIFO_DLQ_URL -> tofu-address=$GOT_FIFO_DLQ_ADDR"
log "    $UNENCRYPTED_QUEUE_URL -> tofu-address=$GOT_UNENCRYPTED_ADDR"

# GitHub issue #372's remainder (this script's header, "THE TOFU-SLOT
# FINDING"): all four aws_sqs_queue resources declare
# count = var.create ? 1 : 0, and a queue's full identity (its URL) carries
# the account id, so it is ClassNeedsDiscovery even though `name` itself is
# a static string - the exact "client-named type whose name happens NOT to
# be statically computable" case internal/live/liveimport/slot.go's gate 4
# names. Before that remainder landed, live-import -approve wrote only
# tofu-estate and tofu-address, and a separate ordinary apply was required
# to converge tofu-slot before a replan was genuinely empty (4 changed,
# below, used to be the assertion). With Request.Config now threaded
# through from the command layer to Ratify (gauntlet/sumaform-record),
# [Ratification.instanceNeedsDiscovery] resolves the same identity a
# stateless replan would and gate 4 admits these four queues at STAMP TIME
# - tofu-slot=0 is written by the same live-import -approve above, asserted
# here BY VALUE and not inferred from the plan staying empty, per HANDOFF's
# safety rule.
GOT_DEFAULT_SLOT="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-slot\"" --output text)"
[ "$GOT_DEFAULT_SLOT" = "0" ] || fail "$DEFAULT_QUEUE_URL carries tofu-slot=$GOT_DEFAULT_SLOT after migrate, want 0 (written at stamp time, issue #372's remainder)"
GOT_FIFO_SLOT="$(awsl sqs list-queue-tags --queue-url "$FIFO_QUEUE_URL" --query "Tags.\"tofu-slot\"" --output text)"
[ "$GOT_FIFO_SLOT" = "0" ] || fail "$FIFO_QUEUE_URL carries tofu-slot=$GOT_FIFO_SLOT after migrate, want 0 (written at stamp time, issue #372's remainder)"
GOT_FIFO_DLQ_SLOT="$(awsl sqs list-queue-tags --queue-url "$FIFO_DLQ_URL" --query "Tags.\"tofu-slot\"" --output text)"
[ "$GOT_FIFO_DLQ_SLOT" = "0" ] || fail "$FIFO_DLQ_URL carries tofu-slot=$GOT_FIFO_DLQ_SLOT after migrate, want 0 (written at stamp time, issue #372's remainder)"
GOT_UNENCRYPTED_SLOT="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query "Tags.\"tofu-slot\"" --output text)"
[ "$GOT_UNENCRYPTED_SLOT" = "0" ] || fail "$UNENCRYPTED_QUEUE_URL carries tofu-slot=$GOT_UNENCRYPTED_SLOT after migrate, want 0 (written at stamp time, issue #372's remainder)"
log "  tofu-slot=0 already on all four queues right after migrate (issue #372's"
log "  remainder: a client-named type whose full identity still needs discovery"
log "  no longer needs a separate convergence apply)"

# The follow-up ordinary apply is now a genuine, empty no-op: nothing is
# left to converge, because tofu-slot was written by the stamp above rather
# than by this apply. Kept as a positive assertion (0 added, 0 changed, 0
# destroyed) rather than removed outright, so a future regression that made
# THIS apply start proposing changes again - the pre-#372-remainder shape -
# still fails loudly here.
CONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the post-migrate apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$CONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$CONVERGE_OUT"; fail "the post-migrate apply was not a genuine no-op - tofu-slot should already be converged from the stamp above"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (genuine no-op, confirming nothing was left to converge)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the post-migrate apply wrote a state file"

log ""
gauntlet_stage migrate pass "4 of 6 eligible (2 untaggable redrive types resolved by provider identity schema), 4 stamped, 0 failed, 2 skipped; tofu-slot=0 written on all 4 queues by the stamp itself (issue #372's remainder), confirmed by value and by a genuine no-op on the follow-up apply"
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
log "=== STAGE 3: test plan (live-plan empty, identities re-checked) ==="
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists ahead of stage 3"

plan_into() { ( cd "$EST" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT_FIFO_ADDR2="$WANT_FIFO_ADDR"
if [ "$BREAK_AT" = "identity" ]; then
  WANT_FIFO_ADDR2="module.fifo_sqs_disabled.aws_sqs_queue.this:0"
  log "  BREAK=identity: expecting tofu-address=$WANT_FIFO_ADDR2 on the fifo queue -"
  log "           the SAME shape and the SAME resource type, just the wrong"
  log "           (and in fact never-created) module. This step must fail."
fi
GOT_FIFO_ADDR2="$(awsl sqs list-queue-tags --queue-url "$FIFO_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_FIFO_ADDR2" = "$WANT_FIFO_ADDR2" ] || fail "$FIFO_QUEUE_URL's tofu-address is $GOT_FIFO_ADDR2, not $WANT_FIFO_ADDR2"
GOT_DEFAULT_ADDR2="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_DEFAULT_ADDR2" = "$WANT_DEFAULT_ADDR" ] || fail "$DEFAULT_QUEUE_URL's tofu-address changed across the empty plan: $WANT_DEFAULT_ADDR -> $GOT_DEFAULT_ADDR2"
log "  identity re-check (read via the AWS CLI, after the state file has never existed this run): both unchanged"

log ""
gauntlet_stage test_plan pass "no resource change proposed, no foreign resources; fifo and default queue tofu-address re-checked against SQS"
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); $BEFORE_N objects before, $AFTER_N after, no state file"
log "STAGE 4 (test apply): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one object out of band) ==="
if [ "$BREAK_AT" = "drift" ]; then
  awsl sqs tag-queue --queue-url "$UNENCRYPTED_QUEUE_URL" --tags Example=tampered-by-BREAK
  log "  BREAK=drift: also tampered $UNENCRYPTED_QUEUE_URL's Example tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl sqs tag-queue --queue-url "$DEFAULT_QUEUE_URL" --tags Example=tampered-out-of-band
DRIFTED_VALUE="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query 'Tags.Example' --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $DEFAULT_QUEUE_URL's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
# No BREAK special-case here on purpose. Under BREAK=drift two objects really
# have been tampered, so this ordinary assertion is the one that must fail -
# that IS the demonstration that it is load-bearing. An inverted branch that
# accepted "more than one, so skip the check" would exit 0 on a corrupted run
# and prove nothing.
[ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
printf '%s\n' "$CHANGED_ADDRS" | grep -qF "$WANT_FIFO_ADDR_BRACKET" && fail "the plan proposes changing $WANT_FIFO_ADDR_BRACKET, which was never touched"
log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

RECONVERGE_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
[ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -40; fail "the reconverge apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
  || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
FIXED_VALUE="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query 'Tags.Example' --output text)"
[ "$FIXED_VALUE" = "$NAME" ] || fail "$DEFAULT_QUEUE_URL's Example tag is \"$FIXED_VALUE\" after reconverging, not \"$NAME\""
log "  reconverged: $DEFAULT_QUEUE_URL's Example tag is back to \"$NAME\""

log ""
gauntlet_stage drift_reconverge pass "one object tampered, exactly 1 object proposed and applied (0 added, 1 changed, 0 destroyed), tag reconverged to \"$NAME\""
log "STAGE 5 (drift and reconverge): PASS"
log ""

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
#   P2/P3  the world MOVES between the approval and the apply - the default
#          queue's Example tag is changed out of band through the AWS CLI,
#          the same mutation STAGE 5 above already proves this estate's plan
#          notices - and the apply must refuse: exit 3, the named summary,
#          the unapproved row printed by address AND by the live queue URL
#          it was computed against, and the reviewed change still not landed
#          when the unencrypted queue's tags are read back through the CLI.
#   P4     nothing has moved (the tag is put back first) and the SAME file
#          must APPLY. This is the inverted control that
#          live/smoke/scenarios/apply-what-was-approved.sh reasons out: a
#          comparison which refuses unconditionally is not a check, so P3's
#          refusal is only worth something if the identical artifact goes
#          through when the world is where the approval left it.
#
# The two instances are deliberately disjoint, and on two different live
# queues - the change under review is module.unencrypted_sqs's own `tags`
# argument, the out-of-band move is module.default_sqs.aws_sqs_queue.this[0]
# - so the refusal has an EXTRA row to name, about a different object,
# rather than a values-only disagreement about the same row.
#
# WHY unencrypted_sqs, against issue #903's own two traps: `tags` is
# in-place on aws_sqs_queue (`name` is the ForceNew one, which is what
# PART F below uses to force a replace), and this module call declares no
# DLQ, so the module threads var.tags to exactly one resource instance -
# its aws_sqs_queue.this[0] (.corpus/sqs/main.tf line 50; line 189's
# merge(var.tags, var.dlq_tags) belongs to the dlq resource fifo_sqs has
# and this call does not). PART D renames this module call later and PART E
# removes it, so P5 reverts the edit, re-applies and replans empty before
# either starts, and the edit never moves a live id: an SQS queue's
# identity IS its URL, which a tag write does not touch, asserted below.
#
# Runs only on a real run. Under any of this script's BREAK controls the
# estate is deliberately left somewhere this part does not describe, so it
# reports no verdict at all and the runner records the stage as not_run,
# never as a pass.
if [ -z "$BREAK_AT" ] && [ -z "${BREAK_COUNT:-}" ]; then
  gauntlet_begin_stage plan_approval
  log "=== PART P: plan, review, apply (the approval gate, live/GAUNTLET.md #12) ==="

  P_REVIEWED_ADDR="module.unencrypted_sqs.aws_sqs_queue.this[0]"
  P_MOVED_ADDR="module.default_sqs.aws_sqs_queue.this[0]"
  [ "$UNENCRYPTED_QUEUE_URL" != "$DEFAULT_QUEUE_URL" ] \
    || fail "the reviewed queue and the moved queue are the same object; this leg would prove nothing"
  log "  reviewed object $UNENCRYPTED_QUEUE_URL ($P_REVIEWED_ADDR), moved object $DEFAULT_QUEUE_URL ($P_MOVED_ADDR)"

  log "=== P1. the change under review: one argument, on one queue ==="
  # Three module calls survive the reduction and all three pass the same
  # `tags = local.tags`; this substitution is anchored inside module
  # "unencrypted_sqs"'s own block, and the counts on both sides prove it
  # reached exactly one of them.
  [ "$(grep -c '^  tags = local\.tags$' "$EST/main.tf")" = "3" ] \
    || fail "main.tf no longer carries exactly three \"tags = local.tags\" module arguments - the corpus pin has moved"
  perl -0777 -pi -e 's/(module "unencrypted_sqs" \{.*?\n)  tags = local\.tags\n/$1  tags = merge(local.tags, { Reviewed = "yes" })\n/s' "$EST/main.tf"
  [ "$(grep -c 'Reviewed = "yes"' "$EST/main.tf")" = "1" ] \
    || fail "the reviewed edit did not write exactly one merge(local.tags, ...) argument"
  [ "$(grep -c '^  tags = local\.tags$' "$EST/main.tf")" = "2" ] \
    || fail "the reviewed edit changed more than one of the three \"tags = local.tags\" module arguments"
  grep -qF 'Reviewed = "yes"' <<< "$(perl -0777 -ne 'print $1 if /(module "unencrypted_sqs" \{.*?\n\})/s' "$EST/main.tf")" \
    || fail "the reviewed edit landed outside module \"unencrypted_sqs\"'s own block"
  log "  edited one argument: module \"unencrypted_sqs\"'s tags now merge in Reviewed = \"yes\""

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
  awsl sqs tag-queue --queue-url "$DEFAULT_QUEUE_URL" --tags Example=moved-after-approval \
    || fail "the out-of-band move (tag-queue on the default queue) failed"
  P_MOVED_VALUE="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query 'Tags.Example' --output text)"
  [ "$P_MOVED_VALUE" = "moved-after-approval" ] || fail "the out-of-band move did not take: the default queue's Example tag reads \"$P_MOVED_VALUE\""
  log "  $DEFAULT_QUEUE_URL's Example tag changed out of band to \"moved-after-approval\" - after the approval, before the apply, through the AWS CLI"

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
  # printed above it also names the moved queue, so asserting over the whole
  # output would pass on a refusal that named nothing at all.
  P_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$P_GATE_OUT")"
  grep -qF "This apply would do, and the approved plan does not include:" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not classify the difference as a change nobody approved"; }
  grep -qF "$P_MOVED_ADDR" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not name $P_MOVED_ADDR, the change nobody approved"; }
  # The row is "<address>  <action>  <identity>", and an aws_sqs_queue's
  # identity IS its queue URL, so this asserts the live object the
  # unapproved change was computed against and not just its address.
  P_MOVED_ROW="$(grep -F "$P_MOVED_ADDR" <<< "$P_REFUSAL" | head -1)"
  grep -qF "$DEFAULT_QUEUE_URL" <<< "$P_MOVED_ROW" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal's row for $P_MOVED_ADDR (\"$P_MOVED_ROW\") does not carry $DEFAULT_QUEUE_URL, the live object the change was computed against"; }
  grep -qF "Exit status 3" <<< "$P_REFUSAL" \
    || { printf '%s\n' "$P_REFUSAL"; fail "the refusal does not tell a pipeline what its exit status means"; }
  if grep -q "Apply complete!" <<< "$P_GATE_OUT"; then
    printf '%s\n' "$P_GATE_OUT" | tail -20; fail "the apply ran anyway after refusing"
  fi
  # Not "no Apply complete line" alone: read the live object the approval
  # was about and confirm the reviewed change did not land.
  P_REVIEWED_TAG="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query 'Tags.Reviewed' --output text)"
  [ "$P_REVIEWED_TAG" = "None" ] || [ -z "$P_REVIEWED_TAG" ] \
    || fail "the refused apply still wrote the reviewed change: $UNENCRYPTED_QUEUE_URL carries Reviewed=\"$P_REVIEWED_TAG\""
  printf '%s\n' "$P_REFUSAL" | head -12
  log "  refused by name, exit $P_GATE_RC, nothing applied - and the row it names is exactly the change that appeared after the approval"

  log "=== P4. the inverted control: put the world back, apply the SAME file ==="
  awsl sqs tag-queue --queue-url "$DEFAULT_QUEUE_URL" --tags "Example=$NAME" \
    || fail "undoing the out-of-band move failed"
  P_RESTORED="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query 'Tags.Example' --output text)"
  [ "$P_RESTORED" = "$NAME" ] || fail "the out-of-band move was not undone: the default queue's Example tag reads \"$P_RESTORED\""
  P_OK_RC=0
  P_OK_OUT="$(cd "$EST" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || P_OK_RC=$?
  [ "$P_OK_RC" = "0" ] \
    || { printf '%s\n' "$P_OK_OUT" | tail -40; fail "the same plan file was refused (exit $P_OK_RC) over a world that had not moved - a comparison that refuses unconditionally is not a check"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$P_OK_OUT" \
    || { grep -E 'Apply complete' <<< "$P_OK_OUT"; fail "the approved apply did not change exactly the one reviewed resource"; }
  P_LANDED="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query 'Tags.Reviewed' --output text)"
  [ "$P_LANDED" = "yes" ] \
    || fail "the approved change did not land: $UNENCRYPTED_QUEUE_URL carries Reviewed=\"$P_LANDED\", want \"yes\""
  log "  the identical artifact applied (0 added, 1 changed, 0 destroyed) and $UNENCRYPTED_QUEUE_URL now carries Reviewed=yes, read via the AWS CLI"

  log "=== P5. put the estate back where the rest of this script expects it ==="
  rm -f "$EST/approved.tfplan"
  perl -0777 -pi -e 's/  tags = merge\(local\.tags, \{ Reviewed = "yes" \}\)\n/  tags = local.tags\n/s' "$EST/main.tf"
  [ "$(grep -c '^  tags = local\.tags$' "$EST/main.tf")" = "3" ] \
    || fail "reverting the reviewed edit did not restore all three \"tags = local.tags\" module arguments"
  P_BACK_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; P_BACK_RC=$?
  [ "$P_BACK_RC" -eq 0 ] || { printf '%s\n' "$P_BACK_OUT" | tail -40; fail "the revert apply failed"; }
  P_GONE="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query 'Tags.Reviewed' --output text)"
  [ "$P_GONE" = "None" ] || [ -z "$P_GONE" ] \
    || fail "the Reviewed tag is still on $UNENCRYPTED_QUEUE_URL after the revert: \"$P_GONE\""
  # The whole leg was in-place: the queue PART D renames and PART E removes
  # must still be the same live object, and an SQS queue's identity is its
  # URL, so this reads that URL back rather than trusting the plan.
  P_URL_AFTER="$(awsl sqs get-queue-url --queue-name "$UNENCRYPTED_QUEUE_NAME" --query 'QueueUrl' --output text)"
  [ "$P_URL_AFTER" = "$UNENCRYPTED_QUEUE_URL" ] \
    || fail "the unencrypted queue is now $P_URL_AFTER, not $UNENCRYPTED_QUEUE_URL - PART P moved a live id a later part depends on"
  P_FINAL_OUT="$(plan_into 2>&1)"; P_FINAL_RC=$?
  [ "$P_FINAL_RC" -eq 0 ] || { printf '%s\n' "$P_FINAL_OUT" | tail -40; fail "the post-revert live-plan exited $P_FINAL_RC"; }
  if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$P_FINAL_OUT"; then
    grep -E '^  # .+ (will be|must be)' <<< "$P_FINAL_OUT"; fail "the estate is not converged again after PART P"
  fi
  [ ! -f "$EST/terraform.tfstate" ] || fail "PART P left a state file behind - every other stage in this script asserts there is none"
  log "  reverted; the estate is converged again, no state file, and PART D starts from where it would have"

  log ""
  log "PART P (plan, review, apply): PASS"
  gauntlet_stage plan_approval pass "one argument edited (module.unencrypted_sqs's tags gain Reviewed=yes; that module call declares no DLQ, so var.tags reaches exactly one resource instance), \"plan -out=approved.tfplan\" wrote a $P_PLAN_BYTES-byte stock-format plan file whose whole change set is one update on $P_REVIEWED_ADDR ($UNENCRYPTED_QUEUE_URL); the world then moved out of band ($DEFAULT_QUEUE_URL's Example tag, through the AWS CLI, never through choudoufu - STAGE 5's own proven mutation, on a DIFFERENT instance and a DIFFERENT live queue from the one under review) and \"apply approved.tfplan\" refused with \"The approved plan no longer matches the live system\" at exit 3, classifying the drift under \"This apply would do, and the approved plan does not include:\" and naming both $P_MOVED_ADDR and the live $DEFAULT_QUEUE_URL it was computed against (an aws_sqs_queue's identity IS its URL), with \"Exit status 3\" spelled out for a pipeline; nothing was applied - list-queue-tags on $UNENCRYPTED_QUEUE_URL still returned no Reviewed tag, read back through the AWS CLI rather than from the absence of an \"Apply complete!\" line. Inverted control on the same run (the shape live/smoke/scenarios/apply-what-was-approved.sh reasons out): with the Example tag put back and nothing else changed, the IDENTICAL file applied - 0 added, 1 changed, 0 destroyed - and $UNENCRYPTED_QUEUE_URL read back with Reviewed=yes, so the refusal is earned by the drift and not handed out to every plan file. The edit was then reverted, re-applied, the unencrypted queue's URL confirmed unchanged and the estate replanned empty with no state file, so PART D starts where it would have. BREAK_APPROVAL=1 asserts stage 12's own recorded Break line (apply the planfile after a mutation and expect success) and correctly fails"
  log ""
fi

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  $DEFAULT_QUEUE_URL (module.default_sqs), $UNENCRYPTED_QUEUE_URL (module.unencrypted_sqs)"

if [ "$BREAK_AT" = "rename" ]; then
  log "=== D1 (BREAK=rename). rename module unencrypted_sqs -> unencrypted_sqs_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "unencrypted_sqs" {/module "unencrypted_sqs_renamed" {/' "$EST/main.tf"
  sed -i.bak 's/module\.unencrypted_sqs\./module.unencrypted_sqs_renamed./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  rm -rf "$EST/.terraform"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the BREAK=rename rename's reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  # Verified directly (BREAK=rename is independently reachable here, unlike
  # corpus-eks-basic/corpus-hongbomiao-labelbox): renaming a MODULE CALL
  # without a moved block does not come back as a clean destroy + create
  # the way corpus-eks-basic's security group does, nor as
  # corpus-hongbomiao-labelbox's IAM-role refusal. This is a genuinely
  # stateless live-plan (no local state, ever - see stage 3/4): it walks
  # only the addresses the CURRENT config declares, so an address no
  # longer declared (the old module.unencrypted_sqs) is never visited at
  # all - there is nothing to propose destroying, and the marker it still
  # carries is simply left behind, orphaned. The new address (module.
  # unencrypted_sqs_renamed) IS declared, has no marker of its own, and
  # the queue's name/URL (unaffected by the module CALL'S own label) is
  # deterministically client-named, so it is proposed for creation - a
  # create that would actually collide with the live queue at apply time
  # (SQS's own CreateQueue is idempotent for identical attributes, so it
  # would likely succeed and bind rather than error, but this script does
  # not go that far). The plan/apply RC IS still expected non-zero here
  # for a DIFFERENT reason than eks-basic's: BREAK=rename only proves this
  # assertion, no further staging runs.
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=rename rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.unencrypted_sqs\.aws_sqs_queue\.this\[0\] will be' <<< "$BREAK_PLAN_OUT" \
    && { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: the old, no-longer-declared address unexpectedly still appears in the plan - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.unencrypted_sqs_renamed\.aws_sqs_queue\.this\[0\] will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: renaming without a moved block did not propose creating the renamed queue - this stage's check is not load-bearing"; }
  log "  BREAK=rename: correctly proposes ONLY a create for the renamed address, no destroy of the old (no-longer-declared) one - the real, precisely-named outcome for a stateless live-plan over a client-named type with no moved block, not the literal destroy-and-create the stage's own Break text describes; see header - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module default_sqs -> default_sqs_renamed ==="
  sed -i.bak 's/module "default_sqs" {/module "default_sqs_renamed" {/' "$EST/main.tf"
  sed -i.bak 's/module\.default_sqs\./module.default_sqs_renamed./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  cat >> "$EST/main.tf" <<'EOF'

moved {
  from = module.default_sqs
  to   = module.default_sqs_renamed
}
EOF
  rm -rf "$EST/.terraform"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # module\.default_sqs_renamed\.aws_sqs_queue\.this\[0\] will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed queue"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.default_sqs\.aws_sqs_queue\.this:0" +-> +"module\.default_sqs_renamed\.aws_sqs_queue\.this:0"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the queue's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  DEFAULT_ADDR_D_AFTER="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
  [ "$DEFAULT_ADDR_D_AFTER" = "module.default_sqs_renamed.aws_sqs_queue.this:0" ] \
    || fail "the default queue carries tofu-address=$DEFAULT_ADDR_D_AFTER after the rename, not module.default_sqs_renamed.aws_sqs_queue.this:0"
  log "  $DEFAULT_QUEUE_URL unchanged, tofu-address now module.default_sqs_renamed.aws_sqs_queue.this:0 - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module unencrypted_sqs -> unencrypted_sqs_renamed, no moved block at all ==="
  sed -i.bak 's/module "unencrypted_sqs" {/module "unencrypted_sqs_renamed" {/' "$EST/main.tf"
  sed -i.bak 's/module\.unencrypted_sqs\./module.unencrypted_sqs_renamed./g' "$EST/outputs.tf"
  rm -f "$EST/main.tf.bak" "$EST/outputs.tf.bak"
  rm -rf "$EST/.terraform"
  ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$EST" && "$TOFU" live-mv -estate="$ESTATE" 'module.unencrypted_sqs.aws_sqs_queue.this[0]' 'module.unencrypted_sqs_renamed.aws_sqs_queue.this[0]' 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.unencrypted_sqs.aws_sqs_queue.this:0" -> "module.unencrypted_sqs_renamed.aws_sqs_queue.this:0"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  UNENCRYPTED_ADDR_D_AFTER="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
  [ "$UNENCRYPTED_ADDR_D_AFTER" = "module.unencrypted_sqs_renamed.aws_sqs_queue.this:0" ] \
    || fail "the unencrypted queue carries tofu-address=$UNENCRYPTED_ADDR_D_AFTER after live-mv, not module.unencrypted_sqs_renamed.aws_sqs_queue.this:0"
  log "  $UNENCRYPTED_QUEUE_URL unchanged, tofu-address now module.unencrypted_sqs_renamed.aws_sqs_queue.this:0 - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$FINAL_PLAN_D_OUT" \
    && { printf '%s\n' "$FINAL_PLAN_D_OUT" | grep -E '^  # .+ will be'; fail "the post-rename plan proposes a resource change"; }
  log "  no resource action proposed. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.default_sqs renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: module.unencrypted_sqs renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, planned - live/GAUNTLET.md #9)
  # ════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.default_sqs_renamed
  # (originally module.default_sqs, renamed above by a moved block, not by
  # live-mv) is bound and converged, standalone, and untouched by anything
  # else in this script. Its `name` argument - not the module CALL's own
  # label, which this stage never touches - changes to a new literal
  # queue name. aws_sqs_queue's `name` is ForceNew in the provider's real
  # schema (confirmed by the plan output itself below, not assumed: AWS
  # has no RenameQueue API, only CreateQueue/DeleteQueue), so this forces
  # a replacement at the SAME declared address (module.default_sqs_
  # renamed.aws_sqs_queue.this[0] never changes) while the physical live
  # object behind it is destroyed and a new one created - the marker
  # moving onto the new object is this stage's own Proves text.
  #
  # THE create_before_destroy SCOPE NOTE. tools/gauntlet/stages.go's Title
  # names create_before_destroy specifically. OpenTofu core rejects a
  # `lifecycle` block written directly on a `module` call ("Reserved block
  # type name in module block" - confirmed empirically against a trivial
  # reproduction outside this repo before writing this section), and the
  # queue resource lives inside the terraform-aws-sqs registry module,
  # whose own source this corpus's established convention (see the
  # header's THE REDUCTION) only ever REMOVES real upstream content from,
  # never adds library-internal lifecycle blocks to. Patching the module's
  # own aws_sqs_queue resource to add create_before_destroy would cross
  # that line, so this evidence pass exercises OpenTofu's DEFAULT replace
  # ordering instead (destroy-then-create - confirmed below by the plan's
  # own "-/+ destroy and then create replacement" legend). The
  # marker-on-new-object and clean-old-object outcomes this stage's Proves
  # text cares about are identical either way; only the instant the two
  # objects would briefly coexist differs, and BREAK=replace below
  # manufactures exactly that coexistence directly via the AWS CLI rather
  # than through an interrupted apply (day2_crash, stage 10, owns testing
  # a real interruption).
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.default_sqs_renamed.aws_sqs_queue.this[0]"
  F_RECORD="$EST/.tofu-records/tofu-records/$ESTATE/aws_sqs_queue/$(record_key "$F_ADDR")"

  log "=== F0. capture the live queue and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$DEFAULT_QUEUE_URL" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $DEFAULT_QUEUE_URL"
  F_OLD_ADDR_TAG="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.default_sqs_renamed.aws_sqs_queue.this:0" ] \
    || fail "$DEFAULT_QUEUE_URL does not carry tofu-address=module.default_sqs_renamed.aws_sqs_queue.this:0 ahead of day2_replace"
  log "  $DEFAULT_QUEUE_URL, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "$BREAK_AT" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live queue carrying the SAME tofu-address and
    # tofu-slot as the one a genuine replace would destroy - the state
    # "skip the destroy half" of a create-before-destroy replace would
    # leave, produced directly via the AWS CLI rather than by actually
    # interrupting an apply (day2_crash's own job).
    #
    # The refusal's message text changed under GitHub issue #409 (choudoufu
    # #409, unrelated to this corpus): bindCountBlock now routes every count
    # block carrying any record-backed entry through the address path
    # unconditionally, before ever asking whether the live set carries slot
    # tags - trusting slot data for a block containing a record-backed entry
    # (this queue's own declared instance, converged by STAGE 2's migrate)
    # is exactly the hazard #409 closed. So this collision, though still a
    # hard refusal naming both live objects, now reads "Indistinguishable
    # instances without per-instance markers" rather than the "Two live
    # resources claiming one slot" this assertion checked before #409
    # landed - see git history for that prior version if this ever needs
    # re-deriving. #409's own fix, not a regression in what this stage
    # proves: no live object collision is ever silently accepted either
    # way.
    BREAK_COLLISION_NAME="${NAME}-default-collision"
    awsl sqs create-queue --queue-name "$BREAK_COLLISION_NAME" \
      --tags "tofu-estate=$ESTATE,tofu-address=module.default_sqs_renamed.aws_sqs_queue.this:0,tofu-slot=0" \
      >/dev/null || fail "BREAK=replace: could not create the collision queue"
    BREAK_COLLISION_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${BREAK_COLLISION_NAME}"
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    awsl sqs delete-queue --queue-url "$BREAK_COLLISION_URL" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Indistinguishable instances without per-instance markers' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the manufactured collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (indistinguishable instances without per-instance markers) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew name argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/name = "\${local\.name}-default"/name = "${local.name}-default-v2"/' "$EST/main.tf"
    rm -f "$EST/main.tf.bak"
    grep -q 'default-v2' "$EST/main.tf" || fail "changing module.default_sqs_renamed's name argument did not match - the corpus pin has moved"
    F_NEW_NAME="${NAME}-default-v2"
    F_NEW_URL="https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${F_NEW_NAME}"

    F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.default_sqs_renamed\.aws_sqs_queue\.this\[0\] must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.default_sqs_renamed's queue when its ForceNew name argument changes"; }
    grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark name as forcing replacement"; }
    grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add and one destroy at the same address"; }
    log "  choudoufu: exactly one forced replace at the same declared address (module.default_sqs_renamed.aws_sqs_queue.this[0]), name forces replacement"

    F_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add and one destroy"; }

    if F_OLD_STILL="$(awsl sqs get-queue-url --queue-name "$DEFAULT_QUEUE_NAME" 2>&1)"; then
      echo "$F_OLD_STILL"; fail "$DEFAULT_QUEUE_NAME still exists after the replace - the old object was orphaned, not destroyed"
    fi
    log "  $DEFAULT_QUEUE_NAME no longer exists (NonExistentQueue) - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_ADDR_TAG="$(awsl sqs list-queue-tags --queue-url "$F_NEW_URL" --query "Tags.\"tofu-address\"" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.default_sqs_renamed.aws_sqs_queue.this:0" ] \
      || fail "$F_NEW_URL carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.default_sqs_renamed.aws_sqs_queue.this:0 - the marker did not move onto the new object"
    log "  $F_NEW_URL (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW object's import_id, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_URL" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_URL - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be' <<< "$F_FINAL_PLAN_OUT" \
      && { printf '%s\n' "$F_FINAL_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the post-replace plan proposes a resource change"; }
    log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

    gauntlet_stage day2_replace pass "choudoufu: changing module.default_sqs_renamed's ForceNew name argument proposed exactly one replace at the same declared address (1 add, 0 change, 1 destroy; -/+ destroy and then create), applied cleanly; the old object ($DEFAULT_QUEUE_URL) is confirmed gone and the new object ($F_NEW_URL) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new object's import_id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace at the same address (plan only, not applied - it shares floci's account with \$EST); BREAK=replace confirms a manufactured marker collision is reported loudly rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
  fi
  gauntlet_end_stage

  # ════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, live/GAUNTLET.md #7)
  # ════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.unencrypted_sqs_
  # renamed (originally module.unencrypted_sqs, renamed here by live-mv
  # with no moved block) is bound and converged. Its block is removed
  # here - a single, standalone, self-contained queue with no sibling
  # module referencing it (unlike fifo_sqs's DLQ pair), same shape
  # E-ORACLE above already proved stock destroys cleanly on cold_deploy's
  # own state. THE OUTPUTS QUIRK (see header): unlike iam-policy's Part E,
  # this estate's outputs.tf DOES reference the module being removed (six
  # "# Unencrypted" blocks), so removing the module block alone would
  # leave a dangling reference and fail re-init with a config error, not
  # a clean destroy plan - both are removed together below, the same
  # move E-ORACLE already made on its own copy.
  #
  # BREAK=remove exercises this stage's own Break control instead: keep
  # the block (and its outputs), and assert the plan proposes no destroy
  # for it at all - the Break text in tools/gauntlet/stages.go, verbatim.
  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live tofu-address one more time ==="
  E_ADDR_BEFORE="$(awsl sqs list-queue-tags --queue-url "$UNENCRYPTED_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text 2>/dev/null || true)"
  [ "$E_ADDR_BEFORE" = "module.unencrypted_sqs_renamed.aws_sqs_queue.this:0" ] \
    || fail "$UNENCRYPTED_QUEUE_URL does not carry tofu-address=module.unencrypted_sqs_renamed.aws_sqs_queue.this:0 before day2_remove even starts (got $E_ADDR_BEFORE)"

  if [ "$BREAK_AT" = "remove" ]; then
    log "=== E1 (BREAK=remove). keep module.unencrypted_sqs_renamed's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK=remove kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.unencrypted_sqs_renamed\.aws_sqs_queue\.this\[0\] will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK=remove: a destroy was proposed for module.unencrypted_sqs_renamed's queue even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK=remove: some resource action was proposed with the block still in the config"; }
    log "  BREAK=remove: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.unencrypted_sqs_renamed's block ==="
    perl -0pi -e 's/module "unencrypted_sqs_renamed" \{.*?\n\}\n\n//s' "$EST/main.tf"
    grep -q 'module "unencrypted_sqs_renamed"' "$EST/main.tf" \
      && fail "removing module.unencrypted_sqs_renamed's block did not match - the config has moved"
    perl -0777 -pi -e 's/\n# Unencrypted\n.*?\n# Disabled/\n# Disabled/s' "$EST/outputs.tf"
    grep -q 'module.unencrypted_sqs_renamed' "$EST/outputs.tf" \
      && fail "removing module.unencrypted_sqs_renamed's outputs did not match - the config has moved"
    rm -rf "$EST/.terraform"
    ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of module.unencrypted_sqs_renamed's queue as a possible rename - this is the honest wall the day2_rename/day2_remove ambiguity names, not a pass"
    fi
    grep -qE '^  # module\.unencrypted_sqs_renamed\.aws_sqs_queue\.this\[0\] will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.unencrypted_sqs_renamed's queue when its block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (module.unencrypted_sqs_renamed's queue), nothing else"

    REMOVE_APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    # A deleted SQS queue's get-queue-url is a real, documented error
    # (AWS.SimpleQueueService.NonExistentQueue), confirmed the same way
    # every other day2_remove check confirms deletion: directly via the
    # AWS CLI against floci, not through choudoufu's own report.
    if E_STILL="$(awsl sqs get-queue-url --queue-name "$UNENCRYPTED_QUEUE_NAME" 2>&1)"; then
      echo "$E_STILL"; fail "$UNENCRYPTED_QUEUE_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    log "  $UNENCRYPTED_QUEUE_NAME no longer exists (NonExistentQueue) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$E_FINAL_PLAN_OUT" \
      && { printf '%s\n' "$E_FINAL_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the post-remove plan proposes a resource change"; }
    log "  no resource action proposed. The removal is complete and invisible to the next plan."

    log ""
    log "STAGE E (day2_remove): PASS"
    gauntlet_stage day2_remove pass "choudoufu: deleting module.unencrypted_sqs_renamed's block proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the object is genuinely gone from the live account (sqs get-queue-url on the old name now returns NonExistentQueue, read via the AWS CLI, not choudoufu's own report), and the next plan proposes no resource action; stock oracle on cold_deploy's own state (E-ORACLE) also proposes exactly one destroy for the same object (before any rename ever touched it)"
    log ""
    # ══════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8, issue
    # #643)
    # ══════════════════════════════════════════════════════════════════════
    #
    # Starts from PART E's real, completed state: five managed instances
    # (module.default_sqs_renamed's replaced queue, the fifo pair and their
    # two untaggable redrive resources), a plan that proposes nothing, and
    # module.unencrypted_sqs_renamed genuinely destroyed. A NEW, entirely
    # synthetic root resource - aws_sqs_queue.count_test, count_test_block()
    # defined above G-ORACLE and byte-for-byte the same text both legs get -
    # is added here in its own file, so day2_count's history is
    # self-contained and never revisits an address any other stage used.
    # G-ORACLE above is the stock oracle for the identical shape, applied
    # for real in the idle greenfield-oracle account before this script tore
    # that container down; the synthetic block is issue #488's sanctioned
    # fallback and the reason it is needed here (four boolean create
    # toggles, no numeric knob anywhere in the module or the example) is
    # G-ORACLE's own WHY A SYNTHETIC BLOCK note.
    #
    # BREAK_COUNT=1 (header) points the scale-down assertion at the WRONG
    # index and the stage goes red - the Break text, verbatim. Reachable
    # only when BREAK is neither "rename" nor "remove", because PART G
    # starts from PART E's real removal.
    gauntlet_begin_stage day2_count
    log "=== G0. choudoufu: add aws_sqs_queue.count_test, count = 2 ==="
    count_test_block 2 > "$EST/day2_count.tf"
    ( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the count-block-add reinit failed"; }
    G_ADD_PLAN="$(plan_into 2>&1)"; G_ADD_PLAN_RC=$?
    [ "$G_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_ADD_PLAN" | tail -30; fail "the count-block-add plan exited $G_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$G_ADD_PLAN" \
      || { printf '%s\n' "$G_ADD_PLAN" | tail -10; fail "adding the 2-instance count block did not plan exactly 2 creates"; }
    G_ADD_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_ADD_APPLY_RC=$?
    [ "$G_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_ADD_APPLY" | tail -30; fail "the count-block-add apply exited $G_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$G_ADD_APPLY" \
      || { grep -E 'Apply complete' <<< "$G_ADD_APPLY"; fail "the count-block-add apply did not create exactly 2 resources"; }

    # Both instances' identities asserted BY VALUE off the live queues, not
    # inferred from the apply having succeeded (HANDOFF's safety rule). A
    # count instance's tofu-address is colon-escaped (live/MARKERS.md:
    # aws_eip.this[2] -> aws_eip.this:2), and tofu-slot is the per-instance
    # marker that makes this block resolvable by a stateless plan at all -
    # the same marker the header's TOFU-SLOT FINDING is about, here carrying
    # two DIFFERENT values for the first time in this estate.
    G_CT0_ADDR="$(awsl sqs list-queue-tags --queue-url "$CT0_URL" --query "Tags.\"tofu-address\"" --output text)"
    G_CT1_ADDR="$(awsl sqs list-queue-tags --queue-url "$CT1_URL" --query "Tags.\"tofu-address\"" --output text)"
    [ "$G_CT0_ADDR" = 'aws_sqs_queue.count_test:0' ] || fail "count_test[0] ($CT0_URL) carries tofu-address=$G_CT0_ADDR, not aws_sqs_queue.count_test:0"
    [ "$G_CT1_ADDR" = 'aws_sqs_queue.count_test:1' ] || fail "count_test[1] ($CT1_URL) carries tofu-address=$G_CT1_ADDR, not aws_sqs_queue.count_test:1"
    G_CT0_SLOT="$(awsl sqs list-queue-tags --queue-url "$CT0_URL" --query "Tags.\"tofu-slot\"" --output text)"
    G_CT1_SLOT="$(awsl sqs list-queue-tags --queue-url "$CT1_URL" --query "Tags.\"tofu-slot\"" --output text)"
    [ "$G_CT0_SLOT" = "0" ] || fail "count_test[0] carries tofu-slot=$G_CT0_SLOT, not 0"
    [ "$G_CT1_SLOT" = "1" ] || fail "count_test[1] carries tofu-slot=$G_CT1_SLOT, not 1"
    G_CT0_TS="$(queue_created_ts "$ENDPOINT" "$CT0_URL")"
    G_CT1_TS="$(queue_created_ts "$ENDPOINT" "$CT1_URL")"
    [ -n "$G_CT0_TS" ] && [ "$G_CT0_TS" != "None" ] || fail "live count_test[0] queue has no CreatedTimestamp"
    [ -n "$G_CT1_TS" ] && [ "$G_CT1_TS" != "None" ] || fail "live count_test[1] queue has no CreatedTimestamp"
    log "  2 instances created, read via the AWS CLI:"
    log "    index 0 = $CT0_URL (tofu-address=$G_CT0_ADDR, tofu-slot=$G_CT0_SLOT, created=$G_CT0_TS)"
    log "    index 1 = $CT1_URL (tofu-address=$G_CT1_ADDR, tofu-slot=$G_CT1_SLOT, created=$G_CT1_TS)"

    G_NOOP_PLAN="$(plan_into 2>&1)"; G_NOOP_PLAN_RC=$?
    [ "$G_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_NOOP_PLAN" | tail -30; fail "the post-add plan exited $G_NOOP_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$G_NOOP_PLAN" \
      && { printf '%s\n' "$G_NOOP_PLAN" | grep -E '^  # .+ will be'; fail "the plan right after adding the count block proposes a resource change - the two new instances did not bind their own markers cleanly"; }
    log "  no resource action proposed - both new instances bind immediately, statelessly, off their own slot markers"

    log "=== G1. scale count down: 2 -> 1 ==="
    count_test_block 1 > "$EST/day2_count.tf"
    G_DOWN_PLAN="$(plan_into 2>&1)"; G_DOWN_PLAN_RC=$?
    [ "$G_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_DOWN_PLAN" | tail -30; fail "the scale-down plan exited $G_DOWN_PLAN_RC"; }

    # The Break control is this index and nothing else: the SAME assertion,
    # pointed at the instance that must survive. The plan really destroys
    # index 1, so under BREAK_COUNT=1 the check below fails and fail()
    # records verdict=fail for day2_count - which is the whole point of
    # having it. (An inverted branch that reported success when the
    # corruption "did not take" would exit 0 on a run the operator asked to
    # break, and prove nothing.)
    G_WANT_DESTROY=1
    G_WANT_KEEP=0
    if [ "${BREAK_COUNT:-}" = "1" ]; then
      G_WANT_DESTROY=0
      G_WANT_KEEP=1
      log "  BREAK_COUNT=1: expecting count_test[0] - the SURVIVOR - to have been"
      log "           the instance destroyed by scaling 2 -> 1. Same resource,"
      log "           same block, same plan; only the index is wrong. This step"
      log "           must fail and the stage must report verdict=fail."
    fi
    grep -qE "^  # aws_sqs_queue\.count_test\[$G_WANT_DESTROY\] will be destroyed" <<< "$G_DOWN_PLAN" \
      || { printf '%s\n' "$G_DOWN_PLAN" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[$G_WANT_DESTROY]"; }
    grep -qE "^  # aws_sqs_queue\.count_test\[$G_WANT_KEEP\] will be" <<< "$G_DOWN_PLAN" \
      && { printf '%s\n' "$G_DOWN_PLAN" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[$G_WANT_KEEP], which should be untouched"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$G_DOWN_PLAN" \
      || { printf '%s\n' "$G_DOWN_PLAN" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

    G_DOWN_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_DOWN_APPLY_RC=$?
    [ "$G_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_DOWN_APPLY" | tail -30; fail "the scale-down apply exited $G_DOWN_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$G_DOWN_APPLY" \
      || { grep -E 'Apply complete' <<< "$G_DOWN_APPLY"; fail "the scale-down apply was not exactly one destroy"; }

    # The destroy, witnessed by absence: a queue's URL is rebuilt from
    # region + account + name, so the URL alone can never say whether the
    # object behind it is the same one (header). get-queue-url on the name
    # must now be a real AWS.SimpleQueueService.NonExistentQueue.
    if G_CT1_STILL="$(awsl sqs get-queue-url --queue-name "$CT1_NAME" 2>&1)"; then
      echo "$G_CT1_STILL"; fail "count_test[1] ($CT1_NAME) still exists in the live account after the scale-down destroy - it was orphaned, not destroyed"
    fi
    # The survivor, witnessed by its own live identifier and its markers -
    # read back through the AWS CLI, never through choudoufu's own report.
    G_CT0_URL_AFTER="$(awsl sqs get-queue-url --queue-name "$CT0_NAME" --query QueueUrl --output text)"
    [ "$G_CT0_URL_AFTER" = "$CT0_URL" ] || fail "count_test[0]'s live queue URL changed across the scale-down: $CT0_URL -> $G_CT0_URL_AFTER"
    G_CT0_TS_DOWN="$(queue_created_ts "$ENDPOINT" "$CT0_URL")"
    [ "$G_CT0_TS_DOWN" = "$G_CT0_TS" ] || fail "count_test[0]'s CreatedTimestamp changed across the scale-down ($G_CT0_TS -> $G_CT0_TS_DOWN) - it was destroyed and recreated, not left alone"
    G_CT0_ADDR_DOWN="$(awsl sqs list-queue-tags --queue-url "$CT0_URL" --query "Tags.\"tofu-address\"" --output text)"
    [ "$G_CT0_ADDR_DOWN" = 'aws_sqs_queue.count_test:0' ] || fail "count_test[0] carries tofu-address=$G_CT0_ADDR_DOWN after the scale-down, not aws_sqs_queue.count_test:0"
    G_CT0_SLOT_DOWN="$(awsl sqs list-queue-tags --queue-url "$CT0_URL" --query "Tags.\"tofu-slot\"" --output text)"
    [ "$G_CT0_SLOT_DOWN" = "0" ] || fail "count_test[0] carries tofu-slot=$G_CT0_SLOT_DOWN after the scale-down, not 0"

    # The destroyed instance's local record is TOMBSTONED, not deleted
    # outright ([projection.RecordStore.tombstone]): the envelope's
    # top-level "identity" is cleared and a "tombstone" entry is added, so
    # the honest check is has(tombstone) and not has(identity), never file
    # absence. A record left still naming the destroyed queue would be the
    # wrong-marker failure that outranks a missing one.
    G_CT1_RECORD="$EST/.tofu-records/tofu-records/$ESTATE/aws_sqs_queue/$(record_key 'aws_sqs_queue.count_test[1]')"
    [ -f "$G_CT1_RECORD" ] || fail "no local record file found for aws_sqs_queue.count_test[1] after the scale-down - expected a tombstoned record, not none at all"
    jq -e 'has("tombstone") and (has("identity") | not)' "$G_CT1_RECORD" >/dev/null \
      || fail "the record at aws_sqs_queue.count_test[1] after the scale-down is not tombstoned: $(cat "$G_CT1_RECORD")"
    log "  $CT1_NAME (count_test[1]) is gone (NonExistentQueue); $CT0_NAME (count_test[0]) still at $G_CT0_URL_AFTER with created=$G_CT0_TS_DOWN, tofu-address=$G_CT0_ADDR_DOWN, tofu-slot=$G_CT0_SLOT_DOWN, all unchanged; count_test[1]'s local record is tombstoned, not deleted"

    log "=== G2. scale count back up: 1 -> 2 ==="
    # CreatedTimestamp is epoch seconds (header), so the gap has to exceed
    # the resolution of the value being compared or a genuine recreate could
    # read as no change.
    sleep 2
    count_test_block 2 > "$EST/day2_count.tf"
    G_UP_PLAN="$(plan_into 2>&1)"; G_UP_PLAN_RC=$?
    [ "$G_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_UP_PLAN" | tail -30; fail "the scale-up plan exited $G_UP_PLAN_RC"; }
    grep -qE '^  # aws_sqs_queue\.count_test\[1\] will be created' <<< "$G_UP_PLAN" \
      || { printf '%s\n' "$G_UP_PLAN" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
    grep -qE '^  # aws_sqs_queue\.count_test\[0\] will be' <<< "$G_UP_PLAN" \
      && { printf '%s\n' "$G_UP_PLAN" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
    grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$G_UP_PLAN" \
      || { printf '%s\n' "$G_UP_PLAN" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
    log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

    G_UP_APPLY="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; G_UP_APPLY_RC=$?
    [ "$G_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$G_UP_APPLY" | tail -30; fail "the scale-up apply exited $G_UP_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$G_UP_APPLY" \
      || { grep -E 'Apply complete' <<< "$G_UP_APPLY"; fail "the scale-up apply was not exactly one create"; }

    G_CT1_TS_UP="$(queue_created_ts "$ENDPOINT" "$CT1_URL")"
    [ -n "$G_CT1_TS_UP" ] && [ "$G_CT1_TS_UP" != "None" ] || fail "no live count_test[1] queue found after the scale-up"
    [ "$G_CT1_TS_UP" -gt "$G_CT1_TS" ] \
      || fail "count_test[1] came back with CreatedTimestamp $G_CT1_TS_UP, not later than the destroyed queue's $G_CT1_TS - the destroy in G1 was not real"
    G_CT1_ADDR_UP="$(awsl sqs list-queue-tags --queue-url "$CT1_URL" --query "Tags.\"tofu-address\"" --output text)"
    [ "$G_CT1_ADDR_UP" = 'aws_sqs_queue.count_test:1' ] || fail "the recreated count_test[1] carries tofu-address=$G_CT1_ADDR_UP, not aws_sqs_queue.count_test:1"
    G_CT1_SLOT_UP="$(awsl sqs list-queue-tags --queue-url "$CT1_URL" --query "Tags.\"tofu-slot\"" --output text)"
    [ "$G_CT1_SLOT_UP" = "1" ] || fail "the recreated count_test[1] carries tofu-slot=$G_CT1_SLOT_UP, not 1"
    G_CT0_TS_UP="$(queue_created_ts "$ENDPOINT" "$CT0_URL")"
    [ "$G_CT0_TS_UP" = "$G_CT0_TS" ] || fail "count_test[0]'s CreatedTimestamp changed across the scale-up ($G_CT0_TS -> $G_CT0_TS_UP)"
    G_CT0_ADDR_UP="$(awsl sqs list-queue-tags --queue-url "$CT0_URL" --query "Tags.\"tofu-address\"" --output text)"
    [ "$G_CT0_ADDR_UP" = 'aws_sqs_queue.count_test:0' ] || fail "count_test[0] carries tofu-address=$G_CT0_ADDR_UP after the scale-up, not aws_sqs_queue.count_test:0"
    # The record at the recreated index must name the NEW object, and the
    # tombstone must be gone - a record still tombstoned while the object is
    # live is the read-half-without-the-write-half shape.
    G_CT1_RECORD_ID="$(record_import_id "$G_CT1_RECORD" 2>/dev/null || true)"
    [ "$G_CT1_RECORD_ID" = "$CT1_URL" ] \
      || fail "the record at aws_sqs_queue.count_test[1] after the scale-up names $G_CT1_RECORD_ID, not the recreated queue $CT1_URL"
    log "  count_test[1] recreated at the SAME url $CT1_URL (deterministic - region + account + name) but created=$G_CT1_TS_UP, was $G_CT1_TS; tofu-address=$G_CT1_ADDR_UP, tofu-slot=$G_CT1_SLOT_UP; count_test[0] created=$G_CT0_TS_UP and tofu-address=$G_CT0_ADDR_UP unchanged throughout the whole cycle"

    log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
    G_FINAL_PLAN="$(plan_into 2>&1)"; G_FINAL_PLAN_RC=$?
    [ "$G_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$G_FINAL_PLAN" | tail -30; fail "the post-scale-up plan exited $G_FINAL_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$G_FINAL_PLAN" \
      && { printf '%s\n' "$G_FINAL_PLAN" | grep -E '^  # .+ will be'; fail "the post-scale-up plan proposes a resource change"; }
    log "  no resource action proposed. The down-then-up cycle is complete and invisible to the next plan."

    log ""
    log "STAGE G (day2_count): PASS"
    gauntlet_stage day2_count pass "synthetic block (all four module calls declare count = var.create ? 1 : 0, a boolean create toggle, so the estate has no knob that scales - issue #488's sanctioned fallback, reusing aws_sqs_queue, a type this estate already exercises four times): scaling aws_sqs_queue.count_test from 2 to 1 proposed and applied exactly one destroy (0 add, 0 change, 1 destroy) of the HIGHER index, count_test[1]; the survivor count_test[0] kept its live queue URL ($CT0_URL), its CreatedTimestamp ($G_CT0_TS) and its tofu-address=aws_sqs_queue.count_test:0 / tofu-slot=0 markers, all read back through the AWS CLI, and count_test[1]'s local record was tombstoned rather than left naming a destroyed queue; scaling 1 back to 2 proposed and applied exactly one create (1 add, 0 change, 0 destroy), and because a queue URL is rebuilt from region + account + name the recreated instance comes back at the SAME url - so the destroy is witnessed two other ways instead, by AWS.SimpleQueueService.NonExistentQueue in between and by a strictly later CreatedTimestamp ($G_CT1_TS -> $G_CT1_TS_UP), with tofu-address=aws_sqs_queue.count_test:1 back on the new object and index 0 untouched throughout; the next plan proposes no resource action; the G-ORACLE stock oracle stood the identical block up for real in the idle greenfield-oracle account and showed the identical shape - destroy the higher index only, create it back under the same url with a new CreatedTimestamp, the lower index unchanged both times"
    log ""
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end

log "=== PASS ==="
log ""
log "A terraform-aws-modules EXAMPLE (reduced per this script's header) -"
log "the SQS queueing surface, new to this corpus - crossed through all"
log "five stages: cold deploy with plain terraform, choudoufu live-import"
log "adoption plus the tofu-slot convergence apply it requires, an empty"
log "replan with the state file deleted and rendered identities checked"
log "against SQS's own answer, a genuine no-op apply, and drift on one"
log "queue reconverging without touching the others."
log ""
log "Six managed resources: four tagged queues, plus"
log "aws_sqs_queue_redrive_policy and aws_sqs_queue_redrive_allow_policy -"
log "two untaggable types with no row in the generated identity table, both"
log "resolved to the right queue URL by the provider's own identity schema"
log "and asserted by value above. Tagged, plus derived-from-tagged; no"
log "third bucket."
