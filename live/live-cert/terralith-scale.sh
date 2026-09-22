#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/terralith-scale.sh: the live-AWS certification harness for
# issue #567 (child E of epic #546) - the one question floci cannot answer:
# does the O(types) discovery advantage survive contact with a service that
# throttles? floci never returns TooManyRequests; this script runs the SAME
# terralith #564's tools/terralith-gen builds (proven apply/destroy against
# floci at -scale 1 and -scale 4 by #564 and #566) against real AWS instead,
# and instruments for pagination depth, streamed volume, and observed
# throttling/backoff on top of the same four gauntlet stages
# live/live-cert/reference-ec2-vpc.sh already proves (cold_deploy, migrate,
# test_plan, test_apply).
#
# It is NOT reference-ec2-vpc.sh with a bigger config: the composition is
# categorically different - IAM (account-global, not region-scoped) and
# Route 53 (a single hosted zone with a real, documented account-wide
# ChangeResourceRecordSets rate limit) dominate this estate instead of a
# handful of EC2 objects, so this script:
#
#   1. Names every resource with a per-run PREFIX (embeds RUN_ID) rather
#      than relying on tags alone for scoping - about half this estate's
#      resource types (aws_iam_role_policy, aws_iam_role_policy_attachment,
#      aws_route53_record) have NO tags argument in the provider schema at
#      all (confirmed: live/e2e/terralith-scale/MIGRATION.md's ratification
#      table, 29/55 UNTAGGABLE at scale 1), so a tag-only scope would miss
#      them entirely. Every enumeration and delete below is scoped by name
#      prefix, by the tofu-cert-run tag (for the ~half that support one, via
#      a provider-level default_tags block - see provider_block), or by
#      cascading from an already-scoped parent (a role's own inline
#      policies/attachments, a zone's own records, a cluster's own
#      services) - never an unscoped list-everything-and-delete.
#   2. Fixes two things terralith-gen's output gets right for floci but
#      wrong for a real, non-us-east-1 region: skip_requesting_account_id
#      (root cause of #572, the ECS identity-resolution defect
#      live/e2e/terralith-scale/MIGRATION.md found - the fix IS to not set
#      it for a real account) and a hardcoded "us-east-1a" availability
#      zone (network.tf) that plain does not exist in us-east-2. Both are
#      corrected in generate_estate below, not in tools/terralith-gen itself
#      - this script's provider/AZ handling is self-owned the same way
#      reference-ec2-vpc.sh's resource_block/provider_block are.
#   3. Verifies teardown the same double-path way reference-ec2-vpc.sh does
#      - choudoufu's own destroy path best-effort, the untouched stock state
#      in COLD_DIR as the trusted primary path, then an independent listing
#      (verify_empty below), then a raw-CLI sweep (sweep below) if anything
#      survives - extended for every resource kind this estate creates
#      (IAM role/policy/instance-profile, ECS cluster/service/task-def,
#      Route 53 zone/record, plus the VPC/subnet/SG reference-ec2-vpc.sh
#      already covers).
#   4. Asserts its own IAM role headroom before cold_deploy spends anything
#      (issue #1230, #1150's third item). Quota exhausted from OUTSIDE the
#      run - a leaked estate, another estate's live-cert, a console session
#      - used to surface as cold_deploy failing with LimitExceeded, i.e. as
#      "choudoufu could not deploy this estate", blaming the product for
#      the account. iam_role_headroom_check below reads Roles/RolesQuota
#      from `aws iam get-account-summary`, asks tools/terralith-gen
#      (-iam-roles, the generator's own test-pinned formula: 11 roles per
#      scale, never a constant typed here) how many aws_iam_role INSTANCES
#      this SCALE creates, and on insufficient room emits a `GAUNTLET
#      refused=1 ... unit=iam-roles` line and exits 2 before anything is
#      created - recorded by tools/gauntlet as outcome: refused on its own
#      rung (#1151), never as a failed certification. It fails OPEN,
#      loudly, if the check itself cannot run (the CLI errors, prints a
#      non-integer, or the generator does): an AWS blip must not become a
#      refusal on the ladder, and cold_deploy's own failure is still there
#      to catch the case the check missed. LIVECERT_RESUME skips it along
#      with cold_deploy: a resumed estate already holds its roles.
#
# Env (beyond what reference-ec2-vpc.sh reads - see that file's own doc
# comment for TARGET/REGION/RUN_ID/TOFU_BIN/TF_COLD_BIN/FLOCI_PORT/
# FLOCI_IMAGE/LIVECERT_WORK_DIR):
#   SCALE           terralith-gen's own -scale (default 1, the smallest
#                    tier - #546's own rule: prove teardown at each tier
#                    before growing).
#   RECORD_STORE_BACKEND  local or s3; anything else is refused before the
#                    run starts. Default s3 for TARGET=aws, local for
#                    TARGET=floci. s3 is the one that puts the VALUES half
#                    of the state model in the cloud: it is checked at 4a2
#                    and torn down at the end; local is a directory inside
#                    WORK and goes with it. "ssm" was the third and the aws
#                    default until GitHub issue #1346 retired Parameter
#                    Store as a record store. choudoufu now refuses that
#                    block, so a new run that names it is refused here
#                    first; a teardown-only dispatch still accepts it, to
#                    clean up what a held run from before #1346 wrote.
#   RECORD_STORE_BUCKET  Required for RECORD_STORE_BACKEND=s3, ignored
#                    otherwise. An EXISTING bucket the run writes two key
#                    namespaces into and deletes those two namespaces from
#                    at teardown; the bucket itself is never created or
#                    deleted, and nothing else in it is touched.
#   THROTTLE_LOG     1 (default) captures TF_LOG=DEBUG for cold_deploy's
#                    apply and migrate's -approve (both bounded, single-pass
#                    operations) to a file under WORK, so this run can grep
#                    it afterward for retry/throttle/pagination evidence
#                    without holding the log in memory. 0 disables it.
#   WALLCLOCK_TRACE  0 (default) or 1. 1 adds sections 2e, 4g and 4h: three
#                    DEBUG-instrumented stock plans before migrate and three
#                    after the steady state is reached on the choudoufu side,
#                    then live/live-cert/wallclock-gaps.py over all six. This
#                    is issue #867's measurement - the idle-gap trace of the
#                    read and sweep passes that #683 took by hand on a branch
#                    that no longer exists. Six extra plans, no extra objects
#                    created, and nothing gates on it.
#   UNTRUSTED_TEARDOWN_TIMEOUT_S  180 (default) seconds. Bounds teardown()'s
#                    best-effort "choudoufu's own destroy path" step, which
#                    is explicitly NOT the trusted path (see teardown()'s
#                    own comment, #1048) - a hang there must never block or,
#                    via the TEARDOWN_DONE re-entry guard, skip the trusted
#                    terraform destroy that runs after it.
#   LIVECERT_INDEX_WAIT_S  1800 (default) seconds. Bounds the index-wait
#                    step between migrate and test_plan (#1046, #1049): the
#                    2026-09-11 scale-50 run found the Resource Groups
#                    Tagging API's search index held 104 of 1,655 resources
#                    migrate had just verified and stamped, 21 minutes after
#                    migrate finished, and the product now refuses
#                    (DIRECT_READ_UNRESOLVED) rather than proposing creates
#                    while that index lags - see the index-wait step's own
#                    comment below for what it polls and records.
#   LIVECERT_INDEX_POLL_S  30 (default) seconds between polls of the index
#                    during that wait. Overridable so a self-test can drive
#                    the same loop on a 1-second clock instead of a 30s one.
#   LIVECERT_HOLD    0 (default) or 1. The maintainer's own words, verbatim
#                    (#1032): "its not about compute its about the time it
#                    takes and it slows down my development" - three
#                    scale-50 cycles in one night each spent 35 min on
#                    cold_deploy, 40 on migrate and 40 on teardown to look
#                    at ONE plan. LIVECERT_HOLD=1 skips teardown (the EXIT
#                    trap still fires; teardown() itself becomes a no-op
#                    that prints where everything is and how to tear it
#                    down later) so a real account can be inspected, or
#                    resumed against (LIVECERT_RESUME below), in minutes
#                    instead of a full cycle. A held run's gauntlet row
#                    carries "held: true" in every stage's own detail, so
#                    nothing that reads live/gauntlet.json can mistake it
#                    for a finished (torn-down, verified-empty)
#                    certification - see what-you-pay.md.
#   LIVECERT_RESUME  <work dir>. Skips cold_deploy and migrate entirely and
#                    runs from index_wait onward against a work dir a
#                    previous LIVECERT_HOLD=1 run left standing - the two
#                    stages that spend the 75 minutes in the maintainer's
#                    complaint above, for a plan-only iteration that does
#                    not need to re-verify either of them. Refuses (before
#                    touching anything) if the work dir's own recorded
#                    PREFIX or SCALE disagrees with this run's environment -
#                    the resumed state names real objects by PREFIX, so a
#                    mismatch would silently plan against the wrong
#                    account's naming. The caller must export the SAME
#                    PREFIX/SCALE (and RECORD_STORE_BACKEND plus
#                    RECORD_STORE_BUCKET, if set non-default) the held run
#                    used; a resumed run's
#                    cold_deploy/migrate stages are logged as
#                    "verdict=skipped", never "pass" - they are not
#                    GAUNTLET protocol lines and never reach
#                    live/gauntlet.json, so a resumed run's Clear can never
#                    read true on a stage this run did not actually verify.
#   LIVECERT_TEARDOWN_ONLY  <work dir>. Equivalent to running this script as
#                    `terralith-scale.sh teardown <work dir>` (the positional
#                    form is checked first): tears down a held work dir on
#                    its own, without paying for cold_deploy or migrate
#                    again. Runs only the trusted stock destroy plus the
#                    verify-empty listing (and the raw-CLI sweep if anything
#                    survives) - not the best-effort "choudoufu's own
#                    destroy path" step, which needs a freshly built binary
#                    and a rebuilt live block this dispatch does not
#                    reconstruct. It only destroys resources an earlier run
#                    already created and verifies the account empty.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/live-cert/lib/live-cert.sh
source "$LIB/live-cert.sh"

# ── teardown-only dispatch, part 1 (issue #1032) ───────────────────────
# `terralith-scale.sh teardown <work dir>` (or LIVECERT_TEARDOWN_ONLY=<work
# dir>) tears down a held work dir without paying for cold_deploy or migrate
# again - that is the whole point of holding one in the first place. This
# half only READS the work dir's own cold-deploy marker (written below, once
# cold_deploy's own resource-count assertion passes - grep this file for
# .livecert-cold-state) and seeds
# PREFIX/RUN_ID/TARGET/REGION/RECORD_STORE_BACKEND/SCALE from it, before any
# of those get their normal fresh-run defaults a few lines down - the rest
# of the cascade below (ESTATE, RECORD_STORE_ARGS, SSM_PREFIX, WORK,
# COLD_DIR, ADOPTED_DIR) then resolves against the resumed values for free,
# through the same "${VAR:-default}" form every one of them already uses.
# Part 2, which actually runs the destroy, sits right before "0. tools"
# below - it needs teardown()/verify_empty()/sweep() already defined.
#
# The marker lines below bracket each half, the way the record store
# selection block above is bracketed, and for the same reason (#1380): a
# self-test must never test this dispatch by EXECUTING this script. A
# mutation of the dispatch that loses its `exit 0` falls straight through
# into "0. tools" and then into a cold deploy, which with TARGET=aws is a
# paid one - that is how #1346 happened, and how a mutation test started a
# real-AWS deploy on 2026-09-18. selftest-hold-resume.sh case 3 and
# selftest-record-store-s3.sh case 7 extract the span from part 1's opening
# marker to part 2's closing marker (the whole prelude, function
# definitions included, stopping before "0. tools") and run that text
# instead. The extracted text ENDS at the dispatch, so no mutation inside it
# can reach a deploy: there is nothing after it to reach.
# >>> teardown-only dispatch part 1
livecert_marker_get() {
  grep -m1 "^$2=" "$1" 2>/dev/null | cut -d= -f2-
}

TEARDOWN_ONLY_DIR=""
if [ "${1:-}" = "teardown" ] && [ -n "${2:-}" ]; then
  TEARDOWN_ONLY_DIR="$2"
elif [ -n "${LIVECERT_TEARDOWN_ONLY:-}" ]; then
  TEARDOWN_ONLY_DIR="$LIVECERT_TEARDOWN_ONLY"
fi
if [ -n "$TEARDOWN_ONLY_DIR" ]; then
  COLD_MARKER_EARLY="$TEARDOWN_ONLY_DIR/.livecert-cold-state"
  [ -f "$COLD_MARKER_EARLY" ] || { echo "teardown: $TEARDOWN_ONLY_DIR has no $COLD_MARKER_EARLY - nothing recorded here to tear down (was cold_deploy ever completed in this work dir?)" >&2; exit 2; }
  PREFIX="${PREFIX:-$(livecert_marker_get "$COLD_MARKER_EARLY" PREFIX)}"
  RUN_ID="${RUN_ID:-$(livecert_marker_get "$COLD_MARKER_EARLY" RUN_ID)}"
  TARGET="${TARGET:-$(livecert_marker_get "$COLD_MARKER_EARLY" TARGET)}"
  REGION="${REGION:-$(livecert_marker_get "$COLD_MARKER_EARLY" REGION)}"
  RECORD_STORE_BACKEND="${RECORD_STORE_BACKEND:-$(livecert_marker_get "$COLD_MARKER_EARLY" RECORD_STORE_BACKEND)}"
  # The bucket travels with the backend (#1145). Without it a held s3
  # estate could not be torn down at all: the RECORD_STORE_BUCKET:? refusal
  # a few dozen lines below fires before the dispatch reaches teardown(),
  # so every object stays in the bucket and the operator is told only that
  # a variable is missing. Empty for every other backend, where the
  # refusal does not apply.
  RECORD_STORE_BUCKET="${RECORD_STORE_BUCKET:-$(livecert_marker_get "$COLD_MARKER_EARLY" RECORD_STORE_BUCKET)}"
  SCALE="${SCALE:-$(livecert_marker_get "$COLD_MARKER_EARLY" SCALE)}"
  [ -n "$PREFIX" ] && [ -n "$TARGET" ] && [ -n "$REGION" ] \
    || { echo "teardown: $COLD_MARKER_EARLY is missing PREFIX/TARGET/REGION - a marker from an older script version?" >&2; exit 2; }
  LIVECERT_WORK_DIR="${LIVECERT_WORK_DIR:-$TEARDOWN_ONLY_DIR}"
fi
# <<< teardown-only dispatch part 1

TARGET="${TARGET:-floci}"
REGION="${REGION:-us-east-1}"
SCALE="${SCALE:-1}"
RUN_ID="${RUN_ID:-lc$(date +%s)-$$}"
PREFIX="${PREFIX:-lc$(date +%s)$$}"
ESTATE="tl-livecert-$PREFIX"

# LIVECERT_HOLD (#1032, see this file's own doc comment above): HOLD_TAG is
# appended to every GAUNTLET stage detail this run reports, pass or fail, so
# a held run's row in live/gauntlet.json can never be mistaken for a
# finished, torn-down, verified-empty certification by anything that reads
# per-stage detail - fail() (below) and the four gauntlet_stage call sites
# each append it themselves; there is no single choke point that already
# sees every stage's detail text.
LIVECERT_HOLD="${LIVECERT_HOLD:-0}"
case "$LIVECERT_HOLD" in
  0|1) ;;
  *) echo "LIVECERT_HOLD must be 0 or 1, got $LIVECERT_HOLD" >&2; exit 2 ;;
esac
HOLD_TAG=""
[ "$LIVECERT_HOLD" = "1" ] && HOLD_TAG=" held: true"

# Which record_store backend the adopted estate declares. Until now this was
# hardcoded to "local", a directory on disk beside the module - which means
# every real-AWS run this harness has ever produced exercised the VALUES half
# of the state model against local disk, not against the cloud. Of the three
# pieces (identity as tags, values in a record store, effects as receipts),
# only identity was genuinely under test.
#
# "s3" is the default for TARGET=aws, and it needs RECORD_STORE_BUCKET to
# name a bucket that already exists - the run writes two key namespaces into
# it and deletes those two at teardown, and never creates or destroys the
# bucket itself. examples/record-store-bucket stands a correct one up. Until
# GitHub issue #1346 the aws default was "ssm", which needed nothing created
# first; Parameter Store is retired as a record store and choudoufu refuses
# the block, so that convenience is gone with it.
# floci keeps "local", because the point there is speed - but a floci run
# that names s3 explicitly gets the same 4a2 values check and the same
# record-store teardown an aws run gets, against the emulator's own
# endpoint (#1145). That is how the s3 arms get exercised without paying
# for a real-AWS cycle.
# The two marker lines bracket everything that decides the backend, so
# selftest-record-store-s3.sh can run this block alone. It must never be
# tested by executing this script: a selection that fails to refuse lets the
# run carry on, and with TARGET=aws that is a paid run. That happened once,
# from a mutation test (GitHub issue #1346).
# >>> record store backend selection
if [ "$TARGET" = "aws" ]; then
  RECORD_STORE_BACKEND="${RECORD_STORE_BACKEND:-s3}"
else
  RECORD_STORE_BACKEND="${RECORD_STORE_BACKEND:-local}"
