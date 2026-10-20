#!/usr/bin/env bash
# corpus-azapi-role-assignment: the azure lane's first PUBLISHED estate
# (#1120), crossed on the floci-az substrate. Azure/terraform-provider-azapi's
# own examples/Microsoft.Authorization_roleAssignments@2022-04-01 root at tag
# v2.13.0 (commit cf3ecee858fa48c0aca95d0b995a8bfd4950c8ec,
# live/corpus-manifest.json), the provider's published example of an RBAC
# role assignment: a resource group declared through azapi_resource, an
# azurerm_user_assigned_identity in it, the built-in AcrPull role read with
# data "azurerm_role_definition", and an azapi_resource of type
# Microsoft.Authorization/roleAssignments@2022-04-01 granting the identity
# that role at the group's scope, named by a fixed GUID. Three managed
# resources and one data source, over both of the lane's providers, with
# references crossing between them in both directions.
#
# Why this root (and not the corpus's AKS, key vault or private-endpoint
# roots): it is the one floci-az at 30000b0 serves as written. Role
# definitions, role assignments (immutable, GUID-named, refused as
# duplicates) and the roleName filter the data source sends are modelled
# (02ae4a5); resource groups and managed identities keep their tags. AKS is
# mocked there (no node resource group, no identity or
# nodeProvisioningProfile echoed, so stock's own replan is never empty), and
# a key vault's properties are fixed (enableSoftDelete reads false), so both
# would fail every stage after cold_deploy on the emulator rather than on
# the product. It also brings what reference-azure has none of: an untaggable
# azapi type (Microsoft.Authorization/roleAssignments carries no tags at any
# API version, internal/live/armtypes/table_generated.txt), so its marker is
# its record; a resource group declared through azapi rather than azurerm;
# and a data source read at a scope another provider's resource computes.
# The one type it shares with reference-azure is the identity.
#
# Deltas, each asserted so a moved pin fails loudly rather than silently:
#   1. required_providers: the root names azure/azapi and hashicorp/azurerm
#      with no version, so each entry is replaced by the lane's pin, read
#      from live/oracle-versions.json (gauntlet_azapi_required_provider,
#      gauntlet_azurerm_required_provider);
#   2. both provider blocks gain subscription_id = floci-az's subscription,
#      because identity reads the subscription from the provider block
#      (#1120's corrections) and azurerm 5 requires one; the endpoints and
#      the credential are gauntlet_floci_az_up's ARM_* environment, and the
#      root's own skip_provider_registration = false is kept;
#   3. choudoufu's roots get a live block, in a file of its own (live.tf).
# Stage additions, each in the stage that needs it: plan_approval gives the
# identity tags = { reviewed = "yes" }; day2_rename renames the role
# assignment's block (roleAssignments -> role_assignment) with a moved
# block in moved.tf; day2_remove deletes that block; day2_count declares a
# two-instance count of azapi user-assigned identities in shards.tf, since
# the published root has no count block.
#
# .corpus is read, never written: the root is copied out per run. Two
# floci-az containers, both started for this run and removed after it, as
# reference-azure: A holds the estate (stock cold-deploys it, choudoufu
# adopts it and runs the day-2 stages, tears it down, then applies the same
# root fresh with a live block); B is the oracle, where stock applies the
# identical root and every day-2 change itself.
#
# Written for #1120 and NOT RUN. floci-az's gaps (live/FLOCI.md, Azure) are
# expected to show by name on the first run rather than be skipped: the
# plan's Resource Graph sweep is refused (lex00/floci-az#11), which
# choudoufu reports as the "Azure sweep unavailable" warning, an incomplete
# sweep. day2_replace, day2_crash, strict and no_local_state report not_run
# with the reason, as on reference-azure.
#
#   go run ./tools/gauntlet run corpus-azapi-role-assignment   # the maintainer, by hand
#   bash live/e2e/corpus-azapi-role-assignment/run.sh           # asks for "run" at the terminal
#
# Needs `just corpus-fetch` first, then docker, curl, terraform (the stock
# binary), python3 and Go on PATH. On macOS the floci-az CA has to be
# trusted in the keychain by hand (Go there ignores SSL_CERT_FILE);
# live/FLOCI.md says how.
#
# Env overrides:
#   TOFU_BIN       path to a prebuilt choudoufu binary; skips the `go build`.
#   BREAK          set to 1 to run drift_reconverge's, day2_rename's and
#                  greenfield's negative controls instead of the real
#                  checks: the resource group is tampered as well and the
#                  single-resource assertion must fail; the role
#                  assignment's own name (its GUID) is changed instead of
#                  its block's and the zero-churn assertion must fail; the
#                  role assignment is dropped from greenfield's expected
#                  inventory and the resource-by-resource match must fail.
#   BREAK_REMOVE   set to 1 to keep the role assignment's block and assert a
#                  destroy is proposed anyway (day2_remove's Break line).
#   BREAK_COUNT    set to 1 to assert the wrong instance was destroyed on the
#                  scale-down (day2_count's Break line); must fail.
#   BREAK_APPROVAL set to 1 to apply the saved plan after the world moved and
#                  expect success (plan_approval's Break line); must fail.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "$ROOT/live/e2e/lib/gauntlet.sh"

