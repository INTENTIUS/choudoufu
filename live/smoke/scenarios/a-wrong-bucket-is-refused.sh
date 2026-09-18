# a-wrong-bucket-is-refused
# CLAIM 29 - A record store bucket without versioning, a lifecycle that expires noncurrent versions, or public-access block is refused by name before anything is applied. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/wrongbucket"
BUCKET="smoke-asserted-records"
mkdir -p "$SMOKE_WORK/est" "$SMOKE_WORK/fresh"; export SMOKE_WORK

write_estate() { # dir estate input
  cat > "$1/main.tf" <<TFEOF
terraform {
  live {
    estate = "$2"

    record_store "s3" {
      bucket = "$BUCKET"
    }
  }
}

resource "terraform_data" "effect" {
  input = "$3"
}
TFEOF
}
write_estate "$SMOKE_WORK/est" smoke-asserted v1
write_estate "$SMOKE_WORK/fresh" smoke-asserted-fresh v1

EXPIRING='{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
TRANSITION_ONLY='{"Rules":[{"ID":"to-glacier","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionTransitions":[{"NoncurrentDays":30,"StorageClass":"GLACIER"}]}]}'

make_correct() {
  awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null \
    && awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration "$EXPIRING" >/dev/null \
    && awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
}
version_count() { awsl s3api list-object-versions --bucket "$BUCKET" --prefix "tofu-records/smoke-asserted/" --query 'length(Versions || `[]`)' --output text; }

# refused_by_name <arm> <setting> <output>: the run failed, the headline
# names the setting, the paragraph names the bucket, and nothing was applied.
refused_by_name() {
  local arm="$1" setting="$2" out="$3"
  grep -q "fails its $setting assertion" <<< "$out" \
    || fail "wrongbucket" "[$arm] the run did not refuse by name - no line says the bucket fails its $setting assertion: $out"
  grep -q "$BUCKET" <<< "$out" || fail "wrongbucket" "[$arm] the refusal never names the bucket: $out"
  grep -qE 'Apply complete|Resources: [0-9]+ added' <<< "$out" && fail "wrongbucket" "[$arm] the run reported applying something past the refusal: $out"
  grep -E "fails its $setting assertion|Bucket \"$BUCKET\"" <<< "$out" | head -2 | evidence
}

step "the claim"
explain \
  "A record in this bucket can be the only copy of what it says: a" \
  "record-backed resource carries no marker and cannot be imported under" \
  "a live block. So three of the bucket's settings are asserted, not" \
  "recommended - versioning, a lifecycle rule that EXPIRES NONCURRENT" \
  "VERSIONS (its window is how long a record destroyed by mistake can" \
  "still be brought back), and public-access block. Encryption at rest is" \
  "deliberately not one of them: S3 encrypts every object by default, so" \
  "that check could not fail. The assertions run before an apply changes" \
  "anything and on an estate's first contact with the bucket. They do" \
  "not run on every plan, and step 3 shows what that costs."

step "1. a correct bucket, and an apply that goes through"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "wrongbucket" "could not create the bucket"
make_correct || fail "wrongbucket" "could not configure the bucket"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK/est" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "wrongbucket" "init failed"
OUT="$(cd "$SMOKE_WORK/est" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "wrongbucket" "the apply against a correct bucket failed: $OUT"
grep -qE 'Resources: 1 added' <<< "$OUT" || fail "wrongbucket" "nothing applied against a correct bucket: $OUT"
awsl s3api get-bucket-versioning --bucket "$BUCKET" --query Status --output text | sed 's/^/versioning: /' | evidence
awsl s3api get-bucket-lifecycle-configuration --bucket "$BUCKET" --query 'Rules[0].NoncurrentVersionExpiration.NoncurrentDays' --output text | sed 's/^/noncurrent versions expire after (days): /' | evidence
proof "with all three in place the apply is not interrupted: the assertion costs a correct bucket nothing."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - an arm that corrupts nothing must not read as a refusal"
  explain \
    "The arms below each break one setting and require a refusal. If the" \
    "check for 'refused by name' could pass on a run that was never" \
    "refused, all four would be scenery. So this control runs the first" \
    "arm WITHOUT suspending versioning and requires the check to notice" \
    "that the apply simply went through."
  write_estate "$SMOKE_WORK/est" smoke-asserted v-break
  cmd "choudoufu apply -auto-approve   # versioning left Enabled"
  BOUT="$(cd "$SMOKE_WORK/est" && chdf apply -auto-approve -input=false -no-color 2>&1)" || true
  if grep -q "fails its versioning assertion" <<< "$BOUT"; then
    fail "wrongbucket" "a correct bucket was refused: $BOUT"
  fi
  grep -qE 'Apply complete' <<< "$BOUT" || fail "wrongbucket" "the control apply neither refused nor completed: $BOUT"
  grep -E 'Apply complete' <<< "$BOUT" | head -1 | evidence
  proof "caught - with nothing corrupted there is no refusal line to find, so the arms' check cannot pass without one."
  exit 0
fi

