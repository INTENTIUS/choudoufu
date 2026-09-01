#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-crossref-orcid-agent recipe; run with: just demo-run corpus-crossref-orcid-agent)
# Issue #274's attempt: .corpus/mastino/prod-eu-west/services/crossref-orcid-agent,
# structurally near-identical to crossref-agent (same four resource types,
# same onboarding shape) but named separately and permanently scheduled
# never to fire. Does NOT cross - BLOCKED BY FLOCI, not choudoufu, at the
# very first apply. The estate's deployment zip has one internal entry
# named crossref-agent_runner.js (a copy-paste leftover from the sibling
# service DataCite cloned this estate from), while main.tf's handler names
# crossref-orcid-agent_runner.js. floci's Lambda CreateFunction eagerly
# validates the handler file exists in the deployment package; real AWS
# Lambda does not - it only surfaces a missing/misnamed handler file at
# invoke time - so this estate would apply cleanly against real AWS and
# only misbehave if actually invoked, which its disabled schedule means it
# likely never has been. The script applies the estate byte for byte and
# pins the exact floci error rather than editing around it; it exits 0 when
# it reaches exactly that blocker. Filed as item 7 on issue #287. Needs
# Docker, the AWS CLI and a populated .corpus; runs on its own port (4703)
# so it can run beside `just demo`.
set -uo pipefail

# A real third-party estate run against a real emulator: issue #274's step
# 6, for .corpus/mastino/prod-eu-west/services/crossref-orcid-agent.
#
# Four resources - aws_cloudwatch_event_rule, aws_cloudwatch_event_target,
# aws_lambda_function and aws_lambda_permission - structurally
# near-identical to crossref-agent (live/e2e/corpus-crossref-agent), but
# named separately and, per the estate's own comment in main.tf,
# deliberately scheduled never to fire (`cron(0 0 1 1 ? 1970)` - a date
# already decades in the past). It passes live-check with zero refused
# sites and, until this script existed, had never touched a cloud. Named in
# #274 as one of its smallest untouched real corpus estates and explicitly
# passed over by an earlier session in favor of a larger one; this script
# is that estate's first real attempt.
#
# IT DOES NOT CROSS. It is BLOCKED BY FLOCI, not by choudoufu, at the very
# first apply - and pinned here rather than hidden, per this campaign's own
# discipline against fabricating a clean convergence.
#
# THE DELTAS. Steps 0-2 below all succeed exactly as they do for
# crossref-agent - the two estates share the same onboarding shape:
#
#   1. `cloud { organization = "datacite-ng" ... }` (#268), replaced with a
#      live block.
#   2. Emulator flags on the provider block.
#   3. FOUR seeded reads. The estate takes its execution role, its subnets
#      and its security group from data sources over a Lambda IAM role and
#      a VPC that already exist in DataCite's real account. All four are
#      seeded through the AWS CLI (ec2 + iam), never through choudoufu, and
#      the data blocks in the estate's own input.tf are kept exactly as
#      written - only the var values naming which VPC objects to read point
#      at what this run seeded.
#   4. A value for var.token (no default in the estate; not identity-bearing).
#   5. `record_store "local" { path = ".tofu-records" }` added to the live
#      block, for the same filename/source_code_hash/publish reason
#      documented in live/e2e/corpus-crossref-agent's header (#275). Never
#      exercised here - the apply itself fails before any record would be
#      written.
#
# THE BLOCKER, found at step 3's apply: floci's Lambda CreateFunction
# rejects this estate's own deployment package with
#
#   InvalidParameterValueException: Handler file
#   'crossref-orcid-agent_runner' not found in deployment package
#
# ...because the zip's ONE internal entry is literally named
# `crossref-agent_runner.js` - byte-identical to the file sitting beside it
# in .corpus, and clearly a copy-paste leftover from the sibling
# crossref-agent service DataCite cloned this estate from - while main.tf's
# `handler = "crossref-orcid-agent_runner.handler"` names the renamed
# function. The outer zip filename was renamed
# (crossref-orcid-agent_runner.js.zip); the file inside it was not.
#
# THIS IS A FLOCI GAP, not an authoring bug that would also fail against
# real AWS. AWS Lambda's CreateFunction does NOT validate that the handler
# file exists inside a .zip deployment package - Lambda accepts the package
# as-is and only discovers a missing/misnamed handler file at INVOKE time,
# with an import/module error (a widely documented AWS Lambda behavior,
# distinct from container-image deployments, which validate at push time).
# floci's LambdaService.extractZipCodeBytes performs this check eagerly, at
# CreateFunction, for every file-based runtime
# (src/main/java/io/github/hectorvent/floci/services/lambda/LambdaService.java,
# ~line 1407 "For file-based runtimes, verify handler file exists" - present
# since floci's very first commit in this fork's history, so inherited
# behavior, not a regression). Real AWS would accept this estate's apply
# exactly as authored; against real AWS the mismatch would only ever
# surface if the function were actually invoked - and per the estate's own
# main.tf comment, its schedule is permanently disabled, so it is entirely
# plausible DataCite's own production apply of this estate has silently
# carried this mismatch with no operational symptom. This script does not
# fix the zip or the estate to route around that - #274's discipline is to
# apply what was authored, byte for byte, and report what floci does with
# it. Filed as item 7 on issue #287 ("floci fork: remaining emulator gaps
# blocking crossings").
#
# THE TYPES, all four literal and client-named
# (internal/live/identity/table_generated.go) - never reached, since the
# apply that would materialize them fails first:
#
#   aws_cloudwatch_event_rule    default/crossref-orcid-agent
#   aws_cloudwatch_event_target  default/crossref-orcid-agent/crossref-orcid-agent
#   aws_lambda_function          crossref-orcid-agent
#   aws_lambda_permission        crossref-orcid-agent/AllowExecutionFromCloudWatch
#
#   bash live/e2e/corpus-crossref-orcid-agent/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate
# (and its runner zip) is copied out to a temp directory first, same as
# every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4703, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 when the run reaches exactly the pinned floci blocker and
# nothing else has moved; non-zero if anything earlier fails, if the apply
# unexpectedly SUCCEEDS (which would mean floci's handler check has been
# relaxed to match AWS, and this script should be rewritten into a real
# crossing), or if the apply fails for a different reason than the one
# pinned above.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/crossref-orcid-agent"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4703}"
FLOCI_NAME="choudoufu-corpus-crossref-orcid-agent-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-crossref-orcid-agent-crossing"
REGION="eu-west-1"

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
[ -d "$SRC" ] || fail "$SRC is missing - run 'just corpus-fetch' first"

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

