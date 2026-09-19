# a-read-only-role-can-plan
# CLAIM 38 - A role with the read-only policy plans an established estate and writes nothing, and a store with no sentinel is still refused by name (REAL AWS, maintainer-run). ~6 min.

SMOKE_WORK="$SMOKE_WORKROOT/readonlyplan"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"

step "the claim"
explain \
  "A plan changes nothing, so a CI plan job is usually given a role that" \
  "may read and not write. Opening the record store used to break that:" \
  "every run sends a conditional write for the store sentinel, and a role" \
  "without s3:PutObject got AccessDenied where a second run gets a version" \
  "conflict, so the run stopped before any plan was built (#1370)." \
  "" \
  "Two things had to be true for that to be fixed, and this measures both" \
  "against real IAM. A run that may not write the sentinel opens the store" \
  "when the sentinel is already there, plans, and writes nothing. And a" \
  "store with NO sentinel is still refused by name, because there is" \
  "nothing there to tell an estate that was never recorded from one whose" \
  "records this run cannot see, and reading that as an empty estate is" \
  "#693 whole."

step "0. real AWS"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "readonlyplan" "this scenario runs against real AWS: it creates an S3 bucket and two IAM roles in the account your credentials name, and removes them. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
command -v jq >/dev/null 2>&1 || fail "readonlyplan" "jq is not installed; the policy renderer needs it"
real_aws_begin readonlyplan
BUCKET="chdf-smoke-roplan-$SUFFIX"
ESTATE="smoke-roplan"
# The second estate name, in the same bucket, that nothing has ever written.
# It is where the refusal under test lives.
EMPTY_ESTATE="smoke-roplan-empty"
# One name per RUN, for #1378's reason: a fixed role name is adopted by the
# next run whatever policy it is carrying.
WRITER="$(role_name smoke-roplan-writer)" || fail "readonlyplan" "could not name the writing role"
READER="$(role_name smoke-roplan-reader)" || fail "readonlyplan" "could not name the reading role"

# The binary the READER runs. BREAK=1 swaps in one whose sentinel handshake
# treats a denied write the way every binary before #1416 did.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - the binary this claim is about"
  explain \
    "The corruption is in the binary: provisionStoreSentinel stops" \
    "carrying a denied sentinel write past to its List and returns the" \
    "denial, which is what every binary before #1416 did. Built with" \
    "go build -overlay, so the source tree is never touched. If the" \
    "reader can still plan with that binary, then nothing this claim" \
    "measures is the code change - it is the policy being wider than it" \
    "says it is."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "readonlyplan" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION"
  command -v go >/dev/null 2>&1 || fail "readonlyplan" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/projection/store.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/store.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
