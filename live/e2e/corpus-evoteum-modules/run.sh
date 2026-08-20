#!/usr/bin/env bash
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for evoteum/tofu-modules (live/corpus-manifest.json, pinned by commit
# 7e8764035c50d1cb2a6ac04636a9f85ba6708d39) - the EIGHTH estate in the
# OpenTofu-native lane, and the second sourced from a commercial vendor's own
# repository rather than a module registry, a personal monorepo or a
# single-maintainer accelerator.
#
# WHY THIS IS OPENTOFU-NATIVE, not merely OpenTofu-compatible. Four
# independent pieces of evidence, all checkable at the pinned commit:
#   1. The repository describes itself as OpenTofu's, not Terraform's. Its
#      GitHub description is "Modules for OpenTofu"; its README opens
#      "Welcome to the **OpenTofu Modules Repository**! This repository
#      contains reusable **OpenTofu modules**..." and says they are
#      "designed to be used with OpenTofu". It never claims compatibility
#      with both, and its own usage example is a `tofu`-fenced code block.
#   2. Every configuration file in the repository carries the .tofu
#      extension. There is not one .tf file anywhere in the pinned tree
#      (130 blobs) - asserted below, over the WHOLE clone, not just the two
#      directories this crossing runs.
#   3. .pre-commit-config.yaml installs tofuutils/pre-commit-opentofu and
#      runs its tofu_validate and tofu_fmt hooks, plus a tfsort hook scoped
#      `files: (variables|outputs)\.tofu$`. No Terraform hook is configured
#      at all.
#   4. The one module in the repository that ships unit tests writes them as
#      aws/app_runner/tests/app_runner.tofutest.hcl and .../iam.tofutest.hcl.
#      `.tofutest.hcl` is a test-file extension OpenTofu recognises and
#      Terraform does not - the only piece of evidence in this lane so far
#      that Terraform could not even parse.
#
# It is real production code rather than a demonstration: the same
# organisation's estate-config repository calls `aws/bucket` from this
# repository, over a setproduct()-built for_each map, to provision its own
# OpenTofu state buckets. Evoteum Ltd (evoteum.com) is a real company with 45
# public repositories and organisation-wide pushes through 2026-08-20; this
# repository's own last push is 2026-06-19 and the two modules crossed here
# were last touched 2025-05-15 - stable, not abandoned, and the honest
# statement of it is "a maintained repository whose networking and dynamodb
# modules have not needed a change in a year", not "actively developed".
#
# THE SCOPING DECISION. The repository ships eleven AWS modules. Two are
# crossed; the other nine are excluded, each for a stated reason:
#   - aws/bucket derives its bucket NAME from
#     `random_password.bucket_suffix.result`. random_password is
#     secret-bearing, and an identity argument that folds a secret random is
#     the wall HANDOFF's item -2 already records for corpus-lambda-simple's
#     random_pet. Crossing it would re-find a known block rather than
#     measure anything new.
#   - aws/certificate is aws_acm_certificate with DNS validation, which
#     needs a real hosted zone and a validation round trip.
#   - aws/cloudfront, aws/load_balancer, aws/load_balancer_domain_binding
#     and aws/load_balancer_target_group all take a vpc_id/certificate/
#     listener that must already exist, i.e. they are leaves of a stack this
#     crossing would have to stand up first.
#   - aws/app_runner and aws/ecs_service both need a container image in a
#     registry before they will apply.
#   - aws/ecs_cluster is self-contained but its second resource,
#     aws_ecs_cluster_capacity_providers, is recorded `unverified` for the
#     pinned floci digest in live/floci-capabilities.json, so a failure
#     there would be ambiguous between a choudoufu gap and an emulator gap.
#   aws/networking and aws/dynamodb are the two that are completely
#   self-contained: no remote state, no pre-existing infrastructure, no
#   secret-bearing random, and between them they exercise a VPC stack (the
#   family whose associations carry no tag at all and must derive their
#   identity from tagged parents) and a client-named table.
#
# WHAT THIS SLICE CONTRIBUTES that the seven earlier OpenTofu-native
# crossings did not:
#   - `for_each = { for idx, cidr in var.public_subnets : cidr =>
#     local.selected_availability_zones[idx] }` - a for-expression that
#     builds the expansion map by indexing a SECOND collection positionally.
#     The keys are static (a list variable's default); the values come from
#     `data.aws_availability_zones`, so the key set is knowable while the
#     values are not, which is exactly the split identity resolution has to
#     get right.
#   - `for_each = aws_subnet.public` on aws_route_table_association - an
#     expansion driven by another RESOURCE's instance map, over the
#     untaggable association family whose identity is a composite of two
#     tagged parents. This is the invariant's "derived-from-tagged" bucket
#     under a for_each rather than a single block.
#   - two `dynamic` blocks over map variables (aws_dynamodb_table's
#     `attribute` and `global_secondary_index`), with the module's own
#     `upper()` normalising the caller's lowercase attribute types.
#   - a data source read (`data.aws_availability_zones.available`) whose
#     result is consumed by an identity-adjacent argument.
#   - and the one this crossing found by getting it wrong first: a for_each
#     key that CANNOT be written into an AWS tag value as it stands. An AWS
#     tag value admits only [A-Za-z0-9 _.:/=+@-]; the address
#     `module.networking.aws_subnet.public["10.0.101.0/24"]` carries
#     brackets, quotes and dots that are not all in it. internal/live/markers
#     escapes it to `module.networking.aws_subnet.public:10@d0@d101@d0/24`,
#     and this is the first estate in either lane whose expansion keys make
#     that layer load-bearing - every earlier crossing's for_each keys were
#     either absent or already inside the charset. The three expected marker
#     strings are written out BY HAND below from the rule documented at
#     internal/live/markers/markers.go:196, never computed by the function
#     that writes them, and the script also asserts each one is inside the
#     AWS charset - an escaping that produced an illegal value would fail on
#     real AWS while passing against a lenient emulator.
#
# THE ONE DELTA, and it is a provider pin, not a resource-shape change.
# aws/networking/main.tofu declares `version = "~> 5.0"`, which excludes the
# 6.59.0 release this fork's identity tables are derived at, so `tofu init`
# refuses the root's own `= 6.59.0` outright. That single line is rewritten
# to `= 6.59.0` - the same substitution corpus-xancloud-iac makes for the
# same reason - and the script asserts the diff against the pinned commit is
# EXACTLY that one line and nothing else. aws/dynamodb declares no provider
# requirements at all and is copied byte-identical with no edit whatsoever.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                    the two real modules - the honest proof they are real
#                    and buildable, and the source of genuinely unmarked
#                    live infra for stage 2.
#   2. MIGRATE       `choudoufu live-import -approve` against that cold
#                    state; markers re-read through the AWS CLI directly.
#   3. TEST PLAN     delete the state file, `choudoufu live-plan`, assert
#                    the plan is EMPTY, and re-assert the rendered
#                    identities against the AWS CLI's own answer - including
#                    the untaggable route-table associations, which have no
#                    marker to read and must re-derive from their parents.
#   4. TEST APPLY    apply the empty plan; assert a genuine no-op by
#                    comparing the estate's tagged-object count.
#   5. DRIFT AND     mutate one live object's tag out of band, replan,
#      RECONVERGE    assert exactly that one object is proposed and fixed.
#
# All five stages pass, for real, against floci at the digest pinned in
# live/floci-image. Nothing in this estate refuses: live-plan's diagnostic
# surface is empty, not merely small.
#
# Two independent negative controls, deliberately on separate switches so
# BOTH are reachable in a real run (a single BREAK that fails fast at stage 2
# leaves the stage-5 control never exercised):
#   BREAK=1        corrupts stage 2's expected tofu-address. Must fail at
#                  stage 2.
#   BREAK_STAGE5=1 tampers a SECOND object before stage 5's drift plan. Must
#                  fail stage 5's exactly-one-object assertion.
#
#   bash live/e2e/corpus-evoteum-modules/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1.
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4730, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion.
#   BREAK_STAGE5  set to 1 to tamper a second object before stage 5.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/evoteum-tofu-modules"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4730}"
FLOCI_NAME="choudoufu-corpus-evoteum-modules-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
REGION="us-west-2"
ESTATE_NAME="evoteum-modules-crossing"

# The module inputs. Every one of these is a variable the modules themselves
# declare and document; this script supplies no argument they do not define.
PROJECT_ID="evtx"
PROJECT_NAME="evoteum-platform"
ENVIRONMENT="development"
TABLE_SHORT_NAME="sessions"

# Derived by the modules' own expressions, restated here so the assertions
# below read as values rather than as string building:
#   aws/networking/locals.tofu  resource_prefix = "${project_id}-${environment}"
#   aws/dynamodb/main.tofu      table_name = lower("${project_id}-${environment}-${table_name}")
RESOURCE_PREFIX="${PROJECT_ID}-${ENVIRONMENT}"
VPC_NAME="${RESOURCE_PREFIX}-vpc"
TABLE_NAME="${PROJECT_ID}-${ENVIRONMENT}-${TABLE_SHORT_NAME}"
# aws/networking/variables.tofu's own public_subnets default, in its own order.
SUBNET_CIDRS=("10.0.101.0/24" "10.0.102.0/24" "10.0.103.0/24")

# The marker a for_each instance carries is NOT its address verbatim, and
# this crossing is the first in the lane whose expansion keys make that
# visible. An AWS tag value admits only [A-Za-z0-9 _.:/=+@-]; the address
# `module.networking.aws_subnet.public["10.0.101.0/24"]` carries brackets and
# quotes that are not in that set, so internal/live/markers' EscapeAddress
# renders `res["key"]` as `res:<escaped key>`, and inside the key every "."
# becomes "@d" (the rule is documented at internal/live/markers/markers.go:196
# and the "." substitution is at :237).
#
# These three values are written out BY HAND from that documented rule, not
# computed by the code that writes them. That is the point: a marker
# assertion whose expected value came from the same function under test can
# only ever agree with itself.
SUBNET_MARKERS=(
  'module.networking.aws_subnet.public:10@d0@d101@d0/24'
  'module.networking.aws_subnet.public:10@d0@d102@d0/24'
  'module.networking.aws_subnet.public:10@d0@d103@d0/24'
)

# This script runs two `tofu init`s (plain and estate), each of which would
# otherwise re-download the ~500MB AWS provider into its own scratch
# directory. Point both at OpenTofu's own conventional shared plugin cache so
# only the first one can ever pay for a download; an operator who already
# exports TF_PLUGIN_CACHE_DIR keeps theirs.
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
mkdir -p "$TF_PLUGIN_CACHE_DIR"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# marker_filter <marker>: a --filters argument matching tofu-address exactly,
# in the CLI's JSON form rather than its Name=,Values= shorthand. The
# shorthand parser is the fragile one - it splits on "," and "=", both of
# which are legal AWS tag-value characters - so every marker lookup here goes
# through JSON and none of them depends on what the shorthand happens to
# tolerate.
marker_filter() {
  printf '[{"Name":"tag:tofu-address","Values":["%s"]}]' "${1//\"/\\\"}"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1"
[ -f "$SRC/aws/networking/network.tofu" ] \
  || fail "$SRC/aws/networking/network.tofu is missing - fetch evoteum/tofu-modules at the pin in live/corpus-manifest.json first"
log "  cold deploy binary: tofu ($(tofu version | head -1))"

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

# OpenTofu-native evidence (2), asserted over the WHOLE pinned clone rather
# than described in prose only: if a future pin introduces a single .tf file
# anywhere, this crossing's central sourcing claim stops being true and it
# should say so loudly rather than keep running.
STRAY_TF="$(find "$SRC" -name '*.tf' -not -path '*/.git/*' -print -quit)"
[ -z "$STRAY_TF" ] \
  || fail "$STRAY_TF exists - the pinned repository is no longer .tofu-only, which is this crossing's own OpenTofu-native evidence"
N_TOFU="$(find "$SRC" -name '*.tofu' -not -path '*/.git/*' | wc -l | tr -d ' ')"
log "  pinned clone is .tofu-only: $N_TOFU .tofu files, 0 .tf files"
# Evidence (3) and (4), same treatment.
grep -q 'tofuutils/pre-commit-opentofu' "$SRC/.pre-commit-config.yaml" \
  || fail "the pinned .pre-commit-config.yaml no longer installs tofuutils/pre-commit-opentofu"
grep -qi 'terraform' "$SRC/.pre-commit-config.yaml" \
  && fail "the pinned .pre-commit-config.yaml now mentions terraform - re-read it before trusting evidence (3)"
[ -f "$SRC/aws/app_runner/tests/app_runner.tofutest.hcl" ] \
  || fail "aws/app_runner/tests/app_runner.tofutest.hcl is gone - evidence (4) no longer holds"
log "  pre-commit runs tofu_validate/tofu_fmt only; app_runner ships .tofutest.hcl tests"

# copy_modules <destdir>: the two real modules, .tofu extension and all, plus
# the single documented provider-pin delta on aws/networking/main.tofu.
copy_modules() {
  local dest="$1"
  mkdir -p "$dest"
  cp -R "$SRC/aws/networking" "$dest/networking"
  cp -R "$SRC/aws/dynamodb" "$dest/dynamodb"

  # Byte-identical BEFORE the pin edit, so the edit is the only thing that
  # can account for a later difference.
  diff -rq "$SRC/aws/networking" "$dest/networking" >/dev/null \
    || fail "aws/networking differs from the pinned commit before any edit"
  diff -rq "$SRC/aws/dynamodb" "$dest/dynamodb" >/dev/null \
    || fail "aws/dynamodb differs from the pinned commit"

  # THE DELTA: one line, asserted rather than assumed.
  grep -qF 'version = "~> 5.0"' "$dest/networking/main.tofu" \
    || fail "aws/networking/main.tofu no longer carries version = \"~> 5.0\" - re-read the pin before applying this crossing's provider-pin delta"
  sed -i.bak 's|version = "~> 5.0"|version = "= 6.59.0"|' "$dest/networking/main.tofu"
  rm -f "$dest/networking/main.tofu.bak"

  local delta
  delta="$(diff "$SRC/aws/networking/main.tofu" "$dest/networking/main.tofu" | grep -E '^[<>]' | sed 's/^..//;s/^ *//')"
  [ "$delta" = 'version = "~> 5.0"
version = "= 6.59.0"' ] \
    || { printf '%s\n' "$delta"; fail "the provider-pin delta touched more than the one version line"; }
  # And nothing else in either module moved.
  diff -rq "$SRC/aws/dynamodb" "$dest/dynamodb" >/dev/null \
    || fail "aws/dynamodb changed after the networking pin edit"
  local other
  other="$(diff -rq "$SRC/aws/networking" "$dest/networking" | grep -v 'main\.tofu' || true)"
  [ -z "$other" ] || { printf '%s\n' "$other"; fail "a file other than aws/networking/main.tofu differs from the pin"; }
}

# write_root <destdir> <live_block>: this crossing's own root wiring. Every
# module argument below is a variable the modules themselves declare; the
# provider block is floci's connection flags.
write_root() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tofu" <<EOF
terraform {
  required_version = ">= 1.11"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
$live_block
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

module "networking" {
  source       = "./networking"
  project_id   = "$PROJECT_ID"
  project_name = "$PROJECT_NAME"
  environment  = "$ENVIRONMENT"
}

module "sessions_table" {
  source       = "./dynamodb"
  project_id   = "$PROJECT_ID"
  project_name = "$PROJECT_NAME"
  environment  = "$ENVIRONMENT"
  table_name   = "$TABLE_SHORT_NAME"
  hash_key     = "pk"
  range_key    = "sk"

  # Lowercase on purpose: the module's own validation permits it and its own
  # upper() normalises it, which is the abstraction its README claims to
  # provide. Asserted on the live table below.
  attributes = {
    pk     = "s"
    sk     = "s"
    status = "s"
  }

  global_secondary_indexes = {
    by_status = {
      hash_key        = "status"
      range_key       = "sk"
      projection_type = "all"
    }
  }
}
EOF
}

LIVE_BLOCK='
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'

copy_modules "$PLAIN"
write_root "$PLAIN" ""
log "  aws/networking and aws/dynamodb copied out of .corpus/evoteum-tofu-modules into $PLAIN"
log "  DELTA confirmed: exactly one line differs from the pin (aws/networking/main.tofu's aws provider constraint); aws/dynamodb is byte-identical"

copy_modules "$ESTATE"
write_root "$ESTATE" "$LIVE_BLOCK"
log "  estate copy written to $ESTATE (stages 2-5: choudoufu, live block added)"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"dynamodb"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"dynamodb"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (dynamodb) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 1: cold deploy (plain tofu apply, the real modules) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 10 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 10 resource instances (1 vpc + 3 subnets + 1 igw + 1 route table + 3 associations + 1 dynamodb table)"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

# The three-instance expansions are the point of this slice, so assert the
# instance COUNT separately from the total above: a for_each that quietly
# collapsed to one key would still leave the total wrong in a way that reads
# as an unrelated failure.
for cidr in "${SUBNET_CIDRS[@]}"; do
  grep -qF "module.networking.aws_subnet.public[\"$cidr\"]: Creation complete" <<< "$COLD_OUT" \
    || fail "aws_subnet.public[\"$cidr\"] was not created - the CIDR-keyed for_each did not expand as its variable default says"
  grep -qF "module.networking.aws_route_table_association.public[\"$cidr\"]: Creation complete" <<< "$COLD_OUT" \
    || fail "aws_route_table_association.public[\"$cidr\"] was not created - the resource-driven for_each did not expand"
done
log "  both for_each expansions produced exactly the 3 keys the module's public_subnets default names"

# The module's own upper() ran: the caller passed lowercase "s".
HASH_TYPE="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query "Table.AttributeDefinitions[?AttributeName=='pk'].AttributeType | [0]" --output text)"
[ "$HASH_TYPE" = "S" ] || fail "the live table's pk attribute type is \"$HASH_TYPE\", not \"S\" - the module's own upper() did not run"
GSI_NAME="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.GlobalSecondaryIndexes[0].IndexName' --output text)"
[ "$GSI_NAME" = "by_status" ] || fail "the live table's first GSI is \"$GSI_NAME\", not \"by_status\" - the dynamic global_secondary_index block did not render"
log "  dynamic blocks rendered: pk is type S (module's upper() over the caller's \"s\"), GSI by_status present"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 2: choudoufu live-import ==="
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "7 of 10 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 7 of 10 as eligible (vpc, 3 subnets, igw, route table, dynamodb table)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE (3)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 3 UNTAGGABLE resources (the three route table associations - the family that carries no tag and derives its identity from tagged parents)"
grep -qE '^(UNADMITTED_TYPE|FAILED) \(' <<< "$IMPORT_OUT" \
  && fail "live-import reported an UNADMITTED_TYPE or FAILED bucket this crossing does not expect - re-read the whole output before changing the assertions above"
log "  7 of 10 verified against the live system; 3 correctly UNTAGGABLE; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "7 resource(s) newly stamped, 0 already stamped, 0 failed, 3 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 7 of 10 resources cleanly"; }
log "  7 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_VPC_ADDR="module.networking.aws_vpc.main"
WANT_TABLE_ADDR="module.sessions_table.aws_dynamodb_table.this"
if [ "${BREAK:-}" = "1" ]; then
  WANT_TABLE_ADDR="module.sessions_table.aws_dynamodb_table.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the DynamoDB table on purpose - this check must fail"
fi

# The VPC is found BY its marker, not by name: the query below is the whole
# recovery mechanism for a taggable server-assigned type.
VPC_ID="$(awsl ec2 describe-vpcs --filters "$(marker_filter "$WANT_VPC_ADDR")" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] \
  || fail "no VPC carries tofu-address=$WANT_VPC_ADDR"
GOT_VPC_ESTATE="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_VPC_ESTATE" = "$ESTATE_NAME" ] || fail "the VPC carries tofu-estate=$GOT_VPC_ESTATE, not $ESTATE_NAME"
GOT_VPC_NAME="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='Name'].Value | [0]" --output text)"
[ "$GOT_VPC_NAME" = "$VPC_NAME" ] \
  || fail "the module's own Name tag is \"$GOT_VPC_NAME\" after stamping, not \"$VPC_NAME\" - the marker replaced the tag set instead of merging into it"
log "  vpc    $VPC_ID -> tofu-address=$WANT_VPC_ADDR tofu-estate=$GOT_VPC_ESTATE, module's own Name=$GOT_VPC_NAME survived"

# Each subnet's marker carries its own for_each key. A single wrong key here
# would be invisible to any count: three subnets, three distinct addresses.
# Print every live subnet's own tofu-address tag verbatim before asserting
# anything about it, so a marker that is merely DIFFERENT from what this
# script expects is legible in the log rather than reported only as an
# absence.
log "  --- every live subnet's own tofu-address tag, read verbatim ---"
awsl ec2 describe-subnets --output json | python3 -c '
import json,sys
for s in json.load(sys.stdin)["Subnets"]:
    t={x["Key"]:x["Value"] for x in s.get("Tags",[])}
    print("    %s %-15s tofu-address=%r" % (s["SubnetId"], s["CidrBlock"], t.get("tofu-address")))
'
SUBNET_IDS=()
for i in 0 1 2; do
  cidr="${SUBNET_CIDRS[$i]}"
  want="${SUBNET_MARKERS[$i]}"
  # The escaped marker must sit inside the AWS tag-value charset - that is
  # the entire reason the escaping exists, and an escaping that produced an
  # illegal value would fail at stamp time on real AWS while passing against
  # a lenient emulator.
  [[ "$want" =~ ^[A-Za-z0-9\ _.:/=+@-]+$ ]] \
    || fail "the expected marker $want is not inside the AWS tag-value charset"
  sid="$(awsl ec2 describe-subnets --filters "$(marker_filter "$want")" --query 'Subnets[0].SubnetId' --output text)"
  [ -n "$sid" ] && [ "$sid" != "None" ] || fail "no subnet carries tofu-address=$want"
  live_cidr="$(awsl ec2 describe-subnets --subnet-ids "$sid" --query 'Subnets[0].CidrBlock' --output text)"
  [ "$live_cidr" = "$cidr" ] \
    || fail "the subnet marked $want has CIDR $live_cidr - the for_each key and the live object disagree"
  SUBNET_IDS+=("$sid")
  log "  subnet $sid -> tofu-address=$want (live CIDR $live_cidr matches the key that marker encodes)"
done

RT_ID="$(awsl ec2 describe-route-tables --filters "$(marker_filter "module.networking.aws_route_table.public")" --query 'RouteTables[0].RouteTableId' --output text)"
[ -n "$RT_ID" ] && [ "$RT_ID" != "None" ] || fail "no route table carries tofu-address=module.networking.aws_route_table.public"
log "  rtable $RT_ID -> tofu-address=module.networking.aws_route_table.public"

TABLE_ARN="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)"
GOT_TABLE_ADDR="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_TABLE_ADDR" = "$WANT_TABLE_ADDR" ] || fail "the DynamoDB table carries tofu-address=$GOT_TABLE_ADDR, not $WANT_TABLE_ADDR"
log "  table  $TABLE_NAME -> tofu-address=$GOT_TABLE_ADDR"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the table's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities asserted
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #|^Error: ' <<< "$PLAN_OUT"; fail "live-plan is not empty"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"

