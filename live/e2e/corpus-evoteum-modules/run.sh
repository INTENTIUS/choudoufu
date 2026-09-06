#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-evoteum-modules recipe; run with: just demo-run corpus-evoteum-modules)
# The eighth OpenTofu-native crossing - counted off
# live/corpus-crossing-manifest.json's own lane field, which reads 7 before
# this one; the ordinals in the recipes above were written when the lane was
# shorter and do not all agree with it. From a fresh sourcing search:
# evoteum/tofu-modules (live/corpus-manifest.json, pinned by commit -
# the repository publishes no tags and its README says why), the
# aws/networking and aws/dynamodb modules - Evoteum Ltd's own reusable
# module library, the second commercial vendor in this lane. Its
# OpenTofu-native evidence is of four independent kinds and includes the
# only one so far that Terraform could not even parse: .tofutest.hcl unit
# tests. All five stages PASS for real - 10 instances cold-deployed, 7
# stamped, 3 route table associations correctly UNTAGGABLE and re-derived
# from their tagged parents, an empty replan with markers re-read via the
# AWS CLI, a genuine no-op apply, and drift on the VPC's Name tag
# reconverging without touching anything else. It is the first crossing in
# either lane whose for_each keys fall outside the AWS tag-value charset,
# so internal/live/markers' address escaping is load-bearing here and the
# expected escaped markers are asserted by hand. Needs Docker, the AWS CLI,
# and the real `tofu` binary; runs on its own port (4730).
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
#   BREAK         set to 1 to corrupt stage 2's identity assertion. Set to
#                 "rename" to exercise day2_rename's own break control
#                 instead - renaming module sessions_table WITHOUT a moved
#                 block, which must propose a destroy and a create. Set to
#                 "replace" to exercise day2_replace's own break control:
#                 manufacture the coexistence a skipped destroy would leave
#                 behind (PART F).
#   BREAK_STAGE5  set to 1 to tamper a second object before stage 5.
#   BREAK_REMOVE  set to 1 to run day2_remove's own break control instead.
#   BREAK_COUNT   set to 1 to run day2_count's own break control (PART G,
#                 far below): after the real scale-down plan, assert the
#                 WRONG subnet (a survivor) was destroyed instead of the
#                 dropped one - the assertion must fail. Only reachable when
#                 BREAK is not "rename" and BREAK_REMOVE is not 1, because
#                 PART G starts from PART E's real, completed removal.
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

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# separate namespace stock (real `tofu`, same as stage 1 - see this script's
# header for why plain `terraform` cannot even parse this .tofu-only estate)
# applies the identical modules into as that stage's own oracle. +1000/+2000
# keeps this estate's own [main, green, oracle] port triple disjoint from
# every other live/e2e script's own FLOCI_PORT default (all under 4800) and
# from a sibling batch estate's triple one port over - see
# corpus-ecs-fargate's own greenfield header for the real collision +20 hit
# on a live run.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1000))
FLOCI_GREEN_NAME="choudoufu-corpus-evoteum-modules-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2000))
FLOCI_ORACLE_NAME="choudoufu-corpus-evoteum-modules-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
GREEN="$WORK/green"
GREEN_ORACLE="$WORK/green-oracle"
GREEN_ESTATE_NAME="evoteum-modules-greenfield"

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
#
# #339: TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE closes the gap a warm
# cache alone does not - without it, init in a directory with no
# .terraform.lock.hcl re-downloads the whole provider purely to compute
# checksums, even when the cache already holds that exact version (see
# live/e2e/README.md, "The shared plugin cache" for the measured numbers).
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"

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
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }
gauntlet_begin

# marker_filter <marker>: a --filters argument matching tofu-address exactly,
# in the CLI's JSON form rather than its Name=,Values= shorthand. The
# shorthand parser is the fragile one - it splits on "," and "=", both of
# which are legal AWS tag-value characters - so every marker lookup here goes
# through JSON and none of them depends on what the shorthand happens to
# tolerate.
marker_filter() {
  printf '[{"Name":"tag:tofu-address","Values":["%s"]}]' "${1//\"/\\\"}"
}

# remove_module_block FILE NAME - day2_remove's edit: delete a whole
# top-level `module "NAME" { ... }` block. Used on both module.sessions_table
# (cold_deploy's own state, the stock oracle) and module.sessions_table_renamed
# (Part D's own renamed state, the real check) - the same module, before and
# after day2_rename's live-mv. No other module references sessions_table's
# output (this crossing's own root wiring has no cross-module reference
# between networking and sessions_table at all - see PART D-ORACLE's header),
# so deleting its block needs no other edit.
remove_module_block() {
  local file="$1" name="$2"
  sed -i.bak "/^module \"$name\" {\$/,/^}\$/d" "$file"
  rm -f "$file.bak"
  grep -q "module \"$name\"" "$file" \
    && fail "removing module \"$name\"'s block did not match in $file - the corpus pin has moved"
}

# set_public_subnets FILE MODULE_NAME HCL_LIST - day2_count's own edit:
# public_subnets is aws/networking's own documented list(string) variable
# (default ["10.0.101.0/24","10.0.102.0/24","10.0.103.0/24"]), not exposed
# as a root variable by write_root's own module block, so this writes (or,
# with HCL_LIST="", removes) a `public_subnets = HCL_LIST` argument directly
# inside the named module block - the real, already-live for_each knob PART
# G/G-ORACLE scale, never a synthetic resource. Balanced on the module
# block's own boundaries (python, not a line-anchored sed) because
# `environment  = "..."` - the nearest fixed anchor - also appears verbatim
# inside module "sessions_table"'s own block, so a sed pattern would not be
# scoped to the right module.
set_public_subnets() {
  local file="$1" mod="$2" list="$3"
  python3 - "$file" "$mod" "$list" <<'PYEOF'
import re, sys
path, mod, lst = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(path).read()
marker = 'module "%s" {\n' % mod
i = s.index(marker)
close = s.index('\n}\n', i)
block = s[i:close]
block = re.sub(r'\n  public_subnets\s*=\s*\[[^\n]*\]', '', block)
if lst:
    block = block + '\n  public_subnets = %s' % lst
s = s[:i] + block + s[close:]
open(path, 'w').write(s)
PYEOF
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
gauntlet_begin_stage cold_deploy
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
gauntlet_stage cold_deploy pass "10 resources added (1 vpc, 3 subnets, 1 igw, 1 route table, 3 associations, 1 dynamodb table); confirmed unmarked"
log ""

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13) - two MORE, fresh floci
# containers, neither reusing a single object stage 1's plain apply created.
# choudoufu applies the identical two modules directly with a live block
# from the start, no migration, no state file ever existing; the estate's
# own oracle is stock `tofu` applying the SAME modules fresh in a third,
# independent namespace, compared structurally via the AWS CLI on both
# endpoints, never through tofu state.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage greenfield
log "=== G0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"dynamodb"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"dynamodb"' <<< "${GH:-}" || fail "floci did not come up healthy (dynamodb) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

GREEN_LIVE_BLOCK='
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
copy_modules "$GREEN"
write_root "$GREEN" "$GREEN_LIVE_BLOCK"
copy_modules "$GREEN_ORACLE"
write_root "$GREEN_ORACLE" ""

