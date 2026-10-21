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
# roots): it is the one floci-az at 30000b0 served as written. Role
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
#   2. both provider blocks gain the endpoint lock's arguments (#2133,
#      live/e2e/lib/floci-az.sh's floci_az_azurerm_args and
#      floci_az_azapi_args): floci-az's subscription, because identity reads
#      the subscription from the provider block (#1120's corrections) and
#      azurerm 5 requires one, and every endpoint and credential argument,
#      so nothing in the operator's shell can aim either provider anywhere
#      but floci-az; azurerm's metadata_host is var.floci_az_host, declared
#      in floci-az.tf; the root's own skip_provider_registration = false is
#      kept;
#   3. choudoufu's roots get a live block, in a file of its own (live.tf),
#      whose records go to a blob container on floci-az A,
#      record_store "azurerm" (the maintainer's ruling for the lane: records
#      live in the record store and the state file is a cache). The storage
#      account (versioning on, a previous-versions lifecycle rule, public
#      access off, the three settings the store refuses an account without)
#      is made with ARM calls before migrate, in a resource group of its own
#      outside the estate, with one container per root: adopted, green and
#      strict (gauntlet_floci_az_record_store in live/e2e/lib/gauntlet.sh).
#      No root holds a .tofu-records directory; a record is read back by
#      copying the container's blobs out (gauntlet_floci_az_blob_mirror).
# Stage additions, each in the stage that needs it: plan_approval gives the
# identity tags = { reviewed = "yes" }; day2_rename renames the role
# assignment's block (roleAssignments -> role_assignment) with a moved
# block in moved.tf, which every later stage keeps; day2_remove deletes
# the role assignment's block; day2_count declares a
# two-instance count of azapi user-assigned identities in shards.tf, since
# the published root has no count block; day2_replace declares, in
# replace.tf, an azapi identity whose name carries a generation and a role
# assignment granting it AcrPull, both create_before_destroy, since the
# published root has no create_before_destroy block, and removes them after;
# day2_crash declares, in crash.tf, a role assignment granting the root's
# identity AcrPull (crash_first, untaggable, held only by its record) and an
# azapi identity that depends on it (crash_second), which teardown destroys.
#
# .corpus is read, never written: the root is copied out per run. Two
# floci-az containers, both started for this run and removed after it, as
# reference-azure: A holds the estate (stock cold-deploys it, choudoufu
# adopts it and runs the day-2 stages, tears it down, then applies the same
# root fresh with a live block); B is the oracle, where stock applies the
# identical root and every day-2 change itself.
#
# Run for #1120: cold_deploy and migrate pass on floci-az 30000b0.
# floci-az's gaps (live/FLOCI.md, Azure) show by name rather than being
# skipped. At 30000b0 the plan's Resource Graph sweep was refused
# (lex00/floci-az#11), which choudoufu reports as the "Azure sweep
# unavailable" warning, an incomplete sweep, and only the sweep finds a
# removed resource of a type that takes tags, so day2_count's scale-down
# could not pass. The pin is now dfa33ca, which runs the query as sent, so
# it is expected to. The role assignment's removal comes from its record,
# so day2_remove does not need the sweep. A stage that fails records the reason and the run goes on
# where it can, the shape the later stages expect put back by hand.
#
# Written for #1120 after the maintainer's run of 2026-10-21 at 31639780dd
# passed every stage above on ghcr.io/lex00/floci-az@sha256:a417b393...,
# and not yet run: day2_replace, day2_crash, no_local_state and strict, and
# the move of every root's records from .tofu-records to the blob container.
# Each stage's own section says what it asserts and what stock does on B.
#
#   day2_replace    the replace.tf pair renamed a -> b: stock's replace
#                   (2 add, 2 destroy, create first) on both, at
#                   -parallelism=1 the log shows each create before its
#                   deposed destroy, ARM answers 404 for both old objects,
#                   the grant's record names its new id, the replan is
#                   empty; the old identity is bound by its marker and
#                   record, the old grant by its record alone.
#   day2_crash      the crash.tf pair applied by the e2e build with
#                   TOFU_E2E_APPLY_RESOURCE_INTERRUPT=azapi_resource.crash_first
#                   at -parallelism=1: crash_first exists with its record in
#                   the container, crash_second does not; the next plan is
#                   exactly crash_second's create, stock's plan from the same
#                   position on B.
#   no_local_state  on the greenfield root, whose records are in the green
#                   container: the cache-serving plan is empty, then with the
#                   cache deleted (the only local artifact) the plan creates,
#                   destroys and replaces nothing, the role assignment found
#                   through its remote record; plan calls counted on the wire
#                   from floci-az A's access log.
#   strict          every strict toggle on against a scratch estate carrying
#                   random_password.db: exactly the secrets refusal, as every
#                   other lane asserts.
#
#   go run ./tools/gauntlet run corpus-azapi-role-assignment   # the maintainer, by hand
#   bash live/e2e/corpus-azapi-role-assignment/run.sh           # asks for "run" at the terminal
#
# Needs `just corpus-fetch` first, then docker, curl, terraform (the stock
# binary), python3 and Go on PATH. On macOS run
# scripts/floci-az-mac-cert.sh once and trust the certificate it prints in
# the keychain (Go there ignores SSL_CERT_FILE); live/FLOCI.md says how.
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
#   BREAK_REPLACE  set to 1 to recreate the old rotating identity after the
#                  replace, carrying both markers for azapi_resource.rotating,
#                  and assert the next plan is empty and silent about it
#                  (day2_replace's Break line); must fail.
#   BREAK_CRASH    set to 1 to assert nothing is proposed after the interrupt
#                  (day2_crash's Break line); must fail.
#   BREAK_CRASH_UNBOUND set to 1 to delete crash_first's record, its only
#                  marker, after the interrupt; the recovery check must fail.
#   BREAK_NO_LOCAL_STATE set to 1 to strip the identity's two markers before
#                  the plan with no local state; the check must fail.
#   BREAK_STRICT   set to 1 to turn secrets back to "store"; the refusal must
#                  disappear.
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
# The script's own GUIDs: day2_replace's grant at generation a and b, and
# day2_crash's crash_first. Each grants a principal AcrPull at the group,
# and none shares a principal with another live assignment, which ARM
# would refuse as a duplicate.
GRANT_GUID_A="3b0e6f1c-5a2d-4c8e-9f17-2d6a4b8c0e1a"
GRANT_GUID_B="7c4d2e9a-1b3f-4a6c-8d5e-9f0a1b2c3d4e"
CRASH_GUID="5e8a1c3b-7d2f-4b6e-a9c1-0f3e5d7b9a2c"
# The record store, on A only: an account outside the estate, one container
# per root.
REC_RG="choudoufu-records"; REC_ACCOUNT="choudoufuazrarecords"
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

# day2_crash needs a build with e2eTestingFeatures set, the ldflags gate the
# engine's TOFU_E2E_APPLY_RESOURCE_INTERRUPT hook sits behind
# (internal/command/apply_e2etesting_crash.go), as reference-k8s and
# reference-ec2-vpc build it: from THIS tree whatever $TOFU came from, and
# unconditionally, since the real check and both Break controls need it.
# Identical to $TOFU everywhere else: the hook is a no-op unless the
# variable names an address the run applies.
mkdir -p "$WORK/bin"
TOFU_CRASH="$WORK/bin/choudoufu-e2e"
( cd "$ROOT" && env -u PWD go build -ldflags="-X 'main.e2eTestingFeatures=yes'" -o "$TOFU_CRASH" ./cmd/choudoufu ) \
  || fail "go build -ldflags e2eTestingFeatures ./cmd/choudoufu failed"