# Re-assert identities directly against the live objects, after the local
# state file was deleted - any answer below can only have come from the
# marker on the live object itself, or, for the associations, from a
# composite re-derived out of two markers.
VPC_ID2="$(awsl ec2 describe-vpcs --filters "$(marker_filter "$WANT_VPC_ADDR")" --query 'Vpcs[0].VpcId' --output text)"
[ "$VPC_ID2" = "$VPC_ID" ] || fail "the VPC bound to $WANT_VPC_ADDR changed across the empty plan: $VPC_ID -> $VPC_ID2"
TABLE_ADDR2="$(awsl dynamodb list-tags-of-resource --resource-arn "$TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$TABLE_ADDR2" = "$WANT_TABLE_ADDR" ] || fail "the table's tofu-address changed across the empty plan: $WANT_TABLE_ADDR -> $TABLE_ADDR2"

# The three route table associations have NO tag to re-read - they are the
# untaggable, derived-from-tagged family. Their identity is the composite
# {route_table_id}/{subnet_id}, and the only way the plan above could have
# come back empty is if that composite resolved to exactly these live
# objects. Confirm the live composites independently.
for i in 0 1 2; do
  sid="${SUBNET_IDS[$i]}"
  assoc="$(awsl ec2 describe-route-tables --route-table-ids "$RT_ID" \
    --query "RouteTables[0].Associations[?SubnetId=='$sid'].RouteTableAssociationId | [0]" --output text)"
  [ -n "$assoc" ] && [ "$assoc" != "None" ] \
    || fail "no live association joins route table $RT_ID to subnet $sid, yet the plan is empty - the composite identity resolved to something else"
  log "  association $assoc = $RT_ID / $sid (untaggable; re-derived from two marked parents)"