gauntlet_plugin_cache
ESTATE="corpus-azapi-role-assignment"
SRC="$ROOT/.corpus/azapi/examples/Microsoft.Authorization_roleAssignments@2022-04-01"
PINNED="cf3ecee858fa48c0aca95d0b995a8bfd4950c8ec"
# The root's own names: var.resource_name's default, the identity's name and
# the role assignment's fixed GUID.
RG="acctest0001"; UAI="TestUAI"; RA_GUID="6faae21a-0cd6-4536-8c23-a278823d12ed"
SUB="/subscriptions/${FLOCI_AZ_SUBSCRIPTION_ID}"
WORK="$(mktemp -d)"
STOCK="$WORK/stock"; ADOPTED="$WORK/adopted"; ORACLE="$WORK/oracle"; GREEN="$WORK/green"
CONTAINER_A="choudoufu-azra-a-$$"; CONTAINER_B="choudoufu-azra-b-$$"
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

# ── 0. tools and the pinned source ───────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - needed as the stock oracle"
command -v curl >/dev/null 2>&1 || fail "curl is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"
[ -f "$SRC/main.tf" ] || fail "$SRC/main.tf is missing - run \`just corpus-fetch\` first"
PIN="$(git -C "$ROOT/.corpus/azapi" rev-parse HEAD 2>/dev/null)"
[ "$PIN" = "$PINNED" ] || fail "the fetched azapi repository is at ${PIN:-nothing}, not the pinned $PINNED - run \`just corpus-fetch\`"

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

# ── the root, copied out of .corpus with its deltas ──────────────────────
# The azapi and azurerm requirements are live/oracle-versions.json's pins,
# read once behind one fail each (the #1216 rule).
AZAPI_REQUIRED_PROVIDER="$(gauntlet_azapi_required_provider)" \
  || fail "could not read the azure/azapi pin from live/oracle-versions.json"
AZURERM_REQUIRED_PROVIDER="$(gauntlet_azurerm_required_provider)" \
  || fail "could not read the hashicorp/azurerm pin from live/oracle-versions.json"