# One line, and nothing around it. A patch that spelled out the whole switch
# went stale on claim 36 the first time the code around it moved (#1379), and
# nothing runs a real-AWS BREAK arm but a person; live/smoke_break_patches_test.go
# runs this block on every gate, so it says out loud when it stops matching.
#
# The case being unreachable is exactly the pre-#1416 behaviour: the denial
# falls through to the default arm, which wraps it and returns.
old = "\t\tcase staterecord.IsAccessDenied(err):\n"
assert src.count(old) == 1, "the break patch no longer matches provisionStoreSentinel's denial arm"
open(sys.argv[2], "w").write(src.replace(old, "\t\tcase false && staterecord.IsAccessDenied(err):\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/store.go" ] || fail "readonlyplan" "the break patch did not apply, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/store.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # a denied sentinel write ends the run"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "readonlyplan" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi

# Every step after this one runs from the bucket-iam.sh EXIT trap that
# real_aws_begin installed: it removes both roles and then the bucket, under
# `set +e`, with a line per resource (#1378). This scenario creates nothing
# else - the estate is terraform_data instances, which exist only as records
# in the bucket the trap empties - so there is no second teardown body here,
# and live/smoke/selftest-teardown.sh already drives that one.

run_as_writer() { ( cd "$SMOKE_WORK/est" && as_role "$WRITER" "$TOFU" "$@" ); }
# The reader always runs under TF_LOG=debug: step 5 reconciles what its plan
# asked S3 for against what the read-only policy grants, and that is read out
# of the request log.
run_as_reader() { # <log-name> <dir> <args...>
  ( cd "$2" && as_role "$READER" env TF_LOG=debug TF_LOG_PATH="$SMOKE_WORKROOT/logs/roplan-$1.log" "$RUN_BIN" "${@:3}" )
}
# The object versions under one estate's three namespaces, as text. What a
# read-only plan must leave untouched, sentinel included: the sentinel lives
# under the records prefix.
versions_under() {
  local e="$1" p
  for p in "tofu-records/$e/" "tofu-hints/$e/" "tofu-outputs/$e/"; do
    aws s3api list-object-versions --bucket "$BUCKET" --prefix "$p" \
      --query 'Versions[].[Key,VersionId]' --output text 2>/dev/null
  done | sort
}

step "1. an estate applied under the FULL policy, which provisions the sentinel"
explain \
  "The reader cannot create the sentinel and must not have to. One run" \
  "under a role that may write is what an estate does once, and this is" \
  "that run: the published policy, unedited, rendered by the script that" \
  "ships."
bucket_up "$BUCKET" || fail "readonlyplan" "could not create the bucket"
cmd "render-policy.sh $ESTATE $BUCKET   # the full policy, for the writing role"
role_with_policy "$WRITER" "$("$POLICY_RENDERER" "$ESTATE" "$BUCKET")" "$BUCKET" \
  || fail "readonlyplan" "could not create the writing role"
write_bucket_estate "$SMOKE_WORK/est" "$ESTATE" "$BUCKET" v1
( cd "$SMOKE_WORK/est" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "readonlyplan" "init failed"
cmd "choudoufu apply -auto-approve   # as the writing role"
A_OUT="$(run_as_writer apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "readonlyplan" "the estate could not apply under the full policy: $A_OUT"
grep -q "Resources: 2 added" <<< "$A_OUT" || fail "readonlyplan" "the apply: $A_OUT"
SENTINEL="tofu-records/$ESTATE/.store-sentinel"
aws s3api head-object --bucket "$BUCKET" --key "$SENTINEL" >/dev/null 2>&1 \
  || fail "readonlyplan" "the apply did not leave a sentinel at $SENTINEL, so the reader below would be refused for the right reason by accident"
echo "sentinel at $SENTINEL; $(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-" --query 'length(Contents || `[]`)' --output text) object(s) under tofu-" | evidence
proof "the estate is recorded and the store carries its sentinel."

step "2. a second role, from the read-only rendering"
explain \
  "render-policy.sh --read-only: the estate's namespaces listed," \
  "s3:GetObject by prefix, the foreign-tag Deny, and nothing that writes," \
  "tags or deletes. Merged into it are the ALLOW statements of a second" \
  "read-only render, for the estate name step 4 uses. Its Deny statements" \
  "are deliberately left out: each render's read Deny accepts its own" \
  "estate's tag and no other, so two of them together refuse every tagged" \
  "object in the bucket, and step 3 would then fail on a denial that has" \
  "nothing to do with what is being measured. The first render's Deny" \
  "still covers the whole bucket, so the reader can read no object of the" \
  "second estate either - only LIST its namespace, which is all step 4" \
  "needs to reach the refusal under test."
RO_ESTATE_POLICY="$("$POLICY_RENDERER" "$ESTATE" "$BUCKET" --read-only)" \
  || fail "readonlyplan" "the read-only render for $ESTATE failed"
RO_EMPTY_POLICY="$("$POLICY_RENDERER" "$EMPTY_ESTATE" "$BUCKET" --read-only)" \
  || fail "readonlyplan" "the read-only render for $EMPTY_ESTATE failed"
# The second render's statements are renamed. IAM refuses a policy that uses
# one Sid twice (MalformedPolicyDocument), which the first real run of this
# scenario met: both renders call their statements ListOwnNamespaces and
# ReadByPrefix.
READER_POLICY="$(printf '%s\n%s\n' "$RO_ESTATE_POLICY" "$RO_EMPTY_POLICY" \
  | jq -s '{Version: "2012-10-17", Statement: (.[0].Statement + (.[1].Statement | map(select(.Effect == "Allow") | .Sid += "OfTheEmptyEstate")))}')" \
  || fail "readonlyplan" "could not merge the two read-only renders"
# Asserted rather than assumed: the merge must have brought nothing that
# writes with it, and it must have brought the second estate's LIST.
jq -e '[.Statement[] | select(.Effect == "Allow") | [.Action] | flatten | .[]
        | select(. == "s3:PutObject" or . == "s3:PutObjectTagging" or . == "s3:DeleteObject")] | length == 0' \
  <<< "$READER_POLICY" >/dev/null \
  || fail "readonlyplan" "the reader's policy allows a write or a delete: $(jq -c '[.Statement[] | select(.Effect=="Allow") | .Sid]' <<< "$READER_POLICY")"
