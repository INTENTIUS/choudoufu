#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/reference-ec2-vpc.sh: the live-AWS certification harness for
# issue #440, scoped to the estate the 2026-08-29 ruling named -
# reference-ec2-vpc alone, ceiling $5/run. It runs the SAME four stages
# live/GAUNTLET.md defines - cold_deploy, migrate, test_plan, test_apply -
# against a real endpoint instead of the emulator, and reports them with the
# GAUNTLET protocol (live/e2e/lib/gauntlet.sh) so the SAME parser
# (tools/gauntlet's ParseProtocol) reads its output; what makes this a
# live-aws measurement rather than another emulator row is where the Go side
# records it (a.LiveCert, never a.Estates - tools/gauntlet/livecert.go), not
# a different wire grammar.
#
# It is NOT live/e2e/reference-ec2-vpc/run.sh pointed at a different
# endpoint: that script's greenfield/adoption/drift/rename/remove/count/
# replace/crash parts, and its `docker rm -f`-as-cleanup, all assume the
# emulator is what gets discarded when the container dies. This script
# creates real objects, so it adds what #440's brief named as its three
# blockers:
#
#   1. AMI RESOLUTION (live/live-cert/lib/live-cert.sh: livecert_ami) - no
#      "ami-12345678" literal; a real, region-valid Amazon Linux id via the
#      SSM public parameter, resolved through the SAME call whether ENDPOINT
#      is floci or real AWS.
#   2. TEARDOWN ON EVERY EXIT PATH (this file: teardown, on_signal, and the
#      trap wiring below) - a real destroy, run unconditionally regardless
#      of which stage failed or whether the process was interrupted
#      externally, followed by an independent listing
#      (livecert_verify_empty) that never trusts the destroy's own exit
#      code, followed by a raw-AWS-CLI sweep (livecert_sweep) if anything
#      survives.
#   3. PROVENANCE (tools/gauntlet/livecert.go, not this file) - this
#      script's own stdout never lands in live/gauntlet.json; the `live-cert`
#      subcommand records it into the artifact's separate LiveCert slice,
#      which Artifact.Rebuild never touches and no headline bar ever sums.
#
# TARGET=floci (default) runs this against the pinned emulator, for Stage 1
# of #440: prove the harness - AMI resolution, and above all teardown, INCLUDING
# under a mid-apply kill (live/live-cert/selftest-kill.sh drives exactly that)
# - before it is ever pointed at a real account. TARGET=aws is Stage 2, and
# refuses to run at all without LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes
# set by the caller; live/live-cert/run.sh is the wrapper that also enforces
# the process-level wall-clock ceiling (the brief's "not just an in-script
# check").
#
# Env:
#   TARGET        floci (default) or aws.
#   REGION        AWS region (default us-east-1).
#   RUN_ID        retry-safe tag value stamped on every resource this run
#                 creates (default: generated). A retry after a partial
#                 teardown should pass the SAME RUN_ID so livecert_verify_empty
#                 and livecert_sweep find what the previous attempt left.
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips `go build`.
#   TF_COLD_BIN   the stock binary for cold_deploy (default: terraform).
#   FLOCI_PORT    host port for the floci container (TARGET=floci only;
#                 default 4816 - distinct from live/e2e/*'s own ranges so a
#                 concurrent gauntlet run never collides on the port).
#   FLOCI_IMAGE   emulator image; defaults to the digest pin in live/floci-image.
#   LIVECERT_WORK_DIR  working directory; default a fresh mktemp -d. Set this
#                 to a known path so an external driver (selftest-kill.sh)
#                 can find $LIVECERT_WORK_DIR/cold_deploy_apply.out to
#                 synchronize a kill against genuine apply progress rather
#                 than a fixed sleep.
#   LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY
#                 must be exactly "yes" for TARGET=aws; refused otherwise,
#                 before anything is created.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/live-cert/lib/live-cert.sh
source "$LIB/live-cert.sh"

TARGET="${TARGET:-floci}"
REGION="${REGION:-us-east-1}"
RUN_ID="${RUN_ID:-livecert-$(date +%s)-$$}"
ESTATE="livecert-ec2-reference"
WORK="${LIVECERT_WORK_DIR:-$(mktemp -d)}"
mkdir -p "$WORK"
FLOCI_PORT="${FLOCI_PORT:-4816}"
FLOCI_NAME="choudoufu-livecert-reference-ec2-vpc-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
COLD_DIR="$WORK/cold"
ADOPTED_DIR="$WORK/adopted"

