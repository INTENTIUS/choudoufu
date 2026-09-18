#!/usr/bin/env bash
# The example, actually run (issue #1059).
#
# Everything else in this project asserts what the files say. This runs them:
# `chant run estates-apply` against the pinned floci emulator, and then four
# checks that read the emulator and the disk directly rather than believing
# choudoufu's own report.
#
#   1. the Op applies the network estate, then the service estate
#   2. the subnet's real VpcId, read with the AWS CLI, IS the producer's VPC id
#   3. there is no state file on either side, and the id is nowhere on disk
#      once the plan file and the (ownership-irrelevant) state cache are gone
#   4. the service root re-resolves the id on a second plan, from that empty
#      starting point, and finds nothing to change
#
# One verdict line per check, greppable:
#
#     PROVE check=<name> verdict=<pass|fail> <detail>
#
# Env:
#   CHOUDOUFU_BIN   an existing choudoufu binary; default builds ./cmd/choudoufu
#   FLOCI_PORT      host port for the emulator (default: a free one the kernel picks)
#   FLOCI_IMAGE     override the pin in live/floci-image
#   PROVE_ENDPOINT  an emulator already up; set it and this starts no container
#   KEEP            1 leaves the scratch project (and the container) up
#   BREAK           1 breaks check 2 on purpose, to prove the check can fail
#
# Why a scratch copy rather than this worktree: `chant run` writes under the
# project directory, and the roots here are checked-in example sources. The
# copy also gets its own git repository with no remote, so chant's lifecycle
# push has nothing to reach even if a future Op here grows a gate.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT="$(cd "$EXAMPLE_DIR/../.." && pwd)"

FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
CONTAINER="cross-estate-prove-$$"
WORK=""
FAILURES=0
PASSES=0

log()  { echo "  $*"; }
step() { echo; echo "=== $* ==="; echo; }
die()  { echo "PROVE fatal: $*" >&2; exit 1; }

cleanup() {
  if [ "${KEEP:-0}" = "1" ]; then
    echo "KEEP=1: container $CONTAINER and scratch project $WORK left up." >&2
    return
  fi
  [ -z "${PROVE_ENDPOINT:-}" ] && docker rm -f "$CONTAINER" >/dev/null 2>&1
  [ -n "$WORK" ] && rm -rf "$WORK"
  return 0
}
trap cleanup EXIT

verdict() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" = "1" ]; then
    PASSES=$((PASSES + 1)); echo "PROVE check=$name verdict=pass $detail"
  else
    FAILURES=$((FAILURES + 1)); echo "PROVE check=$name verdict=fail $detail"
  fi
}

VPC_CIDR="10.90.0.0/16"
SUBNET_CIDR="10.90.1.0/24"
NETWORK_ESTATE="cross-estate-network"
SERVICE_ESTATE="cross-estate-service"

# ---------------------------------------------------------------- preflight

command -v node >/dev/null 2>&1 || die "node is not installed"
command -v git  >/dev/null 2>&1 || die "git is not installed"
command -v aws  >/dev/null 2>&1 || die "the aws CLI is not installed (check 2 reads the emulator with it)"
if [ -z "${PROVE_ENDPOINT:-}" ]; then
  command -v docker >/dev/null 2>&1 || die "docker is not installed"
  docker info >/dev/null 2>&1 || die "the docker daemon is not running"
fi

step "the binary under test"
if [ -n "${CHOUDOUFU_BIN:-}" ]; then
  [ -x "$CHOUDOUFU_BIN" ] || die "CHOUDOUFU_BIN=$CHOUDOUFU_BIN is not executable"
  CHOUDOUFU_BIN="$(cd "$(dirname "$CHOUDOUFU_BIN")" && pwd)/$(basename "$CHOUDOUFU_BIN")"
else
  command -v go >/dev/null 2>&1 || die "no CHOUDOUFU_BIN and no go toolchain to build one"
  BIN_DIR="$(mktemp -d)"
  log "building ./cmd/choudoufu from this worktree"
  ( cd "$ROOT" && env -u PWD go build -o "$BIN_DIR/choudoufu" ./cmd/choudoufu ) || die "go build failed"
  CHOUDOUFU_BIN="$BIN_DIR/choudoufu"
