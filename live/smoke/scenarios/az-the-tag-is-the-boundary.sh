# az-the-tag-is-the-boundary
# CLAIM 13 (azure) - The tag is the boundary. ~5 min.
#
# This proof: on floci-az, the Policy half of the Azure fence
# (live/azure/estate-boundary.json, the shipped template, its two
# definitions and their assignments made with ARM calls rather than a
# deployment) refuses what another estate's apply and a plain ARM client
# try in Alice's resource group: a create whose tofu-estate tag names
# another estate, a create with no tag, and a delete of a resource marked
# for Alice. BREAK=1 removes the two assignments and requires those same
# three writes to go through.
#
# Written for GitHub issue #1120 (unit 15). Not yet run, so its cell reads
# unproven until the maintainer runs both arms on floci-az and the evidence
# file lands. Policy deny and denyAction evaluation (1ecb105) and role
# assignments with enforcement (02ae4a5, bb225ad) are served by floci-az
# since 30000b0, and the compose service turns both on; not yet run.
#
# What floci-az cannot answer today (live/FLOCI.md, Azure): every plan's
# sweep is a Resource Graph query, and its KQL subset refuses the query's
# union, mv-expand and bag_keys, so Alice's apply is expected to stop
# there. The BREAK arm would pass against an emulator that denied nothing,
# which is why it runs only after step 3's refusals have been seen. The
# RBAC half (live/azure/estate-grant.json) is not in this scenario yet:
# every caller here is the same principal, and the boundary measured is
# the tag's alone.

SMOKE_WORK="$SMOKE_WORKROOT/az-the-tag-is-the-boundary"
ALICE="$SMOKE_WORK/alice"; BOB="$SMOKE_WORK/bob"
mkdir -p "$ALICE" "$BOB"; export SMOKE_WORK
RG="smoke-az-alice-rg"
SUB="/subscriptions/${AZURE_SUBSCRIPTION_ID}"
MI_API="2023-01-31"
POLICY_API="2023-04-01"
ALICE_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/alice-id"
STRAY_ID="${SUB}/resourceGroups/${RG}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/stray-id"

azure_up

azure_terraform_block "smoke-az-alice" > "$ALICE/versions.tf" \
  || fail "az-boundary" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$ALICE/main.tf" <<TF
resource "azurerm_resource_group" "app" {
  name     = "${RG}"
  location = "eastus"
}

resource "azurerm_user_assigned_identity" "app" {
  name                = "alice-id"
  location            = azurerm_resource_group.app.location
  resource_group_name = azurerm_resource_group.app.name
}
TF
azure_terraform_block "smoke-az-bob" > "$BOB/versions.tf" \
  || fail "az-boundary" "could not read the azurerm and azapi pins from live/oracle-versions.json"
cat > "$BOB/main.tf" <<TF
resource "azurerm_user_assigned_identity" "intruder" {
  name                = "bob-id"
  location            = "eastus"
  resource_group_name = "${RG}"
}
TF

# boundary_assign installs the template's two definitions at the
# subscription and assigns both at Alice's group with estate=smoke-az-alice,
# reading the policy rules out of live/azure/estate-boundary.json so what is
# measured is the file an operator applies. "[[" is how a template escapes
# a literal "[", and a direct PUT carries the unescaped rule. Installing both
# definitions is the template with deleteLock=true: the delete lock is
# opt-in since the 2026-10-20 ruling on #1122, and this claim measures it on.
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
      "{\"properties\":{\"policyDefinitionId\":\"${SUB}/providers/Microsoft.Authorization/policyDefinitions/${name}\",\"enforcementMode\":\"Default\",\"parameters\":{\"estate\":{\"value\":\"smoke-az-alice\"}}}}" >/dev/null || return 1
  done <<< "$defs"
}
boundary_unassign() {
  local name
  for name in choudoufu-estate-marker-guard choudoufu-estate-delete-guard; do
    arm DELETE "${SUB}/resourceGroups/${RG}/providers/Microsoft.Authorization/policyAssignments/${name}?api-version=${POLICY_API}" >/dev/null || return 1
  done
}

step "1. Alice's estate"
cmd "choudoufu init && choudoufu apply -auto-approve   (alice)"
logged az-boundary-alice-init "az-boundary" "Alice's init failed" -- in_dir "$ALICE" chdf init -input=false -no-color
AOUT="$(cd "$ALICE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-boundary" "Alice's apply failed: $AOUT"
grep -qE 'Apply complete! Resources: 2 added' <<< "$AOUT" \
  || fail "az-boundary" "Alice's apply did not report 2 added: $AOUT"
