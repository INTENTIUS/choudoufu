#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate run against a real emulator: issue #274's step
# 6, for .corpus/k8s-io/infra/aws/terraform/cncf-k8s-infra-aws-capa-ami.
#
# Kubernetes's own SIG cluster-lifecycle IAM setup for CAPA's AMI-building
# pipeline: one literal resource plus two terraform-aws-modules calls.
# refusal-probe reports this entry `blocked: false, instances: 1` - it never
# resolves the two registry modules (`unresolved_modules: 2`), so its "1
# instance, clean" verdict says nothing about what is inside them. Once the
# modules resolve, the estate is FOUR resources:
#
#   aws_iam_policy.imagebuilder                                 client-named
#   module.iam_github_oidc_provider.aws_iam_openid_connect_provider.this[0]
#                                                               server-assigned
#   module.iam_github_oidc_role.aws_iam_role.this[0]            client-named
#   module.iam_github_oidc_role.aws_iam_role_policy_attachment.this["ImageBuilder"]
#                                                    untaggable, an edge
#
# ISSUE #301, THE LANGUAGE WALL THIS SCRIPT USED TO PIN, IS NOW FIXED.
#
# The wall was a bare `each.value` (no trailing `.attr`) forwarding a
# SIBLING managed resource's server-assigned attribute
# (`aws_iam_policy.imagebuilder.arn`) across a module-call argument boundary
# into a child that declares the receiving variable with a concrete type
# (`variable "policies" { type = map(string) }`). #251's declared-type
# conversion (internal/live/identity/typedvar.go) rebuilt the for_each
# source as a whole cty value to apply that conversion, and unconditionally
# DROPPED the pre-conversion expression the moment any concrete declared
# type applied - so the sibling reference had no expression left to resolve
# through by the time `policy_arn = each.value` asked for it, and identity
# resolution's own static evaluator raised "Dynamic value in static
# context" over a for_each whose KEY SET was, in fact, statically known the
# whole time.
#
# #301's fix (typedvar.go's `preservedExpr`) carries the pre-conversion
# expression across the hop in the one case where the declared type cannot
# change what it renders as: a map or object element type of exactly
# cty.String, or an unconstrained cty.DynamicPseudoType. Once the
# expression survives, [resolver.resolveExpr]'s ordinary symbolic path
# builds a PARENT_DERIVED formula for it through the SAME mechanism issue
# #284 already built for a direct reference (`name =
# aws_acm_certificate.cert.arn`) - no second resolution pass, no live
# discovery, needed to CROSS the wall. See
# TestBareEachValueThroughTypedModuleVarBuildsAFormula
# (internal/live/identity/typedvar_test.go) for the fast, non-Docker
# regression fixture, and #301's own tracker comment for the full trace.
#
# THE THREE DELTAS NEEDED TO REACH THE (NOW FIXED) WALL, kept exactly as
# they were - none of this is a #301 concern, all of it is ordinary
# onboarding or upstream drift a plain tofu/terraform run would hit too:
#
#   1. `backend "s3" {}` -> a live block (#268, ordinary).
#
#   2. The module source has NO version constraint, so it resolves to the
#      latest release (6.8.0 at the time this was written). That release
#      renamed both submodules' subdirectories
#      (iam-github-oidc-provider -> iam-oidc-provider,
#      iam-github-oidc-role -> iam-role, at
#      terraform-aws-modules/terraform-aws-iam@31b31d7, a `feat!` that also
#      raised the module's own minimum AWS provider to 6.0), so the
#      estate's `source = ".../modules/iam-github-oidc-provider"` 404s
#      inside the freshly downloaded module. DELTA 2 pins `version = "~>
#      5.0"` on both module calls (the last major series with the old
#      subdirectory names, tag v5.60.0).
#
#   3. The estate's own `version = "~> 5.66"` on the AWS provider resolves
#      to 5.100.0, #269's "release with no list resources at all" shape.
#      DELTA 3 pins `= 6.58.0`, the same fix other #274 scripts already use
#      for the identical constraint.
#
# A FOURTH DELTA, unrelated to #301: `force_detach_policies = true` is
# written by the iam-role module and IAM never returns it on a read, so
# every cold run without a record_store proposes an in-place update that
# never settles - issue #275's shape, and corpus-oidc-provider's own script
# documents the identical delta in more depth. DELTA 4 adds one
# `record_store "local"` block.
#
# THE BLOCKER THIS SCRIPT PINS NOW, discovered only once #301 stopped
# masking it: with all four deltas applied, apply succeeds (4 added, then 0
# added / 2 changed once the record store settles force_detach_policies),
# but a replan is NOT empty - it proposes adding the four `var.tags` keys
# (managed-by, group, subproject, githubRepo) back on both the role and the
# OIDC provider. The choudoufu-written marker tags (tofu-estate,
# tofu-address, tofu-slot) round-trip perfectly in the same diff, which is
# what pins this as a tags gap and not a marker-writing defect.
#
# ISSUE #287 ITEM 8 ORIGINALLY BLAMED THIS ON floci, ON THE THEORY THAT
# CreateRole/CreateOpenIDConnectProvider never merge the provider's
# `default_tags` block in at create time. THAT DIAGNOSIS IS WRONG, verified
# directly against floci's own storage rather than inferred from the plan
# output: a floci build instrumented to log every CreateRole,
# CreateOpenIDConnectProvider, GetRole and GetOpenIDConnectProvider call
# showed the create requests arriving from the provider with all six tags
# already merged (four var.tags keys plus the two markers - default_tags
# merging is the AWS PROVIDER's own client-side job, done before the
# request ever reaches floci), floci storing all six, and every single
# GetRole/GetOpenIDConnectProvider call throughout the entire run -
# including the exact calls backing the plan that shows the spurious diff -
# reporting all six back correctly. No TagRole/UntagRole/
# TagOpenIDConnectProvider/UntagOpenIDConnectProvider call ever fires, so
# nothing after create touches the stored tags either. The gap also isn't
# the second apply or the record store: a plan run immediately after the
# FIRST apply (before the record store ever settles force_detach_policies)
# already shows the same 2-change diff. A hand-built plain-terraform
# repro with the identical shape (default_tags = the same map as an
# explicit `tags` argument threaded through one level of module, the
# module's resource written as `tags = merge(var.tags, {markers})` -
# exactly what internal/live/stamp's rewrite produces for a bare `tags =
# var.tags` argument it cannot append to directly) does NOT reproduce the
# drift against the same floci build, so this isn't a plain
# terraform-provider-aws default_tags quirk either. The defect is
# somewhere in choudoufu's OWN plan pipeline - most likely in how the
# "current" value it renders for a stamped resource's `tags` attribute is
# derived - not in floci and not in the provider. See issue #287's item 8
# thread for the full trace; the floci-side fix this item asked for would
# have been dead code against already-correct behavior, the same shape as
# item 4's "does not reproduce" finding on that same issue.
#
# This script does not edit `policy_arn` to route around the sibling
# reference, and does not disable `default_tags` to route around the new
# blocker - either would no longer be running the estate's own
# configuration, the same discipline corpus-crossref-orcid-agent's script
# already documents for its own (floci-side) blocker.
#
#   bash live/e2e/corpus-cncf-k8s-infra-aws-capa-ami/run.sh
#
# Needs Docker and the AWS CLI. .corpus is read, never written: the estate
# is copied out to a temp directory first, same as every other corpus
# crossing.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4709, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 when the run reaches exactly the state described above (four
# resources created, the each.value wall crossed, and the pinned
# default_tags drift and nothing else remaining on a replan); non-zero if
# anything earlier fails, if the apply fails with the OLD each.value error
# (which would mean #301 has regressed), or if a replan proposes anything
# beyond the four pinned default_tags keys (which would mean either the
# default_tags gap has been fixed - rewrite this into a real crossing - or
# something new has broken).

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/k8s-io/infra/aws/terraform/cncf-k8s-infra-aws-capa-ami"
WORK="$(mktemp -d)"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4709}"
FLOCI_NAME="choudoufu-corpus-cncf-k8s-infra-aws-capa-ami-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="cncf-k8s-infra-aws-capa-ami"
REGION="us-east-2"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

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
[ -f "$EST/iam.tf" ] && [ -f "$EST/oidc.tf" ] && [ -f "$EST/providers.tf" ] && [ -f "$EST/variables.tf" ] \
  || fail "the estate copy is missing one of iam.tf, oidc.tf, providers.tf, variables.tf"
