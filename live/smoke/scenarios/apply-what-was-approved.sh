# apply-what-was-approved
# CLAIM 15 - Apply exactly what was approved: a saved plan crosses the approval gate, and the apply that consumes it still reads the live system - matching down to the planned values, or refusing by name. ~4 min.

SMOKE_WORK="$SMOKE_WORKROOT/approval"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up

export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "CI runs Terraform as: plan on the pull request, a human approves," \
  "apply exactly what was approved. The artifact that crosses that gate" \
  "is the plan file. Under live markers the world stays authoritative -" \
  "an apply always re-reads it and plans against what is there now - so" \
  "the plan file is not an instruction to replay. It is the record of" \
  "what was approved, and the apply compares its own fresh plan against" \
  "it. Same plan, it applies. Different plan, it refuses by name and" \
  "exits 3, which is a pipeline's signal to send the change back to" \
  "review."

step "1. stand the estate up"
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "approval" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "approval" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "approval" "the estate's VPC is not there after the apply"
proof "a live estate, every resource carrying its ownership markers."

step "2. the change under review - plan -out writes the artifact"
explain \
  "A reviewer's change: the log group's retention goes from one day to" \
  "three. The plan is saved with -out, the stock flag, into the stock" \
  "file format. This is what a pipeline attaches to the pull request and" \
  "what a human approves."
cmd "choudoufu plan -out=approved.tfplan"
sed_i "$SMOKE_WORK/logs.tf" 's/retention_in_days = 1/retention_in_days = 3/'
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color -out=approved.tfplan 2>&1)" \
  || fail "approval" "plan -out failed: $PLAN_OUT"
[ -f "$SMOKE_WORK/approved.tfplan" ] || fail "approval" "plan -out wrote no file: $PLAN_OUT"
grep -E "aws_cloudwatch_log_group.app will be updated|retention_in_days" <<< "$PLAN_OUT" | head -3 | evidence
grep -q "aws_cloudwatch_log_group.app" <<< "$PLAN_OUT" \
  || fail "approval" "the saved plan is not about the change under review: $PLAN_OUT"
proof "$(wc -c < "$SMOKE_WORK/approved.tfplan" | tr -d ' ') bytes of stock-format plan file, approved and ready to cross the gate."

step "3. the world moves while the approval waits"
explain \
  "Hours pass between the approval and the apply, and something else" \
  "touches the account: a subnet appears carrying this estate's markers" \
  "for an address the configuration does not declare. The next plan must" \
  "propose destroying it - which is a change nobody approved."
cmd "aws ec2 create-subnet ... + create-tags tofu-estate/tofu-address=aws_subnet.crashed"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the world is deliberately left UNMOVED. The refusal" \
    "below has to be conditional on the drift, not something the" \
    "comparison hands out to every plan file it is given - a check that" \
    "always fires is not a check. So this run must APPLY, and the" \
    "scenario fails if it refuses."
  echo "nothing changed out of band" | evidence
else
  CRASHED="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.0.99.0/24 --query 'Subnet.SubnetId' --output text)"
  [ -n "$CRASHED" ] || fail "approval" "could not create the out-of-band subnet"
  awsl ec2 create-tags --resources "$CRASHED" \
    --tags "Key=tofu-estate,Value=stateless-e2e-block" "Key=tofu-address,Value=aws_subnet.crashed" \
    || fail "approval" "could not mark the out-of-band subnet"
  echo "created $CRASHED, marked as aws_subnet.crashed - after the approval, before the apply" | evidence
fi
proof "the gap between review and apply, with something in it."

step "4. apply the approved plan"
explain \
  "The apply reads the live system and plans against what it finds, the" \
  "way every live-markers run does. It does not replay the file. It" \
  "compares: same resources, same actions, same live objects, same" \
  "planned values, or it refuses before anything changes."
cmd "choudoufu apply approved.tfplan"
CODE=0
GATE_OUT="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color approved.tfplan 2>&1)" || CODE=$?
if [ "${BREAK:-0}" = "1" ]; then
  if [ "$CODE" != "0" ]; then
    fail "approval" "BREAK: the apply refused (exit $CODE) with the world unmoved - the comparison refuses unconditionally, so the refusal it produces is not evidence of anything: $GATE_OUT"
  fi
  grep -E 'Apply complete!' <<< "$GATE_OUT" | evidence
  proof "caught - with nothing moved between review and apply the same file applied, so the refusal this scenario asserts is earned by the drift and not handed out to every plan file."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi
[ "$CODE" = "3" ] \
  || fail "approval" "the apply exited $CODE, want 3 - a plan file whose approval no longer covers the run must refuse with its own status: $GATE_OUT"
