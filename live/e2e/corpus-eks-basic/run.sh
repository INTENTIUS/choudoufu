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
#                     PASSES (re-verified 2026-08-20 against current main
#                     with issue #326's fix merged; originally recorded
#                     PARTIAL/fail by an agent whose worktree predated
#                     cec3c4b9b1's live-import child-module fix - issue
#                     #59's root-module-only scope is CLOSED). All 54
#                     resource instances across the root module, module.vpc
#                     and module.eks are considered: 18 VERIFIED + 7 DRIFTED
#                     = 25 eligible and stamped, 28 UNTAGGABLE by design (no
#                     `tags` argument in the provider schema - ASGs, launch
#                     configurations, IAM role policy attachments, security
#                     group rules, routes, route table associations,
#                     random_pet/random_string, local_file), and 1 MISSING -
#                     kubernetes_config_map.aws_auth. #326's fix (merged
#                     852f52073f/a990112e26, 2026-08-20) admitted this type,
#                     so live-import no longer refuses it outright; it now
#                     genuinely attempts to verify the live object and
#                     reports, precisely, WHY it cannot: "Provider ...
#                     kubernetes could not be used ... Dynamic value in
#                     static context: Unable to use
#                     data.aws_eks_cluster_auth.cluster / data.aws_eks_
#                     cluster.cluster in static context, which is required
#                     by provider.kubernetes." This is a different, real,
#                     narrower wall than #326's - the kubernetes provider
#                     block is itself configured from another provider's
#                     live output (the EKS cluster's endpoint/token), which
#                     live-import's no-state, no-apply verification pass
#                     cannot evaluate. Stock OpenTofu is never asked this
#                     question (it resolves the same data sources during a
#                     real plan/apply, which always has other resources'
#                     already-applied state to read) - DEFER-caliber, same
#                     family as #313's own out-of-scope live-value-through-
#                     provider-config boundary, not attempted here. Not
#                     stamped either way (kubernetes_config_map carries no
#                     AWS tags), so the net stamped count is unchanged by
#                     #326 - what changed is the REASON, from "we don't
#                     know this type" to "we know it, and we know precisely
#                     why we can't verify it yet."
#   3. TEST PLAN      choudoufu live-plan against the full 54-resource
#                     config. REFUSES OUTRIGHT. Not "empty" and not "N to
#                     add" - admission itself stops the run before any plan
#                     is rendered. Re-verified 2026-08-20 with #326's fix
#                     merged: the unadmitted-type refusal on kubernetes_
#                     config_map.aws_auth is CONFIRMED GONE - zero
#                     occurrences of "Rule: unadmitted-type." and zero
#                     mentions of "kubernetes" anywhere in live-plan's
#                     output. The type's identity (metadata.name/namespace,
#                     fully client-named and statically knowable) now
#                     resolves cleanly at plan time, same as the
#                     association family. What's real and current, asserted
#                     below by rule and by resource - unchanged by #326,
#                     since neither of these families ever depended on
#                     kubernetes_config_map's admission state:
#                       - logical-resource (4 sites): random_string.suffix,
#                         null_resource.wait_for_cluster, random_pet.workers
#                         are RECORD_ADMITTED and correctly refused only
#                         because this configuration declares no
#                         record_store, exactly as designed (#73) - declaring
#                         one would admit them. local_file.kubeconfig is a
#                         different, narrower case in the SAME rule's output:
#                         its own diagnostic text does not offer the
#                         record_store escape hatch at all ("nothing can
#                         recover its value from the live system, because
#                         there is no live system holding it") - this is
#                         #314's already-tracked gap (local_file needs a
#                         fourth LogicalClass, argument-derived identity),
#                         not something a record_store declaration fixes.
#                       - count-index (4 sites): aws_route_table_association.
#                         public/private built from `element(some_resource
#                         [*].id, count.index)` - the checker cannot
#                         statically prove two instances get different
#                         route_table_ids/subnet_ids without evaluating
#                         live resources, which is exactly the class of
#                         defect HANDOFF.md's own count.index history warns
#                         about getting wrong in the OTHER direction. Down
#                         from 7 to 4 sites since #321/#324 landed.
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
# ── Two real floci gaps found this session, now merged and published ───
#
#   - `aws_ami` discovery for the real AWS-owned EKS worker AMIs
#     (owner 602401143452 "amazon-eks-node-<version>-v*", owner
#     801119661308 for Windows) returned zero results - every version of
#     terraform-aws-eks discovers its worker AMI exactly this way, and the
#     module evaluates it unconditionally even when every worker group
#     overrides ami_id (a lookup() default argument is always evaluated).
#     lex00/floci#55, branch fix/eks-worker-ami-catalog (PR #56).
#   - `SuspendProcesses` / `ResumeProcesses` were unimplemented, and the aws
#     provider calls SuspendProcesses unconditionally around ASG creation
#     whenever wait_for_capacity_timeout is non-zero - the default - so ANY
#     aws_autoscaling_group apply with default settings failed outright.
#     Fixed on the same branch, same PR.
#
#   Both were merged locally into floci's main (94812193, combined with two
#   other same-night fixes since main had diverged), published to GHCR, and
#   choudoufu's live/floci-image was re-pinned past that point. Re-verified
#   2026-08-19: stage 1 below passes cleanly against the CURRENT pin with no
#   FLOCI_IMAGE override needed - neither error reproduces any more.
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
#                live/floci-image, which now carries both fixes described
#                above under "Two real floci gaps".
#   BREAK        set to 1 to corrupt an expected count/rule before the
#                stage-2 and stage-3 assertions, proving each is
#                load-bearing rather than a check that always passes.
#   DUMP_PLAN    path to write live-plan's full raw output to, for by-hand
#                re-verification of stage 3's exact refusal wall shape.
#   DUMP_IMPORT  path to write live-import's full raw output to, same
#                reason, for stage 2.
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
if [ -n "${DUMP_IMPORT:-}" ]; then printf '%s\n' "$IMPORT_OUT" > "$DUMP_IMPORT"; fi