# one_arm <name> <setting> <corrupt-command...>
ARM_N=0
one_arm() {
  local name="$1" setting="$2"; shift 2
  ARM_N=$((ARM_N+1))
  "$@" >/dev/null || fail "wrongbucket" "[$name] could not corrupt the bucket"
  write_estate "$SMOKE_WORK/est" smoke-asserted "arm-$ARM_N"
  local before after out
  before="$(version_count)"
  cmd "choudoufu apply -auto-approve   # $name"
  out="$(cd "$SMOKE_WORK/est" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    && fail "wrongbucket" "[$name] the apply SUCCEEDED against a bucket that fails its $setting assertion: $out"
  refused_by_name "$name" "$setting" "$out"
  after="$(version_count)"
  [ "$before" = "$after" ] || fail "wrongbucket" "[$name] the refused apply still wrote to the record store: $before object version(s) before, $after after"
  make_correct || fail "wrongbucket" "[$name] could not restore the bucket"
}

step "2. four ways to be wrong, each refused by name with nothing applied"
explain \
  "Each arm breaks one setting with the AWS CLI, changes the" \
  "configuration so there is something to apply, and applies. The run" \
  "must fail, its headline must name the setting, and the record store" \
  "must hold exactly the object versions it held before."
one_arm "versioning suspended" versioning \
  awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Suspended
one_arm "no lifecycle configuration" lifecycle \
  awsl s3api delete-bucket-lifecycle --bucket "$BUCKET"
one_arm "a lifecycle that exists and expires nothing" lifecycle \
  awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration "$TRANSITION_ONLY"
one_arm "no public-access block" public_access_block \
  awsl s3api delete-public-access-block --bucket "$BUCKET"
proof "four refusals, four settings named, and the record store untouched by every one of them. The third arm is the point of the lifecycle assertion: a policy that merely exists satisfies a checkbox and keeps every version forever."

step "3. what not asking on every plan costs"
explain \
  "The assertions are facts about the bucket, and reading them on every" \
  "plan would be three requests and three permissions paid for nothing" \
  "on almost every run. The price: a plan against a bucket that has" \
  "drifted says nothing, and the operator hears it at apply time."
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Suspended >/dev/null
cmd "choudoufu plan   # versioning is Suspended"
P_OUT="$(cd "$SMOKE_WORK/est" && chdf plan -input=false -no-color 2>&1)" \
  || fail "wrongbucket" "the plan failed; the ruling is that an ordinary plan does not assert the bucket: $P_OUT"
grep -q "assertion" <<< "$P_OUT" && fail "wrongbucket" "the plan asserted the bucket: $P_OUT"
grep -E 'Plan:|No changes' <<< "$P_OUT" | head -1 | evidence
proof "the plan went through without a word about the bucket. Step 2's first arm is what the apply that follows it says."

step "4. first contact: a new estate's very first plan refuses, and leaves nothing behind"
explain \
  "The one run a wrong bucket costs nothing to walk away from is an" \
  "estate's first, so that run asserts whatever command it is. The" \
  "store sentinel is how a run knows it is first - and a refusal takes" \
  "the sentinel back out, or the second plan would sail past the bucket" \
  "the first one refused."
( cd "$SMOKE_WORK/fresh" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "wrongbucket" "init failed in the fresh estate"
for attempt in 1 2; do
  cmd "choudoufu plan   # smoke-asserted-fresh, attempt $attempt, versioning still Suspended"
  F_OUT="$(cd "$SMOKE_WORK/fresh" && chdf plan -input=false -no-color 2>&1)" \
    && fail "wrongbucket" "[first contact, attempt $attempt] a brand-new estate planned against a bucket with versioning suspended: $F_OUT"
  grep -q "fails its versioning assertion" <<< "$F_OUT" \
    || fail "wrongbucket" "[first contact, attempt $attempt] the run failed without naming the versioning assertion: $F_OUT"
  LEFT="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "tofu-records/smoke-asserted-fresh/" --query 'length(Contents || `[]`)' --output text)"
  [ "$LEFT" = "0" ] || fail "wrongbucket" "[first contact, attempt $attempt] the refused run left $LEFT object(s) under the new estate's prefix"
done
grep -E "fails its versioning assertion" <<< "$F_OUT" | head -1 | evidence
make_correct || fail "wrongbucket" "could not restore the bucket"
cmd "choudoufu plan   # smoke-asserted-fresh, bucket fixed"
F_OUT="$(cd "$SMOKE_WORK/fresh" && chdf plan -input=false -no-color 2>&1)" || fail "wrongbucket" "the fresh estate's plan failed against a corrected bucket: $F_OUT"
grep -E 'Plan:' <<< "$F_OUT" | head -1 | evidence
proof "refused twice running, nothing left under its prefix either time, and accepted as soon as the bucket was right."

step "5. teardown"
cmd "choudoufu apply -destroy -auto-approve"
( cd "$SMOKE_WORK/est" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "wrongbucket" "teardown failed"
proof "gone."

echo "  What you watched: a correct bucket cost nothing, four wrong ones were"
echo "  each refused by the name of the setting with the record store"
echo "  untouched, a plan said nothing because it does not ask, and a new"
echo "  estate could not take its first step into a bucket that would not"
echo "  keep its records."
