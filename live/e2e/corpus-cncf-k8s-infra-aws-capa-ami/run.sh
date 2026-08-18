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
# IT DOES NOT CROSS. It is BLOCKED BY CHOUDOUFU, not by floci - a genuine
# language-parity defect, verified rather than assumed, and pinned here
# rather than hidden, per this campaign's own discipline against
# fabricating a clean convergence. Filed as issue #301.
#
# THE THREE DELTAS NEEDED JUST TO REACH THE BLOCKER:
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
#      inside the freshly downloaded module. This is upstream module drift
#      a plain `tofu`/`terraform` run today would hit identically - not a
#      choudoufu defect - so DELTA 2 pins `version = "~> 5.0"` on both
#      module calls (the last major series with the old subdirectory
#      names, tag v5.60.0).
#
#   3. The estate's own `version = "~> 5.66"` on the AWS provider resolves
#      to 5.100.0, #269's "release with no list resources at all" shape.
#      DELTA 3 pins `= 6.58.0`, the same fix other #274 scripts already use
#      for the identical constraint.
#
# THE BLOCKER, found at the very first `apply` (nothing live yet, no
# live-plan involved):
#
#   Error: Dynamic value in static context
#
#     on .terraform/modules/iam_github_oidc_role/modules/iam-github-oidc-role/main.tf line 83:
#     83:   policy_arn = each.value
#
#   Unable to use each.value in static context, which is required by
#   module.iam_github_oidc_role:module.iam_github_oidc_role.aws_iam_role_policy_attachment.this["ImageBuilder"].policy_arn
#
# `policies = { ImageBuilder = aws_iam_policy.imagebuilder.arn }` is passed
# into the child module as a variable; the module's own resource does
# `for_each = var.policies` / `policy_arn = each.value`. The for_each KEY
# SET is statically known ("ImageBuilder", a literal map key) - this is a
# `keyOnly` expansion, not a `Non-static for_each expression` refusal. What
# fails is the bare `each.value` itself: a whole-value reference (no
# trailing `.attr`) to a SIBLING managed resource's server-assigned ARN,
# reached across a module-call argument boundary.
#
# VERIFIED AS A GENUINE DEFECT, not an acceptable limitation: the identical
# config, same choudoufu binary, with ONLY the live block removed, applies
# cleanly - `Plan: 4 to add, 0 to change, 0 to destroy` / `Apply complete!
# Resources: 4 added, 0 changed, 0 destroyed.` Stock OpenTofu only requires
# a for_each's own KEY SET known at plan time; values may resolve during
# apply, and "attach the policy I just created to the role I just created"
# is one of the single most common Terraform/OpenTofu patterns there is -
# terraform-aws-modules/iam uses exactly this shape for every "attach N
# policies to a role" call. Per #187's own ruling: "Parity is the bar...
# If OpenTofu accepts it and we refuse, that is a defect."
#
# This is NOT the #187/#284 fix (DiscoverySiblingApply/PlanInstances, which
# handles a for-comprehension for_each KEY SET derived from a managed
# resource's own planned attributes, with each.value.<attr> selected out of
# an object constructor) nor #252's Shape A/B (module-call repetition data,
# and each.value.<attr> selection respectively). All three are confirmed
# present on this tree. The gap is explicitly flagged in the source, not
# merely unnoticed: internal/live/identity/resolve.go's `expansion.keyOnly`
# doc comment says outright that resolving a bare each.value symbolically
# in this position "is a further extension this fix does not make." See
# issue #301 for the full trace.
#
# This script does not edit `policy_arn` to route around the sibling
# reference - that would no longer be running the estate's own
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
# Exit codes: 0 when the run reaches exactly the pinned refusal and nothing
# else has moved; non-zero if anything earlier fails, if the apply
# unexpectedly SUCCEEDS (which would mean the language wall has been
# extended to cover this shape, and this script should be rewritten into a
# real crossing - state deletion, live-plan empty twice, BREAK=1 negative
# control - per every other script in live/e2e), or if the apply fails for
# a different reason than the one pinned above.

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
  }
}

