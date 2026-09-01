#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-oidc-provider recipe; run with: just demo-run corpus-oidc-provider)
# Issue #274's step 6 on .corpus/iam/examples/iam-oidc-provider, whose
# central object is findable only by enumerating the account:
# aws_iam_openid_connect_provider has a server-assigned ARN, so a run that
# cannot list it concludes it does not exist and creates a SECOND one - with
# every plan verdict staying clean, because creating a resource the run
# believes is absent is not an error. Step 7 is that assertion: IAM still
# holds one OIDC provider after a second apply. Three instances cover three
# identity shapes at once - a server-assigned ARN, a name_prefix role whose
# name IAM assigns, and an untaggable attachment whose identity is its two
# endpoints. Step 5 shows force_detach_policies needing a record_store and
# proves it does not settle without one. BREAK=1 corrupts one expected
# identity by a single host label and step 5b must be the only step that
# goes red. Needs Docker, the AWS CLI, outbound HTTPS to GitHub for the
# module's own tls_certificate read, and a populated .corpus; runs on its
# own port (4692) so it can run beside `just demo`.
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# step 6, for .corpus/iam/examples/iam-oidc-provider.
#
# THE POINT OF THIS ONE. aws_iam_openid_connect_provider is ServerAssigned:
# IAM mints its ARN at create time and no argument reconstructs it, so the
# ONLY way a run can find the object it already owns is to enumerate the
# live account and read a marker off what comes back. When this estate was
# first crossed, floci's Cloud Control listed AWS::IAM::OIDCProvider EMPTY
# for an account that had one, so the replan found nothing, concluded the
# provider did not exist, and proposed creating a second one. That is the
# worst shape of failure this project has: a successful-looking plan that
# duplicates a live object.
#
# The fix is in the fork's store-backed Cloud Control lister, which gained a
# store-file-name tier. This script is the end-to-end check on it. Step 5
# asserts the OIDC provider's ARN appears as a rendered import identity, and
# step 7 asserts the emulator still holds exactly ONE OIDC provider after a
# second apply - which is the assertion the original defect would have
# failed while every plan verdict stayed clean.
#
# The estate is a terraform-aws-modules EXAMPLE - the configuration a new
# user copies first - and it instantiates three modules, one of which is
# create = false and contributes nothing. Three instances survive:
#
#   module.github_oidc_iam_provider.aws_iam_openid_connect_provider.this[0]
#   module.github_oidc_iam_role.aws_iam_role.this[0]
#   module.github_oidc_iam_role.aws_iam_role_policy_attachment.this["S3ReadOnly"]
#
# and they cover all three identity shapes at once: one ServerAssigned ARN
# recoverable only by tag-filtered list, one client-named role, and one
# untaggable ATTACHMENT whose identity is its two endpoints and needs no
# carrier at all.
#
# NETWORK. The iam-oidc-provider module reads data "tls_certificate" against
# https://token.actions.githubusercontent.com, which is a real outbound
# fetch to GitHub and not to AWS. It is left alone rather than stubbed: it
# is the estate's own configuration and stubbing it would be a delta this
# script would then have to justify. If the run is offline, init or plan
# fails at that read and says so.
#
#   bash live/e2e/corpus-oidc-provider/run.sh
#
# Needs Docker, the AWS CLI, outbound HTTPS to GitHub, and a populated
# .corpus (`just corpus-fetch`).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4692, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before
#                step 5, proving the identity assertion is load-bearing.
#
# .corpus is shared across every worktree and is NEVER written to: the
# example and the two modules it references are copied out first, preserving
# the relative paths their `source = "../../modules/..."` expects.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC_EXAMPLE="$CORPUS_DIR/iam/examples/iam-oidc-provider"
WORK="$(mktemp -d)"
EST="$WORK/iam/examples/iam-oidc-provider"
FLOCI_PORT="${FLOCI_PORT:-4692}"
FLOCI_NAME="choudoufu-corpus-oidc-provider-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="iam-oidc-provider-crossing"
REGION="eu-west-1"
INSTANCES=3
# local.name = "ex-${basename(path.cwd)}", which is why the copy below keeps
# the directory name. The iam-role module defaults use_name_prefix = true, so
# that string is a name_prefix and IAM appends its own suffix - the role's
# name is server-assigned too, and step 4 reads it back rather than
# predicting it.
ROLE_PREFIX="ex-iam-oidc-provider-"
POLICY_ARN="arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"

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
[ -d "$SRC_EXAMPLE" ] || fail "$SRC_EXAMPLE is missing - run 'just corpus-fetch' first"
for m in iam-oidc-provider iam-role; do
  [ -d "$CORPUS_DIR/iam/modules/$m" ] || fail "$CORPUS_DIR/iam/modules/$m is missing - run 'just corpus-fetch' first"
done

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