log() { printf '%s\n' "$*"; }

case "$TARGET" in
  floci) ENDPOINT="http://127.0.0.1:${FLOCI_PORT}" ;;
  aws)
    ENDPOINT=""
    if [ "${LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY:-}" != "yes" ]; then
      echo "refusing: TARGET=aws needs LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes - nothing has been created" >&2
      exit 2
    fi
    ;;
  *) echo "TARGET must be floci or aws, got $TARGET" >&2; exit 2 ;;
esac

# ── teardown: the piece #440's brief calls the blocker that matters most ──
# Guarded so EXIT (fired for every normal return, including the one
# on_signal below triggers itself with `exit`) never runs this twice.
TEARDOWN_DONE=0
MIGRATE_DONE=0
teardown() {
  [ "$TEARDOWN_DONE" = "1" ] && return 0
  TEARDOWN_DONE=1
  log "=== TEARDOWN (target=$TARGET run=$RUN_ID) ==="

  if [ "$MIGRATE_DONE" = "1" ] && [ -d "$ADOPTED_DIR" ]; then
    # `apply -destroy` against a live-marker estate refuses outright today
    # ("Only the normal planning mode is available under live resource
    # markers... destroying a whole estate in one command is not verified
    # against a live-markers apply yet" - discovered running this exact
    # command here, 2026-08-29; day2_teardown, live/GAUNTLET.md #11, is
    # still a planned stage for exactly this reason). The SAME error names
    # the tested path: delete the resource blocks and `apply` - "the estate
    # sweep plans an owned resource with no configuration as a destroy".
    # That is what this block does, rather than a command choudoufu itself
    # says is unverified.
    log "  attempting choudoufu's own destroy path ($ADOPTED_DIR): the tested route (empty the config, apply) - best effort, proves the live-managed path independent of the stock fallback below"
    {
      cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

EOF
      provider_block
    } > "$ADOPTED_DIR/main.tf"
    ( cd "$ADOPTED_DIR" && "${TOFU:-}" apply -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_choudoufu_destroy.out" 2>&1
    cd_rc=$?
    log "    exit=$cd_rc (see $WORK/teardown_choudoufu_destroy.out) - not trusted alone, verifying by listing below regardless"
    [ "$cd_rc" -ne 0 ] && tail -15 "$WORK/teardown_choudoufu_destroy.out" | sed 's/^/    | /'
  fi

  if [ -d "$COLD_DIR" ] && [ -f "$COLD_DIR/terraform.tfstate" ]; then
    log "  destroying the plain stock state ($COLD_DIR) - this is the primary path: valid the instant cold_deploy finishes, untouched by anything migrate/test_plan/test_apply do afterward"
    ( cd "$COLD_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" terraform destroy -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_stock_destroy.out" 2>&1
    sd_rc=$?
    log "    exit=$sd_rc (see $WORK/teardown_stock_destroy.out) - not trusted alone, verifying by listing next"
    [ "$sd_rc" -ne 0 ] && tail -15 "$WORK/teardown_stock_destroy.out" | sed 's/^/    | /'
  fi

  if livecert_verify_empty; then
    log "  VERIFIED EMPTY by listing: nothing tagged tofu-cert-run=$RUN_ID remains"
  else
    log "  destroy path(s) left resources behind - running the tag-based sweep as the belt-and-suspenders fallback"
    livecert_sweep
    if livecert_verify_empty; then
      log "  VERIFIED EMPTY by listing after the sweep"
    else
      log "  STILL NOT EMPTY after destroy and sweep - see the listing above; this run_id is $RUN_ID, retry teardown with the same RUN_ID"
    fi
  fi

  if [ "$TARGET" = "floci" ]; then
    docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  [ -n "$CURRENT_STAGE" ] && gauntlet_stage "$CURRENT_STAGE" fail "$*"
  exit 1
}

# APPLY_PID names whichever foreground-ish child is currently in flight, so
# on_signal (INT/TERM) can forward the signal to it before tearing down - the
# same "background the risky command, trap forwards, then wait" shape that
# makes bash respond to a trap promptly instead of only after a foreground
# pipeline returns (confirmed empirically while building this script,
# 2026-08-29: a plain foreground `terraform apply` delayed the trap until the
# command finished; backgrounding it and using `wait` did not).
APPLY_PID=""
on_signal() {
  local sig="$1"
  log "=== caught $sig - forwarding to in-flight child (pid ${APPLY_PID:-none}) and tearing down ==="
  if [ -n "$APPLY_PID" ] && kill -0 "$APPLY_PID" 2>/dev/null; then
    kill -TERM "$APPLY_PID" 2>/dev/null || true
    wait "$APPLY_PID" 2>/dev/null || true
  fi
  teardown
  trap - EXIT INT TERM
  exit 130
}
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap teardown EXIT
gauntlet_begin

# ── 0. tools ────────────────────────────────────────────────────────────
log "=== 0. tools (target=$TARGET run_id=$RUN_ID) ==="
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v "${TF_COLD_BIN:-terraform}" >/dev/null 2>&1 || fail "${TF_COLD_BIN:-terraform} is not on PATH (needed for cold_deploy's stock apply)"
TF_COLD="${TF_COLD_BIN:-terraform}"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH (needed to build choudoufu the same way live/e2e scripts do)"
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

if [ "$TARGET" = "floci" ]; then
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
  docker info >/dev/null 2>&1 || fail "docker is not running"
fi

# ── 0b. the endpoint ────────────────────────────────────────────────────
if [ "$TARGET" = "floci" ]; then
  log "=== 0b. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
  docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
    || fail "docker run for $FLOCI_NAME failed"
  healthy=0
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
    case "$H" in *'"ec2":"running"'*) healthy=1; break ;; esac
    sleep 2
  done
  [ "$healthy" = "1" ] || fail "floci did not come up healthy (ec2) at $ENDPOINT"
  log "  healthy"
  export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"
else
  log "=== 0b. target=aws, region=$REGION - using the ambient AWS credential chain, no endpoint override ==="
  unset AWS_ENDPOINT_URL || true
  export AWS_REGION="$REGION"
  IDENTITY="$(aws sts get-caller-identity --query Account --output text 2>&1)" \
    || fail "aws sts get-caller-identity failed - no usable credentials for a real run: $IDENTITY"
  log "  caller account ...${IDENTITY: -4} (only the last 4 digits are ever logged or recorded)"
fi

# ── 1. resolve a real AMI (#440 blocker 1) ─────────────────────────────
log "=== 1. resolving the Amazon Linux AMI via SSM (live-cert.sh: livecert_ami) ==="
AMI="$(livecert_ami)" || fail "AMI resolution failed"
log "  resolved $AMI for $REGION"

resource_block() {
  cat <<EOF
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = {
    Name           = "ec2-reference-vpc"
    tofu-cert-run  = "$RUN_ID"
  }
}

resource "aws_subnet" "main" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.1.0/24"
  availability_zone       = "${REGION}a"
  map_public_ip_on_launch = true
  tags = {
    Name           = "ec2-reference-subnet"
    tofu-cert-run  = "$RUN_ID"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags = {
    Name           = "ec2-reference-igw"
    tofu-cert-run  = "$RUN_ID"
  }
}

resource "aws_security_group" "main" {
  name        = "ec2-reference-sg-$RUN_ID"
  description = "Allow SSH (live-cert #440, run \$RUN_ID)"
  vpc_id      = aws_vpc.main.id

  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name           = "ec2-reference-sg"
    tofu-cert-run  = "$RUN_ID"
  }
}