proof "Alice's group and identity exist, marked tofu-estate=smoke-az-alice."

step "2. the fence: the shipped Policy template, assigned at Alice's group"
explain \
  "Two definitions from live/azure/estate-boundary.json: deny a write whose" \
  "tofu-estate tag is missing or names another estate, and denyAction on" \
  "deleting a resource tagged for this estate. Both are assigned at Alice's" \
  "resource group, the one group her estate owns."
cmd "PUT policyDefinitions x2, PUT policyAssignments x2 at ${RG}"
boundary_assign || fail "az-boundary" "could not install the boundary's definitions and assignments"
proof "the fence is installed, as data, from the file an operator applies."

# denied <what> <output>: the write must have been refused by Policy, by
# name, and not by anything else.
denied() {
  grep -qiE 'RequestDisallowedByPolicy|DeleteDisallowedByPolicy|disallowed by policy' <<< "$2" \
    || fail "az-boundary" "$1 was not refused by Policy: $2"
}

step "3. what the fence refuses"
explain \
  "Bob's estate declares an identity in Alice's group, so its create carries" \
  "tofu-estate=smoke-az-bob. A plain ARM client creates one with no tag at" \
  "all. And the same client deletes Alice's identity. Each must be refused" \
  "by Policy, and Alice's identity must still be there."
logged az-boundary-bob-init "az-boundary" "Bob's init failed" -- in_dir "$BOB" chdf init -input=false -no-color
cmd "choudoufu apply -auto-approve   (bob)"
BRC=0
BOUT="$(cd "$BOB" && chdf apply -auto-approve -input=false -no-color 2>&1)" || BRC=$?
grep -E '^Error|Policy' <<< "$BOUT" | head -3 | evidence
[ "$BRC" != "0" ] || fail "az-boundary" "Bob's apply into Alice's group went through"
denied "Bob's create" "$BOUT"
cmd "PUT ${STRAY_ID} with no tags"
SOUT="$(arm PUT "${STRAY_ID}?api-version=${MI_API}" '{"location":"eastus"}' 2>&1)" \
  && fail "az-boundary" "an untagged create in Alice's group went through: $SOUT"
denied "the untagged create" "$SOUT"
cmd "DELETE ${ALICE_ID}"
DOUT="$(arm DELETE "${ALICE_ID}?api-version=${MI_API}" 2>&1)" \
  && fail "az-boundary" "deleting Alice's identity went through: $DOUT"
denied "the delete of Alice's identity" "$DOUT"
arm GET "${ALICE_ID}?api-version=${MI_API}" >/dev/null \
  || fail "az-boundary" "Alice's identity is gone"
proof "three writes refused by Policy: another estate's tag, no tag, and a delete of a marked resource."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - remove the policy; the denied writes must go through"
  explain \
    "Both assignments are deleted. The same three writes are made again, and" \
    "each must now land. If one is still refused, step 3's refusals came" \
    "from something other than the policy."
  boundary_unassign || fail "az-boundary" "BREAK: could not remove the assignments"
  BOUT2="$(cd "$BOB" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "az-boundary" "BREAK: with the policy gone Bob's apply still failed: $BOUT2"
  grep -qE 'Apply complete! Resources: 1 added' <<< "$BOUT2" \
    || fail "az-boundary" "BREAK: Bob's apply did not create his identity: $BOUT2"
  arm PUT "${STRAY_ID}?api-version=${MI_API}" '{"location":"eastus"}' >/dev/null \
    || fail "az-boundary" "BREAK: the untagged create was still refused with the policy gone"
  arm DELETE "${ALICE_ID}?api-version=${MI_API}" >/dev/null \
    || fail "az-boundary" "BREAK: the delete of Alice's identity was still refused with the policy gone"
  proof "caught. With the assignments removed all three writes landed, so the refusals in step 3 were the policy's."
  exit 0
fi

step "4. teardown"
explain "The fence comes off first: its own delete guard would refuse Alice's destroy."
boundary_unassign || fail "az-boundary" "could not remove the assignments"
DESTROY_OUT="$(cd "$ALICE" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "az-boundary" "Alice's destroy failed: $DESTROY_OUT"
grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$DESTROY_OUT" \
  || fail "az-boundary" "Alice's destroy did not remove exactly her 2 resources: $DESTROY_OUT"
proof "Alice's estate is gone."
