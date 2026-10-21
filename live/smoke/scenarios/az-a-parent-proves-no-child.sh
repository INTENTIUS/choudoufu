# az-a-parent-proves-no-child
# CLAIM 49 (azure) - A parent's marker never proves its child exists. ~3 min.
#
# This proof: on floci-az, two parents that carry the estate's ARM tags
# each have a child that takes none: an azurerm_subnet under a VNet, and an
# azurerm_network_security_rule under a network security group. Both
# children are deleted out of band with ARM DELETEs. The next plan must
# propose exactly the two creates while both parents stand and keep their
# tofu-estate tag, and the apply must put both children back. A child is
# read for itself on every plan; its parent's marker says nothing about it.
#
# Claim D2 of #2119. BREAK=1 runs the identical deletes and holds the plan
# to what a plan inferring the children from their parents would read,
# No changes.; that assertion must fail, and the control passes only when
# the plan instead proposes the two creates, because without it the
# creates would read the same if every plan proposed them.
#
# Not yet run, so its cell reads unproven. A plan's sweep is a Resource
# Graph query, which floci-az runs as sent, union, mv-expand and bag_keys
# included, since dfa33ca (lex00/floci-az#11; live/FLOCI.md, Azure).
# That floci-az serves security rules as children of a network security
# group is assumed from live/FLOCI.md's network row, not confirmed.

SMOKE_WORK="$SMOKE_WORKROOT/az-a-parent-proves-no-child"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az-parent"
RG="smoke-az-parent-rg"
SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
NET_API="2024-05-01"
VNET_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/virtualNetworks/parent-vnet"
NSG_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/networkSecurityGroups/parent-nsg"
SUBNET_ID="${VNET_ID}/subnets/parent-subnet"
RULE_ID="${NSG_ID}/securityRules/allow-ssh"

azure_up

azure_terraform_block "$ESTATE" > "$SMOKE_WORK/versions.tf" \
  || fail "az-a-parent-proves-no-child" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "parent-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.49.0.0/16"]
}

resource "azurerm_subnet" "app" {
  name                 = "parent-subnet"
  resource_group_name  = azurerm_resource_group.app.name
  virtual_network_name = azurerm_virtual_network.app.name
  address_prefixes     = ["10.49.1.0/24"]
}

resource "azurerm_network_security_group" "app" {
  name                = "parent-nsg"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}

resource "azurerm_network_security_rule" "ssh" {
  name                        = "allow-ssh"
  resource_group_name         = azurerm_resource_group.app.name
  network_security_group_name = azurerm_network_security_group.app.name
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "22"
  source_address_prefix       = "10.49.0.0/16"
  destination_address_prefix  = "*"
}
TF
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }
# owner <id> prints the tofu-estate tag ARM holds on a resource.
owner() {
  arm_tags "$1" "$NET_API" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tofu-estate", ""))'
}

step "1. two tagged parents, each with an untagged child"
explain \
  "The VNet and the network security group carry the estate's ARM tags." \
  "The subnet and the security rule are their children and take no tags:" \
  "each is found by the ARM id its declaration derives."
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-parent-init "az-a-parent-proves-no-child" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-parent-proves-no-child" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 5 added' <<< "$APPLY_OUT" \
  || fail "az-a-parent-proves-no-child" "apply did not report 5 added: $APPLY_OUT"
for id in "$VNET_ID" "$NSG_ID"; do
  [ "$(owner "$id")" = "$ESTATE" ] \
    || fail "az-a-parent-proves-no-child" "$id does not carry tofu-estate=$ESTATE after the apply"
done
proof "5 created, and both parents carry tofu-estate=$ESTATE."

step "2. delete both children out of band"
explain \
  "An ARM client deletes the subnet and the security rule. Their parents" \
  "are not touched: the VNet and the security group stand, still tagged" \
  "for this estate."
cmd "DELETE ${SUBNET_ID}"
arm DELETE "${SUBNET_ID}?api-version=${NET_API}" >/dev/null \
  || fail "az-a-parent-proves-no-child" "could not delete the subnet with an ARM DELETE"
cmd "DELETE ${RULE_ID}"
arm DELETE "${RULE_ID}?api-version=${NET_API}" >/dev/null \
  || fail "az-a-parent-proves-no-child" "could not delete the security rule with an ARM DELETE"
