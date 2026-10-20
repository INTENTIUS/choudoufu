# az-the-fence-gap-is-as-stated
# CLAIM 52 (azure) - The fence over derived types is what the docs say. ~5 min.
#
# This proof: live/MARKERS.md, "What the Azure grant cannot reach", says a
# declaration-carried Azure type (an untaggable child such as a subnet) is
# fenced by the resource group's role assignment like any tagged resource
# beside it, and loses only the Policy half: the tag guard's mode: Indexed
# never evaluates an untaggable type, and its denyAction never sees one
# deleted. This measures the Policy half of that sentence on azurerm_subnet.
# With the two definitions of live/azure/estate-boundary.json assigned at
# Alice's resource group, another estate's tagged create there is refused
# by Policy, while the same estate's subnet in Alice's VNet is created
# unopposed and an ARM delete of Alice's own subnet goes through. Alice's
# next plan proposes her subnet back.
#
# Claim D5 of #2119. BREAK=1 deletes the two assignments after the tagged
# create was refused, and requires that same create to land, because
# without that control the subnet writes going through would read the same
# if no Policy had been in force at all.
#
# Not yet run, so its cell reads unproven. Policy evaluation and role
# assignments with enforcement are served by floci-az since 30000b0, and
# the compose service turns both on. Two things still stand in the way
# there (live/FLOCI.md, Azure): floci-az's mode: Indexed skips only
# resource groups, where Azure skips every type without tags, so its marker
# guard denies Bob's untagged subnet create and that step fails for the
# emulator's reason; and every plan's sweep is a Resource Graph query whose
# union, mv-expand and bag_keys its KQL subset refuses. The RBAC half,
# which is what fences the subnet, is not in this scenario yet: every
# caller here is one principal.

SMOKE_WORK="$SMOKE_WORKROOT/az-the-fence-gap-is-as-stated"
ALICE="$SMOKE_WORK/alice"; BOB="$SMOKE_WORK/bob"
mkdir -p "$ALICE" "$BOB"; export SMOKE_WORK
RG="smoke-az-gap-alice-rg"
SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
NET_API="2024-05-01"
POLICY_API="2023-04-01"
VNET_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/virtualNetworks/alice-vnet"
ALICE_SUBNET_ID="${VNET_ID}/subnets/alice-app"
BOB_SUBNET_ID="${VNET_ID}/subnets/bob-intruder"

azure_up

azure_terraform_block "smoke-az-gap-alice" > "$ALICE/versions.tf" \
  || fail "az-the-fence-gap-is-as-stated" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$ALICE/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "alice-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.52.0.0/16"]
}

resource "azurerm_subnet" "app" {
  name                 = "alice-app"
  resource_group_name  = azurerm_resource_group.app.name
  virtual_network_name = azurerm_virtual_network.app.name
  address_prefixes     = ["10.52.1.0/24"]
}
TF
azure_terraform_block "smoke-az-gap-bob" > "$BOB/versions.tf" \
  || fail "az-the-fence-gap-is-as-stated" "could not read the azurerm and azapi pins from live/oracle-versions.json"
# bob_tagged and bob_subnet write Bob's one block: first a taggable
# identity in Alice's group, then an untaggable subnet in Alice's VNet.
bob_tagged() {
  cat > "$BOB/main.tf" <<TF
resource "azurerm_user_assigned_identity" "intruder" {
  name                = "bob-id"
  location            = "eastus"
  resource_group_name = "${RG}"
}
TF
}
bob_subnet() {
  cat > "$BOB/main.tf" <<TF
resource "azurerm_subnet" "intruder" {
  name                 = "bob-intruder"
  resource_group_name  = "${RG}"
  virtual_network_name = "alice-vnet"
  address_prefixes     = ["10.52.9.0/24"]
}
TF
}
bob_apply() { ( cd "$BOB" && chdf apply -auto-approve -input=false -no-color 2>&1 ); }