fi
#
# Every branch on RECORD_STORE_BACKEND below this point is a three-way case
# with a loud default, never an `if ssm ... else`. Issue #1145: the two that
# were written as `if ssm` treated s3 as local disk - one skipped the
# values-piece check while printing that the store was local disk, the other
# skipped teardown's record-store cleanup and left every object behind.
RECORD_KEY_PREFIX="choudoufu/livecert/$PREFIX"
case "$RECORD_STORE_BACKEND" in
  local) RECORD_STORE_ARGS='      path = ".tofu-records"' ;;
  # key_prefix is a record KEY prefix, not an SSM parameter path, so it is
  # store-relative and must not start with "/" (issue #688 refuses that
  # shape loudly). The ssm backend renders it into the parameter name
  # "/choudoufu/livecert/$PREFIX/...", which is what SSM_PREFIX below
  # counts and tears down. Issue #916.
  # Retired (#1346). Only a teardown-only dispatch gets past this: it
  # generates no configuration, and it is how the parameters a held run from
  # before #1346 wrote get deleted. The ssm arms in teardown and in the
  # emptiness check below stay for that and nothing else.
  ssm)   if [ -z "$TEARDOWN_ONLY_DIR" ]; then
           echo "RECORD_STORE_BACKEND=ssm: Parameter Store is retired as a record store (GitHub issue #1346) and choudoufu refuses record_store \"ssm\", so this run could not get past its first plan. Use RECORD_STORE_BACKEND=s3 with RECORD_STORE_BUCKET naming an existing bucket (examples/record-store-bucket stands one up). Only a teardown-only dispatch of a work dir from before #1346 may still name ssm." >&2
           exit 2
         fi
         RECORD_STORE_ARGS="      key_prefix = \"$RECORD_KEY_PREFIX\"
      region     = \"$REGION\"" ;;
  s3)    : "${RECORD_STORE_BUCKET:?RECORD_STORE_BACKEND=s3 needs RECORD_STORE_BUCKET}"
         RECORD_STORE_ARGS="      bucket     = \"$RECORD_STORE_BUCKET\"
      key_prefix = \"$RECORD_KEY_PREFIX\"
      region     = \"$REGION\"" ;;
  *)     echo "unknown RECORD_STORE_BACKEND: $RECORD_STORE_BACKEND (want local or s3)" >&2; exit 2 ;;
esac
# <<< record store backend selection
# Where this run's records land, per backend, as an outside observer names
# them. The ssm backend prepends "/" to the key prefix to make a legal
# parameter path; the s3 backend uses the key prefix verbatim as an object
# key prefix. Both measured against floci on 2026-09-17 with the fixture in
# issue #1145's thread.
SSM_PREFIX="/$RECORD_KEY_PREFIX"
S3_PREFIX="$RECORD_KEY_PREFIX/"
# The guided-discovery hint (internal/live/projection/hint_store.go's
# HintKey) does NOT live under the configured key_prefix: it is keyed
# "tofu-hints/<estate>/guided", deliberately disjoint from the record
# namespace so orphan discovery can never mistake it for a record. A
# key_prefix-scoped teardown therefore misses it. Measured against floci on
# 2026-09-17: an apply with record_store "s3" left
# "tofu-hints/<estate>/guided" in the bucket beside the three keys under the
# prefix, and the ssm run left "/tofu-hints/<estate>/guided" in Parameter
# Store - which the pre-#1145 ssm teardown, prefix-scoped, also left behind.
HINT_SSM_PREFIX="/tofu-hints/$ESTATE"
HINT_S3_PREFIX="tofu-hints/$ESTATE/"
WORK="${LIVECERT_WORK_DIR:-${LIVECERT_RESUME:-$(mktemp -d)}}"
mkdir -p "$WORK"
FLOCI_PORT="${FLOCI_PORT:-4817}"
FLOCI_NAME="choudoufu-livecert-terralith-scale-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
THROTTLE_LOG="${THROTTLE_LOG:-1}"
COLD_DIR="$WORK/cold"
ADOPTED_DIR="$WORK/adopted"

# Both formulas track tools/terralith-gen's composition and MUST be updated
# with it. They were 50*SCALE+5 / 21*SCALE+5 when this script was written
# against the pre-#574 generator; issue #574 then added the count-expanded
# (6 resources * countTeamsPerScale=2 per scale = 12*SCALE, of which 3 per
# team-equivalent are taggable = 6*SCALE) and module-nested (6 resources *
# len(modulePodKeys)=2 * podSizePerScale=1 per scale = 12*SCALE, likewise
# 6*SCALE taggable) identity buckets, so both went up by 24 and 12 per
# scale respectively. EXPECTED matches live/e2e/terralith-scale/run.sh's
# own post-#574 formula (74*SCALE+5) by construction; keep them equal.
EXPECTED=$((74 * SCALE + 5))    # total resources
VERIFIED=$((33 * SCALE + 5))    # taggable (VERIFIED/DRIFTED-eligible) resources - 18 named-team + 1 service-exec-role + 6 count-expanded + 6 module-nested + 2 container per scale, plus a fixed 5 (zone, VPC, subnet, SG, cluster)

# ── what the tag index can actually hold (issue #1143) ──────────────────
#
# VERIFIED above counts what migrate STAMPS. index_wait() used to poll the
# Resource Groups Tagging API for that same number, which cannot be reached:
# a majority of the stamped objects are of types GetResources never returns,
# or returns only in a region this run does not query. The wait therefore
# burned its whole bound on every real-AWS run - 3600s of dead time at scale
# 50 - and then printed a line that read like a measurement of index lag.
# It was not measuring lag. The target was wrong.
#
# index_partition() splits VERIFIED three ways, by TYPE, against what real
# AWS was measured to do on #1134/#1144:
#
#   regional   2*SCALE + 4   aws_ecs_task_definition, aws_ecs_service (1 each
#                            per scale); aws_ecs_cluster, aws_vpc,
#                            aws_subnet, aws_security_group (1 each, fixed).
#                            Ordinary regional objects: the index holds them
#                            in the region they live in, which is this run's.
#
#   global    20*SCALE + 1   aws_iam_policy, aws_iam_instance_profile (10
#                            each per scale: 6 named-team + 2 count-expanded
#                            + 2 module-nested); aws_route53_zone (1, fixed).
#                            IAM and Route53 are global services and the tag
#                            index holds their objects in us-east-1 ONLY,
#                            whatever region the caller is in (#1144). So
#                            these count toward the target only when this
#                            run's own REGION is us-east-1.
#
#   unindexed 11*SCALE       aws_iam_role (6 named-team + 2 count-expanded +
#                            2 module-nested + 1 service-exec per scale).
#                            GetResources returns NOTHING for iam:role in any
#                            region, while iam:ListRoleTags confirms every
#                            one of them carries tofu-estate (#1134, 550 of
#                            550 at scale 50, stable over 35 minutes). These
#                            are unreachable from the tag index anywhere, so
#                            no bound can ever absorb them.
#
# The split is not an estimate. It reproduces all three real-AWS plateaus on
# record, to the object:
#
#   scale  50, us-east-2 -> 2*50+4  = 104   measured 104 (101 ecs + 3 ec2)
#   scale  50, us-east-1 -> 22*50+5 = 1105  measured 1105 (104 + 1000 iam + 1 zone, unioned)
#   scale 128, us-east-2 -> 2*128+4 = 260   measured 260 (257 ecs + 3 ec2)
#
# DECISION, the one #1143 asks for explicitly: livecert_rgta_count keeps
# querying $REGION alone; it does NOT additionally query us-east-1 for the
# global types. The wait exists to let the index settle before test_plan
# READS it, and what test_plan reads is (a) 4a2's own identity check, the
# same single-region livecert_rgta_count, and (b) choudoufu's own sweep,
# which is region-pinned. Unioning us-east-1 into the target would make the
# run wait on objects nothing downstream of the wait consults.
#
# #1144 HAS NOW LANDED, and this is the comment that said it would be the
# place to record what it decided. Two things, and neither moves this
# function:
#
#   The product's sweep became region-aware, and it did so by KNOWING where
#   the index holds a type rather than by querying a second region for it.
#   internal/live/discovery's taggingAPITypeCoverage records that
#   GetResources holds aws_iam_policy and aws_iam_instance_profile in
#   us-east-1 only, and arnJoinReaches routes accordingly: in us-east-1
#   those two types now ride the estate-wide GetResources call, and outside
#   it they still go to the per-type leg. No cross-region call was added, on
#   either side.
#
#   So the sentence this comment used to carry - "routes every aws_iam_ type
#   away from the tagging leg entirely" - is now FALSE for a us-east-1 run,
#   and that makes the wait MORE load-bearing there, not less: test_plan's
#   sweep reads the index for two of the three IAM types. index_partition
#   already counts the global bucket toward the target only when REGION is
#   us-east-1, which is exactly the condition under which the product now
#   reads it, so the split needs no change. aws_iam_role stays in the
#   unindexed bucket and stays routed away, in every region, on both sides.
index_partition() {
  local region="$1"
  local regional=$(( 2 * SCALE + 4 ))
  local global=$(( 20 * SCALE + 1 ))
  local unindexed=$(( 11 * SCALE ))
  local target=$regional
  if [ "$region" = "us-east-1" ]; then
    target=$(( regional + global ))
  fi
  printf '%s %s %s %s\n' "$target" "$regional" "$global" "$unindexed"
}

# index_partition_is_total returns 0 when the split covers every stamped
# object exactly once, and non-zero with a diagnosis on stdout when it does
# not. The partition MUST be total: if it is not, either terralith-gen's
# composition moved under the VERIFIED formula or the formula moved under the
# split, and either way the target index_wait polls to has stopped being
# derived from anything. A kept-separate function rather than an inline `if`
# so selftest-index-wait.sh can extract it verbatim and prove it red.
index_partition_is_total() {
  local _target regional global unindexed sum
  IFS=' ' read -r _target regional global unindexed <<< "$(index_partition "$1")"
  sum=$(( regional + global + unindexed ))
  if [ "$sum" -ne "$VERIFIED" ]; then
    printf 'index_partition at scale=%s splits VERIFIED into %s regional + %s global + %s unindexed = %s, but VERIFIED is %s - the split and the formula have drifted apart, so index_wait has no derivable target (issue #1143)\n' \
      "$SCALE" "$regional" "$global" "$unindexed" "$sum" "$VERIFIED"
    return 1
  fi
  return 0
}

# Checked at CONFIG time, before a single billable object exists: the whole
# point of #1143 is not to discover a bad target after cold_deploy has
# already spent the money.
INDEX_PARTITION_DIAG="$(index_partition_is_total "$REGION")" \
  || { echo "$INDEX_PARTITION_DIAG" >&2; exit 2; }

log() { printf '%s\n' "$*"; }

# >>> heartbeat block
# ── heartbeat (issue #1324) ─────────────────────────────────────────────
#
# This log is written at stage boundaries only, and the gaps between them
# are hours. Measured on the scale-128 run #1324 was filed from:
#
#   Apply complete! Resources: 9477 added ... in 5633s   <- 1h34m of silence
#   stock-terraform plan run 1: 811s (empty)             <- 13.5m of silence
#
# For 1h34m the file does not grow, so a healthy cold_deploy and a wedged
# one are byte-identical from outside and the only way to tell them apart is
# to attach to the process. That is not a theoretical hazard here: a
# scale-50 run blocked for ~40 minutes at 0% CPU on
# CreatePolicy/EntityAlreadyExists and was unblocked by a hand SIGTERM,
# which is why UNTRUSTED_TEARDOWN_TIMEOUT_S exists at all.
#
# One line per interval naming the stage and its elapsed seconds is enough.
# It is not progress and does not try to be - it is evidence the process is
# alive, which is the one thing the silence takes away.
#
# It runs as its own subshell rather than as anything inside a stage. Two
# reasons: a stage is a straight line of blocking commands with nowhere to
# put a periodic call, and index_wait in particular must keep reading the
# clock exactly as often as it does today, because
# live/live-cert/selftest-index-wait.sh shadows `date` and `sleep` and pins
# its poll and clock-read counts (#1410).
LIVECERT_HEARTBEAT_S="${LIVECERT_HEARTBEAT_S:-60}"
HEARTBEAT_PID=""

# heartbeat_stop is called far more often than heartbeat_start: at every
# stage end, at the start of the next stage, from fail(), from on_signal()
# and from teardown(). A heartbeat that outlives its stage would interleave
# its lines with teardown's, and one that outlives the SCRIPT is worse than
# noise: a background child holding the stdout pipe open makes the Go side's
# cmd.Wait() sit out its whole WaitDelay after the script has already
# exited, turning a finished run into "WaitDelay expired before I/O
# complete" 30 seconds later.
heartbeat_stop() {
  [ -n "$HEARTBEAT_PID" ] || return 0
  kill "$HEARTBEAT_PID" 2>/dev/null
  wait "$HEARTBEAT_PID" 2>/dev/null
  HEARTBEAT_PID=""
}

# heartbeat_start begins a heartbeat for one stage. LIVECERT_HEARTBEAT_S=0
# turns it off entirely, which is what the selftests that count lines do.
heartbeat_start() {
  heartbeat_stop
  case "${LIVECERT_HEARTBEAT_S:-0}" in
    ''|*[!0-9]*) return 0 ;;
    0) return 0 ;;
  esac
  local stage="$1" parent=$$ started
  started="$(date +%s)"
  (
    while :; do
      sleep "$LIVECERT_HEARTBEAT_S"
      # Do not outlive the script. If the parent is gone - killed, or
      # SIGKILLed past its own trap - this subshell exits on its own
      # rather than printing into a pipe nobody is reading.
      kill -0 "$parent" 2>/dev/null || exit 0
      printf 'HEARTBEAT stage=%s elapsed_s=%s\n' "$stage" "$(( $(date +%s) - started ))"
    done
  ) &
  HEARTBEAT_PID=$!
}
# <<< heartbeat block

case "$TARGET" in
  floci) ENDPOINT="http://127.0.0.1:${FLOCI_PORT}" ;;
  aws) ENDPOINT="" ;;
  *) echo "TARGET must be floci or aws, got $TARGET" >&2; exit 2 ;;
esac

# ── teardown ────────────────────────────────────────────────────────────
TEARDOWN_DONE=0
MIGRATE_DONE=0
# Bounds the best-effort "choudoufu's own destroy path" step inside
# teardown() below (issue #1048): that step is explicitly NOT the trusted
# path, and on the 2026-09-11 scale-50 run it blocked for ~40 minutes at 0%
# CPU on CreatePolicy/EntityAlreadyExists, unblocked only by a hand SIGTERM.
# Left alone it would have run to this script's own 25,200s process
# ceiling with the trusted terraform destroy never reached. A few minutes
# is generous for a step whose own job is to finish fast or get out of the
# way; override for a slower account.
UNTRUSTED_TEARDOWN_TIMEOUT_S="${UNTRUSTED_TEARDOWN_TIMEOUT_S:-180}"

# ssm_prefix_count counts parameters under a path. NOT `--query
# 'length(Parameters)'`: the CLI applies that per RESULT PAGE, so a prefix
# holding 66 parameters printed "10\n10\n10\n10\n10\n10\n6" - a string every
# numeric comparison chokes on. On the 2026-09-01 proof run that made a
# working record store report "holds no parameters" (a FALSE failure), and
# the same bug in teardown skipped the delete loop and left 75 parameters
# behind. Counting names line-by-line aggregates across pages correctly.
ssm_prefix_count() {
  livecert_aws ssm get-parameters-by-path --path "$1" --recursive \
    --query 'Parameters[].Name' --output text 2>/dev/null \
    | tr '\t' '\n' | grep -c . || true
}

# s3_prefix_count is ssm_prefix_count's opposite number for the s3 backend:
# same line-counting shape, for the same paging reason, with one extra trap
# of its own.
#
# `Contents` is ABSENT from a list-objects-v2 response that matched nothing,
# where `Parameters` is present-and-empty in the ssm case. JMESPath projects
# a missing key to null, and `--output text` renders null as the literal
# string "None" - so the direct transliteration of ssm_prefix_count returns
# 1 for an empty prefix. Measured against floci on 2026-09-17:
#
#   $ aws s3api list-objects-v2 --bucket B --prefix nothing/here/ \
#       --query 'Contents[].Key' --output text | od -c
#   0000000    N   o   n   e  \n
#
# That number is wrong in the direction that hides both defects this
# function exists for: the values-piece check would pass on a store nothing
# ever wrote to, and teardown's "remaining after delete" would report one
# phantom object forever. `|| `[]`` makes the empty case an empty list,
# which --output text renders as nothing at all.
s3_prefix_count() {
  livecert_aws s3api list-objects-v2 --bucket "$RECORD_STORE_BUCKET" --prefix "$1" \
    --query 'Contents[].Key || `[]`' --output text 2>/dev/null \
    | tr '\t' '\n' | grep -c . || true
}

