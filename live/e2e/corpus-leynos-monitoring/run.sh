#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-leynos-monitoring recipe; run with: just demo-run corpus-leynos-monitoring)
# The ninth OpenTofu-native crossing: leynos/df12-www's modules/monitoring -
# a real, actively-maintained personal-site deployment (34 .tofu files to 1
# .tf, README opens "using OpenTofu" with no compatibility claim). cold
# deploy passes for real (the two aws_cloudwatch_metric_alarm resources plus
# aws_cloudwatch_dashboard, aws_budgets_budget targeted out - floci does not
# implement AWS Budgets); migrate fails for real on a confirmed floci bug,
# not a choudoufu one (CloudWatch's TagResource returns a bare `{}` body the
# AWS Go SDK cannot parse - stock OpenTofu hits the identical error with no
# choudoufu involved at all, filed as lex00/floci#88). Needs Docker, the AWS
# CLI, and the real `tofu` binary; runs on its own port (4732).
set -uo pipefail

# The five-stage real-estate crossing (live/corpus-crossing-manifest.json)
# for leynos/df12-www (live/corpus-manifest.json, pinned by commit alone -
# no tags published - e59eabba112b2a4c731123f26845a20f0ae0d938), the
# modules/monitoring module - a real, actively-maintained personal-site
# deployment (241 commits as of the pin, last push the same day, AGPL-3.0,
# dependabot-active) whose README opens "This repository contains the
# configuration for my static website deployment using OpenTofu" with no
# compatibility claim, and whose pinned tree is 34 .tofu files to exactly 1
# .tf file (a Terraform-based test fixture under modules/static_site/tests,
# outside the module this script crosses).
#
# WHY modules/monitoring. It is the one leaf module in df12-www's tree that
# needs nothing else stood up first: no remote state, no live S3 bucket or
# CloudFront distribution actually created (bucket_name/distribution_id are
# free-form strings the two alarms and the dashboard reference as metric
# dimensions, never looked up), no data source. A single-service CloudWatch/
# Budgets shape, disjoint from the five S3/storage/IAM/networking-shaped
# opentofu-native estates already crossed (corpus-hongbomiao-*, corpus-
# sumaform-aws, corpus-giantswarm-crossplane, corpus-evoteum-modules,
# corpus-xancloud-iac, corpus-overture-tiles).
#
# THE SCOPING DECISION: aws_budgets_budget is excluded from every stage.
# Confirmed directly against this checkout's pinned floci image
# (live/floci-image) that AWS Budgets is not implemented: absent from
# live/floci-capabilities.json's service list for this exact image digest,
# absent from the running container's own /_localstack/health service map
# (the CloudWatch-family key there is "monitoring", not "cloudwatch" or
# "budgets" - LocalStack's own naming, not floci's or choudoufu's), and a
# direct `aws budgets create-budget` call against it returns
# "UnknownOperationException: Unknown operation: AWSBudgetServiceGateway.
# CreateBudget". aws_cloudwatch_metric_alarm and aws_cloudwatch_dashboard
# were independently confirmed working by direct probe against the same
# image (put/describe-alarm, put/get-dashboard, tag-resource/list-tags-for-
# resource on the alarm all succeed) - the "unimplemented" status floci-
# capabilities.json records for both under its own "cloudcontrol-list"
# mechanism is a different code path (floci's generic AWS::CloudFormation-
# schema Cloud Control passthrough, used by tools/floci-capability-gen's own
# sweep and by choudoufu's stateless orphan-discovery scan) from the native
# CloudWatch service API the AWS provider's own hand-written Read/Create
# functions actually call for these two resource types, and this crossing
# never invokes stateless discovery - every stage below drives choudoufu
# from an explicit state file or a marker, never a Cloud-Control-backed
# scan. This is a real floci gap for aws_budgets_budget alone, not a
# choudoufu one and not this script's to route around: excluded with
# `-target`, applied identically to every choudoufu/tofu command that plans
# or applies the estate's config (cold deploy, both live-plans, both
# applies), so the resource is never created, never appears in state, and
# never shows up as an unaddressed diff line. The module's own main.tofu is
# never edited to remove it - DELTA discipline below confirms byte-
# identical.
#
# UPDATE 2026-08-21 (a): `-target` scoped cold deploy and the final core
# plan fine, but did NOT scope choudoufu's stateless live-plan identity
# resolution/discovery/stamping passes at all: args.Operation.Targets was
# threaded only into the final tfCtx.Plan() call, after statelessResolve/
# statelessDataReads/statelessDiscover/statelessStamp had already walked the
# entire configs.Config. Those passes hit aws_budgets_budget's own identity
# requirement (account_id has no value anywhere in this module, by design -
# AWS defaults it to the caller's account) and refused hard with "Identity
# argument not set", even though the resource was never targeted. Confirmed
# NOT a floci or choudoufu-admission question at the time: a plain
# `tofu plan -target=<the same 3 resources>` against the identical
# unmodified module succeeded cleanly (Plan: 3 to add) - stock prunes the
# untargeted resource out of the graph before anything ever evaluates it.
# HANDOFF.md label 2, "OpenTofu succeeds, choudoufu refuses". Filed as
# https://github.com/INTENTIUS/choudoufu/issues/352 and FIXED there: the
# stateless pipeline now reads which resource blocks survive -target /
# -exclude off the plan graph's own TargetingTransformer
# (tofu.Context.TargetedResources) and hands that scope to resolution and
# the data-read phase, so a block the graph dropped can no longer refuse the
# run. Re-crossed for real after the fix: stage 3 gets past this wall
# entirely - live-plan exits 0, resolves, discovers, stamps and renders a
# plan over the targeted set.
#
# UPDATE 2026-08-21 (b): stage 3 still does not come back EMPTY, on a wall
# one layer further in and a different label entirely. Both alarms render
# `- datapoints_to_alarm = 1 -> null`, an argument this module's config
# never sets. Stock reproduces it identically: `tofu plan` in the stage-1
# plain directory, against the same live objects with no choudoufu in the
# process, proposes the same line for the same two alarms, and an ISOLATED
# minimal reproduction (one alarm, one directory, stock OpenTofu v1.12.5 +
# hashicorp/aws v5.100.0 against this same floci pin) does not converge
# either: apply writes datapoints_to_alarm = 1 into state, floci's
# DescribeAlarms returns no DatapointsToAlarm key at all, and every later
# plan wants to remove it. hashicorp/aws 6.x converges cleanly on the same
# image, so it is specific to the 5.x line this module's own `~> 5.0`
# constraint resolves to. HANDOFF.md label 1, "OpenTofu fails here too" -
# PARITY, an emulator gap and not this fork's defect. Filed as
# https://github.com/lex00/floci/issues/93.
#
# UPDATE 2026-08-21 (c): lex00/floci#93's own investigation named the
# workaround directly - "have modules/monitoring set datapoints_to_alarm
# explicitly so the non-computed-optional gap never comes into play" - and
# it is applied below as a DELTA (apply_datapoints_delta), setting
# datapoints_to_alarm to the same value evaluation_periods already carries
# on both alarms (AWS's own create-time default when the argument is
# omitted, so nothing about what stage 1 creates changes). Verified for
# real, not assumed: with the DELTA applied to both the PLAIN and ESTATE
# copies, `tofu plan`/`choudoufu live-plan` against the same live alarms no
# longer proposes the "1 -> null" line at all - the config value and the
# refreshed read agree, because there is no non-computed gap left for them
# to disagree through.
#
# Getting there exposed a second, unrelated, real choudoufu bug on the way:
# `choudoufu live-plan` against a configuration with a `live` block delegates
# to plain `choudoufu plan`, handing it the CLI arguments "exactly as they
# arrived" (originalArgs) so it can parse them itself. originalArgs was only
# a second slice header over live-plan's own rawArgs backing array, not an
# independent copy - and arguments.ParseView compacts recognized flags (like
# -no-color) out of its slice IN PLACE. With any flag after -no-color, -target
# being the realistic one since every -target/-exclude run goes through this
# same alias, the compaction silently overwrote -no-color's slot in the
# SHARED backing array, so the delegate lost -no-color and, for exactly one
# -target, saw the last target flag duplicated into the slot -no-color used
# to occupy. The result: this crossing's own -no-color flag never reached the
# delegate, and stage 3's "No changes." string never matched a live-plan
# render riddled with real ANSI escape codes - not a semantics bug (a manual
# rerun confirmed the plan was already genuinely empty), but the automated
# assertion itself was silently unable to see it, and would have stayed that
# way for every other -target-using estate's stage 3/5 checks too. Fixed in
# internal/command/live_plan.go by making originalArgs an explicit copy
# (`append([]string(nil), rawArgs...)`); regression-tested in
# internal/command/live_mode_test.go
# (TestStatelessMode_livePlanIsAnAlias/delegates_with_-target), confirmed to
# fail without the fix (raw ANSI escapes reach the alias's output) and pass
# with it.
#
# Stage 5's own drift simulation then hit a third, distinct, real bug - this
# one floci's, not choudoufu's or this harness's mistake. CloudWatch's
# PutMetricAlarm is documented (the AWS CLI's own bundled help,
# `aws cloudwatch put-metric-alarm help`, the --tags entry) as create-only
# for tags: "If you are using this operation to update an existing alarm,
# any tags you specify in this parameter are ignored. To change the tags of
# an existing alarm, use TagResource or UntagResource." So a real
# out-of-band PutMetricAlarm call - exactly what drift_s3_alarm/
# drift_cf_alarm below simulate - can never touch an existing alarm's tags
# on real AWS. Confirmed directly against this checkout's floci pin: the
# identical call WIPES the alarm's existing tags (list-tags-for-resource
# went from the two markers stage 2 wrote to an empty list), destroying the
# ownership marker this crossing depends on. Filed as
# https://github.com/lex00/floci/issues/95. Worked around in this harness
# (not floci, and not by changing what stage 5 tests) by re-applying the
# known tags via TagResource immediately after each drift call
# (retag_after_drift, stage 5's own code) - restoring what a real
# out-of-band mutation would never have disturbed in the first place, so
# the rest of the stage still exercises real reconvergence.
#
# Re-crossed for real with all three fixes/workarounds in place: all five
# stages pass. STAGE 3 (test plan): PASS for real - genuinely empty plan,
# both alarms' tofu-address markers re-verified unchanged, the dashboard's
# re-derived identity confirmed against its own live content. STAGE 4 (test
# apply): PASS - a genuine no-op, 2 objects before and after, no state file
# either time. STAGE 5 (drift and reconverge): PASS - the drift plan
# proposes fixing exactly the one drifted alarm and nothing else, the
# reconverge apply changes exactly that one resource, and the live value
# reads back correct afterward. leynos/df12-www's modules/monitoring is a
# ninth OpenTofu-native estate at full, real, five-stage parity.
#
# THE OTHER SCOPING DECISION, avoided rather than made: modules/monitoring/
# terraform.tofu pins `aws ~> 5.0`, real and unedited, incompatible with the
# `= 6.59.0` every other opentofu-native crossing in this repo pins at its
# OWN root for reproducibility against the schemas this checkout's admission
# tables were generated from. Rather than edit the module's own version
# constraint (a real, precedented DELTA elsewhere - see corpus-cncf-k8s-
# infra-aws-capa-ami's DELTA 3), this script's root declares no
# required_providers entry for aws at all, leaving the module's own real
# `~> 5.0` as the sole governing constraint (resolves to the newest 5.x
# release the provider mirror serves). Zero edits to the crossed module,
# any module. aws_cloudwatch_metric_alarm and aws_cloudwatch_dashboard are
# old, stable, hand-written (non-Cloud-Control) resources in the AWS
# provider - their schemas (alarm_name, dashboard_name, dimensions,
# dashboard_body) have not moved across the 5.x/6.x boundary - and the
# actual identity/plan/apply behavior below is the evidence for whether that
# assumption holds, not a claim taken on faith.
#
# STAGES:
#   1. COLD DEPLOY   plain `tofu apply` (real OpenTofu core, no choudoufu),
#                     the unmodified module, targeted to the two alarms and
#                     the dashboard only (see scoping decision above) - the
#                     honest proof the module code is real and buildable,
#                     and the source of genuinely unmarked live infra for
#                     stage 2.
#   2. MIGRATE        `choudoufu live-import -approve` against that cold
#                     state; markers re-read through the AWS CLI directly.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`
#                     (same -target set), assert the plan is EMPTY *and*
#                     assert the rendered identities by value - the two
#                     alarms' tofu-address via their own tags, the
#                     dashboard's re-derived identity via its own live
#                     content (it carries no marker - no `tags` argument in
#                     the provider's schema, confirmed at stage 2).
#   4. TEST APPLY     apply the empty plan (same -target set); assert a
#                     genuine no-op by the estate's tagged-object count
#                     before and after.
#   5. DRIFT AND      mutate one alarm's alarm_description out of band via
#      RECONVERGE     the AWS CLI directly against floci (a real config-
#                     owned resource ARGUMENT, not a tag - neither alarm
#                     declares a `tags` argument of its own, see stage 5's
#                     own comment), replan, assert the diff proposes fixing
#                     exactly that one object, apply, and confirm the live
#                     value is back to what the config says.
#
# BREAK=1 corrupts one expected tofu-address ahead of stage 2's assertion
# and tampers BOTH alarms' alarm_description (rather than one) ahead of
# stage 5's, so both assertions are proven load-bearing rather than a grep
# that always matches - the same discipline corpus-hongbomiao-storage's own
# header documents, adapted to this estate having only two taggable objects
# instead of three and no config-declared tags to drift.
#
# day2_rename's own BREAK=1 arm (D1, below - rename without a moved block)
# is written for the same reason but is never reached by an ordinary
# BREAK=1 run: stage 2's own control fires first and `fail` exits there, the
# same limitation corpus-eks-basic's header documents for its own day2_rename
# ("BREAK=1 mutates BOTH stages ... a BREAK=1 run proves stage 2's control
# and leaves stage 3's unexercised"). Verified load-bearing directly instead:
# with stage 2's and stage 5's BREAK checks temporarily neutered in a scratch
# copy, BREAK=1 correctly proposed destroying aws_cloudwatch_metric_alarm.
# cf_requests_spike and creating .cf_requests_spike_renamed - the real tofu
# run this script's own D1 assertions are written to catch.
#
#   bash live/e2e/corpus-leynos-monitoring/run.sh
#
# Needs Docker, the AWS CLI, and the real `tofu` binary on PATH for stage 1
# (stock terraform cannot load a directory of .tofu files at all).
#
# Env overrides:
#   TOFU_BIN      path to a prebuilt choudoufu binary; skips the go build.
#   FLOCI_PORT    host port for the emulator (default 4732, clear of every
#                 other corpus-*/reference-* script's own default).
#   FLOCI_IMAGE   the emulator image; defaults to the digest pin in
#                 live/floci-image.
#   BREAK         set to 1 to corrupt stage 2's identity assertion and
#                 tamper both alarms ahead of stage 5's, proving both are
#                 load-bearing.
#   BREAK_REMOVE  set to 1 to exercise day2_remove's own break control (PART
#                 E, far below): keep the CloudFront alarm's block and
#                 assert no destroy is proposed. Only reachable when BREAK
#                 is not 1, because day2_rename's own control fires first.
#   BREAK_COUNT   set to 1 to exercise day2_count's own break control (PART
#                 G, far below): after the real scale-down plan, assert the
#                 WRONG instance (count_test[0] rather than count_test[1])
#                 was destroyed - the assertion must fail. Only reachable
#                 when BREAK is not 1 and BREAK_REMOVE is not 1, because
#                 PART G starts from PART E's real, completed removal.
#   DEBUG_KEEP    set to 1 to skip the exit trap: the floci container and
#                 the WORK directory are left behind for inspection.
#
# ISSUE #488 / #359 (day2_count). modules/monitoring declares no `count` or
# `for_each` anywhere - every resource (the excluded budget, both alarms,
# the dashboard) is a single hardcoded block with no countable knob of its
# own, confirmed by reading modules/monitoring/main.tofu and dashboard.tofu
# in full: no `count`, no `for_each`, no module-level scaling variable. So
# the PREFERRED shape (a real, already-live count/for_each knob scaled
# through the module's own documented variable, corpus-xancloud-iac's own
# Part F/F-ORACLE) is not honest here - there is no such knob to scale. This
# follows live/e2e/corpus-iam-read-only-policy/run.sh's own PART G/G-ORACLE
# precedent instead (merged as #500, issue #488's own fallback clause): a
# synthetic aws_cloudwatch_metric_alarm.count_test resource, added and
# removed entirely within PART G-ORACLE and PART G below, nothing else in
# this estate ever names it. CloudWatch, not IAM, because this is a
# monitoring estate and the two real alarms already prove the type works
# against this floci pin - reusing it means day2_count exercises the same
# resource type the rest of this crossing already trusts rather than
# introducing a new one purely for this stage.
#
# THE IDENTITY SHAPE, established directly against floci before writing any
# assertion (HANDOFF.md's "identity semantics vary by type" rule) - no tofu
# in the loop: put-metric-alarm twice, describe-alarms, delete-alarms,
# describe-alarms again, put-metric-alarm again, describe-alarms again.
# aws_cloudwatch_metric_alarm has NO server-minted id anywhere in
# DescribeAlarms' response - AlarmArn is
# arn:aws:cloudwatch:<region>:<account>:alarm:<name>, fully determined by
# account, region and name alone (identical both before and after a real
# delete-then-recreate cycle - verified: same ARN both times). This is the
# Route53 shape HANDOFF.md's trap table names, not the IAM-policy shape
# corpus-iam-read-only-policy's own G-ORACLE uses (PolicyId): there is no
# second field to compare. So "genuinely destroyed and recreated" here can
# only be shown by ABSENCE then re-appearance - describe-alarms returning an
# empty MetricAlarms list is the proof of the destroy, not an id changing -
# the same technique this estate's own day2_remove and day2_replace stages
# already use for the identical reason (aws_cloudwatch_metric_alarm's ARN
# is name-derived there too).

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC="$ROOT/.corpus/leynos-df12-www/modules/monitoring"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
ESTATE="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4732}"
FLOCI_NAME="choudoufu-corpus-leynos-monitoring-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