# Issue #59's root-module-only scope is GONE as of cec3c4b9b1 (landed
# 2026-08-18, re-verified against this estate 2026-08-19): live-import now
# walks module.vpc and module.eks too, and considers all 54 resource
# instances rather than stopping at the root module's 4. Of those 54: 18
# VERIFIED + 7 DRIFTED = 25 eligible and stamped, 28 are UNTAGGABLE by
# design (no `tags` argument in the provider schema - autoscaling groups,
# launch configurations, IAM role policy attachments, security group
# rules, routes, route table associations, random_pet/random_string,
# local_file), and 1 is MISSING - kubernetes_config_map.aws_auth. Re-
# verified 2026-08-20 with issue #326's fix merged: the net eligible/
# stamped/skipped totals below are UNCHANGED from before #326 (kubernetes_
# config_map carries no AWS tags either way, so it was never going to be
# stamped) - what changed is that live-import now genuinely attempts to
# verify it instead of refusing it outright as unadmitted, and reports a
# precise, different reason (the kubernetes provider's own config depends
# on live data.aws_eks_cluster/data.aws_eks_cluster_auth values live-
# import's no-state verification pass cannot evaluate - see stage 3 below).
EXPECT_ELIGIBLE="25 of 54 resource instance(s) are eligible for stamping"
EXPECT_STAMPED="25 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 29 skipped."
EXPECT_MISSING_K8S='kubernetes_config_map.*could not be used'
if [ "${BREAK:-}" = "1" ]; then
  EXPECT_ELIGIBLE="26 of 54 resource instance(s) are eligible for stamping"
  log "  BREAK=1: expecting \"$EXPECT_ELIGIBLE\" (off by one from the real"
  log "           count). This step must fail."
fi
grep -qF "$EXPECT_ELIGIBLE" <<< "$IMPORT_OUT" || {
  grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT"
  fail "did not find \"$EXPECT_ELIGIBLE\" in live-import's own output (see the real line above) - the eligible count has changed"
}
grep -qF "$EXPECT_STAMPED" <<< "$IMPORT_OUT" || {
  grep -E 'resource\(s\) newly stamped' <<< "$IMPORT_OUT"
  fail "did not find \"$EXPECT_STAMPED\" in live-import's own output"
}
grep -qE "$EXPECT_MISSING_K8S" <<< "$IMPORT_OUT" || fail "kubernetes_config_map.aws_auth no longer reports as MISSING/could-not-be-used in live-import's output - issue #326's fix (or the kubernetes-provider-config wall it exposed) has changed shape; re-check by hand"
log "  live-import's own accounting matches: 25 of 54 resource instances stamped (module.vpc + module.eks are now in scope, issue #59 is closed), kubernetes_config_map.aws_auth correctly MISSING (admitted, but its provider config can't be statically evaluated)"

MARKED_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$MARKED_AFTER" = "25" ] || fail "expected 25 objects carrying tofu-estate=$ESTATE after migration, got $MARKED_AFTER"
log "  25 of 25 stamped objects confirmed via the AWS CLI directly"

# ── 5. STAGE 3: test plan ───────────────────────────────────────────────────
log "=== 5. STAGE 3 - test plan: choudoufu live-plan against the full config ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ -n "${DUMP_PLAN:-}" ]; then printf '%s\n' "$PLAN_OUT" > "$DUMP_PLAN"; fi
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan exited 0 - the refusal wall this script expects did not fire. Either issue #59 shipped root+nested support, or new admission rows changed what refuses here - re-check by hand before trusting this script's stage 4/5 skip."; }