log "  built $TOFU_CRASH (e2eTestingFeatures=yes, for day2_crash's interrupt)"

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
#   REPLACE  day2_replace's generation, a or b, for replace.tf; empty writes
#            none
#   CRASH    day2_crash's pair for crash.tf: first (crash_first alone, the
#            oracle's position), both, or empty for none
# and, for live mode, RECORD_STORE_HCL, the record_store block live.tf
# carries (gauntlet_floci_az_record_store_block); live mode without one
# fails rather than write a root whose records go nowhere the ruling allows.
#
# The directory itself is kept: write_root replaces the files it writes and
# removes moved.tf, shards.tf, replace.tf and crash.tf when the shape has
# none, and leaves everything else where it is. Every stage after
# cold_deploy and migrate rewrites a root that holds .terraform (with the
# state cache), the lock file and, on the oracle, stock's terraform.tfstate.
# Clearing the directory, as this did until #1120's stage audit, threw them
# away at plan_approval: the next plan had no providers installed and the
# oracle lost its state. (The role assignment's record, its only marker,
# was in the directory too then; it is in the blob container now.)
write_root() {
  local dst="$1" mode="$2"
  mkdir -p "$dst" || fail "could not make $dst"
  cp "$SRC/main.tf" "$dst/main.tf" || fail "could not copy the root out of .corpus"
  local azurerm_args azapi_args host_variable
  azurerm_args="$(gauntlet_floci_az_azurerm_args)" || fail "could not render the azurerm provider block's endpoint lock"
  azapi_args="$(gauntlet_floci_az_azapi_args)" || fail "could not render the azapi provider block's endpoint lock"
  host_variable="$(gauntlet_floci_az_host_variable)" || fail "could not render the floci_az_host variable"
  DST="$dst" AZURERM_ARGS="$azurerm_args" AZAPI_ARGS="$azapi_args" HOST_VARIABLE="$host_variable" ROOT_GUID="$RA_GUID" \
    AZAPI_REQ="$AZAPI_REQUIRED_PROVIDER" AZURERM_REQ="$AZURERM_REQUIRED_PROVIDER" \
    RA="${RA:-roleAssignments}" RA_NAME="${RA_NAME:-$RA_GUID}" REVIEWED="${REVIEWED:-0}" \
    MOVED="${MOVED:-0}" SHARDS="${SHARDS:-0}" REPLACE="${REPLACE:-}" CRASH="${CRASH:-}" \
    GRANT_GUID_A="${GRANT_GUID_A:-}" GRANT_GUID_B="${GRANT_GUID_B:-}" CRASH_GUID="${CRASH_GUID:-}" \
    python3 - <<'PY' || fail "a delta did not match the published root - the corpus pin has moved"
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
# Delta 2: the endpoint lock's arguments in both provider blocks.
once('provider "azurerm" {\n  features {\n  }\n}\n', 'provider "azurerm" {\n' + env["AZURERM_ARGS"] + '\n  features {\n  }\n}\n', "delta 2 (azurerm provider)")
once('provider "azapi" {\n  skip_provider_registration = false\n}\n',
     'provider "azapi" {\n' + env["AZAPI_ARGS"] + '\n  skip_provider_registration = false\n}\n', "delta 2 (azapi provider)")

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
open(os.path.join(env["DST"], "floci-az.tf"), "w").write(
    "# Delta 2, added by live/e2e/corpus-azapi-role-assignment/run.sh: the host\n"
    "# azurerm's metadata_host reads, from each container's TF_VAR_floci_az_host.\n" + env["HOST_VARIABLE"] + "\n")

def drop(name):
    p = os.path.join(env["DST"], name)
    if os.path.exists(p):
        os.remove(p)

if env["MOVED"] == "1":
    open(os.path.join(env["DST"], "moved.tf"), "w").write(
        "# Added by live/e2e/corpus-azapi-role-assignment/run.sh for day2_rename.\n"
        "moved {\n  from = azapi_resource.roleAssignments\n  to   = azapi_resource.role_assignment\n}\n")
else:
    drop("moved.tf")
shards = int(env["SHARDS"])
if shards == 0:
    drop("shards.tf")
else:
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

gen = env["REPLACE"]
if gen == "":
    drop("replace.tf")
elif gen in ("a", "b"):
    guid = env["GRANT_GUID_A"] if gen == "a" else env["GRANT_GUID_B"]
    if not guid:
        sys.exit("REPLACE=%s needs GRANT_GUID_A and GRANT_GUID_B" % gen)
    open(os.path.join(env["DST"], "replace.tf"), "w").write(
        "# Added by live/e2e/corpus-azapi-role-assignment/run.sh for day2_replace:\n"
        "# the published root has no create_before_destroy block. An identity whose\n"
        "# name carries a generation, and a role assignment granting it AcrPull;\n"
        "# a new generation replaces both, create first.\n"
        'resource "azapi_resource" "rotating" {\n'
        '  type                   = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"\n'
        '  name                   = "${var.resource_name}-rotating-%s"\n'
        "  parent_id              = azapi_resource.resourceGroup.id\n"
        "  location               = var.location\n"
        '  response_export_values = ["properties.principalId"]\n'
        "  lifecycle {\n"
        "    create_before_destroy = true\n"
        "  }\n"
        "}\n"
        "\n"
        'resource "azapi_resource" "rotating_grant" {\n'
        '  type      = "Microsoft.Authorization/roleAssignments@2022-04-01"\n'
        '  name      = "%s"\n'
        "  parent_id = azapi_resource.resourceGroup.id\n"
        "  body = {\n"
        "    properties = {\n"
        "      principalId      = azapi_resource.rotating.output.properties.principalId\n"
        '      principalType    = "ServicePrincipal"\n'
        "      roleDefinitionId = data.azurerm_role_definition.roleAcrpull.id\n"
        "    }\n"
        "  }\n"
        "  lifecycle {\n"
        "    create_before_destroy = true\n"
        "  }\n"
        "}\n" % (gen, guid))
else:
    sys.exit("REPLACE=%s is not a, b or empty" % gen)

crash = env["CRASH"]
if crash == "":
    drop("crash.tf")
elif crash in ("first", "both"):
    if not env["CRASH_GUID"]:
        sys.exit("CRASH=%s needs CRASH_GUID" % crash)
    body = (
        "# Added by live/e2e/corpus-azapi-role-assignment/run.sh for day2_crash: a\n"
        "# role assignment, which takes no tags and is held only by its record,\n"
        "# and an identity created after it.\n"
        'resource "azapi_resource" "crash_first" {\n'
        '  type      = "Microsoft.Authorization/roleAssignments@2022-04-01"\n'
        '  name      = "%s"\n'
        "  parent_id = azapi_resource.resourceGroup.id\n"
        "  body = {\n"
        "    properties = {\n"
        "      principalId      = azurerm_user_assigned_identity.uai.principal_id\n"
        '      principalType    = "ServicePrincipal"\n'
        "      roleDefinitionId = data.azurerm_role_definition.roleAcrpull.id\n"
        "    }\n"
        "  }\n"
        "}\n" % env["CRASH_GUID"])
    if crash == "both":
        body += (
            "\n"
            'resource "azapi_resource" "crash_second" {\n'
            '  type       = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"\n'
            '  name       = "${var.resource_name}-crash-second"\n'
            "  parent_id  = azapi_resource.resourceGroup.id\n"
            "  location   = var.location\n"
            "  depends_on = [azapi_resource.crash_first]\n"
            "}\n")
    open(os.path.join(env["DST"], "crash.tf"), "w").write(body)
else:
    sys.exit("CRASH=%s is not first, both or empty" % crash)
PY
  if [ "$mode" = "live" ]; then
    [ -n "${RECORD_STORE_HCL:-}" ] || fail "write_root $dst live: no RECORD_STORE_HCL, so the root would have no record store"
    cat > "$dst/live.tf" <<EOF
# Delta 3, added by live/e2e/corpus-azapi-role-assignment/run.sh: the live
# block, its records in a blob container on floci-az A.
terraform {
  live {
    estate = "$ESTATE"
$RECORD_STORE_HCL
  }
}
EOF
  fi
}

# ── substrate helpers ────────────────────────────────────────────────────
# As reference-azure: each container's ARM_* values are saved after
# gauntlet_floci_az_up and every command runs under one of the two.
save_env() { env | grep -E '^(FLOCI_AZ_(HOST|ENDPOINT|CA_FILE)|ARM_[A-Z_]+|SSL_CERT_FILE|AZURE_CONFIG_DIR|TF_VAR_floci_az_host)=' > "$1"; }
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
# day2_crash's pair, which stays declared through teardown.
CRASH_ID="${RG_ID}/providers/Microsoft.Authorization/roleAssignments/${CRASH_GUID}"
CRASH2_ID="${RG_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${RG}-crash-second"
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
# tag_set <a|b> <id> <api> <key> <value|-> sets or removes one tag and
# keeps the rest, with an ARM GET and a PUT of the body read back
# (gauntlet_arm_set_tag): floci-az serves no PATCH on Microsoft.Network.
tag_set() { "on_$1" gauntlet_arm_set_tag "$2?api-version=$3" "$4" "$5" >/dev/null; }
gone_a() { ! on_a gauntlet_arm GET "$1?api-version=$2" >/dev/null 2>&1; }
# managed_count <dir>: the managed resources in a stock state, the data
# source left out.
managed_count() { ( cd "$1" && terraform state list | grep -vc '^data\.' ); }
# record_file <container> <address>: the path of a copy of the one record
# whose envelope is for <address> in A's blob container <container>, or
# nothing with status 1. Each call copies the container afresh into a new
# directory under $WORK (gauntlet_floci_az_blob_mirror), so what it reads is
# what the store holds now.
record_file() {
  local d
  d="$(mktemp -d "$WORK/records.XXXXXX")" || return 1
  on_a gauntlet_floci_az_blob_mirror "$REC_ACCOUNT" "$1" "$d" >/dev/null || return 1
  gauntlet_record_file "$d" "$2"
}
# names_id <record-file> <arm-id>: the record names <arm-id> as its current
# object, compared as ARM compares ids, without regard to case.
names_id() {
  local got
  got="$(gauntlet_record_identity "$1" 2>/dev/null)" || return 1
  [ "$(printf '%s' "$got" | tr '[:upper:]' '[:lower:]')" = "$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')" ]
}
# record_blob <container> <address>: the blob name of that record, for
# gauntlet_floci_az_blob_delete.
record_blob() {
  local p
  p="$(record_file "$@")" || return 1
  printf '%s\n' "${p#"$WORK"/records.??????/}"
}

# ── 1. cold_deploy: stock stands the root up on A (and B, the oracle) ────
gauntlet_begin_stage cold_deploy
log "=== 1. cold_deploy: two floci-az containers, stock terraform applies the published root on each ==="
mkdir -p "$WORK/a" "$WORK/b"
# A logs every request it serves (GAUNTLET_FLOCI_AZ_ACCESS_LOG), which is
# how no_local_state counts its plans' calls on the wire.
GAUNTLET_FLOCI_AZ_ACCESS_LOG=1 gauntlet_floci_az_up "$CONTAINER_A" "$WORK/a" || fail "floci-az container A ($CONTAINER_A) did not come up"
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
gauntlet_stage cold_deploy pass "3 resources (an azapi resource group, an azurerm user-assigned identity, an azapi role assignment granting it AcrPull, read through data \"azurerm_role_definition\") from plain terraform on the published root plus its deltas (the lane's provider pins, the endpoint lock in both provider blocks) against floci-az $(cat "$ROOT/live/floci-az-image"); a real terraform.tfstate with 3 managed instances, zero tofu-estate tags read back through ARM, the role assignment answering at its id; the identical root cold-deployed by stock on a second container as every later stage's oracle"