# Two more, fresh containers for the greenfield stage (live/GAUNTLET.md #13):
# one namespace choudoufu applies into directly with no migration, and a
# SEPARATE namespace stock applies the identical config into as that stage's
# own oracle.
FLOCI_GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_GREEN_NAME="choudoufu-corpus-leynos-monitoring-green-$$"
FLOCI_ORACLE_PORT=$((FLOCI_PORT + 2))
FLOCI_ORACLE_NAME="choudoufu-corpus-leynos-monitoring-green-oracle-$$"
GREEN_ENDPOINT="http://127.0.0.1:${FLOCI_GREEN_PORT}"
ORACLE_ENDPOINT="http://127.0.0.1:${FLOCI_ORACLE_PORT}"

REGION="us-west-2"
ESTATE_NAME="leynos-monitoring-crossing"
GREEN_ESTATE_NAME="leynos-monitoring-greenfield"
DOMAIN_NAME="leynos-monitoring-crossing.example.com"
BUCKET_NAME="leynos-monitoring-crossing-site"
DISTRIBUTION_ID="E1EYNOSMONITOR"
ENVIRONMENT="crossing"
BUDGET_LIMIT_GBP="5"
BUDGET_EMAIL="budget-alerts@example.com"
DASHBOARD_NAME="${DOMAIN_NAME//./-}-${ENVIRONMENT}-visitors"
S3_ALARM_NAME="S3GetRequestsSpike"
CF_ALARM_NAME="CFRequestsSpike"

TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike
         -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike
         -target=module.monitoring.aws_cloudwatch_dashboard.site)

# #339: TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE closes the gap a warm
# cache alone does not - without it, init in a directory with no
# .terraform.lock.hcl re-downloads the whole provider purely to compute
# checksums, even when the cache already holds that exact version (see
# live/e2e/README.md, "The shared plugin cache" for the measured numbers).
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"