fi
log "$("$CHOUDOUFU_BIN" version | head -1)"
export PATH="$(dirname "$CHOUDOUFU_BIN"):$PATH"

# ------------------------------------------------------------- the emulator

step "the emulator"
if [ -n "${PROVE_ENDPOINT:-}" ]; then
  ENDPOINT="$PROVE_ENDPOINT"
  log "using the emulator already up at $ENDPOINT"
else
  # A port the kernel picks, so several of these can run at once and none of
  # them collides with a long-running container someone else started.
  FLOCI_PORT="${FLOCI_PORT:-$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}"
  docker run -d --rm --name "$CONTAINER" -p "127.0.0.1:$FLOCI_PORT:4566" "$FLOCI_IMAGE" >/dev/null \
    || die "could not start $FLOCI_IMAGE"
  ENDPOINT="http://localhost:$FLOCI_PORT"
  log "$FLOCI_IMAGE starting at $ENDPOINT"
fi
ready=0
for _ in $(seq 1 90); do
  curl -fsS "$ENDPOINT/_localstack/health" >/dev/null 2>&1 && { ready=1; break; }
  sleep 2
done
[ "$ready" = "1" ] || die "floci never answered on $ENDPOINT/_localstack/health"
log "emulator ready"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1
export TF_VAR_aws_region=us-east-1
export CHECKPOINT_DISABLE=1

# --------------------------------------------------------- the scratch copy

step "the project, in a repository with no remote"
WORK="$(mktemp -d)"
tar -C "$EXAMPLE_DIR" -cf - \
  --exclude=node_modules --exclude=.terraform --exclude=dist \
  --exclude='*.tfstate*' --exclude='*.tfplan' . | tar -C "$WORK" -xf -
if [ -d "$EXAMPLE_DIR/node_modules" ]; then
  cp -R "$EXAMPLE_DIR/node_modules" "$WORK/node_modules"
else
  ( cd "$WORK" && npm ci --no-audit --no-fund >/dev/null ) || die "npm ci failed"
fi
if [ "${BREAK:-0}" = "1" ]; then
  # Point the consumer at an estate nothing ever applied. The data source then
  # resolves nothing, the Service phase fails, and check 2 has no subnet to
  # find - which is the failure the checks below must actually report.
  sed -i.bak 's/default     = "cross-estate-network"/default     = "cross-estate-nowhere"/' \
    "$WORK/terraform/service/main.tf" && rm -f "$WORK/terraform/service/main.tf.bak"
  log "BREAK=1: the consumer now filters on estate \"cross-estate-nowhere\""
fi
(
  cd "$WORK"
  git init -q .
  git config user.email prove@example.invalid
  git config user.name "cross-estate prove"
  git add -A >/dev/null && git commit -qm "the cross-estate-dependency example, for the proof"
) || die "could not stand up the scratch repository"
git -C "$WORK" remote | grep -q . && die "the scratch repository has a remote"
log "$WORK"

OUT="$WORK/.prove"; mkdir -p "$OUT"

# --------------------------------------------------- 1. the Op, in one run

step "1. chant run estates-apply (Network, then Service)"
( cd "$WORK" && npx chant run estates-apply ) > "$OUT/apply.log" 2>&1
APPLY_RC=$?
sed 's/^/    /' "$OUT/apply.log" | tail -40
if [ "$APPLY_RC" -eq 0 ]; then
  verdict "op-applies-both-estates" 1 "rc=0"
else
  verdict "op-applies-both-estates" 0 "rc=$APPLY_RC (full log: $OUT/apply.log)"
fi

# ------------------------------------------ 2. the value, read independently