jq -e --arg p "tofu-records/$EMPTY_ESTATE/*" '[.Statement[] | select(.Action == "s3:ListBucket")
        | .Condition.StringLike["s3:prefix"][] | select(. == $p)] | length == 1' \
  <<< "$READER_POLICY" >/dev/null \
  || fail "readonlyplan" "the reader cannot list $EMPTY_ESTATE's records namespace, so step 4 would measure a List denial and not the refusal under test"
cmd "render-policy.sh $ESTATE $BUCKET --read-only   # plus the Allow half of the same for $EMPTY_ESTATE"
# role_with_policy proves the policy is live by reading AND writing one
# marker key, through a ProofThisPolicyIsLive statement it appends. That is
# fine for a reader: the statement grants the two actions on that one key and
# on nothing else, it is outside every tofu- prefix, and every assertion
# below is about the estate's own namespaces.
role_with_policy "$READER" "$READER_POLICY" "$BUCKET" || fail "readonlyplan" "could not create the reading role"
jq -r '.Statement[] | select(.Effect == "Allow") | .Sid' <<< "$READER_POLICY" | tr '\n' ' ' \
  | sed 's/^/what the reader may do: /' | evidence
proof "the reader is allowed a list and a read of the two estates' namespaces, and nothing else."

step "3. the reader plans, and the store is untouched"
explain \
  "A fresh checkout, so there is no state cache to plan from: everything" \
  "this plan knows about the estate it read out of the bucket. The plan" \
  "must be empty, the reader's own put under the estate's prefix must be" \
  "denied, and the three namespaces must hold exactly the object versions" \
  "they held before - the sentinel included, which is the write the run" \
  "sends and is refused."
