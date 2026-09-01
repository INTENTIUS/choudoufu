# import
# The migration path: stock stands the estate up, the state file is deleted, the estate plans empty from markers alone. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/import"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up

step "1. stock OpenTofu stands the estate up - plain local state"
explain \
  "This is the before picture: the PINNED stock OpenTofu, in its own" \
  "container, applies a 43-resource estate the ordinary way - a real" \
  "terraform.tfstate on disk, the file that is normally the one record" \
  "of what you own. The estate's config already carries its two" \
  "ownership tags on every taggable resource, so stock writes them as" \
  "ordinary tags without knowing they mean anything."
cmd "docker compose run opentofu init && ... apply -auto-approve"
stock init -input=false -no-color >/dev/null 2>&1 || fail "import" "stock init failed"
SOUT="$(stock apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "import" "stock apply failed: $SOUT"
grep -E 'Apply complete!' <<< "$SOUT" | head -1 | evidence
grep -qE 'Apply complete! Resources: [1-9][0-9]* added' <<< "$SOUT" \
  || fail "import" "stock apply added nothing: $SOUT"
[ -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "import" "stock apply left no terraform.tfstate - this leg is supposed to be plain stock"
proof "a normal stock estate, with a normal state file. Nothing choudoufu-specific has run."

step "2. adoption is deleting the state file"
explain \
  "Here is the whole migration. Not an export, not an import ceremony," \
  "not a format conversion: the state file is deleted, because the" \
  "identity it held is already sitting on the resources as tags. The" \
  "estate itself does not move, restart, or notice."
cmd "rm terraform.tfstate terraform.tfstate.backup"
rm -f "$SMOKE_WORK/terraform.tfstate" "$SMOKE_WORK/terraform.tfstate.backup"
proof "the one record stock had is gone. If the markers are not enough from here, everything below fails."

export AWS_ENDPOINT_URL="http://localhost:${FLOCI_PORT}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "3. choudoufu init - same config, provider resolved by the fork"
cmd "choudoufu init"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "import" "choudoufu init failed"
proof "no conversion step happened. It is the same configuration directory."

step "3b. two resources were deliberately unowned - adoption is a tag you write"
explain \
  "The estate's two receipts are SSM parameters the operator owns, so the" \
  "stock apply left them unmarked on purpose. Adopting them into the" \
  "estate is writing the two tags - with the AWS CLI, no choudoufu" \
  "command. Any tool that can write two tags can perform an adoption;" \
  "the tag pair IS the ownership contract."
cmd "aws ssm add-tags-to-resource ... tofu-estate=... tofu-address=..."
for pair in "/tofu-receipts/stateless-e2e/demo-effect:aws_ssm_parameter.demo_effect" \
            "/tofu-receipts/stateless-e2e/demo-existence:aws_ssm_parameter.demo_existence"; do
  PARAM="${pair%%:*}"; ADDR="${pair#*:}"
  awsl ssm add-tags-to-resource --resource-type Parameter --resource-id "$PARAM" \
    --tags "Key=tofu-estate,Value=stateless-e2e" "Key=tofu-address,Value=$ADDR" \
    || fail "import" "could not adopt $PARAM"
  echo "adopted $PARAM as $ADDR" | evidence
done
proof "two tag writes, two adoptions. Handover, splitting an estate, renaming - they are all this same operation."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip one live marker; the plan must catch it"
  explain \
    "You asked for proof the assertions can fail. This deletes the VPC's" \
    "tofu-address tag - after this, nothing anywhere says which VPC the" \
    "configuration's aws_vpc.main is. If the next plan is still empty," \
    "this scenario is scenery and the run fails itself."
  cmd "aws ec2 delete-tags --resources <vpc> --tags Key=tofu-address"
  VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[0].VpcId' --output text)"
  [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "import" "BREAK: could not find the marked VPC"
  awsl ec2 delete-tags --resources "$VPC_ID" --tags Key=tofu-address \
    || fail "import" "BREAK: could not strip the marker"
  BOUT="$(cd "$SMOKE_WORK" && chdf live-plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "import" "BREAK: the plan is still empty after a marker was stripped - the empty-plan assertion below is scenery"
  fi
  grep -E '^Plan:|Error:' <<< "$BOUT" | head -2 | evidence
  proof "caught. The stripped marker changed the plan, so the empty-plan claim below is a real check."
  exit 0
fi

step "3c. the migration pass - the count pool takes its slot markers"
explain \
  "One construct needs a third tag: aws_eip.pool has count = 3, and a" \
  "count address is a position, not a name. The first plan names the" \
  "stable tofu-slot each member should carry; writing them is - again -" \
  "ordinary tag writes, taken from the values the plan itself printed."
cmd "choudoufu live-plan   # then: aws ec2 create-tags ... tofu-slot=<n>"
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
  echo "aws_eip.pool[$i] -> $ALLOC gets tofu-slot=$SLOT" | evidence
  SLOTS=$((SLOTS+1))
done
proof "$SLOTS slot markers written. The migration is complete, and every write in it was a tag."

step "4. the adopted estate plans empty, from markers alone"
explain \
  "The claim this whole scenario exists for: 43 resources, created by" \
  "stock, state file gone - and the plan must be empty, because every" \
  "identity is recoverable from the resources themselves. Anything wrong" \
  "in the binding shows up here as a create or a destroy."
cmd "choudoufu live-plan"
POUT="$(cd "$SMOKE_WORK" && chdf live-plan -input=false -no-color 2>&1)" \
  || fail "import" "live-plan failed: $POUT"
grep -E 'No changes\.' <<< "$POUT" | head -1 | evidence
grep -q "No changes." <<< "$POUT" || fail "import" "the adopted estate does not plan empty: $POUT"
proof "migration from a stock state file, lossless, and the file was never converted - it was deleted."

step "5. one identity, checked by value against the cloud"
explain \
  "An empty plan alone can lie - a wrong identity can converge. So the" \
  "last check asks AWS directly, with no choudoufu in the loop: which" \
  "VPC carries the marker for aws_vpc.main?"
cmd "aws ec2 describe-vpcs --filters Name=tag:tofu-address,Values=aws_vpc.main"
VPC_BY_TAG="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_BY_TAG" ] && [ "$VPC_BY_TAG" != "None" ] \
  || fail "import" "no VPC carries tofu-address=aws_vpc.main - convergence alone is never evidence, and this is the evidence"
echo "aws_vpc.main = $VPC_BY_TAG" | evidence
proof "the estate is legible to any cloud tool. Whoever inherits it can list what they got before ever running the binary."

echo "  What you watched: a stock estate hand itself over by losing its"
echo "  state file, two deliberate adoptions performed as tag writes, and"
echo "  the whole 43-resource estate plan empty from its markers alone."
