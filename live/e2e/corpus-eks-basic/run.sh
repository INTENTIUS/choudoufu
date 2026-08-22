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
#                     = 25 eligible and stamped, 5 RECORDED into the
#                     estate's record store (choudoufu #364, 2026-08-22 -
#                     random_string.suffix, module.eks.random_pet.workers[0]
#                     and [1], module.eks.null_resource.wait_for_cluster[0]
#                     and module.eks.local_file.kubeconfig[0], each one a
#                     type whose value IS its identity and which therefore
#                     has no live object to tag; they used to fall off the
#                     end of the migration as SKIPPED and are now carried
#                     across it), 23 UNTAGGABLE by design (no `tags`
#                     argument in the provider schema - ASGs, launch
#                     configurations, IAM role policy attachments, security
#                     group rules, routes, route table associations), and 1
#                     MISSING - kubernetes_config_map.aws_auth. #326's fix (merged
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
#                     config. STILL FAILS, but the wall is a different and
#                     much deeper one than it was, and there is exactly ONE
#                     of it: 1 Error diagnostic as of 2026-08-22, down from
#                     8, then 4. What is left is "Unlistable
#                     marker-discovered type" on aws_launch_configuration -
#                     nothing was refused for want of a declaration, the run
#                     reached marker discovery and found a type the provider
#                     cannot enumerate at all, so the 2 declared instances
#                     (untaggable by design, no `tags` argument) can be
#                     neither tagged nor listed and therefore cannot be
#                     found again. That is HANDOFF.md's fourth row - handling
#                     it would write a wrong marker, so the instance belongs
#                     on the record rung - and it is choudoufu #364's OTHER
#                     half ("removing admission as a gate: a type with no
#                     table row lands on the record rung instead of
#                     refusing"), not attempted here.
#
#                     Three earlier walls are asserted ABSENT below as
#                     negative controls, each flipped by BREAK=3 (BREAK=1
#                     mutates stage 2 as well and exits there, so it never
#                     reaches these three - see the BREAK entry under
#                     "Environment" below):
#                       - unadmitted-type on kubernetes_config_map.aws_auth,
#                         fixed by issue #326 (merged 852f52073f/a990112e26,
#                         2026-08-20). Its identity - metadata.name/namespace,
#                         fully client-named and statically knowable -
#                         resolves cleanly at plan time. The word
#                         "kubernetes" does still appear four times in the
#                         plan's output, and none of the four is a refusal:
#                         they are the tag sweep's own "no CFN type in the
#                         ARN join table" warnings about four kubernetes_*
#                         types, a statement about the sweep's reach over
#                         another provider entirely. The check excludes those
#                         lines by shape; it used to grep the whole output
#                         for the word and went red on a run where nothing
#                         about kubernetes_config_map had changed.
#                       - logical-resource (was 4 sites): FIXED 2026-08-22 by
#                         choudoufu #364, the implied local record store.
#                         random_string.suffix, null_resource.wait_for_cluster,
#                         random_pet.workers and local_file.kubeconfig were
#                         all refused for one reason and one only - "this
#                         configuration declares no record_store" - and
#                         HANDOFF.md's compatible-by-default principle says a
#                         local store is implied when none is declared, the
#                         way stock implies local state. internal/configs'
#                         decoder now fills one in for every live block, so
#                         all four are admitted with no edit to this estate:
#                         the live block this script injects is still the
#                         same four lines it always was. Stage 2 above shows
#                         the other end of the same change - those same
#                         instances are now SEEDED into that store by
#                         live-import instead of skipped. The change names no
#                         resource type anywhere.
#                       - count-index (was 4 sites): FIXED 2026-08-22. They
#                         were module.vpc's aws_route_table_association.public
#                         and .private, whose subnet_id and route_table_id are
#                         `element(<a sibling resource's splat>, <an index
#                         over count.index>)` (terraform-aws-modules/vpc
#                         v6.6.1 main.tf:200-201 and 348-352). Root cause:
#                         internal/live/lint's RuleCountIndex refused
#                         count.index inside ANY collection accessor on
#                         sight, whatever the collection was, so the run
#                         never reached the two resolutions that already knew
#                         exactly what those spellings mean
#                         (resolveElementCall and resolveIndexedTraversal,
#                         internal/live/identity/splat.go - whose own doc
#                         comment names this gap and declines to attempt it).
#                         element(R[*].attr, idx) computes nothing: it
#                         SELECTS one instance of a sibling managed resource,
#                         and what the identity layer builds from it is a
#                         ParentRef, not a rendered string. The fix is
#                         internal/live/lint/sibling_select.go: RuleCountIndex
#                         steps aside for that shape, because the exact
#                         question it approximates is asked again downstream
#                         by identity's own checkCollisions, over the WHOLE
#                         rendered identity instead of one argument at a time.
#                         That difference is what settles the case splat.go
#                         calls the open question - this example passes
#                         single_nat_gateway = true, so route_table_id
#                         collapses onto ONE route table for every instance
#                         and only subnet_id varies, which per-argument
#                         reasoning must refuse and which is completely safe.
#                         Asserted by value, not by the refusal going away:
#                         internal/live/lint/sibling_select_test.go pins the
#                         three rendered identities as exact strings for both
#                         spellings, and a third fixture where the selection
#                         really does collapse onto one object is still
#                         refused - by checkCollisions, quoting the duplicated
#                         identity. The rule names no resource type and
#                         reaches 574 of the 1042 admitted rows. Was 7 sites
#                         before #321/#324, then 4, now 0.
#   4. TEST APPLY     UNREACHABLE. Stage 3 produced no plan to apply.
#   5. DRIFT/RECONVERGE UNREACHABLE for the same reason.
#
# This script does not paper over stages 4-5 by hand-patching the estate to
# dodge the wall - the point of a real-estate crossing is to find what a real
# user hits, not to manufacture a passing shape. In particular it does not
# declare a record_store: the four logical-resource refusals cleared because
# choudoufu implies one now, not because this estate was edited to have one. Stages 1-2 are
# fully real and asserted; stage 3 is real and its refusal is asserted by
# rule and by resource, with BREAK=1 proving stage 2's assertions and
# BREAK=3 proving stage 3's are load-bearing.
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
#                stage-2 and stage-3 assertions, or to 3 to corrupt only
#                stage 3's, proving each is load-bearing rather than a check
#                that always passes. BREAK=1 never reaches stage 3 (stage 2's
#                own mutation exits first), which is why the stage-3-only
#                value exists: the three negative controls there carry issue
#                #326's, internal/live/lint/sibling_select.go's and choudoufu
#                #364's fixes, and a control nothing ever flips proves
#                nothing.
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
gauntlet_begin
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
CURRENT_STAGE=cold_deploy
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
gauntlet_stage cold_deploy pass "54 resources, genuinely cold, genuinely unmarked"