write_bucket_estate "$SMOKE_WORK/reader" "$ESTATE" "$BUCKET" v1
( cd "$SMOKE_WORK/reader" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "readonlyplan" "init failed in the reader's directory"
BEFORE="$(versions_under "$ESTATE")"
[ -n "$BEFORE" ] || fail "readonlyplan" "the estate's namespaces hold no object versions at all, so the comparison below would pass over nothing"
cmd "choudoufu plan   # as the read-only role"
P_OUT="$(run_as_reader plan "$SMOKE_WORK/reader" plan -input=false -no-color 2>&1)" && P_RC=0 || P_RC=$?

if [ "${BREAK:-0}" = "1" ]; then
  [ "$P_RC" != "0" ] || fail "readonlyplan" "the binary that returns a denied sentinel write still planned as the read-only role, so this claim is not measuring #1416's change: $P_OUT"
  P_FLAT="$(flat <<< "$P_OUT")"
  grep -q "No changes." <<< "$P_FLAT" && fail "readonlyplan" "the broken binary rendered an empty plan: $P_OUT"
  denied "$P_FLAT" || fail "readonlyplan" "the plan failed, but not on a denial, so nothing here shows the sentinel write is what stopped it: $P_OUT"
  grep -q "sentinel" <<< "$P_FLAT" \
    || fail "readonlyplan" "the plan was denied but says nothing about the sentinel, so the failure is somewhere else: $P_OUT"
  AFTER="$(versions_under "$ESTATE")"
  [ "$BEFORE" = "$AFTER" ] || fail "readonlyplan" "the broken binary changed the store: $(diff <(echo "$BEFORE") <(echo "$AFTER") | head -5)"
  grep -oE 'AccessDenied[^"]*|provisioning the sentinel[^.]*' <<< "$P_FLAT" | head -1 | mask | evidence
  proof "caught - with the denial returned instead of carried past to the List, the same role and the same policy cannot plan at all. That is what every binary before #1416 did, and it is the whole of issue #1370."
  exit 0
fi

[ "$P_RC" = "0" ] || fail "readonlyplan" "the read-only role could not plan an established estate: $P_OUT"
grep -q "No changes." <<< "$P_OUT" \
  || fail "readonlyplan" "the read-only role's plan was not empty, so it did not read the records the writing role left: $P_OUT"
grep -q "The record store was not read" <<< "$(flat <<< "$P_OUT")" \
  && fail "readonlyplan" "the plan is empty but the run says the record store was not read (#1388), so it planned from something else: $P_OUT"
cmd "aws s3api put-object ...   # as the read-only role, under the estate's own prefix"
echo probe > "$SMOKE_WORK/probe"
W_OUT="$(as_role "$READER" aws s3api put-object --bucket "$BUCKET" --key "tofu-records/$ESTATE/probe" --body "$SMOKE_WORK/probe" 2>&1)" \
  && fail "readonlyplan" "the read-only role wrote an object under the estate's prefix: $W_OUT"
denied "$W_OUT" || fail "readonlyplan" "the reader's write failed, but not on a denial, so nothing here shows the policy refused it: $W_OUT"
AFTER="$(versions_under "$ESTATE")"
[ "$BEFORE" = "$AFTER" ] \
  || fail "readonlyplan" "the read-only plan changed the store: $(diff <(echo "$BEFORE") <(echo "$AFTER") | head -10)"
echo "object versions under the three namespaces before the plan and after it: $(wc -l <<< "$BEFORE" | tr -d ' ') and $(wc -l <<< "$AFTER" | tr -d ' '), identical" | evidence
flat <<< "$W_OUT" | grep -oE 'AccessDenied[^"]*' | head -1 | mask | evidence
proof "an empty plan from the records alone, a direct write refused, and not one object version added or replaced - the sentinel included."

step "4. an estate with no sentinel is still refused, by name"
explain \
  "The same reader, the same bucket, an estate name nothing has ever" \
  "written. The reader may list and read that name's namespaces, so what" \
  "it meets is not a List denial: it is a store that holds no sentinel and" \
  "an identity that cannot provision one. Those two together are" \
  "indistinguishable from an empty estate, and planning an empty estate" \
  "here would propose creating everything in it again. That is #693's" \
  "failure, and it has to stay a refusal."
write_bucket_estate "$SMOKE_WORK/empty" "$EMPTY_ESTATE" "$BUCKET" v1
( cd "$SMOKE_WORK/empty" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || fail "readonlyplan" "init failed in the empty estate's directory"
EMPTY_OBJECTS="$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-records/$EMPTY_ESTATE/" --query 'length(Contents || `[]`)' --output text)"
[ "$EMPTY_OBJECTS" = "0" ] || fail "readonlyplan" "$EMPTY_ESTATE already has $EMPTY_OBJECTS object(s) under its records prefix, so there is no unprovisioned store to refuse"
cmd "choudoufu plan   # as the read-only role, against an estate with no sentinel"
E_OUT="$(run_as_reader empty "$SMOKE_WORK/empty" plan -input=false -no-color 2>&1)" \
  && fail "readonlyplan" "the plan SUCCEEDED against a store with no sentinel that this identity cannot write: $E_OUT"
E_FLAT="$(flat <<< "$E_OUT")"
grep -qE 'Plan: [0-9]+ to add' <<< "$E_FLAT" \
  && fail "readonlyplan" "the run proposed creating an estate it could not tell apart from an unreadable one: $E_OUT"
grep -q "holds no sentinel" <<< "$E_FLAT" \
  || fail "readonlyplan" "the refusal does not say the store holds no sentinel, so an operator is sent to the role's S3 permissions instead: $E_OUT"
grep -q "may not write one" <<< "$E_FLAT" \
  || fail "readonlyplan" "the refusal does not say this identity may not write the sentinel, which is the other half of what has to be fixed: $E_OUT"
grep -q "tofu-records/$EMPTY_ESTATE/.store-sentinel" <<< "$E_FLAT" \
  || fail "readonlyplan" "the refusal does not name the key it looked for: $E_OUT"
grep -oE "record_store: this store holds no sentinel[^;]*;" <<< "$E_FLAT" | head -1 | mask | evidence
proof "refused by name, naming the key, saying this identity may not write it, and proposing nothing."

step "5. what the reader's plan asked S3 for, against what the policy grants"
explain \
  "Every S3 operation the reader made is in its request log. A read-only" \
  "policy that grants something a plan never uses, or a plan that needs" \
  "something it does not grant, is a defect in the policy. There is" \
  "exactly one call on the second side and it is the point of this claim:" \
  "the sentinel write, denied and survived."
USED="$(grep 'stateless/recordstore: HTTP Request Sent' "$SMOKE_WORKROOT/logs/roplan-plan.log" | grep -oE 'rpc.method=[A-Za-z0-9]+' | cut -d= -f2 | sort -u)"
[ -n "$USED" ] || fail "readonlyplan" "the reader's request log holds no record store requests, so nothing was reconciled"
# The same mapping claim 37 uses, so the two reconciliations agree on what a
# call costs. A PutObject choudoufu sends carries the estate tag, so AWS
# authorizes it as s3:PutObject AND s3:PutObjectTagging.
to_action() { case "$1" in
  ListObjectsV2) echo s3:ListBucket ;; GetObject|HeadObject) echo s3:GetObject ;;
  PutObject) echo "s3:PutObject s3:PutObjectTagging" ;;
  DeleteObject) echo s3:DeleteObject ;; GetBucketVersioning) echo s3:GetBucketVersioning ;;
  GetBucketLifecycleConfiguration) echo s3:GetLifecycleConfiguration ;; GetPublicAccessBlock) echo s3:GetBucketPublicAccessBlock ;;
  *) echo "UNMAPPED:$1" ;; esac; }
