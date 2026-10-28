#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-eks examples/eks-managed-node-group
# (tag v21.29.0, commit 91f11ea01656c26b8e6b8eb1b74f19863f60056c, fetched
# into .corpus/eks-v21 by `just corpus-fetch` from live/corpus-manifest.json),
# crossed against floci's real EKS mode. #2291 asked for an eks row on a
# current major: corpus-eks-basic stays at v9.0.0 (self-managed worker
# groups, aws-auth), and v21 has no examples/basic to repin it to, so this
# is a new estate beside it. This is the shape most people deploy today:
# a managed node group on AL2023, cluster add-ons, and access entries in
# place of the aws-auth ConfigMap.
#
# This script has not been run. Its first run is the maintainer's
# (#2291), so it asserts no resource counts it has never seen: each stage
# records the counts it read, and fails only on what is wrong whatever the
# count is (an apply error, a marker present before migration, a stamped
# count the AWS CLI disagrees with, a non-empty replan, a drift fix that
# touches more than the tampered object). After the first run, pin the
# counts it printed as exact expectations, the way corpus-eks-basic does.
#
# The static tier (live/module-compat.json, this example at v21.29.0)
# reads refused: moved-block (16 sites), "Data source not readable before
# resolution" (6), "Identity not resolvable from configuration" (2) and
# unadmitted-type (2). Expect stage 2 or 3 to stop on those; a stop is a
# measured refusal, recorded under its own id, never a script error.
#
# Deltas from the published example, each needed before choudoufu is
# involved, and each applied identically to the plain and adopted copies:
#   1. eks-bottlerocket.tf is dropped. The example builds two clusters that
#      differ only in AMI family; floci's real EKS mode starts one k3s
#      sibling container per cluster, and AL2023 is the default AMI type
#      for managed node groups since Kubernetes 1.30, so the AL2023 cluster
#      is kept and the Bottlerocket twin is not.
#   2. module "eks_al2023" reads source = "../.." (the corpus checkout at
#      the pinned commit) in place of the registry's "~> 21.0", so the run
#      measures v21.29.0 and not whatever 21.x the registry serves that day.
#   3. module "vpc" is pinned to "6.6.1", the tag .corpus/vpc is fetched at,
#      in place of "~> 6.0", for the same reason.
#   4. provider "aws" gains test credentials and the skip_* flags; endpoints
#      come from AWS_ENDPOINT_URL, which every container below sets.
#   5. hashicorp/aws is pinned exactly by gauntlet_pin_aws_provider.
#   6. The adopted copy only: a live block naming this estate.
#
# Stages run: cold_deploy, migrate, test_plan, test_apply, drift_reconverge.
# Every other active stage reports not_run, named below, until the first
# run shows how far this estate gets.
#
# Env overrides, same as every other crossing:
#   TOFU_BIN      a prebuilt choudoufu binary. Used only when it is a static
#                 linux/amd64 ELF (choudoufu runs inside linux/amd64
#                 containers here); otherwise this script builds one.
#   FLOCI_PORT    host port for the emulator (default 4722).
#   FLOCI_IMAGE   the emulator image; defaults to the digest in live/floci-image.
#   BREAK         set to 1 to corrupt an assertion and prove it is load-bearing.
# Needs Docker (with /var/run/docker.sock: floci's real EKS mode spawns a
# k3s sibling container), the AWS CLI, jq and python3.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/eks-v21"
EXAMPLE="examples/eks-managed-node-group"
WORK="$(mktemp -d)"
NET="choudoufu-corpus-eks-managed-node-group-net-$$"
FLOCI_PORT="${FLOCI_PORT:-4722}"
FLOCI_NAME="choudoufu-corpus-eks-managed-node-group-$$"
FLOCI_NS="${FLOCI_NS:-eksm$$}"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
TOOLBOX_IMAGE="choudoufu-corpus-eks-managed-node-group-toolbox:$$"

ESTATE="eks-mng-crossing"
# The example's own region (main.tf's local.region).
REGION="eu-west-1"

PLAIN_REL="plain/eks/$EXAMPLE"
ADOPTED_REL="adopted/eks/$EXAMPLE"
PLAIN="$WORK/$PLAIN_REL"
ADOPTED="$WORK/$ADOPTED_REL"