# boundary_assign installs the template's two definitions at the
# subscription and assigns both at Alice's group with
# estate=smoke-az-gap-alice, reading the policy rules out of
# live/azure/estate-boundary.json so what is measured is the file an
# operator applies (az-the-tag-is-the-boundary.sh does the same). "[[" is
# how a template escapes a literal "[", and a direct PUT carries the
# unescaped rule.
boundary_assign() {
  local defs
  defs="$(python3 - "$ROOT/live/azure/estate-boundary.json" <<'PY'
import json, sys
t = json.load(open(sys.argv[1]))
names = t["variables"]
for r in t["resources"]:
    if r["type"] != "Microsoft.Authorization/policyDefinitions":
        continue
    key = r["name"][len("[variables('"):-len("')]")]
    body = json.dumps({"properties": r["properties"]}).replace("[[", "[")
    print(names[key] + "\t" + body)
PY
)" || return 1
  [ "$(printf '%s\n' "$defs" | grep -c .)" = "2" ] || { echo "the template carries $(printf '%s\n' "$defs" | grep -c .) policy definitions, want 2" >&2; return 1; }
  local name body
  while IFS="$(printf '\t')" read -r name body; do
    arm PUT "${SUB}/providers/Microsoft.Authorization/policyDefinitions/${name}?api-version=${POLICY_API}" "$body" >/dev/null || return 1
    arm PUT "${SUB}/resourceGroups/${RG}/providers/Microsoft.Authorization/policyAssignments/${name}?api-version=${POLICY_API}" \
      "{\"properties\":{\"policyDefinitionId\":\"${SUB}/providers/Microsoft.Authorization/policyDefinitions/${name}\",\"enforcementMode\":\"Default\",\"parameters\":{\"estate\":{\"value\":\"smoke-az-gap-alice\"}}}}" >/dev/null || return 1
  done <<< "$defs"
}
boundary_unassign() {
  local name
  for name in choudoufu-estate-marker-guard choudoufu-estate-delete-guard; do
    arm DELETE "${SUB}/resourceGroups/${RG}/providers/Microsoft.Authorization/policyAssignments/${name}?api-version=${POLICY_API}" >/dev/null || return 1
  done
}

step "1. Alice's estate: a tagged VNet and an untagged subnet"
cmd "choudoufu init && choudoufu apply -auto-approve   (alice)"
logged az-gap-alice-init "az-the-fence-gap-is-as-stated" "Alice's init failed" -- in_dir "$ALICE" chdf init -input=false -no-color
AOUT="$(cd "$ALICE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-the-fence-gap-is-as-stated" "Alice's apply failed: $AOUT"
grep -qE 'Apply complete! Resources: 3 added' <<< "$AOUT" \
  || fail "az-the-fence-gap-is-as-stated" "Alice's apply did not report 3 added: $AOUT"
proof "Alice's group and VNet exist, marked tofu-estate=smoke-az-gap-alice, and her subnet beside them carries no tag."

step "2. the Policy fence, assigned at Alice's group"
explain \
  "The two definitions from live/azure/estate-boundary.json: deny a write" \
  "whose tofu-estate tag is missing or names another estate, and" \
  "denyAction on deleting a resource tagged for this estate. Both are" \
  "assigned at Alice's resource group."
cmd "PUT policyDefinitions x2, PUT policyAssignments x2 at ${RG}"
boundary_assign || fail "az-the-fence-gap-is-as-stated" "could not install the boundary's definitions and assignments"
proof "the fence is installed, as data, from the file an operator applies."

step "3. the fence holds for a taggable type"
explain \
  "Bob's estate declares an identity in Alice's group, so its create" \
  "carries tofu-estate=smoke-az-gap-bob. Policy must refuse it. This is" \
  "what the next step's writes are measured against."
logged az-gap-bob-init "az-the-fence-gap-is-as-stated" "Bob's init failed" -- in_dir "$BOB" chdf init -input=false -no-color
bob_tagged
cmd "choudoufu apply -auto-approve   (bob, an identity in Alice's group)"
BRC=0
BOUT="$(bob_apply)" || BRC=$?
{ grep -E '^Error|Policy' <<< "$BOUT" || true; } | head -3 | evidence
[ "$BRC" != "0" ] || fail "az-the-fence-gap-is-as-stated" "Bob's tagged create in Alice's group went through"
grep -qiE 'RequestDisallowedByPolicy|disallowed by policy' <<< "$BOUT" \
  || fail "az-the-fence-gap-is-as-stated" "Bob's tagged create was refused, but not by Policy: $BOUT"
