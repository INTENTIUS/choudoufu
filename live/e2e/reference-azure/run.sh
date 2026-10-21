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
# estate, read with ARM GETs; the out-of-band hand is an ARM PATCH
# (gauntlet_arm), not the AWS CLI.
#
# Written for #1120 and NOT RUN. floci-az's gaps (live/FLOCI.md, Azure) are
# expected to fail stages by name on the first run rather than be skipped:
# the plan's Resource Graph sweep has no route, and storage account tags
# are dropped, so migrate's marker count reads one short of nine. Those are
# the emulator's to close (emulator policy, live/ADDING-A-PLATFORM.md).
# day2_replace, day2_crash, strict and no_local_state report not_run with
# the reason until each is written for this substrate.
#
# A stage this script cannot pass records fail with the reason and the run
# continues where it can; the runner, not the script, decides what "clear"
# means. A fault in the substrate itself (a container that never came up)
# is a fail on the stage being set up and an exit.
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
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

gauntlet_plugin_cache
ESTATE="reference-azure"
RG="refaz-rg"
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
  if [ "$1" = "live" ]; then cat <<EOF
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
EOF
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
# tag_patch <a|b> <id> <api> <key> <value|-> sets or removes one tag and
# keeps the rest, since an ARM PATCH replaces the tags object whole.
tag_patch() {
  local env="$1" rid="$2" api="$3" key="$4" val="$5" tags
  tags="$("on_$env" gauntlet_arm GET "$rid?api-version=$api" | KEY="$key" VAL="$val" python3 -c '
import json, os, sys
t = json.load(sys.stdin).get("tags") or {}
if os.environ["VAL"] == "-":
    t = {k: v for k, v in t.items() if k.lower() != os.environ["KEY"].lower()}
else:
    t[os.environ["KEY"]] = os.environ["VAL"]
print(json.dumps({"tags": t}))')" || return 1
  "on_$env" gauntlet_arm PATCH "$rid?api-version=$api" "$tags" >/dev/null
}
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
write_config "$ADOPTED" live
( tofu_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
TAGGED="$(count_a)" || fail "could not read A's resources through ARM"
if grep -qE ' 0 failed, 0 skipped' <<< "$SUMMARY_LINE" && [ "$TAGGED" = "9" ]; then
  gauntlet_stage migrate pass "$SUMMARY_LINE; all 9 taggable resources carry tofu-estate=$ESTATE, read back through ARM (the subnet and the NSG rule have no tags argument)"
else
  gauntlet_stage migrate fail "live-import -approve: ${SUMMARY_LINE:-no summary line}; $TAGGED of 9 taggable resources carry tofu-estate=$ESTATE through ARM; first Error: line: \"$(gauntlet_first_error_line <<< "$APPROVE_OUT")\""
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
log "=== 5. drift_reconverge: an ARM PATCH adds a tag to the VNet on A and on B; stock's plan on B is the oracle ==="
tag_patch a "$VNET_ID" "$NET_API" drift yes || fail "could not tamper the VNet on A"
tag_patch b "$VNET_ID" "$NET_API" drift yes || fail "could not tamper the VNet on B"
if [ "${BREAK:-}" = "1" ]; then
  tag_patch a "$PIP_ID" "$NET_API" drift yes || fail "BREAK: could not tamper the public IP on A"
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
  gauntlet_stage drift_reconverge pass "a tag added to the VNet with an ARM PATCH; choudoufu proposed exactly azurerm_virtual_network.app (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle container for the same tamper; apply changed 1 and the tag is gone, read through ARM. BREAK=1 tampers a second resource and the single-resource assertion correctly fails"
fi

# ── 12. plan_approval: plan -out, the world moves, apply refuses ─────────
gauntlet_begin_stage plan_approval
log "=== 12. plan_approval: a saved plan, an out-of-band tag, a refusal; then the same file applies once the world is back ==="
REVIEWED='  tags = { reviewed = "yes" }'
write_config "$ADOPTED" live app 2 "$REVIEWED"
write_config "$ORACLE" stock app 2 "$REVIEWED"
P_PLAN="$(tofu_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
tag_patch a "$SHARD0_ID" "$MI_API" stray yes || fail "could not move the world (a stray tag on shard 0) on A"
P_APPLY="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  tag_patch a "$SHARD0_ID" "$MI_API" stray - || fail "BREAK_APPROVAL: could not put the world back"
  ( tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  APP_JSON="$(on_a gauntlet_arm GET "$APP_ID?api-version=$MI_API")" || fail "could not read the identity through ARM after the refusal"
  grep -q '"reviewed"' <<< "$APP_JSON" && fail "the identity gained the reviewed tag despite the refusal: $APP_JSON"
  tag_patch a "$SHARD0_ID" "$MI_API" stray - || fail "could not put the world back"
  P_APPLY2="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one resource"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the identity gains tags reviewed=yes); the world then moved out of band (a stray tag on shard 0, an ARM PATCH) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the tag removed the identical file applied, 0 added, 1 changed, 0 destroyed; stock's own planfile applied on the oracle container in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
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
  grep -q "azurerm_public_ip.app will be destroyed" <<< "$M_PLAN" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan does not destroy azurerm_public_ip.app"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$M_PLAN" || fail "the plan destroys more than the public IP: $(gauntlet_plan_line <<< "$M_PLAN")"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the destroying apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the removal on B"
  gone_a "$PIP_ID" "$NET_API" || fail "ARM still answers for the public IP after its block was removed and applied"
  gauntlet_stage day2_remove pass "the public IP's block deleted: choudoufu planned exactly azurerm_public_ip.app's destroy (0 add, 0 change, 1 destroy), as stock planned on the oracle, and after the apply ARM answers 404 for it"
fi

# ── 8. day2_count: scale the shard identities down and back up ───────────
gauntlet_begin_stage day2_count
log "=== 8. day2_count: shard count 2 -> 1 -> 2 ==="
current_block() { write_config "$1" "$2" team "$3" "$REVIEWED"; moved_block >> "$1/main.tf"; drop_pip "$1/main.tf"; }
current_block "$ADOPTED" live 1
C_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  grep -q 'azurerm_user_assigned_identity.shard\[0\] will be destroyed' <<< "$C_PLAN" && fail "BREAK_COUNT=1: shard[0] is planned for destroy - the wrong instance"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: the assertion that shard[0] is destroyed correctly fails; the plan destroys $(grep -oE 'azurerm_user_assigned_identity.shard\[[0-9]\] will be destroyed' <<< "$C_PLAN" | head -1)"
else
  grep -q 'azurerm_user_assigned_identity.shard\[1\] will be destroyed' <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down does not destroy shard[1]"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || fail "the scale-down is not exactly one destroy: $(gauntlet_plan_line <<< "$C_PLAN")"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-down apply failed"
  gone_a "$SHARD1_ID" "$MI_API" || fail "ARM still answers for shard 1 after the scale-down"
  gone_a "$SHARD0_ID" "$MI_API" && fail "shard 0 is gone after the scale-down; the survivor did not keep its identity"
  current_block "$ADOPTED" live 2
  U_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the scale-up plan failed"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up is not exactly one create"; }
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-up apply failed"
  gauntlet_stage day2_count pass "count 2 -> 1 destroyed exactly shard[1] (ARM answers 404 for it, shard 0 kept its id); 1 -> 2 created exactly one, at shard[1]"
fi

# ── 9, 10: not written for this substrate yet ────────────────────────────
gauntlet_stage day2_replace not_run "not yet written for the azure lane (#1120): the shape is a create_before_destroy identity whose name changes, as on kind"
gauntlet_stage day2_crash not_run "not yet written for the azure lane (#1120): needs the e2e build's interrupt hook on an apply creating two resources, as the floci and kind estates do"

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
write_config "$GREEN" live
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
( tofu_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (the greenfield estate's destroy failed; the container is removed on exit)"

gauntlet_stage strict not_run "not yet written for the azure lane (#1120)"
gauntlet_stage no_local_state not_run "not yet written for the azure lane (#1120): the plan with no local state reaches the Resource Graph sweep, whose query floci-az serves since 30000b0 but its KQL subset refuses"

gauntlet_end