cleanup() {
  local ns_children c iid
  ns_children="$(docker ps -a --filter "name=floci-${FLOCI_NS}-" --format '{{.Names}}' 2>/dev/null)"
  for c in $ns_children; do
    case "$c" in
      floci-"${FLOCI_NS}"-ec2-*)
        iid="${c#floci-${FLOCI_NS}-ec2-}"
        docker ps -aq --filter "name=floci-ec2-fwd-${iid}-" 2>/dev/null \
          | xargs -r docker rm -f >/dev/null 2>&1 || true
        ;;
    esac
  done
  if [ -n "$ns_children" ]; then
    docker rm -f $ns_children >/dev/null 2>&1 || true
  fi
  gauntlet_floci_teardown "$FLOCI_NAME"
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker rmi -f "$TOOLBOX_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/gauntlet.sh"

gauntlet_plugin_cache
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
gauntlet_begin
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

TF_ORACLE_VERSION="$(gauntlet_oracle_pin terraform_version)"
[ -n "$TF_ORACLE_VERSION" ] || fail "could not read terraform_version from live/oracle-versions.json"

AS_HOST_USER=(--user "$(id -u):$(id -g)" -e HOME=/work/.home)
PLUGIN_CACHE_IN_CONTAINER=(-v "$TF_PLUGIN_CACHE_DIR:$TF_PLUGIN_CACHE_DIR" -e TF_PLUGIN_CACHE_DIR -e TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE)

# Stock terraform at the oracle pin, for the cold deploy.
terraform_run() {
  docker run --rm --platform linux/amd64 --network "$NET" "${AS_HOST_USER[@]}" \
    "${PLUGIN_CACHE_IN_CONTAINER[@]}" \
    -v "$WORK:/work" -w "/work/$PLAIN_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    "hashicorp/terraform:${TF_ORACLE_VERSION}" "$@"
}

# choudoufu, inside the same network so the EKS endpoint floci hands out
# (a k3s container on $NET) is reachable.
tofu_run() {
  local rel="$1"; shift
  docker run --rm --platform linux/amd64 --network "$NET" "${AS_HOST_USER[@]}" \
    "${PLUGIN_CACHE_IN_CONTAINER[@]}" \
    -v "$WORK:/work" -w "/work/$rel" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    "$TOOLBOX_IMAGE" /work/bin/choudoufu "$@"
}

log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v jq >/dev/null 2>&1 || fail "jq is not on PATH"
[ -d "$SRC/$EXAMPLE" ] || fail "$SRC/$EXAMPLE is missing - run 'just corpus-fetch' first"

mkdir -p "$WORK/bin" "$WORK/.home"

is_static_elf64_amd64() {
  [ -f "$1" ] || return 1
  local desc
  desc="$(file -b "$1" 2>/dev/null)"
  grep -q 'ELF 64-bit.*x86-64.*statically linked' <<< "$desc"
}

if [ -n "${TOFU_BIN:-}" ] && is_static_elf64_amd64 "$TOFU_BIN"; then
  cp "$TOFU_BIN" "$WORK/bin/choudoufu"
  log "  using TOFU_BIN=$TOFU_BIN (static linux/amd64)"
