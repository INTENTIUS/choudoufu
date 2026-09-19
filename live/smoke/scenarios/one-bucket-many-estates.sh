# one-bucket-many-estates
# CLAIM 35 - One bucket, many estates: a role scoped to one estate cannot read another's records, and a wrong prefix alone does not change that (REAL AWS, maintainer-run). ~6 min.

SMOKE_WORK="$SMOKE_WORKROOT/manyestates"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"

# s3_as <role> <s3api args...>: one S3 call as a role, output and verdict.
s3_as() { local role="$1"; shift; as_role "$role" awsl s3api "$@" 2>&1; }
# must_deny / must_allow <label> <role> <s3api args...>
must_deny() {
  local label="$1" out; shift
  out="$(s3_as "$@")" && fail "manyestates" "[$label] was ALLOWED: $out"
  denied "$out" || fail "manyestates" "[$label] failed, but not on a denial: $out"
  echo "$label: denied" | evidence
}
must_allow() {
  local label="$1" out; shift
  out="$(s3_as "$@")" || fail "manyestates" "[$label] was refused: $out"
  echo "$label: allowed" | evidence
}

step "the claim"
explain \
  "Every estate's records live in one bucket, and what separates them is" \
  "IAM. There are two defences for reading another estate's objects, and" \
  "they fail to different things. The prefix scope is defeated by a" \
  "prefix written wrong. The object tag is not: a neighbour's object" \
  "carries the neighbour's tag however the prefix was written. So" \
  "reaching another estate's records takes BOTH mistakes. For a list, a" \
  "write or a delete there is only the prefix, because S3 has no" \
  "condition on the tags of an object being overwritten or deleted, and" \
  "this scenario shows that too rather than leaving it out."

step "1. two estates, two roles, one bucket"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "manyestates" "this scenario runs against real AWS: it creates an S3 bucket and IAM roles in the account your credentials name, and removes them. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
command -v jq >/dev/null 2>&1 || fail "manyestates" "jq is not installed; the policy renderer needs it"
real_aws_begin manyestates
BUCKET="chdf-smoke-estates-$SUFFIX"
bucket_up "$BUCKET" || fail "manyestates" "could not create the bucket"
for e in a b; do
  role_with_policy "smoke-estate-$e" "$("$POLICY_RENDERER" "smoke-$e" "$BUCKET")" "$BUCKET" || fail "manyestates" "could not create estate $e's role"
  write_bucket_estate "$SMOKE_WORK/$e" "smoke-$e" "$BUCKET" v1
  ( cd "$SMOKE_WORK/$e" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "manyestates" "init failed in $e"
  OUT="$(cd "$SMOKE_WORK/$e" && as_role "smoke-estate-$e" chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "estate $e could not apply under its own role: $OUT"
  grep -q "Resources: 2 added" <<< "$OUT" || fail "manyestates" "estate $e: $OUT"
done
B_RECORD="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-b/terraform_data/ --query 'Contents[0].Key' --output text)"
B_OUTPUT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-outputs/smoke-b/ --query 'Contents[0].Key' --output text)"
A_RECORD="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-a/terraform_data/ --query 'Contents[0].Key' --output text)"
[ -n "$B_RECORD" ] && [ "$B_RECORD" != "None" ] && [ "$B_OUTPUT" != "None" ] || fail "manyestates" "estate b wrote no record or no output to stay out of"
awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'Contents[].Key' --output text | tr '\t' '\n' | evidence
proof "both estates applied under their own roles, into one bucket."

step "2. estate a's role, at estate b's door"
explain \
  "With the AWS CLI and no choudoufu in the loop. The control comes" \
  "first: the role CAN read its own record, so a denial below is about" \
  "whose object it is, not about a role that can read nothing."
must_allow "a reads its own record" smoke-estate-a get-object --bucket "$BUCKET" --key "$A_RECORD" "$SMOKE_WORK/o"
must_deny "a reads b's record" smoke-estate-a get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
must_deny "a reads b's outputs" smoke-estate-a get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
must_deny "a lists b's records" smoke-estate-a list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-b/
must_deny "a lists the bare prefix tofu-records/smoke-a" smoke-estate-a list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-a
echo x > "$SMOKE_WORK/x"
must_deny "a writes under b's prefix, tagged as a" smoke-estate-a put-object --bucket "$BUCKET" --key tofu-records/smoke-b/intruder --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-a
must_deny "a writes under its OWN prefix, tagged as b" smoke-estate-a put-object --bucket "$BUCKET" --key tofu-records/smoke-a/mislabelled --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-b
proof "out of reach by prefix, and the bare prefix - the one that would also name a neighbour called smoke-a-eu - is refused outright."

