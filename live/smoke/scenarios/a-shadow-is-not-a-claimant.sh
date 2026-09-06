# a-shadow-is-not-a-claimant
# CLAIM 18 - A replaced object's shadow is not a second claimant: the tombstone prunes it, a genuine live duplicate still refuses. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/shadow"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

ESTATE="smoke-shadow"
ADDR="aws_instance.web"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# The record store is the implied local one, a directory beside the module.
# Its key for one address is the address escaped into base64url with the
# padding stripped - the same spelling live/e2e's own crossing scripts read.
record_key() { printf '%s' "$1" | base64 | tr '+/' '-_' | tr -d '=\n'; }
RECORD="$SMOKE_WORK/.tofu-records/tofu-records/$ESTATE/aws_instance/$(record_key "$ADDR")"

AMI="$(awsl ec2 describe-images --query 'Images[0].ImageId' --output text)"
[ -n "$AMI" ] && [ "$AMI" != "None" ] || fail "shadow" "the emulator offers no AMI to launch an instance from"

write_estate() { # $1 = "a" or "b", the subnet the instance sits in
  cat > "$SMOKE_WORK/main.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "$ESTATE"
  }

  # internal/live/pins.AWSProviderVersion - the release the admission
  # evidence was measured against, so this scenario shows a reader the
  # diagnostics its own claim is about and not the provider-drift warning.
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_subnet" "a" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.42.1.0/24"
}

resource "aws_subnet" "b" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.42.2.0/24"
}

# subnet_id is ForceNew, so moving the instance between the two subnets is
# an ordinary configuration edit that schedules a replace at this same
# declared address - the shape this whole claim is about.
resource "aws_instance" "web" {
  ami           = "$AMI"
  instance_type = "t3.micro"
  subnet_id     = aws_subnet.$1.id
}
TFEOF
}

# live_id reads the id of the one instance the estate's marker names, from
# the AWS CLI, restricted to instances that are actually running.
live_id() {
  awsl ec2 describe-instances \
    --filters "Name=tag:tofu-address,Values=$ADDR" "Name=instance-state-name,Values=pending,running" \
    --query 'Reservations[].Instances[].InstanceId' --output text | tr -d '\n'
}

step "the claim"
explain \
  "A replaced object's shadow is not a second claimant. When an apply" \
  "replaces a resource, AWS keeps the destroyed object's tags readable" \
  "for a while - a terminated instance still answers describe-instances," \
  "still lists in the tagging API, still wears this estate's markers. The" \
  "next plan therefore finds TWO objects claiming one address, and it" \
  "cannot tell a corpse from a rival by tags alone. So the apply that" \
  "destroyed the object writes it down: the record keeps a tombstone for" \
  "the identity it destroyed, and only a claimant matching one is dropped." \
  "A tombstone is evidence, never permission - it can turn a refusal into" \
  "a warning and can do nothing else to the live system."

step "1. stand the estate up"
cmd "choudoufu init && choudoufu apply -auto-approve"
write_estate a
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "shadow" "init failed"
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "shadow" "apply failed: $A1"
grep -E 'Apply complete!' <<< "$A1" | evidence
ID0="$(live_id)"
[ -n "$ID0" ] || fail "shadow" "no live instance carries $ADDR after the first apply"
[ -f "$RECORD" ] || fail "shadow" "no record file at $RECORD after the first apply"
REC0="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["identity"]["import_id"])' "$RECORD")"
[ "$REC0" = "$ID0" ] || fail "shadow" "the record names $REC0, the live instance is $ID0"
echo "live instance $ID0; the record at $ADDR names import_id=$REC0" | evidence
proof "one live object, one record naming it by value."

step "2. force a replace at the same declared address"
explain \
  "subnet_id is ForceNew, so moving the instance to the other subnet is" \
  "an ordinary edit that schedules a destroy and a create at the SAME" \
  "declared address. This is the everyday shape - a changed AMI, a" \
  "changed name, a changed subnet - not a corner case."
