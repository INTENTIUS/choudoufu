#!/usr/bin/env bash
# reference-azure: the azure lane's first estate (#1120, live/ADDING-A-
# PLATFORM.md requirement 14), crossed on the floci-az substrate. A
# hand-written reference shape kept in this repository: a resource group, a
# VNet with a subnet, a network security group with one rule, a storage
# account, a user-assigned identity, a public IP and a two-instance count of
# identities through hashicorp/azurerm 5.10.0, plus one azapi_resource of a
# type azurerm also has (Microsoft.ManagedIdentity/userAssignedIdentities),
# so the marker's azapi path is crossed beside the azurerm one. Eleven
# resources; the subnet and the rule carry no tags argument, so nine carry
# the marker.
#
# Two floci-az containers, both started for this run and removed after it:
#
#   A  the estate. Stock terraform cold-deploys it (cold_deploy), choudoufu
#      adopts it from stock's state (migrate) and runs the day-2 stages on
#      it, tears it down, then applies the same shape fresh with a live
#      block (greenfield) and compares it with what stock's cold deploy
#      left.
#   B  the oracle. Stock terraform cold-deploys the identical shape and
#      makes every day-2 change itself, so "what stock does for the same
#      change" is stock's own plan, never a state file choudoufu has moved
#      on from.
#
# What reads differently on this substrate, per tools/gauntlet/stages.go's
# flociAzNotes: identities are ARM ids compared case-insensitively; the
# marker count is the declared resources whose tofu-estate tag names the
# estate, read with ARM GETs; the out-of-band hand is an ARM GET and PUT
# (gauntlet_arm_set_tag), not the AWS CLI.
#
# Run twice for #1120: cold_deploy through test_apply pass on floci-az
# 30000b0. floci-az's gaps (live/FLOCI.md, Azure) fail stages by name rather
# than being skipped. At 30000b0 its Resource Graph refused the sweep's
# query (lex00/floci-az#11), which the plan reported as the "Azure sweep
# unavailable" warning, and only the sweep finds a removed resource of a
# type that takes tags, so day2_remove and day2_count could not pass. The
# pin is now dfa33ca, which runs the query as sent, so both are expected to
# pass; a plan that still warns fails them naming the warning. Gaps are the
# emulator's to close (emulator policy, live/ADDING-A-PLATFORM.md).
# day2_replace, day2_crash, strict and no_local_state are written for this
# substrate since the 2026-10-21 run at 31639780dd cleared every other
# stage, and have not been run: the maintainer's next run measures them.
#
# Records live in the record store and the state file is a cache (the
# maintainer's ruling, 2026-10-21). The record store is object storage on
# the same container A: record_store "azurerm" naming a storage account
# made out of band before migrate (gauntlet_floci_az_record_account, with
# blob versioning, a previous-versions lifecycle rule and anonymous access
# off, the three settings its opening checks assert), one container for
# the adopted estate, one for greenfield's and one for strict's scratch
# estate. No stage reads or deletes a local record directory: a stage that
# reads a record back copies the container (gauntlet_floci_az_blobs), and
# no_local_state deletes only what is local, the state cache.
#
# A stage this script cannot pass records fail with the reason and the run
# continues where it can (day2_remove and day2_count put the shape the later
# stages expect back by hand); the runner, not the script, decides what
# "clear" means. A fault in the substrate itself (a container that never
# came up) is a fail on the stage being set up and an exit.
#
#   go run ./tools/gauntlet run reference-azure   # the maintainer, by hand
#   bash live/e2e/reference-azure/run.sh           # asks for "run" at the terminal
#
# Needs docker, curl, terraform (the stock binary), python3 and Go on PATH.
# On macOS run scripts/floci-az-mac-cert.sh once and trust the certificate
# it prints in the keychain (Go there ignores SSL_CERT_FILE); live/FLOCI.md
# says how.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls instead of the real
#                  checks: a second resource is tampered and the
#                  single-resource assertion must fail; the identity's own
#                  name is changed instead of its block's (a genuine
#                  identity change) and the zero-churn assertion must fail;
#                  the public IP is dropped from greenfield's expected
#                  inventory and the resource-by-resource match must fail.
#   BREAK_REMOVE   set to 1 to keep the public IP's block and assert a
#                  destroy is proposed anyway (day2_remove's Break line).
#   BREAK_COUNT    set to 1 to assert the wrong instance was destroyed on the
#                  scale-down (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved and
#                  expect success (plan_approval's Break line); must fail.
#   BREAK_REPLACE  set to 1 to recreate the renamed identity's old object,
#                  carrying the estate's marker and the block's address,
#                  after day2_replace's apply; the next plan must not be
#                  silent about it (day2_replace's Break line).
#   BREAK_CRASH    set to 1 to assert, after each of day2_crash's real
#                  interrupts, that nothing is proposed (its Break line);
#                  must fail, because a recovered run proposes the remainder.
#   BREAK_CRASH_UNBOUND
#                  set to 1 to strip tofu-estate off the identity the
#                  interrupted two-resource apply did create before
#                  replanning; the recovery check must then fail to hold.
#   BREAK_STRICT   set to 1 to turn secrets back to "store" and require the
#                  refusal to vanish (strict's Break line).
#   BREAK_NO_LOCAL_STATE
#                  strip the VNet's two marker tags before no_local_state's
#                  plan with no local state; that plan must then propose
#                  something other than in-place updates, so the check fails.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

gauntlet_plugin_cache
ESTATE="reference-azure"
RG="refaz-rg"
# The record store's account: a resource group of its own, so day2_teardown
# (which deletes refaz-rg) leaves the records standing for greenfield.
REC_RG="refaz-records-rg"; REC_ACCOUNT="refazrecords"; HOST_A=""
SUB="/subscriptions/${FLOCI_AZ_SUBSCRIPTION_ID}"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
CONTAINER_A="choudoufu-${ESTATE}-a-$$"; CONTAINER_B="choudoufu-${ESTATE}-b-$$"
export TF_IN_AUTOMATION=1
log() { printf '%s\n' "$*"; }

CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_floci_az_down "$CONTAINER_A"
  gauntlet_floci_az_down "$CONTAINER_B"
  rm -rf "$WORK"
}
trap cleanup EXIT
gauntlet_begin

# ── 0. tools ─────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v curl >/dev/null 2>&1 || fail "curl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"

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
# day2_crash needs a build with e2eTestingFeatures set, so the engine's own
# TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook (internal/command/
# apply_e2etesting_crash.go) is reachable, as the kind and floci estates
# build it: from this tree's source whatever $TOFU came from, and identical
# to $TOFU everywhere else, since the hook is a no-op unless the variable
# names an address the run applies.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

# ── the shape ────────────────────────────────────────────────────────────
# The azurerm and azapi requirements are live/oracle-versions.json's pins,
# read once behind one fail each (the #1216 rule, as the kind estates read
# hashicorp/kubernetes's).
AZURERM_REQUIRED_PROVIDER="$(gauntlet_azurerm_required_provider)" \
  || fail "could not read the hashicorp/azurerm pin from live/oracle-versions.json"
AZAPI_REQUIRED_PROVIDER="$(gauntlet_azapi_required_provider)" \
  || fail "could not read the azure/azapi pin from live/oracle-versions.json"

versions_block() { # $1 = "live" to include the live block, anything else for stock
  cat <<EOF
terraform {
  required_providers {
$AZURERM_REQUIRED_PROVIDER
$AZAPI_REQUIRED_PROVIDER
  }
EOF
  if [ "$1" = "live" ]; then
    # The record store is container A's (HOST_A): only container A ever
    # runs choudoufu. REC_CONTAINER picks the estate's container; greenfield
    # names its own, as each lane's greenfield root has a store of its own.
    printf '  live {\n    estate = "%s"\n' "$ESTATE"
    gauntlet_floci_az_record_store "$HOST_A" "$REC_RG" "$REC_ACCOUNT" "${REC_CONTAINER:-adopted}" || return 1
    printf '  }\n'
  fi
  cat <<EOF
}

EOF
  # The provider blocks state the subscription (identity reads it from the
  # block, #1120's corrections) and every endpoint and credential argument
  # (the endpoint lock, #2133): metadata_host is var.floci_az_host, which
  # each container's saved environment sets, and nothing is left for the
  # operator's shell to fill.
  gauntlet_floci_az_provider_blocks || return 1
}
# resource_block writes the eleven resources. $1 is the identity's block
# name (app, then team after the rename); $2 is the shard count; $3 is an
# extra line inside the identity block (plan_approval's tags); $4 is the
# identity's own name (BREAK's rename control).
resource_block() {
  local id="${1:-app}" shards="${2:-2}" extra="${3:-}" idname="${4:-refaz-app}"
  cat <<EOF
resource "azurerm_resource_group" "app" {
  name     = "$RG"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "refaz-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.50.0.0/16"]
}

