# no-silent-orphans
# CLAIM 1 - No Silent Orphans: a resource this estate owns cannot fall out of its plans unnoticed. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/no-silent-orphans"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up

export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "No Silent Orphans: once this estate owns a resource, no later run can" \
  "quietly forget it. The failure this targets is stock's oldest one - a" \
  "state entry lost to a crash, a bad merge, or surgery, and the live" \
  "resource silently drops out of every future plan, still running," \
  "still billing, invisible. Here the record of ownership is ON the" \
  "resource, so forgetting would take losing the resource itself."

step "1. stand the estate up"
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "orphans" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "orphans" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$APPLY_OUT" | grep -oE '[0-9]+')"
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" --query 'Vpcs[0].VpcId' --output text)"
proof "$ADDED resources, each carrying its ownership markers from the create call."

step "2. the crash shape - a resource created, then everyone forgot"
explain \
  "Simulate an apply that died right after a create call succeeded: this" \
  "makes a subnet with the AWS CLI and stamps the estate's markers on it," \
  "as choudoufu's create would have, for an address no run ever recorded" \
  "anywhere. Under stock, a crash in this window is the canonical silent" \
  "orphan - the resource exists, the state never heard of it."
cmd "aws ec2 create-subnet ... + create-tags tofu-estate/tofu-address=aws_subnet.crashed"
CRASHED="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.0.99.0/24 --query 'Subnet.SubnetId' --output text)"
[ -n "$CRASHED" ] || fail "orphans" "could not create the crash-shaped subnet"
if [ "${BREAK:-0}" != "1" ]; then
  awsl ec2 create-tags --resources "$CRASHED" \
    --tags "Key=tofu-estate,Value=stateless-e2e-block" "Key=tofu-address,Value=aws_subnet.crashed" \
    || fail "orphans" "could not mark the crash-shaped subnet"
  echo "created $CRASHED, marked as aws_subnet.crashed - and no run has ever heard of it" | evidence
else
  explain \
    "BREAK control: the markers are deliberately NOT written, making this" \
    "the one thing the claim cannot cover - an unmarked resource is not" \
    "provably the estate's. If the plan still names it as the estate's" \
    "own removal, the assertions below are guessing; if the plan cannot," \
    "the check below must fail, proving it is load-bearing."
  echo "created $CRASHED with NO markers" | evidence
fi

step "3. the next plan finds it - nobody had to remember"
explain \
  "A plan now reads the live system. The marker on the subnet says this" \
  "estate owns an aws_subnet.crashed; the configuration declares no such" \
  "block; so the plan must PROPOSE DESTROYING it, by name. That is the" \
  "claim: owned-but-undeclared is a plan line, never a silence."
cmd "choudoufu plan"
POUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "orphans" "plan failed: $POUT"
if [ "${BREAK:-0}" = "1" ]; then
  if grep -q "aws_subnet.crashed" <<< "$POUT"; then
    fail "orphans" "BREAK: the plan named an UNMARKED resource as the estate's own removal - the naming assertion is guessing"
  fi
  awsl ec2 delete-subnet --subnet-id "$CRASHED" >/dev/null 2>&1 || true
  proof "caught: without the marker, the plan cannot claim it - so the naming check below is a real check, and unmarked resources are exactly the boundary the claim states."
  exit 0
fi
grep -E "aws_subnet.crashed" <<< "$POUT" | head -1 | evidence
grep -q "aws_subnet.crashed" <<< "$POUT" \
  || fail "orphans" "the crash-shaped orphan is not in the plan - a silent orphan: $POUT"
grep -qE 'will be destroyed|1 to destroy|to destroy' <<< "$POUT" \
  || fail "orphans" "the orphan was named but no destroy is proposed: $POUT"
proof "the orphan surfaced by its own marker, with a proposed destroy. The crash window that silently loses a resource under stock does not exist here."

step "4. a deleted block is the same story"
explain \
  "The everyday case: an engineer deletes the security group's block from" \
  "the configuration. The live security group still carries this" \
  "estate's markers, so the next plan proposes destroying it too - now" \
  "two orphans, both named."