cmd "edit subnet_id ; choudoufu apply -auto-approve"
write_estate b
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "shadow" "the replace plan failed: $P2"
grep -qE "# $ADDR must be replaced" <<< "$P2" \
  || { echo "$P2" | grep -E '^  # '; fail "shadow" "changing subnet_id did not schedule a replace at $ADDR"; }
grep -E "# $ADDR must be replaced" <<< "$P2" | head -1 | evidence
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "shadow" "the replace apply failed: $A2"
ID1="$(live_id)"
[ -n "$ID1" ] && [ "$ID1" != "$ID0" ] || fail "shadow" "after the replace the live instance is '$ID1', expected a new id different from $ID0"
proof "$ID0 destroyed, $ID1 created, one declared address throughout."

step "3. the destroyed object's tags are still readable"
explain \
  "This is the whole problem, and it is AWS's own documented behaviour" \
  "rather than an emulator quirk: a terminated instance keeps answering" \
  "for a time. Below, the destroyed object is read with the plain AWS CLI" \
  "- no choudoufu in the loop - and it still wears this estate's markers."
cmd "aws ec2 describe-instances --instance-ids $ID0"
SHADOW_STATE="$(awsl ec2 describe-instances --instance-ids "$ID0" --query 'Reservations[0].Instances[0].State.Name' --output text)"
[ "$SHADOW_STATE" = "terminated" ] || fail "shadow" "$ID0 reads state '$SHADOW_STATE' after the replace, not terminated"
SHADOW_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$ID0" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text)"
[ "$SHADOW_ADDR_TAG" = "$ADDR" ] \
  || fail "shadow" "$ID0's tofu-address tag reads '$SHADOW_ADDR_TAG' after the replace - there is no shadow to prune and this scenario proves nothing"
echo "$ID0 is $SHADOW_STATE and still tagged tofu-address=$SHADOW_ADDR_TAG, tofu-estate=$ESTATE" | evidence
proof "two objects now answer to one address by tag: the live $ID1 and the dead $ID0."

step "4. the record says which one it destroyed"
explain \
  "The apply that destroyed $ID0 recorded it, in the same envelope that" \
  "names the live object. Tombstones accumulate per address rather than" \
  "flipping a flag, so a second replace below adds a second entry - the" \
  "list is capped at 8 destroyed identities per address, oldest evicted" \
  "first, because only the most recent replaces can still have a shadow" \
  "in the air."
cmd "cat .tofu-records/tofu-records/$ESTATE/aws_instance/<address key>"
REC1="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["identity"]["import_id"])' "$RECORD")"
[ "$REC1" = "$ID1" ] || fail "shadow" "after the replace the record names $REC1, not the live $ID1"
TOMBS1="$(python3 -c 'import json,sys;e=json.load(open(sys.argv[1])).get("tombstone") or {};print(" ".join(sorted(v["identity"]["import_id"] for v in e.values())))' "$RECORD")"
[ "$TOMBS1" = "$ID0" ] \
  || fail "shadow" "the record's tombstone list reads '$TOMBS1' after one replace, expected exactly the destroyed $ID0 - if it is empty, see GitHub issue #908: the plan's replace set is read after Core.Apply has drained it, so no replace records a tombstone and every step below this one is measuring a mechanism that never ran"
cmd "edit subnet_id back ; choudoufu apply -auto-approve   # a second replace"
write_estate a
A3="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "shadow" "the second replace apply failed: $A3"
ID2="$(live_id)"
[ -n "$ID2" ] && [ "$ID2" != "$ID1" ] && [ "$ID2" != "$ID0" ] \
  || fail "shadow" "after the second replace the live instance is '$ID2', expected a third distinct id"