# ── 4. STAGE 2: migrate ─────────────────────────────────────────────────────
CURRENT_STAGE=migrate
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
#
# 2026-08-22, choudoufu #364 (the implied local record store): 5 of those
# 28 untaggable-by-design instances are no longer SKIPPED, they are
# RECORDED - module.eks.local_file.kubeconfig[0], module.eks.null_resource.
# wait_for_cluster[0], module.eks.random_pet.workers[0] and [1], and
# random_string.suffix. Every one is record-backed: it has no live object
# to tag because its value IS its identity, and an approved migration now
# seeds the estate's record store from the state's own object for it. That
# store exists without this configuration declaring one, which is the whole
# of #364. The stamped count is unchanged (none of the five was ever
# taggable); what moved is 29 skipped -> 24 skipped and 0 newly recorded ->
# 5 newly recorded, which is five state entries that used to fall off the
# end of the migration now carried across it.
EXPECT_ELIGIBLE="25 of 54 resource instance(s) are eligible for stamping"
EXPECT_STAMPED="25 resource(s) newly stamped, 0 already stamped, 5 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 24 skipped."
EXPECT_MISSING_K8S='kubernetes_config_map.*could not be used'
# BREAK=1 mutates BOTH stages, which means it never reaches stage 3: `fail`
# exits, so a BREAK=1 run proves stage 2's control and leaves stage 3's
# unexercised. BREAK=3 mutates stage 3 only, and is what proves this script's
# three negative controls - the ones carrying #326's, sibling_select.go's and
# #364's fixes - are load-bearing rather than vacuously green.
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
log "  live-import's own accounting matches: 25 of 54 resource instances stamped (module.vpc + module.eks are now in scope, issue #59 is closed), 5 record-backed instances seeded into the implied local record store (#364), kubernetes_config_map.aws_auth correctly MISSING (admitted, but its provider config can't be statically evaluated)"

