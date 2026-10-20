# az-a-removed-derived-block-is-destroyed
# CLAIM 50 (azure) - A removed derived block is destroyed from its record. ~4 min.
#
# This proof: on floci-az, a type that takes no ARM tags cannot be found
# by the sweep, because the Resource Graph query filters on the estate's
# tofu-estate tag and it carries none. So when its block is deleted, the
# estate's record is the only thing that still names it. With the record,
# the plan proposes the destroy at the deleted block's address: the
# Resource Graph pass runs the record-store removal leg and rebuilds the
# ARM id from the record (live/READINESS.md, Azure). With the record lost
# as well, the plan cannot destroy it, and the gap is reported instead.
#
# Claim D3 of #2119, claim 1's sibling for this tier. BREAK=1 loses the
# security rule's record before its block is deleted and requires the
# destroy to disappear with it: if the plan still proposed it, the destroy
# came from something other than the record, and the first half would
# prove nothing about records.
#
# Step 4 is #2119's restated D3: with the record lost, nothing left in the
# run names the subnet, so no plan can destroy it. What the plan does
# report is the gap: every untaggable type the configuration still
# declares is listed under "Not swept for removal" as TYPE_NOT_TAGGABLE
# (here azurerm_network_security_rule, for the rule that stays), and
# live/RECOVERY.md documents the leak and its recovery, restoring the
# record blob's previous version. azapi_resource subnets are left out: the
# removal leg does not read azapi_resource records yet.
#
# Not yet run, so its cell reads unproven. It waits on the emulator:
# a plan's sweep is a Resource Graph query. floci-az serves Resource Graph
# since 30000b0, but its KQL subset refuses the query's union, mv-expand
# and bag_keys with 400 InvalidQuery (live/FLOCI.md, Azure).

SMOKE_WORK="$SMOKE_WORKROOT/az-a-removed-derived-block-is-destroyed"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-az-removed"
RG="smoke-az-removed-rg"
RECORDS="$SMOKE_WORK/.tofu-records"
LOST="$SMOKE_WORK/lost-records"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
NET_API="2024-05-01"
VNET_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/virtualNetworks/removed-vnet"
NSG_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.Network/networkSecurityGroups/removed-nsg"
SSH_ID="${NSG_ID}/securityRules/allow-ssh"
HTTPS_ID="${NSG_ID}/securityRules/allow-https"
EDGE_ID="${VNET_ID}/subnets/removed-subnet-edge"

azure_up

azure_terraform_block "$ESTATE" > "$SMOKE_WORK/versions.tf" \
  || fail "az-a-removed-derived-block-is-destroyed" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$SMOKE_WORK/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_virtual_network" "app" {
  name                = "removed-vnet"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
  address_space       = ["10.50.0.0/16"]
}

resource "azurerm_network_security_group" "app" {
  name                = "removed-nsg"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}

# The rule that stays: an untaggable type the configuration still declares
# once the other blocks are gone.
resource "azurerm_network_security_rule" "https" {
  name                        = "allow-https"
  resource_group_name         = azurerm_resource_group.app.name
  network_security_group_name = azurerm_network_security_group.app.name
  priority                    = 110
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "443"
  source_address_prefix       = "*"
  destination_address_prefix  = "*"
}
TF
# Each removable block in a file of its own, so deleting the block is
# deleting the file.
cat > "$SMOKE_WORK/ssh.tf" <<TF
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
  source_address_prefix       = "10.50.0.0/16"
  destination_address_prefix  = "*"
}
TF
cat > "$SMOKE_WORK/edge.tf" <<TF
resource "azurerm_subnet" "edge" {
  name                 = "removed-subnet-edge"
  resource_group_name  = azurerm_resource_group.app.name
  virtual_network_name = azurerm_virtual_network.app.name
  address_prefixes     = ["10.50.1.0/24"]
}
TF
plan() { ( cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 ); }
# lose_record <type> moves every record of one resource type out of the
# store. A record key is tofu-records/<estate>/<type>/<encoded address>
# (projection.RecordKey), so the type is a directory of its own. Both
# rules' records go together; the rule that stays is still declared and
# found by its derived id.
lose_record() {
  local dir
  dir="$(find "$RECORDS" -type d -name "$1" | head -1)"
  [ -n "$dir" ] || return 1
  mkdir -p "$LOST" && mv "$dir" "$LOST/$1"
}