cleanup() {
  docker rm -f "$FLOCI_NAME" "$FLOCI_GREEN_NAME" "$FLOCI_ORACLE_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
[ -n "${DEBUG_KEEP:-}" ] || trap cleanup EXIT

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
gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v tofu >/dev/null 2>&1 || fail "the real tofu binary is not on PATH - required for stage 1"
[ -f "$SRC/main.tofu" ] && [ -f "$SRC/dashboard.tofu" ] \
  || fail "$SRC is missing - fetch leynos/df12-www at the pin in live/corpus-manifest.json first"
log "  cold deploy binary: tofu ($(tofu version | head -1))"

for f in main.tofu dashboard.tofu variables.tofu outputs.tofu terraform.tofu; do
  [ -f "$SRC/$f" ] || fail "$SRC/$f is missing - the pinned monitoring module no longer ships this file"
done
[ -z "$(find "$SRC" -maxdepth 1 -name '*.tf' -print -quit)" ] \
  || fail "$SRC now contains a .tf file - the pinned monitoring module is no longer .tofu-only"
log "  monitoring module is .tofu-only: main.tofu dashboard.tofu variables.tofu outputs.tofu terraform.tofu"

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

# copy_module <destdir>: the real module, .tofu extension and all, with one
# DELTA applied below.
copy_module() { mkdir -p "$1/modules"; cp -R "$SRC" "$1/modules/monitoring"; }

# apply_datapoints_delta <destdir>: DELTA (WORKAROUND, confirmed real
# upstream hashicorp/aws provider defect - full wire-level evidence in
# lex00/floci#93's comment thread, HANDOFF.md label 1, "OpenTofu fails here
# too"). aws_cloudwatch_metric_alarm.datapoints_to_alarm is schema'd
# Optional without Computed in both v5.100.0 and v6.0.0 of the provider, so
# terraform-plugin-sdk/v2's plan always proposes the config value (null,
# since this module never sets the argument) over whatever the refreshed
# read returns - an eternal "1 -> null" diff against a real, unchanged
# alarm, reproducing identically against stock `tofu plan` with no
# choudoufu in the process and against real AWS, not just floci. Setting it
# explicitly to the same value evaluation_periods already carries (AWS's
# own create-time default when the argument is omitted, so this changes
# nothing about what stage 1 creates or what a user reading this module
# would expect) turns it into an ordinary non-computed field whose config
# value matches the server, and the diff cannot recur. Applied identically
# to BOTH copies of the module (PLAIN and ESTATE) so stage 1's cold deploy
# and stages 2-5's choudoufu runs agree on the same config throughout.
apply_datapoints_delta() {
  perl -0pi -e 's/(  evaluation_periods  = 1\n)/$1  datapoints_to_alarm = 1  # DELTA (lex00\/floci#93): explicit value sidesteps an upstream provider bug - see run.sh header\n/g' \
    "$1/modules/monitoring/main.tofu"
}

# write_root <destdir> <live_block>: this crossing's own root wiring. The
# module call below uses the module's OWN documented variable names and
# nothing else. No required_providers entry for aws - see the header's
# second scoping decision; the module's own `~> 5.0` (unedited) is the sole
# governing constraint.
write_root() {
  local dest="$1" live_block="$2"
  cat > "$dest/main.tofu" <<EOF
terraform {
  required_version = ">= 1.6.0"
$live_block
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
}

module "monitoring" {
  source           = "./modules/monitoring"
  domain_name      = "$DOMAIN_NAME"
  bucket_name      = "$BUCKET_NAME"
  distribution_id  = "$DISTRIBUTION_ID"
  budget_limit_gbp = $BUDGET_LIMIT_GBP
  budget_email     = "$BUDGET_EMAIL"
  environment      = "$ENVIRONMENT"
}
EOF
}

copy_module "$PLAIN"
apply_datapoints_delta "$PLAIN"
write_root "$PLAIN" ""
log "  monitoring module copied out of .corpus/leynos-df12-www into $PLAIN, one DELTA applied (datapoints_to_alarm, lex00/floci#93 - see run.sh header)"

# DELTA: confirm the copy matches the pinned commit exactly except for the
# one documented datapoints_to_alarm line added to each alarm above - never
# any other edit anywhere in the module (aws_budgets_budget included - it
# is scoped out by -target, not by editing main.tofu).
[ "$(grep -c 'DELTA (lex00/floci#93)' "$PLAIN/modules/monitoring/main.tofu")" = "2" ] \
  || fail "the datapoints_to_alarm DELTA did not apply to exactly 2 lines - the corpus pin has moved"
diff -q "$SRC/main.tofu" <(grep -v 'DELTA (lex00/floci#93)' "$PLAIN/modules/monitoring/main.tofu") >/dev/null \
  || fail "modules/monitoring/main.tofu differs from the pinned commit beyond the datapoints_to_alarm DELTA - the corpus pin has moved"
diff -rq "$SRC" "$PLAIN/modules/monitoring" -x main.tofu >/dev/null \
  || fail "modules/monitoring differs from the pinned commit outside main.tofu - this crossing must run the real, unmodified module everywhere else"
log "  DELTA confirmed: modules/monitoring matches the pinned commit exactly, apart from the two documented datapoints_to_alarm lines"

copy_module "$ESTATE"
apply_datapoints_delta "$ESTATE"
write_root "$ESTATE" '
  live {
    estate = "'"$ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'
log "  estate copy written to $ESTATE (stages 2-5: choudoufu, live block added, same DELTA applied)"

# ── 1. floci ─────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"monitoring"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"monitoring"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (monitoring, LocalStack's own key for CloudWatch) at $ENDPOINT"
log "  healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain tofu apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage cold_deploy
log "=== STAGE 1: cold deploy (plain tofu apply, the real unmodified module, targeted - see header) ==="
( cd "$PLAIN" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && tofu apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | tail -40; fail "stage 1 (cold deploy) failed"; }
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed' <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly 3 resources (the two alarms and the dashboard - aws_budgets_budget is targeted out)"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

UNMARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$UNMARKED" = "0" ] || fail "plain tofu's own objects already carry tofu-estate=$ESTATE_NAME before migration - this crossing proves nothing"
log "  confirmed unmarked: 0 objects carry tofu-estate=$ESTATE_NAME before migration"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "3 resources added (2 alarms + dashboard), 0 objects carry tofu-estate=$ESTATE_NAME before migration"

# ══════════════════════════════════════════════════════════════════════════
# PART GREENFIELD (greenfield, active - live/GAUNTLET.md #13)
# ══════════════════════════════════════════════════════════════════════════
#
# A SEPARATE fresh namespace from everything above: greenfield means from
# nothing, so this never touches stage 1's plain-tofu objects (those get
# migrated in stage 2, below). choudoufu applies the identical module
# (copy_module + the one datapoints_to_alarm DELTA, same as every other
# copy in this script) directly, with a live block from the start, no
# migration, no state file ever existing; and the estate's own oracle is
# stock applying the SAME config fresh in a THIRD, independent namespace,
# compared structurally via the AWS CLI on both endpoints, never through
# tofu state. aws_budgets_budget is targeted out here too, the same as
# every other apply in this script - see the header's scoping decision.
gauntlet_begin_stage greenfield
log "=== PART GREENFIELD: 0. two more floci containers, one per fresh namespace ==="
docker run -d --rm -p "${FLOCI_GREEN_PORT}:4566" --name "$FLOCI_GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_GREEN_NAME failed"
docker run -d --rm -p "${FLOCI_ORACLE_PORT}:4566" --name "$FLOCI_ORACLE_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_ORACLE_NAME failed"
for gep in "$GREEN_ENDPOINT" "$ORACLE_ENDPOINT"; do
  GH=""
  for _ in $(seq 1 45); do
    GH="$(curl -fs "${gep}/_localstack/health" 2>/dev/null)" || true
    grep -q '"monitoring"' <<< "${GH:-}" && break
    sleep 2
  done
  grep -q '"monitoring"' <<< "${GH:-}" || fail "floci did not come up healthy (monitoring) at $gep"
done
log "  healthy: greenfield=$GREEN_ENDPOINT oracle=$ORACLE_ENDPOINT"

GREEN="$WORK/green"
copy_module "$GREEN"
apply_datapoints_delta "$GREEN"
write_root "$GREEN" '
  live {
    estate = "'"$GREEN_ESTATE_NAME"'"
    record_store "local" {
      path = ".tofu-records"
    }
  }'

log "=== PART GREENFIELD: 1. choudoufu apply from nothing, no migration, no state file ever existing ==="
( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield init failed"; }
GREEN_APPLY_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY_OUT" | tail -40; fail "the greenfield apply failed"; }
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed' <<< "$GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT"; fail "the greenfield apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY_OUT")"

awsg() { aws --endpoint-url "$GREEN_ENDPOINT" --region "$REGION" "$@"; }

log "=== PART GREENFIELD: 2. markers, read through the AWS CLI directly ==="
GREEN_S3_ARN="$(awsg cloudwatch describe-alarms --alarm-names "$S3_ALARM_NAME" --query 'MetricAlarms[0].AlarmArn' --output text)"
[ -n "$GREEN_S3_ARN" ] && [ "$GREEN_S3_ARN" != "None" ] || fail "the greenfield S3-requests alarm does not exist"
GREEN_S3_ADDR="$(awsg cloudwatch list-tags-for-resource --resource-arn "$GREEN_S3_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_S3_ADDR" = "module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike" ] || fail "the greenfield S3-requests alarm carries tofu-address=$GREEN_S3_ADDR, not module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike"
GREEN_CF_ARN="$(awsg cloudwatch describe-alarms --alarm-names "$CF_ALARM_NAME" --query 'MetricAlarms[0].AlarmArn' --output text)"
[ -n "$GREEN_CF_ARN" ] && [ "$GREEN_CF_ARN" != "None" ] || fail "the greenfield CloudFront-requests alarm does not exist"
GREEN_CF_ADDR="$(awsg cloudwatch list-tags-for-resource --resource-arn "$GREEN_CF_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GREEN_CF_ADDR" = "module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike" ] || fail "the greenfield CloudFront-requests alarm carries tofu-address=$GREEN_CF_ADDR, not module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike"
GREEN_S3_EST="$(awsg cloudwatch list-tags-for-resource --resource-arn "$GREEN_S3_ARN" --query "Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GREEN_S3_EST" = "$GREEN_ESTATE_NAME" ] || fail "the greenfield S3-requests alarm carries tofu-estate=$GREEN_S3_EST, not $GREEN_ESTATE_NAME"
log "  both alarms carry their expected tofu-address markers and tofu-estate=$GREEN_S3_EST - read via the AWS CLI, not choudoufu's own report"

log "=== PART GREENFIELD: 3. the record store holds every instance, including the untaggable dashboard (#364 A2) ==="
GREEN_RECORD_FILES="$(gauntlet_record_count "$GREEN/.tofu-records/tofu-records")"
[ "$GREEN_RECORD_FILES" = "3" ] || fail "expected 3 records under the local record store after the greenfield apply (2 alarms + the untaggable dashboard), found $GREEN_RECORD_FILES"
log "  3 records persisted, one per managed instance, read directly off the local record store"

log "=== PART GREENFIELD: 4. the next plan proposes nothing ==="
GREEN_PLAN_OUT="$(cd "$GREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color "${TARGETS[@]}" 2>&1)"; GREEN_PLAN_RC=$?
[ "$GREEN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$GREEN_PLAN_OUT" | tail -30; fail "the greenfield replan exited $GREEN_PLAN_RC"; }
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$GREEN_PLAN_OUT" \
  || { grep -E '^  #' <<< "$GREEN_PLAN_OUT"; fail "the greenfield replan is not empty"; }
log "  No changes."

log "=== PART GREENFIELD: 5. stock oracle - the identical config applied fresh in its own namespace ==="
ORACLE_GREEN="$WORK/green-oracle"
copy_module "$ORACLE_GREEN"
apply_datapoints_delta "$ORACLE_GREEN"
write_root "$ORACLE_GREEN" ""
( cd "$ORACLE_GREEN" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_GREEN" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the greenfield oracle's init failed"; }
ORACLE_GREEN_APPLY_OUT="$(cd "$ORACLE_GREEN" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)" || {
  printf '%s\n' "$ORACLE_GREEN_APPLY_OUT" | tail -40; fail "the greenfield oracle apply failed"; }
grep -qE 'Apply complete! Resources: 3 added, 0 changed, 0 destroyed' <<< "$ORACLE_GREEN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_GREEN_APPLY_OUT"; fail "the greenfield oracle apply did not create exactly 3 resources"; }
log "  $(grep -E 'Apply complete' <<< "$ORACLE_GREEN_APPLY_OUT")"

alarm_shape() { # $1=endpoint $2=alarm-name - a normalised structural fact
                 # sheet, read via the AWS CLI, never through tofu state.
  aws --endpoint-url "$1" --region "$REGION" cloudwatch describe-alarms --alarm-names "$2" \
    --query 'MetricAlarms[0].[MetricName,Namespace,Statistic,Period,EvaluationPeriods,Threshold,ComparisonOperator]' --output json 2>/dev/null
}

log "=== PART GREENFIELD: 6. object-by-object comparison, via the AWS CLI on both endpoints, tags normalised out ==="
GREEN_S3_SHAPE="$(alarm_shape "$GREEN_ENDPOINT" "$S3_ALARM_NAME")"
ORACLE_S3_SHAPE="$(alarm_shape "$ORACLE_ENDPOINT" "$S3_ALARM_NAME")"
if [ "${BREAK:-}" = "2" ]; then
  GREEN_S3_SHAPE='"tampered-by-BREAK"'
  log "  BREAK=2: tampered the expected greenfield S3 alarm shape - the comparison below must fail"
fi
[ "$GREEN_S3_SHAPE" = "$ORACLE_S3_SHAPE" ] || { printf 'greenfield: %s\noracle:     %s\n' "$GREEN_S3_SHAPE" "$ORACLE_S3_SHAPE"; fail "the greenfield S3-requests alarm differs structurally from the stock oracle"; }
GREEN_CF_SHAPE="$(alarm_shape "$GREEN_ENDPOINT" "$CF_ALARM_NAME")"
ORACLE_CF_SHAPE="$(alarm_shape "$ORACLE_ENDPOINT" "$CF_ALARM_NAME")"
[ "$GREEN_CF_SHAPE" = "$ORACLE_CF_SHAPE" ] || { printf 'greenfield: %s\noracle:     %s\n' "$GREEN_CF_SHAPE" "$ORACLE_CF_SHAPE"; fail "the greenfield CloudFront-requests alarm differs structurally from the stock oracle"; }
log "  both alarms match structurally (metric, namespace, statistic, period, evaluation periods, threshold, comparison operator) between choudoufu's greenfield apply and stock's cold deploy in its own namespace"
gauntlet_stage greenfield pass "3 resources from nothing (2 tagged alarms + the untaggable dashboard), both alarm markers verified via the AWS CLI, 3 records in the local record store (#364 A2), replan empty, stock oracle in its own namespace matches structurally on both alarms"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART D-ORACLE: RENAME, stock oracle (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
#
# The estate's two taggable roots (the dashboard is untaggable, see header):
# a `moved` block renames aws_cloudwatch_metric_alarm.s3_requests_spike, and
# "choudoufu live-mv" (below, after drift_reconverge) renames
# aws_cloudwatch_metric_alarm.cf_requests_spike with no moved block at all.
# Both live inside modules/monitoring's own source (the module CALL itself
# is not renamed), so the rename and its moved block are written into the
# COPIED module source (never $SRC, never the datapoints_to_alarm DELTA's
# own text). The stock oracle runs the same two renames, through moved
# blocks only, on a copy of cold_deploy's own state - before choudoufu or
# live-import ever touch these objects. BREAK=1 exercises this stage's own
# break control instead (see the real leg below): renaming
# cf_requests_spike WITHOUT a moved block, which must make choudoufu
# propose destroying the old address and creating the new one.
gauntlet_begin_stage day2_rename
log "=== D-ORACLE: stock tofu, the same two renames through moved blocks, on cold_deploy's own state ==="
PLAIN_ORACLE="$WORK/plain-oracle"
cp -r "$PLAIN" "$PLAIN_ORACLE"
sed -i.bak 's/resource "aws_cloudwatch_metric_alarm" "s3_requests_spike" {/resource "aws_cloudwatch_metric_alarm" "s3_requests_spike_renamed" {/' "$PLAIN_ORACLE/modules/monitoring/main.tofu"
sed -i.bak 's/resource "aws_cloudwatch_metric_alarm" "cf_requests_spike" {/resource "aws_cloudwatch_metric_alarm" "cf_requests_spike_renamed" {/' "$PLAIN_ORACLE/modules/monitoring/main.tofu"
rm -f "$PLAIN_ORACLE/modules/monitoring/main.tofu.bak"
cat >> "$PLAIN_ORACLE/modules/monitoring/main.tofu" <<'EOF'

moved {
  from = aws_cloudwatch_metric_alarm.s3_requests_spike
  to   = aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
}

moved {
  from = aws_cloudwatch_metric_alarm.cf_requests_spike
  to   = aws_cloudwatch_metric_alarm.cf_requests_spike_renamed
}
EOF
ORACLE_TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike
                -target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
                -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike
                -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed
                -target=module.monitoring.aws_cloudwatch_dashboard.site)
( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$PLAIN_ORACLE" && tofu plan -input=false -no-color "${ORACLE_TARGETS[@]}" 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"

# ══════════════════════════════════════════════════════════════════════════
# PART F-ORACLE: REPLACE, stock (day2_replace, active - live/GAUNTLET.md #9)
# ══════════════════════════════════════════════════════════════════════════
#
# "Stock's replace of the same resource leaves the same single object."
# aws_cloudwatch_metric_alarm.s3_requests_spike (this estate's other
# taggable root, untouched here) is forced to replace via its own
# `alarm_name` argument: CloudWatch has no RenameAlarm API - only
# PutMetricAlarm (upsert by name) and DeleteAlarms - so alarm_name is
# ForceNew in the provider's own schema, confirmed empirically below by
# the plan's own "must be replaced" annotation, not assumed. A fresh copy
# of cold_deploy's own state (same as D-ORACLE above), on the ORIGINAL
# resource name before the real script's own rename ever touches $ESTATE,
# with the same -target exclusion of aws_budgets_budget every command in
# this script uses (see header - a real, confirmed floci gap, not this
# script's to route around).
gauntlet_begin_stage day2_replace
log "=== F-ORACLE: stock tofu, force-replace s3_requests_spike via its ForceNew alarm_name argument, on cold_deploy's own state ==="
REPLACE_PLAIN_ORACLE="$WORK/replace-plain-oracle"
cp -r "$PLAIN" "$REPLACE_PLAIN_ORACLE"
sed -i.bak 's/alarm_name          = "S3GetRequestsSpike"/alarm_name          = "S3GetRequestsSpikeV2"/' "$REPLACE_PLAIN_ORACLE/modules/monitoring/main.tofu"
rm -f "$REPLACE_PLAIN_ORACLE/modules/monitoring/main.tofu.bak"
grep -q 'S3GetRequestsSpikeV2' "$REPLACE_PLAIN_ORACLE/modules/monitoring/main.tofu" \
  || fail "changing s3_requests_spike's alarm_name argument in the replace-oracle copy did not match - the corpus pin has moved"
REPLACE_ORACLE_TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike
                         -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike
                         -target=module.monitoring.aws_cloudwatch_dashboard.site)
( cd "$REPLACE_PLAIN_ORACLE" && tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$REPLACE_PLAIN_ORACLE" && tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_replace stock oracle's init failed"; }
REPLACE_ORACLE_PLAN_OUT="$(cd "$REPLACE_PLAIN_ORACLE" && tofu plan -input=false -no-color "${REPLACE_ORACLE_TARGETS[@]}" 2>&1)"; REPLACE_ORACLE_PLAN_RC=$?
[ "$REPLACE_ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "the day2_replace stock oracle plan exited $REPLACE_ORACLE_PLAN_RC"; }
grep -qE '^  # module\.monitoring\.aws_cloudwatch_metric_alarm\.s3_requests_spike must be replaced' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock does not propose replacing s3_requests_spike when its alarm_name argument changes"; }
grep -qE '~ +alarm_name +=.+forces replacement' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -40; fail "stock's plan does not mark alarm_name as forcing replacement - it may not be ForceNew after all"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$REPLACE_ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$REPLACE_ORACLE_PLAN_OUT" | tail -10; fail "stock's replace plan proposes something other than exactly one add and one destroy at the same address"; }
# PLAN ONLY, never applied - same convention as D-ORACLE above: this
# oracle's copy shares floci's account with $ESTATE, so actually applying
# here would destroy and recreate the real alarm $ESTATE's own later
# stages still depend on.
log "  stock: exactly one replace at the same declared address (module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike), plan only, never applied"

# ══════════════════════════════════════════════════════════════════════════
# PART G-ORACLE: CHANGE COUNT, stock (day2_count, active - live/GAUNTLET.md
# #8, issue #359/#488)
# ══════════════════════════════════════════════════════════════════════════
#
# modules/monitoring has no real count/for_each knob (see header, issue
# #488's fallback clause) - a synthetic aws_cloudwatch_metric_alarm.
# count_test resource, count_test_block() below, is added and removed
# entirely within this oracle and PART G (its real-leg counterpart, far
# below); nothing else in this estate ever names it. Applied for real,
# twice (2 -> 1 -> 2), in the SAME otherwise-idle account PART GREENFIELD's
# own oracle ($ORACLE_ENDPOINT) already finished with above and never
# touches again - a fresh alarm-name prefix collides with nothing that
# account already holds (its own two alarms are named S3GetRequestsSpike/
# CFRequestsSpike). Using this same idle, separate account - rather than
# $ENDPOINT, which the real leg still depends on - is deliberate: PR #502's
# oracle leftovers on a SHARED endpoint once poisoned the real leg's own
# lookup.
gauntlet_begin_stage day2_count
count_test_block() { # $1 = count
  local n="$1"
  cat <<COUNTEOF
resource "aws_cloudwatch_metric_alarm" "count_test" {
  count               = $n
  alarm_name          = "leynos-monitoring-count-test-\${count.index}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  period              = 3600
  statistic           = "Sum"
  threshold           = 100000
  metric_name         = "GetRequests"
  namespace           = "AWS/S3"
  treat_missing_data  = "ignore"
}
COUNTEOF
}
oracle_count_provider() {
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

provider "aws" {
  region = "$REGION"

  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
}

EOF
}

log "=== G-ORACLE: stock, create a 2-instance count block, scale it to 1 and back, in the (idle) greenfield-oracle account ==="
COUNT_ORACLE_DIR="$WORK/count-oracle"
mkdir -p "$COUNT_ORACLE_DIR"
{ oracle_count_provider; count_test_block 2; } > "$COUNT_ORACLE_DIR/main.tf"
( cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_count stock oracle's init failed"; }
ORACLE_COUNT_APPLY_OUT="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 2 added' <<< "$ORACLE_COUNT_APPLY_OUT" \
  || { printf '%s\n' "$ORACLE_COUNT_APPLY_OUT" | tail -30; fail "stock did not create exactly 2 count-test alarms for the day2_count oracle"; }
awso() { aws --endpoint-url "$ORACLE_ENDPOINT" --region "$REGION" "$@"; }
ORACLE_CT0_STILL="$(awso cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
ORACLE_CT1_STILL="$(awso cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
[ "$ORACLE_CT0_STILL" = "1" ] || fail "no oracle count_test[0] alarm found by name after the baseline apply"
[ "$ORACLE_CT1_STILL" = "1" ] || fail "no oracle count_test[1] alarm found by name after the baseline apply"
log "  stock: 2 instances created (leynos-monitoring-count-test-0, leynos-monitoring-count-test-1), confirmed present via the AWS CLI"

{ oracle_count_provider; count_test_block 1; } > "$COUNT_ORACLE_DIR/main.tf"
ORACLE_DOWN_PLAN_OUT="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_DOWN_PLAN_RC=$?
[ "$ORACLE_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-down plan exited $ORACLE_DOWN_PLAN_RC"; }
grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[1\] will be destroyed' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan does not destroy count_test[1]"; }
grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[0\] will be' <<< "$ORACLE_DOWN_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-down plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$ORACLE_DOWN_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_DOWN_PLAN_OUT" | tail -10; fail "stock's scale-down plan proposes something other than exactly one destroy"; }
ORACLE_DOWN_APPLY_OUT="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_DOWN_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$ORACLE_DOWN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_DOWN_APPLY_OUT"; fail "the day2_count stock oracle's scale-down apply was not exactly one destroy"; }
ORACLE_CT0_AFTER_DOWN="$(awso cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
[ "$ORACLE_CT0_AFTER_DOWN" = "1" ] || fail "stock's surviving count_test[0] no longer exists after the scale-down"
ORACLE_CT1_AFTER_DOWN="$(awso cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
[ "$ORACLE_CT1_AFTER_DOWN" = "0" ] || fail "stock's count_test[1] still exists after the scale-down destroy"
log "  stock: exactly one destroy (count_test[1], confirmed genuinely gone via describe-alarms - aws_cloudwatch_metric_alarm has no server-minted id, see header), count_test[0] unchanged"

{ oracle_count_provider; count_test_block 2; } > "$COUNT_ORACLE_DIR/main.tf"
ORACLE_UP_PLAN_OUT="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu plan -input=false -no-color 2>&1)"; ORACLE_UP_PLAN_RC=$?
[ "$ORACLE_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -30; fail "the day2_count stock oracle's scale-up plan exited $ORACLE_UP_PLAN_RC"; }
grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[1\] will be created' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan does not create count_test[1]"; }
grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[0\] will be' <<< "$ORACLE_UP_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock's scale-up plan touches count_test[0], which should be untouched"; }
grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_UP_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_UP_PLAN_OUT" | tail -10; fail "stock's scale-up plan proposes something other than exactly one create"; }
ORACLE_UP_APPLY_OUT="$(cd "$COUNT_ORACLE_DIR" && AWS_ENDPOINT_URL="$ORACLE_ENDPOINT" tofu apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$ORACLE_UP_APPLY_OUT" | tail -30; fail "the day2_count stock oracle's scale-up apply failed"; }
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$ORACLE_UP_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$ORACLE_UP_APPLY_OUT"; fail "the day2_count stock oracle's scale-up apply was not exactly one create"; }
ORACLE_CT1_AFTER_UP="$(awso cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
[ "$ORACLE_CT1_AFTER_UP" = "1" ] || fail "no oracle count_test[1] alarm found by name after the scale-up"
ORACLE_CT0_AFTER_UP="$(awso cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
[ "$ORACLE_CT0_AFTER_UP" = "1" ] || fail "stock's count_test[0] no longer exists after the scale-up"
log "  stock: exactly one create (count_test[1], recreated - confirmed via describe-alarms), count_test[0] unchanged throughout"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage migrate
log "=== STAGE 2: choudoufu live-import ==="
if [ -f "$PLAIN/.terraform.lock.hcl" ]; then
  cp "$PLAIN/.terraform.lock.hcl" "$ESTATE/.terraform.lock.hcl" \
    || fail "could not seed the estate's provider lock file from stage 1's"
  log "  seeded the estate's .terraform.lock.hcl from stage 1's init (checksums only; the estate still runs its own init below)"
fi
( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "estate init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "2 of 3 resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not verify exactly 2 of 3 as eligible (the two alarms - the dashboard is correctly UNTAGGABLE, see header)"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE (1)" <<< "$IMPORT_OUT" \
  || fail "expected exactly 1 UNTAGGABLE resource (the dashboard)"
log "  2 of 3 verified against the live system; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$ESTATE" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
grep -qF "2 resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not stamp exactly 2 of 3 resources cleanly"; }
log "  2 stamped"

log "--- 2c: the markers, read through the AWS CLI directly - never through choudoufu ---"
WANT_S3_ADDR="module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike"
WANT_CF_ADDR="module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike"
if [ "${BREAK:-}" = "1" ]; then
  WANT_CF_ADDR="module.monitoring.aws_cloudwatch_metric_alarm.wrong_name"
  log "  BREAK=1: expecting a wrong tofu-address on the CloudFront alarm on purpose - this check must fail"
fi

S3_ALARM_ARN="$(awsl cloudwatch describe-alarms --alarm-names "$S3_ALARM_NAME" --query 'MetricAlarms[0].AlarmArn' --output text)"
[ -n "$S3_ALARM_ARN" ] && [ "$S3_ALARM_ARN" != "None" ] || fail "the S3-requests alarm does not exist"
CF_ALARM_ARN="$(awsl cloudwatch describe-alarms --alarm-names "$CF_ALARM_NAME" --query 'MetricAlarms[0].AlarmArn' --output text)"
[ -n "$CF_ALARM_ARN" ] && [ "$CF_ALARM_ARN" != "None" ] || fail "the CloudFront-requests alarm does not exist"

GOT_S3_ADDR="$(awsl cloudwatch list-tags-for-resource --resource-arn "$S3_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_S3_ADDR" = "$WANT_S3_ADDR" ] || fail "the S3-requests alarm carries tofu-address=$GOT_S3_ADDR, not $WANT_S3_ADDR"
GOT_CF_ADDR="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CF_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_CF_ADDR" = "$WANT_CF_ADDR" ] || fail "the CloudFront-requests alarm carries tofu-address=$GOT_CF_ADDR, not $WANT_CF_ADDR"
log "  alarm $S3_ALARM_NAME -> tofu-address=$GOT_S3_ADDR"
log "  alarm $CF_ALARM_NAME -> tofu-address=$GOT_CF_ADDR"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: the CloudFront alarm's real tofu-address matched the WRONG expected value above without this script noticing - stage 2's assertion is not load-bearing"
fi

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "2 of 3 stamped (1 skipped, untaggable dashboard), 0 failed; both alarm markers read back via the AWS CLI"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan, empty + identities re-asserted
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_plan
log "=== STAGE 3: no state file, live-plan ==="
rm -f "$ESTATE/terraform.tfstate" "$ESTATE/terraform.tfstate.backup"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "the state file is still there"

plan_into() { ( cd "$ESTATE" && "$TOFU" live-plan -input=false -no-color "${TARGETS[@]}" ); }
PLAN_OUT="$(plan_into 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { printf '%s\n' "$PLAN_OUT" | tail -60; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT"; fail "live-plan is not empty"; }
log "  no resource change proposed, with zero local memory of the migration that stamped it"

