#!/usr/bin/env bash
set -uo pipefail

# A real government estate crossed against a real emulator: issue #274's
# step 6, for
# .corpus/govuk-infrastructure/terraform/deployments/chat-evaluation-ci.
#
# Four instances, hand-written by GDS for their own test account and not by
# anyone here:
#
#   aws_iam_openid_connect_provider.github_actions   server-assigned ARN
#   aws_iam_role.github_actions_bedrock_ci           client-named
#   aws_iam_policy.bedrock_invoke_policy             server-assigned ARN
#   aws_iam_role_policy_attachment.attach_bedrock_invoke  an edge, untaggable
#
# THE POINT OF THIS ONE is the same as corpus-oidc-provider's and the estate
# is not: this is somebody's own root module, not a module example. IAM mints
# the OIDC provider's ARN at create time, so the only way a run finds the one
# it already owns is to enumerate the account and read a marker off what
# comes back. When floci's Cloud Control listed AWS::IAM::OIDCProvider empty,
# the replan concluded the provider did not exist and proposed creating a
# second one - with every plan verdict clean, because creating a resource the
# run believes is absent is not an error. Step 7 asserts the count.
#
# What this estate contributes that the module example does not:
#
#   1. A PROVIDER-LEVEL default_tags block, with six tags in it. The markers
#      are written into each resource's own tags argument and have to survive
#      that merge. Step 4 reads them back off IAM and checks both the marker
#      AND one of GDS's own tags is still there.
#
#   2. Two resources whose names are derived, not literal: the policy is
#      "${var.role_name}-policy" and the role is var.role_name. Both are
#      identity-bearing and both must render from the variable's default.
#
#   3. A cloud block with a workspaces{} stanza, which is the #268 delta in
#      its Terraform-Cloud form rather than its backend "s3" form.
#
# NETWORK. The estate reads data "tls_certificate" against
# https://token.actions.githubusercontent.com, a real outbound fetch to
# GitHub and not to AWS. It is left alone rather than stubbed: it is GDS's
# own configuration, and stubbing it would be a delta this script would then
# have to justify. Offline, the run fails at that read and says so.
#
#   bash live/e2e/corpus-govuk-oidc/run.sh
#
# Needs Docker, the AWS CLI, outbound HTTPS to GitHub, and a populated
# .corpus (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4693, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before
#                step 5, proving the identity assertion is load-bearing.
#
# .corpus is shared across every worktree and is NEVER written to: the estate
# is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/govuk-infrastructure/terraform/deployments/chat-evaluation-ci"
WORK="$(mktemp -d)"
EST="$WORK/chat-evaluation-ci"
FLOCI_PORT="${FLOCI_PORT:-4693}"
FLOCI_NAME="choudoufu-corpus-govuk-oidc-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="govuk-chat-evaluation-ci"
REGION="eu-west-1"
ACCOUNT="000000000000"
INSTANCES=4
# Both from variables.tf's own defaults, which is why step 1 asserts they
# are still the defaults rather than passing a tfvars file.
ROLE_NAME="github_action_govuk_chat_evaluation_bedrock_ci"
POLICY_NAME="${ROLE_NAME}-policy"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

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
[ -f "$EST/main.tf" ] && [ -f "$EST/github_oidc_provider.tf" ] \
  || fail "the estate copy is missing main.tf or github_oidc_provider.tf"
# Every count below is derived from these two defaults. If the corpus pin
# moves them, the run has to say so here rather than as a failed lookup.
grep -qF "default     = \"$ROLE_NAME\"" "$EST/variables.tf" \
  || { grep -n 'default' "$EST/variables.tf"; fail "role_name's default is no longer $ROLE_NAME - the corpus pin has moved"; }
RES_N="$(grep -hc '^resource "' "$EST"/*.tf | awk '{s+=$1} END {print s}')"
[ "$RES_N" = "$INSTANCES" ] \
  || fail "the estate declares $RES_N resource blocks, expected $INSTANCES - the corpus pin has moved"
log "  estate copied out of .corpus into $EST ($RES_N resource blocks)"

# ── 1. the deltas ───────────────────────────────────────────────────────────
log "=== 1. onboarding deltas ==="

# DELTA 1, ordinary onboarding. The estate declares `cloud { organization =
# "govuk" workspaces { tags = [...] } }`; a module may declare remote state
# or a live block, never both, so the cloud block goes and the live block
# replaces it. This is #268 in its Terraform-Cloud form.
perl -0pi -e 's/^  cloud \{\n    organization = "govuk"\n    workspaces \{\n      tags = \[[^\]]*\]\n    \}\n  \}\n/  live {\n    estate = "'"$ESTATE"'" # DELTA 1\n  }\n/m' "$EST/main.tf"
grep -q 'DELTA 1' "$EST/main.tf" || { sed -n '1,20p' "$EST/main.tf"; fail "DELTA 1 did not match the cloud block - the corpus pin has moved"; }
grep -q 'cloud {' "$EST/main.tf" && fail "DELTA 1 left a cloud block behind"
log "  DELTA 1  cloud block removed, live block added    (onboarding, #268)"