cmd "delete the aws_security_group block; choudoufu plan"
python3 - "$SMOKE_WORK/network.tf" <<'PYEOF'
import io,re,sys
p=sys.argv[1]
s=io.open(p,encoding='utf-8').read()
s=re.sub(r'resource "aws_security_group" "main" \{.*?\n\}\n', '', s, flags=re.S)
io.open(p,'w',encoding='utf-8').write(s)
PYEOF
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "orphans" "plan after block removal failed: $P2"
grep -E 'Plan: 0 to add, 0 to change, 2 to destroy' <<< "$P2" | evidence
grep -qE 'Plan: 0 to add, 0 to change, 2 to destroy' <<< "$P2" \
  || fail "orphans" "expected exactly 2 destroys (the crashed subnet and the removed-block SG): $P2"
proof "two owned resources with no blocks, two proposed destroys, zero guessing. Nothing else in the account was touched or proposed."

step "5. applying removes them - loudly, exactly"
cmd "choudoufu apply -auto-approve"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "orphans" "the reconverging apply failed: $AOUT"
grep -E 'Apply complete!' <<< "$AOUT" | evidence
grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$AOUT" \
  || fail "orphans" "the apply did not destroy exactly the two orphans: $AOUT"
proof "both orphans are gone, by an ordinary reviewed apply - found, named, removed."

step "6. where the machinery does not reach, it says so out loud"
explain \
  "Honesty is part of the claim. Two of this estate's types (the log" \
  "group and the bucket) are admitted by the provider's identity schema" \
  "rather than the fork's own table, and the orphan sweep does not cover" \
  "them yet - and the apply SAID SO, up front, naming each type and the" \
  "consequence. Degrading to a loud warning is allowed; silence is not."
grep -E 'no orphan recovery' <<< "$APPLY_OUT" | head -1 | evidence
for t in aws_cloudwatch_log_group aws_s3_bucket; do
  grep -q "$t is admitted by the provider's own identity schema" <<< "$APPLY_OUT" \
    || fail "orphans" "the apply did not warn about $t's missing orphan recovery - the boundary went silent"
done
proof "the claim's boundary is announced by the tool itself, at apply time, before anything could be lost."

step "7. the same claim where values live in the record store"
explain \
  "Everything above surfaced because discovery reads AWS. Record-backed" \
  "resources (terraform_data and friends) have no cloud home - their" \
  "values live in the live backend's record store, here the implied" \
  "local one. The claim must hold there too: delete the block, and the" \
  "recorded resource surfaces from the store's own List - no state" \
  "file consulted, no cloud involved at all."
R="$SMOKE_WORKROOT/orphan-records"; mkdir -p "$R"
cat > "$R/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-orphan-records"
  }
}

resource "terraform_data" "kept" {
  input = "k"
}

resource "terraform_data" "forgotten" {
  input = "f"
}
TFEOF
cmd "choudoufu apply ; delete the forgotten block ; choudoufu plan"
( cd "$R" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "orphans" "record init failed"
ROUT="$(cd "$R" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "orphans" "record apply failed: $ROUT"
cat > "$R/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-orphan-records"
  }
}

resource "terraform_data" "kept" {
  input = "k"
}
TFEOF
RP="$(cd "$R" && chdf plan -input=false -no-color 2>&1)" || fail "orphans" "record plan failed: $RP"
grep -q 'terraform_data.forgotten will be destroyed' <<< "$RP" \
  || fail "orphans" "the recorded resource fell out of the plan silently: $RP"
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$RP" \
  || fail "orphans" "the record plan proposed more than the one destroy: $RP"
grep -E 'terraform_data.forgotten will be destroyed' <<< "$RP" | head -1 | evidence
( cd "$R" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "orphans" "record estate teardown failed"
proof "same guarantee, third backend piece: the store's List is the inventory, so a forgotten record falls INTO the plan too."

step "8. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "orphans" "teardown failed: $DOUT"
REMAINING=$((ADDED-1))
grep -qE "Resources: 0 added, 0 changed, $REMAINING destroyed" <<< "$DOUT" \
  || fail "orphans" "teardown did not remove exactly the $REMAINING remaining resources: $DOUT"
proof "$REMAINING destroyed - the estate is gone."

echo "  What you watched: a crashed create and a deleted block both surface"
echo "  as named plan lines and get removed by an ordinary apply - for cloud"
echo "  and record-backed resources alike - and the one place the sweep"
echo "  cannot reach announced itself at apply time."
echo "  Owned resources do not fall out of plans here - they fall INTO them."