# Re-assert identities directly against the live objects, after the local
# state file was deleted - any answer below can only have come from the
# marker (both alarms) or the live object's own content (the dashboard).
S3_ADDR2="$(awsl cloudwatch list-tags-for-resource --resource-arn "$S3_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$S3_ADDR2" = "$WANT_S3_ADDR" ] || fail "the S3-requests alarm's tofu-address changed across the empty plan: $WANT_S3_ADDR -> $S3_ADDR2"
CF_ADDR2="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CF_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$CF_ADDR2" = "$WANT_CF_ADDR" ] || fail "the CloudFront-requests alarm's tofu-address changed across the empty plan: $WANT_CF_ADDR -> $CF_ADDR2"

# The dashboard has no tag to re-read, so its identity assertion is the live
# object's OWN content, read directly - the plan came back empty above,
# meaning the client-named derivation (its own `dashboard_name` argument)
# found exactly this live object with no diff, and this independently
# confirms what it found is correct: the same widget JSON the config
# declares (a function of distribution_id, which no marker carries either).
GOT_DASHBOARD_BODY="$(awsl cloudwatch get-dashboard --dashboard-name "$DASHBOARD_NAME" --query 'DashboardBody' --output text)"
[ -n "$GOT_DASHBOARD_BODY" ] || fail "the live dashboard $DASHBOARD_NAME does not exist or has no body"
grep -qF "$DISTRIBUTION_ID" <<< "$GOT_DASHBOARD_BODY" \
  || fail "the live dashboard $DASHBOARD_NAME's body does not reference $DISTRIBUTION_ID - re-derived identity did not find the object the configuration means"
