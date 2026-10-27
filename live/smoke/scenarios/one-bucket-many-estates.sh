# one-bucket-many-estates
# CLAIM 28 (aws) - An estate reaches only its own records, and what it declares (REAL AWS, maintainer-run). ~8 min.
#
# This proof: One bucket, many estates: every object an estate writes
# carries its estate's tag, a new estate's first write goes through the
# published policy, and reading a neighbour's records takes two mistakes,
# not one.
#
# It holds three scenarios that each created a bucket and IAM roles on a
# real account against the same published policy: this one (claim 35 until
# #1817), a-new-estate-writes-its-first-record (claim 34) and
# objects-carry-the-estate-tag (claim 36). Their controls are arms here:
#
#   BREAK=1             two arms: without the relabel Deny, and without the
#                       read Deny, a mis-scoped prefix reads b's record
#   BREAK_FIRST_WRITE=1 the write conditioned on s3:ExistingObjectTag; a new
#                       estate's first write must be denied (claim 34's)
#   BREAK_UNTAGGED=1    a binary whose S3 store sends no tags; its first
#                       write must be denied (claim 36's; needs Go)
#
# Each arm stops after its control. The two new arms run before either
# estate applies, so each meets an empty bucket.

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

if [ "${BREAK_FIRST_WRITE:-0}" = "1" ]; then
  step "BREAK_FIRST_WRITE control - the policy that reviews correctly and bricks a new estate"
  explain \
    "An estate's role may write only objects tagged as that estate's. The" \
    "obvious way to say so is a condition on s3:ExistingObjectTag, and it" \
    "is wrong: that key reads the tags an object ALREADY has, and an object" \
    "that does not exist yet has none. This is the published policy with" \
    "that one key changed, and a brand-new estate's first apply under it" \
    "must be denied."
  ROLE_N="$(role_name smoke-new-estate)" || fail "manyestates" "could not name the new estate's role"
  POLICY_N="$("$POLICY_RENDERER" smoke-new "$BUCKET" | jq '(.Statement[] | select(.Sid == "WriteOnlyObjectsTaggedAsThisEstate") | .Condition) = {"StringEquals": {"s3:ExistingObjectTag/tofu-estate": "smoke-new"}}')"
  grep -q '"s3:RequestObjectTag/tofu-estate"' <<< "$POLICY_N" \
    && fail "manyestates" "the break did not rewrite the write statement, so this arm would test the published policy"
  role_with_policy "$ROLE_N" "$POLICY_N" "$BUCKET" || fail "manyestates" "could not create the new estate's role"
  jq -c '.Statement[] | select(.Sid == "WriteOnlyObjectsTaggedAsThisEstate") | {Action, Condition}' <<< "$POLICY_N" | evidence
  write_bucket_estate "$SMOKE_WORK/new" smoke-new "$BUCKET" v1
  logged one-bucket-many-estates-new-init "manyestates" "init failed in new" -- in_dir "$SMOKE_WORK/new" chdf init -input=false -no-color
  cmd "choudoufu apply -auto-approve   # as the new estate's role, into an empty prefix"
  N_OUT="$(cd "$SMOKE_WORK/new" && as_role "$ROLE_N" chdf apply -auto-approve -input=false -no-color 2>&1)" \
    && fail "manyestates" "the first write SUCCEEDED under a policy that conditions the create on the tags of an object that does not exist, so this arm proves nothing: $N_OUT"
  denied "$(flat <<< "$N_OUT")" || fail "manyestates" "the first write failed, but not on a denial: $N_OUT"
  LEFT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-new/ --query 'length(Contents || `[]`)' --output text)"
  [ "$LEFT" = "0" ] || fail "manyestates" "the denied estate still wrote $LEFT object(s)"
  flat <<< "$N_OUT" | grep -oE 'AccessDenied[^.]*' | head -1 | mask | evidence
  proof "caught - the estate could not write its first object. Nothing about that policy looks wrong until a new estate meets it, which is why the published one conditions the create on s3:RequestObjectTag."
  exit 0
fi

if [ "${BREAK_UNTAGGED:-0}" = "1" ]; then
  step "BREAK_UNTAGGED control - a store that does not tag must not be able to write"
  explain \
    "The corruption is in the binary: the S3 store stops sending tags." \
    "Built with go build -overlay, so the source tree is never touched." \
    "If that binary can still write under the published policy, then" \
    "tagging is a convention the policy does not enforce."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "manyestates" "BREAK_UNTAGGED=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION"
  command -v go >/dev/null 2>&1 || fail "manyestates" "BREAK_UNTAGGED=1 needs Go to build the broken binary"
  SRC="$ROOT/pkg/staterecord/s3.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/s3.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