resource "aws_instance" "main" {
  ami                    = "$AMI"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.main.id
  vpc_security_group_ids = [aws_security_group.main.id]

  tags = {
    Name           = "ec2-reference-instance"
    tofu-cert-run  = "$RUN_ID"
  }
}
EOF
}

provider_block() {
  if [ "$TARGET" = "floci" ]; then
    cat <<EOF
provider "aws" {
  region                      = "$REGION"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
EOF
  else
    cat <<EOF
provider "aws" {
  region = "$REGION"
  default_tags {
    tags = {
      tofu-cert-run = "$RUN_ID"
    }
  }
}
EOF
  fi
}

# ══════════════════════════════════════════════════════════════════════
# cold_deploy: stock applies the unmodified configuration for real.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=cold_deploy
mkdir -p "$COLD_DIR"
{
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

EOF
  provider_block
  echo
  resource_block
} > "$COLD_DIR/main.tf"

log "=== 2. cold_deploy: $TF_COLD init ==="
( cd "$COLD_DIR" && "$TF_COLD" init -input=false -no-color ) > "$WORK/cold_deploy_init.out" 2>&1 \
  || { tail -20 "$WORK/cold_deploy_init.out"; fail "stock init failed"; }

log "=== 2b. cold_deploy: $TF_COLD apply (backgrounded so a signal can interrupt it - see on_signal above) ==="
( cd "$COLD_DIR" && "$TF_COLD" apply -input=false -auto-approve -no-color -parallelism=1 ) \
  > "$WORK/cold_deploy_apply.out" 2>&1 &
APPLY_PID=$!
wait "$APPLY_PID"
APPLY_RC=$?
APPLY_PID=""
[ "$APPLY_RC" -eq 0 ] || { tail -30 "$WORK/cold_deploy_apply.out"; fail "stock apply exited $APPLY_RC"; }
grep -qE 'Apply complete! Resources: 5 added' "$WORK/cold_deploy_apply.out" \
  || { grep -E 'Apply complete' "$WORK/cold_deploy_apply.out"; fail "stock apply did not create exactly 5 resources"; }
[ -f "$COLD_DIR/terraform.tfstate" ] || fail "stock apply left no state file to migrate from"
log "  $(grep -E 'Apply complete' "$WORK/cold_deploy_apply.out")"
gauntlet_stage cold_deploy pass "5 resources from stock $TF_COLD against $TARGET, AMI $AMI, tofu-cert-run=$RUN_ID"

# ══════════════════════════════════════════════════════════════════════
# migrate: choudoufu live-import -approve against the stock state file.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
mkdir -p "$ADOPTED_DIR"
{
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

EOF
  provider_block
  echo
  resource_block
} > "$ADOPTED_DIR/main.tf"

log "=== 3. migrate: choudoufu init + live-import (dry run, then -approve) ==="
( cd "$ADOPTED_DIR" && "$TOFU" init -input=false -no-color ) > "$WORK/migrate_init.out" 2>&1 \
  || { tail -20 "$WORK/migrate_init.out"; fail "adopted init failed"; }

IMPORT_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) failed"; }
grep -qF "5 of 5 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify all 5 resources as eligible"; }

