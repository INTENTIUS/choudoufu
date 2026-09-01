#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-crossref-related-agent recipe; run with: just demo-run corpus-crossref-related-agent)
# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/crossref-related-agent,
# structurally identical to demo-corpus-crossref-agent's four types
# (aws_cloudwatch_event_rule, aws_cloudwatch_event_target,
# aws_lambda_function, aws_lambda_permission) and the sibling
# demo-corpus-crossref-orcid-agent was explicitly flagged as untested for.
# It does NOT share crossref-orcid-agent's floci Lambda-handler bug (#287
# item 7): the deployment zip's one internal entry is named
# crossref-related-agent_runner.js, matching what main.tf's handler names,
# unlike crossref-orcid-agent's copy-paste leftover. The same four seeded
# reads (a Lambda role, a VPC, two subnets and a security group) and the
# same record_store delta for the Lambda's filename/source_code_hash/
# publish (#275) apply unchanged. Applied, state file deleted, replanned
# empty twice, all 4 rendered identities checked against the emulator's own
# answer. BREAK=1 corrupts the expected identity and the run must catch it
# in step 5 and nowhere else. Needs Docker, the AWS CLI and a populated
# .corpus; runs on its own port (4711) so it can run beside `just demo`.
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/mastino/prod-eu-west/services/crossref-related-agent.
#
# Four resources - aws_cloudwatch_event_rule, aws_cloudwatch_event_target,
# aws_lambda_function and aws_lambda_permission - DataCite's own cron job
# that pushes related-work metadata to Crossref. Same four types, same
# shape, as live/e2e/corpus-crossref-agent and
# live/e2e/corpus-crossref-orcid-agent, its two siblings under
# .corpus/mastino/prod-eu-west/services/. #274's comment thread named this
# estate explicitly as "untested whether it shares" crossref-orcid-agent's
# floci Lambda-handler bug (#287 item 7) - the question this script answers.
#
# IT DOES NOT SHARE THE BUG. crossref-orcid-agent's deployment zip has one
# internal entry misnamed crossref-agent_runner.js (a copy-paste leftover
# from the original crossref-agent service it was cloned from), while its
# main.tf names crossref-orcid-agent_runner.js as the handler - floci's
# Lambda CreateFunction validates that eagerly and refuses it. This
# estate's zip was checked with `unzip -l` before ever building a binary:
# its one entry is named crossref-related-agent_runner.js, byte-for-byte
# matching what main.tf's `handler = "crossref-related-agent_runner.handler"`
# expects - the rename was done correctly here, unlike its sibling. It
# CROSSES.
#
# THE DELTAS, identical in shape to crossref-agent's own:
#
#   1. terraform.tf declares `cloud { organization = "datacite-ng" ... }`
#      with no `hostname`, which makes `init` a hard error rather than the
#      flag-path's warning; the block goes, a live block replaces it (#268).
#
#   2. input.tf's provider block needs the emulator's flags (no
#      environment-variable form for skip_credentials_validation /
#      skip_metadata_api_check / s3_use_path_style).
#
#   3. FOUR seeded reads, identical in shape to crossref-agent's and
#      store-crawler-results's: a Lambda execution role named "lambda", a
#      VPC, two subnets in it (datacite-private, datacite-alt) and a
#      security group (datacite-private). The estate's own data blocks in
#      input.tf are kept exactly as written; only the var values naming
#      which objects to read point at what this run seeds.
#
#   4. A value for var.token (no default in the estate; not
#      identity-bearing).
#
#   5. `record_store "local" { path = ".tofu-records" }` added to the live
#      block. aws_lambda_function.crossref-related-agent is deployed
#      `filename = "crossref-related-agent_runner.js.zip"`, and filename,
#      source_code_hash and publish are pure inputs the AWS Lambda API
#      never returns from GetFunction - nothing the provider's Read can
#      repopulate. Without a record_store, issue #73's no-state model
#      re-derives prior state from a live read on every plan and proposes
#      the identical "+filename/+source_code_hash/+publish" update forever
#      (confirmed exactly on crossref-agent's identical shape; issue #275
#      built the fix, internal/live/projection/residue.go).
#
# THE TYPES, all four literal and client-named
# (internal/live/identity/table_generated.go: none carries ServerAssigned),
# so every rendered identity is reconstructible from the estate's own
# configuration with no discovery:
#
#   aws_cloudwatch_event_rule    default/crossref-related-agent
#   aws_cloudwatch_event_target  default/crossref-related-agent/crossref-related-agent
#   aws_lambda_function          crossref-related-agent
#   aws_lambda_permission        crossref-related-agent/AllowExecutionFromCloudWatch
#
# NO PROVIDER-VERSION OVERRIDE. `version = "~> 5"` resolves to 5.100.0, the
# release #269 documented as carrying no list resources at all - but every
# type above is client-named, so no instance ever calls a list operation
# and 5.100.0's gap never bites, the same result already confirmed on
# crossref-agent and store-crawler-results.
#
#   bash live/e2e/corpus-crossref-related-agent/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate
# (and its runner zip) is copied out to a temp directory first, same as
# every other corpus crossing. The record store is a directory beside the
# state file, not the state file itself - deleting terraform.tfstate by
# name leaves it untouched.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4711, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected identity string before
#                step 5, proving the identity assertion is load-bearing
#                rather than a grep that always matches.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads command output, an exit code, or the emulator's own answer through
# the AWS CLI.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/prod-eu-west/services/crossref-related-agent"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4711}"
FLOCI_NAME="choudoufu-corpus-crossref-related-agent-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mastino-crossref-related-agent-crossing"
REGION="eu-west-1"
FUNCTION_NAME="crossref-related-agent"
RULE_NAME="crossref-related-agent"

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
cp "$SRC/crossref-related-agent_runner.js.zip" "$EST/"
[ -f "$EST/main.tf" ] || fail "the estate copy is missing main.tf"
[ -f "$EST/crossref-related-agent_runner.js.zip" ] || fail "the estate copy is missing the runner zip"
log "  estate + runner zip copied out of .corpus into $EST"

