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
# A FOURTH FLOCI GAP, FOUND HERE AND SINCE FIXED UPSTREAM:
#
#   lex00/floci#65 (CLOSED 2026-08-22). ELBv2 DescribeListeners/DescribeRules
#   dropped AuthenticateCognitoConfig/AuthenticateOidcConfig entirely -
#   Action.java's model had no fields for either action type, so
#   CreateListener/CreateRule accepted them (stage 1's apply always succeeded
#   cleanly) but the read path echoed back only
#   {"Type": "authenticate-cognito"}, config gone. It surfaced in stage 2:
#   live-import stamps ownership tags by re-planning a synthetic config built
#   from the live-read object, and since the live-read never populated
#   authenticate_cognito/authenticate_oidc, terraform-provider-aws correctly
#   rejected the result as internally inconsistent ("... must be specified
#   when type is 'authenticate-cognito'"). Four instances landed in
#   live-import's own FAILED bucket - the two authenticate-* listeners and
#   the two listener rules under them.
#
#   The fix is in the image live/floci-image pins (bumped to
#   sha256:0afd2648... by commit 4649c73d52, whose own crossing was a
#   different estate's), and this estate had simply not been re-crossed
#   since. Stage 2 below now asserts the reverse: all four stamp, and the
#   provider's validation text must NOT appear. This half of this pass's
#   staleness is unrelated to the credential-veto change below; a worker
#   reconciling issue #365 slice 3 measured the same 51/1/0/28 on a
#   stock-main control binary.
#
# ONE FLOCI GAP FOUND, LEFT OPEN (a genuine feature build, not a one-field
# fix - worked around here with a documented delta so this script can still
# stand the estate up and migrate it for real):
#
#   lex00/floci#63 (OPEN). Cognito CreateUserPoolDomain/DescribeUserPoolDomain/
#   DeleteUserPoolDomain are entirely unimplemented - no code anywhere in
#   floci's Cognito service touches "Domain" at all. DELTA 2 removes
#   aws_cognito_user_pool_domain.this and substitutes the literal value it
#   would have carried (its own `domain = local.name` argument) everywhere
#   the ALB module's authenticate-cognito/authenticate-oidc listener actions
#   referenced it.
#
# WHAT BLOCKS STAGE 3 NOW, AND WHAT USED TO:
#
#   THE WALL THAT CLEARED (#309's last site, gone as of 2026-08-22).
#
#   #309 (CLOSED 2026-08-21, under the reframe that retired admission as a
#   gate - HANDOFF.md). aws_cognito_user_pool_client is no longer unadmitted:
#   the issue's own MarkerlessTypes-widening work (closing comment,
#   2026-08-19) put it in the roster, where record_store-declared estates
#   like this one resolve it as identity.ClassRecordLocated (issue #270).
#   For a while it still blocked this estate's stage 3, one layer down, as
#   RuleMarkerlessType ("Resource type has nowhere to write an ownership
#   marker") rather than RuleUnadmittedType. identity.LocatedType
#   (internal/live/identity/located.go) answered false on two of its
#   conditions independently, and both are now answered:
#
#     condition 3, the identity cannot be recorded IN FULL. CLEARED
#     2026-08-22 (branch gauntlet/albcomplete-importgrammar). It was in
#     IDNotProvenWholeTypes (idnotwhole_generated.go); its Import section
#     documents a composite <user pool id>/<client id> string the exported
#     `id` bullet does not corroborate, so `id` might have been a fragment.
#     Neither route out was open - hashicorp/aws 6.59.0 serves NO wire
#     identity schema for the type, and it had no DocumentedImportIDs
#     grammar, because its page names its segments in prose ("the `id` of
#     the Cognito User Pool, and the `id` of the Cognito User Pool Client")
#     rather than one token at a time. tools/importdocs-gen now reads that
#     sentence. The generic rule is the possessive-of one, not a Cognito
#     one: English states a qualified name in two orders, and where the
#     schema's order ("using the `user_pool_id` and `client_id`", which
#     every existing reader resolves) is written the other way round, each
#     segment is re-read owner-first and matched EXACTLY against the page's
#     own Argument and Attribute Reference.
#     TestPossessiveOfGrammarComposesTheDocumentedImportString pins the
#     composed string BY VALUE against the provider's own documented import
#     example - us-west-2_abc123/3ho4ek12345678909nh3fmhpko - because a
#     reading that swapped the two segments would be the same shape, the
#     same length and a different object.
#
#     condition 2, credential material. CLEARED 2026-08-22 (commit
#     80666bc1c0, issue #365 population 2). The veto was answering the wrong
#     question. It asked whether the TYPE carries a secret anywhere in its
#     schema (identity.credentialMaterial's whole-schema sweep, which is
#     internal/live/projection's residue question, a value-preservation
#     promise the located route makes no claim about). What this route
#     actually writes is locatedImportIDAttr or the identity plan's own
#     components and nothing else, so the only secret it can leak is one
#     that IS an identity component. Narrowed to sensitiveIdentityAttr.
#     aws_cognito_user_pool_client's recorded identity is user_pool_id/id;
#     it never touched client_secret at all, and refusing it bought nothing.
#     Nine of the eleven types the old veto excluded were in that position.
#     The two whose exclusion is a dated maintainer ruling
#     (aws_iam_access_key, aws_iot_certificate) stay refused through a named
#     list, and aws_wafv2_api_key - whose recorded identity IS api_key -
#     still refuses on the narrowed rule itself, which is why the narrowing
#     had to stay identity-aware rather than be a deletion.
#
#   With both answered, live-plan raises ZERO markerless-type diagnostics on
#   this estate. Stage 3 asserts that by count, and it is the load-bearing
#   assertion that this wall is gone.
#
#   #305 (aws_default_network_acl/aws_default_route_table/
#   aws_default_security_group, the VPC module's default-object adopters)
#   is FIXED and merged - it no longer blocks anything here; the 3 sites it
#   used to name are now VERIFIED/DRIFTED and eligible in stage 2 like
#   everything else.
#
#   WHAT THE CLEARED WALL WAS MASKING, AND IS THE STAGE-3 BLOCKER NOW.
#
#   internal/command/live_plan.go runs lint.CheckWith FIRST and returns on
#   the first error-severity issue, before the static-scope evaluation and
#   before identity resolution. So while the one markerless-type refusal
#   stood, it was the ONLY diagnostic this estate could ever print, and this
#   header's previous claim that "this estate's live-plan output carries
#   exactly one distinct Error: line" was a statement about that early
#   return, not about the estate. Measured 2026-08-22 by running the
#   pre-fix binary (80666bc1c0^) and the fixed one against the SAME migrated
#   estate and the same live floci: pre-fix, 1 diagnostic, all of it the
#   markerless-type refusal; fixed, 0 of those and 20 others.
#
#   The 20 are HANDOFF's first row - choudoufu refuses where stock proceeds,
#   which is a defect - and they are the config-language subset wall, not a
#   type-coverage one. Every resource they BLOCK is UNTAGGABLE - six of the
#   twenty are reported at the module call rather than at a resource, and
#   name the module input variable they poison. That is the
#   association/attachment/record family, whose identity is a composite
#   of its parents and so must be evaluable from configuration alone,
#   because there is no tag on it to recover it from. The taggable resources
#   over the very same expressions do NOT refuse - module.alb's
#   aws_lb_target_group.this for_eaches over exactly the same
#   var.target_groups as aws_lb_target_group_attachment.this at line 565 and
#   is silent, because it carries the tofu-address stage 2 wrote.
#
#   Two independent root causes:
#
#     A. A resource or module-output reference nested inside a module
#        INPUT's object or list literal. 12 of the 20 diagnostics, across
#        three of module.alb's inputs:
#
#          var.target_groups          - `target_id = aws_instance.this.id`
#                                       and `target_id =
#                                       module.lambda_*.lambda_function_arn`
#          var.additional_target_group_attachments
#                                     - `target_id = aws_instance.other.id`
#          var.listeners              - `additional_certificate_arns =
#                                       [module.wildcard_cert.acm_certificate_arn]`
#
#        Each whole variable then reads as unavailable inside module.alb, so
#        everything downstream of it collapses: the for_each on
#        aws_lb_target_group_attachment.this, local.lambda_target_groups and
#        the aws_lambda_permission.this for_each built from it,
#        aws_lb_target_group_attachment.additional's target_group_arn and
#        port, and aws_lb_listener_certificate.this's certificate_arn.
#
#        Note what IS available: the map keys are all static - only the
#        leaves are poisoned - and every poisoned leaf is an identity
#        attribute of a resource stage 2 successfully stamped
#        (aws_instance.this.id, aws_lambda_function.this[0].arn,
#        aws_acm_certificate.this[0].arn). internal/live/identity/
#        partialargs.go already substitutes a poisoned leaf inside an object
#        constructor on the direct-argument path. The gap is the module-call
#        variable path in internal/configs (module_call.go,
#        static_evaluator.go, static_scope.go), which is the same surface
#        choudoufu #375 names for the sibling shape on the module-OUTPUT
#        side.
#
#     B. A server-produced attribute driving an untaggable child's identity.
#        8 of the 20 diagnostics, 4 for each of the two certificate module
#        instances. terraform-aws-modules/acm's local.validation_domains is
#        built from aws_acm_certificate.this[0].domain_validation_options,
#        and aws_route53_record.validation[0]'s name and type are elements
#        of it. domain_validation_options is minted by ACM; no static
#        evaluation can produce it, and stock only manages because it reads
#        it back out of the state file this stage deliberately deleted. The
#        answer for this shape is the record rung (HANDOFF's fourth row,
#        issue #364), not a cleverer evaluator: live-import currently SKIPS
#        every untaggable instance rather than recording one, so replanning
#        from nothing has nothing to bind these records to.
#
#   Neither is attempted in this pass. Both touch load-bearing surfaces
#   (internal/configs' static evaluation for A, live-import's skip/record
#   split for B), and neither can satisfy HANDOFF's safety rule from this
#   script alone - the run never gets far enough to render the affected
#   identities, so there is no value to assert. Each is its own unit.
#
#   Checked against #313 (corpus-security-group-complete's
#   data.aws_availability_zones-feeding-a-nested-module-for_each wall):
#   none of the 20 diagnostics mentions data.aws_availability_zones.
#   Different wall; #313 does not reach this estate.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, unmarked infrastructure, all 80
#                          resources, once for real (no manual retries) with
#                          the fixed floci image.
#   stage 2  migrate       PASS - real: 41 VERIFIED + 10 DRIFTED = 51 of 80
#                          resource instances eligible (#305's fix moved the
#                          default-object trio from unadmitted into this
#                          count); all 51 newly stamped, 0 FAILED (floci#65
#                          is fixed in the pinned image - it used to fail 4);
#                          the other 29 not eligible (28 UNTAGGABLE by
#                          provider schema + 1 UNADMITTED_TYPE, which is
#                          live-import's own bucket name for
#                          aws_cognito_user_pool_client - live-import has its
#                          own knowledge question, separate from live-plan's
#                          LocatedType route, and it still answers no) - of
#                          which -approve records 1
#                          (null_resource.download_package, record-backed
#                          since #340, seeded into the record store rather
#                          than skipped) and correctly skips 28. Asserted
#                          against live-import's own report AND confirmed
#                          independently through the AWS CLI.
#   stage 3  test plan     BLOCKED, for real, at 20 diagnostics in the
#                          config-language subset, all of them on untaggable
#                          resources, in the two families above. #309's
#                          markerless-type site is GONE and stage 3 asserts
#                          that by count. Specific counts, summaries and
#                          resource addresses asserted against a real
#                          live-plan run on the really-migrated estate, state
#                          file deleted first, BREAK=1 negative control.
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
#   FLOCI_PORT   host port for the emulator (default 4723, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected stage-3 diagnostic total
#                and one expected refusal site, proving those assertions are
#                load-bearing rather than a grep that always matches. Stages
#                1 and 2 are unaffected and still pass; stage 3 is the one
#                that must fail.
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
STAMPED_WANT=$ELIGIBLE
IMPORT_FAILED_WANT=0
SKIPPED_WANT=$((UNTAGGABLE_WANT + UNADMITTED_WANT))
# SKIPPED_WANT is the DRY RUN's own not-eligible total, which -approve then
# splits in two (issue #340): null_resource.download_package is record-backed,
# so -approve seeds the record store for it and reports it RECORDED rather
# than SKIPPED. The dry run's UNTAGGABLE/UNADMITTED_TYPE counts do not move -
# ratifyRecordBacked still answers StatusUntaggable - so only the -approve
# summary line splits.
RECORDED_WANT=1
APPROVE_SKIPPED_WANT=$((SKIPPED_WANT - RECORDED_WANT))

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
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