MARKED_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$MARKED_AFTER" = "25" ] || fail "expected 25 objects carrying tofu-estate=$ESTATE after migration, got $MARKED_AFTER"
log "  25 of 25 stamped objects confirmed via the AWS CLI directly"
gauntlet_stage migrate pass "25 of 54 resource instances stamped, 25 of 25 confirmed via the AWS CLI; 5 record-backed instances seeded into the implied local record store (#364)"

# ── 5. STAGE 3: test plan ───────────────────────────────────────────────────
CURRENT_STAGE=test_plan
log "=== 5. STAGE 3 - test plan: choudoufu live-plan against the full config ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ -n "${DUMP_PLAN:-}" ]; then printf '%s\n' "$PLAN_OUT" > "$DUMP_PLAN"; fi
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan exited 0 - this estate's wall did not fire. That would be stages 4-5 becoming reachable, which is good news and not something this script may skip past silently: read the plan and rewrite this stage."; }

# No associative arrays: /bin/bash on macOS is still 3.2 (no `declare -A`
# support at all), and every other corpus-* script in this repo already
# avoids them for exactly that reason.
#
# THREE WALLS ARE ASSERTED ABSENT HERE, and one is asserted present.
#
# Absent (each was this estate's stage-3 wall at some point, each is a
# negative control with BREAK=3 flipping it, so none of them can read as
# green merely because the rule stopped firing for an unrelated reason):
#
#   - unadmitted-type on kubernetes_config_map.aws_auth. Fixed by issue
#     #326 (merged 852f52073f/a990112e26, 2026-08-20); its identity
#     resolves cleanly at plan time. The string "kubernetes" DOES still
#     appear in the plan's output, four times, and none of them is a
#     refusal: they are "Incomplete sweep for undeclared resources"
#     warnings saying the four kubernetes_* types have no CFN type in the
#     ARN join table (internal/live/discovery/tagging.go), which is a
#     statement about the tag sweep's reach over an entirely different
#     provider and not about this configuration's admission. The check
#     below excludes exactly those lines rather than grepping the whole
#     output for the word, which is what it used to do - and which went
#     red on a run where nothing about kubernetes_config_map had changed.
#   - count-index on module.vpc's four aws_route_table_association.public/
#     private, all four of them element(<a sibling resource's splat>, <an
#     index over count.index>). Retired by internal/live/lint/
#     sibling_select.go - see the header.
#   - logical-resource on random_string.suffix, random_pet.workers,
#     null_resource.wait_for_cluster and local_file.kubeconfig. Retired by
#     choudoufu #364, the implied local record store: all four were
#     refused for one reason only - "this configuration declares no
#     record_store" - and a live block now implies a local one, so all
#     four are admitted with no edit to this estate at all. That is
#     HANDOFF.md's "a local record store is implied when none is declared,
#     the way stock implies local state" arriving. Stage 2 above shows the
#     other end of the same change: the same instances are now SEEDED into
#     that store by live-import instead of skipped.
#
# Present, and this estate's current wall:
#
#   - "Unlistable marker-discovered type" on aws_launch_configuration. The
#     two declared instances are untaggable by design (the type has no
#     tags argument), so they can only be found again by marker discovery,
#     and the provider cannot list the type at all - so there is no sweep
#     that could find them. This is a DIFFERENT and deeper wall than the
#     admission ones above: nothing was refused for want of a declaration,
#     the run got all the way to discovery and found a type it cannot
#     enumerate. It is the shape HANDOFF.md's fourth row covers ("handling
#     it would write a wrong marker - drop the instance to the record rung,
#     proceed, open a rung ticket"): an instance that can be neither tagged
#     nor listed belongs on the record rung, and choudoufu #364's OTHER
#     half - "removing admission as a gate: a type with no table row lands
#     on the record rung instead of refusing" - is where that lands.
#
#     Re-confirmed for real 2026-08-22 (base 8c0cc5c8dd): live-import's own
#     output shows this instance's identity IS known at migrate time
#     (state carries the live launch configuration name directly, e.g.
#     "aws_launch_configuration live id: test-eks-...-worker-group-..."),
#     it is simply never captured anywhere, because Approve's OutcomeSkipped
#     branch for an untaggable type (stamp.go) records argument residue
#     (issue #341) but not identity, and #364's implied record store is
#     wired only to [identity.TypeIdentity.RecordBacked] types - a
#     type-level fact for a value that IS its own identity with no live
#     object at all, which aws_launch_configuration is not. Extending that
#     same mechanism to this shape would be a category error, not a reuse:
#     RecordBacked licenses skipping cloud observation entirely (see
#     internal/live/lint/logical_type.go's ClassRecordAdmitted doc), which
#     is false for a real cloud object. The correct shape is narrower - the
#     record stands in only for the missing LIST call (an identity index),
#     and a normal provider Read still verifies the object once its
#     identity is known - which is a new leg, not an existing one:
#       1. discovery.Request needs a record-store handle (internal/command's
#          live_plan.go already opens one, as [hintStore], for guided
#          discovery's cost hint alone - the same handle reaches
#          statelessDiscoverOne already and would only need passing
#          through);
#       2. scanType's declared-instance branch (internal/live/discovery/
#          discovery.go, right where scanTypeMarkerFallback's taggable leg
#          sits) needs a companion untaggable leg that checks the record
#          for each declared address before refusing, binding what it finds
#          and refusing (never "propose create") what it does not - the
#          same non-guess discipline scanTypeMarkerFallback already follows
#          for a working-but-empty tag index;
#       3. internal/live/liveimport/stamp.go's Approve needs to seed that
#          record at migrate time for such an instance, from the same
#          object Ratify already read for residue purposes (issue #341's
#          [residuable]) - the identity is already in hand, only the write
#          is missing.
#     Also checked and ruled out as a shortcut: live/mapping.json maps
#     aws_launch_configuration to AWS::AutoScaling::LaunchConfiguration via
#     "former2" (community-sourced), and live/registry.json's own handlers
#     say that CFN type's List needs no input - roster.Listable(...) and
#     roster.Taggable(...), read directly, answer true and false, matching
#     this estate's wall exactly. But
#     internal/live/registry/roster.go's Roster.CloudControlType /
#     EnumerationSource deliberately excludes every via:former2 row from
#     enumeration regardless (only name/alias/service-alias produce a hit;
#     former2 rows still answer CloudControlTypeOrService for parent
#     derivation, a materially weaker claim than "list this and trust what
#     comes back" - its own doc comment calls former2 rows "not Cloud
#     Control enumerable", which this measurement shows is not literally
#     true of THIS row's raw registry facts, so the exclusion reads as a
#     deliberate blanket policy over the whole former2 tier - lower
#     confidence in the tf_type/cfn_type pairing itself, community-sourced
#     and accepted only because no other heuristic found anything - rather
#     than a per-row listability check). Widening that gate to unblock this
#     one type would silently change discovery for all 14 other
#     via:former2, untaggable, unlistable admitted types at once with no
#     independent per-type confirmation that Cloud Control's population for
#     each one actually matches what the TF type name claims - exactly the
#     guess HANDOFF's fourth row exists to forbid, not a derivation from a
#     measured property. Left to whoever owns that policy to confirm or
#     relax; not changed here.
#
#     Not attempted here; it is foundation work spanning discovery,
#     live-import and the record store together, not a per-estate fix, and
#     this script does not paper over it. The structural property behind
#     it - an admitted type with no tags argument at all AND no working
#     list route (native, content-match or Cloud Control) - reaches 215 of
#     today's admitted AWS types by the same measurement (computed from
#     live/survey-full.json's taggable/list_resource signals joined against
#     the identity table, live/mapping.json and the registry roster), so
#     the fix is generic in exactly the sense HANDOFF requires: derived
#     from a property every one of those types shares, not a name checked
#     in control flow.
LOGICAL_SITES='random_string\.suffix|random_pet\.workers|null_resource\.wait_for_cluster|local_file\.kubeconfig'
COUNTINDEX_SITES='aws_route_table_association\.(public|private)'
# The kubernetes_* lines that are NOT a refusal: the tag sweep's own
# "no CFN type in the ARN join table" warnings. Excluded by exact shape
# rather than by the provider prefix, so a real kubernetes refusal - which
# would say "Rule:" or "Error:" - still trips the check.
K8S_NOT_A_REFUSAL='has no CFN type the ARN join table'

