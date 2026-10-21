#!/usr/bin/env bash
# corpus-k8s-infra-gcp-gcve: the gcp lane's second estate and its first
# published root (#1119 workstream H), crossed on the floci-gcp substrate.
# kubernetes/k8s.io's infra/gcp/terraform/k8s-infra-gcp-gcve at commit
# 4046258e7d54fdc156e2f39ad8da51b57be88f55 (live/corpus-manifest.json's
# k8s-io fetch, Apache-2.0): the Kubernetes project's own root for the
# VMware Engine project its vSphere CI runs in.
#
# Why this root, under the estate rule (live/GAUNTLET.md, "adding an
# estate"). It is the GKE-free root in infra/gcp/terraform that carries the
# network skeleton: a custom-mode VPC, a subnetwork and two firewalls, one
# of them reading the subnetwork's range. Around it sit the two shapes
# reference-gcp does not have. google_project_service is a for_each over a
# literal set whose project comes from a data source, five declaration-
# carried instances keyed by string. And two terraform-google-modules
# registry modules: simple_bucket (the estate's one labelled object, inside
# a module, with a google_storage_bucket_iam_member) and projects_iam in
# authoritative mode, whose google_project_iam_binding for_each reads the
# keys its helper module computes. projects_iam is the module most of the
# k8s.io GCP roots call (nine of the eighteen), so how this estate fares is
# how most of that corpus fares.
#
# THE WALL THIS ESTATE MET, now lifted. `choudoufu live-check` on the root
# as this script writes it, offline with the modules installed, refused it:
# "Module output not supported in static context" at projects_iam/main.tf:33
# and :52, the two for_each blocks that read module.helper.set_authoritative
# and set_additive. The helper's outputs are pure functions of the module's
# inputs, so stock plans them; the key-set chase did not follow a module
# output (#2142). #2143 fixed that, and on gcp-lane after merging it
# live-check resolves all 16 instances with no refusal (2026-10-20). The
# stages after migrate were written against the wall-free shape and now run.
#
# The deltas, each the kind every corpus crossing applies:
#
#   1. provider.tf is replaced. Its `backend "gcs"` becomes the live block
#      (or nothing, for stock), its `required_version = "1.10.5"` goes
#      (neither the stock terraform pin nor choudoufu is 1.10.5), and
#      hashicorp/google `~> 6.34.1` becomes the lane's pin, 8.6.0
#      (live/oracle-versions.json), with a provider block pointing every
#      endpoint the root reaches at the run's emulator.
#   2. buckets.tf's simple_bucket `~> 11.0` becomes `~> 12.4`: every 11.x
#      and 12.0-12.3 release requires hashicorp/google `< 8`, and 12.4.0 is
#      the first to admit 8.x (`>= 6.37.0, < 9`). Nothing else in the
#      module call changes.
#   3. vmware-engine.tf's five VMware Engine resources (the private cloud,
#      its network, the network policy and two peerings) are left out:
#      floci-gcp does not emulate VMware Engine, and one of the peerings
#      names a network in another project. The file's four Compute
#      resources are kept byte for byte; the script refuses to run if the
#      text it extracts differs from the copy below. The enabling of
#      vmwareengine.googleapis.com in main.tf is kept: Service Usage is
#      served, and it is the root's own list.
#
# Sixteen instances, as the modules resolve today (simple_bucket 12.4.1,
# projects_iam 8.3.0, inferred from their source, not yet measured): the five
# services, the network, the subnetwork, the two firewalls, the bucket and
# its IAM member, and five project IAM bindings. Only the bucket has a
# labels argument; the other fifteen are declaration-carried.
#
# What each day-2 stage changes, all on the estate's own objects except
# where a stage needs a second object of a labelled type (scratch buckets):
#
#   drift_reconverge  a REST PATCH on maintenance-firewall-internet's
#                     sourceRanges; BREAK=1 also patches the internal one.
#   plan_approval     the saved plan adds a description to the internet
#                     firewall; the world moves by a PATCH on the internal
#                     firewall's sourceRanges.
#   day2_rename       a moved block renames the internet firewall's block;
#                     it carries no label, so stock's zero churn is the
#                     expectation. BREAK=1 renames the firewall itself.
#   day2_remove       maintenance-firewall-internal's block is deleted.
#   day2_count        essentialcontacts.googleapis.com leaves the for_each
#                     set and comes back.
#   day2_replace      a scratch google_storage_bucket renamed under
#                     create_before_destroy.
#   day2_crash        two scratch buckets, the second named from the first.
#
# A stage this script cannot pass records fail with the reason and the run
# continues where it can; the runner, not the script, decides what "clear"
# means. Every stage after test_plan runs in a subshell (gcp_stage_done in
# live/e2e/lib/gcp.sh), so its fail() ends that stage only; a failed day-2
# stage is followed by a reconverging apply on both emulators, and the run
# exits 1 at the end if any stage failed. cold_deploy, migrate and test_plan
# still end the run, as does a fault in the substrate itself (an emulator
# that never came up).
#
#   go run ./tools/gauntlet run corpus-k8s-infra-gcp-gcve   # asks for "run" at the terminal
#   bash live/e2e/corpus-k8s-infra-gcp-gcve/run.sh
#
# Needs `just corpus-fetch` first (a worktree links the primary checkout's
# .corpus), docker, terraform (the stock binary), curl, python3, and
# registry access for the two modules' init.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT     host port for emulator A (default 4790; the oracle
#                  emulator B is FLOCI_PORT+1000). The runner assigns it.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls instead of the real checks:
#                  a second firewall is tampered and the single-object
#                  assertion must fail; the firewall's own name is changed
#                  and the zero-churn assertion must fail; the internet
#                  firewall is dropped from greenfield's expected inventory
#                  and the object-by-object match must fail.
#   BREAK_REMOVE   set to 1 to keep the internal firewall's block and assert
#                  no destroy is proposed (day2_remove's own Break line).
#   BREAK_COUNT    set to 1 to assert the dropped service ended in the state
#                  stock's destroy did NOT leave it in on the oracle
#                  (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved and
#                  expect success (plan_approval's Break line); must fail.
#   BREAK_REPLACE  set to 1 to recreate the replaced bucket's old object,
#                  carrying the estate's labels and the block's address
#                  label, after day2_replace's apply; the next plan must not
#                  be empty (day2_replace's Break line).
#   BREAK_CRASH    set to 1 to assert, after the same real interrupt, that
#                  nothing is proposed (day2_crash's own Break line); must
#                  fail, because a recovered run proposes the remainder.
#   BREAK_CRASH_UNBOUND
#                  set to 1 to strip tofu-estate off the bucket the
#                  interrupted apply did create before replanning; the real
#                  check must then fail to hold.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
#   BREAK_NO_LOCAL_STATE
#                  strip the bucket's tofu-estate label before
#                  no_local_state's plan with no local state; that plan must
#                  then propose something, so the check fails.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
# shellcheck source=live/e2e/lib/gcp.sh
source "$ROOT/live/e2e/lib/gcp.sh"
# shellcheck source=live/e2e/corpus-k8s-infra-gcp-gcve/gcp.sh
source "$ROOT/live/e2e/corpus-k8s-infra-gcp-gcve/gcp.sh"