teardown() {
  [ "$TEARDOWN_DONE" = "1" ] && return 0
  # Before the banner, so no heartbeat line lands in the middle of
  # teardown's own output and nothing is left holding the stdout pipe
  # after this function returns (#1324).
  heartbeat_stop
  log "=== TEARDOWN (target=$TARGET run=$RUN_ID prefix=$PREFIX scale=$SCALE) ==="

  # LIVECERT_HOLD=1 (#1032): the maintainer's own complaint, verbatim -
  # "its not about compute its about the time it takes and it slows down my
  # development" - three scale-50 cycles in one night each spent 35 min on
  # cold_deploy, 40 on migrate and 40 on teardown to look at ONE plan. This
  # applies uniformly to every path that reaches teardown() (a clean finish,
  # fail()'s exit, or a caught signal): whatever stages ran, their resources
  # stay live, and nothing below this block - the untrusted destroy attempt,
  # the trusted stock destroy, the record-store cleanup, verify_empty,
  # sweep, docker rm, and WORK's own removal at the very end - ever runs.
  # HOLD_TAG (set once, near PREFIX/ESTATE above) already marked every
  # GAUNTLET stage detail this run reported with "held: true"; this is the
  # human-facing side of the same fact.
  if [ "${LIVECERT_HOLD:-0}" = "1" ]; then
    log "================================================================"
    log "  LIVECERT_HOLD=1: teardown SKIPPED - every resource this run created is still live and still billing"
    log "    work dir : $WORK"
    log "    estate   : $ESTATE"
    log "    prefix   : $PREFIX"
    log "  resume this estate (skips cold_deploy/migrate, runs from index_wait on):"
    log "    PREFIX=$PREFIX SCALE=$SCALE TARGET=$TARGET REGION=$REGION LIVECERT_RESUME=$WORK bash ${BASH_SOURCE[0]}"
    log "  tear it down later, on its own, once you are done iterating:"
    log "    LIVECERT_TEARDOWN_ONLY=$WORK bash ${BASH_SOURCE[0]}"
    log "  (equivalently: bash ${BASH_SOURCE[0]} teardown $WORK)"
    log "================================================================"
    TEARDOWN_DONE=1
    return 0
  fi

  if [ "$MIGRATE_DONE" = "1" ] && [ -d "$ADOPTED_DIR" ]; then
    log "  attempting choudoufu's own destroy path ($ADOPTED_DIR): best effort, NOT the trusted path - bounded to ${UNTRUSTED_TEARDOWN_TIMEOUT_S}s (#1048) so a hang here can never block or skip the trusted destroy below"
    {
      cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
  live {
    estate = "$ESTATE"
    record_store "$RECORD_STORE_BACKEND" {
$RECORD_STORE_ARGS
    }
  }
}

EOF
      provider_block
    } > "$ADOPTED_DIR/versions.tf"
    ( cd "$ADOPTED_DIR" && timeout "${UNTRUSTED_TEARDOWN_TIMEOUT_S}s" "${TOFU:-}" apply -input=false -auto-approve -no-color ) \
      > "$WORK/teardown_choudoufu_destroy.out" 2>&1
    cd_rc=$?
    if [ "$cd_rc" -eq 124 ]; then
      log "    exit=124: TIMED OUT after ${UNTRUSTED_TEARDOWN_TIMEOUT_S}s (see $WORK/teardown_choudoufu_destroy.out) - not trusted alone, and never trusted to block what follows; proceeding to the trusted destroy regardless"
    else
      log "    exit=$cd_rc (see $WORK/teardown_choudoufu_destroy.out) - not trusted alone"
    fi
    [ "$cd_rc" -ne 0 ] && tail -15 "$WORK/teardown_choudoufu_destroy.out" | sed 's/^/    | /'
  fi

  if [ -d "$COLD_DIR" ] && [ -f "$COLD_DIR/terraform.tfstate" ]; then
    log "  destroying the plain stock state ($COLD_DIR) - the primary, trusted path: valid the instant cold_deploy finishes, untouched by anything migrate/test_plan/test_apply do afterward"
    ( cd "$COLD_DIR" && AWS_ENDPOINT_URL="$ENDPOINT" "${TF_COLD:-terraform}" destroy -input=false -auto-approve -no-color -parallelism=5 ) \
      > "$WORK/teardown_stock_destroy.out" 2>&1
    sd_rc=$?
    log "    exit=$sd_rc (see $WORK/teardown_stock_destroy.out) - not trusted alone, verifying by listing next"
    [ "$sd_rc" -ne 0 ] && tail -30 "$WORK/teardown_stock_destroy.out" | sed 's/^/    | /'
  fi
  # #1048: the guard now marks completion of the TRUSTED destroy attempt
  # above, not entry into this function. It used to be set at the very top,
  # before either destroy path ran, so a second signal arriving while THIS
  # call was still blocked inside the (formerly unbounded) untrusted step
  # made a re-entrant teardown() call return immediately at the guard -
  # skipping the trusted destroy on every invocation, not just the first.
  # It is set here unconditionally (whether or not COLD_DIR existed to
  # destroy) because by this point the trusted destroy has been attempted
  # to the extent it ever will be for this run; everything below is
  # best-effort verification/cleanup that is safe to repeat.
  TEARDOWN_DONE=1

  # The record store is not tagged and no destroy reaches it, so it needs its
  # own teardown. Doing it here rather than in sweep() because it must run on
  # every exit path, including a run that never reached test_plan.
  #
  # Three-way, with a loud default (#1145). The two namespaces are deleted
  # separately because the guided-discovery hint does not live under the
  # configured key_prefix - see HINT_SSM_PREFIX/HINT_S3_PREFIX above. The
  # TARGET=aws gate the ssm arm used to carry is gone with it: both arms now
  # go through livecert_aws, which addresses floci's endpoint on a floci run
  # and the account on an aws one, so the cleanup a real run will do is the
  # cleanup an emulator run exercises.
  case "$RECORD_STORE_BACKEND" in
    local)
      # Nothing to delete out of band: the local store is ".tofu-records"
      # inside $ADOPTED_DIR, which is inside $WORK, which this function
      # removes wholesale at its very end (LIVECERT_KEEP_WORK=1 opts out,
      # and then the records are meant to still be there).
      log "  record store (local disk): a directory inside \$WORK, removed with it at the end of teardown unless LIVECERT_KEEP_WORK=1; nothing to delete out of band"
      ;;
    ssm)
      rs_left="$(ssm_prefix_count "$SSM_PREFIX")"
      log "  record store (ssm $SSM_PREFIX): $rs_left parameter(s) to delete"
      if [ "${rs_left:-0}" -gt 0 ]; then
        livecert_aws ssm get-parameters-by-path --path "$SSM_PREFIX" --recursive \
          --query 'Parameters[].Name' --output text 2>/dev/null | tr '\t' '\n' \
          | while read -r n; do [ -n "$n" ] && livecert_aws ssm delete-parameter --name "$n" >/dev/null 2>&1; done
        log "    remaining after delete: $(ssm_prefix_count "$SSM_PREFIX")"
      fi
      hint_left="$(ssm_prefix_count "$HINT_SSM_PREFIX")"
      log "  guided-discovery hint (ssm $HINT_SSM_PREFIX): $hint_left parameter(s) to delete"
      if [ "${hint_left:-0}" -gt 0 ]; then
        livecert_aws ssm get-parameters-by-path --path "$HINT_SSM_PREFIX" --recursive \
          --query 'Parameters[].Name' --output text 2>/dev/null | tr '\t' '\n' \
          | while read -r n; do [ -n "$n" ] && livecert_aws ssm delete-parameter --name "$n" >/dev/null 2>&1; done
        log "    remaining after delete: $(ssm_prefix_count "$HINT_SSM_PREFIX")"
      fi
      ;;
    s3)
      # `s3 rm --recursive` rather than a per-key delete-object loop: it
      # batches 1000 keys per DeleteObjects request and pages the listing
      # itself, which is what makes this survivable at the 10k rung the s3
      # backend exists for. It exits 0 on a prefix that matches nothing.
      # The BUCKET is the operator's and is never deleted - only the two key
      # namespaces this run wrote.
      rs_left="$(s3_prefix_count "$S3_PREFIX")"
      log "  record store (s3 s3://$RECORD_STORE_BUCKET/$S3_PREFIX): $rs_left object(s) to delete"
      if [ "${rs_left:-0}" -gt 0 ]; then
        livecert_aws s3 rm "s3://$RECORD_STORE_BUCKET/$S3_PREFIX" --recursive >/dev/null 2>&1
        log "    remaining after delete: $(s3_prefix_count "$S3_PREFIX")"
      fi
      hint_left="$(s3_prefix_count "$HINT_S3_PREFIX")"
      log "  guided-discovery hint (s3 s3://$RECORD_STORE_BUCKET/$HINT_S3_PREFIX): $hint_left object(s) to delete"
      if [ "${hint_left:-0}" -gt 0 ]; then
        livecert_aws s3 rm "s3://$RECORD_STORE_BUCKET/$HINT_S3_PREFIX" --recursive >/dev/null 2>&1
        log "    remaining after delete: $(s3_prefix_count "$HINT_S3_PREFIX")"
      fi
      ;;
    *)
      log "  record store: UNKNOWN backend \"$RECORD_STORE_BACKEND\" - nothing was deleted; whatever this run wrote is still there"
      ;;
  esac

  if verify_empty; then
    log "  VERIFIED EMPTY by listing: nothing matching prefix=$PREFIX or tag tofu-cert-run=$RUN_ID remains"
  else
    log "  destroy path(s) left resources behind - running the raw-CLI sweep as the belt-and-suspenders fallback"
    sweep
    if verify_empty; then
      log "  VERIFIED EMPTY by listing after the sweep"
    else
      log "  STILL NOT EMPTY after destroy and sweep - see the listing above; PREFIX=$PREFIX RUN_ID=$RUN_ID, retry teardown with the SAME values"
    fi
  fi

  if [ "$TARGET" = "floci" ]; then
    gauntlet_floci_teardown "$FLOCI_NAME"
  fi

  # Every number this run needs (stage verdicts, timings, throttle/retry/
  # pagination counts) is already on stdout above, via gauntlet_stage and
  # the THROTTLE SUMMARY block - WORK (including the TF_LOG=DEBUG captures)
  # is scratch, not evidence, and LIVECERT_KEEP_WORK=1 opts out for a
  # human who wants to inspect a debug log by hand after a run.
  if [ "${LIVECERT_KEEP_WORK:-0}" != "1" ]; then
    rm -rf "$WORK"
  else
    log "  LIVECERT_KEEP_WORK=1: leaving $WORK in place"
  fi
}

CURRENT_STAGE=""
fail() {
  heartbeat_stop
  printf 'FAIL: %s\n' "$*" >&2
  [ -n "$CURRENT_STAGE" ] && gauntlet_stage "$CURRENT_STAGE" fail "$*$HOLD_TAG"
  exit 1
}

APPLY_PID=""
on_signal() {
  local sig="$1"
  heartbeat_stop
  log "=== caught $sig - forwarding to in-flight child (pid ${APPLY_PID:-none}) and tearing down ==="
  if [ -n "$APPLY_PID" ] && kill -0 "$APPLY_PID" 2>/dev/null; then
    kill -TERM "$APPLY_PID" 2>/dev/null || true
    wait "$APPLY_PID" 2>/dev/null || true
  fi
  teardown
  trap - EXIT INT TERM
  exit 130
}
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap teardown EXIT
gauntlet_begin

# ══════════════════════════════════════════════════════════════════════
# helpers: provider_block, generate_estate, verify_empty, sweep
# ══════════════════════════════════════════════════════════════════════

provider_block() {
  if [ "$TARGET" = "floci" ]; then
    cat <<EOF
provider "aws" {
  region                      = "$REGION"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
  default_tags {
    tags = {
      tofu-cert-run = "$RUN_ID"
    }
  }
}
EOF
  else
    cat <<EOF
provider "aws" {
  region = "$REGION"
  default_tags {
    tags = {
      tofu-cert-run = "$RUN_ID"
    }
  }
}
EOF
  fi
}

# analyze_debug_log reads a TF_LOG=DEBUG capture and prints four
# space-separated numbers: byte size, throttling-error line count, GENUINE
# retry line count, and pagination-continuation line count. Prints to
# stdout only - the caller assigns via `read -r a b c d <<< "$(...)"`.
#
# The retry pattern is deliberately narrower than a first draft
# (`retryable error`) that this script shipped and then caught against its
# own floci proving run (2026-08-29/30): every "retry" hit at floci was
# actually the substring "retry" inside "unretryable error" - the AWS
# provider's own log line for a permanently-failed call (a 404 for a type
# floci does not emulate, or floci's own dummy-credential STS/IAM 404s),
# the OPPOSITE of a retried call. `grep -v unretryable` removes exactly
# that false-positive class while still counting whatever a genuine retry
# line says (this codebase's own retry logging was never observed to fire
# against floci - floci does not throttle, which is this issue's whole
# premise - so this pattern is written from AWS's/the SDK's own vocabulary
# for a retried call, not reverse-engineered from a sample that doesn't
# exist yet).
analyze_debug_log() {
  local f="$1"
  local bytes throttle retry pagination
  bytes="$(wc -c < "$f" | tr -d ' ')"
  # Known bound, deliberately not "fixed": this counts LINES, and a raw XML
  # error body that puts <Code>Throttling</Code> and <Message>Rate
  # exceeded</Message> on separate lines is caught once, by the message. A
  # body carrying the code and no recognised message counts zero. Adding
  # '<Code>Throttling</Code>' to the alternation was tried and reverted: it
  # makes the common two-line body count 2 for one throttle, which corrupts
  # the number far more than the gap it closes. The count is a lower bound,
  # and it is applied identically to both binaries, so a comparison between
  # them stays sound even where the absolute is short.
  throttle="$(grep -cE 'ThrottlingException|Throttling:|TooManyRequestsException|Rate exceeded|PriorRequestNotComplete|RequestLimitExceeded' "$f" 2>/dev/null || true)"
  retry="$(grep -iE 'retry|retrying|backoff|backing off' "$f" 2>/dev/null | grep -vi 'unretryable' | wc -l | tr -d ' ')"
  pagination="$(grep -icE 'NextToken|Marker=|IsTruncated=true|nextMarker' "$f" 2>/dev/null || true)"
  printf '%s %s %s %s\n' "${bytes:-0}" "${throttle:-0}" "${retry:-0}" "${pagination:-0}"
}

# generate_estate writes a fresh terralith-gen output into $1, then corrects
# the two things it gets right for floci/us-east-1 but wrong for a real,
# non-us-east-1 account (see this file's own doc comment, point 2): the
# provider block (skip_requesting_account_id must be false against real AWS
# - #572) and network.tf's hardcoded "us-east-1a" availability zone (must be
# a real AZ in $REGION).
generate_estate() {
  local dir="$1"
  ( cd "$ROOT" && env -u PWD go run ./tools/terralith-gen -scale "$SCALE" -prefix "$PREFIX" -out "$dir" -fmt-bin "" ) \
    > "$WORK/terralith_gen.out" 2>&1 || { cat "$WORK/terralith_gen.out"; fail "terralith-gen failed"; }
  cat "$WORK/terralith_gen.out"

  # Replace the generated provider block wholesale with provider_block's
  # target-appropriate one; keep the generated required_providers block
  # (the version pin belongs to terralith-gen, not this script).
  sed -n '1,/^provider "aws" {$/p' "$dir/versions.tf" | sed '$d' > "$dir/versions.tf.new"
  provider_block >> "$dir/versions.tf.new"
  mv "$dir/versions.tf.new" "$dir/versions.tf"

  # network.tf: "us-east-1a" -> "${REGION}a" - only a real issue for
  # TARGET=aws (floci does not validate AZs), but corrected unconditionally
  # so the SAME generated config is what both targets run, per #546's own
  # rule that stock and choudoufu numbers (and, here, floci and aws runs)
  # come from the same estate or are not reported as a comparison.
  sed -i.bak "s/us-east-1a/${REGION}a/" "$dir/network.tf" && rm -f "$dir/network.tf.bak"

  # iam.tf: terralith-gen's "scoped team" roles trust a SYNTHETIC 12-digit
  # account id (tools/terralith-gen/templates.go: crossAccountID,
  # 100000000000+i) as a cross-account Principal. That id is
  # syntactically valid but does not belong to any real AWS account, and
  # real IAM's trust-policy validation rejects a Principal naming an
  # account it cannot confirm exists - "MalformedPolicyDocument: Invalid
  # principal in policy" (issue #567's live-AWS run, 2026-08-30; floci
  # never validates this and accepted every synthetic id). This is a
  # real-AWS-specific validation rule, not a malformed-output bug the way
  # the TXT record double-quoting fix (tools/terralith-gen/gen.go,
  # writeRecords) was, so it is corrected HERE - the same "self-owned,
  # live-cert-specific" line this file already draws for the AZ and
  # skip_requesting_account_id fixes above - rather than in terralith-gen
  # itself: every synthetic id is replaced with the CALLER's own real
  # account id (definitely exists), which trades away the per-team
  # uniqueness terralith-gen's own duplication counters credit this trust
  # block with (GENERATED.md's duplication percentage, computed before
  # this patch runs, still describes the pre-patch generated content
  # accurately - it is just no longer what actually gets applied here).
  sed -i.bak -E "s/arn:aws:iam::1[0-9]{11}:root/arn:aws:iam::${CALLER_ACCOUNT_ID}:root/g" "$dir/iam.tf" && rm -f "$dir/iam.tf.bak"
}

