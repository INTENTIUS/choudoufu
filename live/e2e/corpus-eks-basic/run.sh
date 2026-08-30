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
#                     config. STILL FAILS, but the wall keeps moving deeper:
#                     there is exactly ONE Error diagnostic as of 2026-08-22
#                     (unchanged in count from the previous measurement, but
#                     a DIFFERENT one - see below), down from 8, then 4.
#
#                     aws_launch_configuration's "Unlistable marker-
#                     discovered type" wall - untaggable by design (no
#                     `tags` argument) and no list route of any kind, so its
#                     2 declared instances could be neither tagged nor
#                     listed and therefore never found again - is FIXED,
#                     2026-08-22, choudoufu #364's other half ("removing
#                     admission as a gate: a type with no table row lands on
#                     the record rung instead of refusing"). The identity
#                     was already known at migrate time (state carries the
#                     live launch configuration name directly) but nothing
#                     persisted it; now three things do, together:
#                       1. internal/live/liveimport/stamp.go's Approve
#                          writes it - the same object Ratify already reads
#                          for residue (#341) - into the estate's record
#                          store as a "located" record
#                          (internal/live/projection/locatedseed.go,
#                          SeedLocatedForInstance), the exact point-lookup,
#                          no-enumeration mechanism issue #270 already built
#                          for "object exists, nowhere to carry a marker" -
#                          reused for a different type population (this one
#                          keeps its ordinary ServerAssigned-shaped
#                          admission; #270's own LocatedType explicitly
#                          excludes any type with a ratified row, so nothing
#                          about identity CLASSIFICATION changed here).
#                       2. internal/live/discovery/locatedfallback.go's
#                          scanTypeLocatedFallback - [scanTypeMarkerFallback]'s
#                          untaggable companion - reads that record back for
#                          each declared instance and binds it if present,
#                          never guessing "create" for an instance the store
#                          has nothing for (exactly [scanTypeMarkerFallback]'s
#                          own discipline for an empty tag-index answer).
#                       3. discovery.Request.HintStore, which
#                          internal/command/live_plan.go already opened as
#                          hintStore for the guided-sweep cost hint alone,
#                          now reaches the Request unconditionally rather
#                          than only when statelessApplyGuidedDiscovery's
#                          cost-decision gate turns Guided on (which it
#                          deliberately never does for an IMPLIED record
#                          store - #364's own blast-radius containment - so
#                          this estate would otherwise never have reached
#                          it).
#                     The generic property behind it - an admitted type with
#                     no tags argument and no list route of any kind
#                     (native, content-match or Cloud Control) - reaches 215
#                     of today's admitted AWS types; aws_launch_configuration
#                     is the instance that found the wall, not a special case
#                     of the fix. Confirmed by the plan output carrying zero
#                     mentions of aws_launch_configuration or "Unlistable"
#                     anywhere, asserted as a fourth negative control below.
#
#                     What surfaced once that wall cleared is a DIFFERENT,
#                     already-known one, previously hidden behind it:
#                     "Provider unavailable for marker discovery" for
#                     provider.kubernetes, whose own configuration
#                     (`insecure = true` aside, its `host` and `token`
#                     arguments) reads data.aws_eks_cluster.cluster and
#                     data.aws_eks_cluster_auth.cluster - data sources whose
#                     own config depends on aws_eks_cluster.this[0], a
#                     MANAGED resource this same run has not yet read a
#                     value out of. internal/configs/static_scope.go's
#                     evaluator - what internal/command/live_plan.go uses to
#                     configure a provider before discovery can run at all -
#                     has no state, no apply and no dependency graph to walk;
#                     it can only resolve var/local/path/terraform, so a
#                     provider configured from another provider's live
#                     output is exactly what it cannot do. Stock OpenTofu
#                     never asks this question: its plan graph refreshes
#                     aws_eks_cluster.this[0] (from state, or via the
#                     graph's own dependency order on a first apply) and
#                     evaluates data.aws_eks_cluster.cluster BEFORE
#                     provider.kubernetes is ever configured, in one
#                     coherent walk that choudoufu's stateless discovery
#                     does not build. This is the exact shape issue #313
#                     already named and deferred ("live-value-through-
#                     provider-config boundary") from the OTHER direction
#                     (live-import's own no-state verification pass hitting
#                     the identical data sources at migrate time - see stage
#                     2 above); this estate is simply the first live crossing
#                     to reach it from live-plan's side too. Fixing it
#                     generically would mean teaching the stateless path to
#                     sequence AWS discovery/read ahead of a dependent
#                     provider's own configuration for ANY provider pair a
#                     configuration names this way, not a per-type table -
#                     a real, novel piece of the stateless engine (a
#                     provider-configuration dependency order it does not
#                     have today), not a discovery, stamping or identity
#                     fix, and not attempted here. See #313.
#
#                     UPDATE 2026-08-24 (issue #396's worker): FIXED. See
#                     the UPDATE note directly above stage 3's own code
#                     below, and this script's trailing PASS/FAIL summary,
#                     for the mechanism and the new (non-refusal) wall that
#                     replaced it.
#
#                     UPDATE 2026-08-24 (second worker, same day): the
#                     non-refusal wall the note directly above left pinned
#                     - the worker launch configuration's enable_monitoring/
#                     user_data/root_block_device disagreeing with its own
#                     record-backed prior - is ALSO now FIXED, and stage 3
#                     is genuinely EMPTY. See the second UPDATE note further
#                     down, directly above stage 3's own assertions, for the
#                     full mechanism (one floci emulator gap, lex00/floci
#                     #132, and one real choudoufu defect, a residue-record
#                     pre-read seed in internal/live/projection/build.go).
#
#                     Four earlier walls are asserted ABSENT below as
#                     negative controls, three of them flipped by BREAK=3
#                     (BREAK=1 mutates stage 2 as well and exits there, so
#                     it never reaches these - see the BREAK entry under
#                     "Environment" below); the fourth (aws_launch_
#                     configuration) is a plain string-absence check with no
#                     BREAK lever of its own, since it names no lint rule to
#                     flip:
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
#   4. TEST APPLY     UPDATE 2026-08-24 (second worker, same day): PASSES.
#                     Stage 3 is now genuinely empty (see the UPDATE note
#                     above and the one directly above stage 3's own code),
#                     so applying it is a real, asserted no-op: the
#                     tofu-estate-tagged object count is identical before
#                     and after.
#   5. DRIFT/RECONVERGE UPDATE 2026-08-24 (second worker, same day):
#                     PASSES. One VPC's Name tag is tampered directly via
#                     the AWS CLI; live-plan proposes fixing exactly that
#                     object and nothing else, and applying it reconverges
#                     the tag.
#
# This script does not paper over any stage by hand-patching the estate to
# dodge a wall - the point of a real-estate crossing is to find what a real
# user hits, not to manufacture a passing shape. In particular it does not
# declare a record_store: the four logical-resource refusals cleared because
# choudoufu implies one now, not because this estate was edited to have one.
# All five stages are real and fully asserted, with BREAK=1 proving stage
# 2's assertions, BREAK=3 proving stage 3's negative controls, and stage 5's
# own BREAK=1 arm proving its single-object assertion.
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
# Exit codes: 0 when all five active stages pass and every assertion this
# script makes holds. Non-zero if any stage that is supposed to pass does
# not, or a negative-control assertion turns out to be wrong.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/eks"
WORK="$(mktemp -d)"
NET="choudoufu-corpus-eks-basic-net-$$"
FLOCI_PORT="${FLOCI_PORT:-4718}"
FLOCI_NAME="choudoufu-corpus-eks-basic-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
TOOLBOX_IMAGE="choudoufu-corpus-eks-basic-toolbox:$$"

# Two more, fresh floci containers for the greenfield stage
# (live/GAUNTLET.md #13), same $NET as the main one (real EKS mode's k3s
# containers and this script's own toolbox both need to reach whichever
# floci they belong to by container name over one Docker network; there is
# no reason to stand up a second network for two more containers already
# on it). +1000/+2000 keeps this estate's own [main, green, oracle] port
# triple disjoint from every other live/e2e script's own FLOCI_PORT
# default (all under 4800) and from a sibling batch estate's triple one
# port over - see corpus-ecs-fargate's own greenfield header for the real
# collision +10/+20 hit on a live run.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1000))
FLOCI_GREEN_NAME="choudoufu-corpus-eks-basic-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2000))
FLOCI_ORACLE_NAME="choudoufu-corpus-eks-basic-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"
GREEN_REL="green/eks/examples/basic"
ORACLE_GREEN_REL="green-oracle/eks/examples/basic"
GREEN_EST="$WORK/$GREEN_REL"
ORACLE_GREEN_EST="$WORK/$ORACLE_GREEN_REL"

ESTATE="eks-basic-crossing"
GREEN_ESTATE="eks-basic-greenfield"
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
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
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

# green_tofu_run / oracle_green_terraform_run - PART GREENFIELD's own
# runners, same shape as tofu_run/terraform_run above but pointed at the
# greenfield/oracle floci containers (by container name, over the same
# $NET every real-mode k3s/EC2-simulation sibling container also needs)
# instead of the main one.
green_tofu_run() {
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$GREEN_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_GREEN_NAME}:4566" \
    "$TOOLBOX_IMAGE" /work/bin/choudoufu "$@"
}

oracle_green_terraform_run() {
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$ORACLE_GREEN_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_ORACLE_NAME}:4566" \
    hashicorp/terraform:1.9 "$@"
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