step "1. a VNet, a security group, two rules and a subnet; the rules and the subnet untagged"
cmd "choudoufu init && choudoufu apply -auto-approve"
logged az-removed-init "az-a-removed-derived-block-is-destroyed" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-removed-derived-block-is-destroyed" "apply failed: $APPLY_OUT"
{ grep -E 'Apply complete!' <<< "$APPLY_OUT" || true; } | evidence
grep -qE 'Apply complete! Resources: 6 added' <<< "$APPLY_OUT" \
  || fail "az-a-removed-derived-block-is-destroyed" "apply did not report 6 added: $APPLY_OUT"
( cd "$SMOKE_WORK" && find .tofu-records -type d -name 'azurerm_*' | sort ) | evidence
for t in azurerm_network_security_rule azurerm_subnet; do
  [ -n "$(find "$RECORDS" -type d -name "$t" 2>/dev/null)" ] \
    || fail "az-a-removed-derived-block-is-destroyed" "no record for $t under .tofu-records after the apply"
done
proof "6 created, and the estate's record store holds records for the security rules and for the subnet."

step "2. delete the ssh rule's block; its record proposes the destroy"
explain \
  "The security rule carries no tag, so the Resource Graph sweep cannot" \
  "list it. Its record still names azurerm_network_security_rule.ssh," \
  "which no block declares now, so the record is what must propose the" \
  "destroy. The cache is deleted first, so the record is the only thing" \
  "that remembers."
cmd "rm ssh.tf .terraform/choudoufu-cache.tfstate && choudoufu plan"
rm -f "$SMOKE_WORK/ssh.tf" "$CACHE"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - lose the rule's record first; the destroy must go with it"
  explain \
    "You asked for proof the destroy is the record's. This moves the" \
    "security rules' records out of the store before the plan. Nothing" \
    "else can name a deleted, untagged rule, so the plan must propose no" \
    "destroy. If it still does, the destroy came from somewhere other" \
    "than the record."
  lose_record azurerm_network_security_rule || fail "az-a-removed-derived-block-is-destroyed" "BREAK: could not find the rule's record to lose"
  BOUT="$(plan)" || fail "az-a-removed-derived-block-is-destroyed" "BREAK: the plan failed: $BOUT"
  { grep -E '^Plan:|^No changes|will be destroyed' <<< "$BOUT" || true; } | evidence
  if grep -qE 'azurerm_network_security_rule\.ssh will be destroyed' <<< "$BOUT"; then
    fail "az-a-removed-derived-block-is-destroyed" "BREAK: with its record lost the plan still proposes destroying the rule - the destroy is not the record's doing: $BOUT"
  fi
  arm GET "${SSH_ID}?api-version=${NET_API}" >/dev/null 2>&1 \
    || fail "az-a-removed-derived-block-is-destroyed" "BREAK: the ssh rule is gone after a plan"
  proof "caught. Without its record the deleted rule's destroy is not proposed, so the destroy below is the record's and nothing else."
  exit 0
fi

PLAN_OUT="$(plan)" || fail "az-a-removed-derived-block-is-destroyed" "plan failed: $PLAN_OUT"
{ grep -E '^Plan:|will be destroyed' <<< "$PLAN_OUT" || true; } | head -3 | evidence
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLAN_OUT" \
  || fail "az-a-removed-derived-block-is-destroyed" "the plan does not propose exactly one destroy: $PLAN_OUT"
grep -qE 'azurerm_network_security_rule\.ssh will be destroyed' <<< "$PLAN_OUT" \
  || fail "az-a-removed-derived-block-is-destroyed" "the one destroy is not azurerm_network_security_rule.ssh: $PLAN_OUT"
proof "exactly one destroy, at the address the deleted block declared and its record holds."

step "3. apply - the ssh rule goes, its security group and the https rule stay"
cmd "choudoufu apply -auto-approve"
APPLY2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-removed-derived-block-is-destroyed" "apply failed: $APPLY2"
{ grep -E 'Apply complete!' <<< "$APPLY2" || true; } | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY2" \
  || fail "az-a-removed-derived-block-is-destroyed" "apply did not destroy exactly one resource: $APPLY2"
if arm GET "${SSH_ID}?api-version=${NET_API}" >/dev/null 2>&1; then
  fail "az-a-removed-derived-block-is-destroyed" "the ssh rule still exists after the apply"
