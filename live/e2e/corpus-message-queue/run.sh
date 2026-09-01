#!/usr/bin/env bash
# (moved from the justfile's retired demo-corpus-message-queue recipe; run with: just demo-run corpus-message-queue)
# Issue #274's crossing, on a real third-party estate rather than a fixture:
# .corpus/mastino/prod-eu-west/services/message-queue, 28 aws_sqs_queue in a
# module and one aws_iam_policy in the root, kept by its authors in a
# Terraform Cloud workspace. The estate is copied out of .corpus and never
# written to; the four onboarding deltas it needs are applied to the copy and
# each one is asserted. It applies 29 resources, writes no terraform.tfstate
# at all, replans empty twice, and every one of the 29 rendered import
# identities is checked as a string - the emulator answers a wrong-region SQS
# queue URL with the right queue's ARN, so the plan verdict cannot tell a
# wrong region from a right one and only the string can. BREAK=1 corrupts one
# expected string and the run must then fail in step 6 and nowhere else.
# Needs Docker, the AWS CLI and a fetched corpus; runs on its own port (4632)
# so it can run beside `just demo`.
set -euo pipefail

# A real third-party estate, crossed against a real emulator.
#
# The estate is .corpus/mastino/prod-eu-west/services/message-queue: 28
# aws_sqs_queue in a module plus one aws_iam_policy in the root, written by
# people who have never heard of this fork, kept in a Terraform Cloud
# workspace. It is not copied into this repository - the run reads it out of
# .corpus, copies it to a temp directory, and applies the onboarding deltas
# there. .corpus is shared between worktrees and is never written to.
#
# Issue #274 is the reason this exists. 28 real estates pass live-check with
# zero refused sites and one of them had ever been run against a cloud. An
# offline pass and "applies, loses its state file, and replans empty" are
# different claims, and the second is the product.
#
# THE DELTAS. Four edits stand between the estate as its authors wrote it and
# a run, and none of them was predicted by any offline instrument:
#
#   1. terraform.tf declares `cloud { organization = "datacite-ng" ... }`.
#      OpenTofu's cloud backend requires a `hostname`, which the block does
#      not set, so `init` dies with "Hostname is required for the cloud
#      backend" before any provider is installed. A module may declare a
#      backend or a live block, never both, so the block goes (issue #268).
#
#   2. terraform.tf asks for aws `~> 5`, which resolves to 5.100.0 - a
#      release with no list resources at all. live-plan then fails with
#      "Unlistable marker-discovered type: The provider cannot list
#      aws_iam_policy". refusal-probe reports this estate blocked 0, sites 0.
#      Pinning = 6.58.0 clears it (issue #269).
#
#   3. input.tf reads `data "template_file"`. hashicorp/template v2.2.0 has no
#      darwin_arm64 package, so `init` fails outright on an Apple Silicon
#      machine. The provider is deprecated in favour of the templatefile()
#      function, which is the edit made here. Nothing to do with this fork.
#
#   4. The provider block needs the emulator's overrides.
#
# WHAT IS ASSERTED, and why it is not the verdict. An empty plan says the
# instances bound. It does not say WHAT they bound BY. live/e2e/per-element
# was run with its canonicalising sort deliberately removed and the plan
# stayed empty, because a set has no order on the wire. The same trap is
# live here and was measured rather than assumed: floci answers
#
#   sqs get-queue-attributes --queue-url \
#     https://sqs.us-east-1.amazonaws.com/000000000000/production_doi
#
# with the eu-west-1 queue's own ARN, when the request is actually signed
# for eu-west-1 (i.e. the run's one configured provider region). A region
# rendered wrong in the import identity would round-trip silently. So step 5
# reads all 29 rendered identity strings out of the run's own trace and
# asserts each one.
#
# Issue #287 item 3 asked whether this is a floci gap. It is not: verified
# against AWS's own "Fix QueueDoesNotExist" guidance (the SDK/CLI never
# derive the destination region from the QueueUrl text; the client's actual
# configured region decides it) and against hashicorp/terraform-provider-aws
# #44777, where a provider maintainer confirms importing cross-region needs
# an explicit "@<region>" suffix on the id - a plain queue URL's embedded
# host is never parsed for it. Rendered identities here carry no such
# suffix. floci was measured (2026-08-18) reproducing exactly this: signing
# for the SAME actual region a queue lives in but naming a DIFFERENT one in
# the queue-url text still resolves and returns the true ARN (floci and
# real AWS agree); signing for a genuinely DIFFERENT actual region correctly
# answers QueueDoesNotExist (also verified, both ways, with two same-named
# queues in two regions so the response body - not just the error/success
# split - proves which region actually answered). So the probe below is not
# an open emulator defect awaiting a fix; it is confirmation that a plain
# queue-URL identity's region segment is structurally unobservable on the
# wire once a run has one fixed provider region, on real AWS as much as on
# floci. Step 5's string-level assertion is the permanent discriminator for
# this class, not a stand-in for a fix that never lands.
#
#   bash live/e2e/corpus-message-queue/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4632, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602 and per-element's 4604).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string before
#                step 5. The run must then FAIL in step 5 and nowhere else:
#                that is the proof the identity assertion is load-bearing
#                rather than a grep that always matches.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads command output, an exit code, or the emulator's own answer through
# the AWS CLI.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_ESTATE="$ROOT/.corpus/mastino/prod-eu-west/services/message-queue"
CORPUS_MODULE="$ROOT/.corpus/mastino/modules/datacite_queues"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4632}"
FLOCI_NAME="choudoufu-corpus-mq-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="mq-crossing-e2e"
REGION="eu-west-1"
ACCOUNT="000000000000"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# The 28 queue names the module declares, with the "production" environment
# the root module passes. Listed here rather than derived from the .tf so a
# module that quietly stopped declaring one is a failure and not a smaller
# expectation.
QUEUES=(
  production_analytics
  production_dead-letter
  production_doi
  production_enriched_doi_index_job
  production_enrichment_batch_process_job
  production_events
  production_events_index
  production_events_other_doi_by_id_job
  production_events_other_doi_job
  production_events_reindex_daily
  production_levriero
  production_levriero_usage
  production_lupo
  production_lupo_background
  production_lupo_doi_registration
  production_lupo_import
  production_lupo_import_other_doi
  production_lupo_queue_batches_datacite_doi
  production_lupo_queue_batches_other_doi
  production_lupo_support
  production_lupo_support_dead-letter
  production_lupo_transfer
  production_queue_batches_datacite_doi-dead-letter
  production_queue_batches_other_doi-dead-letter
  production_salesforce
  production_sashimi
  production_usage
  production_volpino
)

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
[ -d "$CORPUS_ESTATE" ] || fail "$CORPUS_ESTATE is missing. Run 'just corpus-fetch' first."
[ -d "$CORPUS_MODULE" ] || fail "$CORPUS_MODULE is missing. Run 'just corpus-fetch' first."

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU from $ROOT"
fi