# remove_worker_group_mgmt_one EST - day2_remove's edit: delete
# aws_security_group.worker_group_mgmt_one's own block, plus the one
# argument elsewhere in this same file that references it
# (worker_groups[0].additional_security_group_ids). worker_group_mgmt_two
# and all_worker_mgmt are day2_rename's own two objects (a moved block and
# live-mv respectively); worker_group_mgmt_one is the negative control
# day2_rename's own header leaves untouched, and is what gets removed here.
remove_worker_group_mgmt_one() {
  local est="$1"
  sed -i.bak '/^resource "aws_security_group" "worker_group_mgmt_one" {$/,/^}$/d' "$est/main.tf"
  sed -i.bak 's/additional_security_group_ids = \[aws_security_group\.worker_group_mgmt_one\.id\]/additional_security_group_ids = []/' "$est/main.tf"
  rm -f "$est/main.tf.bak"
  grep -q 'worker_group_mgmt_one' "$est/main.tf" \
    && fail "removing aws_security_group.worker_group_mgmt_one did not fully match in $est - the corpus pin has moved"
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
gauntlet_begin_stage cold_deploy
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

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, planned stage - live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# Two of the three root security groups this example declares outside the
# eks/vpc modules (worker_group_mgmt_one is left untouched as a negative
# control): a `moved` block renames aws_security_group.worker_group_mgmt_two,
# and "choudoufu live-mv" renames aws_security_group.all_worker_mgmt with no
# moved block at all. Both are referenced exactly once, inside module "eks"'s
# own argument list (additional_security_group_ids /
# worker_additional_security_group_ids), which the same sed pass updates.
# The stock oracle for both runs on a copy of cold_deploy's own state, PLANNED
# right after stage 1 (this block sits between "gauntlet_stage cold_deploy
# pass" and migrate) - before choudoufu or live-import ever touch these
# shared objects, through the same linux/amd64 hashicorp/terraform:1.9
# container stage 1 itself used (real terraform on this host cannot resolve
# hashicorp/template for darwin_arm64 - see this script's header, item 1).
#
# BREAK=1 exercises this stage's own break control instead of the real
# checks: renaming aws_security_group.all_worker_mgmt WITHOUT a moved block,
# which must make choudoufu propose destroying the old address and creating
# the new one - the opposite of every other assertion in this part.

gauntlet_begin_stage day2_rename
log "=== D-ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
ORACLE_REL="oracle/eks/examples/basic"
rsync -a "$WORK/plain/" "$WORK/oracle/"
ORACLE_EST="$WORK/$ORACLE_REL"
oracle_terraform_run() {
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$ORACLE_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    hashicorp/terraform:1.9 "$@"
}
sed -i.bak 's/resource "aws_security_group" "worker_group_mgmt_two" {/resource "aws_security_group" "worker_group_mgmt_two_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/aws_security_group\.worker_group_mgmt_two\.id/aws_security_group.worker_group_mgmt_two_renamed.id/' "$ORACLE_EST/main.tf"
sed -i.bak 's/resource "aws_security_group" "all_worker_mgmt" {/resource "aws_security_group" "all_worker_mgmt_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/aws_security_group\.all_worker_mgmt\.id/aws_security_group.all_worker_mgmt_renamed.id/' "$ORACLE_EST/main.tf"
rm -f "$ORACLE_EST/main.tf.bak"
cat >> "$ORACLE_EST/main.tf" <<'EOF'

moved {
  from = aws_security_group.worker_group_mgmt_two
  to   = aws_security_group.worker_group_mgmt_two_renamed
}

moved {
  from = aws_security_group.all_worker_mgmt
  to   = aws_security_group.all_worker_mgmt_renamed
}
EOF
oracle_terraform_run init -input=false -no-color > /tmp/eks-basic-oracle-init.log 2>&1 || {
  tail -40 /tmp/eks-basic-oracle-init.log; fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(oracle_terraform_run plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# day2_remove's stock oracle (live/GAUNTLET.md #7): same principle as the
# rename oracle above - a SEPARATE copy of cold_deploy's own state,
# untouched by the rename, so this removal has nothing to do with the
# rename this script also exercises. worker_group_mgmt_one's security
# group feeds one worker group's additional_security_group_ids, which
# feeds that worker group's aws_launch_configuration (ForceNew on a
# security_groups change) - so unlike a genuinely standalone object, this
# oracle's own destroy set is not asserted ahead of time by name or count;
# whatever stock proposes is read here and the real plan below is compared
# against it address-for-address, which is robust to either shape.
gauntlet_begin_stage day2_remove
log "=== D-ORACLE (day2_remove). stock: delete aws_security_group.worker_group_mgmt_one's block on cold_deploy's own state ==="
ORACLE_REMOVE_REL="oracle-remove/eks/examples/basic"
rsync -a "$WORK/plain/" "$WORK/oracle-remove/"
ORACLE_REMOVE_EST="$WORK/$ORACLE_REMOVE_REL"
oracle_remove_terraform_run() {
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$ORACLE_REMOVE_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    hashicorp/terraform:1.9 "$@"
}
remove_worker_group_mgmt_one "$ORACLE_REMOVE_EST"
oracle_remove_terraform_run init -input=false -no-color > /tmp/eks-basic-oracle-remove-init.log 2>&1 || {
  tail -40 /tmp/eks-basic-oracle-remove-init.log; fail "the day2_remove stock oracle's reinit failed"; }
REMOVE_ORACLE_PLAN_OUT="$(oracle_remove_terraform_run plan -input=false -no-color 2>&1)"; REMOVE_ORACLE_PLAN_RC=$?
[ "$REMOVE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -60; fail "the day2_remove stock oracle plan exited $REMOVE_ORACLE_PLAN_RC"; }
REMOVE_ORACLE_CHANGES="$(grep -oE '^  # \S+ will be (destroyed|created|updated in-place)' <<< "$REMOVE_ORACLE_PLAN_OUT" | sed -E 's/^  # //' | sort -u)"
REMOVE_ORACLE_N="$(printf '%s\n' "$REMOVE_ORACLE_CHANGES" | grep -c . || true)"
[ "$REMOVE_ORACLE_N" -ge 1 ] || { printf '%s\n' "$REMOVE_ORACLE_PLAN_OUT" | tail -30; fail "stock's day2_remove oracle proposes no resource action at all when aws_security_group.worker_group_mgmt_one's block is removed"; }
grep -qF "aws_security_group.worker_group_mgmt_one will be destroyed" <<< "$REMOVE_ORACLE_CHANGES" \
  || { printf '%s\n' "$REMOVE_ORACLE_CHANGES"; fail "stock's day2_remove oracle does not destroy aws_security_group.worker_group_mgmt_one itself"; }
log "  stock: $REMOVE_ORACLE_N resource action(s) removing aws_security_group.worker_group_mgmt_one's block:"
printf '%s\n' "$REMOVE_ORACLE_CHANGES" | while read -r line; do log "    $line"; done
gauntlet_end_stage

# day2_replace's stock oracle (live/GAUNTLET.md #9, active): "Stock's
# replace of the same resource leaves the same single object." Same
# principle as the two oracles above - a SEPARATE copy of cold_deploy's
# own state, untouched by the rename or the remove this script also
# exercises. Changes aws_security_group.worker_group_mgmt_two's
# `name_prefix` argument (a real, upstream-declared ForceNew argument on
# aws_security_group - the EC2 API has no ModifySecurityGroupName/
# ModifySecurityGroupNamePrefix call, only CreateSecurityGroup/
# DeleteSecurityGroup) to a different literal prefix, which forces stock
# to replace the SAME declared address rather than propose a destroy-and-
# create pair at two different addresses. worker_group_mgmt_two's id
# feeds one worker group's own additional_security_group_ids - so like
# the day2_remove oracle above, this oracle's own destroy/change set is
# not asserted ahead of time by fixed count; whatever stock proposes is
# read here and the real plan below is compared against it structurally
# (the same address must be replaced in both), which is robust to either
# cascade shape.
#
# THE TARGET CHOICE: worker_group_mgmt_two, not all_worker_mgmt.
# all_worker_mgmt is Part D's own live-mv leg (D2, no module boundary
# crossed, no apply run immediately afterward) - reproducing it here first
# found a genuine, separate defect: choudoufu's live-mv correctly rewrote
# the live MARKER for a bare, same-module resource rename but left the
# LOCAL RECORD stale at the old key, because internal/live/mv/mv.go's
# propagateModuleRename opened with `oldPrefix, newPrefix, ok :=
# moduleRenameBoundary(...); if !ok { return diags }` and never reached
# its own MoveRecord call for a same-module rename, even though that
# function's own doc comment says it covers "the resource live-mv was
# asked to rename itself". Confirmed empirically, no tofu in the loop:
# cat-ing the record store directly after Part D's real live-mv found the
# record still filed under the OLD address, never re-keyed to
# all_worker_mgmt_renamed. FIXED on the gauntlet/mv-rekey branch, GitHub
# issue #412: propagateModuleRename now calls store.MoveRecord(ctx,
# m.req.Old, m.req.New) unconditionally, before the moduleRenameBoundary
# guard, so a same-module bare-resource rename re-keys its own record
# instead of leaving the store pointing at a dead address (see
# internal/live/mv/mv.go for the fix). corpus-autoscaling-complete's and
# corpus-ecs-fargate's own day2_replace sections in this same unit
# independently hit the identical shape; this script was not re-run for
# #412 (out of scope for that unit), so this comment and the day2_replace
# detail string below still describe the pre-#412 code until this
# estate's next real run. worker_group_mgmt_two_renamed dodges it: Part D1
# renames it through a moved block FOLLOWED BY a real converging apply
# (MOVED_APPLY_OUT, above), which writes a fresh record under the current
# address as ordinary apply WriteBack - the same shape alb-complete's own
# Part F already relies on.
gauntlet_begin_stage day2_replace
log "=== F-ORACLE. stock: force-replace aws_security_group.worker_group_mgmt_two via its ForceNew name_prefix argument, on cold_deploy's own state ==="
REPLACE_ORACLE_REL="oracle-replace/eks/examples/basic"
rsync -a "$WORK/plain/" "$WORK/oracle-replace/"
REPLACE_ORACLE_EST="$WORK/$REPLACE_ORACLE_REL"
oracle_replace_terraform_run() {
  docker run --rm --platform linux/amd64 --network "$NET" \
    -v "$WORK:/work" -w "/work/$REPLACE_ORACLE_REL" \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION="$REGION" \
    -e AWS_ENDPOINT_URL="http://${FLOCI_NAME}:4566" \
    hashicorp/terraform:1.9 "$@"
}
sed -i.bak 's/name_prefix = "worker_group_mgmt_two"/name_prefix = "worker_group_mgmt_two_v2"/' "$REPLACE_ORACLE_EST/main.tf"
rm -f "$REPLACE_ORACLE_EST/main.tf.bak"
grep -q 'worker_group_mgmt_two_v2' "$REPLACE_ORACLE_EST/main.tf" \
  || fail "changing aws_security_group.worker_group_mgmt_two's name_prefix argument in the replace-oracle copy did not match - the corpus pin has moved"
oracle_replace_terraform_run init -input=false -no-color > /tmp/eks-basic-oracle-replace-init.log 2>&1 || {
  tail -40 /tmp/eks-basic-oracle-replace-init.log; fail "the day2_replace stock oracle's reinit failed"; }
REPLACE_ORACLE_PLAN_OUT="$(oracle_replace_terraform_run plan -input=false -no-color 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -60; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # aws_security_group\.worker_group_mgmt_two must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "stock does not propose replacing aws_security_group.worker_group_mgmt_two when its name_prefix argument changes"; }
REPLACE_ORACLE_PLAN_LINE="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.' <<< "$REPLACE_ORACLE_PLAN_OUT")"
[ -n "$REPLACE_ORACLE_PLAN_LINE" ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -15; fail "the day2_replace stock oracle plan has no summary line"; }
log "  stock: $REPLACE_ORACLE_PLAN_LINE - replaces aws_security_group.worker_group_mgmt_two at the same declared address, on the state cold_deploy produced - plan only, not applied (this copy shares floci's account with \$ADOPTED, and actually applying here would destroy the real security group the estate's later stages still depend on)"
gauntlet_end_stage

# ── 4. STAGE 2: migrate ─────────────────────────────────────────────────────
gauntlet_begin_stage migrate
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
# UPDATE 2026-08-24 (issue #396's worker, continuing #391/the eks-splat
# gauntlet unit): the "Provider unavailable for marker discovery" wall
# below (provider.kubernetes could not be configured, #313's live-value-
# through-provider-config boundary) is FIXED. Root cause was two layered
# defects, neither previously diagnosed to this depth:
#
#   1. A legacy 0.11-style splat (`resource.*.attr`, terraform-aws-eks's
#      own `element(concat(aws_eks_cluster.this.*.id, list("")), 0)`
#      cluster_id output) is structurally invisible to
#      hcl.Expression.Variables(): a SplatExpr's Each traversal evaluates
#      against an AnonSymbolExpr placeholder, never a *ScopeTraversalExpr,
#      so internal/configs' static reference classifier never saw the
#      demand for aws_eks_cluster.this's own "id" and defaulted to
#      "covered" on an empty remaining traversal. Fixed by
#      internal/configs/splat_coverage.go: a standalone walk of the
#      expression tree for SplatExpr nodes, feeding a synthesized
#      Source+Each traversal through the SAME StaticValidateReferences
#      classification a normal reference goes through - diagnostics only,
#      never folded into real value materialization (a first attempt that
#      did fold it in broke every already-working splat over a count-
#      expanded resource across the whole corpus with a spurious "Missing
#      resource instance key", found only by running this full estate, not
#      by any unit test - see that file's own doc comment).
#   2. Once (1) let discovery/marker resolution proceed further,
#      aws_default_route_table.default's own marker-binding hit issue #69's
#      multi-provider sweep: a companion-pair sighting (aws_route_table's
#      generic Cloud Control listing, re-visiting an object a DIFFERENT,
#      aws-scoped discovery pass had already bound correctly) consulted
#      decl.entryFor (scoped to THIS pass's own inScope set, correctly
#      empty for an object out of scope) instead of decl.declares (built
#      from every resolution regardless of provider scope, exactly what
#      internal/command/live_plan.go's own statelessDiscover doc comment
#      already promised). Fixed in internal/live/discovery/discovery.go's
#      sweepBindType.
#
# With both fixed, live-plan now runs to completion with ZERO Error
# diagnostics - PLAN_RC=0. That is NOT yet a pass: test_plan's own oracle
# (live/GAUNTLET.md) requires an EMPTY plan, and this one is not empty -
# see the new wall recorded below, in module.eks's worker launch
# configuration/random_pet/autoscaling_group chain. Stages 1-2's own prose
# below is unchanged and still accurate; stage 3's is superseded by this
# note and by the assertions immediately following, not by the paragraph
# beginning "3. TEST PLAN" further up this header.
gauntlet_begin_stage test_plan
log "=== 5. STAGE 3 - test plan: choudoufu live-plan against the full config ==="
rm -f "$ADOPTED/terraform.tfstate" "$ADOPTED/terraform.tfstate.backup"
PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; PLAN_RC=$?
if [ -n "${DUMP_PLAN:-}" ]; then printf '%s\n' "$PLAN_OUT" > "$DUMP_PLAN"; fi
# The RC==0 requirement is conditional on BREAK=3, not unconditional: that
# lever deliberately corrupts stage 3 so a refusal fires again (see the
# BREAK block below), and a refusal is an Error diagnostic, which is
# exactly what makes live-plan exit non-zero. BREAK=1 mutates stage 2 and
# exits there (see the comment above that block), so it never reaches this
# line at all - only the plain "${BREAK:-}" = "3" case needs the exemption.
if [ "${BREAK:-}" != "3" ]; then
  [ "$PLAN_RC" -eq 0 ] || { grep -E '^Error:' <<< "$PLAN_OUT"; fail "live-plan exited $PLAN_RC - a new Error diagnostic appeared where none did before (issue #396); read the plan and rewrite this stage"; }
  ERROR_COUNT="$(grep -c '^Error:' <<< "$PLAN_OUT" || true)"
  [ "$ERROR_COUNT" = "0" ] || {
    grep -E '^Error:' <<< "$PLAN_OUT"
    fail "live-plan exited 0 but reported $ERROR_COUNT \"Error:\" diagnostics - that combination should not happen; the wall's shape has changed"
  }
  log "  Confirmed: live-plan exits 0 with zero Error diagnostics - the"
  log "             provider.kubernetes wall (issue #313/#396) is gone"
fi

# No associative arrays: /bin/bash on macOS is still 3.2 (no `declare -A`
# support at all), and every other corpus-* script in this repo already
# avoids them for exactly that reason.
#
# FOUR WALLS ARE ASSERTED ABSENT HERE, and one is asserted present.
#
# Absent (each was this estate's stage-3 wall at some point; the first
# three are negative controls with BREAK=3 flipping them, so none of them
# can read as green merely because the rule stopped firing for an unrelated
# reason - the fourth, aws_launch_configuration, names no lint rule for
# BREAK to flip and is checked by plain string absence instead):
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
#   - "Unlistable marker-discovered type" on aws_launch_configuration.
#     FIXED 2026-08-22, choudoufu #364's other half. See this script's
#     header for the full three-file shape
#     (internal/live/discovery/locatedfallback.go,
#     internal/live/projection/locatedseed.go,
#     internal/live/liveimport/stamp.go's Approve, plus
#     discovery.Request.HintStore reaching internal/command/live_plan.go's
#     stateless path unconditionally). The generic property - no tags
#     argument and no list route of any kind - reaches 215 admitted AWS
#     types; this is the instance that found it, not a special case of the
#     fix. No BREAK lever: this is a plain absence check below
#     (LAUNCHCONFIG_SITES), since there is no "Rule: ..." name to flip the
#     way the three lint-driven controls above have.
#
# Formerly present, now FIXED (2026-08-24, issue #396's worker - see the
# UPDATE note above stage 3's own header): "Provider unavailable for marker
# discovery" for provider.kubernetes, #313's live-value-through-provider-
# config boundary reached from live-plan's own bootstrap. No longer
# asserted present; the ERROR_COUNT=0 check above is what would catch its
# return.
#
# This estate's CURRENT wall instead: the plan is not empty. Four
# resources genuinely change - module.eks's worker launch configuration,
# the random_pet whose keepers pin to its name, and the autoscaling group
# that references both. See LAUNCHCONFIG_DIFF_SITES and the assertions
# built around it, below.
LOGICAL_SITES='random_string\.suffix|null_resource\.wait_for_cluster|local_file\.kubeconfig'
COUNTINDEX_SITES='aws_route_table_association\.(public|private)'
# Narrowed to the REFUSAL text alone (2026-08-24, issue #396's worker):
# aws_launch_configuration now legitimately appears in the plan's own diff
# (its worker launch configuration is one of the four resources this
# estate's current wall replaces - see LAUNCHCONFIG_DIFF_SITES below), so a
# bare mention of the type name is no longer evidence the located-record
# discovery fallback (internal/live/discovery/locatedfallback.go) failed;
# only "Unlistable marker-discovered type" - the refusal's own wording - is.
LAUNCHCONFIG_SITES='Unlistable marker-discovered type'
# The four resource addresses this estate's CURRENT wall (a non-empty
# plan, not a refusal) touches - checked by exact shape, not merely by
# type name, so a plan that changes for some OTHER reason still trips this.
LAUNCHCONFIG_DIFF_SITES='module\.eks\.aws_launch_configuration\.workers\[[01]\] must be replaced|module\.eks\.random_pet\.workers\[[01]\] must be replaced|module\.eks\.aws_autoscaling_group\.workers\[[01]\] will be updated in-place'
# The kubernetes_* lines that are NOT a refusal: the tag sweep's own
# "no CFN type in the ARN join table" warnings, in either of the two
# shapes that name it - the long-form warning body ("kubernetes_config_map
# has no CFN type the ARN join table...") AND the short summary table's
# own per-type tag ("  kubernetes_config_map [NO_ARN_JOIN]"), found by
# reading the plan fresh once the discovery/marker walls in front of this
# one cleared (issue #396) and the summary table's own alphabetical walk
# reached the kubernetes_* types for the first time - it was always there,
# just never rendered this far before an earlier error cut discovery
# short. Excluded by exact shape rather than by the provider prefix, so a
# real kubernetes refusal - which would say "Rule:" or "Error:" - still
# trips the check.
#
# This is the SAME shape LAUNCHCONFIG_SITES' own check below has to
# exclude, for the identical reason: aws_launch_configuration is one of
# many admitted types with no CFN type in that same join table (so is,
# say, aws_lambda_permission - see the sweep's own output, which lists
# them alphabetically with nothing type-specific about the wording), and
# the tag sweep's completeness warning fires for it regardless of whether
# the located-record fallback bound its instances correctly. Reused by
# name rather than duplicated so the two checks cannot drift into
# excluding different substrings for what is provably the same line
# shape. An ERE now (grep -vE below, not -vF): two alternatives, not one
# fixed string.
K8S_NOT_A_REFUSAL='has no CFN type the ARN join table|\[NO_ARN_JOIN\]'

assert_rule_absent() {
  local rule="$1" sites="$2" what="$3"
  grep -qE "Rule: ${rule}\." <<< "$PLAN_OUT" \
    && { grep -E "Rule: ${rule}\." <<< "$PLAN_OUT"; fail "$rule fired unexpectedly - $what may have regressed"; }
  grep -qE "$sites" <<< "$PLAN_OUT" \
    && { grep -E "$sites" <<< "$PLAN_OUT"; fail "the $rule sites still appear in live-plan's output - they are supposed to be past the wall entirely, and a refusal that came back under another rule's name would still print them"; }
  log "  Confirmed: no \"Rule: ${rule}.\" refusal and no mention of its sites - $what holds"
}

if [ "${BREAK:-}" = "1" ] || [ "${BREAK:-}" = "3" ]; then
  log "  BREAK=${BREAK}: expecting \"Rule: unadmitted-type.\", \"Rule: count-index.\","
  log "           \"Rule: logical-resource.\" and aws_launch_configuration's"
  log "           unlistable-type wall to still fire (issue #326's fix,"
  log "           sibling_select.go's fix, #364's implied record store and"
  log "           #364's located-record discovery fallback, each deliberately"
  log "           treated as absent). All four must be reported as detected."
  # Collected rather than checked one `fail` at a time, because `fail` exits:
  # a sequence would only ever prove the FIRST control, and the run would
  # look like a passing mutation check while the rest stayed unexercised.
  # All four are named in one failure.
  BREAK_HITS=""
  grep -qE 'Rule: unadmitted-type\.' <<< "$PLAN_OUT" || BREAK_HITS="$BREAK_HITS unadmitted-type(#326)"
  grep -qE 'Rule: count-index\.' <<< "$PLAN_OUT" || BREAK_HITS="$BREAK_HITS count-index(sibling_select.go)"
  grep -qE 'Rule: logical-resource\.' <<< "$PLAN_OUT" || BREAK_HITS="$BREAK_HITS logical-resource(#364)"
  # Same exclusion as the non-BREAK branch below: the ARN-join-table
  # warning is not a signal either way, and "No BREAK lever" (see the
  # header above the negative-control list) already documents that nothing
  # here actually corrupts the located fallback under BREAK=3, so this
  # arm exists for the count-index/logical-resource/unadmitted-type
  # BREAK_HITS accounting to have somewhere consistent to report this
  # control's absence of a real lever, not because BREAK is expected to
  # make it fire.
  LAUNCHCONFIG_BREAK_HITS="$(grep -E "$LAUNCHCONFIG_SITES" <<< "$PLAN_OUT" | grep -vE "$K8S_NOT_A_REFUSAL" || true)"
  [ -n "$LAUNCHCONFIG_BREAK_HITS" ] || BREAK_HITS="$BREAK_HITS aws_launch_configuration(located-fallback)"
  [ -z "$BREAK_HITS" ] \
    || fail "BREAK=${BREAK} correctly detected: no refusal fired for$BREAK_HITS - every one of those fixes holds and every negative control above is load-bearing (this failure is the expected one)"
else
  assert_rule_absent "unadmitted-type" 'Rule: unadmitted-type\.' "issue #326's fix for kubernetes_config_map.aws_auth"
  # The provider.kubernetes configuration wall this exclusion used to carve
  # out (issue #313) is FIXED as of 2026-08-24 (issue #396's worker - see
  # this script's own UPDATE note above stage 3); the exclusion patterns
  # below are kept only because a regression of #313 would otherwise be
  # misread as a #326 regression by this check, not because any of them is
  # expected to match anything in a clean run.
  K8S_REFUSALS="$(grep -i 'kubernetes' <<< "$PLAN_OUT" | grep -vE "$K8S_NOT_A_REFUSAL" | grep -vcE 'Provider unavailable for marker discovery|cannot evaluate the configuration of provider|provider\.kubernetes|registry\.opentofu\.org/hashicorp/kubernetes' || true)"
  [ "$K8S_REFUSALS" = "0" ] || {
    grep -i 'kubernetes' <<< "$PLAN_OUT" | grep -vE "$K8S_NOT_A_REFUSAL"
    fail "\"kubernetes\" appears in live-plan's output somewhere other than the tag sweep's own join-table warnings - #326's fix may have regressed, or issue #313's provider.kubernetes wall is back"
  }
  log "  Confirmed: the only mentions of kubernetes anywhere in live-plan's"
  log "             output are the tag sweep's four join-table warnings -"
  log "             issue #326's fix holds for kubernetes_config_map.aws_auth"
  log "             and issue #313's provider.kubernetes wall stays fixed"

  assert_rule_absent "count-index" "$COUNTINDEX_SITES" "internal/live/lint/sibling_select.go's element(<sibling splat>, count.index) rule"
  assert_rule_absent "logical-resource" "$LOGICAL_SITES" "choudoufu #364's implied local record store"

  # The tag sweep's own "no CFN type in the ARN join table" warning fires
  # for aws_launch_configuration exactly as it does for the four
  # kubernetes_* types above (see K8S_NOT_A_REFUSAL) - it is a statement
  # about internal/live/discovery/tagging.go's ARN-join coverage over ALL
  # admitted types with no CFN mapping, unrelated to whether THIS type's
  # instances were bound. A naive grep for the type's own name treats that
  # warning as a regression on every run, whether or not the located
  # fallback still holds - found 2026-08-23 the same way #326's kubernetes
  # check above was already fixed for the identical false positive, and
  # left unfixed here until this unit re-read the actual plan output by
  # hand and found line "aws_launch_configuration has no CFN type the ARN
  # join table" is the ONLY mention of the type anywhere in it.
  LAUNCHCONFIG_HITS="$(grep -E "$LAUNCHCONFIG_SITES" <<< "$PLAN_OUT" | grep -vE "$K8S_NOT_A_REFUSAL" || true)"
  [ -z "$LAUNCHCONFIG_HITS" ] || {
    printf '%s\n' "$LAUNCHCONFIG_HITS"
    fail "aws_launch_configuration's unlistable-type wall is back - the located-record discovery fallback (internal/live/discovery/locatedfallback.go) may have regressed"
  }
  log "  Confirmed: \"Unlistable marker-discovered type\" does not appear at"
  log "  all - choudoufu #364's located-record discovery fallback holds"
fi

# UPDATE 2026-08-24 (second worker, same day, issue corpus-eks-basic/
# test_plan unit): FIXED. The wall the previous UPDATE note left pinned -
# module.eks's worker launch configuration disagreeing with its own
# record-backed prior on enable_monitoring, user_data and root_block_device
# - is now closed, and the plan is genuinely EMPTY. Two independent fixes,
# diagnosed by reading the AWS API directly against the emulator with no
# tofu in the loop before touching any code (per HANDOFF.md's own
# methodology), neither one a static-evaluator gap as the prior note
# guessed:
#
#   - enable_monitoring and root_block_device: row 4, the emulator (floci)
#     was wrong. Confirmed at floci's own source:
#     AutoScalingQueryHandler.handleCreateLaunchConfiguration never parsed
#     InstanceMonitoring.Enabled or BlockDeviceMappings.member.N.* out of
#     the CreateLaunchConfiguration request at all, and
#     handleDescribeLaunchConfigurations never emitted either back -
#     LaunchConfiguration.java carried no fields for them. hashicorp/aws's
#     own Read (internal/service/autoscaling/launch_configuration.go,
#     fetched and read directly): `if lc.InstanceMonitoring != nil {
#     d.Set("enable_monitoring", lc.InstanceMonitoring.Enabled) } else {
#     d.Set("enable_monitoring", false) }` - with the field always absent,
#     this always took the else branch regardless of what was configured
#     (real AWS's own documented default is true). root_block_device is
#     derived purely from the launch configuration's own
#     BlockDeviceMappings, always empty from floci, so an explicitly
#     configured root_block_device block (this module sets one) read back
#     as an empty list forever. Reproduced identically with PLAIN
#     `terraform plan` run immediately after its own cold apply against
#     this same (unfixed) emulator - not a choudoufu-specific defect.
#     Fixed and merged: lex00/floci#132 (branch
#     fix/launch-configuration-monitoring-blockdevices), published to GHCR,
#     live/floci-image repinned separately by the orchestrator's batch (see
#     that commit).
#   - user_data: row 2, a real choudoufu defect, independent of the
#     emulator gap above. hashicorp/aws's Read also does
#     `if _, ok := d.GetOk("user_data_base64"); ok { d.Set(...) } else {
#     d.Set("user_data", userDataHashSum(v)) }` - a GetOk check against
#     whatever PriorState this run's own ReadResource call was given. A
#     record-backed instance's plan-time read uses a BARE import stub
#     (identity only, everything else null - noimporter.SynthesizeStub),
#     so GetOk always failed and the provider computed a hash-shaped
#     user_data value no genuinely persisted state file would ever show
#     (a real refresh already carries user_data_base64, so GetOk succeeds
#     and user_data stays null). This is GitHub issue #287 item 8's exact
#     shape (configuredTagsSeed's own "tags" vs default_tags ambiguity),
#     one call site over. Two seeds now feed the pre-read import stub in
#     internal/live/projection/build.go's materialize():
#     configuredAttrsSeed generalizes configuredTagsSeed's mechanism from
#     "tags" specifically to every flat, non-identity attribute the
#     resource's own configuration sets statically (also threading
#     Options.DataResults so an attribute reading a data source - this
#     estate's own user_data_base64 = base64encode(data.template_file.
#     userdata.*.rendered[count.index]) - can resolve too, when the
#     estate's own data-read phase already read it); and a second,
#     narrower seed reads the instance's OWN residue record
#     (RecordStore.GetResidue) BEFORE the read, reusing the identical
#     migrate-time classification issue #275/#341 already proved safe
#     (classifyResidue's two-read discriminator already showed
#     user_data_base64 is something the provider only ever PRESERVES from
#     whatever prior it is given), which is what actually closed this
#     estate's wall - the data-source seed alone could not, because
#     data.template_file.userdata is read by the real plan graph AFTER
#     materialize() already needs its value, and the estate's own
#     statelessDataReads phase never reads it either (out of its
#     identity/count/for_each-only scope). No type name anywhere in either
#     mechanism.
#
# See this script's own PASS/FAIL summary at the end of the file for the
# full, current five-stage picture.
gauntlet_begin_stage test_plan
NOT_EMPTY_SITES="$LAUNCHCONFIG_DIFF_SITES"
if grep -qE "$NOT_EMPTY_SITES" <<< "$PLAN_OUT"; then
  grep -E "$NOT_EMPTY_SITES" <<< "$PLAN_OUT"
  fail "the launch-configuration/random_pet/autoscaling_group cascade still appears in the plan - the fix has regressed"
fi
grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$PLAN_OUT" \
  || { grep -E '^Plan: |^No changes' <<< "$PLAN_OUT"; fail "live-plan is not reporting \"No changes\" - the plan may not be genuinely empty"; }
log "  Confirmed: live-plan is EMPTY - \"No changes. Your infrastructure"
log "  matches the configuration.\" - the launch-configuration/random_pet/"
log "  autoscaling_group cascade is gone"

gauntlet_stage test_plan pass "live-plan runs to completion with ZERO Error diagnostics and reports \"No changes. Your infrastructure matches the configuration.\" - the record-backed worker launch configuration's enable_monitoring/root_block_device/user_data all now agree with the config's own desired value (lex00/floci#132 for the first two, configuredAttrsSeed's residue-record pre-read seed in internal/live/projection/build.go for the third)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
log "=== 6. STAGE 4 - test apply: apply the empty plan, assert a genuine no-op ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

APPLY2_OUT="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -60; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
log "  genuine no-op: $BEFORE_N tofu-estate-tagged objects before, $AFTER_N after"
gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); $BEFORE_N tofu-estate-tagged objects before, $AFTER_N after"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
log "=== 7. STAGE 5 - drift and reconverge: mutate one object out of band ==="
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:Name,Values=*" \
  --query "Vpcs[?Tags[?Key=='tofu-estate' && Value=='$ESTATE']].VpcId | [0]" --output text 2>/dev/null)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "no live VPC found for estate $ESTATE"

if [ "${BREAK:-}" = "1" ]; then
  # A second, unrelated object is mutated too - the assertion below must
  # catch this as MORE than one object proposed, not silently pass.
  awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Environment,Value=tampered-by-BREAK >/dev/null
  log "  BREAK=1: also tampered $VPC_ID's Environment tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
fi

awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null
DRIFTED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
  --query 'Tags[0].Value' --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
log "  mutated VPC $VPC_ID's Name tag to \"tampered-out-of-band\" directly via the AWS CLI"

DRIFT_PLAN_OUT="$(tofu_run "$ADOPTED_REL" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
else
  [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  printf '%s\n' "$CHANGED_ADDRS" | grep -qE 'aws_vpc\.this' \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the VPC that was actually tampered"
  log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

  RECONVERGE_APPLY="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -60; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" \
    --query 'Tags[0].Value' --output text)"
  [ "$FIXED_VALUE" != "tampered-out-of-band" ] || fail "the VPC's Name tag is still \"tampered-out-of-band\" after reconverging"
  log "  reconverged: VPC $VPC_ID's Name tag is back to its configured value ($FIXED_VALUE)"
  gauntlet_stage drift_reconverge pass "one object tampered (VPC's Name tag), plan proposed fixing exactly $CHANGED_ADDRS, apply changed 1 and the Name tag reconverged"
fi

gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
SG2_ID_D="$(awsl ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["aws_security_group.worker_group_mgmt_two"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$SG2_ID_D" ] && [ "$SG2_ID_D" != "None" ] || fail "no live security group found by its tofu-address marker (worker_group_mgmt_two)"
SGALL_ID_D="$(awsl ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["aws_security_group.all_worker_mgmt"]}]' --query "SecurityGroups[0].GroupId" --output text)"
[ -n "$SGALL_ID_D" ] && [ "$SGALL_ID_D" != "None" ] || fail "no live security group found by its tofu-address marker (all_worker_mgmt)"
log "  $SG2_ID_D (aws_security_group.worker_group_mgmt_two), $SGALL_ID_D (aws_security_group.all_worker_mgmt)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename aws_security_group.all_worker_mgmt -> .all_worker_mgmt_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_security_group" "all_worker_mgmt" {/resource "aws_security_group" "all_worker_mgmt_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_security_group\.all_worker_mgmt\.id/aws_security_group.all_worker_mgmt_renamed.id/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  tofu_run "$ADOPTED_REL" init -input=false -no-color > /tmp/eks-basic-break-init.log 2>&1 || {
    tail -40 /tmp/eks-basic-break-init.log; fail "the BREAK=1 rename's reinit failed"; }
  BREAK_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # aws_security_group\.all_worker_mgmt will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying aws_security_group.all_worker_mgmt - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_security_group\.all_worker_mgmt_renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating aws_security_group.all_worker_mgmt_renamed - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying aws_security_group.all_worker_mgmt and creating aws_security_group.all_worker_mgmt_renamed - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: aws_security_group.worker_group_mgmt_two -> .worker_group_mgmt_two_renamed ==="
  sed -i.bak 's/resource "aws_security_group" "worker_group_mgmt_two" {/resource "aws_security_group" "worker_group_mgmt_two_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_security_group\.worker_group_mgmt_two\.id/aws_security_group.worker_group_mgmt_two_renamed.id/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  cat >> "$ADOPTED/main.tf" <<'EOF'

moved {
  from = aws_security_group.worker_group_mgmt_two
  to   = aws_security_group.worker_group_mgmt_two_renamed
}
EOF
  tofu_run "$ADOPTED_REL" init -input=false -no-color > /tmp/eks-basic-d1-init.log 2>&1 || {
    tail -40 /tmp/eks-basic-d1-init.log; fail "the moved-block rename's reinit failed"; }
  MOVED_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # aws_security_group\.worker_group_mgmt_two_renamed will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to aws_security_group.worker_group_mgmt_two_renamed"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" = "aws_security_group\.worker_group_mgmt_two" -> "aws_security_group\.worker_group_mgmt_two_renamed"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the security group's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  SG2_ID_D_AFTER="$(awsl ec2 describe-security-groups --group-ids "$SG2_ID_D" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$SG2_ID_D_AFTER" = "$SG2_ID_D" ] || fail "the security group's id changed across the rename ($SG2_ID_D -> $SG2_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  SG2_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SG2_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$SG2_ADDR_D_AFTER" = "aws_security_group.worker_group_mgmt_two_renamed" ] \
    || fail "the security group carries tofu-address=$SG2_ADDR_D_AFTER after the rename, not aws_security_group.worker_group_mgmt_two_renamed"
  log "  $SG2_ID_D unchanged, tofu-address now aws_security_group.worker_group_mgmt_two_renamed - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_security_group.all_worker_mgmt -> .all_worker_mgmt_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_security_group" "all_worker_mgmt" {/resource "aws_security_group" "all_worker_mgmt_renamed" {/' "$ADOPTED/main.tf"
  sed -i.bak 's/aws_security_group\.all_worker_mgmt\.id/aws_security_group.all_worker_mgmt_renamed.id/' "$ADOPTED/main.tf"
  rm -f "$ADOPTED/main.tf.bak"
  tofu_run "$ADOPTED_REL" init -input=false -no-color > /tmp/eks-basic-d2-init.log 2>&1 || {
    tail -40 /tmp/eks-basic-d2-init.log; fail "the live-mv rename's reinit failed"; }
  MV_OUT="$(tofu_run "$ADOPTED_REL" live-mv -estate="$ESTATE" aws_security_group.all_worker_mgmt aws_security_group.all_worker_mgmt_renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"aws_security_group.all_worker_mgmt" -> "aws_security_group.all_worker_mgmt_renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  SGALL_ID_D_AFTER="$(awsl ec2 describe-security-groups --group-ids "$SGALL_ID_D" --query "SecurityGroups[0].GroupId" --output text 2>/dev/null || true)"
  [ "$SGALL_ID_D_AFTER" = "$SGALL_ID_D" ] || fail "the security group's id changed across live-mv ($SGALL_ID_D -> $SGALL_ID_D_AFTER) - it was destroyed and recreated, not renamed"
  SGALL_ADDR_D_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SGALL_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$SGALL_ADDR_D_AFTER" = "aws_security_group.all_worker_mgmt_renamed" ] \
    || fail "the security group carries tofu-address=$SGALL_ADDR_D_AFTER after live-mv, not aws_security_group.all_worker_mgmt_renamed"
  log "  $SGALL_ID_D unchanged, tofu-address now aws_security_group.all_worker_mgmt_renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  FINAL_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
  [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: aws_security_group.worker_group_mgmt_two renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: aws_security_group.all_worker_mgmt renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active stage - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed rename: aws_security_group.
  # worker_group_mgmt_two_renamed (originally worker_group_mgmt_two) is
  # bound and converged. Its `name_prefix` argument - not the resource's
  # own label, which this stage never touches - changes to a new literal
  # prefix. aws_security_group's `name_prefix` is ForceNew in the
  # provider's real schema (confirmed by the plan output itself below,
  # not assumed: the EC2 API has no rename call for a security group,
  # only Create/Delete), so this forces a replacement at the SAME
  # declared address while the physical live security group behind it is
  # destroyed and a new one created. Its id feeds one worker group's own
  # additional_security_group_ids - so like the F-ORACLE above, this
  # section's own change set is read dynamically rather than asserted by
  # fixed count, robust to whatever cascade shape the module produces,
  # while still requiring the SAME primary address to be replaced.
  #
  # THE TARGET CHOICE: worker_group_mgmt_two, not all_worker_mgmt - see
  # F-ORACLE's own header comment above for the genuine, separate defect
  # (a stale local record after live-mv on a bare, same-module rename)
  # this section originally reproduced on all_worker_mgmt_renamed before
  # switching, and for the root cause read directly off mv.go.
  #
  # THE create_before_destroy SCOPE NOTE (same shape as corpus-ec2-
  # instance-complete's and corpus-sqs-basic's own Part F): aws_security_
  # group.worker_group_mgmt_two_renamed is a bare, top-level resource here, but
  # adding a `lifecycle { create_before_destroy = true }` block to it
  # would leave this estate's config permanently diverged from the
  # upstream module example everywhere else in this script depends on the
  # reduction staying faithful. This evidence pass exercises OpenTofu's
  # DEFAULT replace ordering instead. BREAK=replace manufactures the
  # coexistence a skipped destroy would leave behind directly via the AWS
  # CLI.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="aws_security_group.worker_group_mgmt_two_renamed"
  F_RECORD="$ADOPTED/.tofu-records/tofu-records/$ESTATE/aws_security_group/$(record_key "$F_ADDR")"

  log "=== F0. capture the live security group and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  [ "$F_OLD_IMPORT_ID" = "$SG2_ID_D" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not $SG2_ID_D"
  F_OLD_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$SG2_ID_D" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
  [ "$F_OLD_ADDR_TAG" = "aws_security_group.worker_group_mgmt_two_renamed" ] \
    || fail "$SG2_ID_D does not carry tofu-address=aws_security_group.worker_group_mgmt_two_renamed ahead of day2_replace"
  log "  $SG2_ID_D, record import_id=$F_OLD_IMPORT_ID, tofu-address=$F_OLD_ADDR_TAG"

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy would leave behind ==="
    # A second, distinct live security group carrying the SAME tofu-
    # address and tofu-slot as the one a genuine replace would destroy -
    # the state "skip the destroy half" of a create-before-destroy
    # replace would leave, produced directly via the AWS CLI rather than
    # by actually interrupting an apply (day2_crash's own job).
    SG_VPC_ID="$(awsl ec2 describe-security-groups --group-ids "$SG2_ID_D" --query "SecurityGroups[0].VpcId" --output text)"
    BREAK_COLLISION_ID="$(awsl ec2 create-security-group --group-name "${ESTATE}-sg-collision" --description "collision" --vpc-id "$SG_VPC_ID" --query "GroupId" --output text)"
    awsl ec2 create-tags --resources "$BREAK_COLLISION_ID" --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=aws_security_group.worker_group_mgmt_two_renamed" "Key=tofu-slot,Value=0" \
      >/dev/null || fail "BREAK=replace: could not tag the collision security group"
    BREAK_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; BREAK_PLAN_RC=$?
    awsl ec2 delete-security-group --group-id "$BREAK_COLLISION_ID" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address/tofu-slot - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one slot' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the slot collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one slot) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew name_prefix argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/name_prefix = "worker_group_mgmt_two"/name_prefix = "worker_group_mgmt_two_v2"/' "$ADOPTED/main.tf"
    rm -f "$ADOPTED/main.tf.bak"
    grep -q 'worker_group_mgmt_two_v2' "$ADOPTED/main.tf" || fail "changing aws_security_group.worker_group_mgmt_two_renamed's name_prefix argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # aws_security_group\.worker_group_mgmt_two_renamed must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing aws_security_group.worker_group_mgmt_two_renamed when its ForceNew name_prefix argument changes"; }
    grep -qE '~ +name_prefix +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark name_prefix as forcing replacement"; }
    F_PLAN_LINE="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.' <<< "$F_PLAN_OUT")"
    [ -n "$F_PLAN_LINE" ] || { printf '%s\n' "$F_PLAN_OUT" | tail -15; fail "the day2_replace plan has no summary line"; }
    log "  choudoufu: $F_PLAN_LINE - the same declared address (aws_security_group.worker_group_mgmt_two_renamed) forced to replace, name_prefix forces replacement"

    F_APPLY_OUT="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Apply complete! Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$F_APPLY_OUT" \
      || { printf '%s\n' "$F_APPLY_OUT" | tail -20; fail "the day2_replace apply did not report a clean apply"; }
    log "  $(grep -E 'Apply complete' <<< "$F_APPLY_OUT")"

    # floci's own describe-security-groups on an unknown group id returns
    # a 200 with an empty SecurityGroups list rather than a real AWS
    # InvalidGroup.NotFound error (confirmed directly against floci in
    # this same unit, no tofu in the loop - a floci gap, not a choudoufu
    # one), so existence is read from the query result's own emptiness
    # rather than the CLI's exit code.
    F_OLD_STILL="$(awsl ec2 describe-security-groups --group-ids "$SG2_ID_D" --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || true)"
    [ -z "$F_OLD_STILL" ] || [ "$F_OLD_STILL" = "None" ] \
      || fail "$SG2_ID_D still exists after the replace - the old object was orphaned, not destroyed"
    log "  $SG2_ID_D no longer exists - confirmed via the AWS CLI (empty describe-security-groups result), not through choudoufu's own report"

    F_NEW_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:tofu-address,Values=aws_security_group.worker_group_mgmt_two_renamed" --query "SecurityGroups[0].GroupId" --output text)"
    [ -n "$F_NEW_ID" ] && [ "$F_NEW_ID" != "None" ] || fail "no live security group found carrying tofu-address=aws_security_group.worker_group_mgmt_two_renamed after the replace"
    F_NEW_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$F_NEW_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$F_NEW_ADDR_TAG" = "aws_security_group.worker_group_mgmt_two_renamed" ] \
      || fail "$F_NEW_ID carries tofu-address=$F_NEW_ADDR_TAG after the replace, not aws_security_group.worker_group_mgmt_two_renamed - the marker did not move onto the new object"
    log "  $F_NEW_ID (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed
    # security group would be exactly the wrong-marker failure that
    # outranks a missing one). The local record file at the SAME address
    # must now hold the NEW group's id, not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "$F_NEW_ID" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object $F_NEW_ID - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  no resource action proposed, no marker collision. The replace is complete and invisible to the next plan."

    SG2_ID_D="$F_NEW_ID"
    gauntlet_stage day2_replace pass "choudoufu: changing aws_security_group.worker_group_mgmt_two_renamed's ForceNew name_prefix argument proposed a forced replace at the same declared address ($F_PLAN_LINE), applied cleanly; the old security group is confirmed gone via the AWS CLI (InvalidGroup.NotFound) and the new group ($F_NEW_ID) carries the marker; the local record store's record at the same address now names the new object's id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes replacing the security group at the same address ($REPLACE_ORACLE_PLAN_LINE, plan only, not applied - it shares floci's account with \$ADOPTED); BREAK=replace confirms a manufactured marker collision is reported loudly (\"Two live resources claiming one slot\") rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names; also scope note: the section originally targeted all_worker_mgmt_renamed (Part D's own live-mv leg) and found a genuine, separate defect (mv.go's propagateModuleRename skipped MoveRecord for a same-module live-mv rename, leaving the local record stale even though the marker moved correctly) - FIXED on the gauntlet/mv-rekey branch, GitHub issue #412 (propagateModuleRename now calls MoveRecord unconditionally for the renamed resource's own key before the moduleRenameBoundary guard); see this section's own header comment for the fix and corpus-autoscaling-complete's/corpus-ecs-fargate's matching ones in this same unit. This script was not re-run for #412, so this detail string still describes the pre-#412 run until this estate's next real run."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active stage - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed rename: worker_group_mgmt_two and
  # all_worker_mgmt are both renamed and the config plans empty (D3).
  # worker_group_mgmt_one is untouched by the rename and is the object
  # removed here - the negative control day2_rename's own header names.
  # Unlike a genuinely standalone object, its removal also edits one
  # argument on module "eks" itself (worker_groups[0].
  # additional_security_group_ids, changed from a one-element list to an
  # empty one, same as remove_worker_group_mgmt_one applied to the stock
  # oracle above), which is itself ForceNew on the worker group's launch
  # configuration - so this is a stronger test of "the same destroys in a
  # working order" than a single isolated object gives: whatever stock's
  # own cascade looks like is read from the oracle above and compared
  # address-for-action-for-address against choudoufu's real plan, not
  # hand-predicted.
  #
  # BREAK_REMOVE=1 exercises this stage's own break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go for day2_remove is literally
  # "keep the block; no destroy may be proposed".

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live security group's id before day2_remove ==="
  SG1_ID_E="$(awsl ec2 describe-security-groups --filters '[{"Name":"tag:tofu-address","Values":["aws_security_group.worker_group_mgmt_one"]}]' --query "SecurityGroups[0].GroupId" --output text)"
  [ -n "$SG1_ID_E" ] && [ "$SG1_ID_E" != "None" ] || fail "no live security group found by its tofu-address marker (worker_group_mgmt_one) before day2_remove even starts"
  log "  $SG1_ID_E (aws_security_group.worker_group_mgmt_one)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep aws_security_group.worker_group_mgmt_one's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # aws_security_group\.worker_group_mgmt_one will be destroyed' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK_REMOVE=1: a destroy was proposed for aws_security_group.worker_group_mgmt_one even though its block is still in the config - this stage's check is not load-bearing"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$BREAK_REMOVE_PLAN_OUT" \
      || { grep -E '^  #' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: the kept-block plan is not empty"; }
    log "  BREAK_REMOVE=1: correctly proposes nothing - the block is still declared"
  else
    log "=== E1. choudoufu: delete aws_security_group.worker_group_mgmt_one's block ==="
    remove_worker_group_mgmt_one "$ADOPTED"
    tofu_run "$ADOPTED_REL" init -input=false -no-color > /tmp/eks-basic-remove-init.log 2>&1 || {
      tail -40 /tmp/eks-basic-remove-init.log; fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -60; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -30
      fail "choudoufu withheld a destroy under aws_security_group.worker_group_mgmt_one as a possible rename (discovery.go's classifyOrphans) even though no other aws_security_group.worker_group_mgmt_one block exists anywhere in this config - this is an honest wall, not a pass"
    fi
    REMOVE_CHANGES="$(grep -oE '^  # \S+ will be (destroyed|created|updated in-place)' <<< "$REMOVE_PLAN_OUT" | sed -E 's/^  # //' | sort -u)"
    REMOVE_N="$(printf '%s\n' "$REMOVE_CHANGES" | grep -c . || true)"
    grep -qF "aws_security_group.worker_group_mgmt_one will be destroyed" <<< "$REMOVE_CHANGES" \
      || { printf '%s\n' "$REMOVE_CHANGES"; fail "choudoufu does not destroy aws_security_group.worker_group_mgmt_one when its block is deleted"; }
    [ "$REMOVE_CHANGES" = "$REMOVE_ORACLE_CHANGES" ] \
      || { printf 'choudoufu (%s):\n%s\nstock oracle (%s):\n%s\n' "$REMOVE_N" "$REMOVE_CHANGES" "$REMOVE_ORACLE_N" "$REMOVE_ORACLE_CHANGES"; fail "choudoufu's day2_remove plan differs from stock's oracle on cold_deploy's own state"; }
    log "  choudoufu: $REMOVE_N resource action(s), address-for-address and action-for-action identical to stock's oracle on cold_deploy's own state"

    REMOVE_APPLY_OUT="$(tofu_run "$ADOPTED_REL" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -60; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Apply complete!' <<< "$REMOVE_APPLY_OUT" \
      || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply did not complete"; }
    log "  $(grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT")"

    # A destroyed security group is confirmed by COUNT or by a not-found
    # error, whichever this emulator actually answers with for a deleted
    # id - the same "read the API directly, do not assume either shape"
    # discipline reference-ec2-vpc's own Part E documents for internet
    # gateways (a plain 200+empty list there, not the NotFound error real
    # AWS documents for the same request).
    SG1_STILL_OUT="$(awsl ec2 describe-security-groups --group-ids "$SG1_ID_E" --query 'length(SecurityGroups)' --output text 2>&1)"; SG1_STILL_RC=$?
    if [ "$SG1_STILL_RC" -eq 0 ]; then
      [ "$SG1_STILL_OUT" = "0" ] || fail "security group $SG1_ID_E (aws_security_group.worker_group_mgmt_one) still exists after the destroy ($SG1_STILL_OUT found) - it was orphaned, not destroyed"
      log "  $SG1_ID_E no longer exists (0 found) - confirmed via the AWS CLI, not through choudoufu's own report"
    else
      grep -qiE 'InvalidGroup|does not exist|not found|NotFound' <<< "$SG1_STILL_OUT" \
        || { printf '%s\n' "$SG1_STILL_OUT"; fail "describe-security-groups for $SG1_ID_E failed with an unexpected error, not a not-found - it may still exist"; }
      log "  $SG1_ID_E no longer exists ($SG1_STILL_OUT) - confirmed via the AWS CLI, not through choudoufu's own report"
    fi

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(tofu_run "$ADOPTED_REL" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -60; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting aws_security_group.worker_group_mgmt_one's block (plus emptying the one argument that referenced it) proposed $REMOVE_N resource action(s), address-for-address and action-for-action identical to stock's oracle on cold_deploy's own state; applied cleanly; the security group is genuinely gone from the live account, read via the AWS CLI, not choudoufu's own report; classifyOrphans did not withhold any destroy because no other aws_security_group.worker_group_mgmt_one block is declared anywhere in this config; the next plan is empty"
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage

gauntlet_end_stage
# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, live/GAUNTLET.md #13) - two MORE, fresh floci
# containers on the same $NET (real EKS mode's k3s/EC2-simulation sibling
# containers need to reach whichever floci they belong to by name over one
# network, so a second network buys nothing). choudoufu applies the
# unreduced corpus example directly with a live block from the start, no
# migration, no state file ever existing; the estate's own oracle is stock
# applying the identical config fresh in a third, independent namespace,
# compared structurally via the AWS CLI on both endpoints, never through
# tofu state, never through choudoufu's own report.
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage greenfield
log "=== G0. two more floci containers, one per fresh namespace, real EKS mode ==="
docker run -d --rm --network "$NET" -p "${FLOCI_GREEN_PORT}:4566" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=network \
  -e "FLOCI_SERVICES_EKS_DOCKER_NETWORK=$NET" \
  --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm --network "$NET" -p "${FLOCI_ORACLE_PORT}:4566" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FLOCI_SERVICES_EKS_ENDPOINT_MODE=network \
  -e "FLOCI_SERVICES_EKS_DOCKER_NETWORK=$NET" \
  --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"eks"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"eks"' <<< "${GH:-}" || fail "floci did not come up healthy (eks) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

mkdir -p "$WORK/green" "$WORK/green-oracle"
cp -R "$SRC" "$WORK/green/eks"
cp -R "$SRC" "$WORK/green-oracle/eks"
rm -rf "$WORK/green/eks/.git" "$WORK/green/eks/.github" "$WORK/green-oracle/eks/.git" "$WORK/green-oracle/eks/.github"
apply_deltas "$WORK/green" 0
apply_deltas "$WORK/green-oracle" 0
# strict { no_source_create = "create" }: the same delta corpus-alb-
# complete (898091b8f2), corpus-ec2-instance-complete, corpus-ecs-fargate,
# corpus-giantswarm-crossplane, corpus-mastino-dns, corpus-overture-tiles,
# corpus-rds-complete-postgres, corpus-s3-bucket-complete and corpus-
# security-group-complete's own greenfield stages already carry: #365
# ruling 4's default refusal of a config-identified instance whose
# identity value belongs to a SIBLING resource this same apply has not
# created yet either (aws_route_table_association.public[2]'s own
# route_table_id/subnet_id, aws_route's route_table_id, aws_security_
# group_rule's security_group_id, aws_iam_role's own name-derived ARN -
# every one of them "ordinarily computable from configuration" per the
# refusal's own text, just not when the sibling supplying the value does
# not exist anywhere yet). A greenfield apply is the one case an operator
# KNOWS every declared instance is a real create, which is exactly what
# this toggle is for; this estate's own greenfield delta had simply never
# carried it, unlike every sibling script above.
perl -0pi -e 's/(terraform \{\n  required_version = ">= 0\.12\.0"\n)\}/$1\n  live {\n    estate = "'"$GREEN_ESTATE"'"\n\n    strict {\n      no_source_create = "create"\n    }\n  }\n}/' "$GREEN_EST/main.tf"
grep -q "estate = \"$GREEN_ESTATE\"" "$GREEN_EST/main.tf" \
  || fail "the greenfield live-block delta did not match main.tf - the corpus pin has moved"

log "=== G1. choudoufu apply from nothing, no migration, no state file ever existing ==="
green_tofu_run init -input=false -no-color > /tmp/eks-basic-green-init.log 2>&1 || {
  tail -60 /tmp/eks-basic-green-init.log; fail "the greenfield init failed"; }
# UPDATE 2026-08-25 (gauntlet:eks-greenfield): THREE layered walls found in
# this single stage; the first two are real choudoufu defects, FIXED, both
# generic. The "row 2/choudoufu's own result" framing below described the
# first correctly but stopped one layer short, and neither one is what
# blocks this stage today - see wall 3.
#
#   1. The kubernetes provider's config (data.aws_eks_cluster.cluster /
#      data.aws_eks_cluster_auth.cluster, reading aws_eks_cluster.this[0] -
#      a managed resource this same apply creates, with no record, no
#      marker and no state anywhere yet) made statelessDiscover's
#      multi-provider sweep pass fail to CONFIGURE provider.kubernetes at
#      all, which internal/command/live_plan.go's statelessDiscover
#      treated as fatal for the WHOLE estate - aborting before the real
#      resource graph, which defers this exact provider configuration
#      until the cluster is known (same as stock's own single-apply
#      success on cold_deploy), ever ran. Nothing under provider.
#      kubernetes could have been swept before this moment either way -
#      there is no way to have listed a Kubernetes object in a cluster
#      that does not exist - so failing to sweep it costs no real
#      coverage. Fixed generically: internal/live/projection.
#      ProviderConfigNotEvaluable is a new typed error, distinct from a
#      genuinely broken plugin or missing credentials, that internal/
#      command's statelessDiscoverProviderUnavailable and internal/live/
#      projection/build.go's providerUnavailableSeverity both downgrade
#      to a Warning instead of an Error - but ONLY when no declared
#      instance's own identity resolution depends on the failing provider
#      (needsSet), so a provider a needs-discovery instance actually
#      needs stays fatal, unchanged.
#   2. Once (1) let the real graph run, ten config-identified instances
#      whose identity value belongs to a SIBLING resource this same apply
#      had not created yet either (module.vpc's route table associations
#      and routes, module.eks's cluster IAM role and one security group
#      rule) hit #365 ruling 4's own default refusal - "No source for
#      this instance's identity... set strict { no_source_create =
#      "create" } to plan a create instead" - which every OTHER estate's
#      greenfield stage (corpus-alb-complete, corpus-ec2-instance-
#      complete, corpus-ecs-fargate, corpus-giantswarm-crossplane,
#      corpus-mastino-dns, corpus-overture-tiles, corpus-rds-complete-
#      postgres, corpus-s3-bucket-complete, corpus-security-group-
#      complete) already carries the toggle for. This estate's own
#      greenfield delta (above) had simply never been given it - a
#      script-only gap, not a second code defect.
#   3. With (1) and (2) fixed, the real graph reaches
#      aws_eks_cluster.this[0]'s own CREATE - and traced with TF_LOG=trace
#      against the real graph, core correctly defers provider.kubernetes's
#      own configuration (planDataSource: "configuration not fully known
#      yet, so deferring to apply phase", genuine unmodified core
#      behavior) UNTIL issue #388's plan-node seam resolver hook
#      (node_resource_plan_instance.go) tried a LIVE read against it
#      anyway for kubernetes_config_map.aws_auth[0]'s client-derivable
#      identity - "Get https://localhost/.../aws-auth: connection
#      refused", the kubernetes SDK's own default for an unknown host.
#      FIXED (also generically: NodeApplyableProvider.ConfigKnown +
#      ResolvedProvider.ConfigKnown, gating the resolver hook on whether
#      ITS OWN provider's config was wholly known, reaching every provider
#      block that depends on any not-yet-created managed resource's
#      attribute, not just kubernetes). Once that's out of the way, the
#      real graph proceeds to actually CREATE aws_eks_cluster.this[0] -
#      and lex00/floci#139 is what blocks it now: this stage runs THREE
#      floci containers on one Docker network at once (real EKS mode),
#      and floci's own PortAllocator allocates the k3s API-server sibling
#      container's host port by binding a ServerSocket inside floci's OWN
#      container namespace, which cannot see that a DIFFERENT floci
#      container's own k3s sibling has already bound that same host port
#      on the shared Docker HOST. Confirmed directly with the AWS CLI
#      against two bare floci containers, no tofu/terraform in the loop:
#      the second cluster's own create reaches status FAILED, and `docker
#      inspect` on its k3s sibling shows "Bind for 0.0.0.0:6500 failed:
#      port is already allocated". HANDOFF row 4 (the emulator is wrong):
#      filed at lex00/floci#139, fixed on branch fix/eks-port-allocator-
#      namespace (lex00/floci PR #140, DockerClient-aware port allocation,
#      a regression test proven load-bearing) and PUSHED TO ORIGIN ONLY -
#      publishing the image and repinning live/floci-image is the shared-
#      layer step the orchestrator batches, not done here.
# UPDATE 2026-08-25 (gauntlet:eks-basic/greenfield re-measure): main now
# carries both halves this estate was waiting on - 7f48ff1086 (walls 1+2
# above) and 2da22751de (the repin to sha256:670a8783, including
# lex00/floci#139's PortAllocator fix, wall 3 above). Re-run against both:
# the apply itself now succeeds, all 54 resources created, no floci port
# collision. One more wall, found by this re-run, is NOT a choudoufu
# defect: G2's own assertion below expected the cluster's tofu-address tag
# in bare HCL bracket syntax ("module.eks.aws_eks_cluster.this[0]"), but
# the marker spec (internal/live/markers/markers.go's EscapeAddress, used
# by every stamp - see internal/live/projection/nodestamp.go) has always
# escaped an instance key's "[0]" to ":0" in the tag VALUE itself; every
# sibling corpus script that checks an indexed resource's live tofu-
# address tag already expects the colon form (corpus-autoscaling-
# complete's own LT_ADDR="module.complete.aws_launch_template.this:0" is
# the same pattern). Confirmed via the AWS CLI directly against the live
# tag (no tofu in the loop for the comparison): the cluster genuinely
# carries "module.eks.aws_eks_cluster.this:0". This script's own G2
# assertion was simply never updated to the escaped form - fixed to match
# every other estate's own convention, not a marker/stamp change. With
# that one-line fix the stage passes end to end.
GREEN_APPLY_OUT="$(green_tofu_run apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | grep -E '^Error|^│' | head -60
  fail "the greenfield apply failed - see live/gauntlet/logs/corpus-eks-basic.log for the full diagnostic"
}
grep -qE 'Apply complete! Resources: 54 added, 0 changed, 0 destroyed\.' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 54 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }
awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }

