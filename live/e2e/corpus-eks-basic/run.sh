#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-eks's "basic" example (v9.0.0,
# .corpus/eks/examples/basic, pinned in live/corpus-manifest.json), crossed
# against a real emulator end to end - cold plain apply, choudoufu
# live-import, live-plan, live-apply, and an out-of-band drift/reconverge
# round. EKS is one of the most commonly deployed AWS services via
# Terraform, this module is the de facto standard way people provision it,
# and "basic" is its simplest entry point. It had never been crossed live
# before this script - only used for #102's static refusal-ranking
# scoreboard.
#
# THE FIVE STAGES, and what each one actually found:
#
#   1. COLD DEPLOY   plain terraform apply, no live block, zero choudoufu
#                     awareness. PASSES, but not for free - see "Deltas"
#                     below. 54 resources, genuinely unmarked.
#   2. MIGRATE        choudoufu live-import -approve against that state.
#                     PARTIAL: only 4 of the 54 resources are declared in
#                     the ROOT module (3 security groups + a random_string);
#                     the other 50 live inside module.vpc and module.eks.
#                     live-import v1 is root-module-only (issue #59) and
#                     says so in its own output - this is not a bug in this
#                     script, it is the real, current boundary of adoption
#                     for any module-shaped estate, and terraform-aws-eks is
#                     about as module-shaped as OpenTofu configuration gets.
#   3. TEST PLAN      choudoufu live-plan against the full 54-resource
#                     config. REFUSES OUTRIGHT. Not "empty" and not "50 to
#                     add" - admission itself stops the run before any plan
#                     is rendered. The refusal wall is real, itemized, and
#                     asserted below by rule and by resource: 4 unadmitted
#                     "default_*" adopter types the VPC module manages
#                     (aws_default_vpc/_security_group/_route_table/_
#                     network_acl), 3 unadmitted VPN-gateway types this
#                     estate's VPC module declares even with no VPN gateway
#                     configured, 6 logical-resource types with no
#                     record_store declared (random_pet x3, null_resource,
#                     local_file, plus kubernetes_config_map which is a
#                     different, structural gap - a non-AWS provider
#                     resource has no AWS tag to carry a marker on at all),
#                     and 7 correctly-conservative count-index refusals
#                     (aws_route/aws_route_table_association arguments built
#                     from `element(some_resource[*].id, count.index)` -
#                     the checker cannot statically prove two instances get
#                     different route_table_ids without evaluating live
#                     resources, which is exactly the class of defect
#                     HANDOFF.md's own count.index history warns about
#                     getting wrong in the OTHER direction).
#   4. TEST APPLY     UNREACHABLE. Stage 3 produced no plan to apply.
#   5. DRIFT/RECONVERGE UNREACHABLE for the same reason.
#
# This script does not paper over stages 4-5 by hand-patching the estate to
# dodge the refusal wall - the point of a real-estate crossing is to find
# what a real user hits, not to manufacture a passing shape. Stages 1-2 are
# fully real and asserted; stage 3 is real and its refusal is asserted by
# rule and by resource, with a BREAK=1 control proving each assertion is
# load-bearing.
#
# ── Deltas needed to even cold-deploy this pinned estate ───────────────────
#
# None of these are choudoufu-specific; a plain `terraform apply` against
# this exact pin hits every one of them before choudoufu is ever involved.
#
#   1. Host architecture. `.corpus/eks/examples/basic`'s provider version
#      constraints (`>= 2.28.1` etc., the pre-0.13 in-provider-block
#      syntax) resolve cleanly to modern releases (aws 6.60.0, kubernetes
#      1.10.0, ...) with ONE exception: `hashicorp/template` 2.2.0 (the
#      module still uses the archived template provider) ships no
#      darwin_arm64 build - it predates Apple Silicon entirely, and it is
#      the pinned version's only release. This script runs `terraform` and
#      `choudoufu` inside `--platform linux/amd64` containers for exactly
#      this reason - not a Docker preference, a real provider-availability
#      wall on Apple Silicon hosts. A linux/amd64 host would not need this.
#   2. `terraform-aws-modules/vpc/aws` v2.6.0 (this example's own pin) uses
#      the `list()` builtin, removed from the language after 0.12. Bumped
#      to v6.6.1 - the SAME major-version-agnostic pin `.corpus/vpc` itself
#      uses elsewhere in this repo's corpus - which still hits its own
#      wall: `enable_classiclink_dns_support` / `aws_eip.vpc` / an
#      `enable_classiclink` argument the CURRENT aws provider dropped
#      entirely (AWS retired ClassicLink years ago). v6.6.1 has moved past
#      all of that; the foundational i/o (vpc_id, private_subnets,
#      public_subnets, tags, ...) is unchanged across the jump.
#   3. The EKS module's OWN outputs.tf (not the VPC submodule) also calls
#      the removed `list("")`, and workers.tf / workers_launch_template.tf
#      set `aws_autoscaling_group.tags` as a list of maps - the pre-tag-block
#      ASG tagging shape the CURRENT aws provider replaced with a
#      repeatable `tag { key = ... }` block. Both fixed as pure syntax
#      translations (`tolist([""])`, a `dynamic "tag"` block over the exact
#      same values) - no argument, resource, or identity shape changed.
#   4. The module's own `local.tf` builds ASG tags with the removed `map()`
#      builtin - `tomap({...})`, same story.
#   5. floci's standard provider-endpoint flags on `provider "aws"`.
#   6. This module's `wait_for_cluster_cmd` variable defaults to
#      unauthenticated `wget ... /healthz`. Modern Kubernetes (real EKS
#      included, not just floci) has required auth on /healthz for years,
#      so the DEFAULT genuinely hangs forever against ANY current cluster,
#      not a floci-specific gap. Overridden to a `nc -z` TCP reachability
#      check - the same "wait until the endpoint exists" intent the module's
#      own variable description states, without asserting anything about
#      HTTP auth. This wires through `module "eks" { wait_for_cluster_cmd =
#      ... }` directly, NOT via a root .tfvars - the root example never
#      declares that variable itself, so a root-level override is silently
#      ignored (found the hard way: the default hung for the exact reason
#      above until this was fixed).
#   7. `provider "kubernetes" { insecure = true }` in place of
#      `cluster_ca_certificate = ...`. This is the one delta that IS an
#      artifact of THIS script's own environment (#1 above): floci's EKS
#      "host" endpoint mode returns `https://localhost:<port>`, whose k3s
#      certificate genuinely does carry a `localhost` SAN - a host running
#      terraform natively would need no override here at all. Because
#      terraform and choudoufu run inside a second container in THIS
#      script, the reachable endpoint has to be `network` mode
#      (`https://floci-eks-<name>:6443`, container-DNS-based), and the k3s
#      certificate's SAN list does not cover that name. Documented, not
#      hidden: a floci-side fix would need the k3s cert's SAN list to
#      include its own advertised network-mode hostname.
#
# ── Two real floci gaps found and fixed this session ────────────────────
#
#   - `aws_ami` discovery for the real AWS-owned EKS worker AMIs
#     (owner 602401143452 "amazon-eks-node-<version>-v*", owner
#     801119661308 for Windows) returned zero results - every version of
#     terraform-aws-eks discovers its worker AMI exactly this way, and the
#     module evaluates it unconditionally even when every worker group
#     overrides ami_id (a lookup() default argument is always evaluated).
#     Fixed: lex00/floci#55, branch fix/eks-worker-ami-catalog (PR #56).
#   - `SuspendProcesses` / `ResumeProcesses` were unimplemented, and the aws
#     provider calls SuspendProcesses unconditionally around ASG creation
#     whenever wait_for_capacity_timeout is non-zero - the default - so ANY
#     aws_autoscaling_group apply with default settings failed outright.
#     Fixed on the same branch, same PR.
#
#   Both fixes are pushed to lex00/floci but NOT YET merged or published to
#   the pinned ghcr.io/lex00/floci image this script defaults to using. Set
#   FLOCI_IMAGE to an image built from that branch to reproduce stage 1's
#   PASS below; against the current pin, stage 1 fails earlier, at AMI
#   discovery and then at SuspendProcesses, with the exact errors these
#   fixes were written against.
#
#   bash live/e2e/corpus-eks-basic/run.sh
#
# Needs Docker (with a socket floci can reach - EKS real mode spawns a k3s
# sibling container per cluster) and the AWS CLI. .corpus is read, never
# written: the whole eks/ tree (module root + examples/) is copied out to a
# temp directory first, same as every other corpus crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt linux/amd64 choudoufu binary; skips the
#                `go build`. Must be linux/amd64 - it runs inside a
#                --platform linux/amd64 container regardless of host arch.
#   FLOCI_PORT   host port for the emulator (default 4718).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image. See "Two real floci gaps" above - the
#                pinned image does not yet carry either fix.
#   BREAK        set to 1 to corrupt an expected count/rule before the
#                stage-2 and stage-3 assertions, proving each is
#                load-bearing rather than a check that always passes.
#
# Exit codes: 0 when the script's OWN measurement completed faithfully -
# which includes stage 3's refusal being real and correctly itemized, since
# that is what this estate actually does today. Non-zero only if a stage
# that is supposed to pass does not, or an assertion this script makes
# about the refusal wall's shape turns out to be wrong.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/eks"
WORK="$(mktemp -d)"
NET="choudoufu-corpus-eks-basic-net-$$"
FLOCI_PORT="${FLOCI_PORT:-4718}"
FLOCI_NAME="choudoufu-corpus-eks-basic-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
TOOLBOX_IMAGE="choudoufu-corpus-eks-basic-toolbox:$$"