gauntlet_plugin_cache
ESTATE="corpus-k8s-infra-gcp-gcve"
PIN_COMMIT="4046258e7d54fdc156e2f39ad8da51b57be88f55"
SRC="$ROOT/.corpus/k8s-io/infra/gcp/terraform/k8s-infra-gcp-gcve"
# variables.tf's own default; the emulator registers it on first use.
PROJECT="broadcom-451918"
REGION="us-central1"
ZONE="us-central1-a"
BUCKET="k8s-datadog-billing-analysis-broadcom"
EXPECT_N=16
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
FLOCI_PORT="${FLOCI_PORT:-4790}"
ORACLE_PORT=$((FLOCI_PORT + 1000))
EMU_A="choudoufu-gcve-$$"; EMU_B="choudoufu-gcve-oracle-$$"
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

# ── 0. tools and the corpus pin ──────────────────────────────────────────
log "=== 0. tools ==="
for t in docker terraform curl python3 git; do
  command -v "$t" >/dev/null 2>&1 || fail "$t is not on PATH"
done
docker info >/dev/null 2>&1 || fail "docker is not running"
for f in main.tf variables.tf buckets.tf iam.tf vmware-engine.tf provider.tf; do
  [ -f "$SRC/$f" ] || fail "$SRC/$f is missing - run \`just corpus-fetch\` first (a worktree links the primary checkout's .corpus)"