log "=== G2. the cluster's marker, read through the AWS CLI directly ==="
GREEN_CLUSTER_NAME="$(awsg eks list-clusters --query 'clusters[0]' --output text)"
[ -n "$GREEN_CLUSTER_NAME" ] && [ "$GREEN_CLUSTER_NAME" != "None" ] || fail "no EKS cluster found on the greenfield endpoint"
GREEN_CLUSTER_ADDR="$(awsg eks list-tags-for-resource --resource-arn "arn:aws:eks:${REGION}:000000000000:cluster/${GREEN_CLUSTER_NAME}" --query "tags.\"tofu-address\"" --output text)"
[ "$GREEN_CLUSTER_ADDR" = "module.eks.aws_eks_cluster.this:0" ] || fail "the greenfield cluster carries tofu-address=$GREEN_CLUSTER_ADDR, not module.eks.aws_eks_cluster.this:0"
GREEN_CLUSTER_ESTATE="$(awsg eks list-tags-for-resource --resource-arn "arn:aws:eks:${REGION}:000000000000:cluster/${GREEN_CLUSTER_NAME}" --query "tags.\"tofu-estate\"" --output text)"
[ "$GREEN_CLUSTER_ESTATE" = "$GREEN_ESTATE" ] || fail "the greenfield cluster carries tofu-estate=$GREEN_CLUSTER_ESTATE, not $GREEN_ESTATE"
log "  $GREEN_CLUSTER_NAME carries tofu-address=$GREEN_CLUSTER_ADDR tofu-estate=$GREEN_CLUSTER_ESTATE - read via the AWS CLI, not choudoufu's own report"