gauntlet_begin

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

CURRENT_STAGE=cold_deploy
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
# #339: the shared cache records no checksums, so init in a directory with no
# .terraform.lock.hcl re-downloads the whole provider purely to compute them,
# even when the cache already holds that exact version. TF_PLUGIN_CACHE_MAY_
# BREAK_DEPENDENCY_LOCK_FILE is real terraform's and OpenTofu's own CLI-config
# accommodation for this - both binaries below honor it, so it fixes it for
# each init independently, not just when a lock file happens to already
# exist. Every directory here is a throwaway mktemp copy, never committed,
# never run on a second platform, so the trade-off (only this platform's
# checksum gets recorded) costs nothing.
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
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
gauntlet_stage cold_deploy pass "$INSTANCES resources, once for real (floci fixes #58, #61, #62)"
CURRENT_STAGE=migrate

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
grep -qF "$STAMPED_WANT resource(s) newly stamped, 0 already stamped, $RECORDED_WANT newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, $IMPORT_FAILED_WANT failed, $APPROVE_SKIPPED_WANT skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not report exactly $STAMPED_WANT stamped / $RECORDED_WANT recorded / $IMPORT_FAILED_WANT failed / $APPROVE_SKIPPED_WANT skipped"; }
# The four authenticate-cognito/authenticate-oidc sites used to be
# live-import's whole FAILED bucket (lex00/floci#65: ELBv2 DescribeListeners
# and DescribeRules dropped AuthenticateCognitoConfig/AuthenticateOidcConfig
# on read, so the synthetic config live-import re-plans came back internally
# inconsistent and terraform-provider-aws rejected it with "... must be
# specified when type is 'authenticate-cognito'"). floci#65 is fixed and the
# fix is in the pinned image, so they now stamp like everything else. Asserted
# by name inside the STAMPED section, not just by count, so a regression that
# moved them back into FAILED - or into any other bucket - is caught.
STAMPED_BLOCK="$(awk '/^STAMPED \(/{s=1;next} /^[A-Z_]+ \([0-9]+\)/{s=0} s' <<< "$APPROVE_OUT")"
for addr in 'module.alb.aws_lb_listener.this["ex-cognito"]' \
            'module.alb.aws_lb_listener.this["ex-oidc"]' \
            'module.alb.aws_lb_listener_rule.this["ex-cognito/ex-oidc"]' \
            'module.alb.aws_lb_listener_rule.this["ex-https/ex-cognito"]'; do
  grep -qF "$addr" <<< "$STAMPED_BLOCK" || fail "expected $addr among the STAMPED resources (floci#65 is fixed in the pinned image)"