else
  if [ -n "${TOFU_BIN:-}" ]; then
    log "  TOFU_BIN=$TOFU_BIN is not a static linux/amd64 binary; building one from this tree"
    log "  (choudoufu runs inside --platform linux/amd64 containers whatever the host is)"
  fi
  ( cd "$ROOT" && env -u PWD GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$WORK/bin/choudoufu" ./cmd/choudoufu ) \
    || fail "go build ./cmd/choudoufu (linux/amd64) failed"
  log "  built linux/amd64 $WORK/bin/choudoufu"
fi
chmod +x "$WORK/bin/choudoufu"

docker network create "$NET" >/dev/null || fail "docker network create failed"
cat > "$WORK/Dockerfile.toolbox" <<'EOF'
FROM alpine:3.20
RUN apk add --no-cache git ca-certificates
EOF
docker build --platform linux/amd64 -q -f "$WORK/Dockerfile.toolbox" -t "$TOOLBOX_IMAGE" "$WORK" >/dev/null \
  || fail "toolbox image build failed"
log "  network $NET and toolbox image ready"

mkdir -p "$WORK/plain" "$WORK/adopted"
cp -R "$SRC" "$WORK/plain/eks"
cp -R "$SRC" "$WORK/adopted/eks"
rm -rf "$WORK/plain/eks/.git" "$WORK/plain/eks/.github" "$WORK/adopted/eks/.git" "$WORK/adopted/eks/.github"
[ -f "$PLAIN/main.tf" ] || fail "the estate copy is missing $EXAMPLE/main.tf"
log "  eks module at v21.29.0 + $EXAMPLE copied out of .corpus into $WORK (plain + adopted)"

log "=== 1. deltas (see the header for why each one is needed) ==="
apply_deltas() {
  local est="$1" with_live="$2"

  # 1. one cluster, AL2023.
  [ -f "$est/eks-bottlerocket.tf" ] || fail "eks-bottlerocket.tf is missing from $est - the corpus pin has moved"
  rm -f "$est/eks-bottlerocket.tf"

  # 2. the eks module from the pinned corpus checkout.
  perl -0pi -e 's/(module "eks_al2023" \{\n)  source  = "terraform-aws-modules\/eks\/aws"\n  version = "~> 21\.0"\n/$1  source = "..\/.."\n/' "$est/eks-al2023.tf"
  grep -q 'source = "../.."' "$est/eks-al2023.tf" \
    || fail "the eks module source delta did not match eks-al2023.tf - the corpus pin has moved"

  # 3. the vpc module at .corpus/vpc's tag.
  perl -0pi -e 's/(source  = "terraform-aws-modules\/vpc\/aws"\n  version = )"~> 6\.0"/$1"6.6.1"/' "$est/main.tf"
  grep -q 'version = "6.6.1"' "$est/main.tf" \
    || fail "the vpc module version delta did not match main.tf - the corpus pin has moved"

  # 4. emulator credentials on the provider block.
  perl -0pi -e 's/(provider "aws" \{\n  region = local\.region\n)\}/$1\n  access_key                  = "test"\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n}/' "$est/main.tf"
  grep -q 's3_use_path_style' "$est/main.tf" \
    || fail "the emulator provider delta did not match main.tf - the corpus pin has moved"

  # 5. hashicorp/aws pinned exactly.
  gauntlet_pin_aws_provider "$est/versions.tf" \
    || fail "gauntlet_pin_aws_provider failed for $est/versions.tf"

  # 6. the live block, adopted copy only.
  if [ "$with_live" = "1" ]; then
    perl -0pi -e 's/(terraform \{\n  required_version = "[^"]*"\n)/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n/' "$est/versions.tf"
    grep -q "estate = \"$ESTATE\"" "$est/versions.tf" \
      || fail "the live block delta did not match versions.tf - the corpus pin has moved"
  fi
}

apply_deltas "$PLAIN" 0
apply_deltas "$ADOPTED" 1
for f in main.tf eks-al2023.tf; do
  diff -q "$PLAIN/$f" "$ADOPTED/$f" >/dev/null || fail "plain and adopted $f differ - a delta leaked between copies"
done
DIFF_LINES="$(diff "$PLAIN/versions.tf" "$ADOPTED/versions.tf" | grep -c '^[<>]' || true)"
[ "$DIFF_LINES" -eq 4 ] || fail "plain and adopted versions.tf differ by $DIFF_LINES lines, expected exactly 4 (the live block)"
log "  deltas applied identically to both copies; only the live block differs ($DIFF_LINES lines)"

log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE), real EKS mode ==="
[ -S /var/run/docker.sock ] || fail "no /var/run/docker.sock to mount - floci's EKS real mode needs it to spawn k3s"
gauntlet_floci_start "$FLOCI_NAME" --network "$NET" -p "${FLOCI_PORT}:4566" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=network \
  -e "FLOCI_SERVICES_EKS_DOCKER_NETWORK=$NET" \
  -e "FLOCI_DOCKER_RESOURCE_NAMESPACE=$FLOCI_NS" \
  "$FLOCI_IMAGE" \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"eks"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"eks"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (eks) at $ENDPOINT"