assert_rule_absent() {
  local rule="$1" sites="$2" what="$3"
  grep -qE "Rule: ${rule}\." <<< "$PLAN_OUT" \
    && { grep -E "Rule: ${rule}\." <<< "$PLAN_OUT"; fail "$rule fired unexpectedly - $what may have regressed"; }
  grep -qE "$sites" <<< "$PLAN_OUT" \
    && { grep -E "$sites" <<< "$PLAN_OUT"; fail "the $rule sites still appear in live-plan's output - they are supposed to be past the wall entirely, and a refusal that came back under another rule's name would still print them"; }
  log "  Confirmed: no \"Rule: ${rule}.\" refusal and no mention of its sites - $what holds"
}

if [ "${BREAK:-}" = "1" ] || [ "${BREAK:-}" = "3" ]; then
  log "  BREAK=${BREAK}: expecting \"Rule: unadmitted-type.\", \"Rule: count-index.\""
  log "           and \"Rule: logical-resource.\" to still fire (issue #326's"
  log "           fix, sibling_select.go's fix and #364's implied record"
  log "           store, each deliberately treated as absent). All three"
  log "           must be reported as detected."
  # Collected rather than checked one `fail` at a time, because `fail` exits:
  # a sequence of three would only ever prove the FIRST control, and the run
  # would look like a passing mutation check while two of the three
  # assertions above stayed unexercised. All three are named in one failure.
  BREAK_HITS=""
  grep -qE 'Rule: unadmitted-type\.' <<< "$PLAN_OUT" || BREAK_HITS="$BREAK_HITS unadmitted-type(#326)"
  grep -qE 'Rule: count-index\.' <<< "$PLAN_OUT" || BREAK_HITS="$BREAK_HITS count-index(sibling_select.go)"
  grep -qE 'Rule: logical-resource\.' <<< "$PLAN_OUT" || BREAK_HITS="$BREAK_HITS logical-resource(#364)"
  [ -z "$BREAK_HITS" ] \
    || fail "BREAK=${BREAK} correctly detected: no refusal fired for$BREAK_HITS - every one of those fixes holds and every negative control above is load-bearing (this failure is the expected one)"