for id in "$SUBNET_ID" "$RULE_ID"; do
  if arm GET "${id}?api-version=${NET_API}" >/dev/null 2>&1; then
    fail "az-a-parent-proves-no-child" "ARM still answers for $id after its DELETE"
  fi
done
for id in "$VNET_ID" "$NSG_ID"; do
  [ "$(owner "$id")" = "$ESTATE" ] \
    || fail "az-a-parent-proves-no-child" "$id lost its tofu-estate tag when its child was deleted"
done
proof "both children gone (ARM: 404), both parents standing and still tagged for $ESTATE."

PLAN_OUT="$(plan)" || fail "az-a-parent-proves-no-child" "the plan after the deletes failed: $PLAN_OUT"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - read the plan as one that infers children from parents"
  explain \
    "You asked for proof the plan reads each child for itself. A plan that" \
    "took a tagged parent as proof of its children would read No changes." \
    "here. This holds the real plan to that and requires the check to fail:" \
    "the plan must propose the two creates instead."
  cmd "choudoufu plan   (expecting No changes. from an inferring plan)"
  { grep -E '^Plan:|^No changes|will be created' <<< "$PLAN_OUT" || true; } | evidence
  if grep -q "No changes." <<< "$PLAN_OUT"; then
    fail "az-a-parent-proves-no-child" "BREAK: the plan reads No changes. with both children deleted, so it inferred them from their tagged parents: $PLAN_OUT"
  fi
  grep -qE 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
    || fail "az-a-parent-proves-no-child" "BREAK: the inferring plan's check failed, but the plan is not the two creates either: $PLAN_OUT"
  proof "caught. An inferring plan's No changes. is not what the plan says; it proposes the two creates."
  exit 0
fi

step "3. the plan proposes both children again"
explain \
  "Neither child carries a marker, and their parents' markers say nothing" \
  "about them. Each is read at its derived id, answers 404, and is" \
  "proposed for creation. The parents are unchanged."
cmd "choudoufu plan"
{ grep -E '^Plan:|will be created' <<< "$PLAN_OUT" || true; } | evidence
grep -qE 'Plan: 2 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || fail "az-a-parent-proves-no-child" "the plan does not propose exactly the two creates: $PLAN_OUT"
grep -qE 'azurerm_subnet\.app will be created' <<< "$PLAN_OUT" \
  || fail "az-a-parent-proves-no-child" "the plan does not propose creating azurerm_subnet.app: $PLAN_OUT"
grep -qE 'azurerm_network_security_rule\.ssh will be created' <<< "$PLAN_OUT" \
  || fail "az-a-parent-proves-no-child" "the plan does not propose creating azurerm_network_security_rule.ssh: $PLAN_OUT"
proof "exactly the two creates, with both parents present and marked."

step "4. apply - both children come back"
cmd "choudoufu apply -auto-approve"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-parent-proves-no-child" "apply failed: $APPLY2"
{ grep -E 'Apply complete!' <<< "$APPLY2" || true; } | evidence
grep -qE 'Resources: 2 added, 0 changed, 0 destroyed' <<< "$APPLY2" \
  || fail "az-a-parent-proves-no-child" "apply did not add exactly the two children: $APPLY2"
for id in "$SUBNET_ID" "$RULE_ID"; do
  arm GET "${id}?api-version=${NET_API}" >/dev/null \
    || fail "az-a-parent-proves-no-child" "ARM does not answer for $id after the apply"
done
REPLAN="$(plan)" || fail "az-a-parent-proves-no-child" "the replan failed: $REPLAN"
grep -q "No changes." <<< "$REPLAN" || fail "az-a-parent-proves-no-child" "the replan is not empty: $REPLAN"
proof "both children back, read through ARM, and the replan empty."

step "5. destroy - exactly what was made"
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-parent-proves-no-child" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "az-a-parent-proves-no-child" 5 "$DESTROY_OUT"
proof "5 destroyed. The estate is gone."

echo "  What you watched: two untagged Azure children deleted out of band"
echo "  under tagged parents, proposed again by the next plan and put back,"
echo "  because a parent's tag never stands in for its child."