done
grep -qF "must be specified when" <<< "$APPROVE_OUT" && fail "floci#65's provider validation error text is back in live-import's output - the emulator pin has regressed"
log "  $STAMPED_WANT stamped, $RECORDED_WANT recorded (null_resource.download_package),"
log "  $IMPORT_FAILED_WANT failed, $APPROVE_SKIPPED_WANT skipped - the dry run's"
log "  $SKIPPED_WANT not-eligible, one of them record-backed and so recorded rather than skipped"
log "  the four authenticate-cognito/authenticate-oidc sites stamp now (floci#65 fixed)"

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
gauntlet_stage migrate pass "$STAMPED_WANT of $INSTANCES stamped, $RECORDED_WANT recorded, $IMPORT_FAILED_WANT failed, $APPROVE_SKIPPED_WANT skipped"
CURRENT_STAGE=test_plan

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
[ "$PLAN_RC" -ne 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -30; fail "live-plan succeeded - the config-language subset wall below is gone; update this script and push on to stage 4"; }

log "  all distinct Error: lines from this live-plan run:"
grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c | sed 's/^/    /'

# ── 3a. the wall that cleared: zero markerless-type refusals ───────────────
# identity.LocatedType's condition 2 was the last thing refusing
# aws_cognito_user_pool_client here (condition 3 was answered by the
# possessive-of import grammar on 2026-08-22; condition 2 by commit
# 80666bc1c0, which narrowed the credential veto from "the type has a secret
# anywhere in its schema" to "the identity this route would RECORD carries a
# secret"). This type's recorded identity is user_pool_id/id and never
# touched client_secret, so the veto was answering a question this route does
# not ask. Asserted by count AND by absence of the type's own name anywhere
# in the output, so a refusal that merely changed its wording is still
# caught. See this script's header.
MARKERLESS_SITES_N="$(grep -c '^Error: Resource type has nowhere to write an ownership marker$' <<< "$PLAN_OUT")"
[ "$MARKERLESS_SITES_N" = "0" ] \
  || { grep -E '^Error:' <<< "$PLAN_OUT" | sort -u; fail "expected 0 markerless-type refusals (the credential veto was narrowed in 80666bc1c0), got $MARKERLESS_SITES_N"; }