# DELTA 2, emulator wiring: the flags with no environment-variable form.
perl -0pi -e 's/^(provider "aws" \{\n  region = var\.aws_region\n)/$1\n  access_key                  = "test" # DELTA 2\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n/m' "$EST/main.tf"
grep -q 'DELTA 2' "$EST/main.tf" || { sed -n '/provider "aws"/,/^}/p' "$EST/main.tf"; fail "DELTA 2 did not match the provider block"; }
log "  DELTA 2  emulator flags on the provider          (emulator)"

# No provider pin, no tfvars. The estate's own `version = "~> 6.28"` resolves
# to a release with list resources intact, so #269's pin is not needed, and
# every one of its five variables carries a default. Both absences are
# asserted so a corpus pin moving back cannot silently reintroduce them.
grep -q 'version = "~> 6.28"' "$EST/main.tf" \
  || { grep -n 'version' "$EST/main.tf"; fail "the aws constraint is no longer ~> 6.28, so the 'no provider pin needed' claim is unchecked"; }
VARS_N="$(grep -c '^variable "' "$EST/variables.tf")"
DEFS_N="$(grep -c '^  default' "$EST/variables.tf")"
[ "$VARS_N" = "$DEFS_N" ] \
  || fail "$VARS_N variables but $DEFS_N defaults - the estate now needs a tfvars file and this script does not write one"
log "  no provider pin, and no tfvars: all $VARS_N variables carry defaults"

# The provider's default_tags block is left exactly as GDS wrote it. Step 4
# checks the markers survived the merge with it.
grep -q 'default_tags' "$EST/main.tf" \
  || fail "the estate no longer declares default_tags, so the marker-merge check in step 4 measures nothing"

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

# ── 3. stand the estate up ──────────────────────────────────────────────────
log "=== 3. init and apply: $INSTANCES instances ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# ── 4. what is live, and what carries a marker ──────────────────────────────
log "=== 4. the live objects, their markers, and GDS's own tags ==="
OIDC_ARN="$(awsl iam list-open-id-connect-providers --query 'OpenIDConnectProviderList[0].Arn' --output text)"
[ -n "$OIDC_ARN" ] && [ "$OIDC_ARN" != "None" ] \
  || fail "IAM holds no OIDC provider after an apply that reported creating one"
OIDC_N="$(awsl iam list-open-id-connect-providers --query 'length(OpenIDConnectProviderList)' --output text)"
[ "$OIDC_N" = "1" ] || fail "IAM holds $OIDC_N OIDC providers, expected 1"
awsl iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1 || fail "IAM holds no role named $ROLE_NAME"
POLICY_ARN="arn:aws:iam::${ACCOUNT}:policy/${POLICY_NAME}"
awsl iam get-policy --policy-arn "$POLICY_ARN" >/dev/null 2>&1 \
  || { awsl iam list-policies --scope Local --query 'Policies[].Arn' --output text; fail "IAM holds no policy at $POLICY_ARN"; }
ATTACHED="$(awsl iam list-attached-role-policies --role-name "$ROLE_NAME" \
  --query "AttachedPolicies[?PolicyArn=='$POLICY_ARN'] | length(@)" --output text)"
[ "$ATTACHED" = "1" ] || fail "$POLICY_ARN is not attached to $ROLE_NAME"
log "  OIDC provider $OIDC_ARN"
log "  role          $ROLE_NAME"
log "  policy        $POLICY_ARN"
log "  attachment    $ROLE_NAME -> $POLICY_NAME"

# Three of the four are taggable and must carry both markers. The fourth is
# an aws_iam_role_policy_attachment: an edge, with nowhere to hang a tag and
# no need for one, because its identity is its two endpoints.
tag_of() { # tag_of <list-cmd...> -- reads tofu-address off one object
  local key="$1"; shift
  "$@" --query "Tags[?Key=='$key'].Value | [0]" --output text 2>/dev/null || echo None
}
OIDC_ADDR="$(tag_of tofu-address awsl iam list-open-id-connect-provider-tags --open-id-connect-provider-arn "$OIDC_ARN")"
[ "$OIDC_ADDR" = "aws_iam_openid_connect_provider.github_actions" ] \
  || fail "the OIDC provider carries tofu-address=$OIDC_ADDR"
ROLE_ADDR="$(tag_of tofu-address awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$ROLE_ADDR" = "aws_iam_role.github_actions_bedrock_ci" ] \
  || fail "the role carries tofu-address=$ROLE_ADDR"
POL_ADDR="$(tag_of tofu-address awsl iam list-policy-tags --policy-arn "$POLICY_ARN")"
[ "$POL_ADDR" = "aws_iam_policy.bedrock_invoke_policy" ] \
  || fail "the policy carries tofu-address=$POL_ADDR"