log "=== G1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -60; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 10 added, 0 changed, 0 destroyed' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 10 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }

log "=== G2. the VPC's marker, read through the AWS CLI directly ==="
GREEN_VPC_ADDR="$(awsg ec2 describe-vpcs --filters "$(marker_filter 'module.networking.aws_vpc.main')" --query "Vpcs[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_VPC_ADDR" = "module.networking.aws_vpc.main" ] || fail "the greenfield VPC carries tofu-address=$GREEN_VPC_ADDR, not module.networking.aws_vpc.main"
GREEN_VPC_ESTATE="$(awsg ec2 describe-vpcs --filters "$(marker_filter 'module.networking.aws_vpc.main')" --query "Vpcs[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_VPC_ESTATE" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield VPC carries tofu-estate=$GREEN_VPC_ESTATE, not $GREEN_ESTATE_NAME"
log "  the greenfield VPC carries tofu-address=$GREEN_VPC_ADDR tofu-estate=$GREEN_VPC_ESTATE - read via the AWS CLI, not choudoufu's own report"

log "=== G3. the record store holds every instance, including the 3 untaggable associations (#364 A2) ==="
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" = "10" ] || fail "expected 10 records under the local record store after the greenfield apply (one per managed instance), found $GREEN_RECORD_FILES"
log "  10 records persisted, one per managed instance, read directly off the local record store"

log "=== G4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== G5. stock oracle - the identical modules applied fresh in its own namespace ==="
( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(cd "$GREEN_ORACLE" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | tail -60; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 10 added, 0 changed, 0 destroyed' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 10 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

log "=== G6. object-by-object comparison, via the AWS CLI on both endpoints, marker tags never compared ==="
evoteum_shape() { # $1 = endpoint - a normalised structural fact sheet, read
                   # via the AWS CLI, never through tofu state.
  local ep="$1"
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-vpcs \
    --filters "Name=tag:Name,Values=$VPC_NAME" \
    --query "Vpcs[0].CidrBlock" --output text 2>/dev/null | sed 's/^/vpc_cidr=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-subnets \
    --filters "Name=tag:Name,Values=${RESOURCE_PREFIX}*" \
    --query "sort(Subnets[].CidrBlock)" --output text 2>/dev/null | tr '\t' ',' | sed 's/^/subnet_cidrs_sorted=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-internet-gateways \
    --filters "Name=tag:Name,Values=${RESOURCE_PREFIX}*" \
    --query "length(InternetGateways)" --output text 2>/dev/null | sed 's/^/igw_n=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-route-tables \
    --filters "Name=tag:Name,Values=${RESOURCE_PREFIX}*" \
    --query "length(RouteTables[?length(Associations)>\`0\`])" --output text 2>/dev/null | sed 's/^/route_table_with_assoc_n=/'
  aws --endpoint-url "$ep" --region "$REGION" dynamodb describe-table --table-name "$TABLE_NAME" \
    --query "Table.[BillingModeSummary.BillingMode,length(AttributeDefinitions),length(GlobalSecondaryIndexes)]" --output text 2>/dev/null \
    | awk '{print "table billing="$1" attrs="$2" gsis="$3}'
  aws --endpoint-url "$ep" --region "$REGION" dynamodb describe-table --table-name "$TABLE_NAME" \
    --query "Table.AttributeDefinitions[?AttributeName=='pk'].AttributeType | [0]" --output text 2>/dev/null | sed 's/^/pk_type=/'
}
GREEN_SHAPE="$(evoteum_shape "$GREEN_ENDPOINT" | sort)"
ORACLE_SHAPE="$(evoteum_shape "$ORACLE_ENDPOINT" | sort)"
if [ "$GREEN_SHAPE" != "$ORACLE_SHAPE" ]; then
  diff <(printf '%s\n' "$GREEN_SHAPE") <(printf '%s\n' "$ORACLE_SHAPE") || true
  fail "the greenfield estate's object inventory does not match stock's cold deploy, object by object, in its own namespace"
fi
log "  object-by-object match: vpc cidr, subnet cidrs, igw count, route-table-with-association count, and the dynamodb table's billing mode/attribute count/GSI count/pk type - identical between the greenfield estate and stock's cold deploy in its own namespace, marker tags never part of the comparison"

gauntlet_stage greenfield pass "10 resources from nothing (1 vpc, 3 subnets, 1 igw, 1 route table, 3 untaggable associations, 1 dynamodb table), VPC marker verified via the AWS CLI, 10 records in the local record store (#364 A2, one per managed instance), replan empty, stock oracle in its own namespace matches structurally on vpc/subnets/igw/route-table/dynamodb-table"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The two module calls: a `moved` block renames the WHOLE module call
# "networking" (its own aws_vpc.main, three aws_subnet.public[cidr]
# instances, aws_internet_gateway.main and aws_route_table.public are all
# taggable; its three aws_route_table_association.public[cidr] instances
# are untaggable-by-design derived children), and "choudoufu live-mv"
# (below, after drift_reconverge) renames the whole module call
# "sessions_table" with no moved block at all - its own
# aws_dynamodb_table.this is the only object it carries, and no other
# module references it (no cross-module output references exist between
# networking and sessions_table in this crossing's own root wiring).
# Neither leaf module's own source is touched (DELTA discipline). The
# stock oracle (real tofu - see header for why stock terraform cannot see
# this .tofu-only estate at all) runs the same two renames, through moved
# blocks only, on a copy of cold_deploy's own state - before choudoufu or
# live-import ever touch these objects.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE: stock tofu, the same two renames through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/module "networking" {/module "networking_renamed" {/' "$PLAIN_ORACLE/main.tofu"
sed -i.bak 's/module "sessions_table" {/module "sessions_table_renamed" {/' "$PLAIN_ORACLE/main.tofu"
rm -f "$PLAIN_ORACLE/main.tofu.bak"
cat >> "$PLAIN_ORACLE/main.tofu" <<'EOF'

moved {
  from = module.networking
  to   = module.networking_renamed
}

moved {
  from = module.sessions_table
  to   = module.sessions_table_renamed
}
EOF
( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && tofu plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state (moved-block relocation of a whole module, associations included) - no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7): same principle as the
# rename oracle above - a SEPARATE copy of cold_deploy's own state,
# unrenamed, so this removal has nothing to do with the rename this script
# also exercises. module.sessions_table is the whole target: one resource,
# no other module references it.
gauntlet_begin_stage day2_remove
log "=== D-ORACLE (day2_remove): stock tofu, delete module.sessions_table's block on cold_deploy's own state ==="
PLAIN_ORACLE_REMOVE="$WORK/plain-oracle-remove"
cp -r "$PLAIN" "$PLAIN_ORACLE_REMOVE"
remove_module_block "$PLAIN_ORACLE_REMOVE/main.tofu" "sessions_table"
( cd "$PLAIN_ORACLE_REMOVE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REMOVE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REMOVE" && tofu plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.sessions_table\.aws_dynamodb_table\.this will be destroyed' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock does not propose destroying module.sessions_table.aws_dynamodb_table.this when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -10; fail "stock's remove plan proposes something other than exactly one destroy"; }
log "  stock: exactly one destroy (module.sessions_table.aws_dynamodb_table.this), nothing else, on cold_deploy's own state"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock oracle (day2_replace, live/GAUNTLET.md #9):
# "Stock's replace of the same resource leaves the same single object." A
# THIRD separate copy of cold_deploy's own state ($PLAIN), unrenamed and
# unremoved, so this oracle has nothing to do with the rename/remove oracles
# above. Changes module.sessions_table's `table_name` argument (a real,
# upstream-declared ForceNew argument on aws_dynamodb_table - AWS's
# UpdateTable API has no operation to rename a table) to a different
# literal, which forces stock to replace the SAME declared address rather
# than propose a destroy-and-create pair at two different addresses.
# PLAN ONLY, never applied - same convention as the rename/remove oracles
# above: this copy shares floci's account with $ESTATE, so applying here
# would destroy the real table the estate's own later stages still depend
# on.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_replace
log "=== F-ORACLE: stock tofu, force-replace module.sessions_table's table via its ForceNew table_name argument, on cold_deploy's own state ==="
PLAIN_ORACLE_REPLACE="$WORK/plain-oracle-replace"
cp -r "$PLAIN" "$PLAIN_ORACLE_REPLACE"
rm -rf "$PLAIN_ORACLE_REPLACE/.terraform"
sed -i.bak 's/table_name   = "sessions"/table_name   = "sessions-v2"/' "$PLAIN_ORACLE_REPLACE/main.tofu"
rm -f "$PLAIN_ORACLE_REPLACE/main.tofu.bak"
grep -q 'sessions-v2' "$PLAIN_ORACLE_REPLACE/main.tofu" \
  || fail "changing module.sessions_table's table_name argument in the replace-oracle copy did not match - the corpus pin has moved"
( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_REPLACE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE_REPLACE" && tofu plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.sessions_table\.aws_dynamodb_table\.this must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose replacing module.sessions_table's table when its table_name argument changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add and one destroy at the same address"; }
log "  stock: exactly one replace proposed (destroy the old sessions table, create the sessions-v2 table) at the same declared address, on the state cold_deploy produced - plan only, not applied"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock oracle (day2_count, live/GAUNTLET.md #8,
# issue #359/#488)
# ══════════════════════════════════════════════════════════════════════════
#
# The real, already-live for_each knob this module-heavy estate offers:
# aws/networking's own public_subnets variable
# (default ["10.0.101.0/24","10.0.102.0/24","10.0.103.0/24"]), which drives
# TWO for_each resources in one edit - aws_subnet.public itself, keyed by
# CIDR, and aws_route_table_association.public, whose own for_each is
# `aws_subnet.public` (a RESOURCE's instance map, not a variable) so it
# tracks the subnet set exactly. Dropping the LAST list element (not a
# middle one) is deliberate: `for idx, cidr in var.public_subnets : cidr =>
# local.selected_availability_zones[idx]` keys the map by CIDR but still
# reads its VALUE positionally by idx, so removing anything but the last
# element would reassign a surviving CIDR's own availability_zone (idx
# shifts) and turn "survivors keep identity" into "survivors get an
# in-place update" - dropping the last element leaves every other element's
# own idx, and therefore its own value, untouched. No synthetic resource
# needed (unlike corpus-iam-read-only-policy/corpus-iam-policy, whose only
# real objects are instantiated through non-countable booleans - issue
# #488's fallback clause; this estate's own networking module has the
# harder, real shape corpus-xancloud-iac's PART G already established this
# pattern for).
#
# A THIRD, separate copy of cold_deploy's own state ($PLAIN), untouched by
# the rename/remove/replace oracles above. PLAN ONLY, never applied, same
# discipline as every oracle above: this copy shares floci's account with
# $ESTATE, and $ESTATE's own migrate stage (next) still depends on finding
# all three subnets and associations exactly as $PLAIN's cold deploy left
# them. The down-plan reads directly off that untouched state; the up-plan's
# "the member is not there yet" starting point is simulated with `tofu state
# rm` on a SEPARATE copy - a pure local state edit, no provider API call, so
# it can never touch a live object - the same technique corpus-xancloud-iac's
# own F-ORACLE and corpus-iam-read-only-policy's own G-ORACLE use.
gauntlet_begin_stage day2_count
DROPPED_CIDR="${SUBNET_CIDRS[2]}"
log "=== G-ORACLE: stock tofu, dropping then restoring the last public_subnets CIDR ($DROPPED_CIDR), on cold_deploy's own state (plan-only - see header) ==="
PLAIN_ORACLE_COUNT="$WORK/plain-oracle-count"
cp -r "$PLAIN" "$PLAIN_ORACLE_COUNT"
set_public_subnets "$PLAIN_ORACLE_COUNT/main.tofu" "networking" '["10.0.101.0/24", "10.0.102.0/24"]'
( cd "$PLAIN_ORACLE_COUNT" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_COUNT" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's reinit failed"; }
ORACLE_COUNT_DOWN_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT" && tofu plan -input=false -no-color 2>&1)"; ORACLE_COUNT_DOWN_PLAN_RC=$?
[ "$ORACLE_COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | tail -40; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_COUNT_DOWN_PLAN_RC"; }
grep -qF "# module.networking.aws_subnet.public[\"$DROPPED_CIDR\"] will be destroyed" <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy the dropped subnet"; }
grep -qF "# module.networking.aws_route_table_association.public[\"$DROPPED_CIDR\"] will be destroyed" <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy the dropped subnet's route table association"; }
ORACLE_OTHER_TOUCHED_DOWN="$(grep -E '^  # module\.networking\.(aws_subnet\.public|aws_route_table_association\.public)\[' <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" | grep -vF "\"$DROPPED_CIDR\"" || true)"
[ -z "$ORACLE_OTHER_TOUCHED_DOWN" ] || { printf '%s\n' "$ORACLE_OTHER_TOUCHED_DOWN"; fail "stock's scale-down plan touches a subnet or association other than $DROPPED_CIDR"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$ORACLE_COUNT_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly two destroys (the subnet and its association)"; }
log "  stock (plan-only): exactly two destroys proposed (subnet + association for $DROPPED_CIDR), every other subnet/association untouched"

PLAIN_ORACLE_COUNT_UP="$WORK/plain-oracle-count-up"
cp -r "$PLAIN" "$PLAIN_ORACLE_COUNT_UP"
( cd "$PLAIN_ORACLE_COUNT_UP" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE_COUNT_UP" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock up-oracle's reinit failed"; }
STATE_RM_OUT="$(cd "$PLAIN_ORACLE_COUNT_UP" && tofu state rm "module.networking.aws_route_table_association.public[\"$DROPPED_CIDR\"]" "module.networking.aws_subnet.public[\"$DROPPED_CIDR\"]" 2>&1)"; STATE_RM_RC=$?
[ "$STATE_RM_RC" -eq 0 ] || { printf '%s\n' "$STATE_RM_OUT" | tail -30; fail "the day2_count stock up-oracle's state rm failed"; }
ORACLE_COUNT_UP_PLAN_OUT="$(cd "$PLAIN_ORACLE_COUNT_UP" && tofu plan -input=false -no-color 2>&1)"; ORACLE_COUNT_UP_PLAN_RC=$?
[ "$ORACLE_COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | tail -40; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_COUNT_UP_PLAN_RC"; }
grep -qF "# module.networking.aws_subnet.public[\"$DROPPED_CIDR\"] will be created" <<< "$ORACLE_COUNT_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create the dropped subnet"; }
grep -qF "# module.networking.aws_route_table_association.public[\"$DROPPED_CIDR\"] will be created" <<< "$ORACLE_COUNT_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create the dropped subnet's route table association"; }
ORACLE_OTHER_TOUCHED_UP="$(grep -E '^  # module\.networking\.(aws_subnet\.public|aws_route_table_association\.public)\[' <<< "$ORACLE_COUNT_UP_PLAN_OUT" | grep -vF "\"$DROPPED_CIDR\"" || true)"
[ -z "$ORACLE_OTHER_TOUCHED_UP" ] || { printf '%s\n' "$ORACLE_OTHER_TOUCHED_UP"; fail "stock's scale-up plan touches a subnet or association other than $DROPPED_CIDR"; }
grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_COUNT_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly two creates"; }
log "  stock (plan-only): exactly two creates proposed (subnet + association for $DROPPED_CIDR, state simulated with 'tofu state rm' - no live object ever touched), every other subnet/association untouched"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
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
grep -qF "7 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 3 skipped" <<< "$APPROVE_OUT" \
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
gauntlet_stage migrate pass "7 of 10 verified and stamped, 0 failed, 3 correctly UNTAGGABLE; markers read back via the AWS CLI"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities asserted
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
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
gauntlet_stage test_plan pass "no changes; VPC and table markers unchanged, all three untaggable associations resolved by their composite identity"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
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
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); object count unchanged at $BEFORE_N, no state file"
log ""

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
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
gauntlet_stage drift_reconverge pass "VPC Name tag tampered out of band, exactly 1 object proposed and reconverged, marker survived the incremental tag update"

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  vpc $VPC_ID (module.networking), table $TABLE_NAME (module.sessions_table)"

if [ "${BREAK:-}" = "rename" ]; then
  log "=== D1 (BREAK=rename). rename module sessions_table -> sessions_table_renamed WITHOUT a moved block ==="
  sed -i.bak 's/module "sessions_table" {/module "sessions_table_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the BREAK=rename reinit failed"; }
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  # Verified directly, reproduced identically across two isolated runs:
  # this estate's aws_dynamodb_table.this shows GAUNTLET.md #6's own
  # textbook Break shape - "the plan must show a destroy and a create" -
  # the same clean pair corpus-eks-basic's BREAK=1 asserts for its security
  # group, unlike corpus-hongbomiao-harbor's aws_iam_user (which shows a
  # nondeterministic mix of an ambiguity warning and/or a bare create, never
  # a destroy - see that estate's script). Both types are equally
  # client-named; the difference is in exactly which discovery/plan path
  # each type's identity resolution takes, not asserted further here.
  [ "$BREAK_PLAN_RC" -eq 0 ] \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=rename: the plan exited $BREAK_PLAN_RC - expected a clean exit proposing a destroy and a create (see header)"; }
  grep -qE '^  # module\.sessions_table\.aws_dynamodb_table\.this will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: renaming without a moved block did not propose destroying the live table under its old address - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.sessions_table_renamed\.aws_dynamodb_table\.this will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=rename: renaming without a moved block did not propose creating the table at the renamed address - this stage's check is not load-bearing"; }
  log "  BREAK=rename: correctly proposes destroying module.sessions_table.aws_dynamodb_table.this and creating module.sessions_table_renamed.aws_dynamodb_table.this - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: module networking -> networking_renamed ==="
  sed -i.bak 's/module "networking" {/module "networking_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  cat >> "$ESTATE/main.tofu" <<'EOF'

moved {
  from = module.networking
  to   = module.networking_renamed
}
EOF
  # Renaming a MODULE CALL (not a resource label) changes the module
  # instance registry .terraform tracks, unlike a plain resource rename -
  # a re-init is required even though the source path itself is unchanged.
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  # corpus-vpc-complete's own day2_rename script documents a genuine,
  # unfixed choudoufu defect that reaches exactly this shape: a moved
  # module whose untaggable/derived children (aws_route_table_association
  # among them, named there by type) do not follow the moved parent the
  # way a marker-carrying resource does, because internal/live/moved's
  # alias index is marker-based and has nothing to index for an untaggable
  # type - so a CREATE gets proposed for the derived child instead of it
  # matching structurally under the parent's new address. This estate's
  # networking module carries the exact same type
  # (aws_route_table_association.public, three instances via for_each), so
  # this is checked for explicitly rather than only asserting zero churn
  # generically, precisely so a recurrence here is reported by name instead
  # of as a bare "not zero churn" failure.
  if grep -qE '^  # module\.networking_renamed\.aws_route_table_association\.public\[.+\] will be created' <<< "$MOVED_PLAN_OUT"; then
    printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'
    fail "choudoufu defect (known, documented in corpus-vpc-complete/run.sh, unfixed): the moved-block rename of module.networking proposes a CREATE for its untaggable derived child aws_route_table_association.public instead of matching it structurally under the parent's new address - not zero churn. internal/live/moved's alias index is marker-based and has nothing to index for an untaggable type, so a derived child does not follow its moved parent module. This estate's own aws_route_table_association.public (3 for_each instances) reproduces the exact same class vpc-complete's script already names; reaches every estate that renames a module containing a derived child of a moved parent (aws_security_group_rule, aws_route, aws_route_table_association, aws_vpc_dhcp_options_association, ...). Not fixed in this script-only unit - a Go-level fix to internal/live/moved's alias index (or an equivalent structural-match fallback for untaggable children under a renamed module) is needed."
  fi
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  N_CHANGED_D1="$(grep -cE '^  # .+ will be updated in-place' <<< "$MOVED_PLAN_OUT" || true)"
  [ "$N_CHANGED_D1" -ge 1 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -20; fail "the moved-block rename plan proposes no in-place changes at all - nothing to rewrite the markers"; }
  grep -qF "Plan: 0 to add, $N_CHANGED_D1 to change, 0 to destroy." <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan's summary does not match its own $N_CHANGED_D1 in-place changes"; }
  grep -qE '~ +"tofu-address" += +"module\.networking\.aws_vpc\.main" +-> +"module\.networking_renamed\.aws_vpc\.main"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the VPC's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, $N_CHANGED_D1 in-place tags updates - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE "Resources: 0 added, $N_CHANGED_D1 changed, 0 destroyed" <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply did not change exactly $N_CHANGED_D1 resources"; }

  VPC_ID_D_AFTER="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].VpcId" --output text 2>/dev/null || true)"
  [ "$VPC_ID_D_AFTER" = "$VPC_ID" ] || fail "the VPC's id changed across the rename ($VPC_ID -> $VPC_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  VPC_ADDR_D_AFTER="$(awsl ec2 describe-vpcs --vpc-ids "$VPC_ID" --query "Vpcs[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$VPC_ADDR_D_AFTER" = "module.networking_renamed.aws_vpc.main" ] \
    || fail "the VPC carries tofu-address=$VPC_ADDR_D_AFTER after the rename, not module.networking_renamed.aws_vpc.main"
  log "  $VPC_ID unchanged, tofu-address now module.networking_renamed.aws_vpc.main - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: module sessions_table -> sessions_table_renamed, no moved block at all ==="
  sed -i.bak 's/module "sessions_table" {/module "sessions_table_renamed" {/' "$ESTATE/main.tofu"
  rm -f "$ESTATE/main.tofu.bak"
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.sessions_table.aws_dynamodb_table.this module.sessions_table_renamed.aws_dynamodb_table.this 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.sessions_table.aws_dynamodb_table.this" -> "module.sessions_table_renamed.aws_dynamodb_table.this"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  TABLE_ADDR_D_AFTER="$(awsl dynamodb list-tags-of-resource --resource-arn "$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$TABLE_ADDR_D_AFTER" = "module.sessions_table_renamed.aws_dynamodb_table.this" ] \
    || fail "the table carries tofu-address=$TABLE_ADDR_D_AFTER after live-mv, not module.sessions_table_renamed.aws_dynamodb_table.this"
  log "  $TABLE_NAME unchanged, tofu-address now module.sessions_table_renamed.aws_dynamodb_table.this - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: module.networking renamed with zero churn (0 add, $N_CHANGED_D1 change, 0 destroy), marker rewritten in place across its taggable objects including the untaggable route-table-association children resolving structurally; live-mv: module.sessions_table renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"


  # ══════════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.sessions_table_renamed
  # (originally module.sessions_table) is bound and converged, and is
  # otherwise untouched by anything else in this script until PART E removes
  # it below - the two day-2 stages compose on the SAME address rather than
  # needing a second standalone object. Its `table_name` argument changes to
  # a new literal, which forces `name` (the dynamodb module sets
  # `name = local.table_name`, in turn `lower("${var.project_id}-
  # ${var.environment}-${var.table_name}")`) to change - a real,
  # upstream-declared ForceNew argument on aws_dynamodb_table (AWS's
  # UpdateTable API has no rename operation) - forcing a replace at the SAME
  # declared address (module.sessions_table_renamed.aws_dynamodb_table.this
  # never changes) while the physical table behind it is destroyed and a new
  # one created - the marker moving onto the new table is this stage's own
  # Proves text.
  #
  # THE create_before_destroy SCOPE NOTE (full reasoning in corpus-sqs-
  # basic's own PART F). OpenTofu core rejects a `lifecycle` block written
  # directly on a `module` call, and patching the vendored aws/dynamodb
  # module's own resource to add create_before_destroy would cross this
  # corpus's own provider-pin-only DELTA discipline (see header's THE ONE
  # DELTA), so this evidence pass exercises OpenTofu's default
  # destroy-then-create ordering instead - confirmed below by the plan's own
  # "-/+ destroy and then create replacement" legend. BREAK=replace
  # manufactures the create-before-destroy collision shape directly via the
  # AWS CLI rather than through an interrupted apply (day2_crash's own job).
  #
  # aws_dynamodb_table.this carries no count/for_each (unlike
  # corpus-sqs-basic's aws_sqs_queue.this[0]), so its own tofu-address tag
  # carries no ":0" slot suffix at all (confirmed against every other
  # assertion in this script that reads it, e.g. D2's own
  # TABLE_ADDR_D_AFTER check). VERIFIED empirically (see the BREAK=replace
  # branch's own comment below): a scalar resource's collision does not
  # take corpus-sqs-basic's fungible-set "Two live resources claiming one
  # slot" hard-refusal path at all - it surfaces as a named
  # "Live resource displaced from the address it is marked for" warning
  # instead, with the plan itself still exiting 0 and proposing nothing for
  # either object. Both are loud, named reports of the same underlying
  # collision; which one a given resource shape takes depends on whether it
  # is a member of a fungible (count/for_each) set.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.sessions_table_renamed.aws_dynamodb_table.this"
  F_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_dynamodb_table/$(record_key "$F_ADDR")"

  log "=== F0. capture the live table and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$TABLE_NAME" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $TABLE_NAME"
  F_OLD_TABLE_ARN="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)"
  F_OLD_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$F_OLD_TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_OLD_ADDR_TAG" = "$F_ADDR" ] || fail "$TABLE_NAME does not carry tofu-address=$F_ADDR ahead of day2_replace"
  log "  $TABLE_NAME, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live table carrying the SAME tofu-address as the
    # one a genuine replace would destroy - the state "skip the destroy
    # half" of a create-before-destroy replace would leave, produced
    # directly via the AWS CLI rather than by actually interrupting an
    # apply (day2_crash's own job).
    BREAK_COLLISION_NAME="${TABLE_NAME}-collision"
    awsl dynamodb create-table --table-name "$BREAK_COLLISION_NAME" \
      --attribute-definitions AttributeName=pk,AttributeType=S \
      --key-schema AttributeName=pk,KeyType=HASH \
      --billing-mode PAY_PER_REQUEST \
      --tags "Key=tofu-estate,Value=$ESTATE_NAME" "Key=tofu-address,Value=$F_ADDR" \
      >/dev/null || fail "BREAK=replace: could not create the collision table"
    awsl dynamodb wait table-exists --table-name "$BREAK_COLLISION_NAME" 2>/dev/null || true
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    awsl dynamodb delete-table --table-name "$BREAK_COLLISION_NAME" >/dev/null 2>&1 || true
    # VERIFIED empirically before writing this assertion (a fresh floci
    # container, the collision table created and tagged exactly as above,
    # then a plan run directly with no BREAK-branch code in the loop): for
    # a SCALAR resource (no count/for_each, unlike corpus-sqs-basic's
    # aws_sqs_queue.this[0]) the collision does not take the fungible-set
    # "Two live resources claiming one slot" hard-refusal path at all - it
    # takes discovery.go's singular-address path instead, which reports
    # "Warning: Live resource displaced from the address it is marked for"
    # (naming BOTH identities: the collision table's and the configured
    # one) and returns rc=0 with "No changes." for the resource itself,
    # rather than failing the whole plan. That is still "reporting a
    # collision loudly, not silently proposing nothing" - the stage's own
    # Break text - it is simply a warning-severity report on this shape
    # instead of a hard refusal. So this asserts rc=0 AND the named
    # warning body, not a nonzero exit.
    [ "$BREAK_PLAN_RC" -eq 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the plan exited $BREAK_PLAN_RC - expected rc=0 with a named displaced-resource warning (see the comment immediately above)"; }
    grep -qF 'Warning: Live resource displaced from the address it is marked for' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address but did not report the collision - this stage's check is not load-bearing"; }
    grep -qF "$BREAK_COLLISION_NAME" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the displaced-resource warning does not name the collision table ($BREAK_COLLISION_NAME)"; }
    grep -qF "$F_ADDR" <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the displaced-resource warning does not name the contested address ($F_ADDR)"; }
    grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -40; fail "BREAK=replace: the real, correctly-configured table should still show no resource action alongside the collision warning"; }
    log "  BREAK=replace: choudoufu correctly reported the collision by name (\"Live resource displaced from the address it is marked for\", naming both $BREAK_COLLISION_NAME and $F_ADDR) rather than silently proposing nothing, and left the real table's own plan at no-op - the Break text's own outcome for a scalar (non-fungible) resource"
  else
    log "=== F1. choudoufu: change the ForceNew table_name argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/table_name   = "sessions"/table_name   = "sessions-v2"/' "$ESTATE/main.tofu"
    rm -f "$ESTATE/main.tofu.bak"
    grep -q 'sessions-v2' "$ESTATE/main.tofu" || fail "changing module.sessions_table_renamed's table_name argument did not match - the corpus pin has moved"
    F_NEW_TABLE_NAME="${PROJECT_ID}-${ENVIRONMENT}-sessions-v2"

    F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.sessions_table_renamed\.aws_dynamodb_table\.this must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing module.sessions_table_renamed's table when its ForceNew table_name argument changes"; }
    grep -qE '~ +name +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark name as forcing replacement"; }
    grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add and one destroy at the same address"; }
    log "  choudoufu: exactly one forced replace at the same declared address (module.sessions_table_renamed.aws_dynamodb_table.this), name forces replacement"

    F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add and one destroy"; }

    if F_OLD_STILL="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" 2>&1)"; then
      echo "$F_OLD_STILL"; fail "$TABLE_NAME still exists after the replace - the old object was orphaned, not destroyed"
    fi
    grep -qi 'ResourceNotFoundException' <<< "$F_OLD_STILL" \
      || { echo "$F_OLD_STILL"; fail "describe-table for $TABLE_NAME failed with an unexpected error, not ResourceNotFoundException - it may still exist"; }
    log "  $TABLE_NAME no longer exists (ResourceNotFoundException) - confirmed via the AWS CLI, not through choudoufu's own report"

    F_NEW_TABLE_ARN="$(awsl dynamodb describe-table --table-name "$F_NEW_TABLE_NAME" --query 'Table.TableArn' --output text)"
    F_NEW_ADDR_TAG="$(awsl dynamodb list-tags-of-resource --resource-arn "$F_NEW_TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_NEW_ADDR_TAG" = "$F_ADDR" ] \
      || fail "$F_NEW_TABLE_NAME carries tofu-address=$F_NEW_ADDR_TAG after the replace, not $F_ADDR - the marker did not move onto the new object"
    log "  $F_NEW_TABLE_NAME (the new table) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW table's import_id (its name), not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_TABLE_NAME" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new table $F_NEW_TABLE_NAME - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  No changes. The replace is complete and invisible to the next plan."

    # PART E below reads $TABLE_NAME for its own AWS CLI checks; the live
    # table it must find is now the one this replace just created.
    TABLE_NAME="$F_NEW_TABLE_NAME"

    gauntlet_stage day2_replace pass "choudoufu: changing module.sessions_table_renamed's ForceNew table_name argument proposed exactly one replace at the same declared address (1 add, 0 change, 1 destroy; -/+ destroy and then create), applied cleanly; the old table ($F_OLD_TABLE_ARN) is confirmed gone and the new table ($F_NEW_TABLE_NAME) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new table's name, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace at the same address (plan only, not applied - it shares floci's account with \$ESTATE); BREAK=replace confirms a manufactured marker collision is reported loudly rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active stage - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: module.sessions_table_renamed
  # (originally module.sessions_table) is bound and converged. It is the
  # whole target here too - the same single-resource, no-cross-module-
  # reference shape the stock oracle above already confirmed, just under its
  # renamed address now.
  #
  # BREAK_REMOVE=1 exercises this stage's own break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go for day2_remove is literally
  # "keep the block; no destroy may be proposed".

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live table's ARN one more time ==="
  E_TABLE_ARN="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" --query 'Table.TableArn' --output text)"
  [ -n "$E_TABLE_ARN" ] && [ "$E_TABLE_ARN" != "None" ] || fail "no live table found by name ($TABLE_NAME) before day2_remove even starts"
  E_ARN_BEFORE="$(awsl dynamodb list-tags-of-resource --resource-arn "$E_TABLE_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$E_ARN_BEFORE" = "module.sessions_table_renamed.aws_dynamodb_table.this" ] \
    || fail "$TABLE_NAME does not carry tofu-address=module.sessions_table_renamed.aws_dynamodb_table.this before day2_remove even starts (got $E_ARN_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep module.sessions_table_renamed's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(plan_into 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # module\.sessions_table_renamed\.aws_dynamodb_table\.this will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_REMOVE=1: a destroy was proposed for module.sessions_table_renamed's table even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: the kept-block plan is not empty"; }
    log "  BREAK_REMOVE=1: correctly proposes nothing - the block is still declared"
  else
    log "=== E1. choudoufu: delete module.sessions_table_renamed's block ==="
    remove_module_block "$ESTATE/main.tofu" "sessions_table_renamed"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(plan_into 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -30
      fail "choudoufu withheld the destroy of module.sessions_table_renamed's table as a possible rename (discovery.go's classifyOrphans) even though no other module.sessions_table* block exists anywhere in this config - this is an honest wall, not a pass"
    fi
    grep -qE '^  # module\.sessions_table_renamed\.aws_dynamodb_table\.this will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying module.sessions_table_renamed's table when its block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (module.sessions_table_renamed's table), nothing else, address-for-address identical to stock's oracle on cold_deploy's own state"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    if E_STILL="$(awsl dynamodb describe-table --table-name "$TABLE_NAME" 2>&1)"; then
      echo "$E_STILL"; fail "$TABLE_NAME still exists in the live account after the destroy - it was orphaned, not destroyed"
    fi
    grep -qi 'ResourceNotFoundException' <<< "$E_STILL" \
      || { echo "$E_STILL"; fail "describe-table for $TABLE_NAME failed with an unexpected error, not ResourceNotFoundException - it may still exist"; }
    log "  $TABLE_NAME no longer exists (ResourceNotFoundException) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(plan_into 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting module.sessions_table_renamed's block proposed exactly one destroy (0 add, 0 change, 1 destroy), address-for-address identical to stock's oracle on cold_deploy's own state (module.sessions_table); applied cleanly (0 added, 0 changed, 1 destroyed); the table is genuinely gone from the live account (describe-table now returns ResourceNotFoundException, read via the AWS CLI, not choudoufu's own report), and the next plan is empty; classifyOrphans did not withhold the destroy because no other module.sessions_table* block is declared anywhere in this config"

    # ════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8, issue
    # #359/#488)
    # ════════════════════════════════════════════════════════════════════
    #
    # Starts from Part D's real, completed rename: module.networking_renamed
    # (this estate's only surviving module - Part E just destroyed
    # module.sessions_table_renamed's own table for good) is bound and
    # converged, its three subnets and three route-table associations live
    # and marked. See G-ORACLE's own header (above stage 2) for why the
    # LAST public_subnets CIDR is the one dropped and restored, and why that
    # scales BOTH aws_subnet.public (taggable, server-assigned id) and
    # aws_route_table_association.public (untaggable, composite identity,
    # record-located rather than record-backed - #364 A2) in one edit,
    # through the module's own real, documented variable rather than a
    # synthetic resource.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG subnet
    # (a survivor) was the one destroyed - the Break text in
    # tools/gauntlet/stages.go for day2_count, verbatim: "Expect a different
    # instance to be destroyed; the assertion must fail." Only reachable
    # when BREAK is not "rename" and BREAK_REMOVE is not 1, because PART G
    # starts from PART E's real, completed removal.
    gauntlet_begin_stage day2_count
    SURVIVOR_CIDR_0="${SUBNET_CIDRS[0]}"
    SURVIVOR_CIDR_1="${SUBNET_CIDRS[1]}"
    G_SUBNET_ADDR="module.networking_renamed.aws_subnet.public[\"$DROPPED_CIDR\"]"
    G_ASSOC_ADDR="module.networking_renamed.aws_route_table_association.public[\"$DROPPED_CIDR\"]"
    G_SUBNET_MARKER="${SUBNET_MARKERS[2]/module.networking./module.networking_renamed.}"

    log "=== G0. capture the live ids day2_count must not disturb ==="
    G_DROPPED_SID="${SUBNET_IDS[2]}"
    G_SURVIVOR_SID_0="${SUBNET_IDS[0]}"
    G_SURVIVOR_SID_1="${SUBNET_IDS[1]}"
    G_DROPPED_ADDR_TAG="$(awsl ec2 describe-subnets --subnet-ids "$G_DROPPED_SID" --query "Subnets[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$G_DROPPED_ADDR_TAG" = "$G_SUBNET_MARKER" ] || fail "$G_DROPPED_SID carries tofu-address=$G_DROPPED_ADDR_TAG ahead of day2_count, not $G_SUBNET_MARKER"
    G_DROPPED_ASSOC="$(awsl ec2 describe-route-tables --route-table-ids "$RT_ID" --query "RouteTables[0].Associations[?SubnetId=='$G_DROPPED_SID'].RouteTableAssociationId | [0]" --output text)"
    [ -n "$G_DROPPED_ASSOC" ] && [ "$G_DROPPED_ASSOC" != "None" ] || fail "no live association joins route table $RT_ID to subnet $G_DROPPED_SID ahead of day2_count"
    G_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_subnet/$(record_key "$G_SUBNET_ADDR")"
    [ -f "$G_RECORD" ] || fail "no local record file found for $G_SUBNET_ADDR ahead of day2_count"
    jq -e '(.identity != null) and (.tombstone == null)' "$G_RECORD" >/dev/null \
      || fail "the record at $G_SUBNET_ADDR does not read as identity-present, tombstone-absent ahead of day2_count"
    log "  subnet $G_DROPPED_SID (tofu-address=$G_DROPPED_ADDR_TAG), association $G_DROPPED_ASSOC = $RT_ID/$G_DROPPED_SID, record carries identity and no tombstone - all read directly, not through choudoufu's own report"

    log "=== G1. scale down: drop the last public_subnets CIDR ($DROPPED_CIDR) ==="
    set_public_subnets "$ESTATE/main.tofu" "networking_renamed" '["10.0.101.0/24", "10.0.102.0/24"]'
    COUNT_DOWN_PLAN_OUT="$(plan_into 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -40; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      log "  BREAK_COUNT=1: asserting the WRONG instance (module.networking_renamed.aws_subnet.public[\"$SURVIVOR_CIDR_0\"]) was destroyed instead of $DROPPED_CIDR"
      if grep -qF "# module.networking_renamed.aws_subnet.public[\"$SURVIVOR_CIDR_0\"] will be destroyed" <<< "$COUNT_DOWN_PLAN_OUT"; then
        fail "BREAK_COUNT=1: the plan actually destroys the survivor subnet ($SURVIVOR_CIDR_0) - this assertion is not load-bearing"
      fi
      log "  BREAK_COUNT=1: correctly does NOT destroy the survivor - the wrong-instance assertion above fails to hold, as it must"
    else
      grep -qF "# $G_SUBNET_ADDR will be destroyed" <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy the dropped subnet"; }
      grep -qF "# $G_ASSOC_ADDR will be destroyed" <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy the dropped subnet's route table association"; }
      OTHER_TOUCHED_DOWN="$(grep -E '^  # module\.networking_renamed\.(aws_subnet\.public|aws_route_table_association\.public)\[' <<< "$COUNT_DOWN_PLAN_OUT" | grep -vF "\"$DROPPED_CIDR\"" || true)"
      [ -z "$OTHER_TOUCHED_DOWN" ] || { printf '%s\n' "$OTHER_TOUCHED_DOWN"; fail "choudoufu's scale-down plan touches a subnet or association other than $DROPPED_CIDR"; }
      grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly two destroys"; }
      log "  choudoufu: exactly two destroys (subnet + association for $DROPPED_CIDR), every other subnet/association untouched"

      COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -40; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly two destroys"; }

      # A --subnet-ids lookup by explicit id is the wrong shape to prove
      # absence with: real AWS throws InvalidSubnetID.NotFound for an
      # unknown id there, but floci's own DescribeSubnets returns a plain
      # empty list with exit 0 instead (confirmed directly against this
      # floci image, no tofu in the loop, ahead of writing this check) - an
      # emulator gap in its own right (lex00/floci, not filed here: it
      # changes nothing about whether the subnet is actually gone, only
      # which CLI shape proves it). A --filters lookup sidesteps the whole
      # question: EC2's filter mechanism never errors on zero matches, on
      # real AWS or on floci, so it is the portable way to assert absence.
      G_DROPPED_STILL_N="$(awsl ec2 describe-subnets --filters "Name=subnet-id,Values=$G_DROPPED_SID" --query 'length(Subnets)' --output text)"
      [ "$G_DROPPED_STILL_N" = "0" ] || fail "$G_DROPPED_SID still exists in the live account after the scale-down destroy"
      G_ASSOC_AFTER_DOWN="$(awsl ec2 describe-route-tables --route-table-ids "$RT_ID" --query "RouteTables[0].Associations[?SubnetId=='$G_DROPPED_SID'].RouteTableAssociationId | [0]" --output text 2>/dev/null || true)"
      [ -z "$G_ASSOC_AFTER_DOWN" ] || [ "$G_ASSOC_AFTER_DOWN" = "None" ] \
        || fail "an association still joins route table $RT_ID to the destroyed subnet $G_DROPPED_SID"
      SURVIVOR_SID_0_AFTER_DOWN="$(awsl ec2 describe-subnets --subnet-ids "$G_SURVIVOR_SID_0" --query 'Subnets[0].SubnetId' --output text 2>/dev/null || true)"
      SURVIVOR_SID_1_AFTER_DOWN="$(awsl ec2 describe-subnets --subnet-ids "$G_SURVIVOR_SID_1" --query 'Subnets[0].SubnetId' --output text 2>/dev/null || true)"
      [ "$SURVIVOR_SID_0_AFTER_DOWN" = "$G_SURVIVOR_SID_0" ] || fail "survivor subnet $SURVIVOR_CIDR_0's id changed across the scale-down ($G_SURVIVOR_SID_0 -> $SURVIVOR_SID_0_AFTER_DOWN)"
      [ "$SURVIVOR_SID_1_AFTER_DOWN" = "$G_SURVIVOR_SID_1" ] || fail "survivor subnet $SURVIVOR_CIDR_1's id changed across the scale-down ($G_SURVIVOR_SID_1 -> $SURVIVOR_SID_1_AFTER_DOWN)"
      log "  $G_DROPPED_SID ($DROPPED_CIDR) no longer matches any live subnet (describe-subnets --filters subnet-id, 0 results), its association is gone from route table $RT_ID; both survivor subnets ($G_SURVIVOR_SID_0, $G_SURVIVOR_SID_1) unchanged - all read via the AWS CLI"

      # The record store, asserted by value (HANDOFF's safety rule; the
      # #398-guard shape: a stale record still naming the destroyed subnet's
      # identity would be exactly the wrong-marker failure that outranks a
      # missing one). A destroyed count/for_each instance's record is
      # TOMBSTONED, not deleted - the same shape day2_replace's F2 already
      # established for this estate, checked here by value rather than by
      # file absence.
      jq -e '(has("tombstone")) and (has("identity") | not)' "$G_RECORD" >/dev/null \
        || { cat "$G_RECORD"; fail "the record at $G_SUBNET_ADDR is not tombstoned (has(tombstone) and not has(identity)) after the scale-down destroy"; }
      log "  record store: $G_SUBNET_ADDR is tombstoned, not deleted - read directly off the local record store file, not through choudoufu's own report"

      log "=== G2. scale count back up: restore the last public_subnets CIDR ($DROPPED_CIDR) ==="
      set_public_subnets "$ESTATE/main.tofu" "networking_renamed" ""
      COUNT_UP_PLAN_OUT="$(plan_into 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -40; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qF "# $G_SUBNET_ADDR will be created" <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create the dropped subnet"; }
      grep -qF "# $G_ASSOC_ADDR will be created" <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create the dropped subnet's route table association"; }
      OTHER_TOUCHED_UP="$(grep -E '^  # module\.networking_renamed\.(aws_subnet\.public|aws_route_table_association\.public)\[' <<< "$COUNT_UP_PLAN_OUT" | grep -vF "\"$DROPPED_CIDR\"" || true)"
      [ -z "$OTHER_TOUCHED_UP" ] || { printf '%s\n' "$OTHER_TOUCHED_UP"; fail "choudoufu's scale-up plan touches a subnet or association other than $DROPPED_CIDR"; }
      grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly two creates"; }
      log "  choudoufu: exactly two creates (subnet + association for $DROPPED_CIDR), every other subnet/association untouched"

      COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -40; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly two creates"; }

      G_NEW_SID="$(awsl ec2 describe-subnets --filters "$(marker_filter "$G_SUBNET_MARKER")" --query 'Subnets[0].SubnetId' --output text)"
      [ -n "$G_NEW_SID" ] && [ "$G_NEW_SID" != "None" ] || fail "no live subnet carries tofu-address=$G_SUBNET_MARKER after the scale-up"
      [ "$G_NEW_SID" != "$G_DROPPED_SID" ] || fail "the recreated subnet ($G_NEW_SID) came back with the SAME subnet id it had before being destroyed - the destroy in G1 was not real (verified directly against floci ahead of writing this stage: a subnet's id is server-minted and always differs after a real delete+create)"
      G_NEW_CIDR="$(awsl ec2 describe-subnets --subnet-ids "$G_NEW_SID" --query 'Subnets[0].CidrBlock' --output text)"
      [ "$G_NEW_CIDR" = "$DROPPED_CIDR" ] || fail "the recreated subnet's CIDR is $G_NEW_CIDR, not $DROPPED_CIDR"
      G_NEW_ASSOC="$(awsl ec2 describe-route-tables --route-table-ids "$RT_ID" --query "RouteTables[0].Associations[?SubnetId=='$G_NEW_SID'].RouteTableAssociationId | [0]" --output text)"
      [ -n "$G_NEW_ASSOC" ] && [ "$G_NEW_ASSOC" != "None" ] || fail "no live association joins route table $RT_ID to the recreated subnet $G_NEW_SID"
      [ "$G_NEW_ASSOC" != "$G_DROPPED_ASSOC" ] || fail "the recreated association ($G_NEW_ASSOC) came back with the SAME association id it had before being destroyed"
      SURVIVOR_SID_0_AFTER_UP="$(awsl ec2 describe-subnets --subnet-ids "$G_SURVIVOR_SID_0" --query 'Subnets[0].SubnetId' --output text)"
      SURVIVOR_SID_1_AFTER_UP="$(awsl ec2 describe-subnets --subnet-ids "$G_SURVIVOR_SID_1" --query 'Subnets[0].SubnetId' --output text)"
      [ "$SURVIVOR_SID_0_AFTER_UP" = "$G_SURVIVOR_SID_0" ] || fail "survivor subnet $SURVIVOR_CIDR_0's id changed across the scale-up"
      [ "$SURVIVOR_SID_1_AFTER_UP" = "$G_SURVIVOR_SID_1" ] || fail "survivor subnet $SURVIVOR_CIDR_1's id changed across the scale-up"
      log "  subnet recreated as $G_NEW_SID (CIDR $G_NEW_CIDR, tofu-address=$G_SUBNET_MARKER, a NEW subnet id - was $G_DROPPED_SID), association recreated as $G_NEW_ASSOC (a NEW association id - was $G_DROPPED_ASSOC); both survivor subnets ($G_SURVIVOR_SID_0, $G_SURVIVOR_SID_1) unchanged throughout - all read via the AWS CLI"

      G_RECORD_AFTER_UP="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_subnet/$(record_key "$G_SUBNET_ADDR")"
      jq -e '(.identity != null) and (.tombstone != null)' "$G_RECORD_AFTER_UP" >/dev/null \
        || { cat "$G_RECORD_AFTER_UP"; fail "the record at $G_SUBNET_ADDR does not read as identity-present after the scale-up (its own prior tombstone entry, if any, should still be kept alongside the new identity)"; }
      log "  record store: $G_SUBNET_ADDR carries a live identity again after the scale-up"

      log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(plan_into 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -40; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      gauntlet_stage day2_count pass "choudoufu: dropping the last public_subnets CIDR ($DROPPED_CIDR) destroyed exactly its subnet and route-table-association instances (0 add, 0 change, 2 destroy), leaving both survivor subnets' live ids and tofu-address markers unchanged; the destroyed subnet's local record is tombstoned, not deleted (#398-guard shape, asserted by value); restoring the CIDR created exactly the same two instances under NEW live ids (subnet id and association id both server-minted, verified directly against floci with no tofu in the loop before writing this assertion) while both survivors stayed untouched throughout; the next plan is empty; the G-ORACLE stock oracle on the identical public_subnets edit, applied plan-only on cold_deploy's own state, shows the identical shape: destroy the dropped CIDR's subnet and association only, create them back under new ids, every other subnet/association's id unchanged both times"
    fi
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end
log ""

log "=== PASS: all five stages, real, against evoteum/tofu-modules' own    ==="
log "=== aws/networking and aws/dynamodb modules, .tofu throughout, with   ==="
log "=== one documented provider-pin line as the only delta from the pin   ==="
