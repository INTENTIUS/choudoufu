# a-new-estate-writes-its-first-record
# CLAIM 34 - Under the published IAM policy a new estate's first write into an empty prefix succeeds, and so does every write after it (REAL AWS, maintainer-run). ~4 min.

SMOKE_WORK="$SMOKE_WORKROOT/firstwrite"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"

step "the claim"
explain \
  "An estate's role may write only objects tagged as that estate's. The" \
  "obvious way to say so is a condition on s3:ExistingObjectTag, and it is" \
  "wrong: that key reads the tags an object ALREADY has, and an object" \
  "that does not exist yet has none. A policy written that way reviews" \
  "correctly and denies the first write into every new estate. The" \
  "condition on a create is s3:RequestObjectTag. This runs a brand-new" \
  "estate's whole life under the policy the documentation publishes, and" \
  "its BREAK arm is the policy the documentation must never publish."

step "0. real AWS, and a control: a role that is allowed nothing is denied"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "firstwrite" "this scenario runs against real AWS: it creates an S3 bucket and IAM roles in the account your credentials name, and removes them. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
command -v jq >/dev/null 2>&1 || fail "firstwrite" "jq is not installed; the policy renderer needs it"
real_aws_begin firstwrite
BUCKET="chdf-smoke-firstwrite-$SUFFIX"
bucket_up "$BUCKET" || fail "firstwrite" "could not create the bucket"
echo probe > "$SMOKE_WORK/probe"
awsl s3api put-object --bucket "$BUCKET" --key control/probe --body "$SMOKE_WORK/probe" >/dev/null
role_with_policy smoke-no-policy '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sts:GetCallerIdentity","Resource":"*"}]}' "$BUCKET" \
  || fail "firstwrite" "could not create the control role"
cmd "aws s3api get-object ...   # as a role that is allowed nothing in S3"
C_OUT="$(as_role smoke-no-policy awsl s3api get-object --bucket "$BUCKET" --key control/probe "$SMOKE_WORK/out" 2>&1)" \
  && fail "firstwrite" "a role with no S3 permission read an object, so every 'allowed' below would mean nothing."
denied "$C_OUT" || fail "firstwrite" "the control read failed for some reason other than a denial: $C_OUT"
grep -oE 'AccessDenied[^"]*' <<< "$C_OUT" | head -1 | mask | evidence
proof "a role with nothing is denied, so an 'allowed' from here on is the policy's doing."

# The policy under test. BREAK=1 rewrites the one statement the claim is about.
POLICY="$("$POLICY_RENDERER" smoke-new "$BUCKET")" || fail "firstwrite" "render-policy.sh failed"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the policy that reviews correctly and bricks a new estate"
  explain \
    "The published policy, with one key changed: the write is conditioned" \
    "on s3:ExistingObjectTag instead of s3:RequestObjectTag. Side by side" \
    "the two read the same."
  POLICY="$(jq '(.Statement[] | select(.Sid == "WriteOnlyObjectsTaggedAsThisEstate") | .Condition) = {"StringEquals": {"s3:ExistingObjectTag/tofu-estate": "smoke-new"}}' <<< "$POLICY")"
  grep -q '"s3:RequestObjectTag/tofu-estate"' <<< "$POLICY" \
    && fail "firstwrite" "the break did not rewrite the write statement, so this arm would test the published policy"
fi