ESTATE="eks-basic-crossing"
REGION="us-west-2"

PLAIN_REL="plain/eks/examples/basic"
ADOPTED_REL="adopted/eks/examples/basic"
PLAIN="$WORK/$PLAIN_REL"
ADOPTED="$WORK/$ADOPTED_REL"

cleanup() {
  # Real-mode services spawn sibling containers floci only tracks in its
  # own in-memory state: EKS real mode starts a k3s container per cluster
  # (floci-eks-<cluster-name>), and the worker autoscaling groups here
  # start one EC2 simulation container per instance (floci-ec2-i-<id>).
  # `docker rm -f "$FLOCI_NAME"` does NOT clean either up - they are
  # independent containers, destroyed along with floci's own state by a
  # bare rm -f. Found the hard way, twice: a leftover k3s container from
  # an earlier failed run held floci's next cluster's host port and made
  # EVERY later create fail with "port is already allocated"; leftover
  # EC2 containers held the shared Docker network open and made
  # `docker network rm` fail silently after that.
  docker ps -aq --filter "name=floci-eks-" 2>/dev/null | xargs -r docker rm -f >/dev/null 2>&1 || true
  docker ps -aq --filter "name=floci-ec2-" 2>/dev/null | xargs -r docker rm -f >/dev/null 2>&1 || true
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker rmi -f "$TOOLBOX_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

terraform_run() {
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$PLAIN_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    hashicorp/terraform:1.9 "$@"
}

tofu_run() {
  local rel="$1"; shift
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$rel" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    "$TOOLBOX_IMAGE" /work/bin/choudoufu "$@"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
[ -d "$SRC/examples/basic" ] || fail "$SRC/examples/basic is missing - run 'just corpus-fetch' first"

mkdir -p "$WORK/bin"
if [ -n "${TOFU_BIN:-}" ]; then
  cp "$TOFU_BIN" "$WORK/bin/choudoufu"
  log "  using TOFU_BIN=$TOFU_BIN"
else
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

# .corpus is shared across every worktree and is NEVER written to: the
# whole eks/ tree (module root + examples/) is copied out twice - once for
# the cold PLAIN deploy, once for the ADOPTED (live-block) copy - preserving
# the relative path examples/basic's `source = "../.."` expects.
mkdir -p "$WORK/plain" "$WORK/adopted"
cp -R "$SRC" "$WORK/plain/eks"
cp -R "$SRC" "$WORK/adopted/eks"
rm -rf "$WORK/plain/eks/.git" "$WORK/plain/eks/.github" "$WORK/adopted/eks/.git" "$WORK/adopted/eks/.github"
[ -f "$PLAIN/main.tf" ] || fail "the estate copy is missing main.tf"
log "  eks module + basic example copied out of .corpus into $WORK (plain + adopted)"

# ── 1. the deltas (see header comment for why each one is needed) ──────────
log "=== 1. deltas: host-arch VPC/output/ASG-tag syntax fixes + floci wiring ==="
apply_deltas() {
  local base="$1" with_live="$2"

  # 1a. VPC submodule: v2.6.0 (this example's own pin) -> v6.6.1, the same
  # major-version-agnostic pin .corpus/vpc uses. Removes the list() builtin
  # AND the ClassicLink arguments the current aws provider dropped.
  sed -i.bak 's/version = "2\.6\.0"/version = "6.6.1"/' "$base/eks/examples/basic/main.tf"

  # 1b. floci provider flags + (adopted copy only) the live block.
  perl -0pi -e 's/(provider "aws" \{\n  version = ">= 2\.28\.1"\n  region  = var\.region\n)\}/$1\n  access_key                  = "test"\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  skip_requesting_account_id  = true\n  s3_use_path_style           = true\n}/' "$base/eks/examples/basic/main.tf"
  grep -q 's3_use_path_style' "$base/eks/examples/basic/main.tf" \
    || fail "the emulator delta did not match main.tf - the corpus pin has moved"

  if [ "$with_live" = "1" ]; then
    perl -0pi -e 's/(terraform \{\n  required_version = ">= 0\.12\.0"\n)\}/$1\n  live {\n    estate = "'"$ESTATE"'"\n  }\n}/' "$base/eks/examples/basic/main.tf"
    grep -q "estate = \"$ESTATE\"" "$base/eks/examples/basic/main.tf" \
      || fail "the live block delta did not match main.tf - the corpus pin has moved"
  fi

  # 1c. wait_for_cluster_cmd, wired through the module call directly (a
  # root .tfvars is silently ignored - the root example never declares
  # this variable itself). nc -z checks TCP reachability only, sidestepping
  # the auth-required /healthz that hangs the module's own wget default
  # against any current Kubernetes, floci included.
  perl -0pi -e 's/(  source       = "\.\.\/\.\."\n)/$1  wait_for_cluster_cmd = "until nc -z -w3 \$(echo \$ENDPOINT | sed -e \x27s#https:\/\/##\x27 -e \x27s#:.*##\x27) \$(echo \$ENDPOINT | sed -e \x27s#.*:##\x27); do sleep 4; done"\n/' "$base/eks/examples/basic/main.tf"
  grep -q 'wait_for_cluster_cmd = "until nc' "$base/eks/examples/basic/main.tf" \
    || fail "the wait_for_cluster_cmd delta did not match main.tf - the corpus pin has moved"

  # 1d. kubernetes provider: insecure = true in place of cluster_ca_certificate.
  # See header comment #7 - an artifact of running terraform/choudoufu inside
  # a second container (network-mode k3s endpoint, cert SAN mismatch), not a
  # general floci gap.
  perl -0pi -e 's/  cluster_ca_certificate = base64decode\(data\.aws_eks_cluster\.cluster\.certificate_authority\.0\.data\)\n/  insecure = true\n/' "$base/eks/examples/basic/main.tf"
  grep -q 'insecure = true' "$base/eks/examples/basic/main.tf" \
    || fail "the kubernetes provider delta did not match main.tf - the corpus pin has moved"

  # 1e. outputs.tf: list("") -> tolist([""]), the removed builtin.
  sed -i.bak 's/list("")/tolist([""])/g' "$base/eks/outputs.tf"
  grep -q 'tolist(\[""\])' "$base/eks/outputs.tf" \
    || fail "the outputs.tf list() delta did not match - the corpus pin has moved"

  # 1f. local.tf: map(...) -> tomap({...}), same story, feeding ASG tags.
  python3 - "$base/eks/local.tf" <<'PYEOF'
import sys
path = sys.argv[1]
with open(path) as f:
    content = f.read()
old = '''    map(
      "key", item,
      "value", element(values(var.tags), index(keys(var.tags), item)),
      "propagate_at_launch", "true"
    )'''
new = '''    tomap({
      "key"                 = item,
      "value"               = element(values(var.tags), index(keys(var.tags), item)),
      "propagate_at_launch" = "true"
    })'''
if old not in content:
    print("FAIL: local.tf map() delta did not match - the corpus pin has moved", file=sys.stderr)
    sys.exit(1)
content = content.replace(old, new, 1)
with open(path, "w") as f:
    f.write(content)
PYEOF
  [ $? -eq 0 ] || fail "local.tf map() delta failed"

  # 1g. workers.tf / workers_launch_template.tf: aws_autoscaling_group.tags
  # as a list-of-maps argument -> a dynamic "tag" block over the exact same
  # values. The CURRENT aws provider dropped the list-of-maps shape for a
  # repeatable tag block; this changes no tag key, value or
  # propagate_at_launch bit.
  python3 - "$base/eks" <<'PYEOF'
import sys

def fix(path, old_block, new_block):
    with open(path) as f:
        content = f.read()
    if old_block not in content:
        print(f"FAIL: ASG tags delta did not match {path} - the corpus pin has moved", file=sys.stderr)
        sys.exit(1)
    content = content.replace(old_block, new_block, 1)
    with open(path, "w") as f:
        f.write(content)

workers_old = '''  tags = concat(
    [
      {
        "key"                 = "Name"
        "value"               = "${aws_eks_cluster.this[0].name}-${lookup(var.worker_groups[count.index], "name", count.index)}-eks_asg"
        "propagate_at_launch" = true
      },
      {
        "key"                 = "kubernetes.io/cluster/${aws_eks_cluster.this[0].name}"
        "value"               = "owned"
        "propagate_at_launch" = true
      },
      {
        "key"                 = "k8s.io/cluster/${aws_eks_cluster.this[0].name}"
        "value"               = "owned"
        "propagate_at_launch" = true
      },
    ],
    local.asg_tags,
    lookup(
      var.worker_groups[count.index],
      "tags",
      local.workers_group_defaults["tags"]
    )
  )'''

workers_new = '''  dynamic "tag" {
    for_each = concat(
      [
        {
          "key"                 = "Name"
          "value"               = "${aws_eks_cluster.this[0].name}-${lookup(var.worker_groups[count.index], "name", count.index)}-eks_asg"
          "propagate_at_launch" = true
        },
        {
          "key"                 = "kubernetes.io/cluster/${aws_eks_cluster.this[0].name}"
          "value"               = "owned"
          "propagate_at_launch" = true
        },
        {
          "key"                 = "k8s.io/cluster/${aws_eks_cluster.this[0].name}"
          "value"               = "owned"
          "propagate_at_launch" = true
        },
      ],
      local.asg_tags,
      lookup(
        var.worker_groups[count.index],
        "tags",
        local.workers_group_defaults["tags"]
      )
    )
    content {
      key                 = tag.value["key"]
      value               = tag.value["value"]
      propagate_at_launch = tag.value["propagate_at_launch"]
    }
  }'''

launch_template_old = '''  tags = concat(
    [
      {
        "key" = "Name"
        "value" = "${aws_eks_cluster.this[0].name}-${lookup(
          var.worker_groups_launch_template[count.index],
          "name",
          count.index,
        )}-eks_asg"
        "propagate_at_launch" = true
      },
      {
        "key"                 = "kubernetes.io/cluster/${aws_eks_cluster.this[0].name}"
        "value"               = "owned"
        "propagate_at_launch" = true
      },
    ],
    local.asg_tags,
    lookup(
      var.worker_groups_launch_template[count.index],
      "tags",
      local.workers_group_defaults["tags"]
    )
  )'''

launch_template_new = '''  dynamic "tag" {
    for_each = concat(
      [
        {
          "key" = "Name"
          "value" = "${aws_eks_cluster.this[0].name}-${lookup(
            var.worker_groups_launch_template[count.index],
            "name",
            count.index,
          )}-eks_asg"
          "propagate_at_launch" = true
        },
        {
          "key"                 = "kubernetes.io/cluster/${aws_eks_cluster.this[0].name}"
          "value"               = "owned"
          "propagate_at_launch" = true
        },
      ],
      local.asg_tags,
      lookup(
        var.worker_groups_launch_template[count.index],
        "tags",
        local.workers_group_defaults["tags"]
      )
    )
    content {
      key                 = tag.value["key"]
      value               = tag.value["value"]
      propagate_at_launch = tag.value["propagate_at_launch"]
    }
  }'''

base = sys.argv[1]
fix(f"{base}/workers.tf", workers_old, workers_new)
fix(f"{base}/workers_launch_template.tf", launch_template_old, launch_template_new)
PYEOF
  [ $? -eq 0 ] || fail "workers.tf / workers_launch_template.tf ASG tags delta failed"

  find "$base" -name "*.bak" -delete
}

apply_deltas "$WORK/plain" 0
apply_deltas "$WORK/adopted" 1
DIFF_LINES="$(diff "$PLAIN/main.tf" "$ADOPTED/main.tf" | grep -c '^[<>]' || true)"
[ "$DIFF_LINES" -eq 4 ] || fail "plain and adopted main.tf differ by $DIFF_LINES lines, expected exactly 4 (the live block) - a delta leaked between copies"
log "  deltas applied identically to both copies; only the live block differs ($DIFF_LINES lines)"

# ── 2. floci, real EKS mode (needs the Docker socket for k3s) ──────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE), real EKS mode ==="
[ -S /var/run/docker.sock ] || fail "no /var/run/docker.sock to mount - floci's EKS real mode needs it to spawn k3s"
docker run -d --rm --network "$NET" -p "${FLOCI_PORT}:4566" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=network \
  -e "FLOCI_SERVICES_EKS_DOCKER_NETWORK=$NET" \
  --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
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

# ── 3. STAGE 1: cold deploy, real terraform, zero choudoufu awareness ──────
log "=== 3. STAGE 1 - cold deploy: real terraform apply, no live block ==="
terraform_run init -input=false -no-color > /tmp/eks-basic-init.log 2>&1 || {
  tail -40 /tmp/eks-basic-init.log; fail "terraform init failed"; }
APPLY_OUT="$(terraform_run apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -60
  fail "the cold apply failed"
}
grep -qE 'Apply complete! Resources: 54 added, 0 changed, 0 destroyed\.' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 54 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

[ -f "$PLAIN/terraform.tfstate" ] || fail "no terraform.tfstate was written by the cold apply"
STATE_RESOURCES="$(python3 -c "import json; print(len(json.load(open('$PLAIN/terraform.tfstate'))['resources']))")"
[ "$STATE_RESOURCES" = "54" ] || fail "the state file records $STATE_RESOURCES resources, expected 54"

CLUSTER_NAME="$(awsl eks list-clusters --query 'clusters[0]' --output text)"
[ -n "$CLUSTER_NAME" ] && [ "$CLUSTER_NAME" != "None" ] || fail "no EKS cluster found through the AWS CLI after the cold apply"
CLUSTER_STATUS="$(awsl eks describe-cluster --name "$CLUSTER_NAME" --query 'cluster.status' --output text)"
[ "$CLUSTER_STATUS" = "ACTIVE" ] || fail "cluster $CLUSTER_NAME is $CLUSTER_STATUS, not ACTIVE"
MARKED="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-address" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$MARKED" = "0" ] || fail "expected 0 objects carrying a tofu-address tag before migration, got $MARKED - this test proves nothing"
log "  cluster $CLUSTER_NAME is ACTIVE, confirmed unmarked via the AWS CLI directly ($MARKED tofu-address tags)"

# ── 4. STAGE 2: migrate ─────────────────────────────────────────────────────
log "=== 4. STAGE 2 - migrate: choudoufu live-import against the cold state ==="
tofu_run "$ADOPTED_REL" init -input=false -no-color > /tmp/eks-basic-tofu-init.log 2>&1 || {
  tail -40 /tmp/eks-basic-tofu-init.log; fail "choudoufu init failed"; }

IMPORT_OUT="$(tofu_run "$ADOPTED_REL" live-import -state="/work/$PLAIN_REL/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -60; fail "live-import -approve failed"; }