# ── 1. the deltas ─────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="
perl -0pi -e 's/terraform \{\n  required_providers \{\n    aws = \{\n      source = "hashicorp\/aws"\n      version = "~> 5"\n    \}\n  \}\n\n  required_version = ">= 1\.6"\n\n  cloud \{\n    organization = "datacite-ng"\n\n    workspaces \{\n      name = "prod-eu-west-services-crossref-related-agent"\n    \}\n  \}\n\}/terraform {\n  required_providers {\n    aws = {\n      source  = "hashicorp\/aws"\n      version = "~> 5"\n    }\n  }\n\n  required_version = ">= 1.6"\n\n  # DELTA 1: was a Terraform Cloud block (#268).\n  live {\n    estate = "'"$ESTATE"'"\n\n    # DELTA 5: filename\/source_code_hash\/publish are pure inputs the AWS\n    # Lambda API never returns - see the header for why this estate needs it.\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }\n}/' "$EST/terraform.tf"
grep -q "estate = \"$ESTATE\"" "$EST/terraform.tf" || fail "DELTA 1 did not match terraform.tf - the corpus pin has moved"
grep -q 'record_store "local"' "$EST/terraform.tf" || fail "DELTA 5 did not write a record_store block"
log "  DELTA 1  cloud block removed, live block added              (#268)"
log "  DELTA 5  record_store \"local\" added                         (#275)"

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
# it and a security group. The estate's own data blocks are kept as
# written; only the var values naming which objects to read are seeded
# here.
ROLE_ARN="$(awsl iam create-role --role-name lambda \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  --query 'Role.Arn' --output text)"
[ -n "$ROLE_ARN" ] || fail "could not seed the lambda execution role"

VPC_ID="$(awsl ec2 create-vpc --cidr-block 10.92.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC_ID" ] || fail "could not seed the VPC"
SUBNET_PRIVATE_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.92.1.0/24 \
  --availability-zone "${REGION}a" --query 'Subnet.SubnetId' --output text)"
[ -n "$SUBNET_PRIVATE_ID" ] || fail "could not seed the private subnet"
SUBNET_ALT_ID="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.92.2.0/24 \
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

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: 4 instances ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 4 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the function back through the AWS CLI, never through choudoufu.
LIVE_ARN="$(awsl lambda get-function --function-name "$FUNCTION_NAME" --query 'Configuration.FunctionArn' --output text 2>/dev/null || true)"
[ -n "$LIVE_ARN" ] && [ "$LIVE_ARN" != "None" ] || fail "could not find the Lambda function through the AWS CLI"
log "  the function lives: $LIVE_ARN"
RULE_LIVE="$(awsl events describe-rule --name "$RULE_NAME" --query 'Name' --output text 2>/dev/null || true)"
[ "$RULE_LIVE" = "$RULE_NAME" ] || fail "could not find the CloudWatch event rule through the AWS CLI"
log "  the rule lives: $RULE_LIVE"