log "  identity re-check: both alarms' tofu-address unchanged; the dashboard, read directly off the live object, still carries this configuration's own distribution_id"

log ""
log "STAGE 3 (test plan): PASS"
log ""
gauntlet_stage test_plan pass "no resource change proposed; both alarms' tofu-address unchanged, dashboard body re-derived and matches distribution_id"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$BEFORE_N" = "2" ] \
  || fail "expected 2 objects carrying tofu-estate=$ESTATE_NAME before the no-op apply (the two alarms), got $BEFORE_N"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
[ ! -f "$ESTATE/terraform.tfstate" ] || fail "a state file exists after the apply"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"

log ""
log "STAGE 4 (test apply): PASS"
log ""
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); object count unchanged at $BEFORE_N, no state file"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one object, replan, assert one fix
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage drift_reconverge
log "=== STAGE 5: drift and reconverge (mutate one alarm's alarm_description out of band) ==="

# alarm_description is a real, config-declared ARGUMENT of the alarm
# resource (PutMetricAlarm's AlarmDescription), not a tag - unlike the
# storage/crossplane crossings' drift (a tag value), this estate's two
# alarms carry no `tags` argument of their own in config at all, so the
# only config-owned field left to drift honestly is this one. put-metric-
# alarm is a full-replace API: every other argument is repeated unchanged
# from the module's own text so only alarm_description moves.
drift_s3_alarm() {
  local desc="$1"
  awsl cloudwatch put-metric-alarm \
    --alarm-name "$S3_ALARM_NAME" \
    --comparison-operator GreaterThanThreshold \
    --evaluation-periods 1 \
    --period 3600 \
    --statistic Sum \
    --threshold 100000 \
    --metric-name GetRequests \
    --namespace AWS/S3 \
    --dimensions Name=BucketName,Value="$BUCKET_NAME" Name=StorageType,Value=AllStorageTypes \
    --alarm-description "$desc" \
    --treat-missing-data ignore >/dev/null
}
drift_cf_alarm() {
  local desc="$1"
  awsl cloudwatch put-metric-alarm \
    --alarm-name "$CF_ALARM_NAME" \
    --comparison-operator GreaterThanThreshold \
    --evaluation-periods 1 \
    --period 3600 \
    --statistic Sum \
    --threshold 100000 \
    --metric-name Requests \
    --namespace AWS/CloudFront \
    --dimensions Name=DistributionId,Value="$DISTRIBUTION_ID" Name=Region,Value=Global \
    --alarm-description "$desc" \
    --treat-missing-data ignore >/dev/null
}

# DELTA (HARNESS WORKAROUND, confirmed real floci defect - filed as
# lex00/floci#95): PutMetricAlarm is documented, in the AWS CLI's own
# bundled help text (`aws cloudwatch put-metric-alarm help`, the --tags
# entry) as create-only for tags - "If you are using this operation to
# update an existing alarm, any tags you specify in this parameter are
# ignored. To change the tags of an existing alarm, use TagResource or
# UntagResource." - meaning a real out-of-band PutMetricAlarm call (exactly
# what drift_s3_alarm/drift_cf_alarm below simulate) can never touch an
# existing alarm's tags on real AWS, whatever it is or is not called with.
# Confirmed directly against this checkout's floci pin: a PutMetricAlarm
# update with no --tags argument WIPES the alarm's existing tags (verified
# by list-tags-for-resource immediately after, going from the two markers
# stage 2 wrote to an empty list) - the opposite of the documented behavior,
# and it costs the marker this crossing depends on. retag_after_drift below
# restores the tags a real out-of-band mutation would never have touched,
# so the rest of this stage exercises real reconvergence rather than a
# floci-only marker loss.
retag_after_drift() {
  local arn="$1" addr="$2"
  awsl cloudwatch tag-resource --resource-arn "$arn" \
    --tags "Key=tofu-estate,Value=$ESTATE_NAME" "Key=tofu-address,Value=$addr" >/dev/null
}

if [ "${BREAK:-}" = "1" ]; then
  drift_cf_alarm "tampered-by-BREAK"
  retag_after_drift "$CF_ALARM_ARN" "$WANT_CF_ADDR"
  log "  BREAK=1: also tampered the CloudFront alarm's alarm_description - stage 5 must now see TWO"
  log "           drifted objects and fail the single-object assertion"
fi