# ── IAM role headroom (issue #1230) ─────────────────────────────────────
#
# The one quota this estate is known to have run into from outside a run
# (#1150: "their combined 1,100 IAM roles"), checked before cold_deploy has
# created anything, so exhaustion reads as a refusal of this rung rather
# than as the product failing to deploy. See the header's item 4 for the
# framing; this comment is about the mechanics.
#
# The need side is NOT a constant in this file. EXPECTED/VERIFIED above are
# formulas that track tools/terralith-gen by hand and carry a MUST-update
# note; a roles-per-scale constant typed here from the incident's numbers
# would be exactly the false-verdict hazard #1230 names, in the other
# direction. iam_roles_needed asks the generator (`-iam-roles`, backed by
# IAMRoleInstances and TestIAMRoleInstancesMatchGeneratedHCL, which
# expands the generated HCL's count/for_each and pins the result), so the
# number here moves when the generator's does and nowhere else.
#
# The account side is `aws iam get-account-summary`, one free call, whose
# SummaryMap carries Roles and RolesQuota. `--output text` on a two-element
# projection prints them tab-separated on one line; a missing key prints
# "None", which the integer checks below turn into fail-open.
#
# Fail OPEN, loudly, on anything that stops the check from running. A
# refusal is recorded on the ladder as this scale's outcome (#1151), and a
# refusal minted from a throttled or misconfigured IAM call would be a
# false record of the same kind cold_deploy's LimitExceeded was. The check
# exists to catch the case that happened, not to gate every run on a second
# API being up. The line it prints when it steps aside is deliberately
# hard to miss and says why, so a later LimitExceeded has its explanation
# in the same log.
#
# Two functions rather than one so live/live-cert/selftest-iam-headroom.sh
# can extract iam_role_headroom_check between the markers below and drive
# it with a stubbed iam_roles_needed and a fake `aws` on PATH: no go build,
# no account, and - per #1380 - never by executing this script. The gate
# that calls it sits between its own markers just before cold_deploy, and
# the selftest runs that span too, with a fake `terraform` behind it that
# must never be reached on the refusal arm.
# >>> iam role headroom check
iam_roles_needed() {
  ( cd "$ROOT" && env -u PWD go run ./tools/terralith-gen -scale "$SCALE" -iam-roles ) 2>"$WORK/iam_roles_needed.err"
}

iam_role_headroom_check() {
  local need summary roles quota headroom why=""
  log "=== 0c. iam role headroom: room in the account for scale=$SCALE's aws_iam_role instances? (#1230) ==="
  need="$(iam_roles_needed)" \
    || why="terralith-gen -iam-roles exited non-zero: $(tr '\n' ' ' < "$WORK/iam_roles_needed.err" 2>/dev/null)"
  if [ -z "$why" ]; then
    case "$need" in
      ''|*[!0-9]*) why="terralith-gen -iam-roles printed '$need', not an integer" ;;
    esac
  fi
  if [ -z "$why" ]; then
    summary="$(livecert_aws iam get-account-summary --query 'SummaryMap.[Roles,RolesQuota]' --output text 2>&1)" \
      || why="aws iam get-account-summary failed: $(printf '%s' "$summary" | tr '\n' ' ')"
  fi
  if [ -z "$why" ]; then
    read -r roles quota _ <<< "$(printf '%s' "$summary" | tr '\t\n' '  ')"
    case "${roles:-}" in
      ''|*[!0-9]*) why="aws iam get-account-summary printed Roles='${roles:-}', not an integer (raw: $(printf '%s' "$summary" | tr '\t\n' '  '))" ;;
    esac
  fi
  if [ -z "$why" ]; then
    case "${quota:-}" in
      ''|*[!0-9]*) why="aws iam get-account-summary printed RolesQuota='${quota:-}', not an integer (raw: $(printf '%s' "$summary" | tr '\t\n' '  '))" ;;
    esac
  fi
  if [ -n "$why" ]; then
    log "  IAM ROLE HEADROOM CHECK COULD NOT RUN - $why"
    log "  failing OPEN: continuing into cold_deploy without it, so an AWS blip cannot become a refusal on the ladder (#1230); if cold_deploy fails on LimitExceeded for roles, this is why it was not caught here"
    return 0
  fi
  headroom=$(( quota - roles ))
  if [ "$headroom" -lt 0 ]; then headroom=0; fi
  if [ "$need" -gt "$headroom" ]; then
    gauntlet_refused "$SCALE" "$need" "$headroom" iam-roles \
      "the account holds $roles of its $quota IAM roles (aws iam get-account-summary Roles/RolesQuota), leaving room for $headroom, and terralith-gen -scale $SCALE creates $need aws_iam_role instances; nothing was created and nothing is torn down (#1230)"
    return 1
  fi
  log "  ok: the account holds $roles of its $quota IAM roles, room for $headroom; scale=$SCALE needs $need"
  return 0
}
# <<< iam role headroom check

# verify_empty lists, independently of any destroy command's exit code,
# whether anything this run created still exists - scoped by name PREFIX
# (every resource this estate creates, taggable or not) and cross-checked
# by the tofu-cert-run tag (the taggable half, via resourcegroupstaggingapi,
# informational the same way live-cert.sh's own livecert_verify_empty
# treats it - never the sole gate).
# checked_list runs a listing command and reports through the SAME channel
# whether it failed (dirty: fail-SAFE, never fail-open) or came back
# non-empty (dirty) or came back empty and clean (not dirty). This exists
# because the first draft of this function used `... 2>/dev/null || true`
# on every call, which cannot tell "confirmed empty" apart from "the AWS
# CLI call itself errored" (a dropped endpoint, a throttled list call) -
# discovered empirically building this script (2026-08-29/30): killing the
# floci container mid-teardown made every per-service check fail with
# "connection refused", `|| true` swallowed every one of them, and the
# function reported VERIFIED EMPTY on an account it had not actually been
# able to check at all. Never report empty on an error - HANDOFF's mirror
# rule for the spend side: leaving nothing live is checked, not assumed,
# and a check that cannot run is not a check that passed.
#
# Sets the caller's DIRTY=1 if the command failed (prints its stderr) or
# its filtered output was non-empty (prints the output); leaves DIRTY
# untouched otherwise. Args: <label> <command...>
checked_list() {
  local label="$1"; shift
  local out err rc
  err="$(mktemp)"
  out="$("$@" 2>"$err")"; rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '  verify_empty: %s CHECK FAILED (exit %s) - treating as NOT verified empty: %s\n' "$label" "$rc" "$(tr '\n' ' ' < "$err")"
    DIRTY=1
  elif [ -n "$out" ]; then
    printf '  verify_empty: live %s: %s\n' "$label" "$out"
    DIRTY=1
  fi
  rm -f "$err"
}

verify_empty() {
  DIRTY=0

  local rgta_n
  rgta_n="$(livecert_rgta_count tofu-cert-run "$RUN_ID")"
  if [ "${rgta_n:-0}" != "0" ]; then
    printf '  verify_empty: resourcegroupstaggingapi reports %s resource(s) tagged tofu-cert-run=%s (informational - per-service checks below gate the verdict)\n' "$rgta_n" "$RUN_ID"
  fi

  checked_list "IAM role(s)" livecert_aws iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text
  checked_list "IAM customer-managed policy(ies)" livecert_aws iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].PolicyName" --output text
  checked_list "IAM instance profile(s)" livecert_aws iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text
  checked_list "Route53 zone(s)" livecert_aws route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text
  checked_list "subnet(s)" livecert_aws ec2 describe-subnets --filters "Name=tag:Name,Values=${PREFIX}-subnet" --query 'Subnets[].SubnetId' --output text
  checked_list "security group(s)" livecert_aws ec2 describe-security-groups --filters "Name=group-name,Values=${PREFIX}-ecs-sg" --query 'SecurityGroups[].GroupId' --output text
  checked_list "vpc(s)" livecert_aws ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[].VpcId' --output text

  # ECS cluster/task-definition listing needs client-side filtering
  # (list-clusters has no name filter; list-task-definitions' own
  # --family-prefix IS server-side, used directly) - wrapped in a function
  # so checked_list's rc/output contract still applies to the pipeline as a
  # whole.
  ecs_clusters_for_prefix() {
    local all rc
    all="$(livecert_aws ecs list-clusters --query 'clusterArns' --output text)"; rc=$?
    [ "$rc" -eq 0 ] || return "$rc"
    # grep's own "no match" exit (1) is a valid empty result, not a
    # failure - explicit `return 0` after it means THIS function's exit
    # status only ever reflects list-clusters' own rc, never grep's.
    printf '%s\n' "$all" | tr '\t' '\n' | grep -F "/${PREFIX}-cluster"
    return 0
  }
  # The record store is part of "empty" (#1145). Before this, verify_empty
  # named only the estate's own AWS resources, so "VERIFIED EMPTY by listing:
  # nothing matching prefix=$PREFIX ... remains" was printed over a store
  # still holding every record this run wrote - objects whose keys begin with
  # that very prefix. Both namespaces are listed, for the same reason
  # teardown deletes both. Each is an independent listing, not a re-read of
  # the counts teardown already printed, so a delete that silently did
  # nothing is caught here rather than believed.
  case "$RECORD_STORE_BACKEND" in
    local) ;;
    ssm)
      checked_list "record store parameter(s) under $SSM_PREFIX" \
        livecert_aws ssm get-parameters-by-path --path "$SSM_PREFIX" --recursive --query 'Parameters[].Name' --output text
      checked_list "guided-discovery hint parameter(s) under $HINT_SSM_PREFIX" \
        livecert_aws ssm get-parameters-by-path --path "$HINT_SSM_PREFIX" --recursive --query 'Parameters[].Name' --output text
      ;;
    s3)
      checked_list "record store object(s) under s3://$RECORD_STORE_BUCKET/$S3_PREFIX" \
        livecert_aws s3api list-objects-v2 --bucket "$RECORD_STORE_BUCKET" --prefix "$S3_PREFIX" --query 'Contents[].Key || `[]`' --output text
      checked_list "guided-discovery hint object(s) under s3://$RECORD_STORE_BUCKET/$HINT_S3_PREFIX" \
        livecert_aws s3api list-objects-v2 --bucket "$RECORD_STORE_BUCKET" --prefix "$HINT_S3_PREFIX" --query 'Contents[].Key || `[]`' --output text
      ;;
    *)
      printf '  verify_empty: UNKNOWN record store backend "%s" - the store was not checked, so this run is NOT verified empty\n' "$RECORD_STORE_BACKEND"
      DIRTY=1
      ;;
  esac

  checked_list "ECS cluster(s)" ecs_clusters_for_prefix
  checked_list "ACTIVE ECS task definition(s) (deregistering these is not required for emptiness - they are free and AWS retains INACTIVE families - but ACTIVE ones would mean the estate config was never removed)" \
    livecert_aws ecs list-task-definitions --family-prefix "${PREFIX}-svc-" --status ACTIVE --query 'taskDefinitionArns' --output text

  [ "$DIRTY" = "0" ]
}

# sweep force-deletes everything this run's PREFIX or tofu-cert-run tag
# names, with NO tofu/terraform involved. Order matches AWS's own
# dependency requirements: detach/delete a role's policies before the role,
# detach a policy from every entity before deleting the policy, empty a
# zone of its own (non-NS/SOA) records before deleting the zone, remove ECS
# services before the cluster, subnet/vpc last (mirrors live-cert.sh's own
# livecert_sweep for the network layer this estate shares with
# reference-ec2-vpc). Every step is best-effort (`|| true`).
sweep() {
  printf '  sweep: force-deleting everything named %s-* or tagged tofu-cert-run=%s\n' "$PREFIX" "$RUN_ID"

  local roles
  roles="$(livecert_aws iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text 2>/dev/null || true)"
  for role in $roles; do
    printf '    role %s: detaching managed policies\n' "$role"
    for arn in $(livecert_aws iam list-attached-role-policies --role-name "$role" --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null || true); do
      livecert_aws iam detach-role-policy --role-name "$role" --policy-arn "$arn" >/dev/null 2>&1 || true
    done
    printf '    role %s: deleting inline policies\n' "$role"
    for pname in $(livecert_aws iam list-role-policies --role-name "$role" --query 'PolicyNames' --output text 2>/dev/null || true); do
      livecert_aws iam delete-role-policy --role-name "$role" --policy-name "$pname" >/dev/null 2>&1 || true
    done
    printf '    role %s: removing from instance profiles\n' "$role"
    for prof in $(livecert_aws iam list-instance-profiles-for-role --role-name "$role" --query 'InstanceProfiles[].InstanceProfileName' --output text 2>/dev/null || true); do
      livecert_aws iam remove-role-from-instance-profile --instance-profile-name "$prof" --role-name "$role" >/dev/null 2>&1 || true
    done
    printf '    deleting role %s\n' "$role"
    livecert_aws iam delete-role --role-name "$role" >/dev/null 2>&1 || true
  done

  local profiles
  profiles="$(livecert_aws iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text 2>/dev/null || true)"
  for prof in $profiles; do
    printf '    deleting instance profile %s\n' "$prof"
    livecert_aws iam delete-instance-profile --instance-profile-name "$prof" >/dev/null 2>&1 || true
  done

  local policies
  policies="$(livecert_aws iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].Arn" --output text 2>/dev/null || true)"
  for parn in $policies; do
    for role in $(livecert_aws iam list-entities-for-policy --policy-arn "$parn" --entity-filter Role --query 'PolicyRoles[].RoleName' --output text 2>/dev/null || true); do
      livecert_aws iam detach-role-policy --role-name "$role" --policy-arn "$parn" >/dev/null 2>&1 || true
    done
    printf '    deleting policy %s\n' "$parn"
    livecert_aws iam delete-policy --policy-arn "$parn" >/dev/null 2>&1 || true
  done

  local clusterarn
  clusterarn="$(livecert_aws ecs list-clusters --query 'clusterArns' --output text 2>/dev/null | tr '\t' '\n' | grep -F "/${PREFIX}-cluster" || true)"
  if [ -n "$clusterarn" ]; then
    for svcarn in $(livecert_aws ecs list-services --cluster "$clusterarn" --query 'serviceArns' --output text 2>/dev/null | tr '\t' '\n' || true); do
      printf '    deleting ECS service %s\n' "$svcarn"
      livecert_aws ecs update-service --cluster "$clusterarn" --service "$svcarn" --desired-count 0 >/dev/null 2>&1 || true
      livecert_aws ecs delete-service --cluster "$clusterarn" --service "$svcarn" --force >/dev/null 2>&1 || true
    done
    printf '    deleting ECS cluster %s\n' "$clusterarn"
    livecert_aws ecs delete-cluster --cluster "$clusterarn" >/dev/null 2>&1 || true
  fi

  local zoneid
  zoneid="$(livecert_aws route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text 2>/dev/null || true)"
  if [ -n "$zoneid" ]; then
    printf '    emptying and deleting Route53 zone %s\n' "$zoneid"
    local recs
    recs="$(livecert_aws route53 list-resource-record-sets --hosted-zone-id "$zoneid" \
      --query "ResourceRecordSets[?Type!='NS' && Type!='SOA']" --output json 2>/dev/null || echo '[]')"
    if [ "$recs" != "[]" ] && [ -n "$recs" ] && command -v jq >/dev/null 2>&1; then
      jq '{Changes: [.[] | {Action: "DELETE", ResourceRecordSet: .}]}' <<< "$recs" \
        > "$WORK/sweep_record_delete_batch.json" 2>/dev/null || true
      if [ -s "$WORK/sweep_record_delete_batch.json" ]; then
        livecert_aws route53 change-resource-record-sets --hosted-zone-id "$zoneid" \
          --change-batch "file://$WORK/sweep_record_delete_batch.json" >/dev/null 2>&1 || true
      fi
    fi
    livecert_aws route53 delete-hosted-zone --id "$zoneid" >/dev/null 2>&1 || true
  fi

  local subnets
  subnets="$(livecert_aws ec2 describe-subnets --filters "Name=tag:Name,Values=${PREFIX}-subnet" --query 'Subnets[].SubnetId' --output text 2>/dev/null || true)"
  for sn in $subnets; do
    printf '    deleting subnet %s\n' "$sn"
    livecert_aws ec2 delete-subnet --subnet-id "$sn" >/dev/null 2>&1 || true
  done

  local sgs
  sgs="$(livecert_aws ec2 describe-security-groups --filters "Name=group-name,Values=${PREFIX}-ecs-sg" --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || true)"
  for sg in $sgs; do
    printf '    deleting security group %s\n' "$sg"
    livecert_aws ec2 delete-security-group --group-id "$sg" >/dev/null 2>&1 || true
  done

  local vpcs
  vpcs="$(livecert_aws ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[].VpcId' --output text 2>/dev/null || true)"
  for vpc in $vpcs; do
    printf '    deleting vpc %s\n' "$vpc"
    livecert_aws ec2 delete-vpc --vpc-id "$vpc" >/dev/null 2>&1 || true
  done
}

