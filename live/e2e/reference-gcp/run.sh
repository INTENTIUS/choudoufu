#!/usr/bin/env bash
# reference-gcp: the gcp lane's first estate (#1119, under #1121's
# ruling), crossed on the floci-gcp substrate. A hand-written reference
# shape kept in this repository: a VPC network, a subnetwork, a firewall, a
# Cloud Storage bucket, a service account, a Pub/Sub topic, a blank boot
# disk and a Compute instance on it, plus a two-instance count topic for the
# count stage. Ten instances, no AWS provider anywhere. Four of them (the
# network, the subnetwork, the firewall, the service account) have no
# labels argument in hashicorp/google 8.6.0 and are declaration-carried:
# their identity is recomputed from this configuration. The other six carry
# tofu-estate and the encoded tofu-address label from the create call.
#
# The instance boots from a blank disk the configuration makes, because
# floci-gcp has no public image catalog (docs/services/compute.md); the
# emulator runs no guest, so nothing is lost.
#
# Two floci-gcp containers, both started for this run and removed after it:
#
#   A  the estate. Stock terraform cold-deploys it (cold_deploy), choudoufu
#      adopts it from stock's state (migrate) and runs every day-2 stage on
#      it, tears it down, then applies the same shape fresh with a live
#      block (greenfield) and compares the emulator with what stock's cold
#      deploy left.
#   B  the oracle. Stock terraform cold-deploys the identical shape and
#      applies every day-2 change itself, so each stage's "what stock does
#      for the same change" is stock's own plan on its own emulator.
#
# What reads differently on this substrate, per tools/gauntlet/stages.go's
# floci-gcp notes: identities are the resource names, read with the
# emulator's REST API; the marker count is the labelled types filtered on
# tofu-estate (gcp_labelled_count in gcp.sh); the out-of-band mutation is a
# REST PATCH; day2_rename exercises the moved-block half. The sweep is Cloud
# Asset search (lex00/floci-gcp#7) and labels must come back as written
# (lex00/floci-gcp#6); the pinned image serves both, so a stage that fails on
# either says so in its own verdict, the run goes on, and the failure is a
# floci-gcp finding to file on the fork.
#
# A stage this script cannot pass records fail with the reason and the run
# continues to the next; the runner, not the script, decides what "clear"
# means. Every stage after test_plan runs in a subshell (gcp_stage_done in
# live/e2e/lib/gcp.sh), so its fail() ends that stage only; a failed day-2
# stage is followed by a reconverging apply on both emulators, and the run
# exits 1 at the end if any stage failed. cold_deploy, migrate and test_plan
# still end the run, as does a fault in the substrate itself (an emulator
# that never came up).
#
#   go run ./tools/gauntlet run reference-gcp     # asks for "run" at the terminal
#   bash live/e2e/reference-gcp/run.sh
#
# Needs docker, terraform (the stock binary), curl and python3 on PATH.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT     host port for emulator A (default 4788; the oracle
#                  emulator B is FLOCI_PORT+1000). The runner assigns it.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls instead of the real checks:
#                  a second object is tampered and the single-object
#                  assertion must fail; the topic's own name is changed
#                  instead of its block's (a bare block rename plans the same
#                  one in-place address-label rewrite as the moved block,
#                  because the block name is not part of the topic's
#                  identity) and the marker-rewritten-in-place assertion
#                  must fail; the instance is dropped from greenfield's
#                  expected inventory and the object-by-object match must
#                  fail.
#   BREAK_REMOVE   set to 1 to keep the firewall block and assert no destroy
#                  is proposed (day2_remove's own Break line).
#   BREAK_COUNT    set to 1 to assert the wrong instance was destroyed on the
#                  scale-down (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved and
#                  expect success (plan_approval's Break line); must fail.
#   BREAK_REPLACE  set to 1 to recreate the replaced topic's old object,
#                  carrying the estate's labels and the block's address
#                  label, after day2_replace's apply; the next plan must not
#                  be empty (day2_replace's Break line).
#   BREAK_CRASH    set to 1 to assert, after the same real interrupt, that
#                  nothing is proposed (day2_crash's own Break line); must
#                  fail, because a recovered run proposes the remainder.
#   BREAK_CRASH_UNBOUND
#                  set to 1 to strip tofu-estate off the topic the
#                  interrupted apply did create before replanning; the real
#                  check must then fail to hold.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
#   BREAK_NO_LOCAL_STATE
#                  strip one declared object's tofu-estate label before
#                  no_local_state's plan with no local state; that plan must
#                  then propose something, so the check fails.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/e2e/lib/gcp.sh
source "$ROOT/live/e2e/lib/gcp.sh"
# shellcheck source=live/e2e/reference-gcp/gcp.sh
source "$ROOT/live/e2e/reference-gcp/gcp.sh"

gauntlet_plugin_cache
ESTATE="reference-gcp"
PROJECT="chdf-refgcp"
REGION="us-central1"
ZONE="us-central1-a"
BUCKET="${PROJECT}-refgcp-logs"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
FLOCI_PORT="${FLOCI_PORT:-4788}"
ORACLE_PORT=$((FLOCI_PORT + 1000))
EMU_A="choudoufu-reference-gcp-$$"; EMU_B="choudoufu-reference-gcp-oracle-$$"
EPA="http://127.0.0.1:${FLOCI_PORT}"; EPB="http://127.0.0.1:${ORACLE_PORT}"
# floci-gcp does not validate credentials unless its IAM enforcement is on,
# which this estate leaves off. hashicorp/google reads this variable.
export GOOGLE_OAUTH_ACCESS_TOKEN="chdf-gauntlet-fake-token"
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_floci_teardown "$EMU_A" "$EMU_B"
  rm -rf "$WORK"
}
trap cleanup EXIT
gcp_fail_flag "$WORK/stage-failed"
gauntlet_begin

# ── 0. tools ─────────────────────────────────────────────────────────────
log "=== 0. tools ==="
for t in docker terraform curl python3; do
  command -v "$t" >/dev/null 2>&1 || fail "$t is not on PATH"