# The argument order is whatever S3Store.PutIfVersion has today. It was
# swapped once (#1383, base tags win) and this patch, which spelled the
# arguments out, went stale without anyone noticing, because nothing runs a
# real-AWS BREAK arm but a person. live/smoke_break_patches_test.go now runs
# this block on every gate.
old = '); tagging != "" {\n\t\tinput.Tagging = aws.String(tagging)\n'
assert src.count(old) == 1, "the break patch no longer matches S3Store.PutIfVersion"
open(sys.argv[2], "w").write(src.replace(old, '); false && tagging != "" {\n\t\tinput.Tagging = aws.String(tagging)\n'))
PYEOF
  [ -s "$SMOKE_WORK/break/s3.go" ] || fail "manyestates" "the break patch did not apply, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/s3.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # PutObject with no Tagging"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "manyestates" "the broken binary did not build"
  UNTAGGED_BIN="$SMOKE_WORK/break/choudoufu"
  ROLE_N="$(role_name smoke-untagged-estate)" || fail "manyestates" "could not name the untagged estate's role"
  role_with_policy "$ROLE_N" "$("$POLICY_RENDERER" smoke-new "$BUCKET")" "$BUCKET" || fail "manyestates" "could not create the untagged estate's role"
  write_bucket_estate "$SMOKE_WORK/new" smoke-new "$BUCKET" v1
  logged one-bucket-many-estates-untagged-init "manyestates" "init failed in new" -- in_dir "$SMOKE_WORK/new" "$UNTAGGED_BIN" init -input=false -no-color
  cmd "choudoufu apply -auto-approve   # the untagged binary, as the estate's role"
  N_OUT="$(cd "$SMOKE_WORK/new" && as_role "$ROLE_N" "$UNTAGGED_BIN" apply -auto-approve -input=false -no-color 2>&1)" \
    && fail "manyestates" "a binary that sends no tags applied under the published policy, so the policy does not enforce tagging: $N_OUT"
  denied "$(flat <<< "$N_OUT")" || fail "manyestates" "the untagged apply failed, but not on a denial: $N_OUT"
  LEFT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'length(Contents || `[]`)' --output text)"
  [ "$LEFT" = "0" ] || fail "manyestates" "the untagged binary still wrote $LEFT object(s)"
  flat <<< "$N_OUT" | grep -oE 'AccessDenied[^"]*s3:PutObject' | head -1 | mask | evidence
  proof "caught - without its tags the store cannot write a single object. Under this policy tagging is not a convention."
  exit 0
fi

for e in a b; do
  role_with_policy "$(role_of "$e")" "$("$POLICY_RENDERER" "smoke-$e" "$BUCKET")" "$BUCKET" || fail "manyestates" "could not create estate $e's role"
  write_bucket_estate "$SMOKE_WORK/$e" "smoke-$e" "$BUCKET" v1
  # Estate b keys one instance "a.b": a dot in an instance key is escaped as
  # @d in the record's tofu-address tag, the same escape the resource's own
  # tag carries, and step 2 reads it back by value (#1379).
  if [ "$e" = b ]; then sed_i "$SMOKE_WORK/b/main.tf" 's/"two"/"a.b"/'; fi
  logged "one-bucket-many-estates-$e-init" "manyestates" "init failed in $e" -- in_dir "$SMOKE_WORK/$e" chdf init -input=false -no-color
  OUT="$(cd "$SMOKE_WORK/$e" && as_role "$(role_of "$e")" chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "estate $e could not apply under its own role: $OUT"
  grep -q "Resources: 2 added" <<< "$OUT" || fail "manyestates" "estate $e: $OUT"
done
B_RECORD="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-b/terraform_data/ --query 'Contents[0].Key' --output text)"
B_OUTPUT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-outputs/smoke-b/ --query 'Contents[0].Key' --output text)"
A_RECORD="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-a/terraform_data/ --query 'Contents[0].Key' --output text)"
[ -n "$B_RECORD" ] && [ "$B_RECORD" != "None" ] && [ "$B_OUTPUT" != "None" ] || fail "manyestates" "estate b wrote no record or no output to stay out of"
awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu- --query 'Contents[].Key' --output text | tr '\t' '\n' | evidence
proof "both estates applied under their own roles, into one bucket: a new estate's first write goes through the published policy."