# write_root <dir> <live|stock> copies the root's main.tf into <dir> and
# applies deltas 1 and 2, and delta 3 when the mode is live. The shape of
# the day-2 stages is read from the environment, so every call states the
# whole configuration it wants:
#   RA       the role assignment's block label: roleAssignments (the
#            root's own), role_assignment (after day2_rename), or none
#            (after day2_remove)
#   RA_NAME  the role assignment's GUID (BREAK's rename control changes it)
#   REVIEWED 1 to give the identity tags = { reviewed = "yes" }
#   MOVED    1 to write moved.tf (roleAssignments -> role_assignment)
#   SHARDS   how many azapi identities shards.tf declares; 0 writes none
write_root() {
  local dst="$1" mode="$2"
  rm -rf "$dst"; mkdir -p "$dst"
  cp "$SRC/main.tf" "$dst/main.tf" || fail "could not copy the root out of .corpus"
  DST="$dst" SUBID="$FLOCI_AZ_SUBSCRIPTION_ID" ROOT_GUID="$RA_GUID" \
    AZAPI_REQ="$AZAPI_REQUIRED_PROVIDER" AZURERM_REQ="$AZURERM_REQUIRED_PROVIDER" \
    RA="${RA:-roleAssignments}" RA_NAME="${RA_NAME:-$RA_GUID}" REVIEWED="${REVIEWED:-0}" \
    MOVED="${MOVED:-0}" SHARDS="${SHARDS:-0}" python3 - <<'PY' || fail "a delta did not match the published root - the corpus pin has moved"
import os, re, sys
env = os.environ
path = os.path.join(env["DST"], "main.tf")
src = open(path).read()

def once(old, new, what):
    global src
    n = src.count(old)
    if n != 1:
        sys.exit("%s: expected exactly one match in main.tf, found %d" % (what, n))
    src = src.replace(old, new)

# Delta 1: the lane's provider pins in place of the unversioned entries.
once('    azapi = {\n      source = "Azure/azapi"\n    }\n    azurerm = {\n      source = "hashicorp/azurerm"\n    }\n',
     env["AZAPI_REQ"] + "\n" + env["AZURERM_REQ"] + "\n", "delta 1 (required_providers)")
# Delta 2: the subscription in both provider blocks.
sub = '  subscription_id = "%s"\n' % env["SUBID"]
once('provider "azurerm" {\n  features {\n  }\n}\n', 'provider "azurerm" {\n' + sub + '  features {\n  }\n}\n', "delta 2 (azurerm provider)")
once('provider "azapi" {\n  skip_provider_registration = false\n}\n',
     'provider "azapi" {\n  subscription_id            = "%s"\n  skip_provider_registration = false\n}\n' % env["SUBID"], "delta 2 (azapi provider)")

# plan_approval's tags on the identity.
if env["REVIEWED"] == "1":
    once('  location            = azapi_resource.resourceGroup.location\n}\n',
         '  location            = azapi_resource.resourceGroup.location\n  tags                = { reviewed = "yes" }\n}\n', "the identity's block")
# BREAK's rename control: the role assignment's own name.
if env["RA_NAME"] != env["ROOT_GUID"]:
    once('  name      = "%s"\n' % env["ROOT_GUID"], '  name      = "%s"\n' % env["RA_NAME"], "the role assignment's name")
# day2_rename and day2_remove: the role assignment's block.
header = 'resource "azapi_resource" "roleAssignments" {\n'
if env["RA"] == "role_assignment":
    once(header, 'resource "azapi_resource" "role_assignment" {\n', "the role assignment's block")
elif env["RA"] == "none":
    n = len(re.findall(r'resource "azapi_resource" "roleAssignments" \{\n.*?\n\}\n', src, flags=re.S))
    if n != 1:
        sys.exit("the role assignment's block: expected exactly one, found %d" % n)
    src = re.sub(r'\nresource "azapi_resource" "roleAssignments" \{\n.*?\n\}\n', "\n", src, flags=re.S)
elif env["RA"] != "roleAssignments":
    sys.exit("RA=%s is not roleAssignments, role_assignment or none" % env["RA"])
open(path, "w").write(src)

if env["MOVED"] == "1":
    open(os.path.join(env["DST"], "moved.tf"), "w").write(
        "# Added by live/e2e/corpus-azapi-role-assignment/run.sh for day2_rename.\n"
        "moved {\n  from = azapi_resource.roleAssignments\n  to   = azapi_resource.role_assignment\n}\n")
shards = int(env["SHARDS"])
if shards > 0:
    open(os.path.join(env["DST"], "shards.tf"), "w").write(
        "# Added by live/e2e/corpus-azapi-role-assignment/run.sh for day2_count:\n"
        "# the published root declares no count block of its own.\n"
        'resource "azapi_resource" "shard" {\n'
        "  count     = %d\n"
        '  type      = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"\n'
        '  name      = "${var.resource_name}-shard-${count.index}"\n'
        "  parent_id = azapi_resource.resourceGroup.id\n"
        "  location  = var.location\n"
        "}\n" % shards)
PY
  if [ "$mode" = "live" ]; then
    cat > "$dst/live.tf" <<EOF
# Delta 3, added by live/e2e/corpus-azapi-role-assignment/run.sh.
terraform {
  live {
    estate = "$ESTATE"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
EOF
  fi
}

# ── substrate helpers ────────────────────────────────────────────────────
# As reference-azure: each container's ARM_* values are saved after
# gauntlet_floci_az_up and every command runs under one of the two.
save_env() { env | grep -E '^(FLOCI_AZ_(HOST|ENDPOINT|CA_FILE)|ARM_[A-Z_]+|SSL_CERT_FILE)=' > "$1"; }
on_a() { ( set -a; . "$WORK/a.env"; set +a; "$@" ); }
on_b() { ( set -a; . "$WORK/b.env"; set +a; "$@" ); }
stock_b() { ( cd "$ORACLE" && on_b terraform "$@" ); }
tofu_a() { local dir="$1"; shift; ( cd "$dir" && on_a "$TOFU" "$@" ); }

RG_ID="${SUB}/resourceGroups/${RG}"
UAI_ID="${RG_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${UAI}"
RA_ID="${RG_ID}/providers/Microsoft.Authorization/roleAssignments/${RA_GUID}"
SHARD0_ID="${RG_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${RG}-shard-0"
SHARD1_ID="${RG_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${RG}-shard-1"
RG_API="2021-04-01"; MI_API="2023-01-31"; RA_API="2022-04-01"
# The taggable resources, with the api-version each is read at. The role
# assignment has no tags at any API version and is not in the count.
TAGGED_IDS="
${RG_ID}|${RG_API}
${UAI_ID}|${MI_API}"
ALL_IDS="$TAGGED_IDS
${RA_ID}|${RA_API}"

# arm_read <a|b> prints one JSON object keyed by lowercased ARM id: the id
# ARM answered with, its tags, and the properties this root declares, or
# null for an id that answers 404. The role assignment's principalId is
# left out: it is the identity's, which the server mints, so it differs
# between stock's cold deploy and choudoufu's greenfield by construction.
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
    keep = {}
    if "roleDefinitionId" in p:
        keep["roleDefinition"] = str(p["roleDefinitionId"]).rstrip("/").split("/")[-1].lower()
    for k in ("principalType", "scope"):
        if k in p:
            keep[k] = str(p[k]).lower() if k == "scope" else p[k]
    out[rid.lower()] = {"id": body.get("id", ""), "location": body.get("location"),
                        "tags": body.get("tags") or {}, "props": keep}
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
# managed_count <dir>: the managed resources in a stock state, the data
# source left out.
managed_count() { ( cd "$1" && terraform state list | grep -vc '^data\.' ); }

# ── 1. cold_deploy: stock stands the root up on A (and B, the oracle) ────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two floci-az containers, stock terraform applies the published root on each ==="
mkdir -p "$WORK/a" "$WORK/b"
gauntlet_floci_az_up "$CONTAINER_A" "$WORK/a" || fail "floci-az container A ($CONTAINER_A) did not come up"
save_env "$WORK/a.env"
gauntlet_floci_az_up "$CONTAINER_B" "$WORK/b" || fail "floci-az container B ($CONTAINER_B) did not come up"
save_env "$WORK/b.env"
write_root "$STOCK" stock
write_root "$ORACLE" stock
( cd "$STOCK" && on_a gauntlet_locked_init terraform init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on A"
COLD_OUT="$(cd "$STOCK" && on_a terraform apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$COLD_OUT" | tail -20; fail "stock cold deploy failed on A"; }
grep -qF "Apply complete! Resources: 3 added, 0 changed, 0 destroyed" <<< "$COLD_OUT" || { printf '%s\n' "$COLD_OUT" | tail -5; fail "stock cold deploy did not add exactly 3 resources on A"; }
[ -f "$STOCK/terraform.tfstate" ] || fail "stock left no terraform.tfstate on A"
STOCK_N="$(managed_count "$STOCK")"
[ "$STOCK_N" = "3" ] || fail "stock's state holds $STOCK_N managed instances, want 3"
UNMARKED="$(count_a)" || fail "could not read A's resources through ARM"
[ "$UNMARKED" = "0" ] || fail "$UNMARKED resource(s) already carry tofu-estate=$ESTATE after a plain stock apply - this proves nothing"
on_a gauntlet_arm GET "$RA_ID?api-version=$RA_API" >/dev/null || fail "the role assignment does not answer at $RA_ID after stock's apply"
inventory a > "$WORK/inventory.stock.json" || fail "could not read the cold-deployed inventory on A"
( stock_b init -input=false -no-color >/dev/null 2>&1 ) || fail "stock init failed on B"
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 3 added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock cold deploy failed on B"; }
gauntlet_stage cold_deploy pass "3 resources (an azapi resource group, an azurerm user-assigned identity, an azapi role assignment granting it AcrPull, read through data \"azurerm_role_definition\") from plain terraform on the published root plus its deltas (the lane's provider pins, the subscription in both provider blocks) against floci-az $(cat "$ROOT/live/floci-az-image"); a real terraform.tfstate with 3 managed instances, zero tofu-estate tags read back through ARM, the role assignment answering at its id; the identical root cold-deployed by stock on a second container as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
write_root "$ADOPTED" live
( tofu_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
TAGGED="$(count_a)" || fail "could not read A's resources through ARM"
RA_RECORD="$(gauntlet_record_file "$ADOPTED/.tofu-records/tofu-records" azapi_resource.roleAssignments || true)"
if grep -qE ' 0 failed, 0 skipped' <<< "$SUMMARY_LINE" && [ "$TAGGED" = "2" ] && [ -n "$RA_RECORD" ]; then
  gauntlet_stage migrate pass "$SUMMARY_LINE; both taggable resources (the resource group and the identity) carry tofu-estate=$ESTATE, read back through ARM, and the role assignment, which has no tags at any API version, is held by its record"
else
  gauntlet_stage migrate fail "live-import -approve: ${SUMMARY_LINE:-no summary line}; $TAGGED of 2 taggable resources carry tofu-estate=$ESTATE through ARM; role assignment record: ${RA_RECORD:-none}; first Error: line: \"$(gauntlet_first_error_line <<< "$APPROVE_OUT")\""
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
SWEEP_WARN=""; grep -qF "Azure sweep unavailable" <<< "$PLAN_OUT" && SWEEP_WARN="; the plan warned \"Azure sweep unavailable\" (lex00/floci-az#11: the sweep's KQL is outside floci-az's subset), so the sweep was incomplete"
if [ "$PLAN_RC" = "0" ] && grep -q "No changes." <<< "$PLAN_OUT" && [ -z "$IDS_MISSING" ]; then
  gauntlet_stage test_plan pass "the plan with no state file is empty; all 3 ARM ids answer a GET with the same id, compared case-insensitively$SWEEP_WARN"
else
  gauntlet_stage test_plan fail "the plan with no state file is not empty or failed ($(gauntlet_plan_line <<< "$PLAN_OUT"); first Error: line: \"$(gauntlet_first_error_line <<< "$PLAN_OUT")\") or an id did not answer as itself (${IDS_MISSING:-none})$SWEEP_WARN"
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
# The identity, not the resource group: the root gives the group no tags, and
# azapi's tags are computed when unset, so on the oracle an out-of-band tag
# on the group is no drift at all.
gauntlet_begin_stage drift_reconverge
log "=== 5. drift_reconverge: an ARM PATCH adds a tag to the identity on A and on B; stock's plan on B is the oracle ==="
tag_patch a "$UAI_ID" "$MI_API" drift yes || fail "could not tamper the identity on A"
tag_patch b "$UAI_ID" "$MI_API" drift yes || fail "could not tamper the identity on B"
if [ "${BREAK:-}" = "1" ]; then
  tag_patch a "$RG_ID" "$RG_API" drift yes || fail "BREAK: could not tamper the resource group on A"
fi
ORACLE_PLAN="$(stock_b plan -detailed-exitcode -input=false -no-color 2>&1)"; ORACLE_RC=$?
[ "$ORACLE_RC" -eq 2 ] || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B after the tamper exited $ORACLE_RC, want 2 (changes)"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$ORACLE_PLAN" || { printf '%s\n' "$ORACLE_PLAN" | tail -10; fail "stock's plan on B does not propose exactly one change"; }
grep -q "azurerm_user_assigned_identity.uai" <<< "$ORACLE_PLAN" || fail "stock's plan on B does not name azurerm_user_assigned_identity.uai"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not reconverge B"
DRIFT_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after the tamper failed"; }
if [ "${BREAK:-}" = "1" ]; then
  if grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN"; then
    fail "BREAK=1: two resources were tampered but the plan still proposes exactly one change - the single-resource assertion is not load-bearing"
  fi
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: could not reconverge A"
  gauntlet_stage drift_reconverge pass "BREAK=1 control: with the resource group tampered too the single-resource assertion correctly fails to hold ($(gauntlet_plan_line <<< "$DRIFT_PLAN")); reconverged afterwards"
else
  grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$DRIFT_PLAN" || { printf '%s\n' "$DRIFT_PLAN" | tail -20; fail "the plan after one tamper does not propose exactly one change"; }
  grep -q "azurerm_user_assigned_identity.uai" <<< "$DRIFT_PLAN" || fail "the plan does not name azurerm_user_assigned_identity.uai"
  RECONV="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RECONV" | tail -20; fail "the reconverging apply failed"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$RECONV" || fail "the reconverging apply did not change exactly one resource"
  UAI_JSON="$(on_a gauntlet_arm GET "$UAI_ID?api-version=$MI_API")" || fail "could not read the identity through ARM after reconverging"
  grep -q '"drift"' <<< "$UAI_JSON" && fail "the identity still carries the drift tag after reconverging: $UAI_JSON"
  gauntlet_stage drift_reconverge pass "a tag added to the identity with an ARM PATCH; choudoufu proposed exactly azurerm_user_assigned_identity.uai (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle container for the same tamper; apply changed 1 and the tag is gone, read through ARM. BREAK=1 tampers the resource group too and the single-resource assertion correctly fails"
fi

# ── 12. plan_approval: plan -out, the world moves, apply refuses ─────────
# The world moves on the resource group: choudoufu's stamped tags are the
# group's configured tags, so a stray one is a change its fresh plan sees.
gauntlet_begin_stage plan_approval
log "=== 12. plan_approval: a saved plan, an out-of-band tag, a refusal; then the same file applies once the world is back ==="
REVIEWED=1 write_root "$ADOPTED" live
REVIEWED=1 write_root "$ORACLE" stock
P_PLAN="$(tofu_a "$ADOPTED" plan -out=approved.tfplan -input=false -no-color 2>&1)" || { printf '%s\n' "$P_PLAN" | tail -20; fail "plan -out failed"; }
grep -qF "Plan: 0 to add, 1 to change, 0 to destroy." <<< "$P_PLAN" || { printf '%s\n' "$P_PLAN" | tail -10; fail "the saved plan is not exactly one change"; }
tag_patch a "$RG_ID" "$RG_API" stray yes || fail "could not move the world (a stray tag on the resource group) on A"
P_APPLY="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  tag_patch a "$RG_ID" "$RG_API" stray - || fail "BREAK_APPROVAL: could not put the world back"
  ( tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  UAI_JSON="$(on_a gauntlet_arm GET "$UAI_ID?api-version=$MI_API")" || fail "could not read the identity through ARM after the refusal"
  grep -q '"reviewed"' <<< "$UAI_JSON" && fail "the identity gained the reviewed tag despite the refusal: $UAI_JSON"
  tag_patch a "$RG_ID" "$RG_API" stray - || fail "could not put the world back"
  P_APPLY2="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one resource"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the identity gains tags reviewed=yes); the world then moved out of band (a stray tag on the resource group, an ARM PATCH) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the tag removed the identical file applied, 0 added, 1 changed, 0 destroyed; stock's own planfile applied on the oracle container in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 6. day2_rename: a moved block carries an untaggable's record ─────────
# The role assignment has no tags, so nothing on Azure names its address:
# the rename is its record's alone, and the cloud sees no change at all.
gauntlet_begin_stage day2_rename
log "=== 6. day2_rename: the role assignment's block renamed roleAssignments -> role_assignment with a moved block ==="
if [ "${BREAK:-}" = "1" ]; then
  REVIEWED=1 RA=role_assignment MOVED=1 RA_NAME="0f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0" write_root "$ADOPTED" live
  R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)"
  if grep -qE "Plan: 0 to add, 0 to change, 0 to destroy" <<< "$R_PLAN" || grep -q "No changes." <<< "$R_PLAN"; then
    fail "BREAK=1: the role assignment's own name changed and the plan reads zero churn - the assertion is not load-bearing"
  fi
  REVIEWED=1 RA=role_assignment MOVED=1 write_root "$ADOPTED" live
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: the real rename did not apply"
  REVIEWED=1 RA=role_assignment MOVED=1 write_root "$ORACLE" stock
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK: stock could not apply the moved block on B"
  gauntlet_stage day2_rename pass "BREAK=1 control: changing the role assignment's own name (its GUID) is an identity change and the zero-churn assertion correctly fails ($(gauntlet_plan_line <<< "$R_PLAN")); the real rename applied afterwards"
else
  REVIEWED=1 RA=role_assignment MOVED=1 write_root "$ADOPTED" live
  REVIEWED=1 RA=role_assignment MOVED=1 write_root "$ORACLE" stock
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's plan of the moved block failed on B"
  grep -qE "Plan: 0 to add, 0 to change, 0 to destroy|No changes" <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan of the moved block on B is not zero churn"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the moved block on B"
  R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the plan of the moved block failed"; }
  grep -qE "Plan: 0 to add, 0 to change, 0 to destroy|No changes" <<< "$R_PLAN" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the moved block of an untaggable resource does not plan zero churn, as stock's does ($(gauntlet_plan_line <<< "$R_PLAN"))"; }
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the rename did not apply"
  on_a gauntlet_arm GET "$RA_ID?api-version=$RA_API" >/dev/null || fail "the role assignment no longer answers at $RA_ID after the rename"
  NEW_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records/tofu-records" azapi_resource.role_assignment || true)"
  OLD_REC="$(gauntlet_record_file "$ADOPTED/.tofu-records/tofu-records" azapi_resource.roleAssignments || true)"
  [ -n "$NEW_REC" ] || fail "no record names azapi_resource.role_assignment after the rename"
  [ -z "$OLD_REC" ] || fail "a record still names azapi_resource.roleAssignments after the rename: $OLD_REC"
  grep -q "No changes." <<< "$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the rename is not empty"
  gauntlet_stage day2_rename pass "a moved block renamed the role assignment's block roleAssignments -> role_assignment: zero churn ($(gauntlet_plan_line <<< "$R_PLAN")), as stock planned with the same moved block on the oracle; the role assignment, which carries no tags, still answers at its ARM id, its record now names azapi_resource.role_assignment and none names the old address, and the replan is empty. BREAK=1 changes the role assignment's own GUID and the zero-churn assertion correctly fails"
fi

# ── 7. day2_remove: a deleted block's untaggable resource is destroyed ───
# With no tag to find it by, the destroy can only come from the record.
gauntlet_begin_stage day2_remove
log "=== 7. day2_remove: the role assignment's block is deleted ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  REVIEWED=1 RA=role_assignment write_root "$ADOPTED" live
  M_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan with the block kept failed"; }
  grep -q "azapi_resource.role_assignment will be destroyed" <<< "$M_PLAN" && fail "BREAK_REMOVE=1: the block is kept and a destroy is still proposed"
  # The real removal, on both sides, so the stages after this one start
  # from the shape they expect.
  REVIEWED=1 RA=none write_root "$ADOPTED" live
  REVIEWED=1 RA=none write_root "$ORACLE" stock
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the real removal did not apply on A"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock could not apply the removal on B"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the block kept no destroy is proposed ($(gauntlet_plan_line <<< "$M_PLAN")), so 'a destroy is proposed' correctly fails"
else
  REVIEWED=1 RA=none write_root "$ADOPTED" live
  REVIEWED=1 RA=none write_root "$ORACLE" stock
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)"
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan without the block on B is not one destroy"; }
  M_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan without the role assignment's block failed"; }
  grep -q "azapi_resource.role_assignment will be destroyed" <<< "$M_PLAN" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan does not destroy azapi_resource.role_assignment"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$M_PLAN" || fail "the plan destroys more than the role assignment: $(gauntlet_plan_line <<< "$M_PLAN")"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the destroying apply failed"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the removal on B"
  gone_a "$RA_ID" "$RA_API" || fail "ARM still answers for the role assignment after its block was removed and applied"
  gauntlet_stage day2_remove pass "the role assignment's block deleted: choudoufu planned exactly azapi_resource.role_assignment's destroy (0 add, 0 change, 1 destroy) for a resource that carries no tags, found through its record, as stock planned on the oracle, and after the apply ARM answers 404 for it"
fi

# ── 8. day2_count: a two-instance count of azapi identities ──────────────
gauntlet_begin_stage day2_count
log "=== 8. day2_count: a two-instance count of azapi identities the script declares scales 2 -> 1 -> 2 ==="
REVIEWED=1 RA=none SHARDS=2 write_root "$ADOPTED" live
REVIEWED=1 RA=none SHARDS=2 write_root "$ORACLE" stock
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock could not add the two shards on B"; }
{ APPLY_OUT="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "choudoufu could not add the two shards on A"; }
REVIEWED=1 RA=none SHARDS=1 write_root "$ADOPTED" live
REVIEWED=1 RA=none SHARDS=1 write_root "$ORACLE" stock
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  grep -q 'azapi_resource.shard\[0\] will be destroyed' <<< "$C_PLAN" && fail "BREAK_COUNT=1: shard[0] is planned for destroy - the wrong instance"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: the assertion that shard[0] is destroyed correctly fails; the plan destroys $(grep -oE 'azapi_resource.shard\[[0-9]\] will be destroyed' <<< "$C_PLAN" | head -1)"
else
  grep -q 'azapi_resource.shard\[1\] will be destroyed' <<< "$C_PLAN" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down does not destroy shard[1]"; }
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN" || fail "the scale-down is not exactly one destroy: $(gauntlet_plan_line <<< "$C_PLAN")"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-down apply failed"
  gone_a "$SHARD1_ID" "$MI_API" || fail "ARM still answers for shard 1 after the scale-down"
  gone_a "$SHARD0_ID" "$MI_API" && fail "shard 0 is gone after the scale-down; the survivor did not keep its identity"
  REVIEWED=1 RA=none SHARDS=2 write_root "$ADOPTED" live
  REVIEWED=1 RA=none SHARDS=2 write_root "$ORACLE" stock
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-up plan failed on B"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-up plan on B is not exactly one add"; }
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-up apply failed on B"
  U_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the scale-up plan failed"
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$U_PLAN" || { printf '%s\n' "$U_PLAN" | tail -20; fail "the scale-up is not exactly one create"; }
  grep -q 'azapi_resource.shard\[1\]' <<< "$U_PLAN" || fail "the scale-up does not create shard[1]"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-up apply failed"
  grep -q "No changes." <<< "$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the scale-up is not empty"
  gauntlet_stage day2_count pass "a two-instance count of azapi user-assigned identities added beside the published root (it has no count block): 2 -> 1 destroyed exactly azapi_resource.shard[1] (ARM answers 404 for it, shard 0 kept its id); 1 -> 2 created exactly one, at shard[1]; the replan is empty; stock's plans for the same changes on the oracle container have the same shape. BREAK_COUNT=1 asserts the lower index was destroyed and correctly fails"
fi

# ── 9, 10: not written for this substrate yet ────────────────────────────
gauntlet_stage day2_replace not_run "not yet written for the azure lane (#1120): the shape is a create_before_destroy resource whose name changes, as on kind and as on reference-azure"
gauntlet_stage day2_crash not_run "not yet written for the azure lane (#1120): needs the e2e build's interrupt hook on an apply creating two resources, as the floci and kind estates do"

# ── 11. day2_teardown: destroy everything, nothing left marked ───────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy ==="
T_OUT="$(tofu_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
LEFT="$(count_a)" || fail "could not count A's marked resources after the destroy"
[ "$LEFT" = "0" ] || fail "$LEFT resource(s) still carry tofu-estate=$ESTATE after apply -destroy"
gone_a "$RG_ID" "$RG_API" || fail "the resource group still answers after apply -destroy"
gone_a "$SHARD0_ID" "$MI_API" || fail "shard 0 still answers after apply -destroy"
gauntlet_stage day2_teardown pass "apply -destroy removed every resource in one apply ($(grep -oE 'Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$T_OUT" | head -1)); nothing carries tofu-estate=$ESTATE and the resource group answers 404"