USED_ACTIONS="$(for op in $USED; do to_action "$op"; done | tr ' ' '\n' | sort -u)"
grep -q UNMAPPED <<< "$USED_ACTIONS" && fail "readonlyplan" "the reader made an S3 call this scenario cannot map to an IAM action: $USED_ACTIONS"
GRANTED="$(jq -r '.Statement[] | select(.Effect == "Allow") | .Action | if type == "array" then .[] else . end' <<< "$RO_ESTATE_POLICY" | grep '^s3:' | sort -u)"
ONLY_USED="$(comm -23 <(echo "$USED_ACTIONS") <(echo "$GRANTED") | tr '\n' ' ' | sed 's/ *$//')"
ONLY_GRANTED="$(comm -13 <(echo "$USED_ACTIONS") <(echo "$GRANTED"))"
[ -z "$ONLY_GRANTED" ] || fail "readonlyplan" "the read-only policy grants what the reader's plan never used: $ONLY_GRANTED"
# The one expected difference, asserted by value rather than waved past: the
# sentinel write. Anything else on this side is a permission a plan needs and
# the read-only rendering does not give it.
[ "$ONLY_USED" = "s3:PutObject s3:PutObjectTagging" ] \
  || fail "readonlyplan" "the reader's plan used [$ONLY_USED] and the read-only policy grants none of it; the only call this claim expects to be denied is the sentinel write"
grep 'stateless/recordstore: HTTP Request Sent' "$SMOKE_WORKROOT/logs/roplan-plan.log" \
  | grep 'rpc.method=PutObject' | grep -q '\.store-sentinel' \
  || fail "readonlyplan" "the PutObject the reader sent was not the sentinel write, so something else in this plan tried to write to the bucket"
# The bucket contract reads, by their absence. They are the three actions
# render-policy.sh --read-only leaves out, and this is the measurement that
# says leaving them out costs a plan nothing: the contract is asserted on an
# estate's first contact with its store and before an apply, and a read-only
# plan is neither.
for op in GetBucketVersioning GetBucketLifecycleConfiguration GetPublicAccessBlock; do
  grep -q "rpc.method=$op" <<< "$USED" \
    && fail "readonlyplan" "the reader's plan called $op, so the read-only rendering is wrong to leave the bucket-configuration reads out"
done
echo "used: $(tr '\n' ' ' <<< "$USED_ACTIONS")" | evidence
echo "granted: $(tr '\n' ' ' <<< "$GRANTED")" | evidence
echo "used and not granted: $ONLY_USED (the sentinel write); granted and not used: none" | evidence
proof "the read-only policy grants exactly what the plan used, apart from the one write the plan sends and is refused. No bucket-configuration read was made, which is why the rendering does not grant one."

echo "  What you watched: an estate recorded once by a role that may write, a"
echo "  second role carrying the read-only rendering planning it empty and"
echo "  leaving every object version where it was, the same role refused by"
echo "  name against a store no run has ever provisioned, and the plan's own"
echo "  S3 calls reconciled with what the policy grants."