# ── 1. copy the estate out of .corpus ───────────────────────────────────────
# .corpus is shared across every worktree. A dirty checkout there silently
# contaminates every later measurement, so nothing below ever writes into it.
log "=== 1. copy the estate out of .corpus ==="
MAIN="$WORK/mastino/prod-eu-west/services/message-queue"
mkdir -p "$WORK/mastino/prod-eu-west/services" "$WORK/mastino/modules"
cp -R "$CORPUS_ESTATE" "$WORK/mastino/prod-eu-west/services/"
cp -R "$CORPUS_MODULE" "$WORK/mastino/modules/"
rm -rf "$MAIN/.terraform" "$MAIN/.terraform.lock.hcl"
chmod -R u+w "$WORK"
[ -f "$MAIN/main.tf" ] || fail "the copy did not land: $MAIN/main.tf is missing"
log "  copied to $MAIN; the module's relative source path is preserved"

# ── 2. the four deltas ──────────────────────────────────────────────────────
log "=== 2. the four onboarding deltas ==="

# Delta 1 and 2: the cloud block goes, a live block replaces it, aws is
# pinned. Asserted afterwards rather than assumed - a perl substitution that
# matched nothing looks exactly like one that worked.
perl -0pi -e 's/\n  cloud \{.*?\n  \}\n/\n  live {\n    estate = "'"$ESTATE"'"\n  }\n/s' "$MAIN/terraform.tf"
perl -pi -e 's/version = "~> 5"/version = "= 6.58.0"/' "$MAIN/terraform.tf"
grep -q 'cloud {' "$MAIN/terraform.tf" && fail "delta 1 did not remove the cloud block"
grep -q "estate = \"$ESTATE\"" "$MAIN/terraform.tf" || fail "delta 1 did not add the live block"
grep -q 'version = "= 6.58.0"' "$MAIN/terraform.tf" || fail "delta 2 did not pin the provider"

# Delta 3: data "template_file" -> templatefile(). The provider has no
# darwin_arm64 build and is deprecated in favour of the function.
perl -0pi -e 's/data "template_file" "queue" \{.*?\n\}/locals {\n  queue_policy = templatefile("\${path.module}\/sqs.json", {\n    region = var.region\n  })\n}/s' "$MAIN/input.tf"
perl -pi -e 's/data\.template_file\.queue\.rendered/local.queue_policy/' "$MAIN/main.tf"
grep -q 'template_file' "$MAIN/input.tf" && fail "delta 3 did not remove the template_file data source"
grep -q 'local.queue_policy' "$MAIN/main.tf" || fail "delta 3 did not rewire the policy argument"
grep -qF 'templatefile("${path.module}/sqs.json"' "$MAIN/input.tf" \
  || { grep -n 'templatefile' "$MAIN/input.tf"
       fail "delta 3 wrote a templatefile() path perl interpolated away"; }

