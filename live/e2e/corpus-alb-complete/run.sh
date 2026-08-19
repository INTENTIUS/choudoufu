#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-alb's flagship "complete-alb" example
# (.corpus/alb/examples/complete-alb, pinned in live/corpus-manifest.json at
# tag v9.9.0), crossed through choudoufu against floci - the real, five-
# stage pipeline (cold deploy, migrate, test plan, test apply, drift and
# reconverge). ALB is one of the most commonly deployed AWS resources in
# Terraform, and this is the module's own application-load-balancer example
# (there is also "complete-nlb" for network load balancers - a different
# target, not crossed here). It had never been crossed against a cloud
# before this script existed.
#
# 80 real resources: the root VPC (1 VPC, 3 public + 3 private subnets, 3
# public route tables + 3 associations, 1 internet gateway/route, plus the
# account's default_* adopter trio - manage_default_* defaults to true on
# the v5.x line this module pins, unlike v6.x's opt-in default used by
# corpus-vpc-complete's own crossing), the ALB itself (1 LB, 6 listeners, 7
# listener rules, 1 listener certificate, 3 target groups, 3 target group
# attachments, 1 lambda permission, 1 security group + 2 VPC security group
# rules, 2 route53 A/AAAA records), two ACM certificates (root + wildcard,
# each with its own DNS validation record + validation wait), an S3 log
# bucket (terraform-aws-modules/s3-bucket, ObjectWriter ownership +
# log-delivery-write ACL), two Cognito resources (user pool + client - see
# lex00/floci#63 below for the domain, DELTA'd away), two Lambda functions via
# terraform-aws-modules/lambda (each with its own IAM role/policy/log
# group), and two plain EC2 instances.
#
# THREE REAL FLOCI GAPS FOUND AND FIXED IN THIS PASS (all filed, fixed,
# tested, merged to lex00/floci main, and re-pinned into live/floci-image
# below - not worked around with a config DELTA, because each one is a
# small, precise, generically useful fix rather than a feature this estate
# alone needed):
#
#   lex00/floci#58 (FIXED, dee11c78/12100986). ACM RequestCertificate built
#   a wildcard SAN's DNS validation record NAME with the literal "*." left
#   in it ("_hash.*.example.com." instead of real ACM's "_hash.example.com.").
#   module.wildcard_cert's aws_route53_record.validation therefore created a
#   record whose fqdn never matched what aws_acm_certificate_validation
#   waited for, and the apply hung until it failed outright. Any wildcard-
#   SAN certificate through terraform-aws-modules/acm hits this the same
#   way - a very common pattern (a cert covering both example.com and
#   *.example.com).
#
#   lex00/floci#61 (FIXED, 02430843/aac84853). S3 PutBucketAcl/PutObjectAcl
#   rejected the "log-delivery-write" canned ACL as unsupported.
#   terraform-aws-modules/s3-bucket's attach_elb_log_delivery_policy /
#   attach_lb_log_delivery_policy examples (both on here, as the ALB
#   module's own README says they must be) set object_ownership =
#   "ObjectWriter" with acl = "log-delivery-write" together - the standard
#   way to provision any ALB/NLB or S3-access-log bucket.
#
#   lex00/floci#62 (FIXED, fc25ea3d/4990c8ab). EC2 DescribeInstanceTypes
#   returned an empty result for "t3.nano" (aws_instance.this/other's
#   instance_type here, and a very common smallest-x86_64-burstable
#   default in example configs), so terraform-provider-aws's own instance
#   read failed outright even though RunInstances itself tolerates an
#   absent catalog entry.
#
# All three: reproduced against the OLD image, fixed with a small, targeted
# change plus new/extended regression tests (all green, full relevant test
# suites re-run), verified the fix by reverting it and watching the new
# tests fail, merged to lex00/floci main, and closed with the commit
# references. See each issue for the full detail this header only
# summarizes.
#
# TWO REAL FLOCI GAPS FOUND, LEFT OPEN (genuine feature builds, not
# one-field fixes - worked around here with documented deltas/behavior so
# this script can still stand the estate up and migrate it for real):
#
#   lex00/floci#63 (OPEN). Cognito CreateUserPoolDomain/DescribeUserPoolDomain/
#   DeleteUserPoolDomain are entirely unimplemented - no code anywhere in
#   floci's Cognito service touches "Domain" at all. DELTA 2 removes
#   aws_cognito_user_pool_domain.this and substitutes the literal value it
#   would have carried (its own `domain = local.name` argument) everywhere
#   the ALB module's authenticate-cognito/authenticate-oidc listener actions
#   referenced it.
#
#   lex00/floci#65 (OPEN). ELBv2 DescribeListeners/DescribeRules drop
#   AuthenticateCognitoConfig/AuthenticateOidcConfig entirely -
#   Action.java's model has no fields for either action type, so
#   CreateListener/CreateRule accept them (stage 1's apply succeeds cleanly)
#   but the read path echoes back only {"Type": "authenticate-cognito"},
#   config gone. This surfaces in stage 2: live-import stamps ownership tags
#   by re-planning a synthetic config built from the live-read object, and
#   since the live-read never populated authenticate_cognito/
#   authenticate_oidc, terraform-provider-aws correctly rejects the result
#   as internally inconsistent. Not a crash - live-import's own FAILED
#   bucket, accurately asserted in stage 2 below rather than worked around,
#   since it does not block the other 44 resources from stamping.
#
# ONE REAL CHOUDOUFU ADMISSION GAP BLOCKS STAGE 3, NOT FIXED HERE:
#
#   #309 (open). aws_cognito_user_pool_client is unadmitted: untaggable, no
#   resource identity schema in the pinned provider release, and
#   live/survey-full.json's own evidence says why - Cloud Control's generic
#   list handler needs UserPoolId as scoping input, which no enumeration leg
#   supplies today. What the survey did not consider: the Cognito-native
#   ListUserPoolClients API takes exactly that as its one required
#   parameter, and this configuration's own declaration always supplies it
#   (a reference to the already-admitted, taggable, ServerAssigned
#   aws_cognito_user_pool parent) - the same parent-derived shape as other
#   admitted composite-identity rows, just not yet wired to a discovery leg.
#   See the issue for the full technical lead. 1 site.
#
#   #305 (aws_default_network_acl/aws_default_route_table/
#   aws_default_security_group, the VPC module's default-object adopters)
#   is FIXED and merged as of this script's last verified run - it no
#   longer blocks anything here; the 3 sites it used to name are now
#   VERIFIED/DRIFTED and eligible in stage 2 like everything else.
#
#   Checked against #313 (corpus-security-group-complete's
#   data.aws_availability_zones-feeding-a-nested-module-for_each wall,
#   filed the same session #305 landed): this estate's live-plan output
#   carries exactly one distinct Error: line, the #309 unadmitted-type
#   refusal, and never "Unable to use data.aws_availability_zones.available
#   in static context". Different wall; #313 does not reach this estate.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, unmarked infrastructure, all 80
#                          resources, once for real (no manual retries) with
#                          the fixed floci image.
#   stage 2  migrate       PASS - real: 41 VERIFIED + 10 DRIFTED = 51 of 80
#                          resource instances eligible (#305's fix moved the
#                          default-object trio from unadmitted into this
#                          count); 47 newly stamped + 4 FAILED (floci#65) =
#                          51 attempted; the other 29 correctly skipped (28
#                          UNTAGGABLE by provider schema + 1 UNADMITTED_TYPE,
#                          #309), asserted against live-import's own report
#                          AND confirmed independently through the AWS CLI.
#   stage 3  test plan     BLOCKED, for real, by #309 alone (1 site) - the
#                          exact same type stage 2 already named, specific
#                          counts and resource addresses asserted against a
#                          real live-plan run on the really-migrated estate,
#                          state file deleted first, BREAK=1 negative
#                          control.
#   stage 4  test apply    NOT RUN - depends on stage 3.
#   stage 5  drift/reconverge  NOT RUN - depends on stages 3-4.
#
# A partial, honestly-reported pass is the point: this is the real, current
# behavior of choudoufu (and, until #58/#61/#62 landed, of floci) against a
# real, popular module, not a green claim routed around the truth.
#
#   bash live/e2e/corpus-alb-complete/run.sh
#
# Needs Docker, the AWS CLI, terraform (real, stock terraform - stage 1 is
# deliberately NOT choudoufu), network access (for `terraform init` to
# resolve the vpc/acm/s3-bucket/lambda registry modules, and to fetch the
# Lambda deployment zip fixture - see DELTA 3), and .corpus populated (`just
# corpus-fetch`).
#
# DELTA 3 is not a floci or choudoufu workaround: the example's own two
# lambda module calls build local.downloaded's filename from an md5 of a
# GitHub raw-content URL and fetch it via a null_resource local-exec
# provisioner, which the lambda module's locals then fileexists()-check.
# Terraform evaluates that check once for the plan embedded in "apply" and
# again after the provisioner runs, and a file that appears between those
# two reads is "a function returned an inconsistent result" - a real
# Terraform footgun in this module's own example, independent of the target
# cloud (corpus-lambda-simple's own header avoided this shape entirely by
# building its deployment zip with a `data "external"` script instead).
# DELTA 3 fetches the exact same fixture, to the exact filename Terraform
# expects, before invoking terraform at all, so both reads agree from the
# start; the null_resource's own curl still runs too, and simply overwrites
# the same file (idempotent, harmless).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4722, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image (re-pinned by this change to include
#                #58/#61/#62).
#   BREAK        set to 1 to corrupt the expected stage-3 site counts and
#                one expected unadmitted-type name, proving those
#                assertions are load-bearing rather than a grep that always
#                matches. Stages 1 and 2 are unaffected and still pass;
#                stage 3 is the one that must fail.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice - once for the cold, unmarked
# deploy, once for the migration attempt) and every delta below lands on a
# copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/alb"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4723}"
FLOCI_NAME="choudoufu-corpus-alb-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="alb-complete-crossing"
REGION="eu-west-1"
DOMAIN="terraform-aws-modules.modules.tf"
AMI_PARAM="/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2"
PKG_URL="https://raw.githubusercontent.com/terraform-aws-modules/terraform-aws-lambda/master/examples/fixtures/python3.8-zip/existing_package.zip"
PKG_HASH="$(printf '%s' "$PKG_URL" | md5 2>/dev/null || printf '%s' "$PKG_URL" | md5sum | cut -d' ' -f1)"
PKG_FILE="downloaded_package_${PKG_HASH}.zip"