REC2="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["identity"]["import_id"])' "$RECORD")"
[ "$REC2" = "$ID2" ] || fail "shadow" "after the second replace the record names $REC2, not the live $ID2"
TOMBS2="$(python3 -c 'import json,sys;e=json.load(open(sys.argv[1])).get("tombstone") or {};print(" ".join(sorted(v["identity"]["import_id"] for v in e.values())))' "$RECORD")"
EXPECT2="$(printf '%s\n%s\n' "$ID0" "$ID1" | sort | tr '\n' ' ' | sed 's/ $//')"
[ "$TOMBS2" = "$EXPECT2" ] \
  || fail "shadow" "the tombstone list reads '$TOMBS2' after two replaces, expected both destroyed identities '$EXPECT2'"
echo "identity.import_id = $REC2   (the live object)" | evidence
echo "tombstone[]        = $TOMBS2   (both destroyed objects, oldest evicted past 8)" | evidence
proof "the record names one live object and a LIST of destroyed ones, each by value."

if [ "${BREAK:-0}" = "1" ]; then
  step "5. BREAK control - a genuine live duplicate, tombstoned by nothing"
  explain \
    "BREAK control: a second, genuinely RUNNING instance is created out of" \
    "band wearing this estate's tag and this address's tag - the shape a" \
    "half-finished create_before_destroy, or a hand-rolled copy, leaves" \
    "behind. Nothing destroyed it, so no tombstone names it. The plan must" \
    "REFUSE, naming both live objects, and must not quietly prune it the" \
    "way it prunes the two shadows above. Before the tombstone existed" \
    "this case warned and exited 0, which is exactly why this arm is the" \
    "one that makes the claim load-bearing."
  SUBNET="$(awsl ec2 describe-instances --instance-ids "$ID2" --query 'Reservations[0].Instances[0].SubnetId' --output text)"
  [ -n "$SUBNET" ] && [ "$SUBNET" != "None" ] || fail "shadow" "BREAK: could not read $ID2's own subnet to launch the duplicate beside it"
  cmd "aws ec2 run-instances ... --tag-specifications tofu-estate=$ESTATE,tofu-address=$ADDR"
  DUP="$(awsl ec2 run-instances --image-id "$AMI" --instance-type t3.micro --subnet-id "$SUBNET" --count 1 \
    --tag-specifications "ResourceType=instance,Tags=[{Key=tofu-estate,Value=$ESTATE},{Key=tofu-address,Value=$ADDR}]" \
    --query 'Instances[0].InstanceId' --output text)"
  [ -n "$DUP" ] && [ "$DUP" != "None" ] || fail "shadow" "BREAK: could not create the duplicate instance"
  echo "created $DUP - running, marked for $ADDR, and named by no tombstone" | evidence
  BRC=0
  BP="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || BRC=$?
  if [ "$BRC" = "0" ]; then
    echo "$BP" | tail -20
    fail "shadow" "BREAK: the plan exited 0 with two genuinely live instances claiming $ADDR - the tombstone pruned a live object, which is the failure this claim exists to exclude"
  fi
  grep -qF 'Two live resources claiming one address' <<< "$BP" \
    || { echo "$BP" | tail -20; fail "shadow" "BREAK: the plan failed for some reason other than the collision refusal - this arm is not load-bearing"; }
  grep -qF "$ID2" <<< "$BP" \
    || { echo "$BP" | tail -20; fail "shadow" "BREAK: the collision refusal does not name the surviving live instance $ID2"; }
  grep -qF "$DUP" <<< "$BP" \
    || { echo "$BP" | tail -20; fail "shadow" "BREAK: the collision refusal does not name the manufactured duplicate $DUP"; }
  grep -F 'Two live resources claiming one address' <<< "$BP" | head -1 | evidence
  awsl ec2 terminate-instances --instance-ids "$DUP" >/dev/null 2>&1 || true
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  proof "caught - the plan refused with \"Two live resources claiming one address\", naming both $ID2 and $DUP. A tombstone prunes only what an apply is recorded as having destroyed; a live rival is never pruned, so the refusal survives the mechanism that quiets the shadows."
  exit 0
fi