drift_s3_alarm "tampered-out-of-band"
retag_after_drift "$S3_ALARM_ARN" "$WANT_S3_ADDR"
DRIFTED_VALUE="$(awsl cloudwatch describe-alarms --alarm-names "$S3_ALARM_NAME" --query 'MetricAlarms[0].AlarmDescription' --output text)"
[ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band attribute mutation did not take"
log "  mutated $S3_ALARM_NAME's alarm_description to \"tampered-out-of-band\" directly via the AWS CLI - never through choudoufu"

DRIFT_PLAN_OUT="$(plan_into 2>&1)"; DRIFT_PLAN_RC=$?
[ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -60; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
if [ "${BREAK:-}" = "1" ]; then
  [ "$N_CHANGED" = "1" ] \
    && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than"
  log "           one - the single-object assertion and reconverge apply below are skipped"
else
  [ "$N_CHANGED" = "1" ] \
    || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
  [ "$CHANGED_ADDRS" = "module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not the S3-requests alarm"
  log "  the plan proposes fixing exactly one object: $CHANGED_ADDRS - nothing else in the diff"

  RECONVERGE_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_VALUE="$(awsl cloudwatch describe-alarms --alarm-names "$S3_ALARM_NAME" --query 'MetricAlarms[0].AlarmDescription' --output text)"
  [ "$FIXED_VALUE" = "Alert on unusual S3 GET request volume" ] \
    || fail "the alarm's alarm_description is \"$FIXED_VALUE\" after reconverging, not \"Alert on unusual S3 GET request volume\""
  log "  reconverged: $S3_ALARM_NAME's alarm_description is back to \"Alert on unusual S3 GET request volume\", read via the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"
log ""
gauntlet_stage drift_reconverge pass "S3 alarm's alarm_description tampered, exactly 1 object proposed and applied, reconverged to its configured description"

# ══════════════════════════════════════════════════════════════════════════
# PART D: RENAME (day2_rename, live/GAUNTLET.md #6)
# ══════════════════════════════════════════════════════════════════════════
gauntlet_begin_stage day2_rename
log "=== D0. capture the live ids a rename must not disturb ==="
log "  $S3_ALARM_ARN (aws_cloudwatch_metric_alarm.s3_requests_spike), $CF_ALARM_ARN (aws_cloudwatch_metric_alarm.cf_requests_spike)"

if [ "${BREAK:-}" = "1" ]; then
  log "=== D1 (BREAK=1). rename cf_requests_spike -> cf_requests_spike_renamed WITHOUT a moved block ==="
  sed -i.bak 's/resource "aws_cloudwatch_metric_alarm" "cf_requests_spike" {/resource "aws_cloudwatch_metric_alarm" "cf_requests_spike_renamed" {/' "$ESTATE/modules/monitoring/main.tofu"
  rm -f "$ESTATE/modules/monitoring/main.tofu.bak"
  TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike
           -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike
           -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed
           -target=module.monitoring.aws_cloudwatch_dashboard.site)
  BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
  [ "$BREAK_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -30; fail "the BREAK=1 rename-without-moved plan exited $BREAK_PLAN_RC"; }
  grep -qE '^  # module\.monitoring\.aws_cloudwatch_metric_alarm\.cf_requests_spike will be destroyed' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose destroying the CloudFront alarm - this stage's check is not load-bearing"; }
  grep -qE '^  # module\.monitoring\.aws_cloudwatch_metric_alarm\.cf_requests_spike_renamed will be created' <<< "$BREAK_PLAN_OUT" \
    || { printf '%s\n' "$BREAK_PLAN_OUT" | grep -E '^  # .+ will be'; fail "BREAK=1: renaming without a moved block did not propose creating the renamed alarm - this stage's check is not load-bearing"; }
  log "  BREAK=1: correctly proposes destroying the old alarm and creating the renamed one - the moved-block and live-mv checks below are skipped"
else
  log "=== D1. choudoufu, moved block: aws_cloudwatch_metric_alarm.s3_requests_spike -> .s3_requests_spike_renamed ==="
  sed -i.bak 's/resource "aws_cloudwatch_metric_alarm" "s3_requests_spike" {/resource "aws_cloudwatch_metric_alarm" "s3_requests_spike_renamed" {/' "$ESTATE/modules/monitoring/main.tofu"
  rm -f "$ESTATE/modules/monitoring/main.tofu.bak"
  cat >> "$ESTATE/modules/monitoring/main.tofu" <<'EOF'

moved {
  from = aws_cloudwatch_metric_alarm.s3_requests_spike
  to   = aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
}
EOF
  TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike
           -target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
           -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike
           -target=module.monitoring.aws_cloudwatch_dashboard.site)
  MOVED_PLAN_OUT="$(plan_into 2>&1)"; MOVED_PLAN_RC=$?
  [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
  grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
    && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
  grep -qE '^  # module\.monitoring\.aws_cloudwatch_metric_alarm\.s3_requests_spike_renamed will be updated in-place' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed alarm"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
  grep -qE '~ +"tofu-address" += +"module\.monitoring\.aws_cloudwatch_metric_alarm\.s3_requests_spike" +-> +"module\.monitoring\.aws_cloudwatch_metric_alarm\.s3_requests_spike_renamed"' <<< "$MOVED_PLAN_OUT" \
    || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the alarm's tofu-address marker being rewritten from the old address to the new one"; }
  log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

  MOVED_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)"; MOVED_APPLY_RC=$?
  [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
    || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

  S3_ADDR_D_AFTER="$(awsl cloudwatch list-tags-for-resource --resource-arn "$S3_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$S3_ADDR_D_AFTER" = "module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed" ] \
    || fail "the S3-requests alarm carries tofu-address=$S3_ADDR_D_AFTER after the rename, not module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed"
  log "  $S3_ALARM_ARN unchanged, tofu-address now module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed - read via the AWS CLI"

  log "=== D2. choudoufu, live-mv: aws_cloudwatch_metric_alarm.cf_requests_spike -> .cf_requests_spike_renamed, no moved block at all ==="
  sed -i.bak 's/resource "aws_cloudwatch_metric_alarm" "cf_requests_spike" {/resource "aws_cloudwatch_metric_alarm" "cf_requests_spike_renamed" {/' "$ESTATE/modules/monitoring/main.tofu"
  rm -f "$ESTATE/modules/monitoring/main.tofu.bak"
  # HARNESS WORKAROUND (the same confirmed real floci gap the header
  # documents for aws_budgets_budget - AWS Budgets is not implemented by
  # this image). Every other choudoufu invocation in this script scopes the
  # budget out with -target; live-mv has no such flag (per its own -h text
  # it "does not run a plan over the rest of the configuration", but it
  # still resolves every resource's IMPORT IDENTITY while loading the
  # module, including aws_budgets_budget's own account_id-derived identity,
  # which this estate's skip_requesting_account_id=true provider setting
  # makes impossible to build statically - "Identity argument not set").
  # The budget block is removed from the ESTATE copy (never $SRC, never
  # $PLAIN) for this one command only, the same exclusion the rest of the
  # script achieves through -target; it does not come back, since nothing
  # after this point plans or applies the estate.
  python3 - "$ESTATE/modules/monitoring/main.tofu" <<'PYEOF'
import sys
p = sys.argv[1]
s = open(p).read()
start = s.index('resource "aws_budgets_budget" "monthly_limit" {')
depth, i = 0, start
while True:
    c = s[i]
    if c == '{':
        depth += 1
    elif c == '}':
        depth -= 1
        if depth == 0:
            end = i + 1
            break
    i += 1
while end < len(s) and s[end] == '\n':
    end += 1
open(p, 'w').write(s[:start] + s[end:])
PYEOF
  log "  removed aws_budgets_budget.monthly_limit from the ESTATE copy for this one live-mv call (floci gap, see header; already excluded everywhere else via -target)"
  MV_OUT="$(cd "$ESTATE" && "$TOFU" live-mv -estate="$ESTATE_NAME" module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed 2>&1)"; MV_RC=$?
  [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
  grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
  grep -qF '"module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike" -> "module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed"' <<< "$MV_OUT" \
    || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
  log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

  CF_ADDR_D_AFTER="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CF_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$CF_ADDR_D_AFTER" = "module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed" ] \
    || fail "the CloudFront-requests alarm carries tofu-address=$CF_ADDR_D_AFTER after live-mv, not module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed"
  log "  $CF_ALARM_ARN unchanged, tofu-address now module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed - read via the AWS CLI"

  log "=== D3. one more plan: config and markers agree on both renames, nothing proposed ==="
  TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
           -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed
           -target=module.monitoring.aws_cloudwatch_dashboard.site)
  FINAL_PLAN_D_OUT="$(plan_into 2>&1)"; FINAL_PLAN_D_RC=$?
  [ "$FINAL_PLAN_D_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_D_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_D_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_D_OUT" \
    || { grep -E '^  #' <<< "$FINAL_PLAN_D_OUT"; fail "the post-rename plan is not empty"; }
  log "  No changes. Both renames are complete and invisible to the next plan."

  gauntlet_stage day2_rename pass "moved block: aws_cloudwatch_metric_alarm.s3_requests_spike renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: aws_cloudwatch_metric_alarm.cf_requests_spike renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"

  # ══════════════════════════════════════════════════════════════════════
  # PART F: REPLACE (day2_replace, active - live/GAUNTLET.md #9)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed rename: aws_cloudwatch_metric_
  # alarm.s3_requests_spike_renamed is bound and converged. Its `alarm_name`
  # argument changes to a new literal - a real, upstream-immutable argument
  # (CloudWatch has no RenameAlarm API, only PutMetricAlarm/DeleteAlarms) -
  # forcing a replace at the SAME declared address. F-ORACLE above already
  # confirmed, empirically, that stock marks alarm_name as forcing
  # replacement on cold_deploy's own state. cf_requests_spike_renamed and
  # the dashboard are untouched.
  #
  # THE create_before_destroy SCOPE NOTE (see corpus-sqs-basic's own PART F
  # header for the fuller discussion). This estate's two alarms are plain
  # top-level resources, not module-call children, so a `lifecycle` block
  # COULD be added here without crossing the module-boundary line the sqs/
  # ec2/lambda estates in this same unit hit - but this corpus's own DELTA
  # discipline (header: the module source is only ever reduced, e.g. the
  # aws_budgets_budget exclusion, never given library-internal lifecycle
  # blocks it didn't already declare) applies just as much to a resource
  # living in the module's own copied source as to one inside a `module`
  # call, so this exercises OpenTofu's DEFAULT replace ordering
  # (destroy-then-create) rather than create_before_destroy, the same
  # choice every other estate in this unit makes. BREAK=replace below
  # manufactures the coexistence a skipped destroy half would leave.
  gauntlet_begin_stage day2_replace
  record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
  record_import_id() { jq -r '.identity.import_id' "$1"; }
  F_ADDR="module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed"
  F_RECORD="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_cloudwatch_metric_alarm/$(record_key "$F_ADDR")"

  log "=== F0. capture the live alarm and its record ahead of the forced replace ==="
  [ -f "$F_RECORD" ] || fail "no local record file found for $F_ADDR ahead of day2_replace"
  F_OLD_IMPORT_ID="$(record_import_id "$F_RECORD")"
  # aws_cloudwatch_metric_alarm's import ID is the alarm NAME, not its ARN
  # (live/survey.json's own ImportSyntax=ALARMNAME) - checked against the
  # record file directly, the same correction corpus-lambda-simple's own
  # log-group leg needed in this unit.
  [ "$F_OLD_IMPORT_ID" = "S3GetRequestsSpike" ] || fail "the record for $F_ADDR names $F_OLD_IMPORT_ID ahead of day2_replace, not S3GetRequestsSpike"
  F_OLD_ADDR_TAG="$(awsl cloudwatch list-tags-for-resource --resource-arn "$S3_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
  [ "$F_OLD_ADDR_TAG" = "module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed" ] \
    || fail "$S3_ALARM_ARN does not carry tofu-address=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed ahead of day2_replace"
  log "  $S3_ALARM_ARN, record import_id=$F_OLD_IMPORT_ID (the alarm's name, not its ARN), tofu-address=$F_OLD_ADDR_TAG"

  TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
           -target=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed
           -target=module.monitoring.aws_cloudwatch_dashboard.site)

  if [ "${BREAK:-}" = "replace" ]; then
    log "=== F1 (BREAK=replace). manufacture the coexistence a skipped destroy half would leave behind ==="
    # A second, distinct live alarm carrying the SAME tofu-address as the
    # one a genuine replace would destroy - the state "skip the destroy
    # half" of a create-before-destroy replace would leave, produced
    # directly via the AWS CLI rather than by actually interrupting an
    # apply (day2_crash, stage 10, owns testing a real interruption).
    BREAK_COLLISION_NAME="S3GetRequestsSpikeCollision"
    awsl cloudwatch put-metric-alarm --alarm-name "$BREAK_COLLISION_NAME" \
      --comparison-operator GreaterThanThreshold --evaluation-periods 1 --period 3600 \
      --statistic Sum --threshold 100000 --metric-name GetRequests --namespace AWS/S3 \
      >/dev/null || fail "BREAK=replace: could not create the collision alarm"
    BREAK_COLLISION_ARN="$(awsl cloudwatch describe-alarms --alarm-names "$BREAK_COLLISION_NAME" --query 'MetricAlarms[0].AlarmArn' --output text)"
    awsl cloudwatch tag-resource --resource-arn "$BREAK_COLLISION_ARN" \
      --tags "Key=tofu-estate,Value=$ESTATE_NAME" "Key=tofu-address,Value=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed" \
      >/dev/null || fail "BREAK=replace: could not tag the collision alarm"
    BREAK_PLAN_OUT="$(plan_into 2>&1)"; BREAK_PLAN_RC=$?
    awsl cloudwatch delete-alarms --alarm-names "$BREAK_COLLISION_NAME" >/dev/null 2>&1 || true
    [ "$BREAK_PLAN_RC" -ne 0 ] \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan succeeded with two live objects claiming the same tofu-address - it must report the collision, not propose nothing"; }
    grep -qF 'Two live resources claiming one address' <<< "$BREAK_PLAN_OUT" \
      || { printf '%s\n' "$BREAK_PLAN_OUT" | tail -20; fail "BREAK=replace: the plan failed for a reason other than the address collision - this stage's check is not load-bearing"; }
    log "  BREAK=replace: choudoufu correctly refused with a named collision (two live resources claiming one address) rather than silently proposing nothing - the Break text's own outcome"
  else
    log "=== F1. choudoufu: change the ForceNew alarm_name argument, forcing a replace at the same declared address ==="
    sed -i.bak 's/alarm_name          = "S3GetRequestsSpike"/alarm_name          = "S3GetRequestsSpikeV2"/' "$ESTATE/modules/monitoring/main.tofu"
    rm -f "$ESTATE/modules/monitoring/main.tofu.bak"
    grep -q 'S3GetRequestsSpikeV2' "$ESTATE/modules/monitoring/main.tofu" || fail "changing s3_requests_spike_renamed's alarm_name argument did not match - the corpus pin has moved"

    F_PLAN_OUT="$(plan_into 2>&1)"; F_PLAN_RC=$?
    [ "$F_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_PLAN_OUT" | tail -40; fail "the day2_replace plan exited $F_PLAN_RC"; }
    grep -qE '^  # module\.monitoring\.aws_cloudwatch_metric_alarm\.s3_requests_spike_renamed must be replaced' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | grep -E '^  # .+ (will be|must be)'; fail "choudoufu does not propose replacing s3_requests_spike_renamed when its alarm_name argument changes"; }
    grep -qE '~ +alarm_name +=.+forces replacement' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT"; fail "the plan does not mark alarm_name as forcing replacement"; }
    grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$F_PLAN_OUT" \
      || { printf '%s\n' "$F_PLAN_OUT" | tail -10; fail "the day2_replace plan is not exactly one add and one destroy at the same address"; }
    log "  choudoufu: exactly one forced replace at the same declared address (module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed), alarm_name forces replacement"

    F_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color "${TARGETS[@]}" 2>&1)"; F_APPLY_RC=$?
    [ "$F_APPLY_RC" -eq 0 ] || { printf '%s\n' "$F_APPLY_OUT" | tail -40; fail "the day2_replace apply exited $F_APPLY_RC"; }
    grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$F_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$F_APPLY_OUT"; fail "the day2_replace apply was not exactly one add and one destroy"; }

    if F_OLD_STILL="$(awsl cloudwatch describe-alarms --alarm-names S3GetRequestsSpike --query 'MetricAlarms[0].AlarmArn' --output text 2>&1)"; [ -n "$F_OLD_STILL" ] && [ "$F_OLD_STILL" != "None" ]; then
      echo "$F_OLD_STILL"; fail "S3GetRequestsSpike still exists after the replace - the old object was orphaned, not destroyed"
    fi
    log "  S3GetRequestsSpike no longer exists (confirmed via the AWS CLI, not through choudoufu's own report)"

    F_NEW_ARN="$(awsl cloudwatch describe-alarms --alarm-names S3GetRequestsSpikeV2 --query 'MetricAlarms[0].AlarmArn' --output text)"
    [ -n "$F_NEW_ARN" ] && [ "$F_NEW_ARN" != "None" ] || fail "no alarm found by its new name through the AWS CLI after the replace"
    F_NEW_ADDR_TAG="$(awsl cloudwatch list-tags-for-resource --resource-arn "$F_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$F_NEW_ADDR_TAG" = "module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed" ] \
      || fail "$F_NEW_ARN carries tofu-address=$F_NEW_ADDR_TAG after the replace, not module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed - the marker did not move onto the new object"
    log "  $F_NEW_ARN (the new object) carries tofu-address=$F_NEW_ADDR_TAG - the marker moved onto the new object, read via the AWS CLI"

    # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
    # #398-guard shape: a stale record still naming the destroyed object
    # would be exactly the wrong-marker failure that outranks a missing
    # one). The local record file at the SAME address must now hold the
    # NEW object's import_id (its name), not the one captured in F0.
    F_NEW_IMPORT_ID="$(record_import_id "$F_RECORD")"
    [ "$F_NEW_IMPORT_ID" = "S3GetRequestsSpikeV2" ] \
      || fail "the record for $F_ADDR names $F_NEW_IMPORT_ID after the replace, not the new object's name S3GetRequestsSpikeV2 - a stale record still claiming the destroyed object, the #398-guard shape"
    [ "$F_NEW_IMPORT_ID" != "$F_OLD_IMPORT_ID" ] \
      || fail "sanity: the record's import_id at $F_ADDR did not change at all across the replace"
    log "  record store: import_id $F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID at the same key ($F_ADDR) - read directly off the local record store file, not through choudoufu's own report"

    log "=== F2. one more plan: config and reality agree, no marker collision ==="
    F_FINAL_PLAN_OUT="$(plan_into 2>&1)"; F_FINAL_PLAN_RC=$?
    [ "$F_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$F_FINAL_PLAN_OUT" | tail -40; fail "the post-replace plan exited $F_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$F_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$F_FINAL_PLAN_OUT"; fail "the post-replace plan is not empty"; }
    log "  No changes, no marker collision. The replace is complete and invisible to the next plan."

    gauntlet_stage day2_replace pass "choudoufu: changing s3_requests_spike_renamed's ForceNew alarm_name argument proposed exactly one replace at the same declared address (1 add, 0 change, 1 destroy; -/+ destroy and then create), applied cleanly; the old object (S3GetRequestsSpike) is confirmed gone and the new object ($F_NEW_ARN) carries the marker, both via the AWS CLI; the local record store's record at the same address now names the new object's import_id, not the destroyed one ($F_OLD_IMPORT_ID -> $F_NEW_IMPORT_ID); the next plan proposes no resource action; stock oracle on cold_deploy's own state (F-ORACLE) also proposes exactly one replace at the same address (plan only, not applied); BREAK=replace confirms a manufactured marker collision is reported loudly rather than silently proposed as nothing. Scope note: this exercises OpenTofu's default destroy-then-create ordering, not the create_before_destroy variant the stage's Title names - see this section's own header comment."
  fi
  gauntlet_end_stage

  # ══════════════════════════════════════════════════════════════════════
  # PART E: REMOVE A BLOCK (day2_remove, active - live/GAUNTLET.md #7)
  # ══════════════════════════════════════════════════════════════════════
  #
  # Starts from Part D's real, completed state: both alarms are renamed,
  # bound and converged, and the budget block is already physically gone
  # from this ESTATE copy (D2's own live-mv workaround, see its header -
  # nothing after that point plans or applies against it, so its absence
  # never comes back to matter here). aws_cloudwatch_metric_alarm.
  # cf_requests_spike_renamed is the one removed - the S3-requests alarm
  # and the dashboard stay declared, so aws_cloudwatch_metric_alarm's own
  # declared-type set never empties out the way it would if this were the
  # estate's only alarm.
  #
  # TOFU_DISABLE_GUIDED_DISCOVERY=1 and ordinary "$TOFU" plan (not
  # plan_into's live-plan): this estate declares a record_store, so guided
  # discovery is on by default (see corpus-lambda-simple's day2_remove
  # commit for the full mechanism), and live-plan's own estate-wide
  # removal sweep needs the disable env var and the ordinary "plan"
  # command - not "live-plan" - to run unguided and find a genuine orphan
  # the moment its block disappears, the same finding that unit made.
  #
  # BREAK_REMOVE=1 exercises this stage's own Break control instead: keep
  # the block, and assert the plan proposes no destroy for it at all - the
  # Break text in tools/gauntlet/stages.go, verbatim.

  gauntlet_begin_stage day2_remove
  log "=== E0. capture the live id one more time ==="
  E_CF_ADDR_BEFORE="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CF_ALARM_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || true)"
  [ "$E_CF_ADDR_BEFORE" = "module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed" ] \
    || fail "$CF_ALARM_ARN does not carry tofu-address=module.monitoring.aws_cloudwatch_metric_alarm.cf_requests_spike_renamed before day2_remove even starts (got $E_CF_ADDR_BEFORE)"

  if [ "${BREAK_REMOVE:-}" = "1" ]; then
    log "=== E1 (BREAK_REMOVE=1). keep the CloudFront alarm's block; no destroy may be proposed ==="
    BREAK_REMOVE_PLAN_OUT="$(cd "$ESTATE" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; BREAK_REMOVE_PLAN_RC=$?
    [ "$BREAK_REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$BREAK_REMOVE_PLAN_OUT" | tail -40; fail "the BREAK_REMOVE=1 kept-block plan exited $BREAK_REMOVE_PLAN_RC"; }
    grep -qE '^  # .+ will be (created|destroyed)' <<< "$BREAK_REMOVE_PLAN_OUT" \
      && { grep -E '^  # .+ will be' <<< "$BREAK_REMOVE_PLAN_OUT"; fail "BREAK_REMOVE=1: some resource action was proposed with the block still in the config"; }
    log "  BREAK_REMOVE=1: correctly proposes no resource action - the block is still declared"
  else
    log "=== E1. choudoufu: delete aws_cloudwatch_metric_alarm.cf_requests_spike_renamed's block ==="
    python3 - "$ESTATE/modules/monitoring/main.tofu" <<'PYEOF'