grep -qF 'aws_cognito_user_pool_client' <<< "$PLAN_OUT" \
  && { printf '%s\n' "$PLAN_OUT" | grep -F 'aws_cognito_user_pool_client'; fail "aws_cognito_user_pool_client still appears in live-plan's output - the wall this pass recorded as cleared is not cleared"; }
log "  3a  0 markerless-type refusals; aws_cognito_user_pool_client does not"
log "      appear in live-plan's output at all. #309's last site is gone."

# ── 3b. what the cleared wall was masking ─────────────────────────────────
# internal/command/live_plan.go runs lint.CheckWith first and returns on the
# first error-severity issue, so while that one refusal stood it was the only
# diagnostic this estate could print. Measured 2026-08-22 against the same
# migrated estate and the same live floci: the pre-fix binary (80666bc1c0^)
# printed 1 diagnostic and nothing else; the fixed one prints these 20.
#
# All 20 are HANDOFF's first row - choudoufu refuses where stock proceeds,
# which is a defect - and every resource they BLOCK is UNTAGGABLE, whose
# identity has to come from configuration because there is no tag to recover
# it from. Six of the twenty are reported at the module call rather than at a
# resource, and name the module input variable they poison.
# Two independent root causes, A (a resource or module-output reference
# nested inside a module input's object/list literal, 12) and B (a
# server-produced attribute driving an untaggable child's identity, 8). The
# header carries the full trace of both.
WANT_DIAG_N=20
declare -a WANT_SITES=(
  # A: the poisoned leaves, at the module call
  'Unable to use aws_instance.this in static context'
  'module.lambda_with_allowed_triggers.lambda_function_arn'
  'module.lambda_without_allowed_triggers.lambda_function_arn'
  # A: what collapses inside module.alb once the variable is unavailable
  'module.alb:aws_lb_target_group_attachment.this for_each depends on'
  'module.alb:aws_lambda_permission.this for_each depends on'
  'module.alb:local.lambda_target_groups depends on'
  'module.alb.aws_lb_target_group_attachment.additional["ex-instance-other"].target_group_arn'
  'module.alb.aws_lb_target_group_attachment.additional["ex-instance-other"].port'
  'module.alb.aws_lb_listener_certificate.this["ex-https/0"].certificate_arn'
  # B: the ACM DNS-validation records, once per certificate module instance
  'Unable to use aws_acm_certificate.this[0] in static context'
  'module.acm.aws_route53_record.validation[0].name'
  'module.acm.aws_route53_record.validation[0].type'
  'module.wildcard_cert.aws_route53_record.validation[0].name'
  'module.wildcard_cert.aws_route53_record.validation[0].type'
)
# The break GAUNTLET.md asks stage 3 for is a corrupted expected string, and
# this one is chosen so that a grep which "always matches" is what it
# catches: module.alb.aws_lb_target_group.this is a resource in this very
# configuration, for_each'd over the very same var.target_groups as
# aws_lb_target_group_attachment.this, that does NOT refuse - because it is
# taggable and carries the tofu-address stage 2 wrote. Expecting it among the
# refusal sites must fail, and must fail on that string alone.
if [ "${BREAK:-}" = "1" ]; then
  WANT_SITES+=('module.alb.aws_lb_target_group.this["ex-instance"].name')
  log "  BREAK=1: also expecting a refusal on"
  log "           module.alb.aws_lb_target_group.this[\"ex-instance\"].name,"
  log "           which is real, is in this configuration, shares"
  log "           var.target_groups with the attachment that DOES refuse, and"
  log "           is nonetheless silent. Wrong. This step must fail."