# From here the prefix scope is deliberately WRONG: estate a's allows reach
# every estate's objects. What is left is the tag.
MISSCOPED="$("$POLICY_RENDERER" smoke-a "$BUCKET" | jq --arg b "arn:aws:s3:::$BUCKET" '
  (.Statement[] | select(.Sid == "ReadAndDeleteByPrefix" or .Sid == "WriteOnlyObjectsTaggedAsThisEstate") | .Resource) = [$b + "/tofu-*"]
  | (.Statement[] | select(.Sid == "ListOwnNamespaces") | .Condition.StringLike["s3:prefix"]) = ["tofu-*"]')"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - both defects at once must let the read through"
  explain \
    "The prefix is mis-scoped AND the tag's Deny is removed. If the read" \
    "of estate b's record is still denied, something other than the two" \
    "defences this claim names is doing the denying, and the claim would" \
    "be describing a mechanism it never measured."
  BOTH="$(jq 'del(.Statement[] | select(.Sid == "DenyReadingAnotherEstatesObjects"))' <<< "$MISSCOPED")"
  grep -q DenyReadingAnotherEstatesObjects <<< "$BOTH" && fail "manyestates" "the break did not remove the Deny"
  role_with_policy smoke-estate-a "$BOTH" "$BUCKET" || fail "manyestates" "could not install the doubly broken policy"
  OUT="$(s3_as smoke-estate-a get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/stolen")" \
    || fail "manyestates" "with the prefix mis-scoped AND the tag's Deny removed, the read of estate b's record was still refused: $OUT"
  [ -s "$SMOKE_WORK/stolen" ] || fail "manyestates" "the read was allowed and returned nothing"
  echo "a reads b's record: ALLOWED, $(wc -c < "$SMOKE_WORK/stolen" | tr -d ' ') bytes of another estate's record" | evidence
  proof "caught - with both defences down the record is readable, so in step 3 it was the tag that refused it and nothing else."
  exit 0
fi

step "3. the prefix written wrong, and the tag still holding"
role_with_policy smoke-estate-a "$MISSCOPED" "$BUCKET" || fail "manyestates" "could not install the mis-scoped policy"
jq -c '.Statement[] | select(.Sid == "ReadAndDeleteByPrefix") | .Resource' <<< "$MISSCOPED" | evidence
must_allow "a reads its own record" smoke-estate-a get-object --bucket "$BUCKET" --key "$A_RECORD" "$SMOKE_WORK/o"
must_deny "a reads b's record, prefix mis-scoped" smoke-estate-a get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
proof "one mistake is not enough to read a neighbour's records: the object carries its own estate's tag."

step "4. what the tag cannot defend, shown and not left out"
explain \
  "Under the same mis-scoped policy. S3 gives a write or a delete no" \
  "condition key for the tags of the object it replaces, so nothing but" \
  "the prefix stands here. A throwaway object tagged as estate b's" \
  "stands in for one of b's records."
awsl s3api put-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-b >/dev/null
must_allow "a OVERWRITES an object tagged as b's" smoke-estate-a put-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-a
awsl s3api put-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy-2 --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-b >/dev/null
must_allow "a DELETES an object tagged as b's" smoke-estate-a delete-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy-2
proof "a wrong prefix is enough to destroy a neighbour's records. Reading takes two mistakes; writing and deleting take one. The renderer refuses anything that is not an estate name for this reason."

step "5. a declared dependency is the one read that crosses, and only with its statement"
role_with_policy smoke-estate-a "$("$POLICY_RENDERER" smoke-a "$BUCKET")" "$BUCKET" || fail "manyestates" "could not restore estate a's policy"
must_deny "a reads b's outputs, no dependency declared" smoke-estate-a get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
role_with_policy smoke-estate-a "$("$POLICY_RENDERER" smoke-a "$BUCKET" --reads-outputs-of smoke-b)" "$BUCKET" || fail "manyestates" "could not install the dependency policy"
cmd "render-policy.sh smoke-a $BUCKET --reads-outputs-of smoke-b"
must_allow "a reads b's outputs, dependency declared" smoke-estate-a get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
must_deny "a reads b's RECORDS, dependency declared" smoke-estate-a get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
proof "outputs and nothing else. Everything in them crosses, sensitive values included: a declared dependency is a declared disclosure."

step "6. teardown"
role_with_policy smoke-estate-a "$("$POLICY_RENDERER" smoke-a "$BUCKET")" "$BUCKET" || true
for e in a b; do
  D_OUT="$(cd "$SMOKE_WORK/$e" && as_role "smoke-estate-$e" chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "estate $e's teardown failed: $D_OUT"
  grep -q "2 destroyed" <<< "$D_OUT" || fail "manyestates" "estate $e's teardown did not destroy both instances: $D_OUT"
done
proof "both estates gone, each by its own role."

echo "  What you watched: two estates in one bucket, one's role refused at the"
echo "  other's records by prefix and then, with the prefix deliberately"
echo "  wrong, by the tag alone; what the tag cannot stop; and the one"
echo "  declared read that crosses."