done
log "  identity re-check: VPC and table markers unchanged; all three untaggable associations present as their composite says"

log ""
log "STAGE 3 (test plan): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
log "=== STAGE 5: drift and reconverge (mutate one object's tag out of band) ==="

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  awsl ec2 create-tags --resources "${SUBNET_IDS[0]}" --tags Key=Environment,Value=tampered-by-BREAK >/dev/null
  log "  BREAK_STAGE5=1: also tampered subnet ${SUBNET_IDS[0]}'s Environment tag - stage 5 must now"
  log "                  see TWO drifted objects and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='Name'].Value | [0]" --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated $VPC_ID's Name tag to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "                  one - the single-object assertion and reconverge apply below are skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "$WANT_VPC_ADDR" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the VPC"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='Name'].Value | [0]" --output text)"
  [ "$FIXED_VALUE" = "$VPC_NAME" ] \
    || fail "the VPC's Name tag is \"$FIXED_VALUE\" after reconverging, not \"$VPC_NAME\""
  STILL_MARKED="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$STILL_MARKED" = "$WANT_VPC_ADDR" ] \
    || fail "the VPC's tofu-address is \"$STILL_MARKED\" after the reconverge apply - the marker did not survive an incremental tag update"
  log "  reconverged: $VPC_ID's Name tag is back to \"$VPC_NAME\" and its marker survived, both read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
log ""

log "=== PASS: all five stages, real, against evoteum/tofu-modules' own    ==="
log "=== aws/networking and aws/dynamodb modules, .tofu throughout, with   ==="
log "=== one documented provider-pin line as the only delta from the pin   ==="