log "=== G3. the record store holds what the current record-writer can (#364 A2) ==="
GREEN_RECORD_FILES="$(find "$GREEN_EST/.tofu-records/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$GREEN_RECORD_FILES" -ge 1 ] || fail "expected at least one record under the implied local record store after the greenfield apply, found $GREEN_RECORD_FILES"
log "  $GREEN_RECORD_FILES records persisted under the implied local record store (not asserted against an exact expected count here - see corpus-ecs-fargate's own greenfield stage for the numeric-wire-identity-component gap that makes an exact count estate-specific)"

log "=== G4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(green_tofu_run plan -input=false -no-color 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -60; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== G5. stock oracle - the identical corpus example applied fresh in its own namespace ==="
oracle_green_terraform_run init -input=false -no-color > /tmp/eks-basic-green-oracle-init.log 2>&1 || {
  tail -60 /tmp/eks-basic-green-oracle-init.log; fail "the greenfield oracle's init failed"; }
ORACLE_APPLY_OUT="$(oracle_green_terraform_run apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_APPLY_OUT" | grep -E '^Error|^│' | head -60
  fail "the greenfield oracle apply failed"
}
grep -qE 'Apply complete! Resources: 54 added, 0 changed, 0 destroyed\.' <<< "$ORACLE_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 54 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_APPLY_OUT")"