# ── teardown-only dispatch, part 2 (issue #1032) ───────────────────────
# Part 1 (right after this script's own `source` lines, above) seeded
# PREFIX/RUN_ID/TARGET/REGION/RECORD_STORE_BACKEND/SCALE, and therefore
# ESTATE/RECORD_STORE_ARGS/SSM_PREFIX/WORK/COLD_DIR/ADOPTED_DIR, from the
# work dir's own cold-deploy marker. Everything teardown()/verify_empty()/
# sweep() need is now defined above and the spend guard (the TARGET case
# block, above) has already run - so this is the earliest point this
# dispatch can actually tear anything down, and the latest point it can do
# so before "0. tools" below builds a binary and starts a floci container
# that a plain teardown has no use for. Deliberately does NOT attempt the
# best-effort "choudoufu's own destroy path" step inside teardown(): that
# step rebuilds $ADOPTED_DIR/versions.tf from a live TOFU binary and a
# fully-reconstructed live block, which this dispatch has no reason to pay
# for when the trusted stock destroy plus the independent verify-empty
# listing (and the raw-CLI sweep, if anything survives) is what "tear this
# down" actually needs.
# >>> teardown-only dispatch part 2
if [ -n "$TEARDOWN_ONLY_DIR" ]; then
  log "=== teardown-only: $TEARDOWN_ONLY_DIR (target=$TARGET region=$REGION prefix=$PREFIX scale=$SCALE run_id=$RUN_ID) ==="
  log "  running the bounded, trusted stock destroy plus the verify-empty listing only - not the best-effort choudoufu destroy path (see this dispatch's own comment above)"
  command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
  command -v "${TF_COLD_BIN:-terraform}" >/dev/null 2>&1 || fail "${TF_COLD_BIN:-terraform} is not on PATH (needed for the trusted stock destroy)"
  command -v timeout >/dev/null 2>&1 || fail "timeout is not on PATH"
  TF_COLD="${TF_COLD_BIN:-terraform}"
  MIGRATE_DONE=0   # deliberately: see this dispatch's own comment above
  LIVECERT_HOLD=0  # teardown-only means tear down NOW, even if LIVECERT_HOLD=1 is still set in the environment from the run that created this work dir
  teardown
  trap - EXIT INT TERM
  log "=== teardown-only: done ==="
  exit 0
fi
# <<< teardown-only dispatch part 2

# ── 0. tools ────────────────────────────────────────────────────────────
log "=== 0. tools (target=$TARGET run_id=$RUN_ID prefix=$PREFIX scale=$SCALE) ==="
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v "${TF_COLD_BIN:-terraform}" >/dev/null 2>&1 || fail "${TF_COLD_BIN:-terraform} is not on PATH (needed for cold_deploy's stock apply)"
command -v timeout >/dev/null 2>&1 || fail "timeout is not on PATH (needed to bound teardown's best-effort destroy step, #1048)"
TF_COLD="${TF_COLD_BIN:-terraform}"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH (needed to build choudoufu)"
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

if [ "$TARGET" = "floci" ]; then
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
  docker info >/dev/null 2>&1 || fail "docker is not running"
fi

# ── 0b. the endpoint ────────────────────────────────────────────────────
if [ "$TARGET" = "floci" ]; then
  log "=== 0b. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
  docker run -d -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
    || fail "docker run for $FLOCI_NAME failed"
  healthy=0
  for _ in $(seq 1 45); do
    H="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
    case "$H" in *'"ec2":"running"'*) healthy=1; break ;; esac
    sleep 2
  done
  [ "$healthy" = "1" ] || fail "floci did not come up healthy (ec2) at $ENDPOINT"
  log "  healthy"
  export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION" AWS_ENDPOINT_URL="$ENDPOINT"
  CALLER_ACCOUNT_ID="$(livecert_aws sts get-caller-identity --query Account --output text 2>/dev/null || echo 000000000000)"
else
  log "=== 0b. target=aws, region=$REGION - using the ambient AWS credential chain, no endpoint override ==="
  unset AWS_ENDPOINT_URL || true
  export AWS_REGION="$REGION"
  IDENTITY="$(aws sts get-caller-identity --query Account --output text 2>&1)" \
    || fail "aws sts get-caller-identity failed - no usable credentials for a real run: $IDENTITY"
  log "  caller account ...${IDENTITY: -4} (only the last 4 digits are ever logged or recorded)"
  CALLER_ACCOUNT_ID="$IDENTITY"
fi

# ══════════════════════════════════════════════════════════════════════
# LIVECERT_RESUME (#1032): verify a held work dir before trusting it, then
# skip cold_deploy and migrate and run from index_wait on. See this file's
# own doc comment (top) for what it is for and what it does not attempt
# (a resumed run's cold_deploy/migrate stages are logged skipped, never
# pass - never GAUNTLET protocol lines, never in live/gauntlet.json).
#
# Verified by READING, not by trusting a directory's mere existence: the
# stock state's own resource count (the cold-deploy marker, written right
# after cold_deploy's own "Apply complete! Resources: N added" assertion
# already confirmed it) and the migrate stage's own record (the migrate
# marker, written right after live-import -approve's own stamp-count
# assertion already confirmed it) - both markers are written by a
# COMPLETED, already-verified stage in THIS SAME script, never invented
# here, so this step re-reads evidence rather than re-deriving it.
#
# Refuses outright on any PREFIX or SCALE mismatch between what the work
# dir recorded and what this run's own environment names: the resumed
# state names real objects (IAM roles, an S3-incompatible Route 53 zone,
# an ECS cluster) by PREFIX, so proceeding on a mismatch would silently
# plan (or, worse, later tear down) a PREFIX this run never touched.
#
# Kept as its own function, the same reason index_wait() and teardown() are
# (see their own comments above): so selftest-hold-resume.sh can extract it
# verbatim and drive it against fixture marker files - no AWS calls, no
# docker, no terraform, no go build.
# ══════════════════════════════════════════════════════════════════════
resume_verify() {
  log "=== resume: verifying $LIVECERT_RESUME holds a completed cold_deploy + migrate for prefix=$PREFIX scale=$SCALE ==="
  COLD_MARKER="$WORK/.livecert-cold-state"
  MIGRATE_MARKER="$WORK/.livecert-migrate-state"
  [ -f "$COLD_MARKER" ] || fail "LIVECERT_RESUME=$LIVECERT_RESUME has no cold-deploy record at $COLD_MARKER - cold_deploy was never completed (or held) here"
  [ -f "$MIGRATE_MARKER" ] || fail "LIVECERT_RESUME=$LIVECERT_RESUME has no migrate record at $MIGRATE_MARKER - migrate was never completed here"
  [ -f "$COLD_DIR/terraform.tfstate" ] || fail "LIVECERT_RESUME=$LIVECERT_RESUME has a cold-deploy record but no state file at $COLD_DIR/terraform.tfstate"

  R_PREFIX="$(livecert_marker_get "$COLD_MARKER" PREFIX)"
  R_SCALE="$(livecert_marker_get "$COLD_MARKER" SCALE)"
  R_EXPECTED="$(livecert_marker_get "$COLD_MARKER" EXPECTED)"
  R_TS="$(livecert_marker_get "$COLD_MARKER" TIMESTAMP)"
  M_PREFIX="$(livecert_marker_get "$MIGRATE_MARKER" PREFIX)"
  M_SCALE="$(livecert_marker_get "$MIGRATE_MARKER" SCALE)"
  M_EXPECTED="$(livecert_marker_get "$MIGRATE_MARKER" EXPECTED)"
  M_VERIFIED="$(livecert_marker_get "$MIGRATE_MARKER" VERIFIED)"
  M_TS="$(livecert_marker_get "$MIGRATE_MARKER" TIMESTAMP)"

  [ "$R_PREFIX" = "$PREFIX" ] && [ "$M_PREFIX" = "$PREFIX" ] \
    || fail "LIVECERT_RESUME=$LIVECERT_RESUME was recorded under prefix cold=$R_PREFIX/migrate=$M_PREFIX, this run's PREFIX is $PREFIX - export PREFIX=$R_PREFIX to resume it, or point LIVECERT_RESUME at the right work dir"
  [ "$R_SCALE" = "$SCALE" ] && [ "$M_SCALE" = "$SCALE" ] \
    || fail "LIVECERT_RESUME=$LIVECERT_RESUME was recorded at scale cold=$R_SCALE/migrate=$M_SCALE, this run's SCALE is $SCALE - export SCALE=$R_SCALE to resume it"
  [ "$R_EXPECTED" = "$EXPECTED" ] \
    || fail "LIVECERT_RESUME=$LIVECERT_RESUME's cold apply recorded ${R_EXPECTED} resources, this environment computes ${EXPECTED} at scale=$SCALE - refusing a stale or mismatched cold state"
  [ "$M_EXPECTED" = "$EXPECTED" ] && [ "$M_VERIFIED" = "$VERIFIED" ] \
    || fail "LIVECERT_RESUME=$LIVECERT_RESUME's migrate recorded ${M_VERIFIED} of ${M_EXPECTED}, this environment computes ${VERIFIED} of ${EXPECTED} at scale=$SCALE - refusing a stale or mismatched migrate record"

  log "  stock state ok: ${R_EXPECTED} resources (recorded $R_TS)"
  log "  migrate record ok: ${M_VERIFIED} of ${M_EXPECTED} stamped (recorded $M_TS)"
  log "stage=cold_deploy verdict=skipped detail=resumed from $LIVECERT_RESUME"
  log "stage=migrate verdict=skipped detail=resumed from $LIVECERT_RESUME"
  MIGRATE_DONE=1
  COLD_APPLY_S=0 COLD_LOG_BYTES=0 COLD_THROTTLE_HITS=0 COLD_RETRY_LINES=0
  MIGRATE_S=0 MIGRATE_LOG_BYTES=0 MIGRATE_THROTTLE_HITS=0 MIGRATE_RETRY_LINES=0
  RESUMED=1
}

RESUMED=0
if [ -n "${LIVECERT_RESUME:-}" ]; then
  resume_verify
fi

# STOCK_PLAN_CALLS/CHOUDOUFU_PLAN_CALLS (#1051, INTENTIUS/chant-bench#33):
# each side's exact provider-mediated request count for its OWN plan of the
# converged, no-change estate - stock's at 2d below (its plan makes no other
# kind of call, so this total already IS "what a plan costs" for stock, the
# same reading plan-cost.md's own stock-vs-read-pass table gives it), and
# choudoufu's at 4b2 (the FIRST post-migration plan, the same one every
# other test_plan number describes). Left empty rather than 0 until each is
# actually measured - a RESUMED run skips 2d entirely (it is inside the
# RESUMED==0 span below) and leaves STOCK_PLAN_CALLS empty on purpose, so
# the token this stage appends is simply omitted rather than lying with a
# zero.
STOCK_PLAN_CALLS=""
CHOUDOUFU_PLAN_CALLS=""

# ══════════════════════════════════════════════════════════════════════
# Plan wall-clock instrumentation (issue #578).
#
# Nothing anywhere has ever timed stock `plan` against choudoufu `plan` on
# the same estate. The only stock number on record for this terralith is
# `terraform apply`, so there has been no basis for a comparative planning
# claim in either direction. live/e2e/terralith-scale/MIGRATION.md's 36x
# and 262x figures are floci, on another machine, and one side of them was
# taken to establish an adoption ratio rather than to measure planning
# cost - they are not a baseline and are deliberately not reused here.
#
# What makes the two numbers below comparable, and what would void them:
#
#   - Same machine, same session, same region, same scale, same estate,
#     minutes apart. Stock plans its own state after cold_deploy has
#     converged; choudoufu plans the migrated estate at test_plan.
#   - Both run with TF_LOG unset. The stage-gating plan at test_plan keeps
#     its TF_LOG=DEBUG instrumentation for the throttling measurement, and
#     is reported separately: writing megabytes of debug log inside a timed
#     region measures the log, not the plan.
#   - Both are warm - the provider is already installed in each directory
#     by that directory's own init, well before either timed region opens.
#   - Three runs each, all three values reported, no mean and no selection.
#     Whole-second resolution, from date(1), which is what this script
#     already uses.
#
# Emptiness is recorded rather than enforced. Stock's plan is expected to
# propose nothing; choudoufu's is NOT necessarily expected to, because #566
# found the ECS identity defect #572 leaving 3/55 unresolved at scale 1 and
# 9/205 at scale 4. If either plan proposes anything, the comparison is not
# between two equivalent operations, and the report has to say so rather
# than quietly present the seconds as like-for-like.
#
# This block reports measurements. It never fails the run: a real-money
# certification must not be lost to an instrumentation error.
# ══════════════════════════════════════════════════════════════════════
PLAN_TIMING_REPORT=""

timed_plans() {
  local label="$1" dir="$2" bin="$3"
  local i start end secs out rc verdict
  local secs_list="" verdicts=""

  for i in 1 2 3; do
    start=$(date +%s)
    out="$(cd "$dir" && "$bin" plan -input=false -no-color 2>&1)"
    rc=$?
    end=$(date +%s)
    secs=$((end - start))

    if [ "$rc" -ne 0 ]; then
      verdict="exit${rc}"
    elif grep -qF "No changes. Your infrastructure matches the configuration." <<< "$out"; then
      verdict="empty"
    else
      verdict="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy' <<< "$out" | head -1 | tr ' ' '_')"
      [ -n "$verdict" ] || verdict="non-empty"
      printf '%s\n' "$out" > "$WORK/plantiming_${label}_${i}.out"
      log "    run ${i} was NOT a no-change plan (${verdict}); full output kept at plantiming_${label}_${i}.out"
      grep -E '^  # ' <<< "$out" | head -10 | sed 's/^/      /'
    fi

    log "    ${label} plan run ${i}: ${secs}s (${verdict})"
    secs_list="${secs_list}${secs_list:+ }${secs}"
    verdicts="${verdicts}${verdicts:+,}${verdict}"
  done

  PLAN_TIMING_REPORT="${PLAN_TIMING_REPORT}${PLAN_TIMING_REPORT:+
}  ${label}: ${secs_list} seconds (3 runs, TF_LOG unset, warm provider); verdicts ${verdicts}"
}

# ══════════════════════════════════════════════════════════════════════
# API-CALL INSTRUMENTATION (issue #622, and the call-count half of the
# stock-vs-choudoufu plan comparison #578/#588 measured only in seconds).
#
# What is counted, and what each count is worth:
#
#   * PROVIDER-MEDIATED CALLS - counted EXACTLY. terraform-provider-aws
#     logs one "HTTP Request Sent" entry per AWS SDK request when the
#     provider's log level is DEBUG, carrying rpc.method=<Service>/<Op>.
#     The entry is MULTI-LINE (a body block sits between the header line
#     and the attributes), so the counter below reassembles entries on the
#     leading timestamp before matching - a plain `grep -c` on the header
#     line gets the total right but loses every operation name to the
#     continuation lines. Stock's plan makes no other kind of call, so for
#     stock this IS the plan's API-call count.
#
#   * CHOUDOUFU'S OWN CLIENT CALLS - NOT counted, and not countable from
#     this log. internal/live/cloudcontrol's Client talks to Cloud Control
#     and to the Tagging API over its own net/http client, inside the tofu
#     process, and logs no line per HTTP request. What internal/live/
#     discovery logs is one [DEBUG] line per TYPE listed and per TYPE swept,
#     which is a different quantity in each direction: a Cloud Control
#     listing is one ListResources call per type (plus pagination pages,
#     which are not logged at all), while the WHOLE tagging sweep is one
#     estate-filtered GetResources call that logs one line for every type it
#     covers (tagging.go, sweepViaTagging). So both are reported as type
#     counts, explicitly, rather than dressed up as call counts.
#
#   * TypeScan.Refined - issue #622's question - IS exact: cloudcontrol.go
#     prints one line per GetResource refinement at the same place it
#     increments scan.Refined. Two structural facts decide what a number
#     here means. It can only be produced by scanTypeCloudControl, the
#     per-type Cloud Control path; the tagging sweep never refines at all
#     ("tags always arrive with the candidate", sweepViaTagging's own doc
#     comment), so a run whose sweep is served entirely by the tagging path
#     reads zero however populated the account is. And the refinement
#     scales with the ACCOUNT's object count for the types that DO take the
#     Cloud Control path, not with the estate's, which is why only a real
#     account can answer whether it still fires materially. Reported for
#     BOTH the first post-migration plan (cold hint store, widest sweep) and
#     a steady-state plan taken after the three timed runs; those are
#     different questions and one number answers neither.
#
# Like timed_plans, this block only reports. It never fails the run.
# ══════════════════════════════════════════════════════════════════════
API_CALL_REPORT=""

# apicalls_awk writes the entry-reassembling counter to a file and echoes
# its path. Kept as a file rather than inlined so the same program can be
# re-run by hand over a kept WORK dir (LIVECERT_KEEP_WORK=1).
APICALLS_AWK=""
apicalls_awk() {
  if [ -z "$APICALLS_AWK" ]; then
    APICALLS_AWK="$WORK/apicalls.awk"
    cat > "$APICALLS_AWK" <<'AWKEOF'
function flush() {
  if (entry ~ /HTTP Request Sent/) {
    total++
    op = "unknown"
    # hclog quotes an attribute value containing a space, and several AWS
    # service names DO contain one - rpc.method="Route 53/GetHostedZone",
    # rpc.method="Resource Groups Tagging API/GetResources". The unquoted
    # alternative must come second: matching it first would stop at the
    # opening quote and bucket every Route 53 call as "unknown", which is
    # exactly what the first version of this program did (22 of 156 calls
    # on the floci proving run, all of them Route 53).
    if (match(entry, /rpc\.method="[^"]+"/)) {
      op = substr(entry, RSTART + 12, RLENGTH - 13)
    } else if (match(entry, /rpc\.method=[A-Za-z0-9]+\/[A-Za-z0-9]+/)) {
      op = substr(entry, RSTART + 11, RLENGTH - 11)
    }
    cnt[op]++
  }
  entry = ""
}
/^20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ { flush(); entry = $0; next }
{ entry = entry " " $0 }
END {
  flush()
  # Tab-separated, count BEFORE the operation, because an operation name can
  # contain a space ("Route 53/GetHostedZone"). The first version emitted
  # "<op> <count>" and the reader split on whitespace, so every Route 53 row
  # printed the count as "Route" and the operation as "53" - three identical
  # "53 Route" lines on the scale-1 real-AWS run, with the TOTAL still right.
  printf "TOTAL\t%d\n", total + 0
  for (o in cnt) printf "OP\t%d\t%s\n", cnt[o], o
}
AWKEOF
  fi
  printf '%s\n' "$APICALLS_AWK"
}