done
PIN="$(git -C "$ROOT/.corpus/k8s-io" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "$PIN_COMMIT" ] || fail "the fetched k8s.io is at ${PIN:-nothing}, not the pinned $PIN_COMMIT - run \`just corpus-fetch\`"

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
# apply_e2etesting_crash.go), the same build reference-gcp makes.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the root, cut out of .corpus with its deltas ─────────────────────────
GOOGLE_REQUIRED_PROVIDER="$(gauntlet_required_provider google hashicorp/google google_provider_version "    ")" \
  || fail "could not read the hashicorp/google pin from live/oracle-versions.json"

# The four Compute resources vmware-engine.tf carries, as the pin has them.
# shape_root extracts them from the corpus file and refuses to go on unless
# the extracted text is exactly this.
network_expected() {
  cat <<'EOF'
resource "google_compute_network" "maintenance-vpc" {
  name                    = "maintenance-vpc-network"
  project                 = var.project_id
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "maintenance-subnet" {
  name          = "maintenance-subnet"
  project       = var.project_id
  ip_cidr_range = "192.168.28.0/24"
  region        = "us-central1"
  network       = google_compute_network.maintenance-vpc.id
}

resource "google_compute_firewall" "maintenance-firewall-internet" {
  name    = "maintenance-firewall-internet"
  project = var.project_id
  network = google_compute_network.maintenance-vpc.name

  source_ranges = ["0.0.0.0/0"]

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  allow {
    protocol = "udp"
    ports    = ["51820"]
  }
}

resource "google_compute_firewall" "maintenance-firewall-internal" {
  name    = "maintenance-firewall-internal"
  project = var.project_id
  network = google_compute_network.maintenance-vpc.name

  source_ranges = [google_compute_subnetwork.maintenance-subnet.ip_cidr_range]

  allow {
    protocol = "icmp"
  }
  allow {
    protocol = "tcp"
    ports    = ["0-65535"]
  }
  allow {
    protocol = "udp"
    ports    = ["0-65535"]
  }
}
EOF
}

# versions_block writes providers.tf: $1 = "live" for the live block, $2 the
# emulator endpoint (delta 1).
versions_block() {
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

# The day-2 state of the shape. write_config renders the root from these, so
# every stage after the one that set one keeps it, on both roots.
FW_BLOCK="maintenance-firewall-internet"  # day2_rename: maintenance_firewall_internet
FW_NAME=""                                # BREAK's rename of the firewall itself
FW_DESC=""                                # plan_approval's description
DROP_INTERNAL=0                           # day2_remove
DROP_SERVICE=""                           # day2_count

# shape_root <dir>: the corpus files with deltas 2 and 3 and the day-2
# state applied. main.tf, variables.tf and iam.tf are copied as they are
# unless a stage edits them.
shape_root() {
  SRC="$SRC" DST="$1" EXPECTED="$(network_expected)" FW_BLOCK="$FW_BLOCK" FW_NAME="$FW_NAME" \
    FW_DESC="$FW_DESC" DROP_INTERNAL="$DROP_INTERNAL" DROP_SERVICE="$DROP_SERVICE" python3 - <<'PY'
import os, re, sys
src, dst, env = os.environ["SRC"], os.environ["DST"], os.environ
def read(name):
    return open(os.path.join(src, name)).read()
def write(name, text):
    open(os.path.join(dst, name), "w").write(text)
def die(msg):
    sys.exit(f"shape_root: {msg} - the corpus pin has moved")

# main.tf: the five services, unless day2_count drops one.
main = read("main.tf")
if main.count('"essentialcontacts.googleapis.com",') != 1 or main.count('resource "google_project_service" "project"') != 1:
    die("main.tf does not carry the five-service google_project_service block")
if env["DROP_SERVICE"]:
    line = f'    "{env["DROP_SERVICE"]}",\n'
    if main.count(line) != 1:
        die(f"main.tf has no line {line.strip()}")
    main = main.replace(line, "")
write("main.tf", main)
write("variables.tf", read("variables.tf"))
write("iam.tf", read("iam.tf"))

# delta 2: simple_bucket's version.
b = read("buckets.tf")
if b.count('version = "~> 11.0"') != 1:
    die('buckets.tf does not pin simple_bucket at "~> 11.0"')
write("buckets.tf", b.replace('version = "~> 11.0"', 'version = "~> 12.4"'))

# delta 3: the four Compute resources out of vmware-engine.tf, nothing else.
v = read("vmware-engine.tf")
if len(re.findall(r'^resource "google_vmwareengine_', v, re.M)) != 5:
    die("vmware-engine.tf does not declare exactly five VMware Engine resources")
blocks = []
for typ, name in [("google_compute_network", "maintenance-vpc"), ("google_compute_subnetwork", "maintenance-subnet"),
                  ("google_compute_firewall", "maintenance-firewall-internet"), ("google_compute_firewall", "maintenance-firewall-internal")]:
    m = re.findall(r'^resource "%s" "%s" \{\n.*?^\}\n' % (typ, re.escape(name)), v, re.M | re.S)
    if len(m) != 1:
        die(f"vmware-engine.tf has {len(m)} {typ}.{name} blocks, want 1")
    blocks.append(m[0])
net = "\n".join(blocks)
if net.strip() != env["EXPECTED"].strip():
    die("the four Compute resources extracted from vmware-engine.tf differ from network_expected()")

# The day-2 edits. The description goes in before a rename of the name.
if env["FW_DESC"]:
    net = net.replace('  name    = "maintenance-firewall-internet"\n',
                      f'  name    = "maintenance-firewall-internet"\n  description = "{env["FW_DESC"]}"\n', 1)
if env["FW_NAME"]:
    net = net.replace('name    = "maintenance-firewall-internet"', f'name    = "{env["FW_NAME"]}"', 1)
if env["DROP_INTERNAL"] == "1":
    net = re.sub(r'\nresource "google_compute_firewall" "maintenance-firewall-internal" \{.*?\n\}\n', "\n", net, flags=re.S)
    assert "maintenance-firewall-internal" not in net
if env["FW_BLOCK"] != "maintenance-firewall-internet":
    net = net.replace('resource "google_compute_firewall" "maintenance-firewall-internet"',
                      f'resource "google_compute_firewall" "{env["FW_BLOCK"]}"', 1)
    net += ('\nmoved {\n  from = google_compute_firewall.maintenance-firewall-internet\n'
            f'  to   = google_compute_firewall.{env["FW_BLOCK"]}\n}}\n')
write("network.tf", net)
PY
}
write_config() { # $1 dir, $2 live|stock, $3 endpoint
  mkdir -p "$1" || return 1
  versions_block "$2" "$3" > "$1/providers.tf" && shape_root "$1" || return 1
  # A stage runs in a subshell, so the shape it set is saved for the stages
  # after it (stage_done reads it back).
  printf 'FW_BLOCK=%q\nFW_NAME=%q\nFW_DESC=%q\nDROP_INTERNAL=%q\nDROP_SERVICE=%q\n' \
    "$FW_BLOCK" "$FW_NAME" "$FW_DESC" "$DROP_INTERNAL" "$DROP_SERVICE" > "$WORK/shape.env"
}

# day2_replace's scratch bucket and day2_crash's pair, in files of their own
# beside the root. crash_second's name reads crash_first's, so the graph
# cannot reach it until crash_first has committed: the interrupt below is
# deterministic by construction, as in reference-gcp.
replace_block() { # $1 = the bucket's name suffix
  cat <<EOF

resource "google_storage_bucket" "cbd" {
  project       = "$PROJECT"
  name          = "$PROJECT-gcve-cbd-$1"
  location      = "US"
  force_destroy = true
  lifecycle {
    create_before_destroy = true
  }
}
EOF
}
crash_pair_first() {
  cat <<EOF

resource "google_storage_bucket" "crash_first" {
  project       = "$PROJECT"
  name          = "$PROJECT-gcve-crash-first"
  location      = "US"
  force_destroy = true
}
EOF
}
crash_pair_second() {
  cat <<EOF

resource "google_storage_bucket" "crash_second" {
  project       = "$PROJECT"
  name          = "\${google_storage_bucket.crash_first.name}-second"
  location      = "US"
  force_destroy = true
}
EOF
}

# ── emulator helpers ─────────────────────────────────────────────────────
ra() { gcp_rest "$EPA" "$@"; }
rb() { gcp_rest "$EPB" "$@"; }
stock_b() { ( cd "$ORACLE" && terraform "$@" ); }
count_a() { gcve_labelled_count "$EPA" "$PROJECT" "$ESTATE"; }
exists_a() { ra GET "$1" >/dev/null 2>&1; }
fw_path() { printf 'compute/v1/projects/%s/global/firewalls/%s' "$PROJECT" "$1"; }
bucket_path() { printf 'storage/v1/b/%s' "$1"; }
svc_a() { gcve_service_state "$EPA" "$PROJECT" "$1"; }
svc_b() { gcve_service_state "$EPB" "$PROJECT" "$1"; }
plan_a() { ( cd "$ADOPTED" && "$TOFU" plan -input=false -no-color 2>&1 ); }
apply_a() { ( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 ); }
both_roots() { # rewrite both roots from the day-2 state
  write_config "$ADOPTED" live "$EPA" || fail "could not rewrite the adopted root"
  write_config "$ORACLE" stock "$EPB" || fail "could not rewrite the oracle root"
}
# reconverge: both emulators applied to the roots on disk, after a day-2
# stage failed partway.
reconverge() {
  local rc=0
  ( apply_a >/dev/null 2>&1 ) || { log "  the reconverging apply on A failed"; rc=1; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || { log "  stock's reconverging apply on B failed"; rc=1; }
  return $rc
}
# stage_done <rc> <id> [reconverge]: the shape the stage's subshell saved,
# then gcp_stage_done.
stage_done() {
  # shellcheck disable=SC1091
  [ -f "$WORK/shape.env" ] && . "$WORK/shape.env"
  gcp_stage_done "$@"
}

# ── 1. cold_deploy: stock stands the estate up on A (and B, the oracle) ──
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two floci-gcp containers, stock terraform applies the root on each ==="
gcp_floci_up "$EMU_A" "$FLOCI_PORT" "$PROJECT" || fail "floci-gcp A ($EMU_A) did not come up on :$FLOCI_PORT"
gcp_floci_up "$EMU_B" "$ORACLE_PORT" "$PROJECT" || fail "floci-gcp B ($EMU_B) did not come up on :$ORACLE_PORT"
write_config "$STOCK" stock "$EPA" || fail "could not cut the root out of $SRC"
write_config "$ORACLE" stock "$EPB" || fail "could not cut the oracle root out of $SRC"
INIT_OUT="$(cd "$STOCK" && gauntlet_locked_init terraform init -input=false -no-color 2>&1)" || { printf '%s\n' "$INIT_OUT" | tail -20; fail "stock init failed on A (the two registry modules download here)"; }
COLD_OUT="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A: $(gauntlet_first_error_line <<< "$COLD_OUT")"; }
grep -qF "Apply complete! Resources: $EXPECT_N added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly $EXPECT_N instances on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(cd "$STOCK" && terraform state list | grep -vc '^data\.\|\.data\.' | tr -d ' ')"
[ "$STOCK_N" = "$EXPECT_N" ] || fail "stock's state holds $STOCK_N managed instances, want $EXPECT_N"
UNMARKED="$(count_a)" || fail "could not count labelled buckets on A"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED bucket(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
gcve_inventory "$EPA" "$PROJECT" "$REGION" "$BUCKET" > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( cd "$ORACLE" && gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: $EXPECT_N added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy failed on B"; }
gauntlet_stage cold_deploy pass "$EXPECT_N instances (five google_project_service, the network, the subnetwork, two firewalls, the simple_bucket module's bucket and IAM member, five authoritative project IAM bindings from projects_iam) from plain terraform against floci-gcp $(gcp_floci_image | sed 's/.*@sha256:\(.\{12\}\).*/sha256:\1/'), the k8s.io root at ${PIN_COMMIT:0:10} with three deltas (provider.tf for the lane's 8.6.0 pin and the emulator, simple_bucket ~> 12.4 for 8.x, the five VMware Engine resources the emulator does not serve left out); a real terraform.tfstate, zero tofu-estate labels read back with the REST API; the identical root cold-deployed by stock on a second emulator as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_config "$ADOPTED" live "$EPA" || fail "could not write the adopted root"
( cd "$ADOPTED" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" \
  || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) refused the root. First Error: line: \"$(gauntlet_first_error_line <<< "$IMPORT_OUT")\". If it is \"Module output not supported in static context\", it is projects_iam's for_each over module.helper's outputs, the wall this script's header names"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(cd "$ADOPTED" && "$TOFU" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed: $(gauntlet_first_error_line <<< "$APPROVE_OUT")"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
# The bucket is stamped through a labels-only plan (#1121's
# google-labels-plan write). The fifteen with no labels argument are
# SKIPPED with their identities recorded, as AWS's untaggable resources are
# (reference-eks): the skip is the marker write, and "16 identities
# recorded" says every instance went into the record store. Only the
# missing-labels reason may be skipped.
read -r M_STAMPED M_SKIPPED M_SKIPPED_OTHER M_FAILED M_IDENTITIES < <(gcp_migrate_counts <<< "$APPROVE_OUT")
LABELLED="$(count_a)"
if [ "$M_STAMPED" = "1" ] && [ "$M_SKIPPED" = "$((EXPECT_N - 1))" ] && [ "$M_SKIPPED_OTHER" = "0" ] && [ "$M_FAILED" = "0" ] && [ "$M_IDENTITIES" = "$EXPECT_N" ] && [ "$LABELLED" = "1" ]; then
  gauntlet_stage migrate pass "$EXPECT_N of $EXPECT_N bound: the module's bucket stamped through a labels-only plan and carrying tofu-estate=$ESTATE read back with the REST API; the $((EXPECT_N - 1)) with no labels argument (five services, network, subnetwork, two firewalls, the bucket IAM member, five project IAM bindings) skipped for that reason alone, and all $EXPECT_N identities recorded"
else
  gauntlet_stage migrate fail "live-import -approve did not bind $EXPECT_N of $EXPECT_N with 1 stamped, $((EXPECT_N - 1)) skipped for having no labels argument, 0 failed and $EXPECT_N identities recorded: ${SUMMARY_LINE:-it printed no summary line} ($M_SKIPPED_OTHER skipped for another reason, $M_IDENTITIES identities recorded); $LABELLED bucket(s) carry tofu-estate=$ESTATE on the emulator. FAILED rows: \"$(gcp_failed_rows <<< "$APPROVE_OUT")\". First Error: line: \"$(gauntlet_first_error_line <<< "$APPROVE_OUT")\""
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read with the REST API ==="
PLAN_OUT="$(plan_a)" || { printf '%s\n' "$PLAN_OUT" | tail -20; fail "the post-migration plan failed: $(gauntlet_first_error_line <<< "$PLAN_OUT")"; }
IDS_OK=1; IDS_MISSING=""
for path in "compute/v1/projects/$PROJECT/global/networks/maintenance-vpc-network" \
            "compute/v1/projects/$PROJECT/regions/$REGION/subnetworks/maintenance-subnet" \
            "$(fw_path maintenance-firewall-internet)" "$(fw_path maintenance-firewall-internal)" \
            "$(bucket_path "$BUCKET")"; do
  exists_a "$path" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $path"; }
done
for svc in cloudasset compute essentialcontacts secretmanager vmwareengine; do
  [ "$(svc_a "$svc.googleapis.com")" = "ENABLED" ] || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $svc.googleapis.com"; }
done
POLICY="$(ra POST "v1/projects/$PROJECT:getIamPolicy" '{}' 2>/dev/null)"
for role in roles/admin roles/secretmanager.secretAccessor roles/viewer roles/serviceusage.serviceUsageConsumer roles/bigquery.admin; do
  grep -qF "\"$role\"" <<< "$POLICY" || { IDS_OK=0; IDS_MISSING="$IDS_MISSING $role"; }
done
if grep -q "No changes." <<< "$PLAN_OUT" && [ "$IDS_OK" = "1" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all $EXPECT_N identities confirmed with the REST API: the network, subnetwork, two firewalls and bucket by name under projects/$PROJECT, the five services ENABLED, the five roles in the project's policy"
else
  gauntlet_stage test_plan fail "the plan with no state file is not empty ($(gauntlet_plan_line <<< "$PLAN_OUT")) or an identity is missing (identities confirmed: $IDS_OK;${IDS_MISSING:- none missing})"
  log "  adopting through choudoufu's own apply so the day-2 stages below run on a labelled estate"
  ADOPT_OUT="$(apply_a)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the adopting apply failed"; }
  REPLAN="$(plan_a)" || fail "the replan after the adopting apply failed"
  grep -q "No changes." <<< "$REPLAN" || { printf '%s\n' "$REPLAN" | tail -20; fail "the replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
fi
[ "$(count_a)" = "1" ] || fail "$(count_a) bucket(s) carry tofu-estate=$ESTATE after adoption, want 1"

(
# ── 4. test_apply: no-op apply, marker count unchanged ───────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the labelled-object count must not move ==="
BEFORE_N="$(count_a)"
NOOP_OUT="$(apply_a)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "labelled-object count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); buckets carrying tofu-estate=$ESTATE unchanged at $BEFORE_N, counted with the REST API"

); stage_done $? test_apply reconverge

(
# ── 5. drift_reconverge: one firewall tampered out of band ───────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: a REST PATCH on maintenance-firewall-internet's sourceRanges on A and on B; stock's plan on B is the oracle ==="
TAMPER='{"sourceRanges":["203.0.113.0/24"]}'
ra PATCH "$(fw_path maintenance-firewall-internet)" "$TAMPER" >/dev/null || fail "could not tamper the internet firewall on A"
rb PATCH "$(fw_path maintenance-firewall-internet)" "$TAMPER" >/dev/null || fail "could not tamper the internet firewall on B"
if [ "${BREAK:-}" = "1" ]; then
  ra PATCH "$(fw_path maintenance-firewall-internal)" "$TAMPER" >/dev/null || fail "BREAK: could not tamper the internal firewall on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(plan_a)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" \
    && fail "BREAK=1: two firewalls were tampered but the plan still proposes exactly one change - the single-object assertion is not load-bearing"
  log "  BREAK=1: caught - with a second firewall tampered the plan is $(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)"
  ( apply_a >/dev/null ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two firewalls tampered the single-object assertion correctly fails to hold ($(grep -E '^Plan:' <<< "$DRIFT_PLAN" | head -1)); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q 'google_compute_firewall.maintenance-firewall-internet' <<< "$DRIFT_PLAN" || fail "the plan does not name google_compute_firewall.maintenance-firewall-internet"
  RECONV="$(apply_a)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one object"
  RANGES="$(ra GET "$(fw_path maintenance-firewall-internet)" | python3 -c 'import json,sys; print(",".join(json.load(sys.stdin).get("sourceRanges", [])))')"
  [ "$RANGES" = "0.0.0.0/0" ] || fail "the internet firewall's sourceRanges read $RANGES after reconverging, want 0.0.0.0/0"
  gauntlet_stage drift_reconverge pass "the internet firewall's sourceRanges tampered with a REST PATCH; choudoufu proposed exactly google_compute_firewall.maintenance-firewall-internet (0 add, 1 change, 0 destroy), a declaration-carried object read at the identity its configuration computes, matching stock's own plan on the oracle emulator for the same tamper; apply changed 1 and 0.0.0.0/0 reads back. BREAK=1 tampers the internal firewall as well and the single-object assertion correctly fails"
fi

); stage_done $? drift_reconverge reconverge

(
# ── 6. plan_approval: plan -out, the world moves, apply refuses ──────────
gauntlet_begin_stage plan_approval
log "=== 6. plan_approval: a saved plan, an out-of-band firewall change, a refusal; then the same file applies once the world is back ==="
FW_DESC="reviewed"
both_roots
P_PLAN="$(cd "$ADOPTED" && "$TOFU" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
ra PATCH "$(fw_path maintenance-firewall-internal)" '{"sourceRanges":["198.51.100.0/24"]}' >/dev/null \
  || fail "could not move the world (the internal firewall's sourceRanges) on A"
P_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
put_internal_back() {
  ra PATCH "$(fw_path maintenance-firewall-internal)" '{"sourceRanges":["192.168.28.0/24"]}' >/dev/null
}
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  log "  BREAK_APPROVAL=1: caught - the apply after the world moved exited $P_RC"
  put_internal_back || fail "BREAK_APPROVAL: could not put the world back"
  ( cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  put_internal_back || fail "could not put the world back"
  P_APPLY2="$(cd "$ADOPTED" && "$TOFU" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one object"
  GOT="$(ra GET "$(fw_path maintenance-firewall-internet)" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("description",""))')"
  [ "$GOT" = "reviewed" ] || fail "the internet firewall's description reads \"$GOT\" after the saved plan applied, want reviewed"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the internet firewall gains a description); the world then moved out of band (the internal firewall's sourceRanges, a REST PATCH, never choudoufu) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3; with the range put back the identical file applied, 0 added, 1 changed, 0 destroyed, and the description reads back; stock's own planfile applied on the oracle emulator in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

); stage_done $? plan_approval reconverge

(
# ── 7. day2_rename: a moved block on a declaration-carried firewall ──────
gauntlet_begin_stage day2_rename
log "=== 7. day2_rename: google_compute_firewall.maintenance-firewall-internet becomes .maintenance_firewall_internet through a moved block ==="
NEW_FW="google_compute_firewall.maintenance_firewall_internet"
if [ "${BREAK:-}" = "1" ]; then
  # The block rename moves no object, so the control that can fire is a
  # change of the firewall's own name, which is a replace.
  FW_NAME="maintenance-firewall-internet-renamed"
  write_config "$ADOPTED" live "$EPA" || fail "BREAK: could not rewrite the adopted root"
  R_PLAN="$(plan_a)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK: the plan after renaming the firewall failed"; }
  grep -q "1 to add" <<< "$R_PLAN" && grep -q "1 to destroy" <<< "$R_PLAN" \
    || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK=1: renaming the firewall's own name did not plan a destroy and a create - the zero-churn assertion is not load-bearing: $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"; }
  log "  BREAK=1: caught - renaming the firewall plans $(grep -E '^Plan:' <<< "$R_PLAN" | head -1)"
  FW_NAME=""; FW_BLOCK="maintenance_firewall_internet"
  both_roots
  ( apply_a >/dev/null ) || fail "BREAK: the moved-block apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock's moved-block apply failed on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: renaming the firewall's own name plans a replace ($(grep -E '^Plan:' <<< "$R_PLAN" | head -1)), so the zero-churn assertion correctly fails to hold; the moved block then applied"
else
  FW_BLOCK="maintenance_firewall_internet"
  both_roots
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan failed on B"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's moved-block plan on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's moved-block apply failed on B"
  R_PLAN="$(plan_a)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan failed"; }
  grep -qE 'will be (created|destroyed)|must be replaced' <<< "$R_PLAN" && { printf '%s\n' "$R_PLAN" | grep -E '^  # .+ (will be|must be)'; fail "the moved-block rename proposes a create, a destroy or a replace"; }
  grep -qE "^No changes|Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved-block plan is not zero churn: $(gauntlet_plan_line <<< "$R_PLAN")"; }
  R_APPLY="$(apply_a)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the moved-block apply failed"; }
  grep -qF "0 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || fail "the moved-block apply changed something"
  R_REPLAN="$(plan_a)" || fail "the replan after the moved block failed"
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the moved block is not empty: the record did not follow the block"; }
  exists_a "$(fw_path maintenance-firewall-internet)" || fail "the internet firewall is gone after the rename"
  gauntlet_stage day2_rename pass "moved block: google_compute_firewall.maintenance-firewall-internet -> $NEW_FW, zero churn (no add, change or destroy), the apply touching nothing and the replan empty, the firewall still there (REST); a firewall carries no label, so its record is what followed the block. Stock's plan for the same moved block on the oracle emulator is zero churn too. live-mv is not exercised by this stage, so the moved block is the only rename measured. BREAK=1 renames the firewall itself, which is a genuine identity change and plans a destroy and a create"
fi

); stage_done $? day2_rename reconverge

(
# ── 8. day2_remove: delete the internal firewall's block ─────────────────
gauntlet_begin_stage day2_remove
log "=== 8. day2_remove: maintenance-firewall-internal leaves the configuration; its network and subnetwork stay ==="
FW_INT="$(fw_path maintenance-firewall-internal)"
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  K_PLAN="$(plan_a)" || fail "BREAK_REMOVE: the plan with the block kept failed"
  grep -qE "will be destroyed|[1-9][0-9]* to destroy" <<< "$K_PLAN" && fail "BREAK_REMOVE=1: with the block kept a destroy was still proposed"
  log "  BREAK_REMOVE=1: caught - with the block kept the plan proposes no destroy"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the internal firewall's block kept, no destroy is proposed; the real check is skipped"
  DROP_INTERNAL=1; both_roots
  ( apply_a >/dev/null ) || fail "BREAK_REMOVE: the removal apply failed afterwards"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock's removal apply failed on B afterwards"
else
  DROP_INTERNAL=1; both_roots
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
  exists_a "$FW_INT" && fail "the internal firewall still exists after the remove apply"
  exists_a "compute/v1/projects/$PROJECT/global/networks/maintenance-vpc-network" || fail "the network went with its firewall"
  exists_a "compute/v1/projects/$PROJECT/regions/$REGION/subnetworks/maintenance-subnet" || fail "the subnetwork the firewall read its range from went with it"
  D_REPLAN="$(plan_a)" || fail "the replan after the remove failed"
  grep -q "No changes." <<< "$D_REPLAN" || { printf '%s\n' "$D_REPLAN" | tail -20; fail "the replan after the remove is not empty"; }
  gauntlet_stage day2_remove pass "deleting google_compute_firewall.maintenance-firewall-internal's block proposed exactly one destroy (0 add, 0 change, 1 destroy) at $D_ADDR, stock's own address for it - a firewall carries no label, so the estate's record is what proposed the removal - applied cleanly, the firewall gone (REST: 404), the network and the subnetwork it read its range from still there, and the next plan empty; stock's plan for the same removal on the oracle emulator is also exactly one destroy. BREAK_REMOVE=1 keeps the block and no destroy is proposed"
fi

); stage_done $? day2_remove reconverge

(
# ── 9. day2_count: a key leaves the services' for_each and comes back ────
gauntlet_begin_stage day2_count
log "=== 9. day2_count: google_project_service.project loses essentialcontacts.googleapis.com, then gets it back ==="
GONE="essentialcontacts.googleapis.com"
DROP_SERVICE="$GONE"; both_roots
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(plan_a)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan is not exactly one destroy"; }
grep -qF "google_project_service.project[\"$GONE\"] will be destroyed" <<< "$C_PLAN" || fail "the scale-down does not destroy google_project_service.project[\"$GONE\"], stock's choice"
{ APPLY_OUT="$(apply_a)" && grep -qF "0 added, 0 changed, 1 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-down apply did not destroy exactly one object"; }
# What a destroy does to the service is stock's call, not ours: at 8.6.0
# disable_on_destroy is Optional with no default, and the k8s.io root leaves
# it unset, so Delete drops the resource without disabling the service
# (resource_google_project_service.go:334). The oracle's state after stock's
# own scale-down is the expectation.
GONE_A="$(svc_a "$GONE")"; GONE_B="$(svc_b "$GONE")"
C_REPLAN="$(plan_a)" || fail "the replan after the scale-down failed"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  [ "$GONE_A" != "$GONE_B" ] || fail "BREAK_COUNT=1: $GONE is $GONE_A on both emulators - the 'differs from stock' assertion would hold"
  log "  BREAK_COUNT=1: caught - $GONE is $GONE_A on A and $GONE_B on stock's oracle"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: asserting $GONE ended in a different state from stock's oracle correctly fails to hold; the real check is skipped"
else
  [ -n "$GONE_B" ] && [ "$GONE_A" = "$GONE_B" ] || fail "$GONE is ${GONE_A:-unread} after the scale-down; stock's own scale-down left it ${GONE_B:-unread} on the oracle"
  grep -q "No changes." <<< "$C_REPLAN" || { printf '%s\n' "$C_REPLAN" | tail -20; fail "the replan after the scale-down is not empty: the record for $GONE outlived its destroy"; }
  for svc in cloudasset compute secretmanager vmwareengine; do
    [ "$(svc_a "$svc.googleapis.com")" = "ENABLED" ] || fail "$svc.googleapis.com is not ENABLED after the scale-down; only $GONE should have gone"
  done
  DROP_SERVICE=""; both_roots
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(plan_a)" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan failed"; }
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up plan is not exactly one add"; }
  grep -qF "google_project_service.project[\"$GONE\"]" <<< "$U_PLAN" || fail "the scale-up does not create google_project_service.project[\"$GONE\"]"
  { APPLY_OUT="$(apply_a)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "the scale-up apply did not create exactly one object"; }
  U_REPLAN="$(plan_a)" || fail "the replan after the scale-up failed"
  grep -q "No changes." <<< "$U_REPLAN" || { printf '%s\n' "$U_REPLAN" | tail -20; fail "the replan after the scale-up is not empty"; }
  [ "$(svc_a "$GONE")" = "ENABLED" ] || fail "$GONE is not ENABLED after the scale-up"
  gauntlet_stage day2_count pass "dropping $GONE from google_project_service.project's for_each destroyed exactly that key (the service left $GONE_A, as stock's own destroy left it on the oracle, since the root leaves disable_on_destroy unset; the other four still ENABLED, read with the REST API; the replan empty) - a service carries no label, so the key is found through the record and the configuration's identity; putting it back created exactly that key and the next plan is empty; stock's plans for the same two changes on the oracle emulator have the identical shape. BREAK_COUNT=1 asserts the dropped service differs from stock's and correctly fails"
fi

); stage_done $? day2_count reconverge

(
# ── 9b. day2_replace: a create_before_destroy rename ─────────────────────
#
# A bucket's name is unique, so the replacement create_before_destroy is used
# for is one whose name changes, as on kind (#1541). The old and the new
# bucket carry the same tofu-address label for the window between the create
# and the destroy, and the record's deposed entry is what says which one
# goes. A scratch block of the estate's one labelled type: a firewall
# renamed the same way would leave nothing a sweep could find, so the
# stage's Break control could not fire.
gauntlet_begin_stage day2_replace
log "=== 9b. day2_replace: a bucket renamed under create_before_destroy ==="
add_replace() { replace_block "$1" > "$ADOPTED/day2_replace.tf"; replace_block "$1" > "$ORACLE/day2_replace.tf"; }
CBD_A="$PROJECT-gcve-cbd-a"; CBD_B="$PROJECT-gcve-cbd-b"
add_replace a
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not create $CBD_A on B"
{ X_OUT="$(apply_a)" && grep -qF "1 added, 0 changed, 0 destroyed" <<< "$X_OUT"; } || { printf '%s\n' "$X_OUT" | tail -20; fail "choudoufu could not create $CBD_A"; }
add_replace b
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's replace plan failed on B"
grep -q 'must be replaced' <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock does not plan a replace for the rename on B"; }
( stock_b apply -auto-approve -input=false -no-color -parallelism=1 >/dev/null 2>&1 ) || fail "stock's replace apply failed on B"
X_PLAN="$(plan_a)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the replace plan failed"; }
grep -q 'google_storage_bucket.cbd must be replaced' <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the rename does not plan stock's replace of google_storage_bucket.cbd"; }
X_APPLY="$(cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$X_APPLY" | tail -20; fail "the replace apply failed"; }
CREATED_AT="$(grep -n 'google_storage_bucket.cbd: Creation complete' <<< "$X_APPLY" | head -1 | cut -d: -f1)"
DESTROY_AT="$(grep -n 'google_storage_bucket.cbd (deposed object [^)]*): Destroying' <<< "$X_APPLY" | head -1 | cut -d: -f1)"
[ -n "$CREATED_AT" ] && [ -n "$DESTROY_AT" ] && [ "$CREATED_AT" -lt "$DESTROY_AT" ] \
  || { printf '%s\n' "$X_APPLY" | grep -E 'cbd'; fail "the apply log does not show the new bucket's create complete before the old one's destroy starts"; }
exists_a "$(bucket_path "$CBD_A")" && [ "${BREAK_REPLACE:-}" != "1" ] && fail "$CBD_A still exists after the replace"
exists_a "$(bucket_path "$CBD_B")" || fail "$CBD_B does not exist after the replace"
if [ "${BREAK_REPLACE:-}" = "1" ]; then
  ra POST "storage/v1/b?project=$PROJECT" '{"name":"'"$CBD_A"'","location":"US","labels":{"tofu-estate":"'"$ESTATE"'","tofu-address":"'"$(gcp_address_label google_storage_bucket.cbd google_storage_bucket.cbd)"'"}}' >/dev/null \
    || fail "BREAK_REPLACE: could not recreate $CBD_A"
  B_PLAN="$(plan_a 2>&1)"
  grep -q "No changes." <<< "$B_PLAN" && fail "BREAK_REPLACE=1: with the old bucket back, carrying the block's labels, the next plan is empty - the empty-replan assertion is not load-bearing"
  log "  BREAK_REPLACE=1: caught - $(grep -E '^Plan:|^Error|^Warning' <<< "$B_PLAN" | head -1)"
  ra DELETE "$(bucket_path "$CBD_A")" >/dev/null 2>&1 || true
  gauntlet_stage day2_replace pass "BREAK_REPLACE=1 control: the old bucket recreated with the block's labels after the replace makes the next plan non-empty ($(grep -E '^Plan:|^Error|^Warning' <<< "$B_PLAN" | head -1)), so the empty-replan assertion correctly fails to hold"
else
  X_REPLAN="$(plan_a)" || fail "the replan after the replace failed"
  grep -q "No changes." <<< "$X_REPLAN" || { printf '%s\n' "$X_REPLAN" | tail -20; fail "the replan after the replace is not empty"; }
  gauntlet_stage day2_replace pass "a create_before_destroy rename of a scratch google_storage_bucket.cbd ($CBD_A -> $CBD_B) planned stock's replace (must be replaced, create first); at -parallelism=1 the apply log shows the new bucket's create complete (line $CREATED_AT) before the deposed object's destroy starts (line $DESTROY_AT); only $CBD_B remains, read with the REST API, and the next plan is empty. Stock's replace of the same block on the oracle emulator leaves the same single bucket. BREAK_REPLACE=1 recreates the old bucket with the block's labels and the next plan correctly stops being empty"
fi
rm -f "$ADOPTED/day2_replace.tf" "$ORACLE/day2_replace.tf"
( apply_a >/dev/null ) || fail "could not remove the replace block from A"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "could not remove the replace block from B"

); stage_done $? day2_replace reconverge

(
# ── 10. day2_crash: an apply of two buckets, killed after the first ──────
#
# The engine's own interrupt (internal/command/apply_e2etesting_crash.go)
# self-delivers SIGTERM inside the -parallelism=1 walker the instant
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT's address finishes a real create.
# crash_second's name reads crash_first's, so the second create cannot have
# been dispatched. The oracle is stock on B walked into the same position.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of one bucket and the create of the next ==="
CF="$PROJECT-gcve-crash-first"; CS="$PROJECT-gcve-crash-first-second"
crash_pair_first > "$ORACLE/day2_crash.tf" || fail "could not write crash_first to the oracle root"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash_first apply failed on B"
crash_pair_second >> "$ORACLE/day2_crash.tf" || fail "could not append crash_second to the oracle root"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's remainder plan failed on B"
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
{ crash_pair_first; crash_pair_second; } > "$ADOPTED/day2_crash.tf" || fail "could not write the crash pair to the adopted root"
X_PLAN="$(plan_a)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds"; }
X_OUT="$(cd "$ADOPTED" && TOFU_E2E_APPLY_RESOURCE_INTERRUPT="google_storage_bucket.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - nothing was interrupted"; }
exists_a "$(bucket_path "$CF")" || fail "$CF does not exist after the interrupted apply - the kill landed before the create committed"
exists_a "$(bucket_path "$CS")" && fail "$CS exists after the interrupted apply - there is no remainder to propose"
CF_LABELS="$(gcp_labels "$EPA" "$(bucket_path "$CF")")"
grep -q "\"tofu-estate\": \"$ESTATE\"" <<< "$CF_LABELS" || fail "$CF was created by the interrupted apply but carries no tofu-estate=$ESTATE label: $CF_LABELS"
if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  ra PATCH "$(bucket_path "$CF")" '{"labels":{"tofu-estate":null,"tofu-address":null}}' >/dev/null || fail "BREAK_CRASH_UNBOUND: could not strip the labels off $CF"
fi
R_PLAN="$(plan_a)"; R_RC=$?
R_LINE="$(grep -E '^Plan:|^No changes' <<< "$R_PLAN" | head -1 | sed 's/\.$//')"
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# google_storage_bucket\.crash_second will be created' <<< "$R_PLAN" || return 1
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
  recovered && fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with $CF carrying no label"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with the labels stripped off the bucket the interrupted apply created, the recovery check correctly fails to hold ($R_LINE)"
  ra DELETE "$(bucket_path "$CF")" >/dev/null 2>&1 || true
  ( apply_a >/dev/null ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
elif ! recovered; then
  gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of google_storage_bucket.crash_first and the create of google_storage_bucket.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC). $CF exists carrying tofu-estate=$ESTATE and the second bucket does not, both read with the REST API; stock, walked into the same position on the oracle emulator, plans exactly one add"
  # Best effort, so day2_teardown compares the same set of buckets on A and B.
  ( apply_a >/dev/null 2>&1 ) || log "  the apply after day2_crash's failed recovery check failed too; day2_teardown's count may differ by the crash pair"
else
  R_APPLY="$(apply_a)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || fail "the recovery apply did not add exactly the one remaining bucket"
  R_REPLAN="$(plan_a)" || fail "the replan after the recovery failed"
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
  gauntlet_stage day2_crash pass "an apply creating two buckets was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant google_storage_bucket.crash_first's create committed; crash_second's name reads crash_first's, so the walker cannot have reached it, and the REST API confirms the first exists carrying tofu-estate=$ESTATE and the second does not. The next plan proposed exactly the remainder ($R_LINE, google_storage_bucket.crash_second created) and nothing for crash_first, which it bound by its labels, matching stock's own plan from the same position on the oracle emulator; the recovery apply added exactly one bucket and the plan after it is empty. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips crash_first's labels and the same check correctly fails"
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
[ "$(count_a)" = "0" ] || fail "$(count_a) bucket(s) still carry tofu-estate=$ESTATE after the destroy"
for path in "compute/v1/projects/$PROJECT/global/networks/maintenance-vpc-network" \
            "compute/v1/projects/$PROJECT/regions/$REGION/subnetworks/maintenance-subnet" \
            "$(fw_path maintenance-firewall-internet)"; do
  exists_a "$path" && fail "$path still exists after the destroy - an unlabelled resource the estate declared was left behind"
done
O_EXPECT="$(stock_b state list 2>/dev/null | grep -vc '^data\.\|\.data\.' | tr -d ' ')"
O_T="$(stock_b apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$O_T" | tail -10; fail "stock's destroy failed on B"; }
grep -qF "Resources: 0 added, 0 changed, $O_EXPECT destroyed" <<< "$O_T" || fail "stock's destroy on B did not remove exactly the $O_EXPECT objects its state held"
[ "${T_N:-}" = "$O_EXPECT" ] || fail "apply -destroy removed ${T_N:-an unreported number of} object(s); stock's destroy of the same estate removed $O_EXPECT (cache listed ${T_EXPECT:-?})"
# A destroyed google_project_service leaves the API as stock's destroy
# leaves it on B: the root leaves disable_on_destroy unset, so both stay
# ENABLED (gcve_service_mismatches says why). Asserting "not ENABLED" here
# failed a destroy that did exactly what stock does.
SVC_DIFF="$(gcve_service_mismatches "$EPA" "$EPB" "$PROJECT")"
[ -z "$SVC_DIFF" ] || fail "after both destroys a service differs from stock's oracle: $SVC_DIFF"
gauntlet_stage day2_teardown pass "apply -destroy removed $T_N objects in one apply, the same number stock's destroy of the same estate removed on the oracle emulator; no bucket carries tofu-estate=$ESTATE, the unlabelled network, subnetwork and firewall are gone (REST: 404), and each of the five services is in the state stock's own destroy left it in on the oracle ($(svc_a compute.googleapis.com) for compute.googleapis.com: the root leaves disable_on_destroy unset, so neither destroy disables an API)"

); stage_done $? day2_teardown

(
# ── 12. greenfield: the same root, fresh, with a live block ──────────────
gauntlet_begin_stage greenfield
log "=== 12. greenfield: choudoufu applies the root fresh on the now-empty emulator A ==="
FW_BLOCK="maintenance-firewall-internet"; FW_NAME=""; FW_DESC=""; DROP_INTERNAL=0; DROP_SERVICE=""
write_config "$GREEN" live "$EPA" || fail "could not write the greenfield root"
( cd "$GREEN" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(cd "$GREEN" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "greenfield apply failed: $(gauntlet_first_error_line <<< "$G_OUT")"; }
# Every check below runs and the verdict names each that failed, so one run
# says whether the count, the replans and the inventory hold, not only the
# first of them.
G_PROBLEMS=""
g_problem() { G_PROBLEMS="${G_PROBLEMS:+$G_PROBLEMS; }$*"; }
if ! grep -qF "Apply complete! Resources: $EXPECT_N added, 0 changed, 0 destroyed" <<< "$G_OUT"; then
  ( cd "$STOCK" && terraform state list 2>/dev/null ) | grep -v '^data\.\|\.data\.' > "$WORK/greenfield.expected"
  g_problem "the apply did not add exactly $EXPECT_N instances ($(grep -oE 'Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$G_OUT" | head -1)); stock's cold-deploy addresses with no Creation complete line: $(gcp_not_created "$WORK/greenfield.expected" <<< "$G_OUT")"
fi
[ ! -f "$GREEN/terraform.tfstate" ] || g_problem "a terraform.tfstate appeared after a live-block apply"
G_N="$(count_a)"
[ "$G_N" = "1" ] || g_problem "$G_N bucket(s) carry tofu-estate=$ESTATE after the greenfield apply, want 1"
G_PLAN="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; G_RC=$?
{ [ "$G_RC" -eq 0 ] && grep -q "No changes." <<< "$G_PLAN"; } || { printf '%s\n' "$G_PLAN" | tail -20; g_problem "the greenfield replan is not empty (exit $G_RC, $(gauntlet_plan_line <<< "$G_PLAN"))"; }
rm -f "$GREEN/.terraform/choudoufu-cache.tfstate"
G_PLAN2="$(cd "$GREEN" && "$TOFU" plan -input=false -no-color 2>&1)"; G_RC2=$?
{ [ "$G_RC2" -eq 0 ] && grep -q "No changes." <<< "$G_PLAN2"; } || { printf '%s\n' "$G_PLAN2" | tail -20; g_problem "the greenfield replan without the cache is not empty (exit $G_RC2, $(gauntlet_plan_line <<< "$G_PLAN2"))"; }
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="firewall/maintenance-firewall-internet"
gcve_inventory "$EPA" "$PROJECT" "$REGION" "$BUCKET" "$DROP" > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory${G_PROBLEMS:+; also: $G_PROBLEMS}"
if [ "${BREAK:-}" = "1" ]; then
  diff -q "$WORK/inventory.stock.json" "$WORK/inventory.green.json" >/dev/null \
    && fail "BREAK=1: with the internet firewall dropped from the greenfield inventory the two inventories still match"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the internet firewall from the greenfield inventory makes the object-by-object comparison correctly fail${G_PROBLEMS:+ (the other checks: $G_PROBLEMS)}"
else
  diff -u "$WORK/inventory.stock.json" "$WORK/inventory.green.json" || g_problem "the greenfield inventory differs from stock's cold-deploy inventory (diff above)"
  if [ -n "$G_PROBLEMS" ]; then
    gauntlet_stage greenfield fail "$G_PROBLEMS"
  else
    gauntlet_stage greenfield pass "$EXPECT_N instances applied fresh with a live block and no terraform.tfstate, the bucket carrying tofu-estate=$ESTATE (REST API); replanned empty with and without the cache; the emulator's inventory (the network, the subnetwork's range, both firewalls' rules, the bucket's location, versioning and storage.admin member, the five services' state, the five roles' members in the project policy) matches stock's cold deploy on the same emulator object by object, labels never compared. BREAK=1 drops the internet firewall from the expected inventory and the match correctly fails"
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
  ra PATCH "$(bucket_path "$BUCKET")" '{"labels":{"tofu-estate":null}}' >/dev/null || fail "BREAK_NO_LOCAL_STATE: could not strip the bucket's label"
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
  gauntlet_stage no_local_state fail "with the record store and the cache deleted the plan exited $N_RC and proposes $N_BAD create/destroy/replace(s) (${N_LINE:-no plan line}); the bucket is found by its labels, but the fifteen with no labels argument have only their records and their configuration to be found by. Provider requests: $REQ_N with no local state, $REQ_C cache-serving"
else
  gauntlet_stage no_local_state pass "after the greenfield apply's empty plan, deleting both the record store and the state cache left a plan that created, destroyed and replaced nothing (${N_LINE:-No changes}): the bucket found by its labels, the fifteen declaration-carried instances (services, network, subnetwork, firewalls, the bucket IAM member, the project IAM bindings) by the identity their configuration computes. hashicorp/google sent $REQ_N request(s) for that plan against $REQ_C for the cache-serving plan before it (counted from TF_LOG=debug's request blocks). BREAK_NO_LOCAL_STATE=1 strips the bucket's label and the plan is no longer empty"
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
    estate = "$ESTATE-strict"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["google_storage_bucket"]
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
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming google_storage_bucket) against a scratch estate carrying random_password.db: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

); stage_done $? strict

gauntlet_end
log "corpus-k8s-infra-gcp-gcve: done"
gcp_exit_status