INSTANCES=80
VERIFIED_WANT=41
DRIFTED_WANT=10
UNTAGGABLE_WANT=28
UNADMITTED_WANT=1
ELIGIBLE=$((VERIFIED_WANT + DRIFTED_WANT))
STAMPED_WANT=47
IMPORT_FAILED_WANT=4
SKIPPED_WANT=$((UNTAGGABLE_WANT + UNADMITTED_WANT))

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# copy_tree DEST - the alb module root plus examples/complete-alb,
# preserving the relative layout the example's `source = "../../"` needs.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/alb/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/alb/"
  cp -R "$SRC/examples/complete-alb" "$dest/alb/examples/complete-alb"
  rm -rf "$dest/alb/examples/complete-alb/.terraform" \
         "$dest/alb/examples/complete-alb/.terraform.lock.hcl" \
         "$dest/alb/examples/complete-alb/terraform.tfstate" \
         "$dest/alb/examples/complete-alb/terraform.tfstate.backup"
}

# apply_deltas EST_DIR - DELTA 1 (emulator provider flags), DELTA 2
# (Cognito domain removed, EMULATOR GAP lex00/floci#63), and a provider
# version pin (this checkout's admission tables were generated against
# 6.59.0).
apply_deltas() {
  local est="$1"
  perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$est/main.tf"
  grep -q 'DELTA 1' "$est/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"

  perl -0pi -e 's/resource "aws_cognito_user_pool_domain" "this" \{\n  domain       = local\.name\n  user_pool_id = aws_cognito_user_pool\.this\.id\n\}\n/# DELTA 2 (EMULATOR GAP, lex00\/floci#63): aws_cognito_user_pool_domain\n# removed - CreateUserPoolDomain is unimplemented in floci.\n/s' "$est/main.tf"
  grep -q 'DELTA 2' "$est/main.tf" || fail "DELTA 2 did not match the Cognito domain resource - the corpus pin has moved"
  perl -pi -e 's/aws_cognito_user_pool_domain\.this\.domain/local.name # DELTA 2/g' "$est/main.tf"
  grep -qF 'aws_cognito_user_pool_domain.this.domain' "$est/main.tf" && fail "DELTA 2 left a live reference to the removed Cognito domain resource"

  perl -0pi -e 's/version = ">= 5\.46"/version = "= 6.59.0"/' "$est/versions.tf"
  grep -q '= 6.59.0' "$est/versions.tf" || fail "the provider version pin did not match versions.tf - the corpus pin has moved"
}

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
command -v curl >/dev/null 2>&1 || fail "curl is not on PATH - DELTA 3 needs it to prefetch the Lambda deployment zip"
[ -d "$SRC/examples/complete-alb" ] || fail "$SRC/examples/complete-alb is missing - run 'just corpus-fetch' first"

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