log "  healthy; EKS real mode wired to network $NET"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ── STAGE 1: cold deploy, stock terraform, no live block ────────────────────
gauntlet_begin_stage cold_deploy
log "=== 3. STAGE 1 - cold deploy: terraform $TF_ORACLE_VERSION apply, no live block ==="
gauntlet_locked_init terraform_run init -input=false -no-color > "$WORK/init.log" 2>&1 || {
  tail -40 "$WORK/init.log"; fail "terraform init failed"; }
APPLY_OUT="$(terraform_run apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -60
  fail "the cold apply failed: $(gauntlet_first_error_line <<< "$APPLY_OUT")"
}
COLD_ADDED="$(grep -oE 'Apply complete! Resources: [0-9]+ added' <<< "$APPLY_OUT" | grep -oE '[0-9]+' | head -n1)"
[ -n "$COLD_ADDED" ] && [ "$COLD_ADDED" -gt 0 ] || fail "the cold apply printed no \"Apply complete! Resources: N added\" line"
[ -f "$PLAIN/terraform.tfstate" ] || fail "no terraform.tfstate was written by the cold apply"
STATE_INSTANCES="$(jq '[.resources[] | select(.mode == "managed") | .instances[]] | length' "$PLAIN/terraform.tfstate")"
[ "$STATE_INSTANCES" = "$COLD_ADDED" ] || fail "the apply added $COLD_ADDED but the state records $STATE_INSTANCES managed instances"

CLUSTER_NAME="$(gauntlet_first_match '.clusters[]' awsl eks list-clusters)"
[ -n "$CLUSTER_NAME" ] && [ "$CLUSTER_NAME" != "None" ] || fail "no EKS cluster found through the AWS CLI after the cold apply"
CLUSTER_STATUS="$(awsl eks describe-cluster --name "$CLUSTER_NAME" --query 'cluster.status' --output text)"
[ "$CLUSTER_STATUS" = "ACTIVE" ] || fail "cluster $CLUSTER_NAME is $CLUSTER_STATUS, not ACTIVE"
NODEGROUPS="$(awsl eks list-nodegroups --cluster-name "$CLUSTER_NAME" --output json | jq '.nodegroups | length')"
[ "${NODEGROUPS:-0}" -ge 1 ] 2>/dev/null || fail "cluster $CLUSTER_NAME lists $NODEGROUPS managed node groups through the AWS CLI, expected at least 1"
ACCESS_ENTRIES="$(awsl eks list-access-entries --cluster-name "$CLUSTER_NAME" --output json 2>/dev/null | jq '.accessEntries | length')" || ACCESS_ENTRIES="unread"
MARKED="$(gauntlet_tagged_count awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-address" 2>/dev/null || echo 0)"
[ "$MARKED" = "0" ] || fail "expected 0 objects carrying a tofu-address tag before migration, got $MARKED - this test proves nothing"
log "  $COLD_ADDED instances; cluster $CLUSTER_NAME ACTIVE with $NODEGROUPS managed node group(s) and $ACCESS_ENTRIES access entr(y/ies), read through the AWS CLI; 0 tofu-address tags"
gauntlet_stage cold_deploy pass "$COLD_ADDED resource instances from terraform $TF_ORACLE_VERSION, genuinely cold and unmarked; cluster $CLUSTER_NAME ACTIVE with $NODEGROUPS managed node group(s) and $ACCESS_ENTRIES access entr(y/ies), read through the AWS CLI"

NS_CHILDREN="$(docker ps -a --filter "name=floci-${FLOCI_NS}-" --format '{{.Names}}' 2>/dev/null)"
[ -n "$NS_CHILDREN" ] \
  || fail "no floci child container carries this run's namespace $FLOCI_NS - FLOCI_DOCKER_RESOURCE_NAMESPACE was not honoured, so cleanup()'s run-scoped sweep would leak every sibling this run created"
log "  run-scoped floci children after the cold apply: $(printf '%s ' $NS_CHILDREN)"