# ── 2. migrate: choudoufu live-import against stock's state ──────────────
gauntlet_begin_stage migrate
log "=== 2. migrate: live-import against the stock state file, read-only then -approve ==="
# The record store, before anything reads it: the operator's account on A,
# made with ARM calls, one container per choudoufu root.
on_a gauntlet_floci_az_record_store "$REC_RG" "$REC_ACCOUNT" adopted green strict \
  || fail "could not make the record store's storage account on A"
RS_ADOPTED="$(on_a gauntlet_floci_az_record_store_block "$REC_ACCOUNT" adopted "$REC_RG")" || fail "could not render the adopted root's record_store block"
RS_GREEN="$(on_a gauntlet_floci_az_record_store_block "$REC_ACCOUNT" green "$REC_RG")" || fail "could not render the greenfield root's record_store block"
RS_STRICT="$(on_a gauntlet_floci_az_record_store_block "$REC_ACCOUNT" strict "$REC_RG")" || fail "could not render the strict root's record_store block"
RECORD_STORE_HCL="$RS_ADOPTED"
write_root "$ADOPTED" live
( tofu_a "$ADOPTED" init -input=false -no-color >/dev/null 2>&1 ) || fail "adopted init failed"
IMPORT_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" || { printf '%s\n' "$IMPORT_OUT" | tail -20; fail "live-import (dry run) failed"; }
log "  dry run: $(grep -E 'resource instance\(s\) are eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
APPROVE_OUT="$(tofu_a "$ADOPTED" live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve failed"; }
SUMMARY_LINE="$(grep -E 'resource\(s\) newly stamped' <<< "$APPROVE_OUT" | head -1 | sed 's/\.$//')"
log "  approve: ${SUMMARY_LINE:-no summary line}"
TAGGED="$(count_a)" || fail "could not read A's resources through ARM"
RA_RECORD="$(record_file adopted azapi_resource.roleAssignments || true)"
# The role assignment takes no tags at any API version: live-import reports
# it UNTAGGABLE and SKIPPED (the marker write is what was skipped) and
# records its identity, as it does an untaggable AWS type's. So it alone is
# skipped, and any other skip is a failure named with its reason (#1120's
# first run skipped the azapi resource group too, with nothing said).
SKIPPED_ADDRS="$(gauntlet_live_import_skipped_addrs <<< "$APPROVE_OUT")"
WANT_SKIPPED="azapi_resource.roleAssignments"
if grep -qE ' 0 failed, 1 skipped' <<< "$SUMMARY_LINE" && [ "$SKIPPED_ADDRS" = "$WANT_SKIPPED" ] && [ "$TAGGED" = "2" ] && [ -n "$RA_RECORD" ]; then
  gauntlet_stage migrate pass "$SUMMARY_LINE; both taggable resources (the resource group and the identity) carry tofu-estate=$ESTATE, read back through ARM, and the role assignment, which has no tags at any API version, is the 1 skipped and is held by its record, read back from the record store's blob container on A"