PLAIN="$WORK/plain"
copy_tree "$PLAIN"
PLAIN_EST="$PLAIN/alb/examples/complete-alb"
apply_deltas "$PLAIN_EST"
log "  estate copied out of .corpus into $PLAIN_EST"

# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, $INSTANCES real resources ==="

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"acm"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"acm"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (acm) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# Two preconditions this estate's own DATA SOURCES read but nothing in the
# config creates: the AMI lookup (data.aws_ssm_parameter.al2, AWS's own
# public parameter that floci does not seed) and the Route53 zone
# (data.aws_route53_zone.this, name = var.domain_name - a zone the
# ALB module's real users would already own before adopting it).
log "=== 1b. preconditions: seed the AMI parameter and the Route53 zone ==="
awsl ssm put-parameter --name "$AMI_PARAM" --type String --value "ami-0c55b159cbfafe1f0" --overwrite >/dev/null \
  || fail "could not seed $AMI_PARAM"
awsl route53 create-hosted-zone --name "$DOMAIN" --caller-reference "alb-complete-$$" >/dev/null \
  || fail "could not create the $DOMAIN hosted zone"
log "  $AMI_PARAM seeded; $DOMAIN hosted zone created"

# DELTA 3 (not a floci/choudoufu workaround - see this script's own
# header): prefetch the Lambda deployment zip to the exact filename
# Terraform expects, so the module's own fileexists() check agrees with
# itself across the plan-then-apply boundary.
curl -fsSL -o "$PLAIN_EST/$PKG_FILE" "$PKG_URL" || fail "could not prefetch the Lambda deployment zip fixture"
log "  DELTA 3  Lambda deployment zip prefetched to $PKG_FILE       (module-example quirk, not floci/choudoufu)"