step "2. every object carries its estate's tag"
explain \
  "Every object choudoufu writes to the bucket carries the estate's own" \
  "markers, tofu-estate on all of them and tofu-address on a record, the" \
  "same pair every managed resource carries. The policy below leans on" \
  "that tag, so it is read back object by object, not assumed."
for e in a b; do
  N=0; RECORD_ADDRS=""
  while read -r key; do
    [ -n "$key" ] || continue
    TAGS="$(awsl s3api get-object-tagging --bucket "$BUCKET" --key "$key" --query 'TagSet[].[Key,Value]' --output text | sort | tr '\t' '=' | tr '\n' ' ')"
    grep -q "tofu-estate=smoke-$e " <<< "$TAGS " || fail "manyestates" "$key does not carry tofu-estate=smoke-$e: [$TAGS]"
    case "$key" in
      tofu-records/*/terraform_data/*)
        ADDR="$(grep -oE 'tofu-address=[^ ]+' <<< "$TAGS" | cut -d= -f2)"
        [ -n "$ADDR" ] || fail "manyestates" "record $key carries no tofu-address: [$TAGS]"
        RECORD_ADDRS="$RECORD_ADDRS $ADDR"
        ;;
    esac
    echo "$key   $TAGS" | evidence
    N=$((N+1))
  done < <(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-" --query 'Contents[].Key' --output json | jq -r '.[]' | grep -E "^tofu-[a-z]+/smoke-$e/")
  [ "$N" -ge 4 ] || fail "manyestates" "only $N of estate $e's objects were checked; expected at least an output, a hint and two records"
  case "$e" in
    a) WANT_ADDRS="terraform_data.effect:one terraform_data.effect:two" ;;
    b) WANT_ADDRS="terraform_data.effect:a@db terraform_data.effect:one" ;;
  esac
  # shellcheck disable=SC2086  # one address per word is the point
  GOT_ADDRS="$(printf '%s\n' $RECORD_ADDRS | sort -u | tr '\n' ' ' | sed 's/ *$//')"
  [ "$GOT_ADDRS" = "$WANT_ADDRS" ] \
    || fail "manyestates" "estate $e's records carry tofu-address [$GOT_ADDRS], want [$WANT_ADDRS]"
done
proof "every object tagged with its own estate, and estate b's records carrying terraform_data.effect:a@db for effect[\"a.b\"]: the same escape the resource's own tag uses."

step "3. estate a's role, at estate b's door"
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
    || fail "manyestates" "without the relabel Deny the retag of estate b's record was still refused, so something else is refusing it and step 4 cannot credit that statement: $OUT"
  [ "$(tag_of "$B_RECORD")" = "smoke-a" ] || fail "manyestates" "the retag was allowed and the tag did not change"
  OUT="$(s3_as "$ROLE_A" get-object --bucket "$BUCKET" --key "$B_RECORD" "$SMOKE_WORK/stolen")" \
    || fail "manyestates" "the record was retagged and the read was still refused: $OUT"
  [ -s "$SMOKE_WORK/stolen" ] || fail "manyestates" "the read was allowed and returned nothing"
  echo "a retags b's record as its own: ALLOWED; a then reads it: ALLOWED, $(wc -c < "$SMOKE_WORK/stolen" | tr -d ' ') bytes" | evidence
  awsl s3api put-object-tagging --bucket "$BUCKET" --key "$B_RECORD" --tagging 'TagSet=[{Key=tofu-estate,Value=smoke-b}]' >/dev/null
  proof "caught - without that one statement a widened prefix alone reads a neighbour's record. In step 4 it is the relabel Deny that refuses the retag, and nothing else."

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
  proof "caught - with both defences down the record is readable, so in step 4 it was the tag that refused it and nothing else."
  exit 0
fi

step "4. the prefix written wrong, and the tag still holding"
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

step "5. what the tag cannot defend, shown and not left out"
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

step "6. another estate's outputs are readable only with the statement that grants it"
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
proof "outputs and nothing else. What is there to read is what the other estate wrote: its root output values, never one marked sensitive. The read that uses this grant is a consumer's data \"terraform_estate_outputs\" block naming the other estate; an-estate-reads-another-by-declaring-it runs it."

step "7. every write after the first, as the role"
explain \
  "A policy can let a create through and still break the estate: every" \
  "update and delete choudoufu makes is conditional (If-Match), and AWS" \
  "authorizes a conditional write as a read too. So estate a updates under" \
  "its own published policy and replans from its records alone; step 9's" \
  "destroy is the deletes."
role_with_policy "$ROLE_A" "$("$POLICY_RENDERER" smoke-a "$BUCKET")" "$BUCKET" || fail "manyestates" "could not restore estate a's policy"
write_bucket_estate "$SMOKE_WORK/a" smoke-a "$BUCKET" v2
cmd "choudoufu apply -auto-approve   # an update, under If-Match"
U_OUT="$(cd "$SMOKE_WORK/a" && as_role "$ROLE_A" chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "the update failed under the published policy: $U_OUT"
grep -q "Resources: 0 added, 2 changed, 0 destroyed" <<< "$U_OUT" || fail "manyestates" "the update: $U_OUT"
rm -f "$SMOKE_WORK/a/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # from the records alone"
P_OUT="$(cd "$SMOKE_WORK/a" && as_role "$ROLE_A" chdf plan -input=false -no-color 2>&1)" || fail "manyestates" "the replan failed: $P_OUT"
grep -q "No changes." <<< "$P_OUT" || fail "manyestates" "the replan was not empty: $P_OUT"
echo "2 changed under If-Match; replan from the records empty" | evidence
proof "update and read, as the role, under the policy as published."

step "8. a record tagged as another estate's is refused to this one"
explain \
  "One of estate a's records is retagged out of band as" \
  "tofu-estate=someone-else, under estate a's own prefix. The role's" \
  "prefix scope still reaches it. The tag is now the only thing that says" \
  "it is not this estate's, and the run must stop and name the record" \
  "rather than plan as if it were gone."
awsl s3api put-object-tagging --bucket "$BUCKET" --key "$A_RECORD" --tagging 'TagSet=[{Key=tofu-estate,Value=someone-else}]' >/dev/null || fail "manyestates" "could not retag estate a's record"
must_deny "a reads its own record, retagged as someone else's" "$ROLE_A" get-object --bucket "$BUCKET" --key "$A_RECORD" "$SMOKE_WORK/o"
rm -f "$SMOKE_WORK/a/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # as estate a's role"
P_OUT="$(cd "$SMOKE_WORK/a" && as_role "$ROLE_A" chdf plan -input=false -no-color 2>&1)" && fail "manyestates" "the plan SUCCEEDED with a record the role may not read: $P_OUT"
grep -qE 'Plan: [1-9][0-9]* to add' <<< "$P_OUT" && fail "manyestates" "the run rendered a plan that re-creates the resource whose record it could not read: $P_OUT"
grep -q "$(basename "$A_RECORD")" <<< "$(flat <<< "$P_OUT")" || fail "manyestates" "the refusal does not name the record it could not read: $P_OUT"
grep -E '^.?Error:' <<< "$P_OUT" | head -1 | evidence
awsl s3api put-object-tagging --bucket "$BUCKET" --key "$A_RECORD" --tagging 'TagSet=[{Key=tofu-estate,Value=smoke-a}]' >/dev/null || fail "manyestates" "could not put estate a's tag back"
R_OUT="$(cd "$SMOKE_WORK/a" && as_role "$ROLE_A" chdf plan -input=false -no-color 2>&1)" || fail "manyestates" "the plan failed after the tag was restored: $R_OUT"
grep -q "No changes." <<< "$R_OUT" || fail "manyestates" "the plan was not empty after the tag was restored: $R_OUT"
proof "denied by the tag under its own prefix, and the run refused by naming the record. A record it may not read is not a record that is gone; with the tag back the plan is empty."

step "9. teardown"
role_with_policy "$ROLE_A" "$("$POLICY_RENDERER" smoke-a "$BUCKET")" "$BUCKET" || true
for e in a b; do
  D_OUT="$(cd "$SMOKE_WORK/$e" && as_role "$(role_of "$e")" chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "manyestates" "estate $e's teardown failed: $D_OUT"
  destroyed_exactly manyestates 2 "$D_OUT"
done
proof "both estates gone, each by its own role."

echo "  What you watched: two estates in one bucket, every object tagged with"
echo "  its own estate; one's role refused at the other's records by prefix"
echo "  and then, with the prefix deliberately wrong, by the tag alone; what"
echo "  the tag cannot stop; the one declared read that crosses; and a record"
echo "  retagged as someone else's refused by name to its own estate."
