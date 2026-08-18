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
# ISSUE #287 ITEM 8, THE SPURIOUS default_tags DIFF THIS SCRIPT USED TO
# PIN, IS NOW FIXED TOO.
#
# Discovered only once #301 stopped masking it: with all four deltas
# applied, apply succeeded (4 added, then 0 added / 2 changed once the
# record store settled force_detach_policies), but a replan was NOT empty -
# it proposed adding the four `var.tags` keys (managed-by, group,
# subproject, githubRepo) back on both the role and the OIDC provider,
# forever, on every single replan. The choudoufu-written marker tags
# (tofu-estate, tofu-address, tofu-slot) round-tripped perfectly in the
# same diff, which is what first pinned this as a tags gap rather than a
# marker-writing defect.
#
# It was originally blamed on floci, on the theory that
# CreateRole/CreateOpenIDConnectProvider never merge the provider's
# `default_tags` block in at create time. That diagnosis was wrong,
# verified directly against floci's own storage rather than inferred from
# the plan output: a floci build instrumented to log every CreateRole,
# CreateOpenIDConnectProvider, GetRole and GetOpenIDConnectProvider call
# showed the create requests arriving from the provider with all six tags
# already merged (default_tags merging is the AWS PROVIDER's own
# client-side job, done before the request ever reaches floci), floci
# storing all six, and every GetRole/GetOpenIDConnectProvider call
# throughout the run - including the calls backing the plan that showed
# the spurious diff - reporting all six back correctly.
#
# The real defect was in choudoufu's OWN plan pipeline:
# internal/live/projection/build.go's materialize/importAndRead
# reconstructs a stamped resource's prior state on EVERY plan by calling
# the provider's ImportResourceState then ReadResource (there is no
# persisted state to refresh from), and ImportResourceState answers a bare
# identity with no configuration in hand. terraform-provider-aws's
# transparent-tagging Read implementation uses PriorState.tags as its only
# signal for "which raw tags were explicitly declared" versus "arrived
# through the provider's own default_tags" - with an empty PriorState.tags
# (what ImportResourceState hands back) it falls back to comparing raw tag
# VALUES against its default_tags config, which misclassifies any tag the
# configuration ALSO declares explicitly if it happens to duplicate a
# default_tags entry, exactly this estate's shape (`tags = var.tags`
# forwarded into both the module call and, through the same variable, the
# provider block's own `default_tags`). A plain OpenTofu apply followed by
# a plain refresh never hits this, because the state written at CREATE
# already carries the resource's own declared tags - proven directly: a
# hand-built plain-terraform repro with the identical shape did not
# reproduce the drift against the same floci build. choudoufu re-derives
# prior state through exactly the ImportResourceState/ReadResource path on
# every single plan, so it hit the ambiguity forever.
#
# The fix (`configuredTagsSeed` in internal/live/projection/build.go)
# statically evaluates a taggable resource's own, AS-WRITTEN "tags"
# argument - from the SAME unstamped configuration [projection.Build]
# already resolves, before stamp.Stamp's later, in-memory-only marker
# injection ever touches it (see that package's own "configuration
# synthesis, before the plan runs" doc comment: no file is rewritten, and a
# projection is always built first) - and seeds it into the
# ImportResourceState stub's "tags" attribute before ReadResource sees it.
# That gives ReadResource the same signal a genuinely persisted state
# would have carried. It reads from the schema (Taggable, from
# internal/live/markers, plus a paired "tags_all" attribute - the AWS
# provider's transparent-tagging convention present on nearly every
# taggable AWS type) rather than from a type name list, so it reaches every
# type sharing that convention, not only the two this issue happened to
# name.
#
# This script does not edit `policy_arn` to route around the sibling
# reference, and does not disable `default_tags` to route around the
# tags-diff gap - either would no longer be running the estate's own
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
#   BREAK        set to 1 to corrupt the role's expected tofu-address tag
#                value before step 5, proving the marker assertion there is
#                load-bearing rather than a check that always passes.
#
# Exit codes: 0 when the run reaches exactly the state described above (four
# resources created, the each.value wall crossed, and a truly empty replan
# with both stamped resources' markers verified against IAM directly);
# non-zero if anything earlier fails, if the apply fails with the OLD
# each.value error (which would mean #301 has regressed), or if a replan
# proposes ANY resource change (which would mean the default_tags fix has
# regressed or something new has broken).

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
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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

# ── 5. the live markers, read directly off IAM ──────────────────────────────
# Read through the AWS CLI, never through choudoufu, before trusting the
# plan below to say the same thing.
log "=== 5. the live objects, and their markers ==="
OIDC_ARN="$(awsl iam list-open-id-connect-providers \
  --query 'OpenIDConnectProviderList[0].Arn' --output text)"