# Delta 4: the emulator's provider overrides.
perl -pi -e 's/^  region     = var\.region$/  region     = var.region\n\n  skip_credentials_validation = true\n  skip_requesting_account_id  = true\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true/' "$MAIN/input.tf"
grep -q 's3_use_path_style' "$MAIN/input.tf" || fail "delta 4 did not add the emulator overrides"
log "  all four applied and verified in the copied files"

# ── 3. floci ────────────────────────────────────────────────────────────────
log "=== 3. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"sqs"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
grep -q '"sqs"' <<< "$HEALTH" || fail "floci did not come up healthy (sqs) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"
export TF_VAR_access_key=test TF_VAR_secret_key=test

# The wire does not discriminate the region segment of an SQS import
# identity when the request's actual signed region is the one the queue
# really lives in - which is the only case this run's single fixed provider
# region can ever produce. Measured here rather than asserted, and the
# measurement is what makes step 6 load-bearing: a genuinely wrong ACTUAL
# region (the AWS CLI's own --region, not just the queue-url text) IS caught,
# by both floci and real AWS - see the "WHAT IS ASSERTED" note above, which
# has the #287-3 investigation and citations. That is a different, narrower
# fact than "nothing about a queue URL is ever region-scoped," and this
# probe exists to keep that distinction from eroding: if it ever starts
# failing, a same-actual-region/wrong-label queue URL WOULD be caught by the
# plan and step 6 becomes belt and braces instead of the only discriminator.
probe_region_blindness() {
  awsl sqs create-queue --queue-name probe-region-blindness >/dev/null 2>&1 || return 1
  awsl sqs get-queue-attributes \
    --queue-url "https://sqs.us-east-1.amazonaws.com/${ACCOUNT}/probe-region-blindness" \
    --attribute-names QueueArn >/dev/null 2>&1
}
if probe_region_blindness; then
  log "  probe: signed for $REGION, a queue-url TEXT naming us-east-1 still"
  log "         resolves - confirmed to match real AWS (#287 item 3), not a"
  log "         floci gap: the wire never validates that text, only the"
  log "         request's actual region, which step 6 cannot vary here"
else
  log "  probe: the emulator rejected a wrong-region queue URL (noted; step 6"
  log "         is then belt and braces rather than the only discriminator)"
fi
awsl sqs delete-queue --queue-url "https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/probe-region-blindness" >/dev/null 2>&1 || true

# ── 4. stand the estate up ──────────────────────────────────────────────────
log "=== 4. init and apply: 28 queues in a module, 1 policy in the root ==="
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 29 added' <<< "$APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY_OUT"; fail "the apply did not create exactly 29 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# Read the queues back through the AWS CLI, never through choudoufu. A queue
# that is not really there and a queue that binds to nothing both produce
# silence in every plan assertion below.
LIVE_QUEUES="$(awsl sqs list-queues --query 'QueueUrls' --output text | tr '\t' '\n' | sed 's|.*/||' | sort)"
for q in "${QUEUES[@]}"; do
  grep -qxF "$q" <<< "$LIVE_QUEUES" || fail "queue $q is not live; the module did not declare or create it"
done
LIVE_N="$(wc -l <<< "$LIVE_QUEUES" | tr -d ' ')"
[ "$LIVE_N" = "28" ] || fail "expected 28 live queues, the emulator has $LIVE_N"
log "  all 28 queues live, read back through the AWS CLI"

# Every object here is taggable, so all 29 carry markers and the "identity
# with no carrier" group is empty for this estate. Asserted as a number: a
# dead tagging index would otherwise read as a passing run.
MARKED="$(awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE" \
  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
[ "$MARKED" = "29" ] \
  || fail "expected 29 objects carrying tofu-estate=$ESTATE in the tagging index, got $MARKED"
log "  29 of 29 objects carry markers; nothing here relies on a carrier-free identity"

# ── 5. there is no state file to delete ─────────────────────────────────────
# The per-element fixture deletes one here. This estate never produced one:
# apply under a live block wrote no terraform.tfstate at all. Asserted rather
# than narrated, because "we deleted it" and "it was never written" are
# different claims and only the second one is what issue #73 promises.
log "=== 5. no state file was ever written ==="
[ ! -f "$MAIN/terraform.tfstate" ] \
  || fail "apply under a live block wrote a terraform.tfstate; nothing may read or write one"