# analyze_api_calls reads a TF_LOG=DEBUG capture and logs, for one labelled
# plan: the exact provider-mediated request count with its per-operation
# breakdown, choudoufu's own discovery-client floor, and the per-type
# GetResource refinement counts (#622). Appends one summary line to
# API_CALL_REPORT.
analyze_api_calls() {
  local label="$1" f="$2"
  local prog total refined listings tagsweeps joins
  # API_CALLS_LAST_TOTAL (#1051, INTENTIUS/chant-bench#33) is this call's own
  # exact total, for whichever caller invoked us to read right afterward -
  # bash 3.2 (macOS's /bin/bash, see the ${arr[@]+...} comment further down
  # this file) has no associative arrays, so a single "last result" global,
  # copied into a caller-named variable immediately after the call returns,
  # is the plain way to thread one number out of a function here. Cleared
  # first so a "not instrumented" return (below) cannot leave a caller
  # reading a PREVIOUS call's total as if it were this one's.
  API_CALLS_LAST_TOTAL=""
  if [ ! -f "$f" ]; then
    log "  ${label}: no debug log at $f - not instrumented"
    return 0
  fi
  prog="$(apicalls_awk)"

  awk -f "$prog" "$f" > "$WORK/apicalls_${label}.counts" 2>/dev/null
  total="$(awk -F'\t' '$1=="TOTAL"{print $2}' "$WORK/apicalls_${label}.counts")"
  [ -n "$total" ] || total=0
  API_CALLS_LAST_TOTAL="$total"

  # One line per GetResource refinement, printed beside scan.Refined++.
  refined="$(grep -cF 'refined with GetResource' "$f" 2>/dev/null || true)"
  listings="$(grep -cE 'stateless/discovery: listing .* via Cloud Control' "$f" 2>/dev/null || true)"
  tagsweeps="$(grep -cE 'stateless/discovery: sweeping .* via the Tagging API' "$f" 2>/dev/null || true)"
  joins="$(grep -cF 'joined one from the estate' "$f" 2>/dev/null || true)"

  log "  ${label}: ${total:-0} provider-mediated AWS API request(s) (exact, from rpc.method entries)"
  log "    top operations:"
  awk -F'\t' '$1=="OP"{printf "      %8d %s\n", $2, $3}' "$WORK/apicalls_${label}.counts" | sort -rn | head -25
  # These two counts are types, not calls, and they scale differently:
  # a Cloud Control listing is one ListResources call per TYPE (plus
  # pagination), while the whole Tagging sweep is ONE estate-filtered
  # GetResources call (plus pagination) that logs one line per type it
  # covers (internal/live/discovery/tagging.go, sweepViaTagging). Reporting
  # both as "calls" would overstate the tagging path by a factor of the
  # type count and understate the Cloud Control path by its page depth.
  log "    choudoufu's own Cloud Control / Tagging client (types, not calls - see below):"
  log "      ${listings:-0} type(s) listed via Cloud Control ListResources (>= 1 call each, more with pagination)"
  log "      ${tagsweeps:-0} type(s) covered by the estate-filtered Tagging sweep (ONE GetResources call for all of them, plus pagination)"
  log "      ${joins:-0} tag-index join(s)"
  log "    TypeScan.Refined (#622): ${refined:-0} per-object GetResource refinement(s) total"
  if [ "${refined:-0}" -gt 0 ]; then
    log "    refinements by type:"
    grep -F 'refined with GetResource' "$f" \
      | sed -E 's/.*stateless\/discovery: ([a-z0-9_]+) identifier .*/\1/' \
      | sort | uniq -c | sort -rn | head -25 | sed 's/^/      /'
  fi
  API_CALL_REPORT="${API_CALL_REPORT}${API_CALL_REPORT:+
}  ${label}: ${total:-0} provider-mediated request(s); ${listings:-0} type(s) via Cloud Control, ${tagsweeps:-0} type(s) via the one-call tagging sweep; TypeScan.Refined=${refined:-0}"
}

# instrumented_plan runs ONE extra plan with TF_LOG=DEBUG purely to count
# calls. It is deliberately NOT one of timed_plans' three: writing a debug
# log inside a timed region measures the log, not the plan, which is the
# same rule 2c/4d already state. Its own wall clock is reported anyway, so
# a reader can see what the instrumentation cost.
instrumented_plan() {
  local label="$1" dir="$2" bin="$3"
  local f="$WORK/apicalls_${label}.debug.log"
  local start end secs out rc verdict
  start=$(date +%s)
  out="$(cd "$dir" && TF_LOG=DEBUG TF_LOG_PATH="$f" "$bin" plan -input=false -no-color 2>&1)"
  rc=$?
  end=$(date +%s)
  secs=$((end - start))
  if [ "$rc" -ne 0 ]; then
    verdict="exit${rc}"
  elif grep -qF "No changes. Your infrastructure matches the configuration." <<< "$out"; then
    verdict="empty"
  else
    verdict="$(grep -oE 'Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy' <<< "$out" | head -1 | tr ' ' '_')"
    [ -n "$verdict" ] || verdict="non-empty"
  fi
  printf '%s\n' "$out" > "$WORK/apicalls_${label}.out"
  log "  ${label}: instrumented plan took ${secs}s (${verdict}); NOT a timing measurement - TF_LOG=DEBUG is on"
  analyze_api_calls "$label" "$f"

  # The same capture also answers "was this side throttled", and until now
  # nothing asked it. analyze_debug_log ran only on cold_deploy's apply,
  # migrate's tag writes and test_plan - so every throttle count this harness
  # has ever printed for a PLAN belongs to choudoufu, and stock's plan-side
  # count did not exist. That made the two sides incomparable exactly where
  # the comparison matters: stock's apply (writes) against choudoufu's plan
  # (reads) is not a like-for-like pairing. One call, both labels, no extra
  # AWS request - the log is already on disk.
  read -r _b _t _r _p <<< "$(analyze_debug_log "$f")"
  log "  ${label}: plan-side throttling: ${_t} throttling-error line(s), ${_r} retry line(s), ${_p} pagination-continuation line(s) in ${_b}B"
  printf '%s %s %s %s %s\n' "$label" "$_b" "$_t" "$_r" "$_p" >> "$WORK/plan_throttle_by_label.txt"
}

# The IAM role headroom gate (#1230): before cold_deploy, and only on a run
# that is about to do one. A resumed run (RESUMED=1) skips cold_deploy and
# so skips this - its roles already exist and are its own. Exit 2 rather
# than fail(): fail() would speak a `GAUNTLET stage=cold_deploy fail` line
# for a stage that never started, and the refusal line is the whole record
# (#1151). The EXIT trap still runs teardown(), which finds no state to
# destroy, lists the (empty) prefix, stops the emulator on a floci run and
# removes WORK - the cleanup a refusal wants, and nothing else.
# >>> iam role headroom gate
if [ "$RESUMED" = "0" ]; then
  iam_role_headroom_check || exit 2
fi
# <<< iam role headroom gate

# ══════════════════════════════════════════════════════════════════════
# cold_deploy + migrate: stock applies the unmodified (AZ/provider-corrected)
# generator output for real, then choudoufu adopts it. Wrapped in "if not
# RESUMED" as one span (#1032): a resumed run already verified both stages'
# evidence by reading their markers above and logged them skipped, so
# neither stage's real work - nor its own gauntlet_stage pass call - runs a
# second time.
# ══════════════════════════════════════════════════════════════════════
if [ "$RESUMED" = "0" ]; then
CURRENT_STAGE=cold_deploy
heartbeat_start cold_deploy
log "=== 1. terralith-gen -scale $SCALE -prefix $PREFIX -> $COLD_DIR ==="
generate_estate "$COLD_DIR"
log "  expect ${EXPECTED} resources (${VERIFIED} taggable/eligible)"

log "=== 2. cold_deploy: $TF_COLD init ==="
( cd "$COLD_DIR" && "$TF_COLD" init -input=false -no-color ) > "$WORK/cold_deploy_init.out" 2>&1 \
  || { tail -20 "$WORK/cold_deploy_init.out"; fail "stock init failed"; }

log "=== 2b. cold_deploy: $TF_COLD apply (backgrounded so a signal can interrupt it) ==="
(
  cd "$COLD_DIR" || exit 1
  if [ "$THROTTLE_LOG" = "1" ]; then
    export TF_LOG=DEBUG
    export TF_LOG_PATH="$WORK/cold_deploy_apply.debug.log"
  fi
  exec "$TF_COLD" apply -input=false -auto-approve -no-color -parallelism=10
) > "$WORK/cold_deploy_apply.out" 2>&1 &
APPLY_PID=$!
COLD_APPLY_START=$(date +%s)
wait "$APPLY_PID"
APPLY_RC=$?
COLD_APPLY_END=$(date +%s)
APPLY_PID=""
[ "$APPLY_RC" -eq 0 ] || { tail -40 "$WORK/cold_deploy_apply.out"; fail "stock apply exited $APPLY_RC"; }
grep -qE "Apply complete! Resources: ${EXPECTED} added" "$WORK/cold_deploy_apply.out" \
  || { grep -E 'Apply complete' "$WORK/cold_deploy_apply.out"; fail "stock apply did not create exactly ${EXPECTED} resources"; }
[ -f "$COLD_DIR/terraform.tfstate" ] || fail "stock apply left no state file to migrate from"

# The cold-deploy marker (#1032): written the instant the assertion two
# lines above has confirmed the stock state is real and matches EXPECTED
# exactly, so LIVECERT_RESUME/LIVECERT_TEARDOWN_ONLY never trust a directory
# that merely exists - they trust this file, which exists only because the
# same check every normal run already relies on just passed.
{
  printf 'PREFIX=%s\n' "$PREFIX"
  printf 'SCALE=%s\n' "$SCALE"
  printf 'TARGET=%s\n' "$TARGET"
  printf 'REGION=%s\n' "$REGION"
  printf 'RUN_ID=%s\n' "$RUN_ID"
  printf 'RECORD_STORE_BACKEND=%s\n' "$RECORD_STORE_BACKEND"
  printf 'RECORD_STORE_BUCKET=%s\n' "${RECORD_STORE_BUCKET:-}"
  printf 'EXPECTED=%s\n' "$EXPECTED"
  printf 'TIMESTAMP=%s\n' "$(date -u +%FT%TZ)"
} > "$WORK/.livecert-cold-state"
COLD_APPLY_S=$((COLD_APPLY_END - COLD_APPLY_START))
log "  $(grep -E 'Apply complete' "$WORK/cold_deploy_apply.out") in ${COLD_APPLY_S}s"
COLD_LOG_BYTES=0 COLD_THROTTLE_HITS=0 COLD_RETRY_LINES=0 COLD_PAGINATION_HITS=0
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$WORK/cold_deploy_apply.debug.log" ]; then
  read -r COLD_LOG_BYTES COLD_THROTTLE_HITS COLD_RETRY_LINES COLD_PAGINATION_HITS <<< "$(analyze_debug_log "$WORK/cold_deploy_apply.debug.log")"
  log "  cold_deploy debug log: ${COLD_LOG_BYTES} bytes, ${COLD_THROTTLE_HITS} throttling-error line(s), ${COLD_RETRY_LINES} genuine-retry line(s) - this is the parallelism=10, single-zone Route53 record fan-out, the most plausible place in this pipeline to see ChangeResourceRecordSets pushed back on"
fi
# The prose above is for a human tailing this run; the key=value tokens
# below are for a machine (issue #1051, INTENTIUS/chant-bench#33) - the same
# numbers the sentence already names, appended rather than replacing it, the
# same pattern index_lag_s= already used at test_plan before this issue.
# tools/gauntlet/scalerecord.go's parseColdDeployDetail reads these in
# preference to the sentence shape, which stays there as its fallback for
# every row recorded before this line existed.
gauntlet_stage cold_deploy pass "${EXPECTED} resources from stock $TF_COLD against $TARGET at scale=$SCALE in ${COLD_APPLY_S}s, tofu-cert-run=$RUN_ID, debug log ${COLD_LOG_BYTES}B/${COLD_THROTTLE_HITS} throttle/${COLD_RETRY_LINES} retry resources=${EXPECTED} taggable=${VERIFIED} seconds=${COLD_APPLY_S} throttle=${COLD_THROTTLE_HITS} retry=${COLD_RETRY_LINES}$HOLD_TAG"

# Issue #578: stock's own plan on its own state, AFTER the apply has
# converged and BEFORE anything migrates it - a refresh-and-diff of an
# already-applied estate, which is the operation choudoufu's post-migration
# plan at test_plan is compared against. Unattributed on purpose: it reports
# no verdict, so no stage should be blamed if it goes wrong.
gauntlet_end_stage
log "=== 2c. plan timing: stock $TF_COLD plan x3, converged estate, TF_LOG unset (#578) ==="
timed_plans "stock-terraform" "$COLD_DIR" "$TF_COLD"

log "=== 2d. API calls: one EXTRA stock plan with TF_LOG=DEBUG, outside every timed region ==="
instrumented_plan "stock-terraform" "$COLD_DIR" "$TF_COLD"
# Stock has no sweep leg at all (it trusts its state file outright), so this
# total already is stock's whole plan-call cost, unambiguous and not a leg
# split. Captured immediately after the call above sets it (#1051).
STOCK_PLAN_CALLS="$API_CALLS_LAST_TOTAL"

# ══════════════════════════════════════════════════════════════════════
# WALLCLOCK_TRACE (issue #867): the stock half of the idle-gap comparison.
#
# #683 measured the read pass's head-of-line stall from ONE debug capture per
# side, and #867 asks for three, on the same estate in the same session, so
# that "the sweep showed no straggler" can be said about a sample rather than
# about a single run. The stock side has to be taken HERE, before migrate, for
# the reason 2c states: once live-import has stamped tags on 335 objects the
# stock plan is no longer empty, and a plan that renders 335 changes is not
# comparable to an empty one on wall clock or on request count.
#
# These are not timing measurements and nothing gates on them - TF_LOG=DEBUG
# is on, which is the same rule 2c/2d/4d already state. What they are for is
# live/live-cert/wallclock-gaps.py, which reads in-flight concurrency out of
# the capture.
# ══════════════════════════════════════════════════════════════════════
if [ "${WALLCLOCK_TRACE:-0}" = "1" ]; then
  log "=== 2e. wall-clock trace (#867): stock plan x3 with TF_LOG=DEBUG, pre-migrate, empty ==="
  for i in 1 2 3; do
    instrumented_plan "trace-stock-$i" "$COLD_DIR" "$TF_COLD"
  done
fi

# ══════════════════════════════════════════════════════════════════════
# migrate: choudoufu live-import -approve against the stock state file.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=migrate
heartbeat_start migrate
log "=== 3. migrate: generate the SAME estate into $ADOPTED_DIR (live block + record_store) ==="
generate_estate "$ADOPTED_DIR"
{
  cat <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
  live {
    estate = "$ESTATE"
    record_store "$RECORD_STORE_BACKEND" {
$RECORD_STORE_ARGS
    }
  }
}

EOF
  provider_block
} > "$ADOPTED_DIR/versions.tf"

log "=== 3b. migrate: choudoufu init + live-import (dry run, then -approve) ==="
( cd "$ADOPTED_DIR" && "$TOFU" init -input=false -no-color ) > "$WORK/migrate_init.out" 2>&1 \
  || { tail -20 "$WORK/migrate_init.out"; fail "adopted init failed"; }

IMPORT_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -60; fail "live-import (dry run) failed"; }
printf '%s\n' "$IMPORT_OUT" > "$WORK/migrate_dryrun.out"
grep -qF "${VERIFIED} of ${EXPECTED} resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | grep -E 'eligible for stamping'; fail "live-import did not verify ${VERIFIED} of ${EXPECTED} resources as eligible - see $WORK/migrate_dryrun.out"; }

MIGRATE_APPROVE_ENV=()
if [ "$THROTTLE_LOG" = "1" ]; then
  MIGRATE_APPROVE_ENV=(TF_LOG=DEBUG "TF_LOG_PATH=$WORK/migrate_approve.debug.log")
fi
MIGRATE_START=$(date +%s)
# ${arr[@]+"${arr[@]}"} rather than a bare "${arr[@]}": under `set -u`,
# bash 3.2 - which is what /bin/bash still is on macOS, where this script
# is developed - treats expanding an EMPTY array as an unbound variable and
# aborts. That is only reachable with THROTTLE_LOG=0, which is why every
# run behind this issue (all at the default THROTTLE_LOG=1, so the array
# always held two entries) passed straight over it, and why it surfaced
# only when PR #577's merge verification ran the harness with the debug log
# turned off.
APPROVE_OUT="$(cd "$ADOPTED_DIR" && env ${MIGRATE_APPROVE_ENV[@]+"${MIGRATE_APPROVE_ENV[@]}"} "$TOFU" live-import -state="$COLD_DIR/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -60; fail "live-import -approve failed"; }
MIGRATE_END=$(date +%s)
printf '%s\n' "$APPROVE_OUT" > "$WORK/migrate_approve.out"
SKIPPED=$((EXPECTED - VERIFIED))  # UNTAGGABLE instances (no tags argument in the provider schema): reported as "skipped", not an error - live-import needs no action on these, their identity composes from an already-stamped parent
grep -qF "${VERIFIED} resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, ${SKIPPED} skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve did not stamp exactly ${VERIFIED} resources cleanly (expected ${SKIPPED} skipped/untaggable) - see $WORK/migrate_approve.out"; }
MIGRATE_DONE=1