# ── STAGE 2: migrate ─────────────────────────────────────────────────────────
gauntlet_begin_stage migrate
log "=== 4. STAGE 2 - migrate: choudoufu live-import against the cold state ==="
tofu_run "$ADOPTED_REL" init -input=false -no-color > "$WORK/tofu-init.log" 2>&1 || {
  tail -40 "$WORK/tofu-init.log"; fail "choudoufu init failed: $(gauntlet_first_error_line < "$WORK/tofu-init.log")"; }
IMPORT_OUT="$(tofu_run "$ADOPTED_REL" live-import -state="/work/$PLAIN_REL/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"; IMPORT_RC=$?
if [ -n "${DUMP_IMPORT:-}" ]; then printf '%s\n' "$IMPORT_OUT" > "$DUMP_IMPORT"; fi
[ "$IMPORT_RC" -eq 0 ] || {
  printf '%s\n' "$IMPORT_OUT" | tail -60
  fail "live-import -approve exited $IMPORT_RC: $(gauntlet_first_error_line <<< "$IMPORT_OUT")"
}
ELIGIBLE_LINE="$(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -n1)"
STAMPED_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$IMPORT_OUT" | head -n1)"
[ -n "$STAMPED_LINE" ] || fail "live-import printed no \"newly stamped\" summary line"
STAMPED_N="$(grep -oE '^[0-9]+ resource\(s\) newly stamped' <<< "$STAMPED_LINE" | grep -oE '^[0-9]+')"
FAILED_N="$(grep -oE '[0-9]+ failed' <<< "$STAMPED_LINE" | grep -oE '^[0-9]+')"
[ "${FAILED_N:-1}" = "0" ] || fail "live-import reports $FAILED_N failed: $STAMPED_LINE"
[ "${STAMPED_N:-0}" -gt 0 ] || fail "live-import stamped nothing: $STAMPED_LINE"

gauntlet_estate_objects "$ESTATE" awsl \
  || fail "could not read the account's tofu-estate=$ESTATE inventory after migration"
MARKED_AFTER="$GAUNTLET_ESTATE_N"
if [ "${BREAK:-}" = "1" ]; then
  MARKED_AFTER=$((MARKED_AFTER + 1))
  log "  BREAK=1: counting one object more than the AWS CLI returned; the check below must fail"
fi
# Every stamped object carries tofu-estate except the ones whose marker is
# not an AWS tag (a Kubernetes label on an in-cluster object). This example
# declares no kubernetes or helm provider, so every stamp is an AWS tag.
[ "$MARKED_AFTER" = "$STAMPED_N" ] \
  || fail "live-import reports $STAMPED_N newly stamped but the AWS CLI finds $MARKED_AFTER objects carrying tofu-estate=$ESTATE (GetResources $GAUNTLET_ESTATE_RGTA_N + IAM's own tag APIs $GAUNTLET_ESTATE_IAM_N, $GAUNTLET_ESTATE_BOTH_N in both)"
log "  $ELIGIBLE_LINE"
log "  $STAMPED_LINE"
log "  $MARKED_AFTER objects carry tofu-estate=$ESTATE through the AWS CLI (GetResources $GAUNTLET_ESTATE_RGTA_N + IAM $GAUNTLET_ESTATE_IAM_N)"
gauntlet_stage migrate pass "$ELIGIBLE_LINE; $STAMPED_LINE; $MARKED_AFTER of them read back through the AWS CLI (GetResources $GAUNTLET_ESTATE_RGTA_N + IAM's own tag APIs $GAUNTLET_ESTATE_IAM_N, deduplicated by ARN)"

# ── STAGE 3: replan from nothing ─────────────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 5. STAGE 3 - test plan: choudoufu live-plan with no local state ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ -n "${DUMP_PLAN:-}" ]; then printf '%s\n' "$PLAN_OUT" > "$DUMP_PLAN"; fi
[ "$PLAN_RC" -eq 0 ] || {
  grep -E '^Error:' <<< "$PLAN_OUT" | head -20
  fail "live-plan exited $PLAN_RC: $(gauntlet_first_error_line <<< "$PLAN_OUT")"
}
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$PLAN_OUT" || {
  grep -E '^  # .+ (will|must) be|^Plan: ' <<< "$PLAN_OUT" | head -40
  fail "live-plan after migration is not empty: $(grep -E '^Plan: ' <<< "$PLAN_OUT" | head -n1)"
}
log "  live-plan is empty"
gauntlet_stage test_plan pass "live-plan with no local state exits 0 and reports \"No changes. Your infrastructure matches the configuration.\""

