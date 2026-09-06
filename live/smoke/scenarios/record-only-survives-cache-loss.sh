# record-only-survives-cache-loss
# CLAIM 17 - A record-only composite identity survives cache loss without a duplicate create. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/recordonly"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

cat > "$SMOKE_WORK/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-recordonly"
  }
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_iam_group" "app" {
  name = "smoke-recordonly-group"
}

# name deliberately absent: the provider assigns one when the object is
# created, so the value this instance's own identity needs does not exist
# anywhere in this configuration - not as a literal, not as a reference to
# a sibling resource's own argument. aws_iam_group_policy carries no tags
# argument (untaggable) and no list route this fork wires up
# (unlistable), so once the object exists, its identity - the pair
# (group, name) - has exactly one place it can be recovered from: the
# record this apply writes. Losing the record is losing the only copy.
resource "aws_iam_group_policy" "app" {
  group = aws_iam_group.app.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:ListBucket"]
      Resource = "*"
    }]
  })
}
TFEOF

REC="$SMOKE_WORK/.tofu-records/tofu-records/smoke-recordonly/aws_iam_group_policy/$(python3 -c "import base64; print(base64.urlsafe_b64encode(b'aws_iam_group_policy.app').decode().rstrip('='))")"

step "the claim"
explain \
  "A record-only composite identity survives cache loss without a" \
  "duplicate create. aws_iam_group_policy has no tags argument to carry" \
  "a marker and no list route this fork uses to find it again, and when" \
  "its name argument is left for the provider to assign - the ordinary" \
  "way this resource is used - neither half of its two-part identity" \
  "(group, name) is a literal or a reference this run's own" \
  "configuration states. The record this apply writes is the ONLY place" \
  "that identity is ever recorded. Losing the disposable state cache" \
  "must cost nothing, the way it costs nothing for any other resource;" \
  "losing the record itself is a different disaster, and the honest" \
  "answer to it is a duplicate proposed by name, never a silent guess." \
  "" \
  "This is not one of the 27 wire-composite types PR #851 (issue #746)" \
  "measured: none of the three that actually need that fallback path" \
  "(aws_datazone_glossary_term, aws_opensearchserverless_security_config," \
  "aws_redshift_namespace_registration) is implemented by the pinned" \
  "floci image, and the other 24 all resolve through a ratified" \
  "identity-table row whose components are literals or references this" \
  "run's own configuration can fold - which, measured directly against" \
  "this same image, means losing the record changes nothing for them" \
  "either: config alone rebuilds the identity. aws_iam_group_policy" \
  "reaches the identical record-first recovery mechanism (its ratified" \
  "row's policy-name component has nowhere to come from but the record" \
  "when the name is left for AWS to assign), so it proves the same" \
  "claim on a class of resource the emulator actually serves."

step "1. stand the estate up, and read the record - the only carrier"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "recordonly" "init failed"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "recordonly" "apply failed: $AOUT"
[ -f "$REC" ] || fail "recordonly" "no identity record appeared for aws_iam_group_policy.app at the expected key: $REC"
REC_GROUP="$(python3 -c "import json; d=json.load(open('$REC')); print(d['identity']['import_id'].split(':',1)[0])")"
REC_NAME="$(python3 -c "import json; d=json.load(open('$REC')); print(d['identity']['import_id'].split(':',1)[1])")"
[ -n "$REC_GROUP" ] && [ -n "$REC_NAME" ] || fail "recordonly" "the record did not decode into a group and a policy name: $(cat "$REC")"
echo "record for aws_iam_group_policy.app: group=\"$REC_GROUP\" name=\"$REC_NAME\" (AWS-assigned - no tag, no listing, this is the only copy)" | evidence
proof "the estate is up, and the record store - not a tag, not a listing - is what says which live object aws_iam_group_policy.app owns."

step "2. lose the disposable cache - the plan does not move"
explain \
  "Everything stock would call the state - the cache, the lock info," \
  "the whole .terraform directory - is deleted, same as claim 5's own" \
  "disaster. Re-planning must cost nothing: the record survives, so the" \
  "plan reads the same identity back and finds the same live object." \
  "The rendered read is checked by value against the group and name" \
  "the record held in step 1, not by a passing count alone."
cmd "rm -rf .terraform* ; choudoufu init ; choudoufu plan"
rm -rf "$SMOKE_WORK"/.terraform "$SMOKE_WORK"/.terraform.lock.hcl "$SMOKE_WORK"/terraform.tfstate*
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "recordonly" "re-init after the cache wipe failed"
DLOG="$SMOKE_WORKROOT/recordonly-recover.log"
P1="$(cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$DLOG" chdf plan -input=false -no-color 2>&1)" \
  || fail "recordonly" "the post-wipe plan failed: $P1"