else
  assert_rule_absent "unadmitted-type" 'Rule: unadmitted-type\.' "issue #326's fix for kubernetes_config_map.aws_auth"
  K8S_REFUSALS="$(grep -i 'kubernetes' <<< "$PLAN_OUT" | grep -vcF "$K8S_NOT_A_REFUSAL" || true)"
  [ "$K8S_REFUSALS" = "0" ] || {
    grep -i 'kubernetes' <<< "$PLAN_OUT" | grep -vF "$K8S_NOT_A_REFUSAL"
    fail "\"kubernetes\" appears in live-plan's output somewhere other than the tag sweep's own join-table warnings - #326's fix may have regressed"
  }
  log "  Confirmed: the only mentions of kubernetes anywhere in live-plan's"
  log "             output are the tag sweep's four join-table warnings, not"
  log "             a refusal - issue #326's fix holds for"
  log "             kubernetes_config_map.aws_auth"

  assert_rule_absent "count-index" "$COUNTINDEX_SITES" "internal/live/lint/sibling_select.go's element(<sibling splat>, count.index) rule"
  assert_rule_absent "logical-resource" "$LOGICAL_SITES" "choudoufu #364's implied local record store"
fi

# The wall that IS here, asserted by its own words and by the type it names.
grep -qF 'Unlistable marker-discovered type' <<< "$PLAN_OUT" \
  || { grep -E '^Error:' <<< "$PLAN_OUT"; fail "the \"Unlistable marker-discovered type\" error is gone - this estate's wall has moved again; read the plan and rewrite this stage"; }
