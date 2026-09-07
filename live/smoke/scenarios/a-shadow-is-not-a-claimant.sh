# a-shadow-is-not-a-claimant
# CLAIM 18 - A replaced object's shadow is not a second claimant: the tombstone prunes it, a genuine live duplicate still refuses. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/shadow"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

ESTATE="smoke-shadow"
ADDR="aws_instance.web"

# The emulator's IAM enforcement filter is off by default; step 7 needs it
# on, because the destroy leg it arranges to fail is refused by the
# platform's own policy engine. The harness's "test" key keeps bypassing the
# filter, so every other step runs as the account exactly as before and only
# the one role step 7 creates and assumes is governed.
export FLOCI_IAM_ENFORCEMENT=true

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

write_estate() { # $1 = "a" or "b", the subnet the instance sits in; $2 = "cbd" adds create_before_destroy (step 7)
  local lifecycle=""
  [ "${2:-}" = "cbd" ] && lifecycle=$'\n\n  lifecycle {\n    create_before_destroy = true\n  }'
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
  subnet_id     = aws_subnet.$1.id$lifecycle
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

# live_ids is the same read without the "exactly one" expectation: every
# running instance the marker names, one per line, sorted.
live_ids() {
  awsl ec2 describe-instances \
    --filters "Name=tag:tofu-address,Values=$ADDR" "Name=instance-state-name,Values=pending,running" \
    --query 'Reservations[].Instances[].InstanceId' --output text | tr '\t' '\n' | sed '/^$/d' | sort
}

# as_role runs one command under a role's session credentials, in a subshell
# so nothing leaks back into the account-level steps (claim 13's helper).
as_role() {
  local role="$1" c; shift
  c="$(awsl sts assume-role --role-arn "arn:aws:iam::$ACCT:role/$role" --role-session-name "$role" \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)" || { echo "  could not assume $role" >&2; return 1; }
  ( export AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")" AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")" AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    "$@" )
}
in_work() { cd "$SMOKE_WORK" && chdf "$@"; }
# denied reports whether $1 carries the platform's own refusal. Real EC2
# answers UnauthorizedOperation; the emulator answers 403 with a body the EC2
# SDK cannot parse, which the provider reports as a bare 403 (lex00/floci#189).
denied() { grep -qE 'UnauthorizedOperation|AccessDenied|not authorized to perform|StatusCode: 403' <<< "$1"; }
tombstones_of() { python3 -c 'import json,sys;e=json.load(open(sys.argv[1])).get("tombstone") or {};print(" ".join(sorted(v["identity"]["import_id"] for v in e.values())))' "$1"; }

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
  # The rival leaves and takes its markers with it, so the board is clean
  # again for step 7's own BREAK arm. A terminated instance keeps its tags
  # (step 3), and nothing recorded destroying this one, so the tags are
  # removed by hand - the plain CLI, no choudoufu in the loop.
  cmd "aws ec2 terminate-instances $DUP ; aws ec2 delete-tags $DUP"
  awsl ec2 terminate-instances --instance-ids "$DUP" >/dev/null 2>&1 || fail "shadow" "BREAK: could not terminate the duplicate $DUP"
  awsl ec2 delete-tags --resources "$DUP" --tags Key=tofu-estate Key=tofu-address >/dev/null 2>&1 || fail "shadow" "BREAK: could not remove the duplicate's markers"
  proof "caught - the plan refused with \"Two live resources claiming one address\", naming both $ID2 and $DUP. A tombstone prunes only what an apply is recorded as having destroyed; a live rival is never pruned, so the refusal survives the mechanism that quiets the shadows."
else

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
fi

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

step "7. a failed destroy leg writes no tombstone"
explain \
  "A tombstone says this estate's apply DESTROYED an identity, so the one" \
  "shape it must never be written for is a replace whose destroy leg" \
  "failed. With create_before_destroy the create commits, the old object" \
  "is deposed, and then the platform refuses to terminate it: alive," \
  "running, billed. The refusal below comes from IAM - a role that may do" \
  "everything except ec2:TerminateInstances - and is confirmed with the" \
  "plain CLI before any apply runs. Then the record is read off disk: the" \
  "old object must be named under deposed and must NOT be named under" \
  "tombstone. Step 4 is this step's control - an ordinary replace DOES" \
  "write one - so an empty list here cannot pass by never replacing."
ACCT="$(awsl sts get-caller-identity --query Account --output text)" || fail "shadow" "the emulator did not answer sts get-caller-identity"
TRUST="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCT:root\"},\"Action\":\"sts:AssumeRole\"}]}"
HOLD='{"Version":"2012-10-17","Statement":[{"Sid":"Everything","Effect":"Allow","Action":"*","Resource":"*"},{"Sid":"ButNeverTerminate","Effect":"Deny","Action":"ec2:TerminateInstances","Resource":"*"}]}'
cmd "aws iam create-role no-terminate ; aws iam put-role-policy ... Allow * + Deny ec2:TerminateInstances"
awsl iam create-role --role-name no-terminate --assume-role-policy-document "$TRUST" >/dev/null || fail "shadow" "could not create the no-terminate role"
awsl iam put-role-policy --role-name no-terminate --policy-name hold --policy-document "$HOLD" || fail "shadow" "could not grant the no-terminate role"
# The fence, confirmed with no tofu in the loop: under the role a throwaway
# instance launches and then refuses to terminate. Cleaned up as the account.
cmd "aws ec2 run-instances ; aws ec2 terminate-instances   # as no-terminate"
PROBE="$(as_role no-terminate awsl ec2 run-instances --image-id "$AMI" --instance-type t3.micro --count 1 --query 'Instances[0].InstanceId' --output text 2>&1)" \
  || fail "shadow" "the no-terminate role could not launch an instance, so the role is fenced wrong: $PROBE"
PROBE_T="$(as_role no-terminate awsl ec2 terminate-instances --instance-ids "$PROBE" 2>&1)" && { echo "$PROBE_T"; fail "shadow" "the platform let the no-terminate role terminate $PROBE - IAM enforcement is not on, and nothing below would be measuring a refused destroy"; }
denied "$PROBE_T" || fail "shadow" "the terminate failed for some reason other than a platform denial: $PROBE_T"
awsl ec2 terminate-instances --instance-ids "$PROBE" >/dev/null 2>&1 || true
echo "$PROBE_T" | grep -E 'AccessDenied|UnauthorizedOperation' | head -1 | sed 's/^[[:space:]]*//' | evidence
cmd "edit subnet_id ; lifecycle { create_before_destroy = true } ; choudoufu apply -auto-approve   # as no-terminate"
write_estate b cbd
RC7=0
A7="$(as_role no-terminate in_work apply -auto-approve -input=false -no-color 2>&1)" || RC7=$?
[ "$RC7" != "0" ] || { echo "$A7" | tail -20; fail "shadow" "the apply exited 0 - the destroy leg was not refused, so this step measured an ordinary replace and nothing about a failed one"; }
grep -qE "$ADDR: Creation complete" <<< "$A7" \
  || { echo "$A7" | tail -30; fail "shadow" "the replacement was never created - the apply failed before the shape this step is about existed"; }
denied "$A7" || { echo "$A7" | tail -30; fail "shadow" "the apply failed (exit $RC7) for some reason other than the platform refusing the destroy leg"; }
grep -E "$ADDR: Creation complete" <<< "$A7" | head -1 | evidence
{ sed 's/\x1b\[[0-9;]*m//g' <<< "$A7" | grep -E 'TerminateInstances|terminating EC2 Instance' || true; } | head -1 | sed 's/^[[:space:]]*//' | evidence
LIVE7="$(live_ids)"
ID3="$(grep -vx "$ID2" <<< "$LIVE7" | tr -d '\n')"
[ "$(wc -l <<< "$LIVE7" | tr -d ' ')" = "2" ] && grep -qx "$ID2" <<< "$LIVE7" && [ -n "$ID3" ] \
  || fail "shadow" "expected exactly two running instances marked for $ADDR - the deposed $ID2 and its replacement - but the CLI lists: $(tr '\n' ' ' <<< "$LIVE7")"
cmd "aws ec2 describe-instances --instance-ids $ID2"
STATE7="$(awsl ec2 describe-instances --instance-ids "$ID2" --query 'Reservations[0].Instances[0].State.Name' --output text)"
[ "$STATE7" = "running" ] || fail "shadow" "$ID2 reads state '$STATE7' after the refused destroy, not running - the destroy leg went through after all"
echo "$ID2 is $STATE7 (the deposed object, the destroy the platform refused); $ID3 is its replacement" | evidence
cmd "cat .tofu-records/tofu-records/$ESTATE/aws_instance/<address key>"
cp "$RECORD" "$SMOKE_WORK/record.before-break"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the record is patched by hand to carry the entry the" \
    "write side used to produce before #901 - the deposed, running $ID2" \
    "listed under tombstone as destroyed. The read below must catch it: a" \
    "record naming a live object as destroyed is the exact corruption this" \
    "claim guards against, and an assertion that only ever reads an empty" \
    "list is not load-bearing."
  cmd "python3 -c '...'   # add tombstone[$ID2] to the record file"
  python3 - "$RECORD" "$ID2" <<'PYEOF'
import json, sys, datetime
path, dead = sys.argv[1], sys.argv[2]
rec = json.load(open(path))
tomb = rec.setdefault("tombstone", {})
provider = next((v.get("provider", "") for v in tomb.values()), "")
tomb[dead] = {"identity": {"import_id": dead}, "provider": provider, "time": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")}
json.dump(rec, open(path, "w"), indent=2)
PYEOF
fi
REC7="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["identity"]["import_id"])' "$RECORD")"
DEP7="$(python3 -c 'import json,sys;e=json.load(open(sys.argv[1])).get("deposed") or {};print(" ".join(sorted(v["identity"]["import_id"] for v in e.values() if v.get("identity"))))' "$RECORD")"
TOMBS7="$(tombstones_of "$RECORD")"
[ "$REC7" = "$ID3" ] || fail "shadow" "after the refused destroy the record names $REC7, not the replacement $ID3"
[ "$DEP7" = "$ID2" ] || fail "shadow" "the record's deposed list reads '$DEP7', expected exactly the deposed $ID2 - the write side lost the object the next apply has to destroy"
echo "identity.import_id = $REC7   (the replacement, live)" | evidence
echo "deposed[]          = $DEP7   (the old object, live, the destroy that was refused)" | evidence
echo "tombstone[]        = $TOMBS7" | evidence
if grep -qw "$ID2" <<< "$TOMBS7"; then
  if [ "${BREAK:-0}" = "1" ]; then
    cp "$SMOKE_WORK/record.before-break" "$RECORD"
    proof "caught - the record named the running, deposed $ID2 as destroyed, and the read refused it. That entry is what the write side produced before #901 and what an operator's report would have repeated as fact. The patch is reverted so the teardown below reads the record as the apply wrote it."
  else
    fail "shadow" "the record names the deposed, RUNNING $ID2 as destroyed - a replace whose destroy leg failed wrote a tombstone for a live object (#901)"
  fi
else
  [ "${BREAK:-0}" != "1" ] || fail "shadow" "BREAK: the tombstone patched into the record was not read back - this arm proves nothing"
  for dead in $ID0 $ID1; do
    grep -qw "$dead" <<< "$TOMBS7" || fail "shadow" "the earlier tombstone for $dead is gone - the refused destroy erased the two entries step 4 proved"
  done
  proof "the record names the deposed object under deposed, keeps the two identities step 4 proved under tombstone, and names $ID2 in the tombstone list nowhere: nothing destroyed it, so nothing says it was."
  cmd "choudoufu plan"
  RC7P=0
  P7="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || RC7P=$?
  grep -qF "$ID2" <<< "$P7" \
    || { echo "$P7" | tail -30; fail "shadow" "the next plan (exit $RC7P) does not mention the deposed, running $ID2 at all - a live object this estate created fell out of the plan"; }
  if [ "$RC7P" = "0" ]; then
    grep -qE "$ADDR \(deposed object [0-9a-f]+\) will be destroyed" <<< "$P7" \
      || { echo "$P7" | tail -30; fail "shadow" "the plan exited 0 without proposing the deposed object's destroy - the deposed $ID2 was pruned rather than carried"; }
    grep -E "deposed object .* will be destroyed" <<< "$P7" | head -1 | evidence
    proof "the next plan carries the deposed $ID2 to its destroy rather than pruning it out of the collision - the read half doing its job on an object the record calls deposed, not dead."
  else
    grep -qF 'Two live resources claiming one address' <<< "$P7" \
      || { echo "$P7" | tail -30; fail "shadow" "the next plan failed (exit $RC7P) for some reason other than refusing the collision"; }
    grep -F 'Two live resources claiming one address' <<< "$P7" | head -1 | evidence
    proof "the next plan refuses, naming the deposed $ID2 as a live claimant rather than pruning it - no tombstone licensed dropping it, because nothing destroyed it."
  fi
fi

step "8. teardown"
cmd "choudoufu apply -destroy -auto-approve"
D="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || { echo "$D" | tail -20; fail "shadow" "teardown failed"; }
grep -E 'Apply complete!' <<< "$D" | evidence
proof "the estate is gone."

echo "  What you watched: two replaces at one declared address left two"
echo "  terminated objects still wearing its markers, and the next plan"
echo "  neither refused nor guessed - it dropped exactly the two identities"
echo "  its own record says it destroyed, named them, and bound the address"
echo "  to the third. Then a replace whose destroy leg the platform refused"
echo "  left its old object running and deposed, and the record named it as"
echo "  deposed and nowhere as destroyed. BREAK=1 puts a live rival in the"
echo "  same position and the plan refuses, because nothing recorded"
echo "  destroying that one; and patches the record to call the deposed"
echo "  object destroyed, which the read must catch."