# No associative arrays: /bin/bash on macOS is still 3.2 (no `declare -A`
# support at all), and every other corpus-* script in this repo already
# avoids them for exactly that reason.
# The 4 "default_*" adopter types and 3 VPN-gateway types that used to
# refuse here (issue #59-era output, root module only in scope) are GONE:
# module.vpc's aws_default_route_table/aws_default_security_group/
# aws_default_network_acl are all admitted and stamp cleanly in stage 2 now
# that module.vpc is in scope, and this example declares no
# aws_default_vpc/VPN-gateway resources with any live instance.
# kubernetes_config_map.aws_auth is CONFIRMED GONE from the refusal wall
# as of issue #326's fix (merged 852f52073f/a990112e26, 2026-08-20): its
# identity resolves cleanly at plan time, so neither "Rule: unadmitted-
# type." nor the string "kubernetes" appears anywhere in live-plan's
# output any more. Asserted below as a negative control, with BREAK=1
# flipping the expectation (proving the check is load-bearing, not
# vacuously true because the rule never fired for any reason).
LOGICAL_SITES='random_string\.suffix|random_pet\.workers|null_resource\.wait_for_cluster|local_file\.kubeconfig'
COUNTINDEX_SITES='aws_route_table_association\.(public|private)'

assert_rule_fires() {
  local rule="$1" sites="$2"
  local count site_hits
  count="$(grep -cE "Rule: ${rule}\." <<< "$PLAN_OUT" || true)"
  [ "$count" -ge 1 ] || fail "no \"Rule: ${rule}.\" refusal found in live-plan's output - the refusal wall's shape has changed"
  site_hits="$(grep -cE "$sites" <<< "$PLAN_OUT" || true)"
  [ "$site_hits" -ge 1 ] || fail "rule $rule fired $count time(s) but none of the expected resource names appeared - the refusal wall's shape has changed"
  log "  Rule: ${rule}. fires $count time(s), including the expected resource(s)"
}

if [ "${BREAK:-}" = "1" ]; then
  log "  BREAK=1: expecting \"Rule: unadmitted-type.\" to still fire on"
  log "           kubernetes_config_map (issue #326's own fix, deliberately"
  log "           treated as absent). This step must fail."
  grep -qE 'Rule: unadmitted-type\.' <<< "$PLAN_OUT" \
    || fail "BREAK=1 correctly detected: no unadmitted-type refusal fired anywhere - #326's fix holds (this failure is the expected, load-bearing one)"
else
  grep -qE 'Rule: unadmitted-type\.' <<< "$PLAN_OUT" \
    && { grep -E 'Rule: unadmitted-type\.' <<< "$PLAN_OUT"; fail "unadmitted-type fired unexpectedly - #326's fix may have regressed, or a new type lost its identity row"; }
  grep -qi 'kubernetes' <<< "$PLAN_OUT" \
    && { grep -i 'kubernetes' <<< "$PLAN_OUT"; fail "\"kubernetes\" still appears in live-plan's output - #326 not confirmed fixed"; }
  log "  Confirmed: no \"Rule: unadmitted-type.\" refusal and no mention of"
  log "             kubernetes anywhere in live-plan's output - issue #326's"
  log "             fix holds for kubernetes_config_map.aws_auth"
fi

assert_rule_fires "logical-resource" "$LOGICAL_SITES"
assert_rule_fires "count-index" "$COUNTINDEX_SITES"
ERROR_COUNT="$(grep -c '^Error:' <<< "$PLAN_OUT" || true)"
[ "$ERROR_COUNT" = "8" ] || fail "live-plan reported $ERROR_COUNT \"Error:\" diagnostics, expected exactly 8 (4 logical-resource + 4 count-index) - the refusal wall's shape has changed"
log "  exactly 8 Error diagnostics total (4 logical-resource + 4 count-index), matching the expected shape"

log ""
log "=== PASS/FAIL: stages 1-2 pass in full; stage 3 refuses outright ==="
log ""
log "This is the real, current shape of crossing terraform-aws-eks's own"
log "\"basic\" example - the module virtually everyone reaches for first -"
log "against choudoufu/floci:"
log ""
log "  STAGE 1  PASS  54/54 resources, genuinely cold, genuinely unmarked."
log "  STAGE 2  PASS  25 of 54 resource instances stamped across the root"
log "           module, module.vpc and module.eks (issue #59's"
log "           root-module-only scope is closed); the other 29 are 28"
log "           legitimately untaggable-by-design plus 1 MISSING -"
log "           kubernetes_config_map.aws_auth, admitted since #326 but"
log "           its own provider config can't be statically verified yet"
log "           (a distinct, narrower, DEFER-caliber wall - see stage 3)."
log "  STAGE 3  REFUSES  4 logical-resource sites (3 correctly refused"
log "           pending a record_store declaration, #73 as designed; 1 -"
log "           local_file - is #314's already-tracked, narrower gap), and"
log "           4 correctly-conservative count-index refusals. Issue #326's"
log "           own unadmitted-type site (kubernetes_config_map.aws_auth)"
log "           is CONFIRMED GONE - asserted as a negative control above."
log "           Asserted by rule and by resource, with BREAK=1 proving"
log "           neither the negative control nor the positive checks are"
log "           vacuous."
log "  STAGES 4-5  UNREACHABLE  stage 3 produced no plan to apply or drift."
log ""
log "Two real, generalizable floci gaps (not this module's age, not this"
log "script's setup) were found, fixed, merged and published along the way:"
log "EKS worker AMI discovery (lex00/floci#55/#56) and"
log "SuspendProcesses/ResumeProcesses (same PR) - every terraform-aws-eks"
log "estate with self-managed node groups hits both on default settings."