log "=== 1c. terraform init + apply ==="
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
mkdir -p "$TF_PLUGIN_CACHE_DIR"
( cd "$PLAIN_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "plain terraform init failed"; }
PLAIN_APPLY_OUT="$(cd "$PLAIN_EST" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY_OUT" | tail -60
  fail "the plain terraform apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$PLAIN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT"; fail "the apply did not create exactly $INSTANCES resources - the corpus pin or the emulator has moved"; }
[ -f "$PLAIN_EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"
log "  $(grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT")"
log "  real terraform.tfstate, zero choudoufu markers - the VPC, the ALB with"
log "  6 listeners and 7 listener rules, 2 ACM certificates, an S3 log"
log "  bucket, a Cognito user pool + client, two Lambda functions, and two"
log "  EC2 instances"

# Confirmed unmarked: read the ALB's own tags directly, never through
# choudoufu.
LB_ARN="$(terraform -chdir="$PLAIN_EST" output -raw arn)"
[ -n "$LB_ARN" ] && [ "$LB_ARN" != "None" ] || fail "could not read the ALB's arn from terraform output"
MARKER_COUNT="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "length(TagDescriptions[0].Tags[?Key=='tofu-address'])" --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the ALB already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $LB_ARN carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/alb/examples/complete-alb"
apply_deltas "$ADOPTED_EST"
curl -fsSL -o "$ADOPTED_EST/$PKG_FILE" "$PKG_URL" || fail "could not prefetch the Lambda deployment zip fixture (adopted copy)"