step "5. the shadow arm - the plan prunes the dead and binds the living"
explain \
  "Now plan while both shadows are still listed. Every one of the three" \
  "objects wearing $ADDR is a claimant; the record settles it. The two" \
  "the record names as destroyed are dropped and REPORTED - a warning" \
  "naming each by value, proposing nothing - and the address binds to the" \
  "object the record names. Exit 0, and no resource action, because the" \
  "estate is converged."
cmd "choudoufu plan"
RC5=0
P5="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || RC5=$?
[ "$RC5" = "0" ] || { echo "$P5" | tail -30; fail "shadow" "the shadow-arm plan exited $RC5; a tombstoned shadow must not block the estate"; }
grep -qF 'Live resource displaced from the address it is marked for' <<< "$P5" \
  || { echo "$P5" | tail -30; fail "shadow" "the plan does not report the superseded shadows at all - a dropped claimant must be announced, never silently discarded"; }
for dead in $ID0 $ID1; do
  grep -qF "$dead" <<< "$P5" \
    || { echo "$P5" | tail -30; fail "shadow" "the displaced-marker report does not name the destroyed $dead by value"; }
done
# Diagnostic prose is hard-wrapped to the terminal width, so a phrase out
# of the middle of it is asserted against a whitespace-flattened copy. On
# the raw text the assertion would pass or fail on where the wrap fell.
P5FLAT="$(tr -s '[:space:]' ' ' <<< "$P5")"
grep -qF 'records this one as destroyed by an earlier apply of this estate' <<< "$P5FLAT" \
  || { echo "$P5" | tail -30; fail "shadow" "the report does not say the record is what licensed dropping the claimant"; }
# The report's own boundary sentence, as #900 rewrote it: only a destroy
# this estate APPLIED records an object this way, so the shapes that
# re-point an address without destroying anything are refused instead.
grep -qF 'is refused as a live collision rather than described here as destroyed' <<< "$P5FLAT" \
  || { echo "$P5" | tail -30; fail "shadow" "the report does not state its own boundary - that an object nothing destroyed is refused as a live collision, not described here"; }
grep -qF "$ID2" <<< "$P5" \
  || { echo "$P5" | tail -30; fail "shadow" "the report does not name $ID2 as the object the address owns right now"; }
if grep -qE '^  # .+ (will be (created|updated|destroyed)|must be replaced)' <<< "$P5"; then
  grep -E '^  # .+ (will be|must be)' <<< "$P5"
  fail "shadow" "the plan proposes a resource action - the address did not bind to the live object the record names"
fi
grep -F 'Live resource displaced from the address it is marked for' <<< "$P5" | head -2 | evidence
grep -E "No changes|Your infrastructure matches" <<< "$P5" | head -1 | evidence
proof "exit 0, both dead identities named by value, nothing proposed for either, and $ADDR bound to $ID2 - the object the record names."

step "6. the honest boundary"
explain \
  "The tombstone is read at exactly one place and can cause exactly one" \
  "thing: a claimant leaving a collision set. It authorises no destroy, no" \
  "create, no adoption and no retag, so an entry that is wrong costs a" \
  "refusal this estate would otherwise have made and can reach the live" \
  "system through nothing. That is the whole safety argument, and the" \
  "BREAK arm above is its test: run BREAK=1 and a genuinely live duplicate" \
  "with no tombstone must still refuse."
proof "a tombstone is evidence that an object is dead, never permission to touch one that is not."

step "7. teardown"
cmd "choudoufu apply -destroy -auto-approve"
D="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || { echo "$D" | tail -20; fail "shadow" "teardown failed"; }
grep -E 'Apply complete!' <<< "$D" | evidence
proof "the estate is gone."

echo "  What you watched: two replaces at one declared address left two"
echo "  terminated objects still wearing its markers, and the next plan"
echo "  neither refused nor guessed - it dropped exactly the two identities"
echo "  its own record says it destroyed, named them, and bound the address"
echo "  to the third. BREAK=1 puts a live rival in the same position and the"
echo "  plan refuses, because nothing recorded destroying that one."