import sys
p = sys.argv[1]
s = open(p).read()
start = s.index('resource "aws_cloudwatch_metric_alarm" "cf_requests_spike_renamed" {')
depth, i = 0, start
while True:
    c = s[i]
    if c == '{':
        depth += 1
    elif c == '}':
        depth -= 1
        if depth == 0:
            end = i + 1
            break
    i += 1
while end < len(s) and s[end] == '\n':
    end += 1
open(p, 'w').write(s[:start] + s[end:])
PYEOF
    grep -q 'resource "aws_cloudwatch_metric_alarm" "cf_requests_spike_renamed"' "$ESTATE/modules/monitoring/main.tofu" \
      && fail "removing the CloudFront alarm's block did not match - the config has moved"
    TARGETS=(-target=module.monitoring.aws_cloudwatch_metric_alarm.s3_requests_spike_renamed
             -target=module.monitoring.aws_cloudwatch_dashboard.site)
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_remove reinit failed"; }
    REMOVE_PLAN_OUT="$(cd "$ESTATE" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; REMOVE_PLAN_RC=$?
    [ "$REMOVE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40; fail "the day2_remove plan exited $REMOVE_PLAN_RC"; }
    if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN_OUT"; then
      printf '%s\n' "$REMOVE_PLAN_OUT" | tail -40
      fail "choudoufu withheld the destroy of the CloudFront alarm as a possible rename (discovery.go's classifyOrphans) even though no other aws_cloudwatch_metric_alarm block anywhere in this config is unclaimed - this is the honest wall issue #358 names, not a pass"
    fi
    grep -qE '^  # module\.monitoring\.aws_cloudwatch_metric_alarm\.cf_requests_spike_renamed will be destroyed' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu does not propose destroying the CloudFront alarm when its block is deleted"; }
    grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$REMOVE_PLAN_OUT" \
      || { printf '%s\n' "$REMOVE_PLAN_OUT" | tail -10; fail "choudoufu's remove plan proposes something other than exactly one destroy"; }
    log "  choudoufu: exactly one destroy (the CloudFront alarm), nothing else"

    REMOVE_APPLY_OUT="$(cd "$ESTATE" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; REMOVE_APPLY_RC=$?
    [ "$REMOVE_APPLY_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY_OUT" | tail -40; fail "the day2_remove apply exited $REMOVE_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$REMOVE_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$REMOVE_APPLY_OUT"; fail "the day2_remove apply was not exactly one destroy"; }

    CF_STILL="$(awsl cloudwatch describe-alarms --alarm-names "$CF_ALARM_NAME" --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
    [ "$CF_STILL" = "0" ] || fail "the CloudFront alarm still exists in the live account after the destroy ($CF_STILL found) - it was orphaned, not destroyed"
    log "  the CloudFront alarm no longer exists (0 found) - confirmed via the AWS CLI, not through choudoufu's own report"

    log "=== E2. one more plan: config and reality agree, nothing left to propose ==="
    E_FINAL_PLAN_OUT="$(cd "$ESTATE" && TOFU_DISABLE_GUIDED_DISCOVERY=1 "$TOFU" plan -input=false -no-color 2>&1)"; E_FINAL_PLAN_RC=$?
    [ "$E_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$E_FINAL_PLAN_OUT" | tail -40; fail "the post-remove plan exited $E_FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$E_FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$E_FINAL_PLAN_OUT"; fail "the post-remove plan is not empty"; }
    log "  No changes. The removal is complete and invisible to the next plan."

    gauntlet_stage day2_remove pass "choudoufu: deleting the CloudFront alarm's block proposed exactly one destroy (0 add, 0 change, 1 destroy), applied cleanly (0 added, 0 changed, 1 destroyed), the object is genuinely gone from the live account (describe-alarms on its name no longer returns it, read via the AWS CLI, not choudoufu's own report), and the next plan is empty; classifyOrphans did not withhold the destroy because the S3-requests alarm, the surviving aws_cloudwatch_metric_alarm instance, is bound, not unclaimed"

    # ════════════════════════════════════════════════════════════════════
    # PART G: CHANGE COUNT (day2_count, active - live/GAUNTLET.md #8, issue
    # #359/#488)
    # ════════════════════════════════════════════════════════════════════
    #
    # Starts from Part E's real, completed state. A NEW, entirely synthetic
    # resource (aws_cloudwatch_metric_alarm.count_test, count_test_block()
    # defined above PART G-ORACLE) is added here at the ESTATE root - not
    # inside modules/monitoring, so this never touches the pinned module
    # source and never revisits an address any other stage already used.
    # G-ORACLE above is the stock oracle for the identical shape, applied
    # for real in a separate, otherwise-idle account.
    #
    # BREAK_COUNT=1 exercises this stage's own Break control instead of the
    # real checks: after the real scale-down plan, assert the WRONG
    # instance (count_test[0] rather than count_test[1]) was the one
    # destroyed - the Break text in tools/gauntlet/stages.go for
    # day2_count, verbatim: "Expect a different instance to be destroyed;
    # the assertion must fail." Only reachable when BREAK is not 1 and
    # BREAK_REMOVE is not 1, because PART G starts from PART E's real,
    # completed removal.
    gauntlet_begin_stage day2_count
    COUNT_TAG0='aws_cloudwatch_metric_alarm.count_test:0'
    COUNT_TAG1='aws_cloudwatch_metric_alarm.count_test:1'
    COUNT_RECORD1="$ESTATE/.tofu-records/tofu-records/$ESTATE_NAME/aws_cloudwatch_metric_alarm/$(record_key 'aws_cloudwatch_metric_alarm.count_test[1]')"

    log "=== G0. choudoufu: add aws_cloudwatch_metric_alarm.count_test, count = 2 ==="
    count_test_block 2 > "$ESTATE/day2_count.tofu"
    ( cd "$ESTATE" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ESTATE" && "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "the count-block-add reinit failed"; }
    COUNT_ADD_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_ADD_PLAN_RC=$?
    [ "$COUNT_ADD_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -30; fail "the count-block-add plan exited $COUNT_ADD_PLAN_RC"; }
    grep -qF 'Plan: 2 to add, 0 to change, 0 to destroy.' <<< "$COUNT_ADD_PLAN_OUT" \
      || { printf '%s\n' "$COUNT_ADD_PLAN_OUT" | tail -10; fail "adding the count block did not plan exactly 2 creates"; }
    COUNT_ADD_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_ADD_APPLY_RC=$?
    [ "$COUNT_ADD_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_ADD_APPLY_OUT" | tail -30; fail "the count-block-add apply exited $COUNT_ADD_APPLY_RC"; }
    grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$COUNT_ADD_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$COUNT_ADD_APPLY_OUT"; fail "the count-block-add apply did not create exactly 2 resources"; }

    CT0_STILL="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
    CT1_STILL="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
    [ "$CT0_STILL" = "1" ] || fail "no live count_test[0] alarm found by name"
    [ "$CT1_STILL" = "1" ] || fail "no live count_test[1] alarm found by name"
    CT0_ARN="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'MetricAlarms[0].AlarmArn' --output text)"
    CT1_ARN="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'MetricAlarms[0].AlarmArn' --output text)"
    CT0_ADDR_TAG="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    CT1_ADDR_TAG="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CT1_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
    [ "$CT0_ADDR_TAG" = "$COUNT_TAG0" ] || fail "count_test[0]'s live tofu-address tag is $CT0_ADDR_TAG, not $COUNT_TAG0 (live/MARKERS.md: a count instance's tag value is colon-escaped, e.g. aws_eip.this[2] -> aws_eip.this:2)"
    [ "$CT1_ADDR_TAG" = "$COUNT_TAG1" ] || fail "count_test[1]'s live tofu-address tag is $CT1_ADDR_TAG, not $COUNT_TAG1"
    log "  2 instances created: index 0 = $CT0_ARN (tofu-address=$CT0_ADDR_TAG), index 1 = $CT1_ARN (tofu-address=$CT1_ADDR_TAG) - read via the AWS CLI"

    COUNT_NOOP_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_NOOP_PLAN_RC=$?
    [ "$COUNT_NOOP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_NOOP_PLAN_OUT" | tail -30; fail "the post-add plan exited $COUNT_NOOP_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_NOOP_PLAN_OUT" \
      || { grep -E '^  #' <<< "$COUNT_NOOP_PLAN_OUT"; fail "the plan right after adding the count block is not empty - the new instances did not bind their own markers cleanly"; }
    log "  No changes - both new instances plan empty immediately after creation"

    log "=== G1. scale count down: 2 -> 1 ==="
    count_test_block 1 > "$ESTATE/day2_count.tofu"
    COUNT_DOWN_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_DOWN_PLAN_RC=$?
    [ "$COUNT_DOWN_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -30; fail "the scale-down plan exited $COUNT_DOWN_PLAN_RC"; }

    if [ "${BREAK_COUNT:-}" = "1" ]; then
      log "  BREAK_COUNT=1: asserting the WRONG instance (count_test[0]) was destroyed instead of count_test[1]"
      if grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[0\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT"; then
        fail "BREAK_COUNT=1: the plan actually destroys count_test[0] - this assertion is not load-bearing"
      fi
      log "  BREAK_COUNT=1: correctly does NOT destroy count_test[0] - the wrong-instance assertion above fails to hold, as it must"
    else
      grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[1\] will be destroyed' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan does not destroy count_test[1]"; }
      grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[0\] will be' <<< "$COUNT_DOWN_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-down plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 0 to add, 0 to change, 1 to destroy.' <<< "$COUNT_DOWN_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_DOWN_PLAN_OUT" | tail -10; fail "choudoufu's scale-down plan proposes something other than exactly one destroy"; }
      log "  choudoufu: exactly one destroy (count_test[1]), count_test[0] untouched"

      COUNT_DOWN_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_DOWN_APPLY_RC=$?
      [ "$COUNT_DOWN_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_DOWN_APPLY_OUT" | tail -30; fail "the scale-down apply exited $COUNT_DOWN_APPLY_RC"; }
      grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$COUNT_DOWN_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_DOWN_APPLY_OUT"; fail "the scale-down apply was not exactly one destroy"; }

      # aws_cloudwatch_metric_alarm has no server-minted id (see header's
      # identity-shape note) - absence via describe-alarms, not an id
      # changing, is what proves count_test[1] was genuinely destroyed.
      CT1_GONE="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
      [ "$CT1_GONE" = "0" ] || fail "count_test[1] ($CT1_ARN) still exists in the live account after the scale-down destroy"
      CT0_STILL_AFTER_DOWN="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
      [ "$CT0_STILL_AFTER_DOWN" = "1" ] || fail "count_test[0] no longer exists after the scale-down - it should be untouched"
      CT0_ADDR_AFTER_DOWN="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT0_ADDR_AFTER_DOWN" = "$COUNT_TAG0" ] || fail "count_test[0]'s tofu-address tag changed across the scale-down: $CT0_ADDR_AFTER_DOWN"
      log "  leynos-monitoring-count-test-1 no longer exists (0 found via describe-alarms); $CT0_ARN (count_test[0]) unchanged and marker intact - all read via the AWS CLI"

      # THE RECORD STORE, asserted by value (HANDOFF's safety rule; the
      # #398-guard shape). A destroyed count instance's local record is
      # TOMBSTONED, not deleted - the key must still exist, carrying no
      # current Identity but a Tombstone entry for the object it used to
      # name. Read directly off the local record store file, never through
      # choudoufu's own report.
      [ -f "$COUNT_RECORD1" ] || fail "the record file for count_test[1] is gone after the scale-down destroy - a destroyed count instance's record must be tombstoned, not deleted (#398-guard shape)"
      jq -e 'has("tombstone") and (has("identity") | not)' "$COUNT_RECORD1" >/dev/null \
        || { cat "$COUNT_RECORD1"; fail "count_test[1]'s record does not read as tombstoned (has(\"tombstone\") and not has(\"identity\")) after the scale-down destroy"; }
      log "  record store: count_test[1]'s record is tombstoned (has(\"tombstone\"), no current \"identity\") - the #398-guard shape, read directly off the local record store file"

      log "=== G2. scale count back up: 1 -> 2 ==="
      count_test_block 2 > "$ESTATE/day2_count.tofu"
      COUNT_UP_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_UP_PLAN_RC=$?
      [ "$COUNT_UP_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -30; fail "the scale-up plan exited $COUNT_UP_PLAN_RC"; }
      grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[1\] will be created' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan does not create count_test[1]"; }
      grep -qE '^  # aws_cloudwatch_metric_alarm\.count_test\[0\] will be' <<< "$COUNT_UP_PLAN_OUT" \
        && { printf '%s\n' "$COUNT_UP_PLAN_OUT" | grep -E '^  # .+ will be'; fail "choudoufu's scale-up plan touches count_test[0], which should be untouched"; }
      grep -qF 'Plan: 1 to add, 0 to change, 0 to destroy.' <<< "$COUNT_UP_PLAN_OUT" \
        || { printf '%s\n' "$COUNT_UP_PLAN_OUT" | tail -10; fail "choudoufu's scale-up plan proposes something other than exactly one create"; }
      log "  choudoufu: exactly one create (count_test[1]), count_test[0] untouched"

      COUNT_UP_APPLY_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; COUNT_UP_APPLY_RC=$?
      [ "$COUNT_UP_APPLY_RC" -eq 0 ] || { printf '%s\n' "$COUNT_UP_APPLY_OUT" | tail -30; fail "the scale-up apply exited $COUNT_UP_APPLY_RC"; }
      grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$COUNT_UP_APPLY_OUT" \
        || { grep -E 'Apply complete' <<< "$COUNT_UP_APPLY_OUT"; fail "the scale-up apply was not exactly one create"; }

      CT1_NEW_STILL="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
      [ "$CT1_NEW_STILL" = "1" ] || fail "no live count_test[1] alarm found by name after the scale-up"
      CT1_NEW_ARN="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-1 --query 'MetricAlarms[0].AlarmArn' --output text)"
      # aws_cloudwatch_metric_alarm's ARN is name/region/account-derived, not
      # server-random (established directly against floci ahead of writing
      # this stage - see header) - a destroy+recreate under the same name
      # yields the SAME ARN. That is expected, not a failure: the genuine
      # destroy was already proven by absence in G1 above.
      [ "$CT1_NEW_ARN" = "$CT1_ARN" ] || fail "the recreated count_test[1]'s ARN ($CT1_NEW_ARN) differs from its pre-destroy ARN ($CT1_ARN) - unexpected: aws_cloudwatch_metric_alarm's ARN is name/region/account-derived and should be identical both times"
      CT1_NEW_ADDR_TAG="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CT1_NEW_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT1_NEW_ADDR_TAG" = "$COUNT_TAG1" ] || fail "the recreated count_test[1] ($CT1_NEW_ARN) carries tofu-address=$CT1_NEW_ADDR_TAG, not $COUNT_TAG1"
      CT0_STILL_AFTER_UP="$(awsl cloudwatch describe-alarms --alarm-names leynos-monitoring-count-test-0 --query 'length(MetricAlarms)' --output text 2>/dev/null || echo 0)"
      [ "$CT0_STILL_AFTER_UP" = "1" ] || fail "count_test[0] no longer exists after the scale-up - it should be untouched"
      CT0_ADDR_AFTER_UP="$(awsl cloudwatch list-tags-for-resource --resource-arn "$CT0_ARN" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
      [ "$CT0_ADDR_AFTER_UP" = "$COUNT_TAG0" ] || fail "count_test[0]'s tofu-address tag changed across the scale-up: $CT0_ADDR_AFTER_UP"
      log "  count_test[1] recreated under the same ARN ($CT1_NEW_ARN, deterministic from name/region/account), tofu-address=$CT1_NEW_ADDR_TAG; count_test[0] ($CT0_ARN) untouched throughout the down-then-up cycle - all read via the AWS CLI"

      jq -e 'has("identity") and (.identity.import_id == "leynos-monitoring-count-test-1")' "$COUNT_RECORD1" >/dev/null \
        || { cat "$COUNT_RECORD1"; fail "count_test[1]'s record does not carry a fresh \"identity\" naming the recreated alarm after the scale-up"; }
      log "  record store: count_test[1]'s record carries a live \"identity\" again (import_id=leynos-monitoring-count-test-1), the tombstone entry from G1 carried alongside it - read directly off the local record store file"

      log "=== G3. one more plan: config and reality agree, nothing left to propose ==="
      COUNT_FINAL_PLAN_OUT="$(cd "$ESTATE" && "$TOFU" plan -input=false -no-color 2>&1)"; COUNT_FINAL_PLAN_RC=$?
      [ "$COUNT_FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL_PLAN_OUT" | tail -30; fail "the post-scale-up plan exited $COUNT_FINAL_PLAN_RC"; }
      grep -qF "No changes. Your infrastructure matches the configuration." <<< "$COUNT_FINAL_PLAN_OUT" \
        || { grep -E '^  #' <<< "$COUNT_FINAL_PLAN_OUT"; fail "the post-scale-up plan is not empty"; }
      log "  No changes. The scale-down-then-up cycle is complete and invisible to the next plan."

      gauntlet_stage day2_count pass "choudoufu: scaling aws_cloudwatch_metric_alarm.count_test from 2 to 1 destroyed exactly count_test[1] (0 add, 0 change, 1 destroy), confirmed genuinely gone via describe-alarms (no server-minted id on this type - see header), leaving count_test[0] and its tofu-address marker unchanged; the local record store's record for count_test[1] read tombstoned (has(\"tombstone\"), no current \"identity\") at that same key, the #398-guard shape; scaling back from 1 to 2 created exactly count_test[1] under the SAME ARN (name/region/account-derived) with a fresh \"identity\" record entry (0 add, 0 change -> 1 add, 0 change, 0 destroy) while count_test[0] stayed untouched throughout; the next plan is empty; the G-ORACLE stock oracle on the same 2-instance count block, applied fresh in the idle greenfield-oracle account, shows the identical shape: destroy the higher index only (confirmed absent via describe-alarms), create it back (confirmed present again), the lower index untouched both times"
    fi
    gauntlet_end_stage
  fi
  gauntlet_end_stage
fi
gauntlet_end_stage
gauntlet_end

log "=== PASS: all five stages, real, against leynos/df12-www's own unmodified ==="
log "=== modules/monitoring, .tofu extension throughout, aws_budgets_budget    ==="
log "=== targeted out for a confirmed floci gap (see header)                  ==="