mkdir -p "$EST"
cp "$SRC"/*.tf "$EST/"
cp "$SRC/crossref-orcid-agent_runner.js.zip" "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
[ -f "$EST/crossref-orcid-agent_runner.js.zip" ] || fail "the estate copy is missing the runner zip"
log "  estate + runner zip copied out of .corpus into $EST"

# ── 1. the deltas ─────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="
perl -0pi -e 's/terraform \{\n  required_providers \{\n    aws = \{\n      source = "hashicorp\/aws"\n      version = "~> 5"\n    \}\n  \}\n\n  required_version = ">= 1\.6"\n\n  cloud \{\n    organization = "datacite-ng"\n\n    workspaces \{\n      name = "prod-eu-west-services-crossref-orcid-agent"\n    \}\n\n  \}\n\}/terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      version = "~> 5"\n    }\n  }\n\n  required_version = ">= 1.6"\n\n  # DELTA 1: was a Terraform Cloud block (#268).\n  live {\n    estate = "'"$ESTATE"'"\n\n    # DELTA 5: filename\/source_code_hash\/publish are pure inputs the AWS\n    # Lambda API never returns - see the header for why this estate needs it.\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$EST/terraform.tf"
grep -q "estate = \"$ESTATE\"" "$EST/terraform.tf" || fail "DELTA 1 did not match terraform.tf - the corpus pin has moved"
grep -q 'record_store "local"' "$EST/terraform.tf" || fail "DELTA 5 did not write a record_store block"
log "  DELTA 1  cloud block removed, live block added              (#268)"
log "  DELTA 5  record_store \"local\" added                         (#275, unexercised - see below)"

perl -0pi -e 's/(provider "aws" \{\n  access_key = var\.access_key\n  secret_key = var\.secret_key\n  region     = var\.region\n)\}/$1\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  s3_use_path_style            = true\n}/' "$EST/input.tf"
grep -q 's3_use_path_style' "$EST/input.tf" || fail "DELTA 2 did not match input.tf - the corpus pin has moved"
log "  DELTA 2  emulator flags on the provider                     (emulator)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"lambda"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"lambda"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (lambda) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# DELTA 3, four seeded reads: a Lambda execution role, a VPC, two subnets in
# it and a security group. The estate's own data blocks are kept as written;
# only the var values naming which objects to read are seeded here.
ROLE_ARN="$(awsl iam create-role --role-name lambda \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  --query 'Role.Arn' --output text)"
[ -n "$ROLE_ARN" ] || fail "could not seed the lambda execution role"

VPC_ID="$(awsl ec2 create-vpc --cidr-block 10.90.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC_ID" ] || fail "could not seed the VPC"
SUBNET_PRIVATE_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.90.1.0/24 \
  --availability-zone "${REGION}a" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_PRIVATE_ID" ] || fail "could not seed the private subnet"
SUBNET_ALT_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.90.2.0/24 \
  --availability-zone "${REGION}b" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_ALT_ID" ] || fail "could not seed the alt subnet"
SG_ID="$(awsl ec2 create-security-group --vpc-id "$VPC_ID" --group-name datacite-private \
  --description "datacite-private" --query 'GroupId' --output text)"
[ -n "$SG_ID" ] || fail "could not seed the security group"
log "  DELTA 3  lambda role + VPC + 2 subnets + 1 SG seeded         (seeded reads)"

cat > "$EST/crossing.auto.tfvars" <<EOF
access_key = "test"
secret_key = "test"
security_group_id = "$SG_ID"
subnet_datacite-private_id = "$SUBNET_PRIVATE_ID"
subnet_datacite-alt_id = "$SUBNET_ALT_ID"
token = "test-token"
EOF
log "  DELTA 4  var values for the seeded objects + var.token       (onboarding)"

# ── 3. the apply: blocked by floci, not by choudoufu ────────────────────────
log "=== 3. init and apply: pinned to floci's handler-file check ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
RC=$?

if [ "$RC" -eq 0 ]; then
  fail "the apply SUCCEEDED, which this script does not expect. That is GOOD NEWS: floci's Lambda CreateFunction no longer eagerly validates the handler file against the deployment package (or the corpus pin's zip contents have changed). Rewrite this script into a real crossing (delete the state file, live-plan empty twice, BREAK=1 negative control) per every other script in live/e2e."
fi

grep -qF "Handler file 'crossref-orcid-agent_runner' not found in deployment package" <<< "$APPLY_OUT" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the apply failed, but not with the pinned handler-file error. Something else about the corpus pin or this fork has moved - read the errors above."
}
log "  apply failed exactly as pinned:"
log "    InvalidParameterValueException: Handler file"
log "    'crossref-orcid-agent_runner' not found in deployment package"
log ""
log "  The zip's one internal entry is named crossref-agent_runner.js (a"
log "  copy-paste leftover from the sibling crossref-agent service), not"
log "  crossref-orcid-agent_runner.js as main.tf's handler names. floci"
log "  validates this at CreateFunction time; real AWS Lambda does not -"
log "  it only surfaces a missing/misnamed handler file at invoke time."
log "  DataCite's estate is applied here exactly as authored; this script"
log "  does not edit the zip or the estate to route around the mismatch."

log ""
log "=== BLOCKED (floci, not choudoufu) ==="
log ""
log "Steps 0-2 above ran clean: the estate onboards with the same shape as"
log "crossref-agent (cloud block -> live block, emulator provider flags,"
log "four seeded VPC/IAM reads, a var.token value). The apply itself never"
log "completes, so nothing downstream - state deletion, live-plan, the"
log "rendered identities, DELTA 5's record_store - was ever reached. Filed"
log "as item 7 on issue #287 (floci fork: remaining emulator gaps blocking"
log "crossings)."