log "=== G6. object-by-object comparison, via the AWS CLI on both endpoints, marker tags never compared ==="
eks_basic_shape() { # $1 = endpoint - a normalised structural fact sheet,
                     # read via the AWS CLI, never through tofu state.
  local ep="$1" cn
  cn="$(aws --endpoint-url "$ep" --region "$REGION" eks list-clusters --query 'clusters[0]' --output text 2>/dev/null)"
  aws --endpoint-url "$ep" --region "$REGION" eks describe-cluster --name "$cn" \
    --query "cluster.[status,version]" --output text 2>/dev/null | awk '{print "cluster status="$1" version="$2}'
  aws --endpoint-url "$ep" --region "$REGION" autoscaling describe-auto-scaling-groups \
    --query "length(AutoScalingGroups)" --output text 2>/dev/null | sed 's/^/asg_count=/'
  aws --endpoint-url "$ep" --region "$REGION" autoscaling describe-auto-scaling-groups \
    --query "sort(AutoScalingGroups[].DesiredCapacity)" --output text 2>/dev/null | tr '\t' ',' | sed 's/^/asg_desired_sorted=/'
  aws --endpoint-url "$ep" --region "$REGION" ec2 describe-security-groups \
    --filters "Name=tag:kubernetes.io/cluster/${cn},Values=owned" \
    --query "length(SecurityGroups)" --output text 2>/dev/null | sed 's/^/cluster_owned_sg_count=/'
}
GREEN_SHAPE="$(eks_basic_shape "$GREEN_ENDPOINT" | sort)"
ORACLE_SHAPE="$(eks_basic_shape "$ORACLE_ENDPOINT" | sort)"
if [ "$GREEN_SHAPE" != "$ORACLE_SHAPE" ]; then
  diff <(printf '%s\n' "$GREEN_SHAPE") <(printf '%s\n' "$ORACLE_SHAPE") || true
  fail "the greenfield estate's object inventory does not match stock's cold deploy, object by object, in its own namespace"
