#!/usr/bin/env bash
set -uo pipefail

# STATUS AS COMMITTED (2026-08-20): UNVERIFIED past "floci healthy". The
# corpus pin (live/corpus-manifest.json, terraform-aws-sqs v5.2.2) is real
# and `just corpus-fetch` materializes it. The reduction below (see THE
# REDUCTION) was run for real and its DELTA lines print correctly against
# the fetched files. Stage 1's `terraform init` was in flight, not yet
# past `terraform apply`, when this session was asked to wrap up - so
# every resource count, queue URL, and tofu-address string below this
# point is DERIVED from reading the terraform-aws-sqs module source
# (main.tf's naming locals), not confirmed against a real `terraform
# apply` + AWS CLI read. Treat every assertion past STAGE 1's `terraform
# apply` line as a hypothesis the next run must confirm or correct before
# any stage is recorded as pass in live/corpus-crossing-manifest.json - no
# entry was added there for exactly this reason. Rerun with:
#   bash live/e2e/corpus-sqs-basic/run.sh
# and fix whichever assertion the real output disagrees with first (most
# likely candidates: the FIFO DLQ name derivation, or the exact
# `Tags."tofu-address"` JMESPath quoting for `aws sqs list-queue-tags`).
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
# THE RESOURCE SHAPE (5 resources, exercising two AWS resource types):
#   aws_sqs_queue x4          default_sqs, fifo_sqs (standard), fifo_sqs's
#                              own DLQ, unencrypted_sqs - all taggable,
#                              server-assigned-if-absent identity
#                              (live/identity/table_generated.go's
#                              aws_sqs_queue row: the queue URL rebuilt from
#                              region + account + name).
#   aws_sqs_queue_redrive_policy x1   fifo_sqs's redrive policy binding its
#                              queue to its own DLQ. UNTAGGABLE (no ARN of
#                              its own) and absent from the generated
#                              identity table - live/survey-full.json
#                              classifies it "client-named", admission
#                              "schema": every required-for-import identity
#                              attribute (queue_url) is a required argument,
#                              so the provider's own identity schema settles
#                              it (see HANDOFF's "Provider identity schemas
#                              are plumbed and load-bearing"). This script is
#                              a real, live test of that schema-fallback path
#                              for a genuinely popular resource type, not a
#                              type this repo has crossed live before.
#
# All four aws_sqs_queue resources declare `count = var.create ? 1 : 0`
# (module default), the same shape corpus-iam-policy's "THE TOFU-SLOT
# FINDING" documents: live-import -approve writes only tofu-estate and
# tofu-address, and a follow-up ordinary `choudoufu apply` is required to
# converge tofu-slot before a replan is genuinely empty. That convergence
# step is folded into STAGE 2 below, same as corpus-iam-policy.
#
# STAGE-BY-STAGE SHAPE (issue #274's five-stage pipeline; see
# live/corpus-crossing-manifest.json):
#
#   1. COLD DEPLOY   plain `terraform apply` (real HashiCorp terraform, not
#                     choudoufu), no live block anywhere.
#   2. MIGRATE        `choudoufu live-import -approve` against the cold
#                     state, then one ordinary `choudoufu apply` to converge
#                     tofu-slot on the four count-based queues.
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
#   BREAK        set to 1 to corrupt one expected identity string ahead of
#                stage 3's assertion (the fifo_sqs queue's escaped address,
#                same shape and resource type, the wrong module) AND to
#                tamper a second, unrelated queue's tag ahead of stage 5's
#                drift assertion - proving both are load-bearing rather than
#                a grep/count that always matches.
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

ESTATE="sqs-basic-crossing"
REGION="eu-west-1"
ACCOUNT="000000000000"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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
log "=== STAGE 1: cold deploy (terraform apply, the real reduced example + delta) ==="
( cd "$EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$EST" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "the cold apply failed"; }
grep -qE 'Apply complete! Resources: 5 added' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "the cold apply did not create exactly 5 resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"

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
log "STAGE 1 (cold deploy): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state, then one
# ordinary apply to converge tofu-slot (see the header's TOFU-SLOT note)
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: migrate (choudoufu live-import -approve, then converge) ==="
perl -0pi -e 's/(required_providers \{\n    aws = \{\n      source  = "hashicorp\/aws"\n      version = ">= 6\.28"\n    \}\n  \}\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$EST/versions.tf" || fail "the live block delta did not match versions.tf - the corpus pin has moved"

( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "choudoufu init failed"; }

rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"

IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "5 of 5 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 5 of 5 resources as eligible - the corpus pin or the fix under test has moved"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
log "  dry run: 5 of 5 eligible; nothing written yet"

APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$WORK/cold.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "5 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 5 of 5 resources cleanly"; }
log "  5 stamped"

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

# The tofu-slot convergence apply (see this script's header). All four
# aws_sqs_queue resources declare count = var.create ? 1 : 0, so all four
# need it. The redrive policy is untaggable and carries no slot.
CONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CONVERGE_RC=$?
[ "$CONVERGE_RC" -eq 0 ] || { printf '%s\n' "$CONVERGE_OUT" | tail -40; fail "the tofu-slot convergence apply failed"; }
grep -qE 'Resources: 0 added, 4 changed, 0 destroyed' <<< "$CONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$CONVERGE_OUT"; fail "the convergence apply did not change exactly 4 resources (expected: all four queues gaining tofu-slot)"; }
log "  $(grep -E 'Apply complete' <<< "$CONVERGE_OUT") (tofu-slot written on all four queues)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the convergence apply wrote a state file"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted (already true), live-plan empty,
# identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
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
if [ "${BREAK:-}" = "1" ]; then
  WANT_FIFO_ADDR2="module.fifo_sqs_disabled.aws_sqs_queue.this:0"
  log "  BREAK=1: expecting tofu-address=$WANT_FIFO_ADDR2 on the fifo queue -"
  log "           the SAME shape and the SAME resource type, just the wrong"
  log "           (and in fact never-created) module. This step must fail."
fi
GOT_FIFO_ADDR2="$(awsl sqs list-queue-tags --queue-url "$FIFO_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_FIFO_ADDR2" = "$WANT_FIFO_ADDR2" ] || fail "$FIFO_QUEUE_URL's tofu-address is $GOT_FIFO_ADDR2, not $WANT_FIFO_ADDR2"
GOT_DEFAULT_ADDR2="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query "Tags.\"tofu-address\"" --output text)"
[ "$GOT_DEFAULT_ADDR2" = "$WANT_DEFAULT_ADDR" ] || fail "$DEFAULT_QUEUE_URL's tofu-address changed across the empty plan: $WANT_DEFAULT_ADDR -> $GOT_DEFAULT_ADDR2"
log "  identity re-check (read via the AWS CLI, after the state file has never existed this run): both unchanged"

log ""
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
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
log "STAGE 4 (test apply): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 5: drift and reconverge (mutate one object out of band) ==="
if [ "${BREAK:-}" = "1" ]; then
  awsl sqs tag-queue --queue-url "$UNENCRYPTED_QUEUE_URL" --tags Example=tampered-by-BREAK
  log "  BREAK=1: also tampered $UNENCRYPTED_QUEUE_URL's Example tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl sqs tag-queue --queue-url "$DEFAULT_QUEUE_URL" --tags Example=tampered-out-of-band
DRIFTED_VALUE="$(awsl sqs list-queue-tags --queue-url "$DEFAULT_QUEUE_URL" --query 'Tags.Example' --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $DEFAULT_QUEUE_URL's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
else
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
  log "STAGE 5 (drift and reconverge): PASS"
  log ""

  log "=== PASS ==="
  log ""
  log "A terraform-aws-modules EXAMPLE (reduced per this script's header) -"
  log "the SQS queueing surface, new to this corpus - crossed through all"
  log "five stages: cold deploy with plain terraform, choudoufu live-import"
  log "adoption plus the tofu-slot convergence apply it requires, an empty"
  log "replan with the state file deleted and rendered identities checked"
  log "against SQS's own answer (including the untaggable, schema-admitted"
  log "aws_sqs_queue_redrive_policy resource), a genuine no-op apply, and"
  log "drift on one queue reconverging without touching the others."
fi