# ── 13. greenfield: the same root applied fresh, compared with stock ─────
gauntlet_begin_stage greenfield
log "=== 13. greenfield: choudoufu applies the published root on an empty container A ==="
write_root "$GREEN" live
( tofu_a "$GREEN" init -input=false -no-color >/dev/null 2>&1 ) || fail "greenfield init failed"
G_OUT="$(tofu_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$G_OUT" | tail -20; fail "the greenfield apply failed"; }
grep -qF "Apply complete! Resources: 3 added, 0 changed, 0 destroyed" <<< "$G_OUT" || { printf '%s\n' "$G_OUT" | tail -5; fail "the greenfield apply did not add exactly 3 resources"; }
DROP=""; [ "${BREAK:-}" = "1" ] && DROP="/roleassignments/${RA_GUID}"
inventory a > "$WORK/inventory.green.json" || fail "could not read the greenfield inventory"
if [ -n "$DROP" ]; then
  inventory a "$DROP" > "$WORK/inventory.expected.json"
  cmp -s "$WORK/inventory.expected.json" "$WORK/inventory.stock.json" && fail "BREAK=1: an inventory missing the role assignment still matches stock's - the comparison is not load-bearing"
  gauntlet_stage greenfield pass "BREAK=1 control: dropping the role assignment from the expected inventory makes the resource-by-resource match correctly fail"
elif cmp -s "$WORK/inventory.green.json" "$WORK/inventory.stock.json"; then
  gauntlet_stage greenfield pass "3 resources from one plain apply with a live block; read through ARM, resource by resource with the marker tags normalised out (and the role assignment's principal, which the server mints, left out), they match what stock's cold deploy made"
else
  gauntlet_stage greenfield fail "the greenfield inventory differs from stock's cold deploy: $(diff "$WORK/inventory.stock.json" "$WORK/inventory.green.json" | head -10 | tr '\n' ' ')"
fi
( tofu_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (the greenfield estate's destroy failed; the container is removed on exit)"

gauntlet_stage strict not_run "not yet written for the azure lane (#1120)"
gauntlet_stage no_local_state not_run "not yet written for the azure lane (#1120): the plan with no local state reaches the Resource Graph sweep, whose query floci-az serves since 30000b0 but its KQL subset refuses (lex00/floci-az#11), and the role assignment, untaggable, could only be found through its record"

gauntlet_end