EXPECT_NOT_CONSIDERED="50 resource instance(s) in a non-root module were not considered"
EXPECT_STAMPED="3 resource(s) newly stamped, 0 already stamped, 0 failed, 1 skipped."
if [ "${BREAK:-}" = "1" ]; then
  EXPECT_NOT_CONSIDERED="49 resource instance(s) in a non-root module were not considered"
  log "  BREAK=1: expecting \"$EXPECT_NOT_CONSIDERED\" (off by one from the real"
  log "           count). This step must fail."
fi
grep -qF "$EXPECT_NOT_CONSIDERED" <<< "$IMPORT_OUT" || {
  grep -E 'resource instance\(s\) in a non-root module' <<< "$IMPORT_OUT"
  fail "did not find \"$EXPECT_NOT_CONSIDERED\" in live-import's own output (see the real line above) - issue #59's root-module-only scope, or the count it reports, has changed"
}
grep -qF "$EXPECT_STAMPED" <<< "$IMPORT_OUT" || {
  grep -E 'resource\(s\) newly stamped' <<< "$IMPORT_OUT"
  fail "did not find \"$EXPECT_STAMPED\" in live-import's own output"
}
log "  live-import's own accounting matches: 3 of 4 root-module resources stamped, 50 non-root instances (issue #59) not considered"