fi

# By name first, so BREAK=1 fails on the string it corrupted and not on a
# count it did not touch.
for site in "${WANT_SITES[@]}"; do
  grep -qF "$site" <<< "$PLAN_OUT" \
    || { printf '%s\n' "$PLAN_OUT"; fail "expected $site among the stage-3 refusal sites"; }
done

DIAG_N="$(grep -c '^Error:' <<< "$PLAN_OUT")"
[ "$DIAG_N" = "$WANT_DIAG_N" ] \
  || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected $WANT_DIAG_N Error diagnostics, got $DIAG_N"; }

# The summaries, by count. A shift between buckets at the same total would
# otherwise pass the check above.
while read -r want summary; do
  got="$(grep -c "^Error: $summary\$" <<< "$PLAN_OUT")"
  [ "$got" = "$want" ] || fail "expected $want \"$summary\" diagnostics, got $got"
done <<'SUMMARIES'
7 Dynamic value in static context
7 Unable to compute static value
4 Module output not supported in static context
1 Non-static identity argument
1 Identity not resolvable from configuration
SUMMARIES

# The asymmetry that says this is the untaggable family and not a
# type-coverage gap: aws_lb_target_group.this for_eaches over exactly the
# same var.target_groups as aws_lb_target_group_attachment.this, and is
# silent, because it is taggable and carries the marker stage 2 wrote. If
# that ever starts refusing, the diagnosis in this script's header is wrong.
grep -qF 'in resource "aws_lb_target_group" "this"' <<< "$PLAN_OUT" \
  && fail "aws_lb_target_group.this now refuses too - it shares var.target_groups with the attachment but carries a marker, so this script's untaggable-family diagnosis needs redoing"