# DELTA 4, onboarding: add the live block. record_store is needed for
# null_resource.download_package (an effects-only resource - see the
# record-store fixture).
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \"= 6\.59\.0\"\n    \}\n    null = \{\n      source  = \"hashicorp\/null\"\n      version = \">= 2\.0\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 4 did not match versions.tf - the corpus pin has moved"
log "  DELTA 4  live block + local record_store added             (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - this estate's resource shape has moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "$VERIFIED_WANT" ] || fail "expected $VERIFIED_WANT VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "$DRIFTED_WANT" ] || fail "expected $DRIFTED_WANT DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "$UNTAGGABLE_WANT" ] || fail "expected $UNTAGGABLE_WANT UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "$UNADMITTED_WANT" ] || fail "expected $UNADMITTED_WANT UNADMITTED_TYPE (#309), got ${UNADMITTED_N:-0}"
grep -qF 'module.vpc.aws_default_network_acl.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_network_acl.this[0] among DRIFTED (#305, fixed)"
grep -qF 'module.vpc.aws_default_route_table.default[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_route_table.default[0] among VERIFIED (#305, fixed)"
grep -qF 'module.vpc.aws_default_security_group.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_security_group.this[0] among VERIFIED (#305, fixed)"
grep -qF 'aws_cognito_user_pool_client.this' <<< "$IMPORT_OUT" || fail "expected aws_cognito_user_pool_client.this among UNADMITTED_TYPE (#309)"
log "  $ELIGIBLE of $INSTANCES eligible ($VERIFIED_WANT VERIFIED + $DRIFTED_WANT DRIFTED); $SKIPPED_WANT skipped"
log "  ($UNTAGGABLE_WANT UNTAGGABLE by provider schema + $UNADMITTED_WANT UNADMITTED_TYPE - #309's"
log "  aws_cognito_user_pool_client; #305's default_* trio is now admitted and"
log "  eligible above); nothing written yet"

log "=== 2b. -approve: stamp the eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$STAMPED_WANT resource(s) newly stamped, 0 already stamped, $IMPORT_FAILED_WANT failed, $SKIPPED_WANT skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not report exactly $STAMPED_WANT stamped / $IMPORT_FAILED_WANT failed / $SKIPPED_WANT skipped"; }
# The FAILED sites are all lex00/floci#65 (ELBv2 dropping
# AuthenticateCognitoConfig/AuthenticateOidcConfig on read) - asserted by
# name, not just count, so a different failure shape would be caught.
for addr in 'module.alb.aws_lb_listener.this["ex-cognito"]' 'module.alb.aws_lb_listener.this["ex-oidc"]' 'module.alb.aws_lb_listener_rule.this["ex-cognito/ex-oidc"]'; do
  grep -qF "$addr" <<< "$APPROVE_OUT" || fail "expected $addr among the FAILED-to-stamp resources (floci#65)"
