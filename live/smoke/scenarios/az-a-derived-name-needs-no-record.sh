# az-a-derived-name-needs-no-record
# CLAIM 48 (azure) - A derived identity needs no record and no cache. ~3 min.
#
# This proof: on floci-az, two subnets, a type that takes no ARM tags, are
# applied beside a tagged VNet: one through azurerm_subnet and one through
# azapi_resource (Microsoft.Network/virtualNetworks/subnets). Each is
# declaration-carried (live/READINESS.md, Azure): its ARM id is rebuilt
# from the declaration on every run, azurerm's from resource_group_name,
# virtual_network_name and name (subscription from the provider block),
# azapi's from parent_id, type and name. The scenario deletes the record
# store and the state cache, and the plan must read No changes. from the
# emulator and the configuration alone.
#
# Claim D1 of #2119. BREAK=1 renames the azurerm subnet in the
# configuration once the store and the cache are gone, and requires the
# plan to propose exactly one create at azurerm_subnet.app and leave the
# live subnet alone, because without that control an empty plan would read
# the same if binding ignored the derived name.
#
# Not yet run, so its cell reads unproven. It waits on lex00/floci-az#4: a
# plan's sweep is a Resource Graph query, which floci-az does not serve.
# That floci-az's network handler serves subnets as children of a VNet is
# assumed from live/FLOCI.md's network row, not confirmed.

SMOKE_WORK="$SMOKE_WORKROOT/az-a-derived-name-needs-no-record"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az-derived"
RG="smoke-az-derived-rg"
RECORDS="$SMOKE_WORK/.tofu-records"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
NET_API="2024-05-01"
VNET_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/virtualNetworks/derived-vnet"

azure_up

azure_terraform_block "$ESTATE" > "$SMOKE_WORK/versions.tf" \
  || fail "az-a-derived-name-needs-no-record" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "derived-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.48.0.0/16"]
}

# azapi's subnet waits for azurerm's: ARM serialises writes under one VNet.
resource "azapi_resource" "data" {
  type      = "Microsoft.Network/virtualNetworks/subnets@${NET_API}"
  name      = "derived-data"
  parent_id = azurerm_virtual_network.app.id
  body = {
    properties = {
      addressPrefix = "10.48.2.0/24"
    }
  }
  depends_on = [azurerm_subnet.app]
}
TF
# subnet_tf <name> writes the azurerm subnet's block, so BREAK can rename it.
subnet_tf() {
  cat > "$SMOKE_WORK/subnet.tf" <<TF
resource "azurerm_subnet" "app" {
  name                 = "$1"
  resource_group_name  = azurerm_resource_group.app.name
  virtual_network_name = azurerm_virtual_network.app.name
  address_prefixes     = ["10.48.1.0/24"]
}
TF
}
subnet_tf derived-app
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }

step "1. a VNet and two subnets, neither subnet tagged"
explain \
  "The resource group and the VNet carry the estate's ARM tags. The two" \
  "subnets cannot: Microsoft.Network/virtualNetworks/subnets takes no tags," \
  "through azurerm or through azapi. Each subnet's ARM id is built from" \
  "names the configuration sets, so no tag is needed to find it."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-derived-init "az-a-derived-name-needs-no-record" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-derived-name-needs-no-record" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || fail "az-a-derived-name-needs-no-record" "apply did not report 4 added: $APPLY_OUT"
for s in derived-app derived-data; do
  arm GET "${VNET_ID}/subnets/${s}?api-version=${NET_API}" >/dev/null \
    || fail "az-a-derived-name-needs-no-record" "ARM does not answer for subnet $s after the apply"
done
VNET_TAGS="$(arm_tags "$VNET_ID" "$NET_API")" \
  || fail "az-a-derived-name-needs-no-record" "could not read the VNet's tags"
echo "VNet tags: $VNET_TAGS" | evidence
grep -q "\"tofu-estate\": \"$ESTATE\"" <<< "$VNET_TAGS" \
  || fail "az-a-derived-name-needs-no-record" "the VNet does not carry tofu-estate=$ESTATE: $VNET_TAGS"
proof "4 created: the VNet tagged for $ESTATE, and two subnets ARM answers for that carry no tag at all."

step "2. delete the record store and the cache"
explain \
  "Everything this estate remembers about the subnets goes: the records" \
  "and the state cache. What is left is the configuration and the" \
  "emulator. The subnets carry no marker, so if the plan finds them it" \
  "found them by the id the configuration derives."
cmd "rm -rf .tofu-records .terraform/choudoufu-cache.tfstate"
rm -rf "$RECORDS"
rm -f "$CACHE"
[ ! -e "$RECORDS" ] && [ ! -e "$CACHE" ] \
  || fail "az-a-derived-name-needs-no-record" "the record store or the cache is still on disk"
proof "no record store and no cache."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - rename the azurerm subnet; the plan must propose a create"
  explain \
    "You asked for proof the plan binds by the derived id. The azurerm" \
    "subnet is renamed in the configuration. Its derived id now names" \
    "nothing live, so the plan must propose exactly one create, at" \
    "azurerm_subnet.app, and leave the live derived-app subnet alone. An" \
    "empty plan here would mean binding ignores the name."
  subnet_tf derived-app-renamed
  cmd "sed name = \"derived-app-renamed\" subnet.tf && choudoufu plan"
  BOUT="$(plan)" || fail "az-a-derived-name-needs-no-record" "BREAK: the plan failed: $BOUT"
  { grep -E '^Plan:|^No changes|will be created|will be destroyed' <<< "$BOUT" || true; } | evidence
  grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$BOUT" \
    || fail "az-a-derived-name-needs-no-record" "BREAK: the renamed subnet did not plan exactly one create: $BOUT"
  grep -qE 'azurerm_subnet\.app will be created' <<< "$BOUT" \
    || fail "az-a-derived-name-needs-no-record" "BREAK: the one create is not azurerm_subnet.app: $BOUT"
  arm GET "${VNET_ID}/subnets/derived-app?api-version=${NET_API}" >/dev/null \
    || fail "az-a-derived-name-needs-no-record" "BREAK: the live derived-app subnet is gone after a plan"
  proof "caught. A derived name that matches nothing live plans a create, so the empty plan below is the derivation finding the subnets."
  exit 0
fi

step "3. plan with neither: it must read No changes."
explain \
  "The plan rebuilds each subnet's ARM id from the configuration and reads" \
  "it. Both answer, so nothing is to be created."
cmd "choudoufu plan"
PLAN_OUT="$(plan)" || fail "az-a-derived-name-needs-no-record" "plan failed: $PLAN_OUT"
{ grep -E '^No changes\.|^Plan:' <<< "$PLAN_OUT" || true; } | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" \
  || fail "az-a-derived-name-needs-no-record" "with no record store and no cache the plan is not empty, so the subnets were not found by their derived ids: $PLAN_OUT"
proof "No changes., from the configuration and the emulator alone."

step "4. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-derived-name-needs-no-record" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "az-a-derived-name-needs-no-record" 4 "$DESTROY_OUT"
proof "4 destroyed. The estate is gone."

echo "  What you watched: two Azure subnets with no tags, one through azurerm"
echo "  and one through azapi, found again with the record store and the cache"
echo "  both deleted, because their ids are derived from the configuration."