RES_N="$(grep -hc '^resource "' "$EST"/*.tf | awk '{s+=$1} END {print s}')"
[ "$RES_N" = "1" ] || fail "the estate declares $RES_N resource blocks, expected 1 (the policy - the other three come from the two modules) - the corpus pin has moved"
log "  estate copied out of .corpus into $EST ($RES_N literal resource block + 2 module calls)"

# ── 1. the deltas ───────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="

# DELTA 1, ordinary onboarding: `backend "s3" {}` -> a live block.
# DELTA 4, unrelated to #301: a record_store, so force_detach_policies (IAM
# never returns it) has somewhere to live - see corpus-oidc-provider's own
# script for the fuller trace of this same delta.
grep -q 'backend "s3"' "$EST/providers.tf" || fail "no backend \"s3\" block found - the corpus pin has moved"
grep -q 'version = "~> 5.66"' "$EST/providers.tf" \
  || { grep -n 'version' "$EST/providers.tf"; fail "the aws constraint is no longer ~> 5.66 - the corpus pin has moved"; }
cat > "$EST/providers.tf" <<EOF
terraform {
  required_version = "~> 1.8.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      # DELTA 3: was "~> 5.66", which resolves to 5.100.0 - #269's
      # "release with no list resources at all" shape.
      version = "= 6.58.0"
    }
  }

  # DELTA 1: was \`backend "s3" { ... }\` (#268).
  live {
    estate = "$ESTATE"

    # DELTA 4: force_detach_policies needs somewhere to live (#275).
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "aws" {
  region = var.region

  access_key                  = "test" # DELTA 5, emulator wiring
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true

  default_tags {
    tags = var.tags
  }
}
EOF
grep -q "estate = \"$ESTATE\"" "$EST/providers.tf" || fail "DELTA 1 did not land"
grep -q 'record_store "local"' "$EST/providers.tf" || fail "DELTA 4 did not land"
log "  DELTA 1  backend block removed, live block added             (#268)"
log "  DELTA 3  aws provider pinned = 6.58.0                        (#269-shape)"
log "  DELTA 4  record_store \"local\" added                          (#275)"
log "  DELTA 5  emulator flags on the provider                      (emulator)"

# DELTA 2, module version pins: the estate's own module calls carry no
# version constraint, so `init` resolves the latest release, which renamed
# both submodules' subdirectories out from under this estate's `source`
# paths (terraform-aws-modules/terraform-aws-iam@31b31d7, a feat! that also
# raised the module's minimum AWS provider to 6.0). This is upstream module
# drift a plain tofu/terraform run today would hit identically.
grep -q 'source    = "terraform-aws-modules/iam/aws//modules/iam-github-oidc-provider"' "$EST/oidc.tf" \
  || fail "the oidc-provider module source line has moved - the corpus pin has moved"
grep -q 'source    = "terraform-aws-modules/iam/aws//modules/iam-github-oidc-role"' "$EST/oidc.tf" \
  || fail "the oidc-role module source line has moved - the corpus pin has moved"
perl -0pi -e 's/(source    = "terraform-aws-modules\/iam\/aws\/\/modules\/iam-github-oidc-provider"\n)/$1  version   = "~> 5.0" # DELTA 2\n/' "$EST/oidc.tf"
perl -0pi -e 's/(source    = "terraform-aws-modules\/iam\/aws\/\/modules\/iam-github-oidc-role"\n)/$1  version   = "~> 5.0" # DELTA 2\n/' "$EST/oidc.tf"
[ "$(grep -c 'DELTA 2' "$EST/oidc.tf")" = "2" ] || fail "DELTA 2 did not land on both module calls"
log "  DELTA 2  both module calls pinned version = ~> 5.0            (upstream module drift)"

# ── 2. floci ────────────────────────────────────────────────────────────────
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"iam"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"iam"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (iam) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && "$TOFU" plan -input=false -no-color )
}