# ── STAGE 4: no-op apply ─────────────────────────────────────────────────────
gauntlet_begin_stage test_apply
log "=== 6. STAGE 4 - test apply: apply the empty plan, assert a genuine no-op ==="
gauntlet_estate_objects "$ESTATE" awsl || fail "could not read the inventory before the no-op apply"
BEFORE_N="$GAUNTLET_ESTATE_N"
APPLY2_OUT="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -60; fail "the post-migration apply failed: $(gauntlet_first_error_line <<< "$APPLY2_OUT")"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }
gauntlet_estate_objects "$ESTATE" awsl || fail "could not read the inventory after the no-op apply"
AFTER_N="$GAUNTLET_ESTATE_N"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
log "  genuine no-op: $BEFORE_N tofu-estate objects before, $AFTER_N after"
gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); $BEFORE_N tofu-estate objects before, $AFTER_N after, read through the AWS CLI"

# ── STAGE 5: drift and reconverge ────────────────────────────────────────────
gauntlet_begin_stage drift_reconverge
log "=== 7. STAGE 5 - drift and reconverge: mutate one object out of band ==="
VPC_ID="$(gauntlet_first_match ".Vpcs[] | select(any(.Tags[]?; .Key == \"tofu-estate\" and .Value == \"$ESTATE\")) | .VpcId" \
  awsl ec2 describe-vpcs 2>/dev/null)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "no live VPC carries tofu-estate=$ESTATE"
if [ "${BREAK:-}" = "1" ]; then
  SUBNET_ID="$(gauntlet_first_match '.Subnets[].SubnetId' awsl ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID")"
  awsl ec2 create-tags --resources "$SUBNET_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered subnet $SUBNET_ID's Name tag - the single-object assertion below must fail"
fi
awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(gauntlet_first_match '.Tags[].Value' awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name")"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated VPC $VPC_ID's Name tag through the AWS CLI"

DRIFT_PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC: $(gauntlet_first_error_line <<< "$DRIFT_PLAN_OUT")"; }
CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
OTHER_ACTIONS="$(grep -E '^  # \S+ (will|must) be' <<< "$DRIFT_PLAN_OUT" | grep -vE 'will be updated' || true)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
[ -z "$OTHER_ACTIONS" ] || fail "the drift plan proposes more than updates: $(printf '%s' "$OTHER_ACTIONS" | tr '\n' ';')"
[ "$N_CHANGED" = "1" ] || fail "expected exactly 1 object proposed for a fix, got $N_CHANGED: $(printf '%s' "$CHANGED_ADDRS" | tr '\n' ' ')"
grep -qE '^module\.vpc\.aws_vpc\.this' <<< "$CHANGED_ADDRS" \
  || fail "the plan proposes fixing $CHANGED_ADDRS, not the VPC that was tampered"

RECONVERGE_OUT="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
[ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -60; fail "the reconverge apply failed: $(gauntlet_first_error_line <<< "$RECONVERGE_OUT")"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
  || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
FIXED_VALUE="$(gauntlet_first_match '.Tags[].Value' awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name")"
[ "$FIXED_VALUE" != "tampered-out-of-band" ] || fail "the VPC's Name tag is still tampered after reconverging"
log "  reconverged: VPC $VPC_ID's Name tag reads $FIXED_VALUE"
gauntlet_stage drift_reconverge pass "VPC $VPC_ID's Name tag tampered through the AWS CLI; live-plan proposed one update ($CHANGED_ADDRS) and nothing else; apply changed 1 and the tag read back as $FIXED_VALUE"

# ── Stages this script does not run yet ─────────────────────────────────────
NOT_YET="not written yet: this estate is new for #2291 and its first run decides which day-2 stages it can reach"
for s in day2_rename day2_remove day2_count day2_replace day2_crash day2_teardown plan_approval greenfield strict no_local_state; do
  gauntlet_stage "$s" not_run "$NOT_YET"
done

gauntlet_end
log ""
log "=== corpus-eks-managed-node-group: stages 1-5 pass ==="