provider "aws" {
  region = var.region

  access_key                  = "test" # DELTA 4, emulator wiring
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
log "  DELTA 1  backend block removed, live block added             (#268)"
log "  DELTA 3  aws provider pinned = 6.58.0                        (#269-shape)"
log "  DELTA 4  emulator flags on the provider                      (emulator)"

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

# ── 3. init, and the apply: blocked by choudoufu, not by floci ─────────────
log "=== 3. init and apply: pinned to the each.value/static-context refusal ==="
INIT_OUT="$(cd "$EST" && "$TOFU" init -input=false -no-color 2>&1)"
INIT_RC=$?
[ "$INIT_RC" -eq 0 ] || { printf '%s\n' "$INIT_OUT" | tail -30; fail "init failed"; }
grep -q 'Downloading registry.opentofu.org/terraform-aws-modules/iam/aws 5\.' <<< "$INIT_OUT" \
  || { printf '%s\n' "$INIT_OUT"; fail "init did not resolve the module to a 5.x release - DELTA 2 may not have landed"; }
log "  init OK, modules resolved to a 5.x release (old subdirectory names intact)"

APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
RC=$?

if [ "$RC" -eq 0 ]; then
  fail "the apply SUCCEEDED, which this script does not expect. That is GOOD NEWS: the language wall around a bare each.value forwarding a sibling resource's attribute across a module-call boundary has been extended to cover this shape. Rewrite this script into a real crossing (delete the state file, live-plan empty twice, BREAK=1 negative control) per every other script in live/e2e, and close issue #301."
fi

grep -qF 'Error: Dynamic value in static context' <<< "$APPLY_OUT" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -20
  fail "the apply failed, but not with the pinned static-context error. Something else about the corpus pin or this fork has moved - read the errors above."
}
grep -qF 'module.iam_github_oidc_role:module.iam_github_oidc_role.aws_iam_role_policy_attachment.this["ImageBuilder"].policy_arn' <<< "$APPLY_OUT" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│|Unable to use' | head -20
  fail "the apply failed with a static-context error, but not at the pinned policy_arn site. The corpus pin may have moved - read the errors above."
}
log "  apply failed exactly as pinned:"
log "    Error: Dynamic value in static context"
log "    Unable to use each.value in static context, which is required by"
log "    module.iam_github_oidc_role:module.iam_github_oidc_role.aws_iam_role_policy_attachment.this[\"ImageBuilder\"].policy_arn"

# ── 4. the parity proof: the SAME config applies cleanly without live ──────
log "=== 4. parity check: the identical config, live block removed, applies clean ==="
PARITY="$WORK/parity"
mkdir -p "$PARITY"
cp "$SRC"/*.tf "$PARITY/"
cat > "$PARITY/providers.tf" <<EOF
terraform {
  required_version = "~> 1.8.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  region = var.region

  access_key                  = "test"
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
perl -0pi -e 's/(source    = "terraform-aws-modules\/iam\/aws\/\/modules\/iam-github-oidc-provider"\n)/$1  version   = "~> 5.0"\n/' "$PARITY/oidc.tf"
perl -0pi -e 's/(source    = "terraform-aws-modules\/iam\/aws\/\/modules\/iam-github-oidc-role"\n)/$1  version   = "~> 5.0"\n/' "$PARITY/oidc.tf"
( cd "$PARITY" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "the parity check's own init failed"
PARITY_OUT="$(cd "$PARITY" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"
PARITY_RC=$?
[ "$PARITY_RC" -eq 0 ] || { printf '%s\n' "$PARITY_OUT" | tail -40; fail "the parity apply (no live block) failed too - if this ever happens, issue #301's premise (this is a live-marker-only regression) is WRONG and needs re-checking before anything else in this script is trusted"; }
grep -qE 'Apply complete! Resources: 4 added' <<< "$PARITY_OUT" \
  || { grep -E 'Apply complete' <<< "$PARITY_OUT"; fail "the parity apply did not create exactly 4 resources"; }
log "  confirmed: the SAME binary, SAME config, live block removed:"
log "    $(grep -E 'Apply complete' <<< "$PARITY_OUT" | head -1)"
log "  so the refusal above is specific to the live marker path, not to this"
log "  estate or to stock OpenTofu semantics."

log ""
log "=== BLOCKED (choudoufu, not floci) ==="
log ""
log "Steps 0-2 above ran clean: three onboarding deltas (a live block, a"
log "module version pin for unrelated upstream drift, and a #269-shape"
log "provider version pin) get the estate to init. The first apply then"
log "refuses a pattern stock OpenTofu applies without complaint - a bare"
log "each.value forwarding a sibling resource's ARN through a module-call"
log "for_each - and step 4 proves that with the same binary, same config,"
log "live block removed. Filed as issue #301."