# The migrate marker (#1032): written the instant the assertion above has
# confirmed every eligible resource was actually stamped, same discipline
# as the cold-deploy marker above - LIVECERT_RESUME's own verification reads
# this file, never a directory's mere existence.
{
  printf 'PREFIX=%s\n' "$PREFIX"
  printf 'SCALE=%s\n' "$SCALE"
  printf 'ESTATE=%s\n' "$ESTATE"
  printf 'EXPECTED=%s\n' "$EXPECTED"
  printf 'VERIFIED=%s\n' "$VERIFIED"
  printf 'TIMESTAMP=%s\n' "$(date -u +%FT%TZ)"
} > "$WORK/.livecert-migrate-state"
MIGRATE_S=$((MIGRATE_END - MIGRATE_START))
log "  ${VERIFIED} of ${EXPECTED} stamped in ${MIGRATE_S}s"
MIGRATE_LOG_BYTES=0 MIGRATE_THROTTLE_HITS=0 MIGRATE_RETRY_LINES=0
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$WORK/migrate_approve.debug.log" ]; then
  read -r MIGRATE_LOG_BYTES MIGRATE_THROTTLE_HITS MIGRATE_RETRY_LINES _ <<< "$(analyze_debug_log "$WORK/migrate_approve.debug.log")"
  log "  migrate debug log: ${MIGRATE_LOG_BYTES} bytes, ${MIGRATE_THROTTLE_HITS} throttling-error line(s), ${MIGRATE_RETRY_LINES} genuine-retry line(s) - this is ${VERIFIED} sequential tag-write API calls (one per resource, not batched), the most plausible place to see a WRITE-side rate limit"
fi
# Tokens for the same reason cold_deploy's own gauntlet_stage call above
# carries them now (issue #1051): resources/taggable/skipped/seconds/
# throttle/retry, read by tools/gauntlet/scalerecord.go's parseMigrateDetail
# in preference to the sentence.
gauntlet_stage migrate pass "${VERIFIED} of ${EXPECTED} verified, ${VERIFIED} stamped, ${SKIPPED} skipped, in ${MIGRATE_S}s, debug log ${MIGRATE_LOG_BYTES}B/${MIGRATE_THROTTLE_HITS} throttle/${MIGRATE_RETRY_LINES} retry resources=${EXPECTED} taggable=${VERIFIED} skipped=${SKIPPED} seconds=${MIGRATE_S} throttle=${MIGRATE_THROTTLE_HITS} retry=${MIGRATE_RETRY_LINES}$HOLD_TAG"
fi # RESUMED == 0 (cold_deploy + migrate)

# index_wait (#1046, #1049): migrate's ${VERIFIED} tag writes above are
# verified against the account at write time, but the Resource Groups
# Tagging API's own SEARCH INDEX is a separate, eventually-consistent copy -
# the 2026-09-11 scale-50 run read it about 21 minutes after migrate had
# verified and stamped all 1,655 taggable resources with zero failures, and
# it held 104 of them. Discovery's direct-read fallback (#1046) now refuses
# a count/for_each aws_iam_policy instance with DIRECT_READ_UNRESOLVED
# rather than proposing a create while the index is silent for it (#1049),
# which is the safe outcome - but a test_plan that walks straight into a
# cold index at scale would spend its whole run recording a lag it never
# measured, instead of the plan choudoufu actually produces once the
# account is consistent.
#
# This polls the SAME index test_plan itself will read (livecert_rgta_count
# against tofu-estate=$ESTATE, exactly 4a2's own query) every
# LIVECERT_INDEX_POLL_S seconds until it reaches $VERIFIED stamped, or
# LIVECERT_INDEX_WAIT_S runs out. It never fails the run: a lagged index
# after the bound is exactly the condition #1046/#1049 are about, and the
# product's own refusal at test_plan is what records it, not this step -
# the caller is expected to run this unattributed (CURRENT_STAGE cleared),
# since it is a measurement, not a gated stage. Sets the caller's
# INDEX_LAG_S to the elapsed seconds either way (converged or bound-tripped)
# so it can ride along in test_plan's own detail line, on both the pass and
# the refusal path.
#
# Kept as its own function, rather than inlined at the call site, so
# selftest-index-wait.sh can extract it verbatim (the same pattern
# selftest-teardown-timeout.sh already uses for teardown()) and drive it
# against a stubbed `aws` with no real AWS calls.
index_wait() {
  local idx_n elapsed start target regional global unindexed
  IFS=' ' read -r target regional global unindexed <<< "$(index_partition "$REGION")"
  INDEX_TARGET_N=$target

  log "=== 3c. index wait: polling the tag index for tofu-estate=$ESTATE in $REGION every ${LIVECERT_INDEX_POLL_S}s, bound ${LIVECERT_INDEX_WAIT_S}s (#1046, #1143) ==="
  # Say the whole split out loud, every run. A wait that silently narrowed
  # its target would be #1143 again from the other side: the run would
  # converge, look complete, and nobody would know it had stopped counting
  # most of the estate.
  log "  migrate stamped ${VERIFIED} objects; ${target} of them are what the tag index can hold when queried in ${REGION}, and ${target} is what this wait polls to:"
  log "    ${regional} regional - aws_ecs_task_definition, aws_ecs_service (per scale); aws_ecs_cluster, aws_vpc, aws_subnet, aws_security_group (fixed)"
  if [ "$REGION" = "us-east-1" ]; then
    log "    ${global} global - aws_iam_policy, aws_iam_instance_profile (per scale); aws_route53_zone (fixed). Counted, because the tag index holds global-service objects in us-east-1 and this run's region IS us-east-1"
  else
    log "  NOT waiting for ${global} global object(s) - aws_iam_policy, aws_iam_instance_profile, aws_route53_zone. IAM and Route53 are global services whose objects the tag index holds in us-east-1 ONLY, and this run queries ${REGION} (#1144). They exist and they are stamped; they are not visible from here"
  fi
  log "  NOT waiting for ${unindexed} aws_iam_role(s) - resourcegroupstaggingapi GetResources returns nothing for iam:role in ANY region, while iam:ListRoleTags confirms every one of them carries tofu-estate (#1134, measured against real AWS and stable over 35 minutes). No bound can absorb these; waiting for them is what made this step unsatisfiable (#1143)"

  if [ "$target" -le 0 ]; then
    INDEX_LAG_S=0
    INDEX_CONVERGED=na
    INDEX_NOTE="tag index wait skipped: nothing this estate stamped is reachable from the index in ${REGION}"
    log "index wait SKIPPED: nothing this estate stamped is reachable from the tag index in ${REGION}, so there is no target to converge on. Not waiting is the honest answer - a 0-of-0 'converged' would be a false pass, and the bound would be pure dead time"
    return 0
  fi

  start=$(date +%s)
  while :; do
    idx_n="$(livecert_rgta_count tofu-estate "$ESTATE")"
    elapsed=$(( $(date +%s) - start ))
    log "  index wait: t=${elapsed}s tag index holds ${idx_n:-0} of a reachable ${target} (of ${VERIFIED} stamped)"
    if [ "${idx_n:-0}" -ge "$target" ]; then
      INDEX_LAG_S=$elapsed
      INDEX_CONVERGED=yes
      INDEX_NOTE="tag index converged on ${idx_n} of a reachable ${target}, itself $((VERIFIED - target)) short of the ${VERIFIED} stamped (see the wait's own breakdown)"
      log "index converged after ${INDEX_LAG_S}s: ${idx_n} of a reachable ${target}. This is NOT ${VERIFIED} of ${VERIFIED}: $((VERIFIED - target)) stamped object(s) are outside what the tag index can hold from ${REGION} and were never part of the target - see the breakdown above before reading this as 'every object is in the index'"
      return 0
    fi
    if [ "$elapsed" -ge "$LIVECERT_INDEX_WAIT_S" ]; then
      INDEX_LAG_S=$elapsed
      INDEX_CONVERGED=no
      INDEX_NOTE="tag index did NOT converge: ${idx_n:-0} of a reachable ${target} after ${LIVECERT_INDEX_WAIT_S}s"

      # This is the second half of #1143. The old code printed "still at N
      # of M ... proceeding" and returned 0, and the run went on to pass
      # test_plan - so a bound that tripped on an IMPOSSIBLE target was
      # indistinguishable, in the log and in the recorded row alike, from a
      # bound that tripped on a slow index. Now the target is reachable, so
      # tripping the bound means something: the index genuinely did not
      # catch up. Say that, and carry index_converged=no into test_plan's
      # own detail so the recorded row cannot be read as a converged
      # measurement either. The run still continues, deliberately:
      # test_plan's own refusal (DIRECT_READ_UNRESOLVED, #1046/#1049) is the
      # product's verdict on a lagged index, and this step is a measurement,
      # not a gate.
      log "index NOT CONVERGED: ${idx_n:-0} of a reachable ${target} after ${LIVECERT_INDEX_WAIT_S}s. The target is what the index CAN hold from ${REGION}, so this is a genuine index lag, not #1143's unsatisfiable target. Proceeding to test_plan, whose own verdict - not this line - is the run's answer; index_converged=no rides into the recorded row"
      return 0
    fi
    sleep "$LIVECERT_INDEX_POLL_S"
  done
}

LIVECERT_INDEX_WAIT_S="${LIVECERT_INDEX_WAIT_S:-1800}"
LIVECERT_INDEX_POLL_S="${LIVECERT_INDEX_POLL_S:-30}"
INDEX_LAG_S=0
# The reachable target index_wait actually polled to, recorded beside the
# lag so a reader can tell 104 of 104 from 104 of 1655 without the log.
INDEX_TARGET_N=0
# yes | no | na (no reachable target) | skipped (not an aws run). Never
# empty: test_plan's detail carries it as a token, and an empty token would
# read to tools/gauntlet/scalerecord.go exactly like the old run that could
# not distinguish converged from timed-out at all.
INDEX_CONVERGED=skipped
# One clause of plain prose for the same thing, folded into test_plan's own
# detail sentence beside the tokens. A token is for the parser; a reader
# scanning a row should not have to know that index_converged=no is the
# interesting one.
INDEX_NOTE="tag index wait skipped (target=$TARGET)"
if [ "$TARGET" = "aws" ]; then
  index_wait
else
  # The skip stands, on a narrower reason than it used to carry. #1152
  # (lex00/floci#205) is FIXED: the pinned emulator serves aws_iam_policy and
  # aws_iam_instance_profile through GetResources in us-east-1 and nothing
  # for aws_iam_role, which is what real AWS does, so index_partition's
  # global half is no longer unserved here. That sentence has been removed
  # rather than reworded, because a reason that has stopped being true is
  # worse than no reason at all.
  #
  # Two reasons survive it, and neither is about IAM:
  #
  #   The emulator's index is written synchronously. A probe tagged an object
  #   and the very next GetResources returned it, with no settling. Index lag
  #   is what this wait exists to absorb (#1046, #1049) and it is a real-AWS
  #   property; a floci run of it would wait zero seconds and report a
  #   convergence that measured nothing.
  #
  #   The target is not derivable from evidence here anyway. index_partition
  #   builds it from nine types across its regional and global buckets, and
  #   seven of the nine have no tagging-sweep row at the pinned digest at
  #   all - silence in live/floci-capabilities.json is "not yet probed", not
  #   a clean bill of health.
  #
  # live/indexwait_partition_test.go holds both halves of that: it goes red
  # if the emulator starts serving aws_iam_role (which would make the
  # unindexed bucket wrong here while staying right on AWS), and red again
  # once all nine target-bearing types are probed and implemented, at which
  # point this skip is worth re-deciding.
  log "=== 3c. index wait: target=$TARGET - skipping. The tag index's own lag is a real-AWS property and the emulator's index is written synchronously, so a wait here would measure nothing; and seven of the nine types index_partition derives its target from have no tagging-sweep row at the pinned digest, so there is no evidence-backed target to poll to (see live/indexwait_partition_test.go) ==="
fi

# ══════════════════════════════════════════════════════════════════════
# test_plan: replan from nothing; identities checked against the AWS CLI;
# this is also where the throttling/pagination measurement runs, since it
# is choudoufu's full estate-wide sweep (#546's O(types) side).
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_plan
heartbeat_start test_plan
log "=== 4. test_plan: choudoufu plan must be empty (instrumented) ==="
PLAN_LOG="$WORK/test_plan.debug.log"
PLAN_START=$(date +%s)
if [ "$THROTTLE_LOG" = "1" ]; then
  PLAN_OUT="$(cd "$ADOPTED_DIR" && TF_LOG=DEBUG TF_LOG_PATH="$PLAN_LOG" "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
else
  PLAN_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
fi
PLAN_END=$(date +%s)
PLAN_S=$((PLAN_END - PLAN_START))
printf '%s\n' "$PLAN_OUT" > "$WORK/test_plan.out"

# TP_FAIL defers this stage's failure instead of taking it immediately, so
# that the plan-timing measurement at 4d still runs (issue #588). It is NOT
# a softening of the verdict: TP_FAIL is non-empty iff the old code would
# have called fail(), the same fail() is called with the same message a few
# steps further down, and the stage still reports `fail`. What changes is
# only that a run which is going to fail this stage anyway now yields the
# one number it was dispatched to produce before it exits.
#
# Why that matters here specifically: 4d is the ONLY measurement of
# choudoufu's plan wall-clock, #588's whole blocked cell, and it sat behind
# an early `exit 1`. #578 got stock's side at scale 4 and lost choudoufu's
# entirely (to #580's refusal), so the pair could not be formed and the
# claim's slope stayed unknown - a second run losing it to a DIFFERENT
# scale-4 failure would repeat that at full price. timed_plans already
# records a per-run verdict (`empty` / `Plan:_N_to_add...` / `exitN`) beside
# every duration, so a number taken on a non-empty plan is self-labelling in
# the output and cannot be mistaken for a no-change plan by a later reader.
#
# The success path below is deliberately left in its original order
# (gating plan -> 4b -> 4c -> stage pass -> 4d), so a passing run's numbers
# stay directly comparable to #578's scale-1 run; the fallback only fires on
# the path that previously produced nothing at all.
TP_FAIL=""
if [ "$PLAN_RC" -ne 0 ]; then
  printf '%s\n' "$PLAN_OUT" | tail -40
  # Carry the plan's OWN diagnosis into the stage detail, not the exit code.
  # "the post-migrate plan exited 1" is the exact shape this repository
  # refuses everywhere else - an exit code standing in for a verdict - and
  # it is what the recorded live_cert row is stuck with until the run that
  # produced it is repeated. A row that names the rule and the first error
  # is readable without the log; a row that names a number is not.
  PLAN_ERR="$(grep -m1 -E '^Error: ' <<< "$PLAN_OUT" | tr -d '\r')"
  PLAN_RULE="$(grep -m1 -oE 'Rule: [a-z0-9-]+' <<< "$PLAN_OUT")"
  PLAN_ERR_N="$(grep -c -E '^Error: ' <<< "$PLAN_OUT" | tr -d ' ')"
  TP_FAIL="the post-migrate plan exited ${PLAN_RC} with ${PLAN_ERR_N} error(s)${PLAN_RULE:+, ${PLAN_RULE}}${PLAN_ERR:+ - first: ${PLAN_ERR}}"
elif ! grep -qF "No changes. Your infrastructure matches the configuration." <<< "$PLAN_OUT"; then
  grep -E '^  #' <<< "$PLAN_OUT" | head -20
  TP_FAIL="the post-migrate plan is not empty - see $WORK/test_plan.out"
else
  log "  plan empty in ${PLAN_S}s"
fi

log "=== 4a2. state model: prove each piece was actually exercised, not just configured ==="
# A run that DECLARES a cloud record store and then never writes to it looks
# identical, in every other line of this log, to one that used local disk. So
# each piece is checked against the cloud, by listing, and the check fails the
# stage rather than warning - "configured" is not "used".
#
# Identity is proved by the tag index, values by the record store. Effects are
# not asserted here: this fixture declares none, so an assertion would be
# vacuously green and worse than no assertion at all.
if [ "$TARGET" = "aws" ]; then
  ident_n="$(livecert_rgta_count tofu-estate "$ESTATE")"
  log "  identity (tofu-estate=$ESTATE tags in the cloud): $ident_n resource(s)"
  [ "${ident_n:-0}" -gt 0 ] || fail "identity piece unused: no resource in the account carries tofu-estate=$ESTATE"
fi