fi
arm GET "${NSG_ID}?api-version=${NET_API}" >/dev/null 2>&1 || fail "az-a-removed-derived-block-is-destroyed" "the security group went with its rule"
arm GET "${HTTPS_ID}?api-version=${NET_API}" >/dev/null 2>&1 || fail "az-a-removed-derived-block-is-destroyed" "the https rule went with the ssh rule"
proof "the ssh rule is gone (ARM: 404), and the security group and the https rule are still there."

step "4. lose the subnet's record, then delete its block"
explain \
  "Now both things that name the subnet go at once: its record is" \
  "moved out of the store, its block is deleted, and the cache with" \
  "them. No plan can propose destroying an object nothing names, so" \
  "the subnet stays live. The plan reports the gap instead: the" \
  "untaggable types it still knows are listed under Not swept for" \
  "removal as TYPE_NOT_TAGGABLE, and live/RECOVERY.md says how to get" \
  "the record back."
cmd "mv .tofu-records/.../azurerm_subnet lost-records/ && rm edge.tf && choudoufu plan -verbose"
lose_record azurerm_subnet || fail "az-a-removed-derived-block-is-destroyed" "could not find the subnet's record to lose"
rm -f "$SMOKE_WORK/edge.tf" "$CACHE"
LOUT="$( cd "$SMOKE_WORK" && chdf plan -verbose -input=false -no-color 2>&1 )" \
  || fail "az-a-removed-derived-block-is-destroyed" "the plan with the record lost failed: $LOUT"
LOST_PLAN="$(smoke_log_dir)/az-a-removed-derived-block-is-destroyed-lost.plan"
mkdir -p "$(smoke_log_dir)"
printf '%s\n' "$LOUT" > "$LOST_PLAN"
{ grep -E '^Plan:|^No changes|will be destroyed|^Not swept for removal|TYPE_NOT_TAGGABLE' <<< "$LOUT" || true; } | head -8 | evidence
if grep -qE 'will be destroyed|[1-9][0-9]* to destroy' <<< "$LOUT"; then
  fail "az-a-removed-derived-block-is-destroyed" "the plan proposes a destroy with nothing left to name the subnet: $LOUT"
fi
GAP="$(grep -E 'TYPE_NOT_TAGGABLE' <<< "$LOUT" || true)"
grep -qF 'azurerm_network_security_rule' <<< "$GAP" \
  || fail "az-a-removed-derived-block-is-destroyed" "Not swept for removal does not list azurerm_network_security_rule as TYPE_NOT_TAGGABLE, so the gap the lost record falls into is unreported (full plan in $LOST_PLAN)"
if grep -qF 'azurerm_subnet.edge' <<< "$LOUT"; then
  fail "az-a-removed-derived-block-is-destroyed" "the plan names azurerm_subnet.edge, an address nothing left in this run can know"
fi
grep -qF 'A removed block of an untaggable type, record lost' "$ROOT/live/RECOVERY.md" \
  && grep -qE '^- Azure: the `azurerm` store' "$ROOT/live/RECOVERY.md" \
  || fail "az-a-removed-derived-block-is-destroyed" "live/RECOVERY.md no longer documents the lost-record leak and the azurerm store's recovery"
arm GET "${EDGE_ID}?api-version=${NET_API}" >/dev/null 2>&1 \
  || fail "az-a-removed-derived-block-is-destroyed" "the subnet is gone; nothing named it, so nothing may have deleted it"
proof "no destroy proposed, the subnet still live, the untaggable types it still knows listed as TYPE_NOT_TAGGABLE, and live/RECOVERY.md documenting the recovery."

step "5. clean up by hand, and destroy exactly what remains"
arm DELETE "${EDGE_ID}?api-version=${NET_API}" >/dev/null || fail "az-a-removed-derived-block-is-destroyed" "could not delete the subnet by hand"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-a-removed-derived-block-is-destroyed" "apply -destroy failed: $DESTROY_OUT"
destroyed_exactly "az-a-removed-derived-block-is-destroyed" 4 "$DESTROY_OUT"
proof "4 destroyed. The estate is gone."

echo "  What you watched: an untagged security rule whose block was deleted"
echo "  destroyed from its record, and an untagged subnet whose record and"
echo "  block went together left live, with the gap reported by type and"
echo "  never dropped without a word. Keep the record store and the second"
echo "  case is gone."