[ ! -f "$MAIN/terraform.tfstate.backup" ] || fail "a terraform.tfstate.backup exists"
log "  no terraform.tfstate, no backup; the markers are all the identity there is"

# ── 6. THE VALUE, not the verdict ───────────────────────────────────────────
log "=== 6. live-plan, and the 29 rendered identities read out of the run ==="
set +e
PLAN_OUT="$(cd "$MAIN" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
set -e
[ "$PLAN_RC" -eq 0 ] || { grep -vE '^\d{4}-' <<< "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { grep -vE '^\d{4}-' <<< "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the plan is not empty"; }
grep -qE '^Foreign resources: (none|nothing was swept)' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"
       fail "the plan reports foreign resources; every live object here carries this estate's markers"; }

WANT=()
for q in "${QUEUES[@]}"; do
  WANT+=("https://sqs.${REGION}.amazonaws.com/${ACCOUNT}/${q}")
done
WANT+=("arn:aws:iam::${ACCOUNT}:policy/sqs")

# The deliberate break. With BREAK=1 one expected string is corrupted in
# exactly the way the wire cannot see - the region segment - and the loop
# below must go red. Everything above it must still pass, which is the whole
# point: the verdict does not move, only the assertion does.
if [ "${BREAK:-}" = "1" ]; then
  WANT[0]="https://sqs.us-east-1.amazonaws.com/${ACCOUNT}/${QUEUES[0]}"
  log "  BREAK=1: expecting ${WANT[0]}, which is the same object with the"
  log "           wrong region. The plan above stayed empty. Step 6 must fail."
fi

for id in "${WANT[@]}"; do
  grep -qF "from import identity \"$id\"" <<< "$PLAN_OUT" || {
    grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
    fail "no instance materialized from import identity \"$id\". The identities the run actually rendered are listed above."
  }
done
GOT_N="$(grep -oE 'from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u | wc -l | tr -d ' ')"
[ "$GOT_N" = "29" ] \
  || fail "the run materialized $GOT_N distinct identities, expected 29"
log "  all 29 asserted, and no thirtieth"

# The controls. These are the strings a plausible defect would produce, and
# every one of them names the same live object as far as the emulator is
# concerned. None may appear.
for bad in \
  "https://sqs.us-east-1.amazonaws.com/${ACCOUNT}/${QUEUES[2]}" \
  "http://sqs.${REGION}.localhost.localstack.cloud:4566/${ACCOUNT}/${QUEUES[2]}" \
  "from import identity \"${QUEUES[2]}\""
do
  grep -qF "$bad" <<< "$PLAN_OUT" \
    && fail "the run rendered \"$bad\". A wrong region round-trips silently (the probe in step 3 measured that), the endpoint-derived URL is the emulator's spelling rather than the identity, and a bare queue name is not an import identity at all."
done
log "  wrong-region, endpoint-derived and bare-name spellings all absent"

# ── 7. and it converges ─────────────────────────────────────────────────────
# One empty plan is not evidence. Issue #266's defect produced one new
# resource per run because every run started where the last one did.
log "=== 7. the next run proposes nothing, and applying it adds nothing ==="
set +e
PLAN2_OUT="$(cd "$MAIN" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN2_RC=$?
set -e
[ "$PLAN2_RC" -eq 0 ] || { printf '%s\n' "$PLAN2_OUT" | tail -30; fail "the second live-plan exited $PLAN2_RC"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN2_OUT" \
  || { printf '%s\n' "$PLAN2_OUT" | grep -vE '^\s*$' | tail -30
       fail "the second plan is not empty, so the run does not converge"; }

APPLY2_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Resources: [1-9][0-9]* added' <<< "$APPLY2_OUT" \
  && { printf '%s\n' "$APPLY2_OUT" | tail -20
       fail "the second apply added a resource over one the estate already owns - issue #266's shape"; }
AFTER_N="$(awsl sqs list-queues --query 'length(QueueUrls)' --output text)"
[ "$AFTER_N" = "28" ] || fail "the emulator has $AFTER_N queues after the second apply, expected 28"
[ ! -f "$MAIN/terraform.tfstate" ] || fail "a state file exists after the second run"
log "  converged: nothing proposed, nothing added, still 28 queues, still no state file"

log ""
log "=== PASS ==="
log ""
log "A third-party estate its authors keep in Terraform Cloud applied 29"
log "resources, wrote no state file at all, and replanned empty twice - and"
log "each of the 29 bound by an identity string this run printed and this"
log "script checked. Run it again with BREAK=1: everything above step 6"
log "still passes and step 6 goes red, which is what the string assertion"
log "buys over the verdict."