resource "azurerm_subnet" "app" {
  name                 = "refaz-subnet"
  resource_group_name  = azurerm_resource_group.app.name
  virtual_network_name = azurerm_virtual_network.app.name
  address_prefixes     = ["10.50.1.0/24"]
}

resource "azurerm_network_security_group" "app" {
  name                = "refaz-nsg"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}

resource "azurerm_network_security_rule" "ssh" {
  name                        = "ssh"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "22"
  source_address_prefix       = "10.0.0.0/8"
  destination_address_prefix  = "*"
  resource_group_name         = azurerm_resource_group.app.name
  network_security_group_name = azurerm_network_security_group.app.name
}

resource "azurerm_storage_account" "app" {
  name                     = "refazstore"
  resource_group_name      = azurerm_resource_group.app.name
  location                 = azurerm_resource_group.app.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_user_assigned_identity" "$id" {
  name                = "$idname"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
$extra
}

resource "azurerm_public_ip" "app" {
  name                = "refaz-pip"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  allocation_method   = "Static"
  sku                 = "Standard"
}

resource "azurerm_user_assigned_identity" "shard" {
  count               = $shards
  name                = "refaz-shard-\${count.index}"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}

# The azapi spelling of a type azurerm also has: its marker goes through
# azapi's top-level tags argument (#1122's ruling).
resource "azapi_resource" "worker" {
  type      = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
  name      = "refaz-worker"
  parent_id = azurerm_resource_group.app.id
  location  = "eastus"
}
EOF
}
write_config() { # $1 dir, $2 live|stock, then resource_block's args
  local dir="$1" mode="$2"; shift 2
  mkdir -p "$dir"
  { versions_block "$mode" && echo && resource_block "$@"; } > "$dir/main.tf" \
    || fail "could not write $dir/main.tf's provider blocks"
}
moved_block() { printf '\nmoved {\n  from = azurerm_user_assigned_identity.app\n  to   = azurerm_user_assigned_identity.team\n}\n'; }

# ── substrate helpers ────────────────────────────────────────────────────
# Each container is aimed at by its own ARM_* values. gauntlet_floci_az_up
# exports them; they are saved per container and every command below runs
# under one of the two (on_a, on_b), so nothing reaches the wrong one.
save_env() { env | grep -E '^(FLOCI_AZ_(HOST|ENDPOINT|CA_FILE)|ARM_[A-Z_]+|SSL_CERT_FILE|AZURE_CONFIG_DIR|TF_VAR_floci_az_host)=' > "$1"; }
on_a() { ( set -a; . "$WORK/a.env"; set +a; "$@" ); }
on_b() { ( set -a; . "$WORK/b.env"; set +a; "$@" ); }
stock_b() { ( cd "$ORACLE" && on_b terraform "$@" ); }
tofu_a() { local dir="$1"; shift; ( cd "$dir" && on_a "$TOFU" "$@" ); }