grep -q "The approved plan no longer matches the live system" <<< "$GATE_OUT" \
  || fail "approval" "the apply stopped, but not with the named refusal: $GATE_OUT"
# Everything from the refusal's own summary line onward. The plan above it
# also names the subnet, so asserting over the whole output would pass on a
# refusal that named nothing at all.
REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$GATE_OUT")"
grep -q "aws_subnet.crashed" <<< "$REFUSAL" \
  || fail "approval" "the refusal does not name the change nobody approved: $REFUSAL"
grep -q "Exit status 3" <<< "$REFUSAL" \
  || fail "approval" "the refusal does not tell a pipeline what its exit status means: $REFUSAL"
head -12 <<< "$REFUSAL" | evidence
echo "exit status: $CODE" | evidence
if grep -q "Apply complete!" <<< "$GATE_OUT"; then
  fail "approval" "the apply ran anyway after refusing: $GATE_OUT"
fi
proof "refused by name, exit 3, nothing applied - and the row it names is exactly the change that appeared after the approval."

step "5. the same change, a different value"
explain \
  "The subtler failure, and the one a comparison over resource names" \
  "alone would wave through: the change set is IDENTICAL - the same" \
  "resource, the same update, the same live log group - and what it" \
  "writes moved. Someone edited the configuration after the approval and" \
  "asked for fourteen days instead of three. The apply has to refuse" \
  "that too, and name the attribute."
cmd "edit retention_in_days 3 -> 14 ; choudoufu apply approved.tfplan"
awsl ec2 delete-subnet --subnet-id "$CRASHED" >/dev/null 2>&1 || fail "approval" "could not remove the out-of-band subnet"
sed_i "$SMOKE_WORK/logs.tf" 's/retention_in_days = 3/retention_in_days = 14/'
VCODE=0
VALUE_OUT="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color approved.tfplan 2>&1)" || VCODE=$?
[ "$VCODE" = "3" ] \
  || fail "approval" "the apply exited $VCODE, want 3 - the same change writing a different value must refuse like any other difference: $VALUE_OUT"
VALUE_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$VALUE_OUT")"
# Collapsed to one line first: the diagnostic printer folds at its width,
# and this assertion is about the sentence, not about where the fold landed.
VALUE_FLAT="$(tr '\n' ' ' <<< "$VALUE_REFUSAL" | tr -s ' ')"
grep -q "disagree about the values it writes" <<< "$VALUE_FLAT" \
  || fail "approval" "the refusal does not say the values moved: $VALUE_REFUSAL"
grep -q "after.retention_in_days" <<< "$VALUE_REFUSAL" \
  || fail "approval" "the refusal does not name the attribute that moved: $VALUE_REFUSAL"
head -14 <<< "$VALUE_REFUSAL" | evidence
echo "exit status: $VCODE" | evidence
sed_i "$SMOKE_WORK/logs.tf" 's/retention_in_days = 14/retention_in_days = 3/'
proof "refused again, exit 3, and it names after.retention_in_days - the values are part of what was approved, not just the resource names."

step "6. re-plan, re-approve, apply"
explain \
  "The way forward the refusal names: plan again against the world as it" \
  "is now, review THAT, and apply the artifact it produced. The same two" \
  "commands, and this time they agree."
cmd "choudoufu plan -out=approved.tfplan && choudoufu apply approved.tfplan"
( cd "$SMOKE_WORK" && chdf plan -input=false -no-color -out=approved.tfplan >/dev/null 2>&1 ) \
  || fail "approval" "the re-plan failed"
SECOND="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color approved.tfplan 2>&1)" \
  || fail "approval" "the re-approved apply failed: $SECOND"
grep -E 'Apply complete!' <<< "$SECOND" | evidence
grep -q "Apply complete!" <<< "$SECOND" || fail "approval" "the re-approved apply did not complete: $SECOND"
RETENTION="$(awsl logs describe-log-groups --log-group-name-prefix "/stateless-e2e-block/app" --query 'logGroups[0].retentionInDays' --output text)"
[ "$RETENTION" = "3" ] \
  || fail "approval" "the approved change did not land: retention is $RETENTION, want 3"
proof "retention is $RETENTION days, exactly what was reviewed. The gate held twice and then let the right thing through."

step "7. teardown"
cmd "choudoufu apply -destroy -auto-approve"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "approval" "teardown failed"
proof "estate gone."

echo "  What you watched: a plan saved to a file, approved, and then"
echo "  applied against a world that had moved underneath it - refused by"
echo "  name with its own exit status, never replayed. Then refused again"
echo "  for a change that kept its name and its action and moved the value"
echo "  it writes. Then the same two commands over an unmoved world,"
echo "  applying exactly what was reviewed."
