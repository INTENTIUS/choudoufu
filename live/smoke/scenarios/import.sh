# import
# The migration path: stock stands the estate up, the state file is deleted, the estate plans empty from markers alone. ~2 min.
# container) stands an estate up with a plain local state file; the state
# file is deleted; choudoufu adopts the estate off its markers and plans
# empty. BREAK=1 strips one live marker and requires the plan to catch it.

SMOKE_WORK="$SMOKE_WORKROOT/import"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up

step "1. stock opentofu stands the estate up, plain local state"
stock init -input=false -no-color >/dev/null 2>&1 || fail "import" "stock init failed"
SOUT="$(stock apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "import" "stock apply failed: $SOUT"
grep -qE 'Apply complete! Resources: [1-9][0-9]* added' <<< "$SOUT" \
  || fail "import" "stock apply added nothing: $SOUT"
[ -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "import" "stock apply left no terraform.tfstate - this leg is supposed to be plain stock"
note "stock estate up: $(grep -oE 'Resources: [0-9]+ added' <<< "$SOUT") - a real terraform.tfstate on disk"

step "2. adoption is deleting the state file"
rm -f "$SMOKE_WORK/terraform.tfstate" "$SMOKE_WORK/terraform.tfstate.backup"
note "terraform.tfstate deleted; the estate itself never moved - identity lives on the resources"

export AWS_ENDPOINT_URL="http://localhost:${FLOCI_PORT}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "3. choudoufu init resolves providers"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "import" "choudoufu init failed"

step "3b. adoption is a tag you write - the two receipts take their markers"
# The receipts are the operator's own resources and deliberately carry no
# marker from the stock apply; adopting them is writing the two tags, with
# any tool that can write tags. That IS the ownership contract.
for pair in "/tofu-receipts/stateless-e2e/demo-effect:aws_ssm_parameter.demo_effect" \
            "/tofu-receipts/stateless-e2e/demo-existence:aws_ssm_parameter.demo_existence"; do
  PARAM="${pair%%:*}"; ADDR="${pair#*:}"
  awsl ssm add-tags-to-resource --resource-type Parameter --resource-id "$PARAM" \
    --tags "Key=tofu-estate,Value=stateless-e2e" "Key=tofu-address,Value=$ADDR" \
    || fail "import" "could not adopt $PARAM"
done
note "two aws CLI tag writes - no choudoufu command, no import ceremony"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip one live marker; the plan must catch it"
  VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[0].VpcId' --output text)"
  [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "import" "BREAK: could not find the marked VPC"
  awsl ec2 delete-tags --resources "$VPC_ID" --tags Key=tofu-address \
    || fail "import" "BREAK: could not strip the marker"
  BOUT="$(cd "$SMOKE_WORK" && chdf live-plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "import" "BREAK: the plan is still empty after a marker was stripped - the empty-plan assertion below is scenery"
  fi
  note "caught: the stripped marker changed the plan, so the assertions are load-bearing"
  exit 0
fi

step "3c. the first plan is the migration pass - slot markers land"
# aws_eip.pool's count instances take their stable tofu-slot markers on
# the first plan after adoption (the full harness's own step 3c); the
# emptiness claim belongs to the plan AFTER that pass.
# The migration plan names the slot each pool member should carry; the
# writes are ordinary CLI tag writes, the same instrument adoption used
# above - the full harness's own method (its step 3c).
MOUT="$(cd "$SMOKE_WORK" && chdf live-plan -input=false -no-color 2>&1)" \
  || fail "import" "the migration-pass live-plan failed: $MOUT"
LIVE_EIPS="$(awsl ec2 describe-addresses \
  --query 'Addresses[].[AllocationId,Tags[?Key==`tofu-address`]|[0].Value]' --output text)"
SLOTS=0
for i in 0 1 2; do
  SLOT="$(grep -A40 "aws_eip.pool\[$i\] will be updated" <<< "$MOUT" | grep -oE '"tofu-slot"[[:space:]]*=[[:space:]]*"[0-9]+"' | head -1 | grep -oE '[0-9]+')"
  [ -n "$SLOT" ] || continue
  ALLOC="$(awk -v want="aws_eip.pool:$i" '$2==want {print $1; exit}' <<< "$LIVE_EIPS")"
  [ -n "$ALLOC" ] || fail "import" "no live EIP carries tofu-address=aws_eip.pool:$i"
  awsl ec2 create-tags --resources "$ALLOC" --tags "Key=tofu-slot,Value=$SLOT" >/dev/null \
    || fail "import" "could not write tofu-slot=$SLOT onto $ALLOC"
  SLOTS=$((SLOTS+1))
done
note "$SLOTS tofu-slot markers written via the CLI - the plan named them, tags carried them"

step "4. the adopted estate plans empty, from markers alone"
POUT="$(cd "$SMOKE_WORK" && chdf live-plan -input=false -no-color 2>&1)" \
  || fail "import" "live-plan failed: $POUT"
grep -q "No changes." <<< "$POUT" || fail "import" "the adopted estate does not plan empty: $POUT"
note "No changes. - the state file was never the record; the tags were"

step "5. one identity, asserted by value against the cloud"
VPC_BY_TAG="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_BY_TAG" ] && [ "$VPC_BY_TAG" != "None" ] \
  || fail "import" "no VPC carries tofu-address=aws_vpc.main - convergence alone is never evidence, and this is the evidence"
note "aws_vpc.main = $VPC_BY_TAG, found by its marker with the AWS CLI - no choudoufu in that loop"