log "  3b  $DIAG_N diagnostics; every resource they block is untaggable. The"
log "      taggable aws_lb_target_group.this over the same var.target_groups"
log "      is silent, because it carries the marker stage 2 wrote"

log ""
log "STAGE 3 (test_plan): BLOCKED for real - $DIAG_N config-language-subset"
log "diagnostics on untaggable resources (families A and B, see header). The"
log "markerless-type wall that used to be the only thing this estate could"
log "print is gone, and these were behind it."
log ""
gauntlet_stage test_plan fail "the markerless-type wall is GONE: identity.LocatedType's credential veto was narrowed to the recorded identity (commit 80666bc1c0, #365 population 2) and aws_cognito_user_pool_client's identity is user_pool_id/id, which never touched client_secret; 0 markerless-type refusals and the type's name does not appear in the output at all. lint.CheckWith returns on the first error-severity issue, so that one refusal was masking everything else: measured against the same migrated estate and the same live floci, the pre-fix binary printed 1 diagnostic and the fixed one prints $DIAG_N. All $DIAG_N are HANDOFF's first row (choudoufu refuses where stock proceeds) and every resource they BLOCK is UNTAGGABLE (six of the twenty are reported at the module call rather than at a resource, and name the module input variable they poison). Family A, 12: a resource or module-output reference nested inside a module input's object/list literal (aws_instance.this.id and module.lambda_*.lambda_function_arn in var.target_groups, aws_instance.other.id in var.additional_target_group_attachments, module.wildcard_cert.acm_certificate_arn in var.listeners) makes the whole variable unavailable inside module.alb, collapsing aws_lb_target_group_attachment.this's for_each, local.lambda_target_groups and aws_lambda_permission.this's for_each, aws_lb_target_group_attachment.additional's target_group_arn and port, and aws_lb_listener_certificate.this's certificate_arn - the map keys are all static and every poisoned leaf is an identity attribute of a resource stage 2 stamped, so this is the module-call variable path in internal/configs, the same surface #375 names for the module-OUTPUT side. Family B, 8: aws_acm_certificate.this[0].domain_validation_options is minted by ACM and drives aws_route53_record.validation[0]'s name and type in both certificate module instances; no evaluator can produce it, so the answer is the record rung (#364) and live-import's SKIP-every-untaggable behaviour is what has to change. Neither attempted here; each is its own unit"
log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
gauntlet_stage test_apply not_run "depends on stage 3, which does not produce a clean plan"
log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="
gauntlet_stage drift_reconverge not_run "depends on stages 3-4"
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY (partial pass, reported honestly) ==="
log ""
log "  stage 1  cold_deploy        PASS ($INSTANCES resources, once for real - see"
log "                              header for the 3 floci fixes this needed:"
log "                              #58, #61, #62)"
log "  stage 2  migrate            PASS (real: $STAMPED_WANT of $INSTANCES stamped, $IMPORT_FAILED_WANT failed -"
log "                              floci#65 is fixed in the pinned image and its"
log "                              4 sites now stamp, see header)"
log "  stage 3  test_plan          BLOCKED - $DIAG_N config-language-subset diagnostics"
log "                              on untaggable resources. #309's markerless-type"
log "                              site is GONE; these were behind it. See header"
log "                              for families A and B."
log "  stage 4  test_apply         NOT RUN"
log "  stage 5  drift_reconverge   NOT RUN"
log ""
log "$INSTANCES real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's or live-plan's own"
log "output, or a tag read straight through the AWS CLI - never choudoufu's"
log "own self-report. Run again with BREAK=1: stages 1 and 2 still pass and"
log "stage 3's site-count assertions are the ones that fail."