done
docker info >/dev/null 2>&1 || fail "docker is not running"

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
# day2_crash needs the e2e build, for the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (internal/command/
# apply_e2etesting_crash.go), the same build reference-k8s makes.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the shape ────────────────────────────────────────────────────────────
# The hashicorp/google requirement is live/oracle-versions.json's
# google_provider_version (8.6.0, #1121), read once behind one fail.
GOOGLE_REQUIRED_PROVIDER="$(gauntlet_required_provider google hashicorp/google google_provider_version "    ")" \
  || fail "could not read the hashicorp/google pin from live/oracle-versions.json"

versions_block() { # $1 = "live" to include the live block; $2 = the emulator endpoint
  cat <<EOF
terraform {
  required_providers {
$GOOGLE_REQUIRED_PROVIDER
  }
EOF
  if [ "$1" = "live" ]; then cat <<EOF
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
EOF
  fi
  echo "}"
  echo
  # A refused block must stop the run: a root without it reaches googleapis.com.
  gcp_provider_block "$2" "$PROJECT" "$REGION" "$ZONE" || fail "gcp_provider_block refused (the line above says why); no root is written without the emulator endpoints"
}
# resource_block writes the ten instances. $1 is the events topic's block
# name (events, then stream after the rename); $2 is the shard count; $3 is
# an extra label line for the events topic (plan_approval's reviewed).
resource_block() {
  local topic="${1:-events}" shards="${2:-2}" extra="${3:-}"
  cat <<EOF

resource "google_compute_network" "main" {
  project                 = "$PROJECT"
  name                    = "refgcp-net"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "main" {
  project       = "$PROJECT"
  region        = "$REGION"
  name          = "refgcp-subnet"
  network       = google_compute_network.main.id
  ip_cidr_range = "10.10.0.0/24"
}

resource "google_compute_firewall" "ssh" {
  project       = "$PROJECT"
  name          = "refgcp-ssh"
  network       = google_compute_network.main.name
  source_ranges = ["10.10.0.0/16"]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}

resource "google_service_account" "app" {
  project    = "$PROJECT"
  account_id = "refgcp-app"
}

resource "google_storage_bucket" "logs" {
  project                     = "$PROJECT"
  name                        = "$BUCKET"
  location                    = "US"
  uniform_bucket_level_access = true
  force_destroy               = true
  labels = {
    tier = "logs"
  }
}

resource "google_pubsub_topic" "$topic" {
  project = "$PROJECT"
  name    = "refgcp-events"
  labels = {
    tier = "events"
$extra
  }
}

resource "google_compute_disk" "boot" {
  project = "$PROJECT"
  zone    = "$ZONE"
  name    = "refgcp-boot"
  type    = "pd-standard"
  size    = 10
}

resource "google_compute_instance" "vm" {
  project      = "$PROJECT"
  zone         = "$ZONE"
  name         = "refgcp-vm"
  machine_type = "e2-small"
  boot_disk {
    source      = google_compute_disk.boot.self_link
    auto_delete = false
  }
  network_interface {
    subnetwork = google_compute_subnetwork.main.self_link
  }
}

resource "google_pubsub_topic" "shard" {
  count   = $shards
  project = "$PROJECT"
  name    = "refgcp-shard-\${count.index}"
}
EOF
}
write_config() { # $1 dir, $2 live|stock, $3 endpoint, then resource_block's args
  local dir="$1" mode="$2" ep="$3"; shift 3
  { versions_block "$mode" "$ep"; resource_block "$@"; } > "$dir/main.tf"
}

# day2_crash's pair, appended to a root that already holds the shape.
# crash_second's name reads crash_first's, so the graph cannot reach it
# until crash_first has committed: the interrupt below is deterministic by
# construction, as in reference-k8s.
crash_pair_first() {
  cat <<EOF

resource "google_pubsub_topic" "crash_first" {
  project = "$PROJECT"
  name    = "refgcp-crash-first"
}
EOF
}
crash_pair_second() {
  cat <<EOF

resource "google_pubsub_topic" "crash_second" {
  project = "$PROJECT"
  name    = "\${google_pubsub_topic.crash_first.name}-second"
}
EOF
}

# ── emulator helpers ─────────────────────────────────────────────────────
ra() { gcp_rest "$EPA" "$@"; }
rb() { gcp_rest "$EPB" "$@"; }
stock_b() { ( cd "$ORACLE" && terraform "$@" ); }
count_a() { gcp_labelled_count "$EPA" "$PROJECT" "$ZONE" "$ESTATE"; }
exists_a() { ra GET "$1" >/dev/null 2>&1; }
topic_path() { printf 'v1/projects/%s/topics/%s' "$PROJECT" "$1"; }
plan_a() { ( cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1 ); }
apply_a() { ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 ); }
# reconverge: both emulators applied to the roots on disk, after a day-2
# stage failed partway.
reconverge() {
  local rc=0
  ( apply_a >/dev/null 2>&1 ) || { log "  the reconverging apply on A failed"; rc=1; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || { log "  stock's reconverging apply on B failed"; rc=1; }
  return $rc
}
stage_done() { gcp_stage_done "$@"; }

# The day-2 shape's pieces, here rather than in the stage that first uses
# them because every stage runs in its own subshell: plan_approval's label,
# day2_rename's moved block, day2_remove's firewall removal and
# day2_count's rewrite of both roots at a count.
REVIEWED='    reviewed = "yes"'
moved_block() { cat <<'EOF'

moved {
  from = google_pubsub_topic.events
  to   = google_pubsub_topic.stream
}
EOF
}
remove_fw() { python3 - "$1" <<'PY'
import re, sys
p = sys.argv[1]; s = open(p).read()
s = re.sub(r'\nresource "google_compute_firewall" "ssh" \{.*?\n\}\n', '\n', s, flags=re.S)
assert 'google_compute_firewall' not in s
open(p, 'w').write(s)
PY
}
scale_to() { # $1 count, both roots, firewall already gone
  write_config "$ADOPTED" live "$EPA" stream "$1" "$REVIEWED"; moved_block >> "$ADOPTED/main.tf"; remove_fw "$ADOPTED/main.tf" || fail "could not rewrite the adopted root at count $1"
  write_config "$ORACLE" stock "$EPB" stream "$1" "$REVIEWED"; moved_block >> "$ORACLE/main.tf"; remove_fw "$ORACLE/main.tf" || fail "could not rewrite the oracle root at count $1"
}

# ── 1. cold_deploy: stock stands the estate up on A (and B, the oracle) ──
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two floci-gcp containers, stock terraform applies the shape on each ==="
gcp_floci_up "$EMU_A" "$FLOCI_PORT" "$PROJECT" || fail "floci-gcp A ($EMU_A) did not come up on :$FLOCI_PORT"
gcp_floci_up "$EMU_B" "$ORACLE_PORT" "$PROJECT" || fail "floci-gcp B ($EMU_B) did not come up on :$ORACLE_PORT"
mkdir -p "$STOCK" "$ORACLE"
write_config "$STOCK" stock "$EPA"
write_config "$ORACLE" stock "$EPB"
( cd "$STOCK" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 10 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 10 instances on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(cd "$STOCK" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "10" ] || fail "stock's state holds $STOCK_N instances, want 10"
UNMARKED="$(count_a)" || fail "could not count labelled objects on A"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED object(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
gcp_inventory "$EPA" "$PROJECT" "$REGION" "$ZONE" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( cd "$ORACLE" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 10 added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy failed on B"; }
gauntlet_stage cold_deploy pass "10 instances (network, subnetwork, firewall, service account, bucket, topic, boot disk, instance, 2 count topics) from plain terraform against floci-gcp $(gcp_floci_image | sed 's/.*@sha256:\(.\{12\}\).*/sha256:\1/'), a real terraform.tfstate with 10 instances, zero tofu-estate labels read back with the REST API; the identical shape cold-deployed by stock on a second emulator as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
mkdir -p "$ADOPTED"
write_config "$ADOPTED" live "$EPA"
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
# The six labelled instances are stamped through a labels-only plan
# (#1121's google-labels-plan write). The four the provider gives no labels
# argument are SKIPPED with their identities recorded, as AWS's untaggable
# resources are (reference-eks): the skip is the marker write, and "10
# identities recorded" says all ten went into the record store. Only the
# missing-labels reason may be skipped; a failed stamp prints its own row.
read -r M_STAMPED M_SKIPPED M_SKIPPED_OTHER M_FAILED M_IDENTITIES < <(gcp_migrate_counts <<< "$APPROVE_OUT")
LABELLED="$(count_a)"
if [ "$M_STAMPED" = "6" ] && [ "$M_SKIPPED" = "4" ] && [ "$M_SKIPPED_OTHER" = "0" ] && [ "$M_FAILED" = "0" ] && [ "$M_IDENTITIES" = "10" ] && [ "$LABELLED" = "6" ]; then
  gauntlet_stage migrate pass "10 of 10 bound: the 6 labelled instances stamped through a labels-only plan and carrying tofu-estate=$ESTATE read back with the REST API; the 4 with no labels argument (network, subnetwork, firewall, service account) skipped for that reason alone, and all 10 identities recorded"
else
  gauntlet_stage migrate fail "live-import -approve did not bind 10 of 10 with 6 stamped, 4 skipped for having no labels argument, 0 failed and 10 identities recorded: ${SUMMARY_LINE:-it printed no summary line} ($M_SKIPPED_OTHER skipped for another reason, $M_IDENTITIES identities recorded); $LABELLED object(s) carry tofu-estate=$ESTATE on the emulator. FAILED rows: \"$(gcp_failed_rows <<< "$APPROVE_OUT")\". First Error: line: \"$(gauntlet_first_error_line <<< "$APPROVE_OUT")\". If the stamped count is right and the label count is not, the emulator did not keep the labels as written (lex00/floci-gcp#6)"
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with the REST API ==="
PLAN_OUT="$(plan_a)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed"; }
IDS_OK=1; IDS_MISSING=""
for path in "compute/v1/projects/$PROJECT/global/networks/refgcp-net" \
            "compute/v1/projects/$PROJECT/regions/$REGION/subnetworks/refgcp-subnet" \
            "compute/v1/projects/$PROJECT/global/firewalls/refgcp-ssh" \
            "v1/projects/$PROJECT/serviceAccounts/refgcp-app@$PROJECT.iam.gserviceaccount.com" \
            "storage/v1/b/$BUCKET" "$(topic_path refgcp-events)" \
            "compute/v1/projects/$PROJECT/zones/$ZONE/disks/refgcp-boot" \
            "compute/v1/projects/$PROJECT/zones/$ZONE/instances/refgcp-vm" \
            "$(topic_path refgcp-shard-0)" "$(topic_path refgcp-shard-1)"; do
  exists_a "$path" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $path"; }
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all 10 identities (resource names, projects/$PROJECT/...) confirmed present with the REST API"
else
  gauntlet_stage test_plan fail "the plan with no state file is not empty ($(gauntlet_plan_line <<< "$PLAN_OUT")) or an identity is missing (identities confirmed: $IDS_OK;${IDS_MISSING:- none missing})"
  log "  adopting through choudoufu's own apply so the day-2 stages below run on a labelled estate"
  ADOPT_OUT="$(apply_a)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the adopting apply failed"; }
  REPLAN="$(plan_a)" || fail "the replan after the adopting apply failed"
  grep -q "No changes." <<< "$REPLAN" || { printf '%s\n' "$REPLAN" | tail -20; fail "the replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
fi
[ "$(count_a)" = "6" ] || fail "$(count_a) object(s) carry tofu-estate=$ESTATE after adoption, want 6"

(
# ── 4. test_apply: no-op apply, marker count unchanged ───────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(apply_a)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); objects carrying tofu-estate=$ESTATE unchanged at $BEFORE_N across the four labelled types, counted with the REST API"

); stage_done $? test_apply reconverge

(
# ── 5. drift_reconverge: one object tampered out of band ─────────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: a REST PATCH on the bucket's tier label on A and on B; stock's plan on B is the oracle ==="
TAMPER='{"labels":{"tier":"tampered"}}'
ra PATCH "storage/v1/b/$BUCKET" "$TAMPER" >/dev/null || fail "could not tamper the bucket on A"
rb PATCH "storage/v1/b/$BUCKET" "$TAMPER" >/dev/null || fail "could not tamper the bucket on B"
if [ "${BREAK:-}" = "1" ]; then
  ra PATCH "$(topic_path refgcp-events)" '{"topic":{"labels":{"tier":"tampered"}},"updateMask":"labels"}' >/dev/null || fail "BREAK: could not tamper the topic on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(plan_a)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" \
    && fail "BREAK=1: two objects were tampered but the plan still proposes exactly one change - the single-object assertion is not load-bearing"
  log "  BREAK=1: caught - with a second object tampered the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( apply_a >/dev/null ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two objects tampered the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "google_storage_bucket.logs" <<< "$DRIFT_PLAN" || fail "the plan does not name google_storage_bucket.logs"
  RECONV="$(apply_a)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  TIER="$(gcp_labels "$EPA" "storage/v1/b/$BUCKET" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tier",""))')"
  [ "$TIER" = "logs" ] || fail "the bucket's tier label reads $TIER after reconverging, want logs"
  gauntlet_stage drift_reconverge pass "one bucket label tampered with a REST PATCH; choudoufu proposed exactly google_storage_bucket.logs (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle emulator for the same tamper; apply changed 1 and tier=logs reads back. BREAK=1 tampers the topic as well and the single-object assertion correctly fails"
fi

); stage_done $? drift_reconverge reconverge

(
# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band label change, a refusal; then the same file applies once the world is back ==="
write_config "$ADOPTED" live "$EPA" events 2 "$REVIEWED"
write_config "$ORACLE" stock "$EPB" events 2 "$REVIEWED"
P_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
# The world moves on a value the configuration declares: the bucket's tier
# label. A label the configuration does not declare would not do it.
# hashicorp/google's labels are non-authoritative at 8.6.0: the planned
# effective_labels is the prior's plus terraform_labels
# (tpgresource.setLabelsFields, google/tpgresource/labels.go:136-151), so an
# undeclared key rides along on both sides and the fresh plan proposes
# nothing for it, stock's and choudoufu's alike. The stray label this stage
# used to add (an AWS port, where tags are authoritative) left the fresh plan
# identical to the saved one, and the apply would have gone through.
# A bucket PATCH merges labels, so the markers stay.
ra PATCH "storage/v1/b/$BUCKET" '{"labels":{"tier":"moved"}}' >/dev/null \
  || fail "could not move the world (the bucket's tier label) on A"
P_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
put_world_back() {
  ra PATCH "storage/v1/b/$BUCKET" '{"labels":{"tier":"logs"}}' >/dev/null
}
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  put_world_back || fail "BREAK_APPROVAL: could not put the world back"
  ( cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  put_world_back || fail "could not put the world back"
  P_APPLY2="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  GOT="$(gcp_labels "$EPA" "$(topic_path refgcp-events)" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("reviewed",""))')"
  [ "$GOT" = "yes" ] || fail "the events topic does not read reviewed=yes after the saved plan applied"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the events topic gains reviewed=yes); the world then moved out of band (the bucket's declared tier label set to moved, a REST PATCH, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3; with tier=logs put back the identical file applied, 0 added, 1 changed, 0 destroyed, and reviewed=yes reads back; stock's own planfile applied on the oracle emulator in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

); stage_done $? plan_approval reconverge

(
# ── 7. day2_rename: a moved block ────────────────────────────────────────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: google_pubsub_topic.events becomes .stream through a moved block ==="
if [ "${BREAK:-}" = "1" ]; then
  # A bare block rename does not destroy the topic either: the block name
  # is not part of its identity, so it plans the same in-place
  # address-label rewrite as the moved block. The control that can fire is
  # a change of the topic's own name, which is a replace.
  write_config "$ADOPTED" live "$EPA" events 2 "$REVIEWED"
  sed -i.bak 's/name    = "refgcp-events"/name    = "refgcp-events-renamed"/' "$ADOPTED/main.tf" && rm -f "$ADOPTED/main.tf.bak"
  R_PLAN="$(plan_a)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the topic failed"; }
  grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK=1: renaming the topic's own name did not plan a destroy and a create - the marker-rewritten-in-place assertion is not load-bearing: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"; }
  log "  BREAK=1: caught - renaming the topic plans $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  { write_config "$ADOPTED" live "$EPA" stream 2 "$REVIEWED"; moved_block >> "$ADOPTED/main.tf"; }
  ( apply_a >/dev/null ) || fail "BREAK: the moved-block apply failed"
  { write_config "$ORACLE" stock "$EPB" stream 2 "$REVIEWED"; moved_block >> "$ORACLE/main.tf"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the topic's own name plans a replace ($(grep -E '^Plan:' <<< "$R_PLAN" | head -1)), so the marker-rewritten-in-place assertion correctly fails to hold; the moved block then applied"
else
  { write_config "$ADOPTED" live "$EPA" stream 2 "$REVIEWED"; moved_block >> "$ADOPTED/main.tf"; }
  { write_config "$ORACLE" stock "$EPB" stream 2 "$REVIEWED"; moved_block >> "$ORACLE/main.tf"; }
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(plan_a)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)' <<< "$R_PLAN" && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a create or a destroy - not the marker rewritten in place"; }
  grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not exactly one in-place change (the address label rewrite)"; }
  grep -qE '~ +"tofu-address" += +".*" -> ".*"' <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN"; fail "the moved-block plan does not show the tofu-address label being rewritten"; }
  R_APPLY="$(apply_a)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$R_APPLY" || fail "the moved-block apply was not exactly one in-place change"
  WANT="$(gcp_address_label google_pubsub_topic.stream google_pubsub_topic.stream)"
  GOT="$(gcp_labels "$EPA" "$(topic_path refgcp-events)" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tofu-address",""))')"
  [ "$GOT" = "$WANT" ] || fail "the topic's tofu-address label reads $GOT after the rename, want $WANT (google_pubsub_topic.stream)"
  [ "$(count_a)" = "6" ] || fail "$(count_a) labelled objects after the rename, want 6"
  gauntlet_stage day2_rename pass "moved block: google_pubsub_topic.events -> .stream, no add and no destroy, one in-place change confined to the tofu-address label rewrite (0 add, 1 change, 0 destroy), the label reading the encoding of google_pubsub_topic.stream afterwards, read with the REST API; stock's plan for the same moved block on the oracle emulator is zero churn, since stock never writes the label. The moved-block half only: live-mv is not exercised by this stage. BREAK=1 renames the topic itself, which is a genuine identity change and plans a destroy and a create"
fi

); stage_done $? day2_rename reconverge

(
# ── 8. day2_remove: delete the firewall's block ──────────────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: the firewall block leaves the configuration; its network stays ==="
FW="compute/v1/projects/$PROJECT/global/firewalls/refgcp-ssh"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(plan_a)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the firewall block kept, no destroy is proposed; the real check is skipped"
  remove_fw "$ADOPTED/main.tf" && remove_fw "$ORACLE/main.tf" || fail "BREAK_REMOVE: could not remove the block afterwards"
  ( apply_a >/dev/null ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  remove_fw "$ADOPTED/main.tf" && remove_fw "$ORACLE/main.tf" || fail "could not remove the firewall block"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan failed on B"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's remove plan on B is not exactly one destroy"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remove apply failed on B"
  D_PLAN="$(plan_a)" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan failed"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$D_PLAN" || { printf '%s\n' "$D_PLAN" | tail -20; fail "the remove plan is not exactly one destroy"; }
  D_LINE="$(grep -E '^[[:space:]]*# .* will be destroyed' <<< "$D_PLAN" | head -1)"
  D_ADDR="$(sed -E 's/^[[:space:]#]*//; s/ will be destroyed.*$//' <<< "$D_LINE")"
  [ "$D_ADDR" = "$(gauntlet_destroyed_addr "$O_PLAN")" ] || fail "the one destroy is ${D_ADDR:-unnamed}, not stock's $(gauntlet_destroyed_addr "$O_PLAN")"
  D_APPLY="$(apply_a)" || { printf '%s\n' "$D_APPLY" | tail -20; fail "the remove apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed" <<< "$D_APPLY" || fail "the remove apply did not destroy exactly one object"
  exists_a "$FW" && fail "the firewall still exists after the remove apply"
  exists_a "compute/v1/projects/$PROJECT/global/networks/refgcp-net" || fail "the network went with its firewall"
  D_REPLAN="$(plan_a)" || fail "the replan after the remove failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -20; fail "the replan after the remove is not empty"; }
  gauntlet_stage day2_remove pass "deleting google_compute_firewall.ssh's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at $D_ADDR, stock's own address for it - a firewall carries no label, so the estate's record for it is what proposed the removal - applied cleanly, the firewall gone (REST: 404), the network it sat on still there, and the next plan empty; stock's plan for the same removal on the oracle emulator is also exactly one destroy. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

); stage_done $? day2_remove reconverge

(
# ── 9. day2_count: shards 2 -> 1 -> 2 ────────────────────────────────────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: google_pubsub_topic.shard scales 2 -> 1 -> 2 ==="
scale_to 1
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(plan_a)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
grep -q 'google_pubsub_topic.shard\[1\] will be destroyed' <<< "$C_PLAN" || fail "the scale-down does not destroy google_pubsub_topic.shard[1], stock's choice"
{ APPLY_OUT="$(apply_a)" && grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-down apply did not destroy exactly one object"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  exists_a "$(topic_path refgcp-shard-0)" || fail "BREAK_COUNT=1: shard-0 was destroyed - the 'wrong instance' assertion would hold"
  log "  BREAK_COUNT=1: caught - shard-0 still exists"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting the lower index (refgcp-shard-0) was destroyed correctly fails to hold; the real check is skipped"
else
  exists_a "$(topic_path refgcp-shard-0)" || fail "shard-0 was destroyed on the scale-down"
  exists_a "$(topic_path refgcp-shard-1)" && fail "shard-1 still exists after the scale-down"
  scale_to 2
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(plan_a)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -q 'google_pubsub_topic.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  { APPLY_OUT="$(apply_a)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-up apply did not create exactly one object"; }
  U_REPLAN="$(plan_a)" || fail "the replan after the scale-up failed"
  grep -q "No changes." <<< "$U_REPLAN" || { printf '%s\n' "$U_REPLAN" | tail -20; fail "the replan after the scale-up is not empty"; }
  [ "$(count_a)" = "6" ] || fail "$(count_a) labelled objects after the count cycle, want 6"
  gauntlet_stage day2_count pass "scaling google_pubsub_topic.shard from 2 to 1 destroyed exactly google_pubsub_topic.shard[1] (refgcp-shard-0 untouched, both read with the REST API) - the instance key is in the topic's tofu-address label, so the leaving instance is found at its own address; back to 2 created exactly google_pubsub_topic.shard[1]; the next plan is empty; stock's plans for the same two changes on the oracle emulator have the identical shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

); stage_done $? day2_count reconverge

(
# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
#
# A topic's name is unique in its project, so the replacement
# create_before_destroy is used for is one whose name changes, as on kind
# (#1541). The old and the new topic carry the same tofu-address label for
# the window between the create and the destroy, and the record's deposed
# entry is what says which one goes.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a topic renamed under create_before_destroy ==="
replace_block() { # $1 = the topic's name suffix
  cat <<EOF

resource "google_pubsub_topic" "cbd" {
  project = "$PROJECT"
  name    = "refgcp-cbd-$1"
  lifecycle {
    create_before_destroy = true
  }
}
EOF
}
add_replace() { replace_block "$1" > "$ADOPTED/day2_replace.tf"; replace_block "$1" > "$ORACLE/day2_replace.tf"; }
add_replace a
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not create refgcp-cbd-a on B"
{ X_OUT="$(apply_a)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$X_OUT"; } || { printf '%s\n' "$X_OUT" | tail -20; fail "choudoufu could not create refgcp-cbd-a"; }
add_replace b
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's replace plan failed on B"
grep -q 'must be replaced' <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock does not plan a replace for the rename on B"; }
( stock_b apply -auto-approve -input=false -no-color -parallelism=1 >/dev/null 2>&1 ) || fail "stock's replace apply failed on B"
X_PLAN="$(plan_a)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the replace plan failed"; }
grep -q 'google_pubsub_topic.cbd must be replaced' <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the rename does not plan stock's replace of google_pubsub_topic.cbd"; }
X_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$X_APPLY" | tail -20; fail "the replace apply failed"; }
CREATED_AT="$(grep -n 'google_pubsub_topic.cbd: Creation complete' <<< "$X_APPLY" | head -1 | cut -d: -f1)"
DESTROY_AT="$(grep -n 'google_pubsub_topic.cbd (deposed object [^)]*): Destroying' <<< "$X_APPLY" | head -1 | cut -d: -f1)"
[ -n "$CREATED_AT" ] && [ -n "$DESTROY_AT" ] && [ "$CREATED_AT" -lt "$DESTROY_AT" ] \
  || { printf '%s\n' "$X_APPLY" | grep -E 'cbd'; fail "the apply log does not show the new topic's create complete before the old one's destroy starts"; }
exists_a "$(topic_path refgcp-cbd-a)" && [ "${BREAK_REPLACE:-}" != "1" ] && fail "refgcp-cbd-a still exists after the replace"
exists_a "$(topic_path refgcp-cbd-b)" || fail "refgcp-cbd-b does not exist after the replace"
if [ "${BREAK_REPLACE:-}" = "1" ]; then
  ra PUT "$(topic_path refgcp-cbd-a)" '{"labels":{"tofu-estate":"'"$ESTATE"'","tofu-address":"'"$(gcp_address_label google_pubsub_topic.cbd google_pubsub_topic.cbd)"'"}}' >/dev/null \
    || fail "BREAK_REPLACE: could not recreate refgcp-cbd-a"
  B_PLAN="$(plan_a 2>&1)"
  grep -q "No changes." <<< "$B_PLAN" && fail "BREAK_REPLACE=1: with the old topic back, carrying the block's labels, the next plan is empty - the empty-replan assertion is not load-bearing"
  log "  BREAK_REPLACE=1: caught - $(grep -E '^Plan:|^Error|^Warning' <<< "$B_PLAN" | head -1)"
  ra DELETE "$(topic_path refgcp-cbd-a)" >/dev/null 2>&1 || true
  gauntlet_stage day2_replace pass "BREAK_REPLACE=1 control: the old topic recreated with the block's labels after the replace makes the next plan non-empty ($(grep -E '^Plan:|^Error|^Warning' <<< "$B_PLAN" | head -1)), so the empty-replan assertion correctly fails to hold"
else
  X_REPLAN="$(plan_a)" || fail "the replan after the replace failed"
  grep -q "No changes." <<< "$X_REPLAN" || { printf '%s\n' "$X_REPLAN" | tail -20; fail "the replan after the replace is not empty"; }
  gauntlet_stage day2_replace pass "a create_before_destroy rename of google_pubsub_topic.cbd (refgcp-cbd-a -> refgcp-cbd-b) planned stock's replace (must be replaced, create first); at -parallelism=1 the apply log shows the new topic's create complete (line $CREATED_AT) before the deposed object's destroy starts (line $DESTROY_AT); only refgcp-cbd-b remains, read with the REST API, and the next plan is empty. Stock's replace of the same block on the oracle emulator leaves the same single topic. BREAK_REPLACE=1 recreates the old topic with the block's labels and the next plan correctly stops being empty"
fi
rm -f "$ADOPTED/day2_replace.tf" "$ORACLE/day2_replace.tf"
( apply_a >/dev/null ) || fail "could not remove the replace block from A"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not remove the replace block from B"

); stage_done $? day2_replace reconverge

(
# ── 10. day2_crash: an apply of two objects, killed after the first ─────
#
# The engine's own interrupt (internal/command/apply_e2etesting_crash.go)
# self-delivers SIGTERM inside the -parallelism=1 walker the instant
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT's address finishes a real create.
# crash_second's name reads crash_first's, so the second create cannot have
# been dispatched. The oracle is stock on B walked into the same position.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one topic and the create of the next ==="
crash_pair_first >> "$ORACLE/main.tf" || fail "could not append crash_first to the oracle root"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash_first apply failed on B"
crash_pair_second >> "$ORACLE/main.tf" || fail "could not append crash_second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's remainder plan failed on B"
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
{ crash_pair_first; crash_pair_second; } >> "$ADOPTED/main.tf" || fail "could not append the crash pair to the adopted root"
X_PLAN="$(plan_a)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds"; }
X_OUT="$(cd "$ADOPTED" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="google_pubsub_topic.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - nothing was interrupted"; }
exists_a "$(topic_path refgcp-crash-first)" || fail "refgcp-crash-first does not exist after the interrupted apply - the kill landed before the create committed"
exists_a "$(topic_path refgcp-crash-first-second)" && fail "refgcp-crash-first-second exists after the interrupted apply - there is no remainder to propose"
CF_LABELS="$(gcp_labels "$EPA" "$(topic_path refgcp-crash-first)")"
grep -q "\"tofu-estate\": \"$ESTATE\"" <<< "$CF_LABELS" || fail "refgcp-crash-first was created by the interrupted apply but carries no tofu-estate=$ESTATE label: $CF_LABELS"
if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  ra PATCH "$(topic_path refgcp-crash-first)" '{"topic":{"labels":{}},"updateMask":"labels"}' >/dev/null || fail "BREAK_CRASH_UNBOUND: could not strip the labels off refgcp-crash-first"
fi
R_PLAN="$(plan_a)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# google_pubsub_topic\.crash_second will be created' <<< "$R_PLAN" || return 1
  local proposed
  proposed="$(grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN")"
  grep -q 'crash_first' <<< "$proposed" && return 1
  return 0
}
if [ "${BREAK_CRASH:-}" = "1" ]; then
  grep -qF "No changes." <<< "$R_PLAN" && fail "BREAK_CRASH=1: the plan after a real interrupted two-object apply came back empty"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's Break line - assert nothing is proposed - correctly fails to hold"
  ( apply_a >/dev/null ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  recovered && fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with refgcp-crash-first carrying no label"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the labels stripped off the topic the interrupted apply created, the recovery check correctly fails to hold ($R_LINE)"
  ra DELETE "$(topic_path refgcp-crash-first)" >/dev/null 2>&1 || true
  ( apply_a >/dev/null ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
elif ! recovered; then
  gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of google_pubsub_topic.crash_first and the create of google_pubsub_topic.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). refgcp-crash-first exists carrying tofu-estate=$ESTATE and the second topic does not, both read with the REST API; stock, walked into the same position on the oracle emulator, plans exactly one add"
  # Best effort, so day2_teardown compares the same set of objects on A and B.
  ( apply_a >/dev/null 2>&1 ) || log "  the apply after day2_crash's failed recovery check failed too; day2_teardown's count may differ by the crash pair"
else
  R_APPLY="$(apply_a)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || fail "the recovery apply did not add exactly the one remaining topic"
  R_REPLAN="$(plan_a)" || fail "the replan after the recovery failed"
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
  gauntlet_stage day2_crash pass "an apply creating two topics was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant google_pubsub_topic.crash_first's create committed; crash_second's name reads crash_first's, so the walker cannot have reached it, and the REST API confirms the first exists carrying tofu-estate=$ESTATE and the second does not. The next plan proposed exactly the remainder ($R_LINE, google_pubsub_topic.crash_second created) and nothing for crash_first, which it bound by its labels, matching stock's own plan from the same position on the oracle emulator; the recovery apply added exactly one topic and the plan after it is empty. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips crash_first's labels and the same check correctly fails"
fi
gauntlet_end_stage

); stage_done $? day2_crash reconverge

(
# ── 11. day2_teardown: destroy the adopted estate ────────────────────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy on the adopted estate, and stock's own destroy on B ==="
T_EXPECT="$( (cd "$ADOPTED" && "$TOFU" state list 2>/dev/null) | wc -l | tr -d ' ')"
T_OUT="$(cd "$ADOPTED" && "$TOFU" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
T_N="$(grep -oE 'Resources: 0 added, 0 changed, [0-9]+ destroyed' <<< "$T_OUT" | grep -oE '[0-9]+ destroyed' | grep -oE '[0-9]+')"
[ "$(count_a)" = "0" ] || fail "$(count_a) object(s) still carry tofu-estate=$ESTATE after the destroy"
for path in "compute/v1/projects/$PROJECT/global/networks/refgcp-net" "compute/v1/projects/$PROJECT/regions/$REGION/subnetworks/refgcp-subnet" "v1/projects/$PROJECT/serviceAccounts/refgcp-app@$PROJECT.iam.gserviceaccount.com"; do
  exists_a "$path" && fail "$path still exists after the destroy - an unlabelled resource the estate declared was left behind"
done
O_EXPECT="$(stock_b state list 2>/dev/null | wc -l | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
[ "${T_N:-}" = "$O_EXPECT" ] || fail "apply -destroy removed ${T_N:-an unreported number of} object(s); stock's destroy of the same estate removed $O_EXPECT (cache listed ${T_EXPECT:-?})"
gauntlet_stage day2_teardown pass "apply -destroy removed $T_N objects in one apply, the same number stock's destroy of the same estate removed on the oracle emulator; no object of the four labelled types carries tofu-estate=$ESTATE and the unlabelled network, subnetwork and service account are gone (REST: 404)"

); stage_done $? day2_teardown

(
# ── 12. greenfield: the same shape, fresh, with a live block ─────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the shape fresh on the now-empty emulator A ==="
mkdir -p "$GREEN"
write_config "$GREEN" live "$EPA"
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed"; }
# Every check below runs and the verdict names each that failed, so one run
# says whether the count, the replans and the inventory hold, not only the
# first of them.
G_PROBLEMS=""
g_problem() { G_PROBLEMS="${G_PROBLEMS:+$G_PROBLEMS; }$*"; }
if ! grep -qF "Apply complete! Resources: 10 added, 0 changed, 0 destroyed" <<< "$G_OUT"; then
  ( cd "$STOCK" && terraform state list 2>/dev/null ) > "$WORK/greenfield.expected"
  g_problem "the apply did not add exactly 10 instances ($(grep -oE 'Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$G_OUT" | head -1)); stock's cold-deploy addresses with no Creation complete line: $(gcp_not_created "$WORK/greenfield.expected" <<< "$G_OUT")"
fi
[ ! -f "$GREEN/terraform.tfstate" ] || g_problem "a terraform.tfstate appeared after a live-block apply"
G_N="$(count_a)"
[ "$G_N" = "6" ] || g_problem "$G_N object(s) carry tofu-estate=$ESTATE after the greenfield apply, want 6"
G_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; G_RC=$?
{ [ "$G_RC" -eq 0 ] && grep -q "No changes." <<< "$G_PLAN"; } || { printf '%s\n' "$G_PLAN" | tail -20; g_problem "the greenfield replan is not empty (exit $G_RC, $(gauntlet_plan_line <<< "$G_PLAN"))"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; G_RC2=$?
{ [ "$G_RC2" -eq 0 ] && grep -q "No changes." <<< "$G_PLAN2"; } || { printf '%s\n' "$G_PLAN2" | tail -20; g_problem "the greenfield replan without the cache is not empty (exit $G_RC2, $(gauntlet_plan_line <<< "$G_PLAN2"))"; }
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="instance/refgcp-vm"
gcp_inventory "$EPA" "$PROJECT" "$REGION" "$ZONE" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory${G_PROBLEMS:+; also: $G_PROBLEMS}"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null \
    && fail "BREAK=1: with the instance dropped from the greenfield inventory the two inventories still match"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the instance from the greenfield inventory makes the object-by-object comparison correctly fail${G_PROBLEMS:+ (the other checks: $G_PROBLEMS)}"
else
  diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json" || g_problem "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  if [ -n "$G_PROBLEMS" ]; then
    gauntlet_stage greenfield fail "$G_PROBLEMS"
  else
    gauntlet_stage greenfield pass "10 instances applied fresh with a live block and no terraform.tfstate, the 6 labelled ones carrying tofu-estate=$ESTATE (REST API); replanned empty with and without the cache; the emulator's inventory (the network, the subnetwork's range, the firewall's rule, the service account, the bucket's location and access, the topics, the disk's size and type, the instance's machine type, subnetwork and boot disk) matches stock's cold deploy on the same emulator object by object, labels never compared. BREAK=1 drops the instance from the expected inventory and the match correctly fails"
  fi
fi
); stage_done $? greenfield

(
# ── no_local_state: the record store and the cache both gone ────────────
gauntlet_begin_stage no_local_state
log "=== no_local_state: the cache-serving plan, then no record store and no cache at all ==="
NLS="$WORK/no-local-state"; mkdir -p "$NLS"
( cd "$GREEN" && TF_LOG=debug TF_LOG_PATH="$NLS/cached.log" "$TOFU" plan -input=false -no-color > "$NLS/cached.plan" 2>&1 ); C_RC=$?
cp -R "$GREEN/.tofu-records" "$NLS/records.keep" 2>/dev/null || true
cp "$GREEN/.terraform/choudoufu-cache.tfstate" "$NLS/cache.keep" 2>/dev/null || true
rm -rf "$GREEN/.tofu-records" "$GREEN/.terraform/choudoufu-cache.tfstate"
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  ra PATCH "storage/v1/b/$BUCKET" '{"labels":{"tofu-estate":null}}' >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not strip the bucket's label"
fi
( cd "$GREEN" && TF_LOG=debug TF_LOG_PATH="$NLS/cold.log" "$TOFU" plan -input=false -no-color > "$NLS/cold.plan" 2>&1 ); N_RC=$?
REQ_C="$(gcp_request_count "$NLS/cached.log")"; REQ_N="$(gcp_request_count "$NLS/cold.log")"
N_LINE="$(grep -E '^Plan:|^No changes|^Error' "$NLS/cold.plan" | head -1)"
N_BAD="$(gcp_churn_count < "$NLS/cold.plan")"
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  if [ "$N_RC" -eq 0 ] && grep -q "No changes." "$NLS/cold.plan"; then
    fail "BREAK_NO_LOCAL_STATE=1: with the bucket's label stripped the plan with no local state is still empty"
  fi
  gauntlet_stage no_local_state pass "BREAK_NO_LOCAL_STATE=1 control: with the bucket's tofu-estate label stripped, the plan with no record store and no cache is no longer empty (${N_LINE:-exit $N_RC}), so the check correctly fails"
elif [ "$C_RC" -ne 0 ] || ! grep -q "No changes." "$NLS/cached.plan"; then
  gauntlet_stage no_local_state fail "the cache-serving plan before the deletion was not empty (exit $C_RC), so there is nothing to compare the plan with no local state against"
elif [ "$N_RC" -ne 0 ] || [ "$N_BAD" != "0" ]; then
  gauntlet_stage no_local_state fail "with the record store and the cache deleted the plan exited $N_RC and proposes $N_BAD create/destroy/replace(s) (${N_LINE:-no plan line}); the labelled objects are found by their labels, but the four with no labels argument have only their records and their configuration to be found by. Provider requests: $REQ_N with no local state, $REQ_C cache-serving"
else
  gauntlet_stage no_local_state pass "after the greenfield apply's empty plan, deleting both the record store and the state cache left a plan that created, destroyed and replaced nothing (${N_LINE:-No changes}): the six labelled objects found by their labels, the four declaration-carried ones by the identity their configuration computes. hashicorp/google sent $REQ_N request(s) for that plan against $REQ_C for the cache-serving plan before it (counted from TF_LOG=debug's request blocks). BREAK_NO_LOCAL_STATE=1 strips the bucket's label and the plan is no longer empty"
fi
rm -rf "$GREEN/.tofu-records"; cp -R "$NLS/records.keep" "$GREEN/.tofu-records" 2>/dev/null || true
cp "$NLS/cache.keep" "$GREEN/.terraform/choudoufu-cache.tfstate" 2>/dev/null || true
( cd "$GREEN" && "$TOFU" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  greenfield teardown failed; the emulator goes with the run"

); stage_done $? no_local_state

(
# ── 13. strict: every toggle on, one refusal ─────────────────────────────
gauntlet_begin_stage strict
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test ("refuse" or "store")
  cat <<EOF
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "reference-gcp-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["google_pubsub_topic"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
EOF
}
log "=== 13. strict: every strict toggle on ==="
strict_block "refuse" > "$STRICT/main.tf"
( cd "$STRICT" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block "store" > "$STRICT/main.tf"
  STRICT_OFF="$(cd "$STRICT" && "$TOFU" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming google_pubsub_topic) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

); stage_done $? strict

gauntlet_end
log "reference-gcp: done"
gcp_exit_status