mkdir -p "$WORK/iam/examples" "$WORK/iam/modules"
cp -R "$SRC_EXAMPLE" "$EST"
for m in iam-oidc-provider iam-role; do
  cp -R "$CORPUS_DIR/iam/modules/$m" "$WORK/iam/modules/$m"
done
rm -rf "$EST/.terraform" "$EST/.terraform.lock.hcl"
[ -f "$EST/main.tf" ] && [ -f "$EST/versions.tf" ] || fail "the estate copy is missing main.tf or versions.tf"
[ "$(basename "$EST")" = "iam-oidc-provider" ] \
  || fail "the copy's directory name is not iam-oidc-provider, so basename(path.cwd) will not produce $ROLE_NAME"
log "  estate + 2 modules copied out of .corpus into $WORK"

# ── 1. the deltas ───────────────────────────────────────────────────────────
# Each asserts it landed. A corpus pin that moved out from under this script
# has to say so at the edit, not three steps later as an unexplained plan.
log "=== 1. onboarding deltas ==="

# DELTA 1, emulator wiring: the flags with no environment-variable form.
perl -0pi -e 's/(provider "aws" \{\n  region = "eu-west-1"\n)\}/$1\n  access_key                  = "test" # DELTA 1\n  secret_key                  = "test"\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n}/' "$EST/main.tf"
grep -q 'DELTA 1' "$EST/main.tf" || fail "DELTA 1 did not match the provider block in main.tf - the corpus pin has moved"
log "  DELTA 1  emulator flags on the provider          (emulator)"

# DELTA 2, the live block. There is no backend and no cloud block to remove -
# this example declares neither - so this is an addition and nothing else.
perl -0pi -e 's/^(terraform \{\n)/$1  live {\n    estate = "'"$ESTATE"'" # DELTA 2\n  }\n\n/m' "$EST/versions.tf"
grep -q 'DELTA 2' "$EST/versions.tf" || { cat "$EST/versions.tf"; fail "DELTA 2 did not match versions.tf"; }
log "  DELTA 2  live block added                        (onboarding, #268)"

# No provider pin and no backend edit. The example's own `version = ">= 6.28"`
# resolves to a release with list resources intact, so #269's pin is not
# needed, and it declares no backend or cloud block to remove. Both absences
# are asserted so that a corpus pin moving back does not silently
# reintroduce them.
grep -q 'version = ">= 6.28"' "$EST/versions.tf" \
  || { grep -n 'version' "$EST/versions.tf"; fail "the aws constraint is no longer >= 6.28, so the 'no provider pin needed' claim is unchecked"; }