NET="Microsoft.Network"; MI="Microsoft.ManagedIdentity/userAssignedIdentities"
# The taggable resources, with the api-version each is read at. The subnet
# and the rule have no tags argument and are not in the count.
TAGGED_IDS="
${SUB}/resourceGroups/${RG}|2021-04-01
${SUB}/resourceGroups/${RG}/providers/${NET}/virtualNetworks/refaz-vnet|2024-05-01
${SUB}/resourceGroups/${RG}/providers/${NET}/networkSecurityGroups/refaz-nsg|2024-05-01
${SUB}/resourceGroups/${RG}/providers/Microsoft.Storage/storageAccounts/refazstore|2023-05-01
${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-app|2023-01-31
${SUB}/resourceGroups/${RG}/providers/${NET}/publicIPAddresses/refaz-pip|2024-05-01
${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-shard-0|2023-01-31
${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-shard-1|2023-01-31
${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-worker|2023-01-31"
ALL_IDS="$TAGGED_IDS
${SUB}/resourceGroups/${RG}/providers/${NET}/virtualNetworks/refaz-vnet/subnets/refaz-subnet|2024-05-01
${SUB}/resourceGroups/${RG}/providers/${NET}/networkSecurityGroups/refaz-nsg/securityRules/ssh|2024-05-01"

# arm_read <env a|b> prints one JSON object keyed by lowercased ARM id: the
# id ARM answered with, its tags, and the properties this shape declares,
# or null for an id that answers 404. Python reads through the container's
# own CA, as the providers do.
arm_read() {
  "on_$1" env IDS="$ALL_IDS" python3 - <<'PY'
import json, os, ssl, urllib.request, urllib.error
ctx = ssl.create_default_context(cafile=os.environ["FLOCI_AZ_CA_FILE"])
out = {}
for line in os.environ["IDS"].split():
    rid, api = line.split("|")
    req = urllib.request.Request(os.environ["FLOCI_AZ_ENDPOINT"] + rid + "?api-version=" + api,
                                 headers={"Authorization": "Bearer floci-az"})
    try:
        body = json.load(urllib.request.urlopen(req, context=ctx, timeout=30))
    except urllib.error.HTTPError as e:
        if e.code == 404:
            out[rid.lower()] = None
            continue
        raise
    p = body.get("properties") or {}
    keep = {k: p.get(k) for k in ("addressSpace", "addressPrefix", "addressPrefixes", "priority", "access",
                                  "direction", "destinationPortRange", "publicIPAllocationMethod") if k in p}
    out[rid.lower()] = {"id": body.get("id", ""), "location": body.get("location"),
                        "sku": (body.get("sku") or {}).get("name"), "tags": body.get("tags") or {}, "props": keep}
print(json.dumps(out, sort_keys=True))
PY
}
# count_a is the marker count on A: taggable resources whose tofu-estate
# tag names the estate, key and value compared case-insensitively.
count_a() {
  arm_read a | ESTATE="$ESTATE" python3 -c '
import json, os, sys
inv = json.load(sys.stdin)
n = 0
for rid, r in inv.items():
    if r is None:
        continue
    for k, v in r["tags"].items():
        if k.lower() == "tofu-estate" and str(v).lower() == os.environ["ESTATE"].lower():
            n += 1
print(n)'
}
# inventory <a|b> [drop-id-suffix]: arm_read normalised for comparison:
# marker tags removed, ids as lowercased keys, the answering id dropped.
inventory() {
  arm_read "$1" | DROP="${2:-}" python3 -c '
import json, os, sys
inv = json.load(sys.stdin)
drop = os.environ.get("DROP", "").lower()
out = {}
for rid, r in inv.items():
    if drop and rid.endswith(drop):
        continue
    if r is not None:
        r = dict(r)
        r.pop("id", None)
        r["tags"] = {k: v for k, v in r["tags"].items() if not k.lower().startswith("tofu-")}
    out[rid] = r
print(json.dumps(out, sort_keys=True, indent=1))'
}
# tag_set <a|b> <id> <api> <key> <value|-> sets or removes one tag and
# keeps the rest, with an ARM GET and a PUT of the body read back
# (gauntlet_arm_set_tag): floci-az serves no PATCH on Microsoft.Network.
tag_set() { "on_$1" gauntlet_arm_set_tag "$2?api-version=$3" "$4" "$5" >/dev/null; }
gone_a() { ! on_a gauntlet_arm GET "$1?api-version=$2" >/dev/null 2>&1; }
VNET_ID="${SUB}/resourceGroups/${RG}/providers/${NET}/virtualNetworks/refaz-vnet"
PIP_ID="${SUB}/resourceGroups/${RG}/providers/${NET}/publicIPAddresses/refaz-pip"
APP_ID="${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-app"
SHARD0_ID="${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-shard-0"
SHARD1_ID="${SUB}/resourceGroups/${RG}/providers/${MI}/refaz-shard-1"
NET_API="2024-05-01"; MI_API="2023-01-31"

# ── 1. cold_deploy: stock stands the estate up on A (and B, the oracle) ──
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two floci-az containers, stock terraform applies the shape on each ==="
mkdir -p "$WORK/a" "$WORK/b"
gauntlet_floci_az_up "$CONTAINER_A" "$WORK/a" || fail "floci-az container A ($CONTAINER_A) did not come up"
save_env "$WORK/a.env"
HOST_A="$FLOCI_AZ_HOST"
gauntlet_floci_az_up "$CONTAINER_B" "$WORK/b" || fail "floci-az container B ($CONTAINER_B) did not come up"
save_env "$WORK/b.env"
# B's SSL_CERT_FILE bundle was built on top of A's, so it trusts both; A's
# saved one trusts A alone, which is all A's commands reach.
write_config "$STOCK" stock
write_config "$ORACLE" stock
( cd "$STOCK" && on_a gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK" && on_a terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 11 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 11 resources on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(cd "$STOCK" && terraform state list | wc -l | tr -d ' ')"
[ "$STOCK_N" = "11" ] || fail "stock's state holds $STOCK_N instances, want 11"
UNMARKED="$(count_a)" || fail "could not read A's resources through ARM"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED resource(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
inventory a > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 11 added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy failed on B"; }
gauntlet_stage cold_deploy pass "11 resources (resource group, VNet, subnet, NSG, NSG rule, storage account, identity, public IP, two count identities, and one azapi_resource identity) from plain terraform against floci-az $(cat "$ROOT/live/floci-az-image"), a real terraform.tfstate with 11 instances, zero tofu-estate tags read back through ARM; the identical shape cold-deployed by stock on a second container as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
on_a gauntlet_floci_az_record_account "$REC_RG" "$REC_ACCOUNT" adopted green strict \
  || fail "could not make the record store's storage account $REC_ACCOUNT on A"
write_config "$ADOPTED" live
( tofu_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
TAGGED="$(count_a)" || fail "could not read A's resources through ARM"
# The subnet and the NSG rule have no tags argument: live-import reports
# them UNTAGGABLE and SKIPPED (the marker write is what was skipped) and
# records their identity, as it does an untaggable AWS type's. So exactly
# those two are skipped, and any other skip is a failure named with its
# reason (#1120's first run skipped azapi_resource.worker with nothing said).
SKIPPED_ADDRS="$(gauntlet_live_import_skipped_addrs <<< "$APPROVE_OUT")"
WANT_SKIPPED="azurerm_network_security_rule.ssh azurerm_subnet.app"
# The two untaggable resources' identities are recorded in the record
# store, a container on A: read back by each envelope's own address.
# records_copy <container>: a fresh copy of it, its path printed. A new
# directory per reading (mktemp, since a caller reads the path through a
# command substitution, where a counter would not survive).
records_copy() {
  local d
  d="$(mktemp -d "$WORK/records.XXXXXX")" || return 1
  on_a gauntlet_floci_az_blobs "$REC_ACCOUNT" "$1" "$d" >/dev/null || return 1
  printf '%s\n' "$d"
}
M_RECORDS="$(records_copy adopted)" || fail "could not copy the record store's adopted container on A"
M_UNRECORDED=""
for a in $WANT_SKIPPED; do
  gauntlet_record_file "$M_RECORDS" "$a" >/dev/null || M_UNRECORDED="$M_UNRECORDED $a"
done
if grep -qE ' 0 failed, 2 skipped' <<< "$SUMMARY_LINE" && [ "$SKIPPED_ADDRS" = "$WANT_SKIPPED" ] && [ "$TAGGED" = "9" ] && [ -z "$M_UNRECORDED" ]; then
  gauntlet_stage migrate pass "$SUMMARY_LINE; all 9 taggable resources carry tofu-estate=$ESTATE, read back through ARM; the 2 skipped are the subnet and the NSG rule, which have no tags argument, and each has its record in the record store, a blob container on the same floci-az ($REC_ACCOUNT/adopted, $(gauntlet_record_envelope_count "$M_RECORDS") record envelopes)"
else
  gauntlet_stage migrate fail "live-import -approve: ${SUMMARY_LINE:-no summary line}; $TAGGED of 9 taggable resources carry tofu-estate=$ESTATE through ARM; skipped (want only $WANT_SKIPPED): \"$(gauntlet_live_import_skips <<< "$APPROVE_OUT")\"; no record in $REC_ACCOUNT/adopted for:${M_UNRECORDED:- none}; first Error: line: \"$(gauntlet_first_error_line <<< "$APPROVE_OUT")\""
fi

# ── 3. test_plan: replan from nothing ────────────────────────────────────
gauntlet_begin_stage test_plan
log "=== 3. test_plan: choudoufu plan with no state file, identities read through ARM ==="
PLAN_OUT="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
IDS_MISSING="$(arm_read a | python3 -c '
import json, sys
inv = json.load(sys.stdin)
print(" ".join(rid for rid, r in sorted(inv.items()) if r is None or r["id"].lower() != rid))')" \
  || fail "could not read A's resources through ARM"
if [ "$PLAN_RC" = "0" ] && grep -q "No changes." <<< "$PLAN_OUT" && [ -z "$IDS_MISSING" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all 11 ARM ids answer a GET with the same id, compared case-insensitively"
else
  gauntlet_stage test_plan fail "the plan with no state file is not empty or failed ($(gauntlet_plan_line <<< "$PLAN_OUT"); first Error: line: \"$(gauntlet_first_error_line <<< "$PLAN_OUT")\") or an id did not answer as itself (${IDS_MISSING:-none})"
  log "  adopting through choudoufu's own apply so the day-2 stages below run on a marked estate"
  ADOPT_OUT="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$ADOPT_OUT" | tail -20; fail "the adopting apply failed; nothing below would measure day-2 behaviour"; }
  REPLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the adopting apply failed"
  grep -q "No changes." <<< "$REPLAN" || { printf '%s\n' "$REPLAN" | tail -20; fail "the replan after the adopting apply is not empty; nothing below would measure day-2 behaviour"; }
fi

# ── 4. test_apply: no-op apply, marker count unchanged ───────────────────
gauntlet_begin_stage test_apply
log "=== 4. test_apply: apply the empty plan; the marked-resource count must not move ==="
BEFORE_N="$(count_a)" || fail "could not count A's marked resources"
NOOP_OUT="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$NOOP_OUT" | tail -20; fail "the no-op apply failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NOOP_OUT" || { printf '%s\n' "$NOOP_OUT" | tail -5; fail "the no-op apply changed something"; }
AFTER_N="$(count_a)" || fail "could not count A's marked resources"
[ "$BEFORE_N" = "$AFTER_N" ] || fail "marked-resource count moved across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); resources carrying tofu-estate=$ESTATE unchanged at $BEFORE_N, read through ARM"

# ── 5. drift_reconverge: one resource tampered out of band ───────────────
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: an ARM PUT adds a tag to the VNet on A and on B; stock's plan on B is the oracle ==="
tag_set a "$VNET_ID" "$NET_API" drift yes || fail "could not tamper the VNet on A"
tag_set b "$VNET_ID" "$NET_API" drift yes || fail "could not tamper the VNet on B"
if [ "${BREAK:-}" = "1" ]; then
  tag_set a "$PIP_ID" "$NET_API" drift yes || fail "BREAK: could not tamper the public IP on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -q "azurerm_virtual_network.app" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name azurerm_virtual_network.app"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two resources were tampered but the plan still proposes exactly one change - the single-resource assertion is not load-bearing"
  fi
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with two resources tampered the single-resource assertion correctly fails to hold ($(gauntlet_plan_line <<< "$DRIFT_PLAN")); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "azurerm_virtual_network.app" <<< "$DRIFT_PLAN" || fail "the plan does not name azurerm_virtual_network.app"
  RECONV="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one resource"
  VNET_JSON="$(on_a gauntlet_arm GET "$VNET_ID?api-version=$NET_API")" || fail "could not read the VNet through ARM after reconverging"
  grep -q '"drift"' <<< "$VNET_JSON" && fail "the VNet still carries the drift tag after reconverging: $VNET_JSON"
  gauntlet_stage drift_reconverge pass "a tag added to the VNet with an ARM GET and PUT; choudoufu proposed exactly azurerm_virtual_network.app (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle container for the same tamper; apply changed 1 and the tag is gone, read through ARM. BREAK=1 tampers a second resource and the single-resource assertion correctly fails"
fi

# ── 12. plan_approval: plan -out, the world moves, apply refuses ─────────
gauntlet_begin_stage plan_approval
log "=== 12. plan_approval: a saved plan, an out-of-band tag, a refusal; then the same file applies once the world is back ==="
REVIEWED='  tags = { reviewed = "yes" }'
write_config "$ADOPTED" live app 2 "$REVIEWED"
write_config "$ORACLE" stock app 2 "$REVIEWED"
P_PLAN="$(tofu_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
tag_set a "$SHARD0_ID" "$MI_API" stray yes || fail "could not move the world (a stray tag on shard 0) on A"
P_APPLY="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  tag_set a "$SHARD0_ID" "$MI_API" stray - || fail "BREAK_APPROVAL: could not put the world back"
  ( tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  APP_JSON="$(on_a gauntlet_arm GET "$APP_ID?api-version=$MI_API")" || fail "could not read the identity through ARM after the refusal"
  grep -q '"reviewed"' <<< "$APP_JSON" && fail "the identity gained the reviewed tag despite the refusal: $APP_JSON"
  tag_set a "$SHARD0_ID" "$MI_API" stray - || fail "could not put the world back"
  P_APPLY2="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one resource"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the identity gains tags reviewed=yes); the world then moved out of band (a stray tag on shard 0, an ARM PUT) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the tag removed the identical file applied, 0 added, 1 changed, 0 destroyed; stock's own planfile applied on the oracle container in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 6. day2_rename: a moved block rewrites tofu-address in place ─────────
gauntlet_begin_stage day2_rename
log "=== 6. day2_rename: the identity's block renamed app -> team with a moved block ==="
if [ "${BREAK:-}" = "1" ]; then
  write_config "$ADOPTED" live team 2 "$REVIEWED" refaz-team; moved_block >> "$ADOPTED/main.tf"
  R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)"
  if grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$R_PLAN" || grep -q "No changes." <<< "$R_PLAN"; then
    fail "BREAK=1: the identity's own name changed and the plan reads zero churn - the assertion is not load-bearing"
  fi
  write_config "$ADOPTED" live team 2 "$REVIEWED"; moved_block >> "$ADOPTED/main.tf"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the real rename did not apply"
  gauntlet_stage day2_rename pass "BREAK=1 control: changing the identity's own name is an identity change and the zero-churn assertion correctly fails ($(gauntlet_plan_line <<< "$R_PLAN")); the real rename applied afterwards"
else
  write_config "$ADOPTED" live team 2 "$REVIEWED"; moved_block >> "$ADOPTED/main.tf"
  write_config "$ORACLE" stock team 2 "$REVIEWED"; moved_block >> "$ORACLE/main.tf"
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's plan of the moved block failed on B"
  grep -qE "Plan: 0 to add, [01] to change, 0 to destroy|No changes" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan of the moved block on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the moved block on B"
  R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the plan of the moved block failed"; }
  grep -qE "Plan: 0 to add, 1 to change, 0 to destroy" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved block does not plan exactly one in-place change (the tofu-address tag)"; }
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the rename did not apply"
  ADDR="$(on_a gauntlet_arm GET "$APP_ID?api-version=$MI_API" | python3 -c 'import json,sys; t=json.load(sys.stdin).get("tags") or {}; print(next((v for k,v in t.items() if k.lower()=="tofu-address"), ""))')"
  [ "$ADDR" = "azurerm_user_assigned_identity.team" ] || fail "the identity's tofu-address reads '$ADDR' after the rename, want azurerm_user_assigned_identity.team"
  gauntlet_stage day2_rename pass "a moved block renamed the identity's block app -> team: 0 add, 1 in-place change, 0 destroy, the change being the tofu-address tag, which reads azurerm_user_assigned_identity.team through ARM afterwards; stock with the same moved block planned zero churn on the oracle. BREAK=1 changes the identity's own name and the zero-churn assertion correctly fails"
fi

# ── 7. day2_remove: a deleted block's resource is destroyed ──────────────
# The public IP takes tags, so its removal is found by its marker through
# the Resource Graph sweep and by nothing else: the record leg skips a type
# that takes tags. floci-az runs the sweep's query since dfa33ca
# (lex00/floci-az#11), so the destroy is expected. A plan whose sweep still
# failed proposes no destroy, and the stage fails naming the warning
# (gauntlet_azure_sweep_note). The run goes on: the public IP is removed by
# hand on A and by stock on B, so the stages below start from the shape
# they expect.
gauntlet_begin_stage day2_remove
log "=== 7. day2_remove: the public IP's block is deleted ==="
drop_pip() { python3 -c '
import re, sys
p = sys.argv[1]; s = open(p).read()
s = re.sub(r"resource \"azurerm_public_ip\" \"app\" \{.*?\n\}\n", "", s, flags=re.S)
open(p, "w").write(s)' "$1"; }
write_config "$ADOPTED" live team 2 "$REVIEWED"; moved_block >> "$ADOPTED/main.tf"
write_config "$ORACLE" stock team 2 "$REVIEWED"; moved_block >> "$ORACLE/main.tf"
if [ "${BREAK_REMOVE:-}" != "1" ]; then drop_pip "$ADOPTED/main.tf"; drop_pip "$ORACLE/main.tf"; fi
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)"
M_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan without the public IP's block failed"; }
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  grep -q "azurerm_public_ip.app will be destroyed" <<< "$M_PLAN" && fail "BREAK_REMOVE=1: the block is kept and a destroy is still proposed"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the block kept no destroy is proposed ($(gauntlet_plan_line <<< "$M_PLAN")), so 'a destroy is proposed' correctly fails"
else
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan without the block on B is not one destroy"; }
  if grep -q "azurerm_public_ip.app will be destroyed" <<< "$M_PLAN" && grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$M_PLAN"; then
    ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the destroying apply failed"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the removal on B"
    gone_a "$PIP_ID" "$NET_API" || fail "ARM still answers for the public IP after its block was removed and applied"
    gauntlet_stage day2_remove pass "the public IP's block deleted: choudoufu planned exactly azurerm_public_ip.app's destroy (0 add, 0 change, 1 destroy), as stock planned on the oracle, and after the apply ARM answers 404 for it"
  else
    printf '%s\n' "$M_PLAN" | tail -20
    gauntlet_stage day2_remove fail "the plan without the public IP's block does not destroy exactly azurerm_public_ip.app ($(gauntlet_plan_line <<< "$M_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$M_PLAN")\")$(gauntlet_azure_sweep_note <<< "$M_PLAN")"
    log "  removing the public IP by hand on A and through stock on B, so the stages below start from the shape they expect"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the removal on B"
    on_a gauntlet_arm DELETE "$PIP_ID?api-version=$NET_API" >/dev/null || fail "could not delete the public IP on A through ARM after day2_remove failed"
  fi
fi

# ── 8. day2_count: scale the shard identities down and back up ───────────
# As day2_remove: the identity that leaves the count is found only by the
# sweep, which floci-az serves since dfa33ca (lex00/floci-az#11). Should the
# scale-down still plan no destroy, the stage fails naming the warning, and
# the configuration goes back to two shards with both still standing, the
# shape the teardown expects.
gauntlet_begin_stage day2_count
log "=== 8. day2_count: shard count 2 -> 1 -> 2 ==="
current_block() { write_config "$1" "$2" team "$3" "$REVIEWED"; moved_block >> "$1/main.tf"; drop_pip "$1/main.tf"; }
current_block "$ADOPTED" live 1
C_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  grep -q 'azurerm_user_assigned_identity.shard\[0\] will be destroyed' <<< "$C_PLAN" && fail "BREAK_COUNT=1: shard[0] is planned for destroy - the wrong instance"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: the assertion that shard[0] is destroyed correctly fails; the plan destroys $(grep -oE 'azurerm_user_assigned_identity.shard\[[0-9]\] will be destroyed' <<< "$C_PLAN" | head -1)"
elif ! { grep -q 'azurerm_user_assigned_identity.shard\[1\] will be destroyed' <<< "$C_PLAN" && grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN"; }; then
  printf '%s\n' "$C_PLAN" | tail -20
  gauntlet_stage day2_count fail "the scale-down 2 -> 1 does not destroy exactly azurerm_user_assigned_identity.shard[1] ($(gauntlet_plan_line <<< "$C_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$C_PLAN")\")$(gauntlet_azure_sweep_note <<< "$C_PLAN")"
  log "  shard[1] is still standing; the configuration goes back to two shards for the stages below"
  current_block "$ADOPTED" live 2
else
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-down apply failed"
  gone_a "$SHARD1_ID" "$MI_API" || fail "ARM still answers for shard 1 after the scale-down"
  gone_a "$SHARD0_ID" "$MI_API" && fail "shard 0 is gone after the scale-down; the survivor did not keep its identity"
  current_block "$ADOPTED" live 2
  U_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the scale-up plan failed"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up is not exactly one create"; }
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-up apply failed"
  gauntlet_stage day2_count pass "count 2 -> 1 destroyed exactly shard[1] (ARM answers 404 for it, shard 0 kept its id); 1 -> 2 created exactly one, at shard[1]"
fi

# ── helpers the last four stages share ───────────────────────────────────
# tag_a <id> <api> <key>: one tag's value on A, the key compared
# case-insensitively, or nothing.
tag_a() {
  on_a gauntlet_arm GET "$1?api-version=$2" 2>/dev/null | KEY="$3" python3 -c '
import json, os, sys
try:
    t = json.load(sys.stdin).get("tags") or {}
except ValueError:
    t = {}
print(next((v for k, v in t.items() if k.lower() == os.environ["KEY"].lower()), ""))'
}
mi_id() { printf '%s/resourceGroups/%s/providers/%s/%s' "$SUB" "$RG" "$MI" "$1"; }
exists_a() { on_a gauntlet_arm GET "$1?api-version=$2" >/dev/null 2>&1; }
plan_line() { grep -E '^Plan:|^No changes' <<< "$1" | head -1 | sed 's/\.$//'; }
# mi_block <block> <name> [extra]: an identity in the estate's group.
mi_block() {
  cat <<EOT
resource "azurerm_user_assigned_identity" "$1" {
  name                = "$2"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
${3:-}
}
EOT
}
CBD='  lifecycle {
    create_before_destroy = true
  }'

# ── 9. day2_replace: a create_before_destroy identity whose name changes ──
#
# The shape tools/gauntlet/stages.go's flociAzNotes names, as on kind
# (gauntlet_kind_day2_replace): an ARM name is unique within its resource
# group, so the replacement create_before_destroy exists for is a rename.
# The old object carries the block's tofu-address, and its record is what
# binds it to the block (internal/live/projection's record-first read, the
# record verified against that marker), so the plan is stock's replace,
# create first. Stock applies the same rename on B for real: the apply
# order is the oracle, and only an apply has one. The block lives in a file
# of its own and is removed from both roots at the end.
gauntlet_begin_stage day2_replace
log "=== 9. day2_replace: azurerm_user_assigned_identity.hashed renamed refaz-cfg-a -> refaz-cfg-b under create_before_destroy ==="
R_BLOCK="azurerm_user_assigned_identity.hashed"
replace_tf() { mi_block hashed "refaz-cfg-$2" "$CBD" > "$1/day2_replace.tf"; }
# replace_order <apply-log> <where>: the new object's create completes
# before the deposed old object's destroy starts.
replace_order() {
  R_CREATED="$(grep -nE "${R_BLOCK//./\\.}: Creation complete" <<< "$1" | head -1 | cut -d: -f1)"
  R_DESTROYING="$(grep -nE "${R_BLOCK//./\\.} \(deposed object [^)]*\): Destroying" <<< "$1" | head -1 | cut -d: -f1)"
  [ -n "$R_CREATED" ] && [ -n "$R_DESTROYING" ] || { printf '%s\n' "$1" | tail -20; fail "$2: the apply log has no create complete (${R_CREATED:-none}) or no deposed destroy (${R_DESTROYING:-none}) for $R_BLOCK"; }
  [ "$R_CREATED" -lt "$R_DESTROYING" ] || { printf '%s\n' "$1" | tail -20; fail "$2: the old object's destroy started (log line $R_DESTROYING) before the new object's create completed (line $R_CREATED)"; }
}
# is_replace <plan> <where>: stock's plan shape for the rename.
is_replace() {
  grep -qF "Plan: 1 to add, 0 to change, 1 to destroy." <<< "$1" || { printf '%s\n' "$1" | tail -20; fail "$2: the rename is not one add and one destroy: $(plan_line "$1")"; }
  grep -qE "# ${R_BLOCK//./\\.} must be replaced" <<< "$1" || { printf '%s\n' "$1" | grep -E '# ' | head -5; fail "$2: $R_BLOCK is not planned as a replace"; }
  grep -qF "+/- create replacement and then destroy" <<< "$1" || fail "$2: the replace is not create-first"
}
CFG_A="$(mi_id refaz-cfg-a)"; CFG_B="$(mi_id refaz-cfg-b)"
replace_tf "$ORACLE" a; replace_tf "$ADOPTED" a
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's apply of refaz-cfg-a failed on B"
R_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the apply of refaz-cfg-a failed"; }
grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." <<< "$R_APPLY" || fail "the apply of refaz-cfg-a did not add exactly one resource"
[ "$(tag_a "$CFG_A" "$MI_API" tofu-address)" = "$R_BLOCK" ] || fail "refaz-cfg-a carries tofu-address '$(tag_a "$CFG_A" "$MI_API" tofu-address)', want $R_BLOCK: nothing would bind it on the rename"

replace_tf "$ORACLE" b; replace_tf "$ADOPTED" b
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's rename plan failed on B"; }
is_replace "$O_PLAN" "stock on B (the oracle)"
R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the rename plan failed: $(gauntlet_first_error_line <<< "$R_PLAN")"; }
is_replace "$R_PLAN" "choudoufu"
O_APPLY="$(stock_b apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$O_APPLY" | tail -10; fail "stock's rename apply failed on B"; }
replace_order "$O_APPLY" "stock on B (the oracle)"
R_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the rename apply failed"; }
grep -qF "Apply complete! Resources: 1 added, 0 changed, 1 destroyed." <<< "$R_APPLY" || fail "the rename apply did not add one resource and destroy one"
replace_order "$R_APPLY" "choudoufu"
exists_a "$CFG_B" "$MI_API" || fail "refaz-cfg-b does not answer through ARM after the replace"
exists_a "$CFG_A" "$MI_API" && fail "refaz-cfg-a still answers through ARM after the replace"
[ "$(tag_a "$CFG_B" "$MI_API" tofu-address)" = "$R_BLOCK" ] || fail "refaz-cfg-b carries tofu-address '$(tag_a "$CFG_B" "$MI_API" tofu-address)', want $R_BLOCK"

if [ "${BREAK_REPLACE:-}" = "1" ]; then
  # The old object back, carrying the block's marker. On the Resource Graph
  # leg an object at an id the configuration no longer computes, which the
  # record does not name, is reported (a displaced marker), never destroyed
  # (internal/live/discovery/displaced.go): the Break line's "must report
  # rather than proposing nothing" is that warning, or any proposed change.
  on_a gauntlet_arm PUT "$CFG_A?api-version=$MI_API" "{\"location\":\"eastus\",\"tags\":{\"tofu-estate\":\"$ESTATE\",\"tofu-address\":\"$R_BLOCK\"}}" >/dev/null \
    || fail "BREAK_REPLACE: could not recreate refaz-cfg-a through ARM"
  B_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$B_PLAN" | tail -20; fail "BREAK_REPLACE: the plan over the recreated refaz-cfg-a failed"; }
  if grep -q "No changes." <<< "$B_PLAN" && ! grep -qF "Live resource displaced from the address it is marked for" <<< "$B_PLAN"; then
    fail "BREAK_REPLACE=1: the old object is back, carrying the block's address, and the plan proposes nothing and reports nothing - the empty-plan assertion is not load-bearing"
  fi
  B_SAID="$(plan_line "$B_PLAN")"; grep -qF "Live resource displaced" <<< "$B_PLAN" && B_SAID="$B_SAID, with the warning \"Live resource displaced from the address it is marked for\""
  on_a gauntlet_arm DELETE "$CFG_A?api-version=$MI_API" >/dev/null || fail "BREAK_REPLACE: could not delete the recreated refaz-cfg-a"
else
  R_REPLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the replace failed"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the replace is not empty"; }
fi

rm -f "$ORACLE/day2_replace.tf" "$ADOPTED/day2_replace.tf"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's removal of the replace block failed on B"
R_RM="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_RM" | tail -20; fail "removing the replace block failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed." <<< "$R_RM" || fail "removing the replace block did not destroy exactly refaz-cfg-b"
exists_a "$CFG_B" "$MI_API" && fail "refaz-cfg-b still answers through ARM after its block was removed"
if [ "${BREAK_REPLACE:-}" = "1" ]; then
  gauntlet_stage day2_replace pass "BREAK_REPLACE=1 control: after the create_before_destroy rename refaz-cfg-a -> refaz-cfg-b, refaz-cfg-a was recreated through ARM carrying tofu-estate=$ESTATE and tofu-address=$R_BLOCK, and the next plan was not silent about it ($B_SAID), so the empty-plan assertion is load-bearing; the real replan check is skipped"
else
  gauntlet_stage day2_replace pass "a create_before_destroy identity whose name changes (refaz-cfg-a -> refaz-cfg-b) plans as stock's replace: '$R_BLOCK must be replaced', +/- create replacement and then destroy, 1 add and 1 destroy, because refaz-cfg-a's record binds it to the block and its tofu-address confirms the record. At -parallelism=1 the apply log shows refaz-cfg-b's creation complete (line $R_CREATED) before refaz-cfg-a's deposed destroy starts (line $R_DESTROYING), the order stock's own apply of the same rename shows on the oracle container; ARM answers 404 for refaz-cfg-a and refaz-cfg-b carries the block's tofu-address, and the next plan is empty. The block is removed from both roots afterwards. BREAK_REPLACE=1 recreates refaz-cfg-a carrying the block's marker and the next plan correctly is not silent"
fi

# ── 10. day2_crash: SIGTERM inside a rename, and between two creates ─────
#
# Two windows, as the kind and EKS estates interrupt them, each killed by
# the engine's own hook: internal/command/apply_e2etesting_crash.go
# self-delivers SIGTERM synchronously inside the -parallelism=1 graph
# walker the instant TOFU_E2E_APPLY_RESOURCE_INTERRUPT's address finishes a
# create, so nothing else can be dispatched until it returns.
#
# The create_before_destroy rename window first (the stage's own Proves,
# and reference-ec2-vpc's): the new identity created, the old one still
# standing with the block's tofu-address, the record holding it as the
# address's deposed object. The recovery plan destroys it as the deposed
# object, stock's own plan in this position, because the Resource Graph leg
# hands the projection the record's deposed half
# (internal/live/discovery/displaced.go's verdictDeposed). What must agree
# is the end state stock's replace leaves: exactly the new object, the old
# one gone, the record's deposed entry cleared, an empty replan.
#
# Then the window flociAzNotes names: an apply creating two identities,
# killed after the first, and the next plan must propose exactly the
# remainder, binding the first at its declared id. Stock on B, walked into
# the same position by applying the first alone, is the oracle.
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM inside a create_before_destroy rename, then between two creates ==="
X_BLOCK="azurerm_user_assigned_identity.crash_rename"
X_OLD="$(mi_id refaz-crash-a)"; X_NEW="$(mi_id refaz-crash-b)"
crash_rename_tf() { mi_block crash_rename "refaz-crash-$1" "$CBD" > "$ADOPTED/day2_crash_rename.tf"; }
# record_deposed <records-dir> <address>: the names the record's deposed
# entries hold, one per line.
record_deposed() {
  local f
  f="$(gauntlet_record_file "$1" "$2")" || return 1
  python3 - "$f" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
for v in (d.get("deposed") or {}).values():
    i = v.get("identity") or {}
    print((i.get("attrs") or {}).get("name") or i.get("import_id", ""))
PY
}
crash_rename_tf a
X_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$X_APPLY" | tail -20; fail "the apply of refaz-crash-a failed"; }
[ "$(tag_a "$X_OLD" "$MI_API" tofu-address)" = "$X_BLOCK" ] || fail "refaz-crash-a carries tofu-address '$(tag_a "$X_OLD" "$MI_API" tofu-address)', want $X_BLOCK"
crash_rename_tf b
# A non-zero exit is the normal outcome: the process was killed.
X_OUT="$(cd "$ADOPTED" && on_a env TOFU_E2E_APPLY_RESOURCE_INTERRUPT="$X_BLOCK" "$TOFU_CRASH" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash_rename.log"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted rename apply exited 0, so the engine's self-signal never landed and nothing was interrupted"; }
grep -qE "${X_BLOCK//./\\.}: Creation complete" <<< "$X_OUT" || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply never completed refaz-crash-b's create, so the kill landed before the window opened"; }
grep -qE "${X_BLOCK//./\\.} \(deposed object [^)]*\): Destroying" <<< "$X_OUT" && { printf '%s\n' "$X_OUT" | tail -20; fail "the deposed destroy of refaz-crash-a was dispatched before the kill, so the window was closed when it landed"; }
[ "$(tag_a "$X_OLD" "$MI_API" tofu-address)" = "$X_BLOCK" ] && [ "$(tag_a "$X_NEW" "$MI_API" tofu-address)" = "$X_BLOCK" ] \
  || fail "after the interrupt refaz-crash-a carries '$(tag_a "$X_OLD" "$MI_API" tofu-address)' and refaz-crash-b carries '$(tag_a "$X_NEW" "$MI_API" tofu-address)'; the window is both carrying $X_BLOCK"
X_RECORDS="$(records_copy adopted)" || fail "could not copy the record store's adopted container after the interrupted rename"
X_DEP="$(record_deposed "$X_RECORDS" "$X_BLOCK" | tr '\n' ' ' | sed 's/ $//')" || fail "the interrupted rename apply left no record for $X_BLOCK in $REC_ACCOUNT/adopted"
[ "$X_DEP" = "refaz-crash-a" ] || fail "the record's deposed entries for $X_BLOCK are [${X_DEP:-none}], want [refaz-crash-a]: the interrupted apply's write-back did not record the old object as deposed"
X_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the plan after the interrupted rename failed: $(gauntlet_first_error_line <<< "$X_PLAN")"; }
X_LINE="$(plan_line "$X_PLAN")"
if [ "${BREAK_CRASH:-}" = "1" ]; then
  grep -q "No changes." <<< "$X_PLAN" && { printf '%s\n' "$X_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted create_before_destroy rename is empty, so the window's check is not load-bearing"; }
  log "  BREAK_CRASH=1, rename window: caught - the plan after the interrupt proposes work ($X_LINE)"
fi
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$X_PLAN" && grep -qE "# ${X_BLOCK//./\\.} \(deposed object [^)]*\) will be destroyed" <<< "$X_PLAN" \
  || { printf '%s\n' "$X_PLAN" | grep -E '^Plan:|^No changes| will be | must be |^Warning' | head -10; fail "the plan after the interrupted rename is $X_LINE, not the one deposed-object destroy that leaves stock's end state"; }
X_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$X_APPLY" | tail -20; fail "the recovery apply after the interrupted rename failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 1 destroyed." <<< "$X_APPLY" || { printf '%s\n' "$X_APPLY" | tail -10; fail "the recovery apply did not destroy exactly one resource"; }
grep -qiF "Destroying... [id=$X_OLD]" <<< "$X_APPLY" || { printf '%s\n' "$X_APPLY" | grep -E 'Destroying' | head -3; fail "the recovery apply's one destroy is not refaz-crash-a"; }
exists_a "$X_OLD" "$MI_API" && fail "refaz-crash-a still answers through ARM after the recovery apply"
[ "$(tag_a "$X_NEW" "$MI_API" tofu-address)" = "$X_BLOCK" ] || fail "refaz-crash-b is not there carrying $X_BLOCK after the recovery apply"
X_RECORDS="$(records_copy adopted)" || fail "could not copy the record store's adopted container after the recovery apply"
X_DEP="$(record_deposed "$X_RECORDS" "$X_BLOCK" | tr '\n' ' ' | sed 's/ $//')" || fail "the record for $X_BLOCK is gone after the recovery apply"
[ -z "$X_DEP" ] || fail "the record still holds deposed [$X_DEP] for $X_BLOCK after the recovery apply destroyed it"
X_REPLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_REPLAN" | tail -20; fail "the replan after the rename window's recovery failed"; }
grep -q "No changes." <<< "$X_REPLAN" || { printf '%s\n' "$X_REPLAN" | grep -E '^Plan:| will be | must be ' | head -10; fail "the replan after the rename window's recovery is not empty"; }
rm -f "$ADOPTED/day2_crash_rename.tf"
X_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$X_APPLY" | tail -20; fail "removing the rename window's block failed"; }
exists_a "$X_NEW" "$MI_API" && fail "refaz-crash-b still answers through ARM after its block was removed"
if [ "${BREAK_CRASH:-}" = "1" ]; then
  CRASH_RENAME_DETAIL="The create_before_destroy rename window is interrupted too: BREAK_CRASH=1 asserted nothing is proposed after its real interrupt and that correctly failed ($X_LINE) before the window was recovered."
else
  CRASH_RENAME_DETAIL="The create_before_destroy rename window is interrupted first (the stage's own Proves): $X_BLOCK renamed refaz-crash-a -> refaz-crash-b was killed by the engine's own hook the instant the new identity's create committed (exit $X_RC), at -parallelism=1, leaving both carrying the block's tofu-address and the record in $REC_ACCOUNT/adopted holding refaz-crash-a as the address's deposed object. The next plan was stock's own in that position, $X_LINE, the deposed object's destroy; one apply destroyed refaz-crash-a (ARM answers 404), left refaz-crash-b carrying the block's address, cleared the record's deposed entry, and the replan is empty."
fi

# The two-resource window. The pair lives in day2_crash.tf in each root;
# crash_second depends on crash_first, so under -parallelism=1 the walker
# cannot reach it before the hook returns.
crash_pair() { # first|both
  mi_block crash_first refaz-crash-first
  if [ "$1" = "both" ]; then
    echo
    mi_block crash_second refaz-crash-second '  depends_on          = [azurerm_user_assigned_identity.crash_first]'
  fi
}
C_FIRST="$(mi_id refaz-crash-first)"; C_SECOND="$(mi_id refaz-crash-second)"
crash_pair first > "$ORACLE/day2_crash.tf"
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash_first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash_first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash_first apply failed on B"
crash_pair both > "$ORACLE/day2_crash.tf"
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" && grep -q 'azurerm_user_assigned_identity.crash_second will be created' <<< "$O_REMAINDER" \
  || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add of crash_second - the oracle for this stage is not what it should be"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash_first alone plans exactly one add (crash_second) for the remainder, and applies it"

BEFORE_N="$(count_a)" || fail "could not count A's marked resources before the crash pair"
crash_pair both > "$ADOPTED/day2_crash.tf"
P_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds - there is no two-resource apply to interrupt"; }
P_RECORDS="$(records_copy adopted)" || fail "could not copy the record store's adopted container before the interrupted apply"
P_BEFORE="$(gauntlet_record_envelope_count "$P_RECORDS")"
P_OUT="$(cd "$ADOPTED" && on_a env TOFU_E2E_APPLY_RESOURCE_INTERRUPT="azurerm_user_assigned_identity.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; P_RC=$?
printf '%s\n' "$P_OUT" > "$WORK/day2_crash.log"
[ "$P_RC" -ne 0 ] || { printf '%s\n' "$P_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted"; }
exists_a "$C_FIRST" "$MI_API" || { printf '%s\n' "$P_OUT" | tail -20; fail "refaz-crash-first does not answer through ARM after the interrupted apply - the kill landed before the create committed"; }
exists_a "$C_SECOND" "$MI_API" && { printf '%s\n' "$P_OUT" | tail -20; fail "refaz-crash-second answers through ARM after the interrupted apply - the kill landed after both creates"; }
[ "$(tag_a "$C_FIRST" "$MI_API" tofu-estate | tr '[:upper:]' '[:lower:]')" = "$ESTATE" ] \
  || fail "refaz-crash-first was created by the interrupted apply but does not carry tofu-estate=$ESTATE - the marker the rerun is supposed to find is not there"
P_RECORDS="$(records_copy adopted)" || fail "could not copy the record store's adopted container after the interrupted apply"
gauntlet_record_file "$P_RECORDS" "azurerm_user_assigned_identity.crash_first" >/dev/null \
  || fail "the interrupted apply created refaz-crash-first but wrote no record for azurerm_user_assigned_identity.crash_first in $REC_ACCOUNT/adopted"
P_AFTER="$(gauntlet_record_envelope_count "$P_RECORDS")"
[ "$P_AFTER" = "$((P_BEFORE + 1))" ] || fail "records went $P_BEFORE -> $P_AFTER across the interrupted apply, want exactly one more - the record for the one resource it did create, and nothing else"
log "  refaz-crash-first exists and is marked; refaz-crash-second does not; records $P_BEFORE -> $P_AFTER"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  tag_set a "$C_FIRST" "$MI_API" tofu-estate - || fail "BREAK_CRASH_UNBOUND: could not strip tofu-estate off refaz-crash-first"
  log "  BREAK_CRASH_UNBOUND=1: stripped tofu-estate off refaz-crash-first through ARM"
fi
C_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)"; C_RC=$?
C_LINE="$(plan_line "$C_PLAN")"
recovered() {
  [ "$C_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$C_PLAN" || return 1
  grep -qE '^[[:space:]]*# azurerm_user_assigned_identity\.crash_second will be created' <<< "$C_PLAN" || return 1
  local proposed
  proposed="$(grep -E '^[[:space:]]*# .* (will|must) be' <<< "$C_PLAN")"
  grep -q 'crash_first' <<< "$proposed" && return 1
  return 0
}
pair_gone() { # removes the pair from both roots and applies both
  rm -f "$ADOPTED/day2_crash.tf" "$ORACLE/day2_crash.tf"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's removal of the crash pair failed on B"
  P_RM="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$P_RM" | tail -20; fail "removing the crash pair failed"; }
}
if [ "${BREAK_CRASH:-}" = "1" ]; then
  [ "$C_RC" -eq 0 ] || { printf '%s\n' "$C_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $C_RC"; }
  grep -qF "No changes." <<< "$C_PLAN" && { printf '%s\n' "$C_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-resource apply came back empty, so this stage's own check is not load-bearing"; }
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
  pair_gone
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($C_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped. $CRASH_RENAME_DETAIL"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  if recovered; then
    printf '%s\n' "$C_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with refaz-crash-first carrying no tofu-estate - it is not measuring whether the crashed-out resource was bound at all"
  fi
  on_a gauntlet_arm DELETE "$C_FIRST?api-version=$MI_API" >/dev/null || fail "BREAK_CRASH_UNBOUND: could not delete the unmarked refaz-crash-first"
  pair_gone
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with tofu-estate stripped off the identity the interrupted apply created - the unrecovered run this stage exists to catch - the recovery check correctly fails to hold ($C_LINE, exit $C_RC); the real check is skipped. $CRASH_RENAME_DETAIL"
else
  if ! recovered; then
    printf '%s\n' "$C_PLAN" | grep -E '^Plan:|^No changes|will be|must be|^Error' | head -20
    gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of azurerm_user_assigned_identity.crash_first and the create of crash_second is not exactly the remainder: ${C_LINE:-no plan line} (exit $C_RC; first Error: line: \"$(gauntlet_first_error_line <<< "$C_PLAN")\"). refaz-crash-first answers through ARM carrying tofu-estate=$ESTATE and refaz-crash-second does not; stock, walked into the same position on the oracle container, plans exactly one add (crash_second). $CRASH_RENAME_DETAIL"
  else
    C_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$C_APPLY" | tail -20; fail "the recovery apply exited non-zero"; }
    grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$C_APPLY" || { printf '%s\n' "$C_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining resource"; }
    exists_a "$C_SECOND" "$MI_API" || fail "refaz-crash-second does not answer through ARM after the recovery apply"
    exists_a "$C_FIRST" "$MI_API" || fail "refaz-crash-first is gone after the recovery apply - the recovery replaced the resource the crash created instead of binding it"
    C_REPLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_REPLAN" | tail -30; fail "the replan after the recovery failed: $(gauntlet_first_error_line <<< "$C_REPLAN")"; }
    grep -q "No changes." <<< "$C_REPLAN" || { printf '%s\n' "$C_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
    AFTER_N="$(count_a)" || fail "could not count A's marked resources after the recovery"
    [ "$AFTER_N" = "$BEFORE_N" ] || fail "the estate's own marked resources went $BEFORE_N -> $AFTER_N across the crash pair"
    pair_gone
    grep -qF "Apply complete! Resources: 0 added, 0 changed, 2 destroyed" <<< "$P_RM" || fail "removing the crash pair did not destroy exactly its two identities"
    gauntlet_stage day2_crash pass "$CRASH_RENAME_DETAIL Then an apply creating two identities was interrupted by a real SIGTERM (exit $P_RC), delivered by the engine itself inside the -parallelism=1 graph walker the instant azurerm_user_assigned_identity.crash_first's create committed; crash_second depends on crash_first, so the walker cannot have reached it - ARM answers for refaz-crash-first, carrying tofu-estate=$ESTATE from its create, and 404 for refaz-crash-second. The interrupted apply wrote exactly one record (envelopes $P_BEFORE -> $P_AFTER in $REC_ACCOUNT/adopted), crash_first's. The next plan proposed exactly the remainder ($C_LINE, crash_second created) and nothing for crash_first, which it bound at its declared ARM id - matching stock's own plan from the same position on the oracle container, reached by applying crash_first alone; the recovery apply added exactly one identity, both answer through ARM, the plan after it is empty, and the estate's own marked count is unchanged at $AFTER_N. The pair is removed from both roots afterwards. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 strips tofu-estate off refaz-crash-first and the same recovery check correctly fails"
  fi
fi
gauntlet_end_stage

# ── 11. day2_teardown: destroy everything, nothing left marked ───────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy ==="
T_OUT="$(tofu_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
LEFT="$(count_a)" || fail "could not count A's marked resources after the destroy"
[ "$LEFT" = "0" ] || fail "$LEFT resource(s) still carry tofu-estate=$ESTATE after apply -destroy"
gone_a "${SUB}/resourceGroups/${RG}" "2021-04-01" || fail "the resource group still answers after apply -destroy"
gauntlet_stage day2_teardown pass "apply -destroy removed every resource in one apply ($(grep -oE 'Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$T_OUT" | head -1)); nothing carries tofu-estate=$ESTATE and the resource group answers 404"

# ── 13. greenfield: the same shape applied fresh, compared with stock ────
gauntlet_begin_stage greenfield
log "=== 13. greenfield: choudoufu applies the shape on an empty container A ==="
REC_CONTAINER=green write_config "$GREEN" live
( tofu_a "$GREEN" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(tofu_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "the greenfield apply failed"; }
grep -qF "Apply complete! Resources: 11 added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield apply did not add exactly 11 resources"; }
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="/publicipaddresses/refaz-pip"
inventory a > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ -n "$DROP" ]; then
  inventory a "$DROP" > "$WORK/inventory.expected.json"
  cmp -s "$WORK/inventory.expected.json" "$WORK/inventory.stock.json" && fail "BREAK=1: an inventory missing the public IP still matches stock's - the comparison is not load-bearing"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the public IP from the expected inventory makes the resource-by-resource match correctly fail"
elif cmp -s "$WORK/inventory.green.json" "$WORK/inventory.stock.json"; then
  gauntlet_stage greenfield pass "11 resources from one plain apply with a live block; read through ARM, resource by resource with the marker tags normalised out, they match what stock's cold deploy made"
else
  gauntlet_stage greenfield fail "the greenfield inventory differs from stock's cold deploy: $(diff "$WORK/inventory.stock.json" "$WORK/inventory.green.json" | head -10 | tr '\n' ' ')"
fi
# ── 15. no_local_state: the state cache gone, the record store remote ────
#
# #1098's ruling, read for this lane's store: "local state" is every local
# artifact, and with record_store "azurerm" the only one is the state cache
# (.terraform/choudoufu-cache.tfstate); the records are blobs in
# $REC_ACCOUNT/green, which a fresh clone reaches as this run does, so the
# stage asserts no local record directory exists rather than deleting one -
# the shape reference-eks's live-cert target takes with its records in the
# cluster. The cache-serving plan is taken first, then the cache is deleted,
# and the plan with only the account left must find every declared
# resource: the nine that take tags by their markers, the subnet and the
# NSG rule through their records in the container. Nothing created,
# destroyed or replaced; an in-place update is listed, not failed. Both
# plans are -refresh=false, the only mode the cache serves reads in, and
# their azurerm requests are counted from TF_LOG=DEBUG's request lines
# (azurerm logs each one as "[DEBUG] AzureRM Request:"); azapi's and the
# record store's own requests are not in that count. Run on the greenfield
# root, before its teardown, as the kind lane runs it.
gauntlet_begin_stage no_local_state
log "=== 15. no_local_state: the cache-serving plan, then no state cache, records only in the container ==="
nls_calls() { local n; n="$(grep -cE '\[DEBUG\] [A-Za-z]+ Request: ' "$1" 2>/dev/null)"; echo "${n:-0}"; }
NLS_CACHE="$GREEN/.terraform/choudoufu-cache.tfstate"
NLS_WARM="$(tofu_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)"
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$NLS_WARM" \
  || { printf '%s\n' "$NLS_WARM" | tail -20; fail "the no-op apply that writes the state cache did not apply cleanly"; }
[ -s "$NLS_CACHE" ] || fail "no state cache at $NLS_CACHE after a no-op apply, so there is nothing to serve from or delete"
[ ! -e "$GREEN/.tofu-records" ] || fail "the greenfield root holds a local .tofu-records directory, so its records are not only in the record store"
NLS_BASE="$( (export TF_LOG=DEBUG TF_LOG_PATH="$WORK/nls.base.log"; tofu_a "$GREEN" plan -refresh=false -input=false -no-color) 2>&1)"; NLS_BASE_RC=$?
[ "$NLS_BASE_RC" -eq 0 ] && grep -q "No changes." <<< "$NLS_BASE" \
  || { printf '%s\n' "$NLS_BASE" | tail -20; fail "the cache-serving plan (exit $NLS_BASE_RC) is not empty, so it is no baseline"; }
NLS_BASE_CALLS="$(nls_calls "$WORK/nls.base.log")"
rm -f "$NLS_CACHE"
[ ! -e "$NLS_CACHE" ] || fail "the state cache survived deletion"
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  tag_set a "$VNET_ID" "$NET_API" tofu-estate - && tag_set a "$VNET_ID" "$NET_API" tofu-address - \
    || fail "BREAK_NO_LOCAL_STATE: could not strip the VNet's markers"
fi
NLS_PLAN="$( (export TF_LOG=DEBUG TF_LOG_PATH="$WORK/nls.log"; tofu_a "$GREEN" plan -refresh=false -input=false -no-color) 2>&1)"; NLS_RC=$?
NLS_CALLS="$(nls_calls "$WORK/nls.log")"
NLS_BAD=""
if [ "$NLS_RC" -ne 0 ]; then
  NLS_BAD="the plan with no local state exited $NLS_RC: $(gauntlet_first_error_line <<< "$NLS_PLAN")"
elif grep -qE '# .+ (will be (created|destroyed)|must be replaced)' <<< "$NLS_PLAN"; then
  NLS_BAD="with no local state the plan proposes: $(grep -E '# .+ (will be (created|destroyed)|must be replaced)' <<< "$NLS_PLAN" | sed -E 's/^[[:space:]#]*//' | tr '\n' ';')"
fi
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  tag_set a "$VNET_ID" "$NET_API" tofu-estate "$ESTATE" && tag_set a "$VNET_ID" "$NET_API" tofu-address azurerm_virtual_network.app \
    || fail "BREAK_NO_LOCAL_STATE: could not put the VNet's markers back"
  if [ -z "$NLS_BAD" ]; then
    gauntlet_stage no_local_state fail "BREAK_NO_LOCAL_STATE=1: with the VNet's markers stripped and the state cache deleted, the plan still found everything, so the check is not load-bearing"
  else
    gauntlet_stage no_local_state pass "BREAK_NO_LOCAL_STATE=1 control: with the VNet's tofu-estate and tofu-address stripped through ARM and the state cache deleted, the check correctly failed ($NLS_BAD); the markers were put back"
  fi
elif [ -n "$NLS_BAD" ]; then
  printf '%s\n' "$NLS_PLAN" | grep -E '^Plan:|will be|must be|^Error|^Warning' | head -20
  gauntlet_stage no_local_state fail "with the state cache deleted and the records only in $REC_ACCOUNT/green: $NLS_BAD"
else
  NLS_UPD="$(grep -E '^[[:space:]]*# \S+ will be updated in-place' <<< "$NLS_PLAN" | sed -E 's/^[[:space:]#]*//; s/ will be .*$//' | sort -u | tr '\n' ' ' | sed 's/ $//')"
  NLS_RATIO="uncounted (no azurerm request line in the debug log)"
  [ "$NLS_BASE_CALLS" -gt 0 ] && NLS_RATIO="$(awk -v a="$NLS_CALLS" -v b="$NLS_BASE_CALLS" 'BEGIN { printf "%.2fx", a / b }')"
  gauntlet_stage no_local_state pass "with the state cache deleted - a fresh clone with only the account left, the records being blobs in $REC_ACCOUNT/green and no local record directory existing at all - the plan ($(plan_line "$NLS_PLAN")) created, destroyed and replaced nothing: the nine resources that take tags found by their markers, the subnet and the NSG rule through their records in the container. In-place updates: ${NLS_UPD:-none}. The empty plan before the deletion was served from a cache a no-op apply wrote. plan_calls_no_local_state=$NLS_CALLS plan_calls_cache_serving=$NLS_BASE_CALLS ratio=$NLS_RATIO (both plan -refresh=false, azurerm requests counted from TF_LOG=DEBUG); stock in this position has no plan at all, only one import block per resource. BREAK_NO_LOCAL_STATE=1 strips the VNet's markers and the check correctly fails"
fi
gauntlet_end_stage
( tofu_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (the greenfield estate's destroy failed; the container is removed on exit)"

# ── 14. strict: every toggle on, one refusal ─────────────────────────────
#
# What reference-k8s and reference-ec2-vpc assert, against a scratch estate
# carrying one resource, random_password.db: secrets = "refuse" refuses it,
# no_source_create = "refuse" and marker_repair = "never" (with a markers
# "record" selection naming azurerm_user_assigned_identity, which this
# scratch estate never declares) are on and reach nothing. The toggles are
# substrate-neutral; the scratch estate's records go to the record store's
# strict container, the store the lane uses.
gauntlet_begin_stage strict
log "=== 14. strict: every strict toggle on ==="
STRICT="$WORK/strict"
mkdir -p "$STRICT"
strict_block() { # $1 = the secrets setting under test ("refuse" or "store")
  cat <<EOT
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "$ESTATE-strict"
$(gauntlet_floci_az_record_store "$HOST_A" "$REC_RG" "$REC_ACCOUNT" strict)
    strict {
      secrets          = "$1"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["azurerm_user_assigned_identity"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
EOT
}
strict_block refuse > "$STRICT/main.tf" || fail "could not write the strict-stage scratch estate"
( tofu_a "$STRICT" init -input=false -no-color >/dev/null 2>&1 ) || fail "choudoufu init for the strict-stage scratch estate failed"
STRICT_ON="$(tofu_a "$STRICT" plan -input=false -no-color 2>&1)"; STRICT_ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_block store > "$STRICT/main.tf" || fail "BREAK_STRICT: could not rewrite the scratch estate"
  STRICT_OFF="$(tofu_a "$STRICT" plan -input=false -no-color 2>&1)"; STRICT_OFF_RC=$?
  [ "$STRICT_OFF_RC" -eq 0 ] || { printf '%s\n' "$STRICT_OFF" | tail -20; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $STRICT_OFF_RC - a refusal appeared where none should"; }
  grep -q "^Error:" <<< "$STRICT_OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$STRICT_OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$STRICT_ON_RC" -eq 1 ] || { printf '%s\n' "$STRICT_ON" | tail -20; fail "the every-toggle-on plan exited $STRICT_ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$STRICT_ON")" -eq 1 ] || { printf '%s\n' "$STRICT_ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$STRICT_ON" || { printf '%s\n' "$STRICT_ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$(tr -s ' \n' '  ' <<< "$STRICT_ON")" || { printf '%s\n' "$STRICT_ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming azurerm_user_assigned_identity) against a scratch estate carrying random_password.db, its record store the lane's $REC_ACCOUNT/strict container: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
gauntlet_end_stage

gauntlet_end