grep -qE 'cannot list aws_launch_configuration' <<< "$PLAN_OUT" \
  || fail "the unlistable-type error no longer names aws_launch_configuration - the wall has changed shape"
ERROR_COUNT="$(grep -c '^Error:' <<< "$PLAN_OUT" || true)"
[ "$ERROR_COUNT" = "1" ] || {
  grep -E '^Error:' <<< "$PLAN_OUT"
  fail "live-plan reported $ERROR_COUNT \"Error:\" diagnostics, expected exactly 1 (aws_launch_configuration's unlistable-type wall; the 4 logical-resource and 4 count-index sites are fixed) - the wall's shape has changed"
}
log "  exactly 1 Error diagnostic total: aws_launch_configuration is"
log "  marker-discovered and the provider cannot list it, so its 2 declared"
log "  instances cannot be found again. Was 8 diagnostics, then 4, now 1."

gauntlet_stage test_plan fail "1 Error diagnostic: aws_launch_configuration is marker-discovered and unlistable, so its 2 instances cannot be found again; the 4 logical-resource refusals are FIXED by choudoufu #364's implied local record store and the 4 count-index ones by sibling_select.go. Re-confirmed 2026-08-22: the identity IS known at migrate time (state carries the live launch configuration name) but nothing persists it, because #364's record store is wired to RecordBacked types only, a different shape (no live object at all) than an untaggable-but-real one. HANDOFF row 4; the generic property (no tags argument, no working list route) reaches 215 admitted AWS types; fixing it needs discovery wired to the record store plus a migrate-time identity write - foundation work, not attempted here (see this script's header)"
gauntlet_stage test_apply not_run "stage 3 produced no plan to apply or drift"
gauntlet_stage drift_reconverge not_run "stage 3 produced no plan to apply or drift"
CURRENT_STAGE=""
gauntlet_end

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
log "           root-module-only scope is closed), 5 seeded into the implied"
log "           local record store (choudoufu #364), and of the remaining 24"
log "           23 are legitimately untaggable-by-design plus 1 MISSING -"
log "           kubernetes_config_map.aws_auth, admitted since #326 but"
log "           its own provider config can't be statically verified yet"
log "           (a distinct, narrower, DEFER-caliber wall - see stage 3)."
log "  STAGE 3  FAILS  1 Error diagnostic and nothing else, down from 8,"
log "           then 4. What is left is not an admission refusal at all:"
log "           aws_launch_configuration is marker-discovered and the"
log "           provider cannot list it, so its 2 declared instances can be"
log "           neither tagged nor found. That is HANDOFF's fourth row - the"
log "           instance belongs on the record rung - and it is choudoufu"
log "           #364's other half, removing admission as a gate."
log "           The 4 logical-resource sites are FIXED by #364's implied"
log "           local record store, with no edit to this estate; the 4"
log "           count-index sites by internal/live/lint/sibling_select.go;"
log "           issue #326's unadmitted-type site by #326. All three are"
log "           asserted ABSENT above, by rule and by resource, with"
log "           BREAK=3 proving neither the negative controls nor the"
log "           positive check on the remaining wall are vacuous."
log "  STAGES 4-5  UNREACHABLE  stage 3 produced no plan to apply or drift."
log ""
log "Two real, generalizable floci gaps (not this module's age, not this"
log "script's setup) were found, fixed, merged and published along the way:"
log "EKS worker AMI discovery (lex00/floci#55/#56) and"
log "SuspendProcesses/ResumeProcesses (same PR) - every terraform-aws-eks"
log "estate with self-managed node groups hits both on default settings."