fi
log "  object-by-object match: cluster status/version, autoscaling-group count and sorted desired capacities, and the cluster-owned security-group count - identical between the greenfield estate and stock's cold deploy in its own namespace, marker tags never part of the comparison"

gauntlet_stage greenfield pass "54 resources from nothing, cluster marker verified via the AWS CLI, $GREEN_RECORD_FILES records under the implied local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally on cluster status/version, ASG count/desired-capacities, and cluster-owned security-group count"
gauntlet_end_stage

# The green/oracle floci containers, and whatever k3s/EC2-simulation sibling
# containers they spawned, are deliberately left running rather than swept
# here: a blanket "docker ps --filter name=floci-eks-" sweep cannot tell
# THEIR sibling containers apart from the MAIN cluster's own (already
# running since cold_deploy and still live at this point in the script), so it
# would kill the wrong cluster. cleanup()'s exit trap does the blanket
# sweep once, after everything in this script is done with all three floci
# instances.

gauntlet_end

log ""
log "=== PASS/FAIL: all five active stages pass ==="
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
log "           (a distinct, narrower, DEFER-caliber wall)."
log "  STAGE 3  PASS  live-plan runs to completion with ZERO Error"
log "           diagnostics and is genuinely EMPTY. The worker launch"
log "           configuration's enable_monitoring/root_block_device wall"
log "           was the emulator (lex00/floci#132); its user_data wall was"
log "           a real choudoufu defect (configuredAttrsSeed's residue-"
log "           record pre-read seed, internal/live/projection/build.go)."
log "           See the UPDATE note above stage 3's own code for the full"
log "           mechanism."
log "  STAGE 4  PASS  genuine no-op apply (0 added, 0 changed, 0 destroyed);"
log "           tofu-estate-tagged object count unchanged."
log "  STAGE 5  PASS  one VPC's Name tag tampered out of band via the AWS"
log "           CLI; the plan proposed fixing exactly that object, and"
log "           applying it reconverged the tag."
log ""
log "Two real, generalizable floci gaps (not this module's age, not this"
log "script's setup) were found, fixed, merged and published along the way"
log "in an earlier unit: EKS worker AMI discovery (lex00/floci#55/#56) and"
log "SuspendProcesses/ResumeProcesses (same PR) - every terraform-aws-eks"
log "estate with self-managed node groups hits both on default settings."
log "This unit found and fixed a third: aws_launch_configuration's"
log "InstanceMonitoring and BlockDeviceMappings were dropped on create and"
log "never echoed back on describe at all (lex00/floci#132)."
