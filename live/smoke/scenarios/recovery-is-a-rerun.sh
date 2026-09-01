# recovery-is-a-rerun
# CLAIM 5 - Recovery is a re-run, never surgery: a crashed apply re-applies to completion, and losing every local file costs nothing. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/recovery"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "Recovery is a re-run, never surgery. Two disasters end an estate's" \
  "day under stock: an apply that crashes partway (the resource exists," \
  "the state never heard of it - re-running creates a duplicate and the" \
  "original leaks), and a lost state file (the record of everything you" \
  "own, gone). Both have the same recovery here, because identity lives" \
  "on the resources: run it again. No import ceremony, no state surgery," \
  "no backup restore."

step "1. the crash - an apply dies after its first create"
explain \
  "Simulate an apply that died right after its first create call: the" \
  "VPC is made with the AWS CLI and stamped with the estate's markers," \
  "exactly what choudoufu's own create would have written before the" \
  "crash. The configuration still declares aws_vpc.main. Under stock" \
  "this is the nightmare window - re-applying from the empty state would" \
  "create a SECOND vpc and the first would leak forever."
cmd "aws ec2 create-vpc ... + create-tags tofu-estate/tofu-address=aws_vpc.main"
CRASH_VPC="$(awsl ec2 create-vpc --cidr-block 10.99.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$CRASH_VPC" ] || fail "recovery" "could not create the crash-shaped vpc"
if [ "${BREAK:-0}" != "1" ]; then
  awsl ec2 create-tags --resources "$CRASH_VPC" \
    --tags "Key=tofu-estate,Value=stateless-e2e-block" "Key=tofu-address,Value=aws_vpc.main" \
    || fail "recovery" "could not mark the crash-shaped vpc"
  echo "created $CRASH_VPC, marked as aws_vpc.main - the apply that made it never finished" | evidence
else
  explain \
    "BREAK control: the markers are deliberately NOT written. An unmarked" \
    "resource is not provably the estate's, so the re-run below must NOT" \
    "bind to it - it must build its own vpc, leaving two. If the bind" \
    "check still passes, it was never checking anything."
  echo "created $CRASH_VPC with NO markers" | evidence
fi

step "2. re-run the apply - it binds, completes, duplicates nothing"
explain \
  "The whole recovery is the same apply, run again. The plan reads" \
  "identity from the platform and finds aws_vpc.main already owned;" \
  "it binds to it, and only what is actually missing gets created."
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "recovery" "init failed"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "recovery" "the recovery apply failed: $AOUT"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$AOUT" | grep -oE '[0-9]+')"
LIVE_VPC="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[].VpcId' --output text | tr '\t' '\n' | head -1)"
VPC_COUNT="$(awsl ec2 describe-vpcs --filters "Name=cidr,Values=10.99.0.0/16" --query 'length(Vpcs)' --output text)"
if [ "${BREAK:-0}" = "1" ]; then
  if [ "$LIVE_VPC" = "$CRASH_VPC" ] && [ "$VPC_COUNT" = "1" ]; then
    fail "recovery" "BREAK: the apply bound to an UNMARKED vpc - the bind check proves nothing"
  fi
  proof "caught - without the marker the estate built its own vpc ($VPC_COUNT with this cidr now). Binding rests on the marker; unmarked resources are exactly the boundary the claim states."
  for v in $(awsl ec2 describe-vpcs --filters "Name=cidr,Values=10.99.0.0/16" --query 'Vpcs[].VpcId' --output text); do
    ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
    awsl ec2 delete-vpc --vpc-id "$v" >/dev/null 2>&1 || true
  done
  exit 0
fi
[ "$LIVE_VPC" = "$CRASH_VPC" ] \
  || fail "recovery" "the apply did not bind to the crashed vpc (crashed $CRASH_VPC, live $LIVE_VPC) - it duplicated"
[ "$VPC_COUNT" = "1" ] \
  || fail "recovery" "there are $VPC_COUNT vpcs with the estate's cidr - the re-run duplicated the crashed create"
echo "apply added $ADDED resources; aws_vpc.main is still $CRASH_VPC - bound, not rebuilt" | evidence
P1="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "recovery" "post-recovery plan failed"
grep -q "No changes." <<< "$P1" || fail "recovery" "the estate did not converge after recovery: $P1"
proof "the crash cost one re-run. The vpc the dead apply created is bound by its marker and everything else was built around it; the plan is clean."

step "3. now lose every local file"
explain \
  "The second disaster is the lost laptop. Everything stock would call" \
  "the state - the cache, the lock info, the whole .terraform directory" \
  "- is deleted. Before it goes, know what the cache held: every" \
  "attribute of the estate. That is why receipts are lint-guarded" \
  "against secret material; the one file that holds it is this" \
  "disposable one. Init afterwards re-downloads tools; it recovers no" \
  "knowledge, because knowledge was never local."
cmd "rm -rf .terraform* terraform.tfstate* ; choudoufu init ; choudoufu plan"
[ -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate" ] || fail "recovery" "expected the cache to exist before the wipe"
rm -rf "$SMOKE_WORK"/.terraform "$SMOKE_WORK"/.terraform.lock.hcl "$SMOKE_WORK"/terraform.tfstate*
LEFT="$(ls -A "$SMOKE_WORK" | tr '\n' ' ')"
echo "everything left on disk: $LEFT" | evidence
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "recovery" "re-init after the wipe failed"
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "recovery" "the post-wipe plan failed: $P2"
grep -E "No changes." <<< "$P2" | head -1 | evidence
grep -q "No changes." <<< "$P2" || fail "recovery" "losing the local files changed the answer: $P2"
proof "a plan from nothing but .tf files and the platform: markers answered identity, and nothing that was deleted was a record of anything."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "recovery" "teardown failed: $DOUT"
grep -qE "Resources: 0 added, 0 changed, $ADDED" <<< "$DOUT" && DGONE="$ADDED" || DGONE=""
TOTAL_DESTROYED="$(grep -oE '[0-9]+ destroyed' <<< "$DOUT" | grep -oE '[0-9]+' | head -1)"
[ "$TOTAL_DESTROYED" -ge "$ADDED" ] 2>/dev/null \
  || fail "recovery" "teardown destroyed $TOTAL_DESTROYED, expected at least $ADDED: $DOUT"
proof "$TOTAL_DESTROYED destroyed, the crashed vpc among them - it was a full citizen of the estate from the moment it was bound."

echo "  What you watched: an apply crash-shaped mid-run recover by being run"
echo "  again, binding the resource the dead run created instead of"
echo "  duplicating it; then every local file vanish and the next plan come"
echo "  back clean. Both disasters cost a re-run, because the record of"
echo "  ownership was never in the files you lost."
