# one-bucket-many-estates
# CLAIM 35 - One bucket, many estates: reading a neighbour's records takes two mistakes, not one (REAL AWS, maintainer-run). ~6 min.

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
# One name per RUN. A fixed role name is adopted by the next run whatever
# policy it is carrying, and two runs at once overwrite each other's - and
# this scenario's whole method is installing one policy after another on
# estate a's role and measuring what it may do (#1378).
ROLE_A="$(role_name smoke-estate-a)" || fail "manyestates" "could not name estate a's role"
ROLE_B="$(role_name smoke-estate-b)" || fail "manyestates" "could not name estate b's role"
role_of() { case "$1" in a) echo "$ROLE_A" ;; b) echo "$ROLE_B" ;; esac; }
for e in a b; do
  role_with_policy "$(role_of "$e")" "$("$POLICY_RENDERER" "smoke-$e" "$BUCKET")" "$BUCKET" || fail "manyestates" "could not create estate $e's role"
  write_bucket_estate "$SMOKE_WORK/$e" "smoke-$e" "$BUCKET" v1
  ( cd "$SMOKE_WORK/$e" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "manyestates" "init failed in $e"
  OUT="$(cd "$SMOKE_WORK/$e" && as_role "$(role_of "$e")" chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "estate $e could not apply under its own role: $OUT"
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
must_allow "a reads its own record" "$ROLE_A" get-object --bucket "$BUCKET" --key "$A_RECORD" "$SMOKE_WORK/o"
must_deny "a reads b's record" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
must_deny "a reads b's outputs" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
must_deny "a lists b's records" "$ROLE_A" list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-b/
must_deny "a lists the bare prefix tofu-records/smoke-a" "$ROLE_A" list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-a
echo x > "$SMOKE_WORK/x"
must_deny "a writes under b's prefix, tagged as a" "$ROLE_A" put-object --bucket "$BUCKET" --key tofu-records/smoke-b/intruder --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-a
must_deny "a writes under its OWN prefix, tagged as b" "$ROLE_A" put-object --bucket "$BUCKET" --key tofu-records/smoke-a/mislabelled --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-b
# A neighbour whose NAME begins with this estate's. The list above already
# refuses the bare prefix; this is the same trap for a write, a delete and a
# read, where the object ARN's trailing slash is the whole defence. An ARN
# ending "tofu-records/smoke-a*" would also be "tofu-records/smoke-a-eu/...".
awsl s3api put-object --bucket "$BUCKET" --key tofu-records/smoke-a-eu/decoy --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-a-eu >/dev/null \
  || fail "manyestates" "could not write the smoke-a-eu decoy"
must_deny "a writes under smoke-a-eu, tagged as a" "$ROLE_A" put-object --bucket "$BUCKET" --key tofu-records/smoke-a-eu/intruder --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-a
must_deny "a deletes under smoke-a-eu" "$ROLE_A" delete-object --bucket "$BUCKET" --key tofu-records/smoke-a-eu/decoy
must_deny "a reads under smoke-a-eu" "$ROLE_A" get-object --bucket "$BUCKET" --key tofu-records/smoke-a-eu/decoy "$SMOKE_WORK/o"
proof "out of reach by prefix, and the bare prefix - the one that would also name a neighbour called smoke-a-eu - is refused outright."

# From here the prefix scope is deliberately WRONG: estate a's allows reach
# every estate's objects. What is left is the tag.
MISSCOPED="$("$POLICY_RENDERER" smoke-a "$BUCKET" | jq --arg b "arn:aws:s3:::$BUCKET" '
  (.Statement[] | select(.Sid == "ReadAndDeleteByPrefix" or .Sid == "WriteOnlyObjectsTaggedAsThisEstate") | .Resource) = [$b + "/tofu-*"]
  | (.Statement[] | select(.Sid == "ListOwnNamespaces") | .Condition.StringLike["s3:prefix"]) = ["tofu-*"]')"

tag_of() { awsl s3api get-object-tagging --bucket "$BUCKET" --key "$1" --query 'TagSet[?Key==`tofu-estate`].Value | [0]' --output text; }

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control 1 - without the relabel Deny, one mistake IS enough"
  explain \
    "The prefix is mis-scoped and ONE statement is removed: the Deny on" \
    "relabelling another estate's object. The read Deny is still there." \
    "The write statement checks the tag a request SENDS and nothing about" \
    "the object it lands on, so the role retags estate b's record as its" \
    "own and the read Deny then has nothing to object to. This is the" \
    "policy this repository published until #1381, measured."
  NORELABEL="$(jq 'del(.Statement[] | select(.Sid == "DenyRelabellingAnotherEstatesObjects"))' <<< "$MISSCOPED")"
  grep -q DenyRelabellingAnotherEstatesObjects <<< "$NORELABEL" && fail "manyestates" "the break did not remove the relabel Deny"
  grep -q DenyReadingAnotherEstatesObjects <<< "$NORELABEL" || fail "manyestates" "the break removed the read Deny too, so this arm would prove nothing about relabelling"
  role_with_policy "$ROLE_A" "$NORELABEL" "$BUCKET" || fail "manyestates" "could not install the policy without the relabel Deny"
  OUT="$(s3_as "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o")" \
    && fail "manyestates" "before any retag the read of estate b's record was ALLOWED, so the read Deny is not in force and this arm measures nothing: $OUT"
  OUT="$(s3_as "$ROLE_A" put-object-tagging --bucket "$BUCKET" --key "$B_RECORD" --tagging 'TagSet=[{Key=tofu-estate,Value=smoke-a}]')" \
    || fail "manyestates" "without the relabel Deny the retag of estate b's record was still refused, so something else is refusing it and step 3 cannot credit that statement: $OUT"
  [ "$(tag_of "$B_RECORD")" = "smoke-a" ] || fail "manyestates" "the retag was allowed and the tag did not change"
  OUT="$(s3_as "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/stolen")" \
    || fail "manyestates" "the record was retagged and the read was still refused: $OUT"
  [ -s "$SMOKE_WORK/stolen" ] || fail "manyestates" "the read was allowed and returned nothing"
  echo "a retags b's record as its own: ALLOWED; a then reads it: ALLOWED, $(wc -c < "$SMOKE_WORK/stolen" | tr -d ' ') bytes" | evidence
  awsl s3api put-object-tagging --bucket "$BUCKET" --key "$B_RECORD" --tagging 'TagSet=[{Key=tofu-estate,Value=smoke-b}]' >/dev/null
  proof "caught - without that one statement a widened prefix alone reads a neighbour's record. In step 3 it is the relabel Deny that refuses the retag, and nothing else."

  step "BREAK control 2 - both defects at once must let the read through"
  explain \
    "The prefix is mis-scoped AND the tag's Deny is removed. If the read" \
    "of estate b's record is still denied, something other than the two" \
    "defences this claim names is doing the denying, and the claim would" \
    "be describing a mechanism it never measured."
  BOTH="$(jq 'del(.Statement[] | select(.Sid == "DenyReadingAnotherEstatesObjects"))' <<< "$MISSCOPED")"
  grep -q DenyReadingAnotherEstatesObjects <<< "$BOTH" && fail "manyestates" "the break did not remove the Deny"
  role_with_policy "$ROLE_A" "$BOTH" "$BUCKET" || fail "manyestates" "could not install the doubly broken policy"
  OUT="$(s3_as "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/stolen")" \
    || fail "manyestates" "with the prefix mis-scoped AND the tag's Deny removed, the read of estate b's record was still refused: $OUT"
  [ -s "$SMOKE_WORK/stolen" ] || fail "manyestates" "the read was allowed and returned nothing"
  echo "a reads b's record: ALLOWED, $(wc -c < "$SMOKE_WORK/stolen" | tr -d ' ') bytes of another estate's record" | evidence
  proof "caught - with both defences down the record is readable, so in step 3 it was the tag that refused it and nothing else."
  exit 0
fi

step "3. the prefix written wrong, and the tag still holding"
role_with_policy "$ROLE_A" "$MISSCOPED" "$BUCKET" || fail "manyestates" "could not install the mis-scoped policy"
jq -c '.Statement[] | select(.Sid == "ReadAndDeleteByPrefix") | .Resource' <<< "$MISSCOPED" | evidence
must_allow "a reads its own record" "$ROLE_A" get-object --bucket "$BUCKET" --key "$A_RECORD" "$SMOKE_WORK/o"
must_deny "a reads b's record, prefix mis-scoped" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
# The tag is only a defence if it cannot be rewritten. The write statement
# checks the tag a request sends, never the object it lands on, so this is
# the obvious next move for a role that has just been refused.
must_deny "a retags b's record as its own" "$ROLE_A" put-object-tagging --bucket "$BUCKET" --key "$B_RECORD" --tagging 'TagSet=[{Key=tofu-estate,Value=smoke-a}]'
must_deny "a strips b's record's tags" "$ROLE_A" delete-object-tagging --bucket "$BUCKET" --key "$B_RECORD"
[ "$(tag_of "$B_RECORD")" = "smoke-b" ] || fail "manyestates" "estate b's record is no longer tagged smoke-b: $(tag_of "$B_RECORD")"
must_deny "a reads b's record after both attempts" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
proof "one mistake is not enough to read a neighbour's records: the object carries its own estate's tag, and this role can neither change that tag nor remove it."

step "4. what the tag cannot defend, shown and not left out"
explain \
  "Under the same mis-scoped policy. S3 gives a write or a delete no" \
  "condition key for the tags of the object it replaces, so nothing but" \
  "the prefix stands here. A throwaway object tagged as estate b's" \
  "stands in for one of b's records."
awsl s3api put-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-b >/dev/null
must_allow "a OVERWRITES an object tagged as b's" "$ROLE_A" put-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-a
awsl s3api put-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy-2 --body "$SMOKE_WORK/x" --tagging tofu-estate=smoke-b >/dev/null
must_allow "a DELETES an object tagged as b's" "$ROLE_A" delete-object --bucket "$BUCKET" --key tofu-records/smoke-b/decoy-2
proof "a wrong prefix is enough to destroy a neighbour's records. Reading takes two mistakes; writing and deleting take one. The renderer refuses anything that is not an estate name for this reason."

step "5. another estate's outputs are readable only with the statement that grants it"
role_with_policy "$ROLE_A" "$("$POLICY_RENDERER" smoke-a "$BUCKET")" "$BUCKET" || fail "manyestates" "could not restore estate a's policy"
must_deny "a reads b's outputs, no dependency declared" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
role_with_policy "$ROLE_A" "$("$POLICY_RENDERER" smoke-a "$BUCKET" --reads-outputs-of smoke-b)" "$BUCKET" || fail "manyestates" "could not install the dependency policy"
cmd "render-policy.sh smoke-a $BUCKET --reads-outputs-of smoke-b"
must_allow "a reads b's outputs, dependency declared" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
must_deny "a reads b's RECORDS, dependency declared" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
# The same, with the prefix ALSO written wrong. The dependency's tag is
# accepted under its outputs prefix and nowhere else. It used to be accepted
# across the whole bucket, and then this read went through (#1381).
DEP_MISSCOPED="$("$POLICY_RENDERER" smoke-a "$BUCKET" --reads-outputs-of smoke-b | jq --arg b "arn:aws:s3:::$BUCKET" '
  (.Statement[] | select(.Sid == "ReadAndDeleteByPrefix" or .Sid == "WriteOnlyObjectsTaggedAsThisEstate") | .Resource) = [$b + "/tofu-*"]')"
role_with_policy "$ROLE_A" "$DEP_MISSCOPED" "$BUCKET" || fail "manyestates" "could not install the mis-scoped dependency policy"
must_allow "a reads b's outputs, dependency declared, prefix mis-scoped" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_OUTPUT" "$SMOKE_WORK/o"
must_deny "a reads b's RECORDS, dependency declared, prefix mis-scoped" "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/o"
proof "outputs and nothing else. What is there to read is what the other estate wrote: its root output values, never one marked sensitive. No choudoufu run makes this read; the grant is for a reader you write yourself."

step "6. teardown"
role_with_policy "$ROLE_A" "$("$POLICY_RENDERER" smoke-a "$BUCKET")" "$BUCKET" || true
for e in a b; do
  D_OUT="$(cd "$SMOKE_WORK/$e" && as_role "$(role_of "$e")" chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "estate $e's teardown failed: $D_OUT"
  destroyed_exactly manyestates 2 "$D_OUT"
done
proof "both estates gone, each by its own role."

echo "  What you watched: two estates in one bucket, one's role refused at the"
echo "  other's records by prefix and then, with the prefix deliberately"
echo "  wrong, by the tag alone; what the tag cannot stop; and the one"
echo "  declared read that crosses."