# The residue record itself, read as a file and never through choudoufu -
# proof that DELTA 5 actually did something on this real estate, not just
# that the plan happens to be empty. GitHub issue #364 unit A1 collapsed
# the once-separate "tofu-residue" namespace into the one per-instance
# envelope every record now lives in
# (internal/live/projection/record.go's RecordKeyPrefix); the residue
# member is what a residue record carries inside that shared file, checked
# below rather than by a directory that no longer exists.
RESIDUE_FILE="$(find "$EST/.tofu-records/tofu-records" -type f 2>/dev/null -exec grep -l '"residue":{' {} \; | head -1)"
[ -n "$RESIDUE_FILE" ] || fail "no record carrying a residue member was written under .tofu-records/tofu-records/ - DELTA 5 had no effect"
grep -q '"filename"' "$RESIDUE_FILE" || { cat "$RESIDUE_FILE"; fail "the residue record does not carry filename"; }
grep -q '"source_code_hash"' "$RESIDUE_FILE" || { cat "$RESIDUE_FILE"; fail "the residue record does not carry source_code_hash"; }
grep -q '"publish"' "$RESIDUE_FILE" || { cat "$RESIDUE_FILE"; fail "the residue record does not carry publish"; }
grep -qE '"runtime"|"handler"|"timeout"' "$RESIDUE_FILE" \
  && { cat "$RESIDUE_FILE"; fail "the residue record carries an attribute the provider DOES return (runtime/handler/timeout). A record that duplicates a value the cloud answers is a second opinion, and the plan would go empty over real drift."; }
log "  the residue record carries filename/source_code_hash/publish, and none of runtime/handler/timeout"

# ── 4. no state file, ever ──────────────────────────────────────────────────
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"
[ -d "$EST/.tofu-records" ] || fail "the record store did not survive deleting the state file"
log "=== 4. state file deleted (the record store is not the state file, and survives) ==="

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. live-plan, and the rendered identities read out of the run ==="
plan_into() {
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail "the plan proposes a resource change - DELTA 5's record_store should make this empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT_RULE='default/crossref-related-agent'
WANT_TARGET='default/crossref-related-agent/crossref-related-agent'
WANT_FUNC='crossref-related-agent'
WANT_PERM='crossref-related-agent/AllowExecutionFromCloudWatch'
if [ "${BREAK:-}" = "1" ]; then
  WANT_FUNC="wrong-function-name"
  log "  BREAK=1: expecting \"$WANT_FUNC\" for the function, the wrong name."
  log "           The plan above stayed empty. This step must fail."
fi
for WANT in "$WANT_RULE" "$WANT_TARGET" "$WANT_FUNC" "$WANT_PERM"; do
  grep -qF "from import identity \"$WANT\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$WANT\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "4" ] || fail "the run materialized $GOT_N distinct identities, expected 4"
log "  all 4 rendered identities asserted, and no others"

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing, and applying it adds nothing ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply was not a no-op"; }
STILL="$(awsl lambda get-function --function-name "$FUNCTION_NAME" --query 'Configuration.FunctionArn' --output text 2>/dev/null || echo None)"
[ "$STILL" = "$LIVE_ARN" ] || fail "expected the function to still be $LIVE_ARN afterward, got $STILL"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: nothing proposed, nothing added, all 4 resources still there, still no state file"

log ""
log "=== PASS ==="
log ""
log "DataCite's own crossref-related-agent job - a CloudWatch event rule"
log "and target driving a Lambda function, with its EventBridge invoke"
log "permission - applied against an emulator, lost its state file, and"
log "replanned empty twice. All 4 rendered identities were checked against"
log "the emulator's own answer. The Lambda's filename/source_code_hash/"
log "publish arguments - pure inputs the AWS API never returns - converge"
log "because of DELTA 5's record_store (#275), the same fix crossref-agent's"
log "identical shape already confirmed. Unlike crossref-orcid-agent"
log "(#287 item 7), this sibling's deployment zip's internal handler file"
log "name actually matches what main.tf names, so it does not hit that"
log "floci gap. Run again with BREAK=1: everything above step 5 still"
log "passes and step 5 goes red."
