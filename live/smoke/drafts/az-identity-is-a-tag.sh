# az-identity-is-a-tag
# CLAIM 7 (azure) - Identity is a tag you can read, move and carve by. ~4 min.
#
# This proof: on floci-az, an azurerm and azapi estate applied greenfield
# carries its marker as ARM tags (tofu-estate and tofu-address, written
# lowercase) on the create itself, any ARM read shows them, the replan is
# empty with no state file, and losing the cache changes nothing. BREAK=1
# strips the tofu-estate tag with an ARM GET and PUT behind choudoufu's back
# (arm_set_tag; floci-az serves no PATCH on Microsoft.Network): the
# default replan must refuse the object by name rather than PUT over it
# (#1120's create-or-update refusal), and under policy { declared_untagged =
# "adopt" } the replan must propose the tag write and the apply must restore
# the tag, read back through ARM.
#
# Written for GitHub issue #1120 (unit 15). Not yet run, so its cell reads
# unproven until the maintainer runs both arms on floci-az and the evidence
# file lands.
#
# floci-az keeps tags on resource groups, network resources and managed
# identities, which is every type here, and runs the sweep query as sent,
# union, mv-expand and bag_keys included, since dfa33ca
# (lex00/floci-az#11; live/FLOCI.md, Azure), not yet run. Every type here
# is on azurerm's identity-schema route, so it plans only once that route
# answers a subscription from the provider block (#1120 unit 10's note).

SMOKE_WORK="$SMOKE_WORKROOT/az-identity-is-a-tag"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az"
RG="smoke-az-rg"

azure_up

azure_terraform_block "$ESTATE" > "$SMOKE_WORK/versions.tf" \
  || fail "az-identity" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "smoke-az-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.40.0.0/16"]
}

resource "azurerm_user_assigned_identity" "app" {
  name                = "smoke-az-id"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}

# The azapi spelling of a type azurerm also has: the marker goes through
# azapi's top-level tags argument, the arm-tags surface's other half.
resource "azapi_resource" "worker" {
  type      = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
  name      = "smoke-az-worker"
  parent_id = azurerm_resource_group.app.id
  location  = "eastus"
}
TF

SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
VNET_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/virtualNetworks/smoke-az-vnet"
WORKER_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smoke-az-worker"
NET_API="2024-05-01"
MI_API="2023-01-31"

step "1. an Azure estate, one plain apply"
explain \
  "A resource group, a VNet, a user-assigned identity through azurerm, and a" \
  "second identity through azapi. A live block, a local record store, and" \
  "nothing pre-created. Stock OpenTofu would write terraform.tfstate here;" \
  "choudoufu keeps only a disposable cache."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-identity-init "az-identity" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-identity" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || fail "az-identity" "apply did not report 4 added: $APPLY_OUT"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "az-identity" "a terraform.tfstate appeared; a live-block apply never writes an authoritative state file"
proof "four objects created through two providers, and no terraform.tfstate exists."

step "2. the marker, read back through ARM - no choudoufu in the loop"
explain \
  "If the ownership record is on the object, any ARM client can read it." \
  "This asks floci-az for the VNet's and the azapi identity's tags. The keys" \
  "are written lowercase; ARM treats tag keys case-insensitively."
cmd "GET ${VNET_ID}?api-version=${NET_API}  (tags)"
VNET_TAGS="$(arm_tags "$VNET_ID" "$NET_API" 2>&1)" || fail "az-identity" "ARM could not read the VNet: $VNET_TAGS"
echo "$VNET_TAGS" | evidence
grep -q "\"tofu-estate\": \"${ESTATE}\"" <<< "$VNET_TAGS" \
  || fail "az-identity" "the VNet carries no tofu-estate=${ESTATE} tag: $VNET_TAGS"
grep -q '"tofu-address": "azurerm_virtual_network.app"' <<< "$VNET_TAGS" \
  || fail "az-identity" "the VNet's tofu-address tag does not name azurerm_virtual_network.app: $VNET_TAGS"
WORKER_TAGS="$(arm_tags "$WORKER_ID" "$MI_API" 2>&1)" || fail "az-identity" "ARM could not read the azapi identity: $WORKER_TAGS"
echo "$WORKER_TAGS" | evidence
grep -q "\"tofu-estate\": \"${ESTATE}\"" <<< "$WORKER_TAGS" \
  || fail "az-identity" "the azapi_resource carries no tofu-estate tag; the arm-tags stamp missed azapi's top-level tags: $WORKER_TAGS"
proof "the estate and the address rode the create on both providers' objects, and an ARM read shows them."

step "3. the replan - prior state rebuilt from Azure"
explain \
  "With no state file the next plan asks ARM what exists, at the id each" \
  "block declares. If binding is right, the plan is empty."