step "1. a brand-new estate, an empty prefix, and the published policy"
role_with_policy smoke-new-estate "$POLICY" "$BUCKET" || fail "firstwrite" "could not create the estate's role"
jq -c '.Statement[] | select(.Sid == "WriteOnlyObjectsTaggedAsThisEstate") | {Action, Condition}' <<< "$POLICY" | evidence
write_bucket_estate "$SMOKE_WORK/est" smoke-new "$BUCKET" v1
( cd "$SMOKE_WORK/est" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "firstwrite" "init failed"
EMPTY="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-new/ --query 'length(Contents || `[]`)' --output text)"
[ "$EMPTY" = "0" ] || fail "firstwrite" "the prefix is not empty ($EMPTY objects), so this is not a first write"
cmd "choudoufu apply -auto-approve   # as the estate's role, into a prefix holding nothing"
A_OUT="$(cd "$SMOKE_WORK/est" && as_role smoke-new-estate chdf apply -auto-approve -input=false -no-color 2>&1)" && A_RC=0 || A_RC=$?

if [ "${BREAK:-0}" = "1" ]; then
  [ "$A_RC" != "0" ] || fail "firstwrite" "the first write SUCCEEDED under a policy that conditions the create on the tags of an object that does not exist. Either the emulator is not evaluating s3:ExistingObjectTag the way AWS does, or this arm proves nothing: $A_OUT"
  denied "$(flat <<< "$A_OUT")" || fail "firstwrite" "the first write failed, but not on a denial: $A_OUT"
  LEFT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-new/ --query 'length(Contents || `[]`)' --output text)"
  [ "$LEFT" = "0" ] || fail "firstwrite" "the denied estate still wrote $LEFT object(s)"
  flat <<< "$A_OUT" | grep -oE 'AccessDenied[^.]*' | head -1 | mask | evidence
  proof "caught - the estate could not write its first object. Nothing about that policy looks wrong until a new estate meets it."
  exit 0
fi

[ "$A_RC" = "0" ] || fail "firstwrite" "a brand-new estate could not apply under the published policy: $A_OUT"
grep -q "Resources: 2 added, 0 changed, 0 destroyed" <<< "$A_OUT" || fail "firstwrite" "the first apply: $A_OUT"
for key in $(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'Contents[].Key' --output text); do
  TAG="$(awsl s3api get-object-tagging --bucket "$BUCKET" --key "$key" --query 'TagSet[?Key==`tofu-estate`].Value | [0]' --output text)"
  [ "$TAG" = "smoke-new" ] || fail "firstwrite" "$key is tagged tofu-estate=$TAG"
done
awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'Contents[].Key' --output text | tr '\t' '\n' | sed 's/$/   tofu-estate=smoke-new/' | evidence
proof "every object of the estate's first apply was created under the policy, and carries the tag the policy demanded."

step "2. and every write after the first"
explain \
  "A policy can let a create through and still break the estate: every" \
  "update and delete choudoufu makes is conditional (If-Match), and AWS" \
  "authorizes a conditional write as a read too. So the rest of the" \
  "estate's life runs under the same role, counts checked."
write_bucket_estate "$SMOKE_WORK/est" smoke-new "$BUCKET" v2
cmd "choudoufu apply -auto-approve   # an update, under If-Match"
U_OUT="$(cd "$SMOKE_WORK/est" && as_role smoke-new-estate chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "firstwrite" "the update failed under the published policy: $U_OUT"
grep -q "Resources: 0 added, 2 changed, 0 destroyed" <<< "$U_OUT" || fail "firstwrite" "the update: $U_OUT"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # from the records alone"
P_OUT="$(cd "$SMOKE_WORK/est" && as_role smoke-new-estate chdf plan -input=false -no-color 2>&1)" || fail "firstwrite" "the replan failed: $P_OUT"
grep -q "No changes." <<< "$P_OUT" || fail "firstwrite" "the replan was not empty: $P_OUT"
cmd "choudoufu apply -destroy -auto-approve   # deletes, under If-Match"
D_OUT="$(cd "$SMOKE_WORK/est" && as_role smoke-new-estate chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "firstwrite" "the destroy failed under the published policy: $D_OUT"
grep -q "Resources: 0 added, 0 changed, 2 destroyed" <<< "$D_OUT" || fail "firstwrite" "the destroy did not remove both instances: $D_OUT"
LEFT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-new/terraform_data/ --query 'length(Contents || `[]`)' --output text)"
[ "$LEFT" = "0" ] || fail "firstwrite" "$LEFT record(s) left after the destroy"
echo "2 added; 2 changed under If-Match; replan empty; 2 destroyed under If-Match; no record left" | evidence
proof "create, update, read and delete, all as the role, all under the policy as published."

echo "  What you watched: enforcement proven on, a new estate's first objects"
echo "  written into an empty prefix under the published policy, and the"
echo "  conditional updates and deletes that a tighter-looking policy denies."