for c in "awsl iam list-role-tags --role-name $ROLE_NAME" "awsl iam list-policy-tags --policy-arn $POLICY_ARN"; do
  E="$(eval "$c --query \"Tags[?Key=='tofu-estate'].Value | [0]\" --output text" 2>/dev/null)"
  [ "$E" = "$ESTATE" ] || fail "an object carries tofu-estate=$E, expected $ESTATE"
done
log "  3 of $INSTANCES objects carry tofu-address and tofu-estate; the fourth is"
log "  an aws_iam_role_policy_attachment, which is untaggable and re-derives"
log "  its identity from the declaration on every run"

# The markers did not displace GDS's own default_tags. A stamping pass that
# REPLACED the tags argument rather than merging into it would leave every
# assertion above green and quietly strip six tags off three live objects.
GDS_TAG="$(tag_of Service awsl iam list-role-tags --role-name "$ROLE_NAME")"
[ "$GDS_TAG" = "govuk-chat-evaluation-ci" ] \
  || { awsl iam list-role-tags --role-name "$ROLE_NAME" --output text; fail "the role's own Service default_tag is $GDS_TAG - the markers displaced it"; }
log "  and the provider's default_tags survived: Service=$GDS_TAG"

# ── 5. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 5. no state file, and the rendered identities read out of the run ==="
plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error|^│' | head -30; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
# Not a "Plan:" grep. This estate declares root outputs and live-plan holds
# no state to diff them against, so OpenTofu's renderer never prints the
# summary line - empty plan or not. See corpus-iam-policy/run.sh's header.
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"
       grep -E '^ +[+~-] [a-z_]+ +=' <<< "$PLAN_OUT" | head -20
       fail "the plan proposes a resource change. If it proposes CREATING an OIDC provider, that is the defect this script exists for: the live one was not enumerated."; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"; fail "the plan reports foreign resources"; }
log "  no resource change proposed; nothing foreign"

WANT=("$OIDC_ARN" "$ROLE_NAME" "$POLICY_ARN" "${ROLE_NAME}/${POLICY_ARN}")
if [ "${BREAK:-}" = "1" ]; then
  # Not a string nothing could produce: the same account and the same ARN
  # shape, with the role's name where the policy's should be - which is what
  # a derivation that lost the "-policy" suffix would render.
  WANT[2]="arn:aws:iam::${ACCOUNT}:policy/${ROLE_NAME}"
  log "  BREAK=1: expecting ${WANT[2]}, the SAME account and the SAME shape of"
  log "           ARN as the real one, just without the -policy suffix. The"
  log "           plan above stayed empty. This step must fail."
fi
for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$id\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | grep -c .)"
[ "$GOT_N" = "$INSTANCES" ] || fail "the run materialized $GOT_N distinct identities, expected $INSTANCES"
log "  all $INSTANCES rendered identities asserted as strings, and no fifth:"
for id in "${WANT[@]}"; do log "    $id"; done

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing either ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }
for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN2_OUT" || fail "the second run did not materialize \"$id\""
done
log "  the second cold plan is empty too, with the same $INSTANCES identities"

# ── 7. and applying it adds nothing ─────────────────────────────────────────
# The assertion the original defect would have failed. A run that cannot
# enumerate the OIDC provider proposes creating one, and IAM would then hold
# two, while every verdict above stayed clean.
log "=== 7. applying it adds nothing, and IAM still holds ONE OIDC provider ==="
APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the second apply was not a no-op"; }
AFTER_N="$(awsl iam list-open-id-connect-providers --query 'length(OpenIDConnectProviderList)' --output text)"
[ "$AFTER_N" = "1" ] \
  || fail "IAM now holds $AFTER_N OIDC providers, not 1 - the run created a second one over the one it already owned"
AFTER_ARN="$(awsl iam list-open-id-connect-providers --query 'OpenIDConnectProviderList[0].Arn' --output text)"
[ "$AFTER_ARN" = "$OIDC_ARN" ] || fail "the surviving OIDC provider is $AFTER_ARN, not $OIDC_ARN"
AFTER_POL="$(awsl iam list-policies --scope Local --query 'length(Policies)' --output text)"
[ "$AFTER_POL" = "1" ] || fail "IAM now holds $AFTER_POL customer-managed policies, not 1"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  $(grep -E 'Apply complete' <<< "$APPLY2_OUT" | head -1)"
log "  still 1 OIDC provider and 1 policy, and they are the same ones"

log ""
log "=== PASS ==="
log ""
log "A government department's own root module - four instances, a"
log "provider-level default_tags block, and a Terraform Cloud workspace -"
log "applied against an emulator, stripped of its state file, and replanned"
log "empty twice. All $INSTANCES rendered identities were checked as strings"
log "against IAM's own answer, GDS's own tags survived the marker write, and"
log "IAM still holds one OIDC provider rather than two."
log ""
log "It cost two deltas: the cloud block for a live block, and the emulator"
log "flags. No provider pin, no tfvars, no record_store."
log ""
log "Run again with BREAK=1: everything above step 5 still passes and step 5"
log "goes red."