step "2. the subnet's real VpcId is the producer's VPC id"
q() { aws "$@" --output text 2>/dev/null; }
VPC_ID="$(q ec2 describe-vpcs --filters "Name=cidr,Values=$VPC_CIDR" --query 'Vpcs[0].VpcId')"
SUBNET_ID="$(q ec2 describe-subnets --filters "Name=cidr-block,Values=$SUBNET_CIDR" --query 'Subnets[0].SubnetId')"
SUBNET_VPC_ID="$(q ec2 describe-subnets --subnet-ids "$SUBNET_ID" --query 'Subnets[0].VpcId')"
VPC_ESTATE="$(q ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=tofu-estate" --query 'Tags[0].Value')"
SUBNET_ESTATE="$(q ec2 describe-tags --filters "Name=resource-id,Values=$SUBNET_ID" "Name=key,Values=tofu-estate" --query 'Tags[0].Value')"
log "producer VPC:        $VPC_ID (tofu-estate=$VPC_ESTATE)"
log "consumer subnet:     $SUBNET_ID (tofu-estate=$SUBNET_ESTATE)"
log "that subnet's VpcId: $SUBNET_VPC_ID"
if [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] && [ "$SUBNET_VPC_ID" = "$VPC_ID" ]; then
  verdict "value-crossed-estates" 1 "subnet=$SUBNET_ID vpc=$VPC_ID"
else
  verdict "value-crossed-estates" 0 "subnet_vpc=$SUBNET_VPC_ID want=$VPC_ID"
fi
if [ "$VPC_ESTATE" = "$NETWORK_ESTATE" ] && [ "$SUBNET_ESTATE" = "$SERVICE_ESTATE" ]; then
  verdict "two-estates-two-owners" 1 "$VPC_ESTATE / $SUBNET_ESTATE"
else
  verdict "two-estates-two-owners" 0 "$VPC_ESTATE / $SUBNET_ESTATE want $NETWORK_ESTATE / $SERVICE_ESTATE"
fi

# ------------------------------------------------- 3. no stored copy, at all

step "3. no state file, and no copy of the id left on disk"
STATE="$(find "$WORK/terraform" -name '*.tfstate' -o -name '*.tfstate.backup' -o -name 'terraform.tfstate.d' \
         | grep -v '/\.terraform/choudoufu-cache\.tfstate$')"
if [ -z "$STATE" ]; then
  verdict "no-state-file" 1 "no terraform.tfstate under either root"
else
  verdict "no-state-file" 0 "found: $(echo "$STATE" | tr '\n' ' ')"
fi

# The two places the id legitimately IS on disk after a run, neither of which
# is consulted for ownership: the saved plan, and choudoufu's state cache
# (live/, issue #685 - a candidate verified against the tag index every run,
# never a fact trusted, which is why deleting it changes no plan). Remove
# both and the consumer has nothing left to read the id out of but the cloud.
rm -f "$WORK/terraform/service/chant.tfplan" "$WORK/terraform/network/chant.tfplan"
rm -f "$WORK/terraform"/*/.terraform/choudoufu-cache.tfstate
LEFT="$(grep -rl -- "$VPC_ID" "$WORK/terraform/service" 2>/dev/null)"
if [ -z "$LEFT" ]; then
  verdict "no-stored-copy" 1 "$VPC_ID appears in no file under terraform/service"
else
  verdict "no-stored-copy" 0 "still on disk in: $(echo "$LEFT" | tr '\n' ' ')"
fi

# ---------------------------------------- 4. the consumer resolves it again

step "4. the service root re-resolves the id from nothing, and is clean"
( cd "$WORK/terraform/service" && choudoufu plan -no-color -input=false ) > "$OUT/replan.log" 2>&1
REPLAN_RC=$?
sed 's/^/    /' "$OUT/replan.log" | tail -25
if [ "$REPLAN_RC" -eq 0 ] && grep -q "No changes" "$OUT/replan.log"; then
  verdict "reresolves-clean" 1 "second plan: No changes"
else
  verdict "reresolves-clean" 0 "rc=$REPLAN_RC, no 'No changes' line (full log: $OUT/replan.log)"
fi

# ------------------------------------------------------------------ verdict

step "verdict"
echo "PROVE passes=$PASSES failures=$FAILURES"
[ "$FAILURES" -eq 0 ] || exit 1