else
  gauntlet_stage migrate fail "live-import -approve: ${SUMMARY_LINE:-no summary line}; $TAGGED of 2 taggable resources carry tofu-estate=$ESTATE through ARM; role assignment record: ${RA_RECORD:-none}; skipped (want only $WANT_SKIPPED): \"$(gauntlet_live_import_skips <<< "$APPROVE_OUT")\"; first Error: line: \"$(gauntlet_first_error_line <<< "$APPROVE_OUT")\""
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
SWEEP_WARN=""; grep -qF "Azure sweep unavailable" <<< "$PLAN_OUT" && SWEEP_WARN="; the plan warned \"Azure sweep unavailable\", so the sweep was incomplete, though floci-az runs its query as sent since dfa33ca (lex00/floci-az#11)"
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
log "=== 5. drift_reconverge: an ARM PUT adds a tag to the identity on A and on B; stock's plan on B is the oracle ==="
tag_set a "$UAI_ID" "$MI_API" drift yes || fail "could not tamper the identity on A"
tag_set b "$UAI_ID" "$MI_API" drift yes || fail "could not tamper the identity on B"
if [ "${BREAK:-}" = "1" ]; then
  tag_set a "$RG_ID" "$RG_API" drift yes || fail "BREAK: could not tamper the resource group on A"
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
  gauntlet_stage drift_reconverge pass "a tag added to the identity with an ARM GET and PUT; choudoufu proposed exactly azurerm_user_assigned_identity.uai (0 add, 1 change, 0 destroy), matching stock's own plan on the oracle container for the same tamper; apply changed 1 and the tag is gone, read through ARM. BREAK=1 tampers the resource group too and the single-resource assertion correctly fails"
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
tag_set a "$RG_ID" "$RG_API" stray yes || fail "could not move the world (a stray tag on the resource group) on A"
P_APPLY="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)"; P_RC=$?
if [ "${BREAK_APPROVAL:-}" = "1" ]; then
  [ "$P_RC" -eq 0 ] && fail "BREAK_APPROVAL=1: applying the saved plan after the world moved succeeded - the refusal is not load-bearing"
  tag_set a "$RG_ID" "$RG_API" stray - || fail "BREAK_APPROVAL: could not put the world back"
  ( tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: the saved plan did not apply once the world was put back"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_APPROVAL: stock could not apply the reviewed change on B"
  gauntlet_stage plan_approval pass "BREAK_APPROVAL=1 control: applying the saved plan after the world moved exited $P_RC (refused), so the stage's own Break line correctly fails; applied once the world was put back"
else
  [ "$P_RC" -eq 3 ] || { printf '%s\n' "$P_APPLY" | tail -20; fail "apply of the saved plan after the world moved exited $P_RC, want 3 (the refusal)"; }
  grep -qF "The approved plan no longer matches the live system" <<< "$P_APPLY" || { printf '%s\n' "$P_APPLY" | tail -20; fail "the refusal does not carry its documented sentence"; }
  UAI_JSON="$(on_a gauntlet_arm GET "$UAI_ID?api-version=$MI_API")" || fail "could not read the identity through ARM after the refusal"
  grep -q '"reviewed"' <<< "$UAI_JSON" && fail "the identity gained the reviewed tag despite the refusal: $UAI_JSON"
  tag_set a "$RG_ID" "$RG_API" stray - || fail "could not put the world back"
  P_APPLY2="$(tofu_a "$ADOPTED" apply -input=false -no-color approved.tfplan 2>&1)" || { printf '%s\n' "$P_APPLY2" | tail -20; fail "the saved plan did not apply once the world was put back"; }
  grep -qF "Apply complete! Resources: 0 added, 1 changed, 0 destroyed" <<< "$P_APPLY2" || fail "the saved plan's apply did not change exactly one resource"
  ( stock_b plan -out=approved.tfplan -input=false -no-color >/dev/null 2>&1 && stock_b apply -input=false -no-color approved.tfplan >/dev/null 2>&1 ) || fail "stock's own planfile did not apply on B"
  gauntlet_stage plan_approval pass "plan -out wrote one update (the identity gains tags reviewed=yes); the world then moved out of band (a stray tag on the resource group, an ARM PUT) and apply of the saved plan refused with \"The approved plan no longer matches the live system\" at exit 3, nothing applied; with the tag removed the identical file applied, 0 added, 1 changed, 0 destroyed; stock's own planfile applied on the oracle container in the unchanged case. BREAK_APPROVAL=1 expects success after the move and correctly fails"
fi

# ── 6. day2_rename: a moved block carries an untaggable's record ─────────
# The role assignment has no tags, so nothing on Azure names its address:
# the rename is its record's alone, and the cloud sees no change at all.
# The plan finds the record under the old address through the moved block
# (internal/live/projection/located.go, locatedIdentityWithAliases, which
# the record-first read uses); the apply writes it under the new address
# and retires the old key when it names the same object
# (internal/live/projection/writeback.go, retireMovedRecords). The gate
# asserts the new record; it does not assert the old one's absence.
# moved.tf stays in every stage after this one, as Terraform's own guidance
# keeps a moved block, so that an old key left by an earlier build or an
# interrupted apply still resolves to role_assignment and day2_remove does
# not read two records of one object as two removed blocks.
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
  NEW_REC="$(record_file adopted azapi_resource.role_assignment || true)"
  [ -n "$NEW_REC" ] || fail "no record names azapi_resource.role_assignment after the rename"
  grep -q "No changes." <<< "$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || fail "the replan after the rename is not empty"
  gauntlet_stage day2_rename pass "a moved block renamed the role assignment's block roleAssignments -> role_assignment: zero churn ($(gauntlet_plan_line <<< "$R_PLAN")), as stock planned with the same moved block on the oracle; the role assignment, which carries no tags, still answers at its ARM id, a record in the blob container now names azapi_resource.role_assignment, and the replan is empty. BREAK=1 changes the role assignment's own GUID and the zero-churn assertion correctly fails"
fi

# ── 7. day2_remove: a deleted block's untaggable resource is destroyed ───
# With no tag to find it by, the destroy can only come from the record.
# moved.tf stays (see day2_rename), so any record still under the old
# address resolves to role_assignment and the one object is proposed once,
# at the address its block last had.
#
# A failure here is recorded and the run goes on: the role assignment is
# removed by hand on A (and by stock on B), so the stages after this one
# start from the shape they expect and are measured rather than lost.
gauntlet_begin_stage day2_remove
log "=== 7. day2_remove: the role assignment's block is deleted ==="
if [ "${BREAK_REMOVE:-}" = "1" ]; then
  REVIEWED=1 RA=role_assignment MOVED=1 write_root "$ADOPTED" live
  M_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan with the block kept failed"; }
  grep -q "azapi_resource.role_assignment will be destroyed" <<< "$M_PLAN" && fail "BREAK_REMOVE=1: the block is kept and a destroy is still proposed"
  # The real removal, on both sides, so the stages after this one start
  # from the shape they expect.
  REVIEWED=1 RA=none MOVED=1 write_root "$ADOPTED" live
  REVIEWED=1 RA=none MOVED=1 write_root "$ORACLE" stock
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: the real removal did not apply on A"
  ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_REMOVE: stock could not apply the removal on B"
  gauntlet_stage day2_remove pass "BREAK_REMOVE=1 control: with the block kept no destroy is proposed ($(gauntlet_plan_line <<< "$M_PLAN")), so 'a destroy is proposed' correctly fails"
else
  REVIEWED=1 RA=none MOVED=1 write_root "$ADOPTED" live
  REVIEWED=1 RA=none MOVED=1 write_root "$ORACLE" stock
  O_PLAN="$(stock_b plan -input=false -no-color 2>&1)"
  grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's plan without the block on B is not one destroy"; }
  M_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$M_PLAN" | tail -20; fail "the plan without the role assignment's block failed"; }
  if grep -q "azapi_resource.role_assignment will be destroyed" <<< "$M_PLAN" && grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$M_PLAN"; then
    ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the destroying apply failed"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the removal on B"
    gone_a "$RA_ID" "$RA_API" || fail "ARM still answers for the role assignment after its block was removed and applied"
    gauntlet_stage day2_remove pass "the role assignment's block deleted: choudoufu planned exactly azapi_resource.role_assignment's destroy (0 add, 0 change, 1 destroy) for a resource that carries no tags, found through its record, as stock planned on the oracle, and after the apply ARM answers 404 for it"
  else
    printf '%s\n' "$M_PLAN" | tail -20
    gauntlet_stage day2_remove fail "the plan without the role assignment's block does not destroy exactly azapi_resource.role_assignment ($(gauntlet_plan_line <<< "$M_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$M_PLAN")\")$(gauntlet_azure_sweep_note <<< "$M_PLAN")"
    log "  removing the role assignment by hand on A and through stock on B, so the stages below start from the shape they expect"
    ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not apply the removal on B"
    on_a gauntlet_arm DELETE "$RA_ID?api-version=$RA_API" >/dev/null || fail "could not delete the role assignment on A through ARM after day2_remove failed"
  fi
fi

# ── 8. day2_count: a two-instance count of azapi identities ──────────────
# The shards take tags, so the one that leaves the count is found by its
# marker through the Resource Graph sweep and by nothing else: the record
# leg skips an instance whose ARM type takes tags. floci-az runs the
# sweep's query since dfa33ca (lex00/floci-az#11), so the destroy is
# expected; a scale-down that plans none fails the stage naming the
# warning. The run then goes on with both shards still standing and the
# configuration back at two, the shape the teardown expects (the oracle
# stays at one; nothing after this stage reads it).
gauntlet_begin_stage day2_count
log "=== 8. day2_count: a two-instance count of azapi identities the script declares scales 2 -> 1 -> 2 ==="
REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root "$ADOPTED" live
REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root "$ORACLE" stock
{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "stock could not add the two shards on B"; }
{ APPLY_OUT="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "choudoufu could not add the two shards on A"; }
REVIEWED=1 RA=none MOVED=1 SHARDS=1 write_root "$ADOPTED" live
REVIEWED=1 RA=none MOVED=1 SHARDS=1 write_root "$ORACLE" stock
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || fail "stock's scale-down plan failed on B"
grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's scale-down plan on B is not exactly one destroy"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's scale-down apply failed on B"
C_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$C_PLAN" | tail -20; fail "the scale-down plan failed"; }
if [ "${BREAK_COUNT:-}" = "1" ]; then
  grep -q 'azapi_resource.shard\[0\] will be destroyed' <<< "$C_PLAN" && fail "BREAK_COUNT=1: shard[0] is planned for destroy - the wrong instance"
  gauntlet_stage day2_count pass "BREAK_COUNT=1 control: the assertion that shard[0] is destroyed correctly fails; the plan destroys $(grep -oE 'azapi_resource.shard\[[0-9]\] will be destroyed' <<< "$C_PLAN" | head -1)"
elif ! { grep -q 'azapi_resource.shard\[1\] will be destroyed' <<< "$C_PLAN" && grep -qF "Plan: 0 to add, 0 to change, 1 to destroy." <<< "$C_PLAN"; }; then
  printf '%s\n' "$C_PLAN" | tail -20
  gauntlet_stage day2_count fail "the scale-down 2 -> 1 does not destroy exactly azapi_resource.shard[1] ($(gauntlet_plan_line <<< "$C_PLAN"); first Error: line: \"$(gauntlet_first_error_line <<< "$C_PLAN")\")$(gauntlet_azure_sweep_note <<< "$C_PLAN")"
  log "  shard[1] is still standing; the configuration goes back to two shards for the stages below"
  REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root "$ADOPTED" live
else
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "the scale-down apply failed"
  gone_a "$SHARD1_ID" "$MI_API" || fail "ARM still answers for shard 1 after the scale-down"
  gone_a "$SHARD0_ID" "$MI_API" && fail "shard 0 is gone after the scale-down; the survivor did not keep its identity"
  REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root "$ADOPTED" live
  REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root "$ORACLE" stock
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

# late_root <dir> <live|stock>: the shape every stage from day2_count on
# writes, with whatever REPLACE or CRASH the caller adds in front.
late_root() { REVIEWED=1 RA=none MOVED=1 SHARDS=2 write_root "$@"; }
# run_isolated <stage> <function> [recover]: runs a stage written after the
# 2026-10-21 run in a subshell, so a fail() inside it records that stage's
# verdict and ends the subshell rather than the run, and the stages after it
# are still measured (day2_remove and day2_count record a failure and go on
# for the same reason). The subshell inherits no EXIT trap, so it cannot
# tear the containers down. A body that ends without any verdict line is
# given one. [recover] then puts the shape the later stages expect back. The
# stage clock is restarted after, since the subshell's own advance of it is
# lost with the subshell.
run_isolated() {
  local id="$1" body="$2" recover="${3:-}" out="$WORK/stage-$1.out" rc
  ( "$body" ) | tee "$out"; rc=${PIPESTATUS[0]}
  if ! grep -q "^GAUNTLET stage=$id verdict=" "$out"; then
    gauntlet_stage "$id" fail "the stage ended (exit $rc) without reporting a verdict; its log is above"
  fi
  if [ "$rc" -ne 0 ] && [ -n "$recover" ]; then
    log "  $id stopped early (exit $rc); putting the shape back for the stages below"
    "$recover"
  fi
  CURRENT_STAGE=""
  _GAUNTLET_LAST_T=$(date +%s)
}
# recover_late_shape: both roots back at the late shape with no replace or
# crash pair, applied; a failure is logged, and the stages below measure
# what is left.
recover_late_shape() {
  late_root "$ORACLE" stock; ( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (stock could not put B back)"
  late_root "$ADOPTED" live; ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (choudoufu could not put A back)"
}

# ── 9. day2_replace: a create_before_destroy rename, marker and record ──
#
# A name is unique within its parent, so the replacement
# create_before_destroy exists for is one whose name changes, as on kind
# (gauntlet_kind_day2_replace, #1541) and as the stage's floci-az note says.
# The published root has none, so replace.tf adds a pair beside it, as
# day2_count adds shards: an azapi identity named with a generation, and a
# role assignment granting that identity AcrPull. Moving the generation a ->
# b replaces both, create first: the new identity, then the new grant for
# its principal, then the old grant, then the old identity. A grant that
# kept its principal could not be created first at all, since ARM refuses a
# second assignment of one role to one principal at one scope.
#
# The two halves are owned differently, which is why both are here. The old
# identity carries tofu-estate and tofu-address=azapi_resource.rotating, and
# its record (every applied instance's identity is recorded, #364 A2) names
# it; the plan's record-first read imports it from that record and verifies
# its marker (internal/live/projection/build.go, materializeFromRecord), so
# the name change is a replace at the address rather than a create beside a
# displaced marker. The old grant takes no tags: its record is the only
# thing binding it to rotating_grant. After the apply each record must name
# the new object.
#
# Stock on B is the oracle for the plan's shape and the apply's order. Each
# apply runs at -parallelism=1 and the log's line order is the evidence,
# for each block: the new object's "Creation complete" before the old one's
# "(deposed object ...): Destroying". The pair is removed from both roots at
# the end, so every later stage's counts are what they were.
#
# BREAK_REPLACE=1 is the stage's Break line: after the replace it recreates
# the old identity by hand carrying both markers for azapi_resource.rotating,
# and the next plan must not be empty and silent about it.
day2_replace_stage() {
gauntlet_begin_stage day2_replace
log "=== 9. day2_replace: an identity and its grant renamed under create_before_destroy ==="
ROT_A_ID="${RG_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${RG}-rotating-a"
ROT_B_ID="${RG_ID}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${RG}-rotating-b"
GRANT_A_ID="${RG_ID}/providers/Microsoft.Authorization/roleAssignments/${GRANT_GUID_A}"
GRANT_B_ID="${RG_ID}/providers/Microsoft.Authorization/roleAssignments/${GRANT_GUID_B}"
# replace_order <log> <block> <where>: the line of <block>'s create
# completing and of its deposed object's destroy starting, create first, or
# fail; appends "<create line><<destroy line>" to ORDER. Called directly,
# never in a command substitution, so a fail() records the stage.
replace_order() {
  local created destroying
  created="$(grep -nE "${2//./\\.}: Creation complete" <<< "$1" | head -1 | cut -d: -f1)"
  destroying="$(grep -nE "${2//./\\.} \(deposed object [^)]*\): Destroying" <<< "$1" | head -1 | cut -d: -f1)"
  [ -n "$created" ] && [ -n "$destroying" ] || { printf '%s\n' "$1" | tail -20; fail "$3: the apply log has no create complete (${created:-none}) or no deposed destroy (${destroying:-none}) for $2"; }
  [ "$created" -lt "$destroying" ] || { printf '%s\n' "$1" | tail -20; fail "$3: $2's old object's destroy started (log line $destroying) before its new object's create completed (line $created)"; }
  ORDER="${ORDER:+$ORDER, }${2#azapi_resource.} $created<$destroying"
}
# replace_shape <plan> <where>: stock's replace of both blocks, create first.
replace_shape() {
  grep -qF "Plan: 2 to add, 0 to change, 2 to destroy." <<< "$1" || { printf '%s\n' "$1" | tail -20; fail "$2: the rename is not two adds and two destroys: $(gauntlet_plan_line <<< "$1")"; }
  local b
  for b in azapi_resource.rotating azapi_resource.rotating_grant; do
    grep -qE "# ${b//./\\.} must be replaced" <<< "$1" || { printf '%s\n' "$1" | grep -E '# ' | head -8; fail "$2: $b is not planned as a replace"; }
  done
  [ "$(grep -cF "+/- create replacement and then destroy" <<< "$1")" -ge 1 ] || fail "$2: the replace is not create-first"
  if grep -qE '^[[:space:]]*# .* will be (created|destroyed)' <<< "$1"; then
    printf '%s\n' "$1" | grep -E '# ' | head -8
    fail "$2: beside the two replaces the plan creates or destroys something on its own"
  fi
}
# The estate starts converged, or nothing below measures the replace alone.
# The oracle is brought to the same shape first: day2_count's failure path
# leaves it a shard behind.
late_root "$ORACLE" stock
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock could not converge B to the late shape before day2_replace"
late_root "$ADOPTED" live
PRE_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$PRE_PLAN" | tail -20; fail "the plan before day2_replace failed"; }
grep -q "No changes." <<< "$PRE_PLAN" || { printf '%s\n' "$PRE_PLAN" | tail -20; fail "the estate is not converged before day2_replace ($(gauntlet_plan_line <<< "$PRE_PLAN")), so the replace could not be measured alone"; }

REPLACE=a late_root "$ORACLE" stock; REPLACE=a late_root "$ADOPTED" live
{ O_APPLY="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "2 added, 0 changed, 0 destroyed" <<< "$O_APPLY"; } || { printf '%s\n' "$O_APPLY" | tail -20; fail "stock could not add generation a's pair on B"; }
{ A_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 2 added, 0 changed, 0 destroyed." <<< "$A_APPLY"; } || { printf '%s\n' "$A_APPLY" | tail -20; fail "choudoufu could not add generation a's pair on A"; }
ROT_A_JSON="$(on_a gauntlet_arm GET "$ROT_A_ID?api-version=$MI_API")" || fail "the generation-a identity does not answer at $ROT_A_ID"
grep -qiE '"tofu-address" *: *"azapi_resource\.rotating"' <<< "$ROT_A_JSON" || fail "the generation-a identity does not carry tofu-address=azapi_resource.rotating: $ROT_A_JSON"
on_a gauntlet_arm GET "$GRANT_A_ID?api-version=$RA_API" >/dev/null || fail "the generation-a grant does not answer at $GRANT_A_ID"
G_REC_A="$(record_file adopted azapi_resource.rotating_grant || true)"
[ -n "$G_REC_A" ] && names_id "$G_REC_A" "$GRANT_A_ID" || fail "no record in the blob container names the generation-a grant ($GRANT_A_ID) for azapi_resource.rotating_grant, so nothing would bind it on the rename"

REPLACE=b late_root "$ORACLE" stock; REPLACE=b late_root "$ADOPTED" live
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's rename plan failed on B"; }
replace_shape "$O_PLAN" "stock on B (the oracle)"
R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_PLAN" | tail -20; fail "the rename plan failed"; }
replace_shape "$R_PLAN" "choudoufu"
O_APPLY="$(stock_b apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$O_APPLY" | tail -10; fail "stock's rename apply failed on B"; }
ORDER=""; replace_order "$O_APPLY" azapi_resource.rotating "stock on B (the oracle)"; replace_order "$O_APPLY" azapi_resource.rotating_grant "stock on B (the oracle)"; O_ORDER="$ORDER"
R_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color -parallelism=1 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the rename apply failed"; }
grep -qF "Apply complete! Resources: 2 added, 0 changed, 2 destroyed." <<< "$R_APPLY" || { printf '%s\n' "$R_APPLY" | tail -5; fail "the rename apply did not add two objects and destroy two"; }
ORDER=""; replace_order "$R_APPLY" azapi_resource.rotating choudoufu; replace_order "$R_APPLY" azapi_resource.rotating_grant choudoufu; R_ORDER="$ORDER"
gone_a "$ROT_A_ID" "$MI_API" || fail "the generation-a identity still answers after the replace"
gone_a "$GRANT_A_ID" "$RA_API" || fail "the generation-a grant still answers after the replace"
ROT_B_JSON="$(on_a gauntlet_arm GET "$ROT_B_ID?api-version=$MI_API")" || fail "the generation-b identity does not answer at $ROT_B_ID after the replace"
grep -qiE '"tofu-address" *: *"azapi_resource\.rotating"' <<< "$ROT_B_JSON" || fail "the generation-b identity does not carry tofu-address=azapi_resource.rotating: $ROT_B_JSON"
on_a gauntlet_arm GET "$GRANT_B_ID?api-version=$RA_API" >/dev/null || fail "the generation-b grant does not answer at $GRANT_B_ID after the replace"
G_REC_B="$(record_file adopted azapi_resource.rotating_grant || true)"
[ -n "$G_REC_B" ] && names_id "$G_REC_B" "$GRANT_B_ID" || fail "after the replace azapi_resource.rotating_grant's record does not name the generation-b grant ($GRANT_B_ID) as its object: ${G_REC_B:+$(gauntlet_record_identity "$G_REC_B")}"
G_DEPOSED="$(gauntlet_record_identity "$G_REC_B" deposed)"
[ -z "$G_DEPOSED" ] || fail "after the replace azapi_resource.rotating_grant's record still holds deposed object(s) [$(tr '\n' ' ' <<< "$G_DEPOSED")], which a later plan would read as a second claimant"

if [ "${BREAK_REPLACE:-}" = "1" ]; then
  on_a gauntlet_arm PUT "$ROT_A_ID?api-version=$MI_API" "{\"location\":\"eastus\",\"tags\":{\"tofu-estate\":\"$ESTATE\",\"tofu-address\":\"azapi_resource.rotating\"}}" >/dev/null \
    || fail "BREAK_REPLACE: could not recreate the generation-a identity"
  B_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)"; B_RC=$?
  if [ "$B_RC" -eq 0 ] && grep -q "No changes." <<< "$B_PLAN" && ! grep -qF "${RG}-rotating-a" <<< "$B_PLAN"; then
    printf '%s\n' "$B_PLAN" | tail -20
    fail "BREAK_REPLACE=1: the old identity is back carrying azapi_resource.rotating's markers and the plan is empty and says nothing about it - the empty-plan assertion is not load-bearing"
  fi
  if grep -qE "# .* will be destroyed" <<< "$B_PLAN"; then B_WHAT="proposed destroying it ($(gauntlet_plan_line <<< "$B_PLAN"))"
  elif [ "$B_RC" -ne 0 ]; then B_WHAT="refused (exit $B_RC: $(gauntlet_first_error_line <<< "$B_PLAN"))"
  elif grep -q "No changes." <<< "$B_PLAN"; then B_WHAT="proposed nothing but named it ($(grep -m1 -F "${RG}-rotating-a" <<< "$B_PLAN" | sed 's/^[[:space:]]*//'))"
  else B_WHAT="proposed work ($(gauntlet_plan_line <<< "$B_PLAN"))"; fi
  on_a gauntlet_arm DELETE "$ROT_A_ID?api-version=$MI_API" >/dev/null || fail "BREAK_REPLACE: could not delete the recreated identity afterwards"
else
  R_REPLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the replace failed"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the replace is not empty ($(gauntlet_plan_line <<< "$R_REPLAN"))"; }
  grep -qiE "displaced|claiming one" <<< "$R_REPLAN" && { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the replace is empty but reports a marker collision"; }
fi

late_root "$ORACLE" stock; late_root "$ADOPTED" live
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's removal of the replace pair failed on B"
RM_OUT="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$RM_OUT" | tail -20; fail "removing the replace pair failed"; }
grep -qF "Apply complete! Resources: 0 added, 0 changed, 2 destroyed." <<< "$RM_OUT" || { printf '%s\n' "$RM_OUT" | tail -5; fail "removing the replace pair did not destroy exactly the generation-b identity and grant"; }
gone_a "$ROT_B_ID" "$MI_API" || fail "the generation-b identity still answers after its block was removed"
gone_a "$GRANT_B_ID" "$RA_API" || fail "the generation-b grant still answers after its block was removed"

if [ "${BREAK_REPLACE:-}" = "1" ]; then
  gauntlet_stage day2_replace pass "BREAK_REPLACE=1 control: after the create_before_destroy rename a -> b the old identity was recreated by hand carrying tofu-estate=$ESTATE and tofu-address=azapi_resource.rotating, and the next plan $B_WHAT rather than proposing nothing in silence, so the empty-plan assertion is load-bearing; the real replan check is skipped"
else
  gauntlet_stage day2_replace pass "an azapi identity named with a generation and a role assignment granting it AcrPull, both create_before_destroy, added beside the published root (it has none): renaming a -> b plans as stock's replace on B, both blocks 'must be replaced', create replacement and then destroy, 2 add and 2 destroy, nothing else. At -parallelism=1 each new object's creation completes before its old object's deposed destroy starts (log lines: $R_ORDER; stock on B: $O_ORDER). ARM answers 404 for both generation-a objects; the generation-b identity carries tofu-address=azapi_resource.rotating, and the grant, which takes no tags, is named by its record in the blob container, which no longer names the destroyed one. The next plan is empty with no marker collision. The pair is removed from both roots afterwards (2 destroyed). BREAK_REPLACE=1 recreates the old identity carrying the block's markers and the next plan correctly does not stay silent"
fi
}
run_isolated day2_replace day2_replace_stage recover_late_shape

# ── 10. day2_crash: an apply of two resources, killed after the first ────
#
# As on the kind estates (reference-k8s's section 10): kill an apply after
# one resource exists and before the next does, and the next plan must
# propose exactly the remainder, binding what the crash created rather than
# creating it a second time (an ARM PUT to an existing id would update it,
# which #1120's overwrite refusal guards) or reading it as an orphan. The
# create_before_destroy window the floci estates interrupt is not
# interrupted here: day2_replace measures that replace uninterrupted, and
# killing it between a create and its deposed destroy is not yet done on
# this lane (the kind estates' gauntlet_kind_day2_crash_rename is the shape
# it would take).
#
# crash.tf's first resource is the untaggable kind: a role assignment
# granting the root's identity AcrPull. It carries no marker from its create
# PUT, so the only thing the next plan can bind it by is the record the
# interrupted apply wrote, in the blob container. Since this stage's
# desk-check the record is written as the create returns (#1944's mid-apply
# write, which skipped an untaggable azapi object until
# internal/live/projection/writeback.go's recordCarried asked the object),
# and the e2e build's interrupt is graceful, so the final write-back runs
# too. The second is an azapi identity that depends on the first.
#
# The kill is the engine's own: TOFU_E2E_APPLY_RESOURCE_INTERRUPT names
# azapi_resource.crash_first, and the e2e build self-delivers SIGTERM inside
# the -parallelism=1 walker the instant that create commits. crash_second
# depends on crash_first, so it cannot have been dispatched.
#
# The oracle is stock on B walked into the same position: it applies
# crash_first alone, and its plan for the pair is what "exactly the
# remainder" means here.
day2_crash_stage() {
gauntlet_begin_stage day2_crash
log "=== 10. day2_crash: SIGTERM between the create of a role assignment and the create of an identity ==="
CRASH=first late_root "$ORACLE" stock
O_PLAN="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_PLAN" || { printf '%s\n' "$O_PLAN" | tail -10; fail "stock's crash-first plan on B is not exactly one add"; }
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's crash-first apply failed on B"
CRASH=both late_root "$ORACLE" stock
O_REMAINDER="$(stock_b plan -input=false -no-color 2>&1)" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan failed on B"; }
grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$O_REMAINDER" || { printf '%s\n' "$O_REMAINDER" | tail -10; fail "stock's remainder plan on B is not exactly one add - the oracle for this stage is not what it should be"; }
grep -qE '# azapi_resource\.crash_second will be created' <<< "$O_REMAINDER" || fail "stock's remainder plan on B does not create crash_second"
( stock_b apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stock's remainder apply failed on B"
log "  oracle: stock at crash_first alone plans exactly one add (crash_second) for the remainder, and applies it"

CRASH=both late_root "$ADOPTED" live
X_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan failed"; }
grep -qF "Plan: 2 to add, 0 to change, 0 to destroy." <<< "$X_PLAN" || { printf '%s\n' "$X_PLAN" | tail -20; fail "the pre-crash plan is not exactly two adds ($(gauntlet_plan_line <<< "$X_PLAN")) - there is no two-resource apply to interrupt"; }
# The interrupt is delivered by the engine itself, so this runs in the
# plain foreground. A non-zero exit is the NORMAL outcome: the process was
# interrupted.
X_OUT="$(cd "$ADOPTED" && on_a env TOFU_E2E_APPLY_RESOURCE_INTERRUPT="azapi_resource.crash_first" "$TOFU_CRASH" apply -input=false -auto-approve -no-color -parallelism=1 2>&1)"; X_RC=$?
printf '%s\n' "$X_OUT" > "$WORK/day2_crash.log"
log "  interrupted apply exited $X_RC (a genuine interrupt is not expected to exit 0)"
[ "$X_RC" -ne 0 ] || { printf '%s\n' "$X_OUT" | tail -20; fail "the interrupted apply exited 0 - the engine's self-signal never landed, so nothing was interrupted and this stage would measure a clean apply"; }
on_a gauntlet_arm GET "$CRASH_ID?api-version=$RA_API" >/dev/null || { printf '%s\n' "$X_OUT" | tail -20; fail "crash_first does not answer at $CRASH_ID after the interrupted apply - the kill landed before the create committed, so there is no window to recover from"; }
gone_a "$CRASH2_ID" "$MI_API" || { printf '%s\n' "$X_OUT" | tail -20; fail "crash_second answers at $CRASH2_ID after the interrupted apply - the kill landed after both creates, so there is no remainder to propose"; }
# What the interrupted apply wrote, read off the store by the envelope's own
# address rather than counted (#1235).
X_REC="$(record_file adopted azapi_resource.crash_first || true)"
[ -n "$X_REC" ] || fail "the interrupted apply created crash_first but no record in the blob container names azapi_resource.crash_first; a role assignment takes no tags, so nothing is left for the recovery to bind it by"
names_id "$X_REC" "$CRASH_ID" || fail "the record for azapi_resource.crash_first names $(gauntlet_record_identity "$X_REC"), not $CRASH_ID"
log "  crash_first exists and its record names it; crash_second does not exist"

if [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  # The unrecovered run this stage exists to catch: the object is there and
  # nothing marks it as the estate's. For a role assignment that is its
  # record gone.
  X_BLOB="$(record_blob adopted azapi_resource.crash_first)" || fail "BREAK_CRASH_UNBOUND: could not name crash_first's record blob"
  on_a gauntlet_floci_az_blob_delete "$REC_ACCOUNT" adopted "$X_BLOB" || fail "BREAK_CRASH_UNBOUND: could not delete crash_first's record blob $X_BLOB"
  log "  BREAK_CRASH_UNBOUND=1: deleted crash_first's record blob $X_BLOB"
fi

R_PLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)"; R_RC=$?
R_LINE="$(gauntlet_plan_line <<< "$R_PLAN")"
# What the real check asserts, as one predicate, so the two Break controls
# require the SAME predicate to fail.
recovered() {
  [ "$R_RC" -eq 0 ] || return 1
  grep -qF "Plan: 1 to add, 0 to change, 0 to destroy." <<< "$R_PLAN" || return 1
  grep -qE '^[[:space:]]*# azapi_resource\.crash_second will be created' <<< "$R_PLAN" || return 1
  # Nothing may be proposed for the resource the crash did create.
  local r_proposed
  r_proposed="$(grep -E '^[[:space:]]*# .* (will be|must be)' <<< "$R_PLAN")"
  grep -q 'crash_first' <<< "$r_proposed" && return 1
  return 0
}

if [ "${BREAK_CRASH:-}" = "1" ]; then
  [ "$R_RC" -eq 0 ] || { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after the interrupt exited $R_RC"; }
  grep -qF "No changes." <<< "$R_PLAN" && { printf '%s\n' "$R_PLAN" | tail -20; fail "BREAK_CRASH=1: the plan after a real interrupted two-resource apply came back empty, so this stage's own check is not load-bearing"; }
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH: the recovery apply failed afterwards"
  gauntlet_stage day2_crash pass "BREAK_CRASH=1 control: after the same real interrupt the plan proposes work ($R_LINE), so the stage's own Break line - interrupt and then assert nothing is proposed - correctly fails to hold; the real check is skipped"
elif [ "${BREAK_CRASH_UNBOUND:-}" = "1" ]; then
  if recovered; then
    printf '%s\n' "$R_PLAN" | grep -E '^Plan:|will be'
    fail "BREAK_CRASH_UNBOUND=1: the recovery check still holds with crash_first's record deleted - it is not measuring whether the crashed-out role assignment was bound at all"
  fi
  on_a gauntlet_arm DELETE "$CRASH_ID?api-version=$RA_API" >/dev/null || fail "BREAK_CRASH_UNBOUND: could not delete the unrecorded crash_first afterwards"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "BREAK_CRASH_UNBOUND: the apply after the cleanup failed"
  gauntlet_stage day2_crash pass "BREAK_CRASH_UNBOUND=1 control: with crash_first's record deleted from the blob container - the unrecovered run this stage exists to catch, for a resource that takes no tags - the recovery check correctly fails to hold (${R_LINE:-no plan line}; first Error: line: \"$(gauntlet_first_error_line <<< "$R_PLAN")\"); the real check is skipped"
elif ! recovered; then
  printf '%s\n' "$R_PLAN" | grep -E '^Plan:|^No changes|will be|must be|^Error' | head -20
  gauntlet_stage day2_crash fail "the plan after a real interrupt between the create of azapi_resource.crash_first (a role assignment, no tags) and the create of azapi_resource.crash_second is not exactly the remainder: ${R_LINE:-no plan line} (exit $R_RC; first Error: line: \"$(gauntlet_first_error_line <<< "$R_PLAN")\"). crash_first answers at its ARM id and its record in the blob container names it, crash_second answers 404; stock, walked into the same position on B, plans exactly one add (crash_second)"
  log "  converging A with a plain apply so the stages below start from a whole estate"
  ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (the converging apply failed too; teardown measures what is left)"
else
  R_APPLY="$(tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$R_APPLY" | tail -20; fail "the recovery apply failed"; }
  grep -qF "Apply complete! Resources: 1 added, 0 changed, 0 destroyed" <<< "$R_APPLY" || { printf '%s\n' "$R_APPLY" | tail -5; fail "the recovery apply did not add exactly the one remaining resource"; }
  on_a gauntlet_arm GET "$CRASH2_ID?api-version=$MI_API" >/dev/null || fail "crash_second does not answer after the recovery apply"
  on_a gauntlet_arm GET "$CRASH_ID?api-version=$RA_API" >/dev/null || fail "crash_first is gone after the recovery apply - the recovery replaced the role assignment the crash created instead of binding it"
  R_REPLAN="$(tofu_a "$ADOPTED" plan -input=false -no-color 2>&1)" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery failed"; }
  grep -q "No changes." <<< "$R_REPLAN" || { printf '%s\n' "$R_REPLAN" | tail -20; fail "the replan after the recovery is not empty"; }
  gauntlet_stage day2_crash pass "an apply creating two resources beside the published root was interrupted by a real SIGTERM (exit $X_RC), delivered by the engine itself inside the -parallelism=1 walker the instant azapi_resource.crash_first's create committed (internal/command/apply_e2etesting_crash.go); crash_second depends on crash_first, so the walker cannot have reached it. crash_first is a role assignment, which takes no tags: it answers at its ARM id and the interrupted apply left its record in the blob container naming it; crash_second answers 404. The next plan proposed exactly the remainder ($R_LINE, azapi_resource.crash_second created) and nothing for crash_first, bound by its record - not a second PUT to its id, not an orphan - matching stock's plan from the same position on B; the recovery apply added one, both answer through ARM, and the replan is empty. BREAK_CRASH=1 asserts nothing is proposed and correctly fails; BREAK_CRASH_UNBOUND=1 deletes crash_first's record and the same recovery check correctly fails"
fi
}
# recover_crash_shape: A converged with the crash pair declared, for teardown.
recover_crash_shape() { CRASH=both late_root "$ADOPTED" live; ( tofu_a "$ADOPTED" apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (choudoufu could not converge A)"; }
run_isolated day2_crash day2_crash_stage recover_crash_shape

# ── 11. day2_teardown: destroy everything, nothing left marked ───────────
gauntlet_begin_stage day2_teardown
log "=== 11. day2_teardown: apply -destroy ==="
T_OUT="$(tofu_a "$ADOPTED" apply -destroy -auto-approve -input=false -no-color 2>&1)" || { printf '%s\n' "$T_OUT" | tail -20; fail "apply -destroy failed"; }
LEFT="$(count_a)" || fail "could not count A's marked resources after the destroy"
[ "$LEFT" = "0" ] || fail "$LEFT resource(s) still carry tofu-estate=$ESTATE after apply -destroy"
gone_a "$RG_ID" "$RG_API" || fail "the resource group still answers after apply -destroy"
gone_a "$SHARD0_ID" "$MI_API" || fail "shard 0 still answers after apply -destroy"
gone_a "$CRASH_ID" "$RA_API" || fail "day2_crash's role assignment (crash_first), held only by its record, still answers after apply -destroy"
gone_a "$CRASH2_ID" "$MI_API" || fail "day2_crash's identity (crash_second) still answers after apply -destroy"
gauntlet_stage day2_teardown pass "apply -destroy removed every resource in one apply ($(grep -oE 'Resources: [0-9]+ added, [0-9]+ changed, [0-9]+ destroyed' <<< "$T_OUT" | head -1)); nothing carries tofu-estate=$ESTATE and the resource group answers 404, as do shard 0 and day2_crash's pair, the role assignment among them found only by its record"

# ── 13. greenfield: the same root applied fresh, compared with stock ─────
gauntlet_begin_stage greenfield
log "=== 13. greenfield: choudoufu applies the published root on an empty container A ==="
RECORD_STORE_HCL="$RS_GREEN" write_root "$GREEN" live
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

# ── 15. no_local_state: the cache gone, the records in the blob container ─
#
# #1098's ruling: "local state" is every local artifact. This lane's records
# live in the record store, a blob container on A (the green one for this
# root), so a fresh clone of the greenfield root has them, and the only
# local state is the state cache; that is deleted, and anything else local
# that could hold state (a .tofu-records directory, a terraform.tfstate)
# must not exist at all. Every declared resource must then be found with
# nothing created, destroyed or replaced: the resource group and the
# identity by their markers, the role assignment, which takes no tags,
# through its record in the container (with no record, a live role
# assignment at its declared id is refused as an unrecorded overwrite,
# internal/live/projection/ownership.go's overwritesUnrecordedAtKey, so an
# empty plan is the record's doing).
#
# The oracle is the root's own cache-serving plan, taken just before the
# deletion after a no-op apply wrote the cache (a plan never persists it),
# which must be empty. Both plans run -refresh=false, as reference-eks's and
# reference-k8s-platform-app's do, and both are counted on the wire: every
# request A served between two marks in its access log
# (gauntlet_floci_az_requests_between), ARM, tokens and blobs alike.
#
# BREAK_NO_LOCAL_STATE=1 is the stage's Break line: the identity's two
# markers are stripped before the plan with no local state, which must then
# fail the check; they are put back.
# The cache's path is internal/command's stateCachePathFor:
# CHOUDOUFU_STATE_CACHE when it names a path, else choudoufu-cache.tfstate
# under the root's data dir (TF_DATA_DIR, default .terraform).
NLS="$WORK/no-local-state"; mkdir -p "$NLS"
case "${CHOUDOUFU_STATE_CACHE:-}" in
  off) CACHE="" ;;
  "") CACHE="$GREEN/${TF_DATA_DIR:-.terraform}/choudoufu-cache.tfstate"
      case "${TF_DATA_DIR:-}" in /*) CACHE="$TF_DATA_DIR/choudoufu-cache.tfstate" ;; esac ;;
  /*) CACHE="$CHOUDOUFU_STATE_CACHE" ;;
  *) CACHE="$GREEN/$CHOUDOUFU_STATE_CACHE" ;;
esac
no_local_state_stage() {
gauntlet_begin_stage no_local_state
log "=== 15. no_local_state: the cache-serving plan, then no state cache at all ==="
[ -n "$CACHE" ] || fail "CHOUDOUFU_STATE_CACHE=off is set, so there is no cache for no_local_state to measure"
# nls_plan <label>: plan -refresh=false in the greenfield root between two
# marks, with a debug log; prints "<cache hits> <requests> <rc>", requests
# "-" when the access log has no such span.
nls_plan() {
  local rc hits reqs
  on_a gauntlet_floci_az_mark "$1-start" || true
  ( cd "$GREEN" && on_a env TF_LOG=debug TF_LOG_PATH="$NLS/$1.log" "$TOFU" plan -refresh=false -input=false -no-color > "$NLS/$1.plan" 2>&1 ); rc=$?
  on_a gauntlet_floci_az_mark "$1-end" || true
  hits="$(grep -c 'state cache hit' "$NLS/$1.log" 2>/dev/null || true)"
  reqs="$(gauntlet_floci_az_requests_between "$CONTAINER_A" "$1-start" "$1-end" 2>/dev/null)" || reqs="-"
  printf '%s %s %s\n' "${hits:-0}" "${reqs:--}" "$rc"
}
WARM="$(tofu_a "$GREEN" apply -auto-approve -input=false -no-color 2>&1)"
grep -qF "Apply complete! Resources: 0 added, 0 changed, 0 destroyed" <<< "$WARM" \
  || { printf '%s\n' "$WARM" | tail -20; fail "the no-op apply that writes the greenfield root's state cache did not apply cleanly ($(gauntlet_plan_line <<< "$WARM"))"; }
[ -s "$CACHE" ] || fail "no state cache at $CACHE after a no-op apply, so there is nothing to serve from or delete"
cp "$CACHE" "$NLS/cache.keep" || fail "could not keep a copy of the state cache"
for f in "$GREEN/.tofu-records" "$GREEN/terraform.tfstate"; do
  [ ! -e "$f" ] || fail "$f exists: the greenfield root holds local state beside the cache, and records are supposed to be in the blob container"
done
G_RA_REC="$(record_file green azapi_resource.roleAssignments || true)"
[ -n "$G_RA_REC" ] && names_id "$G_RA_REC" "$RA_ID" || fail "no record in the green container names the role assignment ($RA_ID) for azapi_resource.roleAssignments after greenfield's apply, so nothing remote could find it"
read -r HITS_C REQ_C RC_C <<< "$(nls_plan cache-serving)"
[ "$RC_C" = "0" ] && grep -q "No changes." "$NLS/cache-serving.plan" \
  || { tail -20 "$NLS/cache-serving.plan"; fail "the cache-serving plan (exit $RC_C) is not empty, so it is no baseline"; }
[ "$REQ_C" != "-" ] && [ "$REQ_C" -gt 0 ] \
  || fail "floci-az A's access log counted no request for the cache-serving plan (${REQ_C}), so nothing here can count a plan's calls: QUARKUS_HTTP_ACCESS_LOG_ENABLED may not be honoured by this image"

# A fresh clone: no cache.
rm -f "$CACHE"
[ ! -e "$CACHE" ] || fail "the state cache survived deletion"
NLS_TAGS=""
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  NLS_TAGS="$(on_a gauntlet_arm GET "$UAI_ID?api-version=$MI_API" | python3 -c 'import json,sys; t=json.load(sys.stdin).get("tags") or {}; print(" ".join("%s=%s" % (k, v) for k, v in t.items() if k.lower().startswith("tofu-")))')"
  [ -n "$NLS_TAGS" ] || fail "BREAK_NO_LOCAL_STATE: the identity carries no marker to strip"
  printf '%s\n' "$NLS_TAGS" > "$NLS/tags.stripped"
  for kv in $NLS_TAGS; do tag_set a "$UAI_ID" "$MI_API" "${kv%%=*}" - || fail "BREAK_NO_LOCAL_STATE: could not strip ${kv%%=*} off the identity"; done
fi
read -r _ REQ_N RC_N <<< "$(nls_plan no-local-state)"
NLS_BAD=""
if [ "$RC_N" != "0" ]; then
  NLS_BAD="the plan with no local state exited $RC_N: $(gauntlet_first_error_line < "$NLS/no-local-state.plan")"
elif grep -qE '# .+ (will be (created|destroyed)|must be replaced)' "$NLS/no-local-state.plan"; then
  NLS_BAD="with no local state the plan proposes: $(grep -E '# .+ (will be (created|destroyed)|must be replaced)' "$NLS/no-local-state.plan" | sed -E 's/^[[:space:]#]*//' | tr '\n' ';')"
fi
NLS_UPD="$(grep -cE '# .+ will be updated in-place' "$NLS/no-local-state.plan" || true)"
# Put back what this stage took, whatever the verdict.
if [ -n "$NLS_TAGS" ]; then
  for kv in $NLS_TAGS; do tag_set a "$UAI_ID" "$MI_API" "${kv%%=*}" "${kv#*=}" || log "  (could not put ${kv%%=*} back on the identity)"; done
  : > "$NLS/tags.stripped"
fi
cp "$NLS/cache.keep" "$CACHE" || log "  (could not put the state cache back)"
if [ "${BREAK_NO_LOCAL_STATE:-}" = "1" ]; then
  [ -n "$NLS_BAD" ] || fail "BREAK_NO_LOCAL_STATE=1: with the identity's markers stripped and no local state, the plan still found everything, so the check is not load-bearing"
  gauntlet_stage no_local_state pass "BREAK_NO_LOCAL_STATE=1 control: with the identity's tofu-estate and tofu-address stripped and the state cache deleted, the check correctly failed ($NLS_BAD); the markers and the cache were put back"
elif [ -n "$NLS_BAD" ]; then
  tail -20 "$NLS/no-local-state.plan"
  gauntlet_stage no_local_state fail "with the state cache deleted and the records in the green blob container: $NLS_BAD"
elif [ "$REQ_N" = "-" ] || [ "$REQ_N" -le 0 ]; then
  gauntlet_stage no_local_state fail "the plan with no local state was empty, but floci-az A's access log counted no request for it (${REQ_N}), so the verdict cannot report its calls"
else
  RATIO="$(awk -v a="$REQ_N" -v b="$REQ_C" 'BEGIN { printf "%.2f", a / b }')"
  gauntlet_stage no_local_state pass "the greenfield root's records live in a blob container on floci-az A (record_store \"azurerm\"), so the only local state is the state cache, and no .tofu-records or terraform.tfstate exists; with the cache deleted - a fresh clone with only the account and the store left - the plan created, destroyed and replaced nothing, $NLS_UPD in-place update(s): the resource group and the identity found by their markers at their declared ARM ids, and the role assignment, which takes no tags, through its record in the container, which names it. The empty plan before the deletion was served from a cache written by a no-op apply ($HITS_C instance(s) answered by a cache hit). plan_calls_no_local_state=$REQ_N plan_calls_cache_serving=$REQ_C ratio=${RATIO}x (both plan -refresh=false; every request floci-az A served between two marks, counted from its access log); stock in this position has no plan at all, only one import block per object. The cache was put back. BREAK_NO_LOCAL_STATE=1 strips the identity's markers and the check correctly fails"
fi
}
# recover_no_local_state: the identity's markers and the cache put back, if
# the stage stopped between taking and returning them.
recover_no_local_state() {
  local kv
  if [ -s "$WORK/no-local-state/tags.stripped" ]; then
    for kv in $(cat "$WORK/no-local-state/tags.stripped"); do tag_set a "$UAI_ID" "$MI_API" "${kv%%=*}" "${kv#*=}" || log "  (could not put ${kv%%=*} back on the identity)"; done
  fi
  [ -n "$CACHE" ] && [ -s "$WORK/no-local-state/cache.keep" ] && cp "$WORK/no-local-state/cache.keep" "$CACHE"
  return 0
}
run_isolated no_local_state no_local_state_stage recover_no_local_state

( tofu_a "$GREEN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || log "  (the greenfield estate's destroy failed; the container is removed on exit)"

# ── 14. strict: every toggle on, one refusal ─────────────────────────────
#
# What every other lane asserts (reference-eks's reference_eks_stage_strict,
# the kind estates' strict stage): a scratch estate carrying one
# random_password, with secrets = "refuse", no_source_create = "refuse" and
# marker_repair = "never" beside a markers "record" selection naming a type
# this root declares, plans to exactly one refusal, the secrets toggle's.
# Its records go where this lane's go, the strict container on A. It reaches
# no Azure provider. BREAK_STRICT=1 turns secrets back to "store" and the
# refusal must disappear.
strict_stage() {
gauntlet_begin_stage strict
log "=== 14. strict: every strict toggle on, against a scratch estate ==="
STRICT="$WORK/strict"; mkdir -p "$STRICT"
strict_root() { # $1: the secrets setting
  cat <<HCL
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "$ESTATE-strict"
$RS_STRICT
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
HCL
}
strict_root refuse > "$STRICT/main.tf"
( tofu_a "$STRICT" init -input=false -no-color > "$STRICT/init.out" 2>&1 ) || { tail -20 "$STRICT/init.out"; fail "choudoufu init for the strict-stage scratch estate failed"; }
ON="$(tofu_a "$STRICT" plan -input=false -no-color 2>&1)"; ON_RC=$?
if [ "${BREAK_STRICT:-}" = "1" ]; then
  strict_root store > "$STRICT/main.tf"
  OFF="$(tofu_a "$STRICT" plan -input=false -no-color 2>&1)"; OFF_RC=$?
  [ "$OFF_RC" -eq 0 ] || { printf '%s\n' "$OFF" | tail -30; fail "BREAK_STRICT=1: the plan with secrets = \"store\" exited $OFF_RC"; }
  grep -q "^Error:" <<< "$OFF" && fail "BREAK_STRICT=1: turning secrets off did not clear every refusal"
  grep -qF 'random_password.db will be created' <<< "$OFF" || fail "BREAK_STRICT=1: the plan with secrets = \"store\" does not propose creating random_password.db"
  gauntlet_stage strict pass "BREAK_STRICT=1 control: with secrets back to \"store\" the refusal is gone and the plan is an ordinary create; the real check is skipped"
else
  [ "$ON_RC" -eq 1 ] || { printf '%s\n' "$ON" | tail -30; fail "the every-toggle-on plan exited $ON_RC, not the refusal's usual 1"; }
  [ "$(grep -c '^Error:' <<< "$ON")" -eq 1 ] || { printf '%s\n' "$ON"; fail "every strict toggle on refused more than one thing"; }
  grep -qF 'Error: Logical resource is not admitted' <<< "$ON" || { printf '%s\n' "$ON"; fail "the one refusal is not \"Logical resource is not admitted\""; }
  grep -qF 'strict { secrets = "refuse" }' <<< "$ON" || { printf '%s\n' "$ON"; fail "the refusal does not cite strict { secrets = \"refuse\" }"; }
  gauntlet_stage strict pass "every strict toggle on (secrets = refuse, no_source_create = refuse, marker_repair = never with a markers \"record\" selection naming azurerm_user_assigned_identity) against a scratch estate carrying random_password.db, its records in the strict blob container on A: exactly one refusal, Logical resource is not admitted under strict { secrets = \"refuse\" }; the other two toggles are on and silent. The scratch estate reaches no Azure provider. BREAK_STRICT=1 turns secrets back to \"store\" and the refusal disappears"
fi
}
run_isolated strict strict_stage

gauntlet_end
