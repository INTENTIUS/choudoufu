# az-a-markerless-type-is-held-by-its-record
# CLAIM 51 (azure) - A markerless type is held by its record, or refused. ~3 min.
#
# This proof: on floci-az, an azurerm type that takes no ARM tags and whose
# name the provider mints is held by the estate's record.
# azurerm_management_group is one: its name is Optional and Computed at
# azurerm 5.10.0, and its create sets a new GUID when the name is left out
# (internal/live/substrate/azure_minted.go names it and the six others).
# Such an instance has no name to compute and no tag to carry a marker, so
# under a live block, whose record store is implied when none is declared,
# it is record-located: the apply writes the GUID the provider chose to the
# record, and every later plan reads it back from there. No Azure type is
# on the markerless list, because every admitted azurerm type's id is built
# from arguments (live/READINESS.md, Azure).
#
# Claim D4 of #2119, restated to the record rung: an untaggable,
# server-minted type is held by its record, or refused by name when its
# identity cannot be recorded. BREAK=1 is the second half. The same block
# in a root with no live block has no record store to hold the minted name,
# and live-check, which reads a configuration offline, must refuse it by
# name: "Unmarked apply of a marker-only resource", naming
# azurerm_management_group.minted and the name it omits
# (internal/live/stamp/unmarked.go, NAME_OMITTED).
#
# Not yet run, so its cell reads unproven. It waits on the emulator twice.
# floci-az's generic ARM store, served since 30000b0, keeps
# subscription-scoped ids only, so the management group's tenant-scoped
# create (/providers/Microsoft.Management/managementGroups/{name}) still
# answers 404 there. And every plan's sweep is a Resource Graph query,
# which floci-az serves since 30000b0 with a KQL subset that refuses the
# query's union, mv-expand and bag_keys (live/FLOCI.md, Azure). The BREAK
# arm reads the configuration offline and needs neither.

SMOKE_WORK="$SMOKE_WORKROOT/az-a-markerless-type-is-held-by-its-record"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az-held"
RG="smoke-az-held-rg"
DISPLAY="smoke-az-held-mg"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
MG_API="2021-04-01"

azure_up
resources() {
  cat <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

# No name: azurerm mints a GUID for it at create time.
resource "azurerm_management_group" "minted" {
  display_name = "${DISPLAY}"
}
TF
}
azure_terraform_block "$ESTATE" > "$SMOKE_WORK/versions.tf" \
  || fail "az-a-markerless-type-is-held-by-its-record" "could not read the azurerm and azapi pins from live/oracle-versions.json"
resources > "$SMOKE_WORK/main.tf"
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }
# minted_names prints the name of every management group whose display
# name is this scenario's, read through ARM.
minted_names() {
  arm GET "/providers/Microsoft.Management/managementGroups?api-version=${MG_API}" \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); print("\n".join(g["name"] for g in d.get("value", []) if (g.get("properties") or {}).get("displayName") == sys.argv[1]))' "$DISPLAY"
}

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the same block with no live block; it must be refused by name"
  explain \
    "You asked for proof the admission is the record's. This root has" \
    "the same management group and no live block, so there is no estate" \
    "and no record store: nothing could hold the name azurerm mints." \
    "live-check reads the configuration offline and must refuse" \
    "azurerm_management_group.minted by name."
  NOSTORE="$SMOKE_WORK/no-store"
  mkdir -p "$NOSTORE"
  # azure_terraform_block's terraform and provider blocks with its live
  # block (the two-space-indented `live {` up to its closing brace) taken out.
  azure_terraform_block "$ESTATE" | awk '/^  live \{/ { skip = 1; next } skip && /^  \}$/ { skip = 0; next } !skip' > "$NOSTORE/versions.tf" \
    || fail "az-a-markerless-type-is-held-by-its-record" "BREAK: could not write the root with no live block"
  if grep -q 'live {' "$NOSTORE/versions.tf"; then
    fail "az-a-markerless-type-is-held-by-its-record" "BREAK: the no-store root still has a live block, so this control would prove nothing"
  fi
  resources > "$NOSTORE/main.tf"
  logged az-held-break-init "az-a-markerless-type-is-held-by-its-record" "BREAK: init failed" -- in_dir "$NOSTORE" chdf init -input=false -no-color
  cmd "choudoufu live-check .   (no live block)"
  BRC=0
  BOUT="$(cd "$NOSTORE" && chdf live-check -no-color . 2>&1)" || BRC=$?
  { grep -E 'Unmarked apply|azurerm_management_group|sets no name' <<< "$BOUT" || true; } | head -5 | evidence
  echo "live-check exit: $BRC" | evidence
  [ "$BRC" != "0" ] \
    || fail "az-a-markerless-type-is-held-by-its-record" "BREAK: live-check passed a minted, untaggable instance with no record store to hold it: $BOUT"
  grep -q 'Unmarked apply of a marker-only resource' <<< "$BOUT" \
    || fail "az-a-markerless-type-is-held-by-its-record" "BREAK: the refusal is not the unmarked-apply summary: $BOUT"
  grep -q 'azurerm_management_group.minted' <<< "$BOUT" \
    || fail "az-a-markerless-type-is-held-by-its-record" "BREAK: the refusal does not name azurerm_management_group.minted: $BOUT"
  grep -q 'sets no name' <<< "$BOUT" \
    || fail "az-a-markerless-type-is-held-by-its-record" "BREAK: the refusal does not say the name is omitted and minted: $BOUT"
  proof "caught. With nowhere to record its minted name the management group is refused by name, so the admission below is the record's and nothing else."
  exit 0