MARKED_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$MARKED_AFTER" = "3" ] || fail "expected 3 objects carrying tofu-estate=$ESTATE after migration, got $MARKED_AFTER"
log "  3 of 3 stamped objects confirmed via the AWS CLI directly (the 3 root-module security groups; random_string.suffix is untaggable, the module's other 50 resources are out of live-import v1's scope)"

# ── 5. STAGE 3: test plan ───────────────────────────────────────────────────
log "=== 5. STAGE 3 - test plan: choudoufu live-plan against the full config ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan exited 0 - the refusal wall this script expects did not fire. Either issue #59 shipped root+nested support, or new admission rows changed what refuses here - re-check by hand before trusting this script's stage 4/5 skip."; }

# No associative arrays: /bin/bash on macOS is still 3.2 (no `declare -A`
# support at all), and every other corpus-* script in this repo already
# avoids them for exactly that reason.
UNADMITTED_SITES='aws_default_vpc\.this|aws_default_security_group\.this|aws_default_route_table\.default|aws_default_network_acl\.this|aws_vpn_gateway_attachment\.this|aws_vpn_gateway_route_propagation\.(public|private|intra)|kubernetes_config_map\.aws_auth|aws_vpc_ipv4_cidr_block_association\.this'
LOGICAL_SITES='random_pet\.(workers|workers_launch_template|node_groups)|null_resource\.wait_for_cluster|local_file\.kubeconfig'
COUNTINDEX_SITES='aws_route\.(private_nat_gateway|private_dns64_nat_gateway|private_ipv6_egress)|aws_route_table_association\.(public|private)'
if [ "${BREAK:-}" = "1" ]; then
  UNADMITTED_SITES='this-resource-type-does-not-exist-anywhere'
  log "  BREAK=1: expecting the unadmitted-type rule to fire on a resource"
  log "           name that cannot appear in the output. This step must fail."