[ -n "$OIDC_ARN" ] && [ "$OIDC_ARN" != "None" ] \
  || fail "IAM holds no OIDC provider after an apply that reported creating one"
ROLE_NAME="gh-image-builder"
awsl iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1 \
  || fail "IAM holds no role named $ROLE_NAME after an apply that reported creating one"

WANT_OIDC_ADDR="module.iam_github_oidc_provider.aws_iam_openid_connect_provider.this:0"
WANT_ROLE_ADDR="module.iam_github_oidc_role.aws_iam_role.this:0"
if [ "${BREAK:-}" = "1" ]; then
  # Not a string nothing could produce: the same address shape, the same
  # module, the same resource - just the wrong instance key, which is
  # exactly what a marker written against the wrong count slot would carry.
  WANT_ROLE_ADDR="module.iam_github_oidc_role.aws_iam_role.this:1"
  log "  BREAK=1: expecting tofu-address=$WANT_ROLE_ADDR on the role, which"
  log "           differs from what apply actually wrote. This step must fail."
fi

OIDC_ADDR="$(awsl iam list-open-id-connect-provider-tags --open-id-connect-provider-arn "$OIDC_ARN" \
  --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$OIDC_ADDR" = "$WANT_OIDC_ADDR" ] \
  || fail "the OIDC provider carries tofu-address=$OIDC_ADDR, expected $WANT_OIDC_ADDR"
OIDC_EST="$(awsl iam list-open-id-connect-provider-tags --open-id-connect-provider-arn "$OIDC_ARN" \
  --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$OIDC_EST" = "$ESTATE" ] || fail "the OIDC provider carries tofu-estate=$OIDC_EST, expected $ESTATE"
ROLE_ADDR="$(awsl iam list-role-tags --role-name "$ROLE_NAME" \
  --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$ROLE_ADDR" = "$WANT_ROLE_ADDR" ] \
  || fail "the role carries tofu-address=$ROLE_ADDR, expected $WANT_ROLE_ADDR"
ROLE_EST="$(awsl iam list-role-tags --role-name "$ROLE_NAME" \
  --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$ROLE_EST" = "$ESTATE" ] || fail "the role carries tofu-estate=$ROLE_EST, expected $ESTATE"
log "  OIDC provider $OIDC_ARN  tofu-address=$OIDC_ADDR"
log "  role          $ROLE_NAME  tofu-address=$ROLE_ADDR"
# BREAK=1 corrupted WANT_ROLE_ADDR above, so a run that reaches this line
# with BREAK=1 set proves nothing - the ROLE_ADDR comparison above is the
# check, and it already exited the script the moment it disagreed.

# ── 6. replan: issue #287 item 8's tags-diff gap is fixed ──────────────────
#      the OIDC provider's and the role's tags no longer drift, ever ───────
log "=== 6. replan: a truly empty plan ==="
PLAN_OUT="$(plan_into 2>&1)"
[ ! -f "$EST/terraform.tfstate" ] || fail "the plan wrote a state file"

CHANGES="$(grep -cE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT")"
[ "$CHANGES" = "0" ] || {
  grep -E '^  # .+ will be' <<< "$PLAN_OUT"
  grep -E '^ +[+~-] "' <<< "$PLAN_OUT"
  fail "the plan proposes $CHANGES resource changes, expected exactly 0. If this is 2 again (the OIDC provider's and the role's tags), issue #287 item 8's fix (internal/live/projection/build.go's configuredTagsSeed) has regressed."
}
grep -qE 'No changes\.' <<< "$PLAN_OUT" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "expected OpenTofu's own \"No changes\" line and did not find it"; }
log "  No changes. Your infrastructure matches the configuration."

PLAN2_OUT="$(plan_into 2>&1)"
CHANGES2="$(grep -cE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT")"
[ "$CHANGES2" = "0" ] || { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second replan is not empty: $CHANGES2 changes"; }
log "  a second replan is empty too - stable, not a fluke of ordering"

log ""
log "=== PASS: #301 AND #287 ITEM 8 BOTH CROSSED ==="
log ""
log "The each.value language wall (issue #301) is fixed and this estate now"
log "applies on the first try, four resources, no discovery pass needed. The"
log "spurious tags diff choudoufu's own plan pipeline used to render for"
log "aws_iam_role/aws_iam_openid_connect_provider after a clean create"
log "(issue #287 item 8) is fixed too: internal/live/projection/build.go's"
log "materialize now seeds a taggable resource's OWN, as-declared \"tags\""
log "value into ImportResourceState's stub before ReadResource sees it,"
log "which is the signal a provider whose Read distinguishes explicitly"
log "declared tags from ones arriving through the provider's own"
log "default_tags was missing every single time choudoufu re-derived prior"
log "state with no persisted state to refresh from. Both stamped resources'"
log "markers were read directly off IAM and verified by string, not"
log "inferred from a clean-looking plan verdict."