cmd "choudoufu plan"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "az-identity" "plan failed: $PLAN_OUT"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" || fail "az-identity" "replan is not empty: $PLAN_OUT"
proof "an empty plan, with nothing stored anywhere that could have remembered the estate."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the tag; the replan must refuse, and adopt must restore it"
  explain \
    "This removes tofu-estate from the VNet with an ARM GET and PUT, behind" \
    "choudoufu's back. If the next plan is still empty, the tag is not what" \
    "the plan reads. A VNet create is a PUT, and a PUT to an id that exists" \
    "updates it, so the plan must not propose one: it stops and names the" \
    "object. Then the live block opts in with declared_untagged = \"adopt\"," \
    "and the replan must propose writing the tag back."
  cmd "GET ${VNET_ID}, then PUT it back without tofu-estate"
  arm_set_tag "${VNET_ID}?api-version=${NET_API}" tofu-estate - >/dev/null \
    || fail "az-identity" "BREAK: could not strip the tag with an ARM GET and PUT"
  STRIPPED="$(arm_tags "$VNET_ID" "$NET_API" 2>&1)"
  if grep -q 'tofu-estate' <<< "$STRIPPED"; then
    fail "az-identity" "BREAK: the PUT left tofu-estate on the VNet, so this control would prove nothing: $STRIPPED"
  fi
  BRC=0
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || BRC=$?
  printf '%s\n' "$BOUT" > "$(smoke_log_dir)/az-identity-break.plan"
  BFLAT="$(tr '\n' ' ' <<< "$BOUT" | tr -s ' ')"
  grep -E '^Error: |^Plan:|No changes' <<< "$BOUT" | head -2 | evidence
  if grep -q "No changes." <<< "$BOUT"; then
    fail "az-identity" "BREAK: the plan is still empty after the tag was stripped; the tag is not what the plan reads"
  fi
  [ "$BRC" = "1" ] || fail "az-identity" "BREAK: the plan exited $BRC, want 1 (full plan in $(smoke_log_dir)/az-identity-break.plan)"
  grep -q 'Error: Unmarked live object at the declared id would be overwritten' <<< "$BOUT" \
    || fail "az-identity" "BREAK: the plan does not refuse the untagged VNet by name; it was adopted in silence or planned as a PUT over it: $(grep -E '^Plan:|^Error' <<< "$BOUT" | head -3)"
  grep -q 'azurerm_virtual_network.app' <<< "$BFLAT" \
    || fail "az-identity" "BREAK: the refusal does not name the block: $BOUT"

  sed_i "$SMOKE_WORK/versions.tf" "s/estate = \"${ESTATE}\"/estate = \"${ESTATE}\"\\
    policy { declared_untagged = \"adopt\" }/"
  grep -q 'declared_untagged = "adopt"' "$SMOKE_WORK/versions.tf" \
    || fail "az-identity" "BREAK: the adopt policy did not land in versions.tf"
  AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "az-identity" "BREAK: the adopting apply failed: $AOUT"
  grep -E 'Apply complete!' <<< "$AOUT" | evidence
  grep -qE 'Resources: 0 added, [1-9][0-9]* changed, 0 destroyed' <<< "$AOUT" \
    || fail "az-identity" "BREAK: the adopting apply did not change the VNet in place: $AOUT"
  RESTORED="$(arm_tags "$VNET_ID" "$NET_API" 2>&1)" || fail "az-identity" "BREAK: ARM could not read the VNet: $RESTORED"
  echo "$RESTORED" | evidence
  grep -q "\"tofu-estate\": \"${ESTATE}\"" <<< "$RESTORED" \
    || fail "az-identity" "BREAK: the adopting apply did not write tofu-estate back: $RESTORED"
  proof "caught. The stripped tag took the VNet out of the estate: the plan refused it by name with exit 1 instead of a PUT over it, and the one opt-in that adopts it wrote the tag back, read through ARM."
  exit 0
fi

step "4. the state cache - present, disposable, and never trusted"
explain \
  "The apply wrote a cache under .terraform/. Delete it, replan, require the" \
  "identical answer."
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "az-identity" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "az-identity" "plan without the cache failed: $PLAN2"
grep -E 'No changes\.' <<< "$PLAN2" | head -1 | evidence
grep -q "No changes." <<< "$PLAN2" || fail "az-identity" "deleting the cache changed the plan: $PLAN2"
proof "the cache was there and its loss changed nothing."

step "5. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-identity" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, 4 destroyed" <<< "$DESTROY_OUT" \
  || fail "az-identity" "destroy did not remove exactly the 4 created resources: $DESTROY_OUT"
if arm GET "${SUB}/resourcegroups/${RG}?api-version=2021-04-01" >/dev/null 2>&1; then
  fail "az-identity" "the resource group ${RG} still exists after destroy"
fi
proof "4 destroyed, and ARM no longer answers for the group."

echo "  What you watched: an Azure estate on floci-az with its ownership carried"
echo "  as two ARM tags any client reads, planned from those tags with no state."