APPROVE_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve failed"; }
grep -qF "5 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 0 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 5 resources cleanly"; }
MIGRATE_DONE=1
log "  5 of 5 stamped"
gauntlet_stage migrate pass "5 of 5 verified, 5 stamped, 0 skipped"

# ══════════════════════════════════════════════════════════════════════
# test_plan: replan from nothing; identities checked against the AWS CLI.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
log "=== 4. test_plan: choudoufu plan must be empty ==="
PLAN_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "the post-migrate plan exited $PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "the post-migrate plan is not empty"; }

log "=== 4b. test_plan: rendered identity checked against the AWS CLI directly ==="
INSTANCE_ID="$(livecert_aws ec2 describe-instances \
  --filters "Name=tag:tofu-cert-run,Values=$RUN_ID" "Name=instance-state-name,Values=running,pending" \
  --query "Reservations[0].Instances[0].InstanceId" --output text)"
[ -n "$INSTANCE_ID" ] && [ "$INSTANCE_ID" != "None" ] || fail "no live instance found by its tofu-cert-run tag"
ADDR_TAG="$(livecert_aws ec2 describe-tags \
  --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-address" \
  --query "Tags[0].Value" --output text)"
[ "$ADDR_TAG" = "aws_instance.main" ] || fail "the instance carries tofu-address=$ADDR_TAG, not aws_instance.main - identity read via the AWS CLI, not choudoufu's own report"
EST_TAG="$(livecert_aws ec2 describe-tags \
  --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=tofu-estate" \
  --query "Tags[0].Value" --output text)"
[ "$EST_TAG" = "$ESTATE" ] || fail "the instance carries tofu-estate=$EST_TAG, not $ESTATE"
log "  instance $INSTANCE_ID: tofu-address=$ADDR_TAG tofu-estate=$EST_TAG, read via the AWS CLI"
gauntlet_stage test_plan pass "post-migrate plan is empty; aws_instance.main's tofu-address/tofu-estate confirmed via the AWS CLI directly"

# ══════════════════════════════════════════════════════════════════════
# test_apply: applying the empty plan is a genuine no-op.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
log "=== 5. test_apply: the empty plan applies as a genuine no-op ==="
BEFORE_N="$(livecert_aws resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
NOOP_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_RC=$?
[ "$NOOP_RC" -eq 0 ] || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply exited $NOOP_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_OUT" \
  || { grep -E 'Apply complete' <<< "$NOOP_OUT"; fail "the no-op apply was not a genuine no-op"; }
AFTER_N="$(livecert_aws resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N"

CURRENT_STAGE=""
gauntlet_end
log "=== all four stages passed against target=$TARGET; teardown runs next via the EXIT trap ==="