fi

step "1. a resource group and a management group with no name"
explain \
  "The resource group carries the estate's ARM tags. The management group" \
  "takes no tags, and with its name left out azurerm mints a GUID, so no" \
  "run can compute its id from the configuration either. The plan admits" \
  "it as record-located: the apply writes the minted name to the record."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-held-init "az-a-markerless-type-is-held-by-its-record" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-markerless-type-is-held-by-its-record" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY_OUT" \
  || fail "az-a-markerless-type-is-held-by-its-record" "apply did not report 2 added: $APPLY_OUT"
NAMES="$(minted_names)" \
  || fail "az-a-markerless-type-is-held-by-its-record" "could not list management groups through ARM"
echo "minted: $NAMES" | evidence
[ "$(grep -c . <<< "$NAMES")" = "1" ] \
  || fail "az-a-markerless-type-is-held-by-its-record" "want exactly one management group named $DISPLAY after the apply, ARM lists: $NAMES"
[ -n "$(find "$SMOKE_WORK/.tofu-records" -type d -name azurerm_management_group 2>/dev/null)" ] \
  || fail "az-a-markerless-type-is-held-by-its-record" "no record for azurerm_management_group under .tofu-records after the apply"
proof "planned and applied: one management group under a name azurerm chose, and a record holding that name."

step "2. delete the cache; the management group is found again from its record"
explain \
  "With the cache gone, the plan has the resource group's tag and the" \
  "management group's record. The management group cannot be found by tag" \
  "or by a name computed from the configuration; only the record says" \
  "which one this estate made. If the record holds it, the plan is empty."
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
rm -f "$CACHE"
PLAN_OUT="$(plan)" || fail "az-a-markerless-type-is-held-by-its-record" "plan failed: $PLAN_OUT"
{ grep -E '^No changes\.|^Plan:' <<< "$PLAN_OUT" || true; } | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" \
  || fail "az-a-markerless-type-is-held-by-its-record" "the plan without the cache is not empty, so the record did not hold the management group: $PLAN_OUT"
if grep -q 'Unmarked apply of a marker-only resource' <<< "$PLAN_OUT"; then
  fail "az-a-markerless-type-is-held-by-its-record" "an unmarked-apply refusal under a live block with a record store: $PLAN_OUT"
fi
[ "$(minted_names)" = "$NAMES" ] \
  || fail "az-a-markerless-type-is-held-by-its-record" "a plan changed the management groups ARM lists"
proof "No changes., with no cache: the management group is held by its record."

step "3. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-markerless-type-is-held-by-its-record" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "az-a-markerless-type-is-held-by-its-record" 2 "$DESTROY_OUT"
[ -z "$(minted_names)" ] \
  || fail "az-a-markerless-type-is-held-by-its-record" "ARM still lists a management group named $DISPLAY after destroy"
proof "2 destroyed, and ARM lists no management group by that display name."

echo "  What you watched: an Azure type with no tags and a provider-minted"
echo "  name held by the estate's record, found again with no cache, and the"
echo "  same block refused by name where there is no record to hold it."