# ── 3. init, and the apply: #301 crossed ────────────────────────────────────
log "=== 3. init and apply: the each.value wall is crossed ==="
INIT_OUT="$(cd "$EST" && "$TOFU" init -input=false -no-color 2>&1)"
INIT_RC=$?
[ "$INIT_RC" -eq 0 ] || { printf '%s\n' "$INIT_OUT" | tail -30; fail "init failed"; }
grep -q 'Downloading registry.opentofu.org/terraform-aws-modules/iam/aws 5\.' <<< "$INIT_OUT" \
  || { printf '%s\n' "$INIT_OUT"; fail "init did not resolve the module to a 5.x release - DELTA 2 may not have landed"; }
log "  init OK, modules resolved to a 5.x release (old subdirectory names intact)"

APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
RC=$?

if grep -qF 'Error: Dynamic value in static context' <<< "$APPLY_OUT"; then
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the apply refused with the OLD #301 error. The each.value fix (internal/live/identity/typedvar.go's preservedExpr) has regressed."
fi
[ "$RC" -eq 0 ] || { printf '%s\n' "$APPLY_OUT" | tail -40; fail "the apply failed, and not with the old #301 error - something else has moved. Read the errors above."; }
grep -qE 'Apply complete! Resources: 4 added, 0 changed, 0 destroyed' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 4 resources"; }
log "  apply succeeded: $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"
log "  the bare each.value -> sibling ARN shape resolved to a PARENT_DERIVED"
log "  formula and the estate applied clean on the first try - #301 crossed."