done
grep -qF "must be specified when" <<< "$APPROVE_OUT" || fail "expected floci#65's provider validation error text among the FAILED details"
log "  $STAMPED_WANT stamped, $IMPORT_FAILED_WANT failed (floci#65, named above), $SKIPPED_WANT skipped -"
log "  matches the dry run exactly"

log "=== 2c. the ALB's own marker, read through the AWS CLI directly ==="
WANT_LB_ADDR="module.alb.aws_lb.this:0"
GOT_LB_ADDR="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "TagDescriptions[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_LB_ADDR" = "$WANT_LB_ADDR" ] || fail "the ALB carries tofu-address=$GOT_LB_ADDR, not $WANT_LB_ADDR"
GOT_LB_ESTATE="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "TagDescriptions[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_LB_ESTATE" = "$ESTATE" ] || fail "the ALB carries tofu-estate=$GOT_LB_ESTATE, not $ESTATE"
log "  $LB_ARN now carries tofu-address=$GOT_LB_ADDR tofu-estate=$GOT_LB_ESTATE"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - #309 may be fixed; update this script"; }

WANT_UNADMITTED_N=$UNADMITTED_WANT
WANT_TYPES=(aws_cognito_user_pool_client)
if [ "${BREAK:-}" = "1" ]; then
  WANT_UNADMITTED_N=2
  WANT_TYPES[1]="aws_default_dhcp_options"
  log "  BREAK=1: expecting 2 unadmitted-type sites (one more than the real"
  log "           1) and aws_default_dhcp_options among them - a real AWS"
  log "           default-object type, same shape as the ones #305 already"
  log "           fixed, just not one this estate's config actually"
  log "           declares. Both wrong. This step must fail."
fi

log "  all distinct Error: lines from this live-plan run:"
grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c | sed 's/^/    /'
UNADMITTED_SITES_N="$(grep -c '^Error: Resource type is outside the live-markers subset$' <<< "$PLAN_OUT")"
[ "$UNADMITTED_SITES_N" = "$WANT_UNADMITTED_N" ] \
  || { fail "expected $WANT_UNADMITTED_N unadmitted-type sites (#309), got $UNADMITTED_SITES_N"; }
for t in "${WANT_TYPES[@]}"; do
  grep -qE "resource \"$t\"" <<< "$PLAN_OUT" \
    || { printf '%s\n' "$PLAN_OUT" | grep -E '^Error:|resource "'; fail "expected $t among the unadmitted-type refusals"; }
done
log "  #309 confirmed: exactly 1 aws_cognito_user_pool_client site - no"
log "  identity schema in the pinned provider release, untaggable, and no"
log "  discovery leg wired to the Cognito-native ListUserPoolClients API"
log "  yet (see the issue for the technical lead). #305's default-object"
log "  trio is fixed and no longer appears as an unadmitted-type site here"
log "  (confirmed VERIFIED/DRIFTED and eligible in stage 2 above)."

log ""
log "STAGE 3 (test_plan): BLOCKED for real - #309 (1 site), the same type"
log "stage 2 already named as UNADMITTED_TYPE; #305's 3 sites are no longer"
log "part of this wall"
log ""
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS ($INSTANCES resources, once for real - see"
log "                              header for the 3 floci fixes this needed:"
log "                              #58, #61, #62)"
log "  stage 2  migrate            PASS (real: $STAMPED_WANT of $INSTANCES stamped, $IMPORT_FAILED_WANT failed on"
log "                              floci#65, see header)"
log "  stage 3  test_plan          BLOCKED - #309 (choudoufu, see header); #305's"
log "                              trio is fixed and no longer a stage-3 wall here"
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "$INSTANCES real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