fi

assert_rule_fires() {
  local rule="$1" sites="$2"
  local count site_hits
  count="$(grep -cE "Rule: ${rule}\." <<< "$PLAN_OUT" || true)"
  [ "$count" -ge 1 ] || fail "no \"Rule: ${rule}.\" refusal found in live-plan's output - the refusal wall's shape has changed"
  site_hits="$(grep -cE "$sites" <<< "$PLAN_OUT" || true)"
  [ "$site_hits" -ge 1 ] || fail "rule $rule fired $count time(s) but none of the expected resource names appeared - the refusal wall's shape has changed"
  log "  Rule: ${rule}. fires $count time(s), including the expected resource(s)"
}

assert_rule_fires "unadmitted-type" "$UNADMITTED_SITES"
assert_rule_fires "logical-resource" "$LOGICAL_SITES"
assert_rule_fires "count-index" "$COUNTINDEX_SITES"

log ""
log "=== PARTIAL: stages 1-2 pass in full; stage 3 refuses outright ==="
log ""
log "This is the real, current shape of crossing terraform-aws-eks's own"
log "\"basic\" example - the module virtually everyone reaches for first -"
log "against choudoufu/floci:"
log ""
log "  STAGE 1  PASS  54/54 resources, genuinely cold, genuinely unmarked."
log "  STAGE 2  PARTIAL  3/4 root-module resources stamped; 50 resources"
log "           inside module.vpc and module.eks are out of live-import"
log "           v1's scope (issue #59) - not a bug this script works around."
log "  STAGE 3  REFUSES  4 unadmitted default_* adopter types, 3 unadmitted"
log "           VPN-gateway types, 6 logical-resource types with no"
log "           record_store declared, 7 correctly-conservative"
log "           count-index refusals. Asserted by rule and by resource"
log "           above, with BREAK=1 proving neither check is vacuous."
log "  STAGES 4-5  UNREACHABLE  stage 3 produced no plan to apply or drift."
log ""
log "Two real, generalizable floci gaps (not this module's age, not this"
log "script's setup) were found and fixed along the way: EKS worker AMI"
log "discovery (lex00/floci#55/#56) and SuspendProcesses/ResumeProcesses"
log "(same PR) - every terraform-aws-eks estate with self-managed node"
log "groups hits both on default settings."