proof "refused by Policy: another estate's tag in Alice's group."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - remove the policy; the refused create must go through"
  explain \
    "Both assignments are deleted and Bob's identity is applied again. It" \
    "must land now. If it is still refused, step 3's refusal came from" \
    "something other than the policy, and the subnet writes this scenario" \
    "measures would prove nothing about Policy."
  boundary_unassign || fail "az-the-fence-gap-is-as-stated" "BREAK: could not remove the assignments"
  BOUT2="$(bob_apply)" \
    || fail "az-the-fence-gap-is-as-stated" "BREAK: with the policy gone Bob's apply still failed: $BOUT2"
  grep -qE 'Apply complete! Resources: 1 added' <<< "$BOUT2" \
    || fail "az-the-fence-gap-is-as-stated" "BREAK: Bob's apply did not create his identity: $BOUT2"
  proof "caught. With the assignments removed the same create landed, so step 3's refusal was the policy's."
  exit 0
fi

step "4. the stated gap: Policy does not see an untaggable type"
explain \
  "Bob's estate now declares a subnet in Alice's VNet instead. A subnet" \
  "takes no tags, so the deny's mode: Indexed never evaluates it, and" \
  "live/MARKERS.md says the create goes through. Then a plain ARM client" \
  "deletes Alice's own subnet: denyAction covers only tagged resources," \
  "so that goes through as well. On real Azure the group's role" \
  "assignment is what would stop both; here every caller is one principal."
bob_subnet
cmd "choudoufu apply -auto-approve   (bob, a subnet in Alice's VNet)"
SOUT="$(bob_apply)" \
  || fail "az-the-fence-gap-is-as-stated" "Bob's subnet in Alice's VNet was refused, which live/MARKERS.md says Policy cannot do: $SOUT"
{ grep -E 'Apply complete!' <<< "$SOUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 1 added' <<< "$SOUT" \
  || fail "az-the-fence-gap-is-as-stated" "Bob's apply did not create his subnet: $SOUT"
arm GET "${BOB_SUBNET_ID}?api-version=${NET_API}" >/dev/null \
  || fail "az-the-fence-gap-is-as-stated" "ARM does not answer for Bob's subnet after his apply"
cmd "DELETE ${ALICE_SUBNET_ID}"
DOUT="$(arm DELETE "${ALICE_SUBNET_ID}?api-version=${NET_API}" 2>&1)" \
  || fail "az-the-fence-gap-is-as-stated" "deleting Alice's subnet was refused, which live/MARKERS.md says denyAction cannot do: $DOUT"
if arm GET "${ALICE_SUBNET_ID}?api-version=${NET_API}" >/dev/null 2>&1; then
  fail "az-the-fence-gap-is-as-stated" "ARM still answers for Alice's subnet after its DELETE"
fi
proof "both went through under the same policy that refused step 3: the subnet is outside the Policy half, as stated."

step "5. Alice's next plan proposes her subnet back"
cmd "choudoufu plan   (alice)"
POUT="$(cd "$ALICE" && chdf plan -input=false -no-color 2>&1)" \
  || fail "az-the-fence-gap-is-as-stated" "Alice's plan failed: $POUT"
{ grep -E '^Plan:|will be created' <<< "$POUT" || true; } | evidence
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$POUT" \
  || fail "az-the-fence-gap-is-as-stated" "Alice's plan does not propose exactly her subnet back: $POUT"
grep -qE 'azurerm_subnet\.app will be created' <<< "$POUT" \
  || fail "az-the-fence-gap-is-as-stated" "Alice's plan does not name azurerm_subnet.app: $POUT"
if grep -qF 'bob-intruder' <<< "$POUT"; then
  fail "az-the-fence-gap-is-as-stated" "Alice's plan names Bob's subnet, which is no block or record of hers"
fi
proof "Alice's plan proposes her subnet back and leaves Bob's alone."

step "6. teardown"
explain "The fence comes off first: its delete guard would refuse Alice's destroy."
boundary_unassign || fail "az-the-fence-gap-is-as-stated" "could not remove the assignments"
BDESTROY="$(cd "$BOB" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-the-fence-gap-is-as-stated" "Bob's destroy failed: $BDESTROY"
destroyed_exactly "az-the-fence-gap-is-as-stated" 1 "$BDESTROY"
ADESTROY="$(cd "$ALICE" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-the-fence-gap-is-as-stated" "Alice's destroy failed: $ADESTROY"
destroyed_exactly "az-the-fence-gap-is-as-stated" 2 "$ADESTROY"
proof "both estates are gone."

echo "  What you watched: Policy refusing another estate's tagged write in"
echo "  Alice's group, and letting the same estate create a subnet there and"
echo "  delete hers, because Policy never evaluates an untaggable type. The"
echo "  group's role assignment is the fence over the subnet."