grep -q "No changes." <<< "$P1" || fail "recordonly" "losing the cache alone changed the answer: $P1"
READ_LINE="$(grep -oE 'Action=GetGroupPolicy&GroupName=[^&]+&PolicyName=[^&]+' "$DLOG" | tail -1)"
[ -n "$READ_LINE" ] || fail "recordonly" "no GetGroupPolicy read appears in the debug log - nothing verified the identity at all"
READ_GROUP="$(sed -E 's/.*GroupName=([^&]+).*/\1/' <<< "$READ_LINE")"
READ_NAME="$(sed -E 's/.*PolicyName=([^&]+).*/\1/' <<< "$READ_LINE")"
[ "$READ_GROUP" = "$REC_GROUP" ] && [ "$READ_NAME" = "$REC_NAME" ] \
  || fail "recordonly" "the plan read group=\"$READ_GROUP\" name=\"$READ_NAME\", not the recorded group=\"$REC_GROUP\" name=\"$REC_NAME\""
echo "post-wipe plan: No changes. - and it read group=\"$READ_GROUP\" name=\"$READ_NAME\", exactly the record's own values" | evidence
proof "the cache was disposable, exactly as claimed: the plan rebuilt the same identity from the record and verified the same live object."

step "3. lose the record too - the honest answer is a duplicate, by name"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: this time the identity record itself is deleted" \
    "before the cache-loss re-plan, not just the cache. Nothing anywhere -" \
    "no tag, no listing, no record - can say which live object" \
    "aws_iam_group_policy.app owns any more. A run that shrugged and" \
    "planned 'No changes' anyway would be binding an instance no honest" \
    "source vouches for. It must instead propose creating a second copy," \
    "by name, the same announced-duplicate failure mode issue #270" \
    "accepts for this whole class - never a silent bind and never a" \
    "silent orphan."
  cmd "rm -f <the identity record> ; rm -rf .terraform* ; choudoufu init ; choudoufu plan"
  rm -f "$REC"
else
  explain \
    "One more disaster, for contrast rather than the control: cache and" \
    ".terraform lost a second time, record still standing. If the claim" \
    "is honest that the record - never the cache - is what recovery" \
    "rests on, losing the cache twice over changes nothing either time." \
    "BREAK=1 runs this same step with the record itself removed first," \
    "which is the actual control and must propose a create instead."
  cmd "rm -rf .terraform* ; choudoufu init ; choudoufu plan"
fi
rm -rf "$SMOKE_WORK"/.terraform "$SMOKE_WORK"/.terraform.lock.hcl "$SMOKE_WORK"/terraform.tfstate*
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "recordonly" "re-init before the break plan failed"
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "recordonly" "the plan itself failed rather than proposing a create: $P2"
if [ "${BREAK:-0}" = "1" ]; then
  grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$P2" \
    || fail "recordonly" "BREAK: the record is gone but the plan did not propose exactly 1 to add: $P2"
  grep -q 'aws_iam_group_policy.app will be created' <<< "$P2" \
    || fail "recordonly" "BREAK: the proposed create does not name aws_iam_group_policy.app: $P2"
  grep -E 'aws_iam_group_policy.app will be created' <<< "$P2" | head -1 | evidence
  proof "caught - with the record gone, nothing could say the object already existed, so the plan proposed a second one by name. The record was the only carrier; the BREAK control proves it, not just states it."
else
  grep -q "No changes." <<< "$P2" \
    || fail "recordonly" "with the record intact this should still plan clean: $P2"
  proof "record intact, cache lost twice over - still No changes. The claim holds on the path that matters: the record, never the cache."
fi

step "4. teardown"
if [ "${BREAK:-0}" != "1" ]; then
  cmd "choudoufu apply -destroy -auto-approve"
  DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
    || fail "recordonly" "teardown failed: $DOUT"
  grep -qE '[0-9]+ destroyed' <<< "$DOUT" || fail "recordonly" "teardown reported no destroys: $DOUT"
  proof "gone - the group and its inline policy both destroyed."
else
  awsl iam delete-group-policy --group-name "$REC_GROUP" --policy-name "$REC_NAME" >/dev/null 2>&1 || true
  awsl iam delete-group --group-name "$REC_GROUP" >/dev/null 2>&1 || true
  proof "cleaned up by hand: BREAK's own plan proposed a create it never applied, so there is only ever the one live policy to remove."
fi

echo "  What you watched: an untaggable, unlistable, composite-identity"
echo "  resource recover from total cache loss for free because its"
echo "  record survived, and propose a named duplicate - never a silent"
echo "  bind - the one time the record itself was gone too."