# The values check is NOT under the TARGET=aws gate the identity check
# above keeps (#1145). Identity needs resourcegroupstaggingapi, whose floci
# coverage is its own question; the record store does not - floci serves
# both Parameter Store and S3, so a floci run that declares a cloud backend
# can and must prove the same thing an aws run does. That is what makes the
# s3 arm below something an emulator run exercises rather than a branch
# nothing has ever executed.
#
# Three-way with a loud default. It used to be `if ssm ... else`, and the
# else printed "(local disk)" over an s3 store: an s3 run skipped this check
# entirely and said the reason was a backend it was not using.
case "$RECORD_STORE_BACKEND" in
  s3)
    rec_n="$(s3_prefix_count "$S3_PREFIX")"
    log "  values (record_store s3 at s3://$RECORD_STORE_BUCKET/$S3_PREFIX): $rec_n object(s) in the bucket"
    [ "${rec_n:-0}" -gt 0 ] || fail "values piece unused: record_store is \"s3\" but s3://$RECORD_STORE_BUCKET/$S3_PREFIX holds no objects - the store was declared and never written"
    # No read-side check: a grep of the plan log for the store's name was
    # tried for the retired ssm arm and removed as vacuous (it matched the
    # provider's own type sweep), and the read side is proved at the cache
    # stage (5b), whose "state cache supplied N" line comes from the
    # projection itself.
    ;;
  local)
    log "  values: record_store is \"local\", a directory on disk beside the module, so the CLOUD values piece is NOT under test in this run"
    ;;
  *)
    fail "values piece not checked: unknown record_store backend \"$RECORD_STORE_BACKEND\" - refusing to report a state-model verdict for a store this harness cannot list"
    ;;
esac

log "=== 4b. test_plan: throttling/pagination read from the debug log ==="
if [ "$THROTTLE_LOG" = "1" ] && [ -f "$PLAN_LOG" ]; then
  read -r PLAN_LOG_BYTES THROTTLE_HITS RETRY_LINES PAGINATION_HITS <<< "$(analyze_debug_log "$PLAN_LOG")"
  log "  test_plan debug log: ${PLAN_LOG_BYTES} bytes, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} genuine-retry line(s), ${PAGINATION_HITS} pagination-continuation line(s)"
else
  PLAN_LOG_BYTES=0 THROTTLE_HITS=0 RETRY_LINES=0 PAGINATION_HITS=0
  log "  THROTTLE_LOG=$THROTTLE_LOG - not instrumented for this stage"
fi

log "=== 4b2. test_plan: API call total (#1051, INTENTIUS/chant-bench#33) - moved ahead of the pass/fail split below, not after it as 4e used to run this same analysis, so BOTH the pass and the deferred-failure path get the number rather than only whichever one used to run analyze_api_calls second ==="
analyze_api_calls "choudoufu-first" "$PLAN_LOG"
CHOUDOUFU_PLAN_CALLS="$API_CALLS_LAST_TOTAL"

# The deferred failure from the gating plan (see TP_FAIL above) is taken
# HERE, after the plan-timing measurement has had its chance to run. This is
# the stage's real failure: same message, same fail(), verdict still `fail`.
if [ -n "$TP_FAIL" ]; then
  log "=== 4d (fallback path): the gating plan did NOT pass, so this stage will fail - taking the plan-timing measurement first, because it is the one number this run was dispatched for (#588) ==="
  gauntlet_end_stage
  timed_plans "choudoufu" "$ADOPTED_DIR" "$TOFU"
  log "=== PLAN TIMING SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) - PARTIAL ==="
  log "  WARNING: choudoufu's gating plan was NOT a no-change plan, so the two sides below are NOT like-for-like. Read each run's own verdict, not the seconds alone."
  printf '%s\n' "$PLAN_TIMING_REPORT"
  log "  stage-gating choudoufu plan, measured separately WITH TF_LOG=DEBUG: ${PLAN_S}s (${PLAN_LOG_BYTES} bytes of debug log written inside that region)"
  # #622's refinement count is a property of the sweep, not of whether the
  # plan came back empty, so a run that is about to fail this stage still
  # yields it - already computed at 4b2 above (CHOUDOUFU_PLAN_CALLS), before
  # this branch even ran, for exactly that reason; not re-run here.
  instrumented_plan "choudoufu-steady" "$ADOPTED_DIR" "$TOFU"
  log "=== API CALL SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) - PARTIAL ==="
  printf '%s\n' "$API_CALL_REPORT"
  CURRENT_STAGE=test_plan
  heartbeat_start test_plan
  # index_lag_s (#1046, #1049) rides along on the SAME detail string a
  # refusal already carries, so a row that reads DIRECT_READ_UNRESOLVED
  # also names how long the index had been given to catch up before this
  # plan ran, without a second field the runner would need to know about -
  # gauntlet_stage's own detail is free text to end of line (see
  # live/e2e/lib/gauntlet.sh), so this needs no change there.
  #
  # index_converged=/index_target= (#1143) ride the same way, and they are
  # what stop index_lag_s from lying. On its own, index_lag_s=3600 says only
  # "the wait took an hour"; it cannot say whether the index caught up at
  # 3600s or the bound tripped, and before #1143 the bound tripped on every
  # real-AWS run because the target could not be reached. A recorded row now
  # names the target that was actually polled to and whether it was met.
  #
  # seconds=/
  # throttle=/retry= (issue #1051) ride the same way: 4b above already
  # measured them before TP_FAIL was ever checked, so a refused plan still
  # reports whatever it cost up to the refusal. plan_calls_choudoufu=/
  # plan_calls_stock= (#1051, INTENTIUS/chant-bench#33) are the same idea for
  # the plan's own API-call total: each is appended only when its own
  # variable is non-empty (THROTTLE_LOG=0 leaves no debug log for 4b2 to
  # read, and a RESUMED run skips 2d entirely) - an absent token is what
  # tools/gauntlet/scalerecord.go's parser then leaves absent, never reading
  # it as a zero that was never measured.
  PLAN_CALLS_TOKENS=""
  [ -n "$CHOUDOUFU_PLAN_CALLS" ] && PLAN_CALLS_TOKENS="plan_calls_choudoufu=${CHOUDOUFU_PLAN_CALLS}"
  [ -n "$STOCK_PLAN_CALLS" ] && PLAN_CALLS_TOKENS="${PLAN_CALLS_TOKENS}${PLAN_CALLS_TOKENS:+ }plan_calls_stock=${STOCK_PLAN_CALLS}"
  fail "${TP_FAIL} ${INDEX_NOTE}; index_lag_s=${INDEX_LAG_S} index_converged=${INDEX_CONVERGED} index_target=${INDEX_TARGET_N} seconds=${PLAN_S} throttle=${THROTTLE_HITS} retry=${RETRY_LINES} ${PLAN_CALLS_TOKENS}"
fi

log "=== 4c. test_plan: rendered identity checked against the AWS CLI directly (spot check: the zone and one team role) ==="
ZONEID="$(livecert_aws route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text)"
[ -n "$ZONEID" ] && [ "$ZONEID" != "None" ] || fail "no live zone found for prefix $PREFIX"
ZTAG="$(livecert_aws route53 list-tags-for-resource --resource-type hostedzone --resource-id "${ZONEID#/hostedzone/}" --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$ZTAG" = "aws_route53_zone.main" ] || fail "the zone carries tofu-address=$ZTAG, not aws_route53_zone.main - identity read via the AWS CLI, not choudoufu's own report"
ROLEARN="$(livecert_aws iam get-role --role-name "${PREFIX}-team-0000-role" --query 'Role.Arn' --output text)"
[ -n "$ROLEARN" ] && [ "$ROLEARN" != "None" ] || fail "no live role found for ${PREFIX}-team-0000-role"
RTAG="$(livecert_aws iam list-role-tags --role-name "${PREFIX}-team-0000-role" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$RTAG" = "aws_iam_role.team_0000_role" ] || fail "the role carries tofu-address=$RTAG, not aws_iam_role.team_0000_role"
log "  zone $ZONEID and role $ROLEARN: tofu-address confirmed via the AWS CLI directly"
# plan_calls_choudoufu=/plan_calls_stock= (#1051, INTENTIUS/chant-bench#33):
# see the identical construction and its comment on the TP_FAIL/fail() path
# above - same two variables, same "only append what was actually measured"
# rule, just on the pass path instead of the refusal path.
PLAN_CALLS_TOKENS=""
[ -n "$CHOUDOUFU_PLAN_CALLS" ] && PLAN_CALLS_TOKENS="plan_calls_choudoufu=${CHOUDOUFU_PLAN_CALLS}"
[ -n "$STOCK_PLAN_CALLS" ] && PLAN_CALLS_TOKENS="${PLAN_CALLS_TOKENS}${PLAN_CALLS_TOKENS:+ }plan_calls_stock=${STOCK_PLAN_CALLS}"
gauntlet_stage test_plan pass "post-migrate plan is empty in ${PLAN_S}s; zone/role tofu-address confirmed via the AWS CLI; debug log ${PLAN_LOG_BYTES} bytes, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} retry line(s); ${INDEX_NOTE}; index_lag_s=${INDEX_LAG_S} index_converged=${INDEX_CONVERGED} index_target=${INDEX_TARGET_N} seconds=${PLAN_S} throttle=${THROTTLE_HITS} retry=${RETRY_LINES} ${PLAN_CALLS_TOKENS}$HOLD_TAG"

# Issue #578: the same three-run, TF_LOG-unset measurement stock got at
# 2c, on the migrated estate, so the two sides differ in the binary and
# the state model and in nothing else this script controls. The gating
# plan above keeps its debug instrumentation and stays out of this number.
gauntlet_end_stage
log "=== 4d. plan timing: choudoufu plan x3, migrated estate, TF_LOG unset (#578) ==="
timed_plans "choudoufu" "$ADOPTED_DIR" "$TOFU"
log "=== PLAN TIMING SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) ==="
printf '%s\n' "$PLAN_TIMING_REPORT"
log "  stage-gating choudoufu plan, measured separately WITH TF_LOG=DEBUG: ${PLAN_S}s (${PLAN_LOG_BYTES} bytes of debug log written inside that region)"

# The stage-gating plan at step 4 is the FIRST plan after migration: cold
# hint store, widest sweep. The one below is the FIFTH, taken after
# timed_plans' three, so it is the steady state the sweep narrowing (#627)
# is supposed to have narrowed. #622 asks about the steady state, but the
# first plan is the only thing the difference can be read against, so both
# are counted and reported separately.
log "=== 4e. API calls: the FIRST post-migration plan (stage-gating, already TF_LOG=DEBUG) - computed at 4b2 above, before the pass/fail split, not re-run here (its line is already in API_CALL_REPORT, printed below) ==="
log "=== 4f. API calls: one EXTRA steady-state choudoufu plan with TF_LOG=DEBUG, outside every timed region ==="
instrumented_plan "choudoufu-steady" "$ADOPTED_DIR" "$TOFU"
log "=== API CALL SUMMARY (scale=$SCALE, ${EXPECTED} resources, target=$TARGET) ==="
printf '%s\n' "$API_CALL_REPORT"

# ══════════════════════════════════════════════════════════════════════
# WALLCLOCK_TRACE (issue #867): the choudoufu half, and the analysis.
#
# Three steady-state plans, taken after 4d's three timed runs and 4f's, so the
# hint store is warm and the sweep is the narrowed one (#627) rather than the
# first plan's widest. Then wallclock-gaps.py over all six captures at once,
# so the run log itself carries the comparison instead of it living only in a
# directory someone has to still have.
#
# The gap threshold is 0.8s because that is the one #683 reported at, and a
# re-measurement that moves its own threshold is not a re-measurement.
# ══════════════════════════════════════════════════════════════════════
if [ "${WALLCLOCK_TRACE:-0}" = "1" ]; then
  log "=== 4g. wall-clock trace (#867): choudoufu plan x3 with TF_LOG=DEBUG, steady state ==="
  for i in 1 2 3; do
    instrumented_plan "trace-choudoufu-$i" "$ADOPTED_DIR" "$TOFU"
  done

  log "=== 4h. wall-clock trace (#867): idle gaps >= 0.8s, both sides, one instrument ==="
  # The label=path list is built in the positional parameters rather than in
  # an array: this script runs under `set -u`, and in bash 3.2 - the bash
  # macOS ships, and the one a maintainer running this by hand is most likely
  # to hit - expanding an EMPTY array under `set -u` is an unbound-variable
  # error, so the "nothing to analyze" branch would abort the run instead of
  # reporting.
  set --
  for i in 1 2 3; do
    [ -f "$WORK/apicalls_trace-stock-$i.debug.log" ] \
      && set -- "$@" "stock-$i=$WORK/apicalls_trace-stock-$i.debug.log"
  done
  for i in 1 2 3; do
    [ -f "$WORK/apicalls_trace-choudoufu-$i.debug.log" ] \
      && set -- "$@" "choudoufu-$i=$WORK/apicalls_trace-choudoufu-$i.debug.log"
  done
  if [ "$#" -eq 0 ]; then
    log "  no trace captures on disk - nothing to analyze"
  elif ! command -v python3 >/dev/null 2>&1; then
    log "  python3 is not on PATH; the captures are under $WORK, run live/live-cert/wallclock-gaps.py over them by hand"
  else
    python3 "$ROOT/live/live-cert/wallclock-gaps.py" --min 0.8 "$@" \
      | tee "$WORK/wallclock-gaps.txt" | sed 's/^/  /'
  fi
fi

# ══════════════════════════════════════════════════════════════════════
# test_apply: applying the empty plan is a genuine no-op.
# ══════════════════════════════════════════════════════════════════════
CURRENT_STAGE=test_apply
heartbeat_start test_apply
log "=== 5. test_apply: the empty plan applies as a genuine no-op ==="
BEFORE_N="$(livecert_rgta_count tofu-cert-run "$RUN_ID")"
NOOP_OUT="$(cd "$ADOPTED_DIR" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_RC=$?
[ "$NOOP_RC" -eq 0 ] || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply exited $NOOP_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_OUT" \
  || { grep -E 'Apply complete' <<< "$NOOP_OUT"; fail "the no-op apply was not a genuine no-op"; }
AFTER_N="$(livecert_rgta_count tofu-cert-run "$RUN_ID")"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
log "  genuine no-op: $BEFORE_N objects before, $AFTER_N after"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at $BEFORE_N objects=$BEFORE_N$HOLD_TAG"

log "=== 5b. state cache: written by the apply, and USED by the plan after it (#685) ==="
# Placement matters and the first attempt got it wrong. test_apply (stage 5)
# is the first choudoufu APPLY, so it is the first thing that can write a
# cache - a check before it would have asserted against a file that cannot
# exist yet. This runs one more plan, after the apply, which is the first plan
# in the whole harness that has a cache to read.
#
# A cache that is written and never consulted is indistinguishable from a
# working one in every other line of this log, which is the state this fork
# shipped for months while its documentation described a cache. So all three
# halves are asserted and zero hits FAILS rather than warns.
if [ -n "${CHOUDOUFU_STATE_CACHE:-}" ]; then
  [ -s "$CHOUDOUFU_STATE_CACHE" ] \
    || fail "state cache enabled at $CHOUDOUFU_STATE_CACHE and the apply wrote nothing there"
  log "  written: $(wc -c < "$CHOUDOUFU_STATE_CACHE" | tr -d " ")B at $CHOUDOUFU_STATE_CACHE"

  CACHE_LOG="$WORK/cache_plan.debug.log"
  CACHE_OUT="$(cd "$ADOPTED_DIR" && TF_LOG=DEBUG TF_LOG_PATH="$CACHE_LOG" "$TOFU" plan -input=false -no-color 2>&1)"; CACHE_RC=$?
  [ "$CACHE_RC" -eq 0 ] || { printf '%s\n' "$CACHE_OUT" | tail -20; fail "the post-apply plan exited $CACHE_RC"; }
  grep -qF "No changes. Your infrastructure matches the configuration." <<< "$CACHE_OUT" \
    || fail "the post-apply plan was not empty, so a cache hit count from it would not be comparable"

  CACHE_HITS="$(grep -oE "state cache supplied [0-9]+ instance" "$CACHE_LOG" 2>/dev/null | grep -oE "[0-9]+" | tail -1)"
  [ -n "$CACHE_HITS" ] \
    || fail "the plan never reported a state-cache result; the cache was written but the plan did not load it"
  log "  USED: the post-apply plan answered $CACHE_HITS instance(s) from the cache instead of reading them"
  [ "$CACHE_HITS" -gt 0 ] \
    || fail "the state cache was written and loaded and supplied 0 instances - written, not used"
else
  log "  CHOUDOUFU_STATE_CACHE unset, so the cache half is NOT under test in this run"
fi

CURRENT_STAGE=""
heartbeat_stop
gauntlet_end
log "=== all four stages passed against target=$TARGET scale=$SCALE; teardown runs next via the EXIT trap ==="
log "=== THROTTLE SUMMARY (target=$TARGET scale=$SCALE) ==="
log "  cold_deploy apply (${EXPECTED} resources, parallelism=10, single Route53 zone): ${COLD_APPLY_S}s, debug log ${COLD_LOG_BYTES}B, ${COLD_THROTTLE_HITS} throttling-error line(s), ${COLD_RETRY_LINES} genuine-retry line(s)"
log "  migrate -approve (${VERIFIED} sequential tag-write API calls): ${MIGRATE_S}s, debug log ${MIGRATE_LOG_BYTES}B, ${MIGRATE_THROTTLE_HITS} throttling-error line(s), ${MIGRATE_RETRY_LINES} genuine-retry line(s)"
log "  test_plan (choudoufu's own O(types) full-account sweep): ${PLAN_S}s, debug log ${PLAN_LOG_BYTES}B, ${THROTTLE_HITS} throttling-error line(s), ${RETRY_LINES} genuine-retry line(s), ${PAGINATION_HITS} pagination-continuation line(s)"
