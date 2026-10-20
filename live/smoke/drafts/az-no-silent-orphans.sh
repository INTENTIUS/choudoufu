# az-no-silent-orphans
# CLAIM 1 (azure) - Owned resources never fall out of a plan. ~4 min.
#
# This proof: on floci-az, a resource this estate owns whose block is
# deleted is proposed for removal by the next plan, found by its tofu-estate
# tag through the Resource Graph sweep (one query per subscription,
# matching the marker case-insensitively), and destroyed by the apply. A
# resource whose tag says another estate is never touched. BREAK=1 strips
# the orphan's tofu-estate tag with an ARM PATCH before its block is
# deleted, and requires the replan to leave it alone: an unmarked object is
# not this estate's to destroy.
#
# DRAFT: written for GitHub issue #1120 (unit 15) and never run
# (live/smoke/drafts/README.md). Claim 1's azure cell stays open until the
# maintainer runs both arms green and promotes it.
#
# What floci-az cannot answer today (live/FLOCI.md, Azure): it has no
# Microsoft.ResourceGraph route, and the sweep is a Resource Graph query, so
# step 2's plan is expected to be refused by name ("Microsoft.ResourceGraph
# not registered") until the fork serves it. Neither arm can pass before
# then; a BREAK run that passes against a sweep that never ran would prove
# nothing, which is why step 2 requires the plan to propose the removal
# before the control is read at all.

SMOKE_WORK="$SMOKE_WORKROOT/az-no-silent-orphans"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az"
RG="smoke-az-orphans-rg"

azure_up

azure_terraform_block "$ESTATE" > "$SMOKE_WORK/versions.tf" \
  || fail "az-orphans" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "smoke-az-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.41.0.0/16"]
}
TF
# The orphan-to-be, in a file of its own so deleting the block is deleting
# the file.
cat > "$SMOKE_WORK/orphan.tf" <<'TF'
resource "azurerm_user_assigned_identity" "doomed" {
  name                = "smoke-az-doomed"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}
TF

SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
MI_API="2023-01-31"
DOOMED_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smoke-az-doomed"
FOREIGN_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smoke-az-foreign"

step "1. an Azure estate, and a neighbour's resource in the same group"
explain \
  "Three resources under a live block: a resource group, a VNet and a" \
  "user-assigned identity. Then an identity nobody declares is created" \
  "beside them with an ARM PUT, tagged for another estate. The sweep reads" \
  "every tagged resource in the subscription, so this one is in its answer" \
  "and must be left alone."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-orphans-init "az-orphans" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-orphans" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: 3 added' <<< "$APPLY_OUT" \
  || fail "az-orphans" "apply did not report 3 added: $APPLY_OUT"
cmd "PUT ${FOREIGN_ID} tagged tofu-estate=someone-else"
arm PUT "${FOREIGN_ID}?api-version=${MI_API}" '{"location":"eastus","tags":{"tofu-estate":"someone-else","tofu-address":"azurerm_user_assigned_identity.doomed"}}' >/dev/null \
  || fail "az-orphans" "could not create the neighbour's identity with an ARM PUT"
proof "three resources this estate owns, and one beside them that another estate's tag claims at the same address."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip the orphan's tag first; the replan must leave it alone"
  explain \
    "This removes tofu-estate from the identity with an ARM PATCH before its" \
    "block is deleted. Nothing then says the object is this estate's, so" \
    "the replan must not propose destroying it. If it does, the sweep is" \
    "destroying by something other than the tag."
  cmd "PATCH ${DOOMED_ID} {\"tags\": {}}"
  arm PATCH "${DOOMED_ID}?api-version=${MI_API}" '{"tags":{}}' >/dev/null \
    || fail "az-orphans" "BREAK: could not strip the orphan's tags"
  rm -f "$SMOKE_WORK/orphan.tf"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
    || fail "az-orphans" "BREAK: the replan failed: $BOUT"
  grep -E 'No changes\.|^Plan:' <<< "$BOUT" | head -1 | evidence
  grep -q "No changes." <<< "$BOUT" \
    || fail "az-orphans" "BREAK: the replan proposes something for an object that carries no marker: $(grep -E 'will be|^Plan:' <<< "$BOUT" | head -5)"
  arm GET "${DOOMED_ID}?api-version=${MI_API}" >/dev/null \
    || fail "az-orphans" "BREAK: the untagged identity is gone"
  proof "caught. With its tag stripped the identity is nobody's, the replan proposes nothing, and ARM still answers for it."
  exit 0
fi

step "2. the block is deleted; the plan must propose removing what it made"
explain \
  "orphan.tf is deleted. No state file remembers the identity, and no" \
  "block declares it. Only its tofu-estate tag, read through the Resource" \
  "Graph sweep, says it is this estate's. The plan must propose destroying" \
  "it, at the address its tofu-address tag names, and nothing else: not" \
  "the neighbour that claims the same address under another estate's tag."
cmd "rm orphan.tf && choudoufu plan"
rm -f "$SMOKE_WORK/orphan.tf"
POUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "az-orphans" "the plan after deleting the block failed: $POUT"
grep -E 'will be destroyed|^Plan:' <<< "$POUT" | evidence
grep -q 'azurerm_user_assigned_identity.doomed will be destroyed' <<< "$POUT" \
  || fail "az-orphans" "the plan does not propose destroying the orphan at its address: $POUT"
grep -qE '^Plan: 0 to add, 0 to change, 1 to destroy\.' <<< "$POUT" \
  || fail "az-orphans" "the plan proposes more than the one removal; the neighbour or something else was swept in: $POUT"
proof "the orphan was found by its tag alone and proposed for removal at the address it carries; the other estate's object was not."

step "3. apply, and read the result through ARM"
cmd "choudoufu apply -auto-approve"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-orphans" "the apply failed: $AOUT"
grep -E 'Apply complete!' <<< "$AOUT" | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$AOUT" \
  || fail "az-orphans" "the apply did not destroy exactly the orphan: $AOUT"
if arm GET "${DOOMED_ID}?api-version=${MI_API}" >/dev/null 2>&1; then
  fail "az-orphans" "ARM still answers for the orphan after the apply that destroyed it"
fi
arm GET "${FOREIGN_ID}?api-version=${MI_API}" >/dev/null \
  || fail "az-orphans" "the neighbour's identity is gone; the sweep reached past this estate"
RPLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "az-orphans" "the replan failed: $RPLAN"
grep -q "No changes." <<< "$RPLAN" || fail "az-orphans" "the replan is not empty: $RPLAN"
proof "one destroyed, the neighbour untouched, and the replan empty."

step "4. teardown"
arm DELETE "${FOREIGN_ID}?api-version=${MI_API}" >/dev/null \
  || fail "az-orphans" "could not delete the neighbour's identity"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-orphans" "apply -destroy failed: $DOUT"
grep -qE "Resources: 0 added, 0 changed, 2 destroyed" <<< "$DOUT" \
  || fail "az-orphans" "destroy did not remove exactly the 2 resources left: $DOUT"
proof "the estate is gone."