grep -qE 'backend "|cloud \{' "$EST"/*.tf \
  && fail "the estate now declares a backend or cloud block, so the 'no backend edit needed' claim is wrong"
log "  no provider pin and no backend edit needed       (this estate costs neither)"
log "  (DELTA 3, a record_store, arrives in step 5 - with the evidence for it)"

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
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 )
  fail "init failed"; }
APPLY_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | grep -E '^Error|^│' | head -30
  fail "the first apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly $INSTANCES instances"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT" | head -1)"

# ── 4. what is live, and what carries a marker ──────────────────────────────
# Read through the AWS CLI, never through choudoufu.
log "=== 4. the live objects, and their markers ==="
OIDC_ARN="$(awsl iam list-open-id-connect-providers \
  --query 'OpenIDConnectProviderList[0].Arn' --output text)"
[ -n "$OIDC_ARN" ] && [ "$OIDC_ARN" != "None" ] \
  || fail "IAM holds no OIDC provider after an apply that reported creating one"
OIDC_N="$(awsl iam list-open-id-connect-providers --query 'length(OpenIDConnectProviderList)' --output text)"
[ "$OIDC_N" = "1" ] || fail "IAM holds $OIDC_N OIDC providers, expected 1"

# The role's name is server-assigned - the module hands "ex-iam-oidc-provider-"
# to name_prefix - so it is read back off IAM rather than predicted. What the
# configuration DOES determine is the prefix and that there is exactly one.
ROLE_NAME="$(awsl iam list-roles \
  --query "Roles[?starts_with(RoleName, '$ROLE_PREFIX') == \`true\`].RoleName | [0]" --output text)"
[ -n "$ROLE_NAME" ] && [ "$ROLE_NAME" != "None" ] \
  || fail "IAM holds no role whose name begins with $ROLE_PREFIX"
ROLE_N="$(awsl iam list-roles \
  --query "Roles[?starts_with(RoleName, '$ROLE_PREFIX') == \`true\`] | length(@)" --output text)"
[ "$ROLE_N" = "1" ] || fail "IAM holds $ROLE_N roles beginning with $ROLE_PREFIX, expected 1"
ATTACHED="$(awsl iam list-attached-role-policies --role-name "$ROLE_NAME" \
  --query "AttachedPolicies[?PolicyArn=='$POLICY_ARN'] | length(@)" --output text)"
[ "$ATTACHED" = "1" ] || fail "$POLICY_ARN is not attached to $ROLE_NAME"
log "  OIDC provider $OIDC_ARN"
log "  role          $ROLE_NAME"
log "  attachment    $ROLE_NAME -> $POLICY_ARN"

# Markers versus no marker. The OIDC provider and the role are taggable and
# must carry both markers; the attachment is not a taggable object at all -
# it is an edge, and its identity is its two endpoints - so it carries
# nothing and needs nothing.
#
# Both addresses are count instances, and the marker carries the ESCAPED
# address: AWS tag values cannot hold "[" or "]", so `this[0]` is written
# `this:0`. That rule is live/MARKERS.md, "Escaping rule".
OIDC_TAG="$(awsl iam list-open-id-connect-provider-tags --open-id-connect-provider-arn "$OIDC_ARN" \
  --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$OIDC_TAG" = "module.github_oidc_iam_provider.aws_iam_openid_connect_provider.this:0" ] \
  || fail "the OIDC provider carries tofu-address=$OIDC_TAG"
OIDC_EST="$(awsl iam list-open-id-connect-provider-tags --open-id-connect-provider-arn "$OIDC_ARN" \
  --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$OIDC_EST" = "$ESTATE" ] || fail "the OIDC provider carries tofu-estate=$OIDC_EST, expected $ESTATE"
ROLE_TAG="$(awsl iam list-role-tags --role-name "$ROLE_NAME" \
  --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || echo None)"
[ "$ROLE_TAG" = "module.github_oidc_iam_role.aws_iam_role.this:0" ] \
  || fail "the role carries tofu-address=$ROLE_TAG"
log "  2 of $INSTANCES objects carry a tofu-address marker; the third is an"
log "  aws_iam_role_policy_attachment, which is untaggable and re-derives its"
log "  identity from the declaration on every run"

plan_into() {
  rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
  ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color )
}

# ── 5. force_detach_policies: an argument IAM never returns ─────────────────
# The iam-role module writes force_detach_policies = true. IAM does not
# report it on a read - it is a delete-time behaviour flag, not state - so
# every cold run reads it as false and proposes an in-place update to true.
# Applying that update does not settle it, because there is nowhere for the
# value to live: the state file is gone by design and no cloud read answers.
#
# That is issue #275's shape, and the estate needs one block for it. The step
# shows both halves rather than jumping to the answer, so that the day the
# argument stops needing a record store this says so instead of going quiet.
log "=== 5. force_detach_policies, and the record store it needs ==="
PLAN0_OUT="$(plan_into 2>&1)"; PLAN0_RC=$?
[ "$PLAN0_RC" -eq 0 ] || { printf '%s\n' "$PLAN0_OUT" | grep -E '^Error|^│' | head -30; fail "the first live-plan exited $PLAN0_RC"; }
grep -qE '^  # module\.github_oidc_iam_role\.aws_iam_role\.this\[0\] will be updated in-place' <<< "$PLAN0_OUT" \
  || { grep -E '^  # .+ will be' <<< "$PLAN0_OUT"
       fail "expected exactly the role's in-place update. If this is now absent, force_detach_policies has somewhere to live already and this step should be inverted."; }
grep -qE '^ +~ force_detach_policies = false -> true' <<< "$PLAN0_OUT" \
  || { grep -E '^ +[+~-] [a-z_]+ +=' <<< "$PLAN0_OUT" | head; fail "the in-place update is not the force_detach_policies one this step is about"; }
CHANGES0="$(grep -cE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN0_OUT")"
[ "$CHANGES0" = "1" ] || { grep -E '^  # .+ will be' <<< "$PLAN0_OUT"; fail "the plan proposes $CHANGES0 resource changes, expected exactly 1"; }
log "  1 in-place update, and it is force_detach_policies false -> true"

APPLY_FD="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_FD" | tail -20; fail "the force_detach_policies apply failed"; }
PLAN0B_OUT="$(plan_into 2>&1)"
grep -qE '^ +~ force_detach_policies = false -> true' <<< "$PLAN0B_OUT" \
  || fail "the force_detach_policies diff SETTLED after an apply, with no record_store declared. There is nowhere for the value to live in that configuration, so this is not the fix below arriving early - re-read it."
log "  applying it does not settle it: the same update comes back"

# DELTA 3, and it is one block. A record_store gives the estate a residue
# namespace, and one apply through it classifies force_detach_policies by
# putting the role to the provider twice - once with a prior carrying only
# the identity, once with the applied object - and recording it because IAM
# answers nothing for it either way. The module is left exactly as
# terraform-aws-modules wrote it.
perl -0pi -e 's/^(  live \{\n    estate = "'"$ESTATE"'" # DELTA 2\n)/$1\n    record_store "local" {\n      path = ".tofu-records" # DELTA 3\n    }\n/m' "$EST/versions.tf"
grep -q 'DELTA 3' "$EST/versions.tf" || { cat "$EST/versions.tf"; fail "DELTA 3 did not reach the live block"; }
log "  DELTA 3  record_store \"local\" added              (residue, #275)"

APPLY_R="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_R" | grep -E '^Error|^│' | head -20; fail "the apply through the record store failed"; }
RESKEY="$(grep -rl 'force_detach_policies' "$EST/.tofu-records" 2>/dev/null | head -1)"
[ -n "$RESKEY" ] || { find "$EST/.tofu-records" -type f 2>/dev/null | head -20; fail "no residue record carrying force_detach_policies was written"; }
# GitHub issue #364 unit A1 collapsed the once-separate "tofu-residue" root
# into the single per-instance envelope every record now lives in
# (internal/live/projection/record.go's RecordKeyPrefix), so a directory
# name can no longer say this key is a residue key - the envelope's own
# "residue" member does.
grep -qF '"residue":{' "$RESKEY" || { cat "$RESKEY"; fail "the force_detach_policies record carries no residue member: $RESKEY"; }
# It must not carry what IAM does answer. A stored copy of a live answer is a
# second opinion that would make the plan go empty over real drift.
for forbidden in '"name"' '"arn"' '"assume_role_policy"' '"path"'; do
  grep -q "$forbidden" "$RESKEY" && { cat "$RESKEY"; fail "the residue record carries $forbidden, which IAM answers"; }
done
log "  residue recorded in the merged tofu-records envelope, carrying force_detach_policies and nothing IAM answers"

# ── 5b. THE VALUE, not the verdict ──────────────────────────────────────────
log "=== 5b. no state file, and the rendered identities read out of the run ==="
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

WANT=("$OIDC_ARN" "$ROLE_NAME" "${ROLE_NAME}/${POLICY_ARN}")
if [ "${BREAK:-}" = "1" ]; then
  # Not a string nothing could produce: the SAME ARN shape, the same account,
  # the same host - just the wrong path component, which is exactly what a
  # provider that reconstructed the ARN instead of reading it would render.
  WANT[0]="${OIDC_ARN%/*}/token.actions.github.com"
  log "  BREAK=1: expecting ${WANT[0]}, which differs from the live ARN in one"
  log "           host label. The plan above stayed empty. This step must fail."
fi
for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$id\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | grep -c .)"
[ "$GOT_N" = "$INSTANCES" ] || fail "the run materialized $GOT_N distinct identities, expected $INSTANCES"
log "  all $INSTANCES rendered identities asserted as strings, and no fourth:"
for id in "${WANT[@]}"; do log "    $id"; done

# ── 6. and it converges ─────────────────────────────────────────────────────
log "=== 6. the next run proposes nothing either ==="
PLAN2_OUT="$(plan_into 2>&1)"; PLAN2_RC=$?
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN2_OUT" \
  && { grep -E '^  # .+ will be' <<< "$PLAN2_OUT"; fail "the second plan proposes a resource change, so the run does not converge"; }
for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN2_OUT" \
    || fail "the second run did not materialize \"$id\""
done
log "  the second cold plan is empty too, with the same $INSTANCES identities"

# ── 7. and applying it adds nothing ─────────────────────────────────────────
# This is the assertion the original defect would have failed. A run that
# cannot enumerate the OIDC provider proposes creating one, and IAM would
# then hold two - while every plan verdict above stayed clean, because
# creating a resource the run believes is absent is not an error.
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
AFTER_ROLES="$(awsl iam list-roles \
  --query "Roles[?starts_with(RoleName, '$ROLE_PREFIX') == \`true\`] | length(@)" --output text)"
[ "$AFTER_ROLES" = "1" ] \
  || fail "IAM now holds $AFTER_ROLES roles beginning with $ROLE_PREFIX, not 1 - a name_prefix role was created over the one the estate already owned"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  $(grep -E 'Apply complete' <<< "$APPLY2_OUT" | head -1)"
log "  still 1 OIDC provider, and it is the same one"

log ""
log "=== PASS ==="
log ""
log "A terraform-aws-modules example whose central object has a"
log "server-assigned ARN - findable only by enumerating the account and"
log "reading a marker - applied against an emulator, stripped of its state"
log "file, and replanned empty twice. All $INSTANCES rendered identities were"
log "checked as strings against IAM's own answer, and IAM still holds one"
log "OIDC provider rather than two."
log ""
log "It cost three deltas: the emulator flags, a live block, and one"
log "record_store for force_detach_policies - an argument IAM never answers."
log "No provider pin and no backend edit."
log ""
log "Run again with BREAK=1: everything above step 5b still passes and step"
log "5b goes red."