# ── 4. force_detach_policies settles through the record store ─────────────
log "=== 4. a second apply settles force_detach_policies through the record store ==="
APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
[ $? -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the second apply (settling force_detach_policies through the record store) failed"; }
grep -qE 'Apply complete! Resources: 0 added, [0-9]+ changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply proposed adding or destroying something - expected only in-place changes"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY2_OUT" | head -1)"

# ── 5. the pinned blocker: choudoufu's own plan renders a spurious tags ────
#      diff on these two IAM resource types after a clean create ──────────
log "=== 5. replan: the pinned tags-diff gap, and nothing else ==="
PLAN_OUT="$(plan_into 2>&1)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the plan wrote a state file"

CHANGES="$(grep -cE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT")"
[ "$CHANGES" = "2" ] || {
  grep -E '^  # .+ will be' <<< "$PLAN_OUT"
  fail "the plan proposes $CHANGES resource changes, expected exactly 2 (the OIDC provider's and the role's tags). If this is 0, the tags-diff gap has been fixed - rewrite this script into a real crossing (state deletion, live-plan empty twice, BREAK=1 negative control) per every other script in live/e2e, and close out this pin."
}
grep -qE '^  # module\.iam_github_oidc_provider\.aws_iam_openid_connect_provider\.this\[0\] will be updated in-place' <<< "$PLAN_OUT" \
  || fail "expected the OIDC provider's in-place update - the corpus pin may have moved"
grep -qE '^  # module\.iam_github_oidc_role\.aws_iam_role\.this\[0\] will be updated in-place' <<< "$PLAN_OUT" \
  || fail "expected the role's in-place update - the corpus pin may have moved"
grep -qE '^ +~ force_detach_policies' <<< "$PLAN_OUT" \
  && fail "force_detach_policies is back in the diff - the record store did not settle it (DELTA 4 may not have taken effect)"

# The four default_tags keys, proposed as additions on both resources - and
# nothing else. tofu-estate/tofu-address/tofu-slot must NOT appear as a
# proposed change: if they do, the marker itself is drifting, which is a
# choudoufu defect and a very different, much worse problem than a missing
# provider tag.
for key in managed-by group subproject githubRepo; do
  grep -qE "^ +\+ \"${key}\"" <<< "$PLAN_OUT" \
    || { grep -E '^ +[+~-] "' <<< "$PLAN_OUT"; fail "expected \"$key\" to be proposed as an added tag - the pinned drift has changed shape"; }
done
for marker in tofu-estate tofu-address tofu-slot; do
  grep -qE "^ +[+~-] \"${marker}\"" <<< "$PLAN_OUT" \
    && { grep -E '^ +[+~-] "' <<< "$PLAN_OUT"; fail "$marker appears as a proposed CHANGE - the ownership marker itself is drifting, which is a choudoufu defect, not the pinned floci gap"; }
done
log "  exactly 2 in-place updates, both adding back the same 4 default_tags"
log "  keys (managed-by, group, subproject, githubRepo); the tofu-estate/"
log "  tofu-address/tofu-slot markers are stable in the same diff."

PLAN2_OUT="$(plan_into 2>&1)"
CHANGES2="$(grep -cE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT")"
[ "$CHANGES2" = "2" ] || { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the replan is not stable: $CHANGES2 changes the second time, $CHANGES the first"; }
log "  a second replan proposes the identical 2 changes - stable, not growing"

log ""
log "=== #301 CROSSED; BLOCKED ON A SEPARATE, UNRELATED TAGS-DIFF GAP ==="
log ""
log "The each.value language wall (issue #301) is fixed and this estate now"
log "applies on the first try, four resources, no discovery pass needed. What"
log "remains is a spurious tags diff choudoufu's own plan renders for"
log "aws_iam_role/aws_iam_openid_connect_provider after a clean create -"
log "verified NOT to be floci (every CreateRole/CreateOpenIDConnectProvider/"
log "GetRole/GetOpenIDConnectProvider call throughout the run reports the"
log "full, correctly-merged tag set) and NOT a plain terraform-provider-aws"
log "default_tags quirk either (a hand-built plain-terraform repro with the"
log "identical shape does not reproduce it). A different, narrower gap in"
log "choudoufu's own plan pipeline that deserves its own issue rather than a"
log "fix folded into this one. The ownership markers themselves are stable"
log "throughout, which is what distinguishes this from a marker defect."
