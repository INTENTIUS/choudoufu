# cas-holds-under-every-sse-flavour
# CLAIM 33 (aws) - Compare-and-swap holds under every SSE flavour (REAL AWS, maintainer-run). ~6 min.

SMOKE_WORK="$SMOKE_WORKROOT/sseflavours"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
FLAVOURS="sse-s3 sse-kms-aws sse-kms-cmk dsse-kms"

step "the claim"
explain \
  "choudoufu asserts nothing about how a record store bucket is" \
  "encrypted at rest, because S3 encrypts everything by default and that" \
  "check could not fail. What it owes instead is that EVERY flavour" \
  "works: SSE-S3, SSE-KMS under the AWS-managed key, SSE-KMS under a" \
  "customer managed key, and DSSE-KMS. The reason it should: the ETag is" \
  "opaque here. Under SSE-KMS an ETag stops being the content's MD5, but" \
  "it is still a valid entity tag for If-Match, and the store hands it" \
  "back untouched. That is an argument. This is the measurement."

step "0. this one runs on real AWS, and says so"
explain \
  "An emulator's object store does not reproduce the ETag semantics that" \
  "are the whole subject, so there is no honest local form of this claim." \
  "It creates four buckets and one KMS key in YOUR account and removes" \
  "them at the end. The key cannot be deleted at once: AWS makes it wait" \
  "seven days, during which it costs nothing."
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "sseflavours" "this scenario runs against real AWS and creates four S3 buckets and one KMS key in the account your credentials name. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
command -v go >/dev/null 2>&1 || fail "sseflavours" "this scenario runs a Go test against the real buckets; Go is not installed"
unset AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3
export AWS_REGION="${AWS_REGION:-us-east-2}"
ACCOUNT="$(aws sts get-caller-identity --query Account --output text)" || fail "sseflavours" "no usable AWS credentials"
SUFFIX="${ACCOUNT: -4}-$(date +%H%M%S)"
echo "account ...${ACCOUNT: -4}, region $AWS_REGION" | evidence

# The binary under test. BREAK=1 swaps in one that interprets the ETag.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a store that reads the ETag as a content hash must be caught"
  explain \
    "The red arm this claim has: the one plausible way to break the" \
    "opaque-ETag property is to stop treating it as opaque. This binary" \
    "checks, after every write, that the ETag S3 returned is the MD5 of" \
    "what it sent - a 'sanity check' that is true under SSE-S3 and false" \
    "under every KMS flavour. Built with go build -overlay."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "sseflavours" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION"
  SRC="$ROOT/internal/live/staterecord/s3.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/s3.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\treturn aws.ToString(out.ETag), nil\n}\n"
assert src.count(old) >= 1, "the break patch no longer matches S3Store.PutIfVersion"
new = ("\tif sum := md5.Sum(payload); aws.ToString(out.ETag) != `\"`+hex.EncodeToString(sum[:])+`\"` {\n"
       "\t\treturn \"\", fmt.Errorf(\"staterecord: s3: BREAK: the ETag %s is not the MD5 of what was written to %q\", aws.ToString(out.ETag), key)\n"
       "\t}\n" + old)
src = src.replace(old, new, 1)
src = src.replace('import (\n', 'import (\n\t"crypto/md5"\n\t"encoding/hex"\n', 1)
open(sys.argv[2], "w").write(src)
PYEOF
  [ -s "$SMOKE_WORK/break/s3.go" ] || fail "sseflavours" "the break patch did not apply, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/s3.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # the ETag must equal the payload's MD5"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "sseflavours" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi

bucket_of() { echo "chdf-smoke-$1-$SUFFIX"; }
KEY_ID=""
# This runs from an EXIT trap under smoke.sh's `set -euo pipefail`, where a
# single failed call ends the trap and skips everything after it with
# nothing printed (#1378). errexit and nounset go off first, each step
# prints its own line naming the resource, and the key is reached whatever
# the four buckets did.
sse_empty_bucket() { # <bucket>: every version and delete marker, or a line saying why not
  local b="$1" del n i=0
  while [ "$i" -lt 500 ]; do
    i=$((i+1))
    del="$(aws s3api list-object-versions --bucket "$b" --max-items 500 --query '{Objects: [Versions, DeleteMarkers][] | [?@ != `null`] | [].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null)"
    if [ -z "$del" ]; then
      echo "  COULD NOT LIST the object versions of bucket $b - empty it by hand" >&2
      return 1
    fi
    n="$(python3 -c 'import json,sys; print(len((json.load(sys.stdin) or {}).get("Objects") or []))' <<< "$del" 2>/dev/null)"
    if [ -z "$n" ]; then
      echo "  COULD NOT READ the object-version listing of bucket $b - empty it by hand" >&2
      return 1
    fi
    [ "$n" = "0" ] && return 0
    if ! aws s3api delete-objects --bucket "$b" --delete "$del" >/dev/null 2>&1; then
      echo "  COULD NOT DELETE $n object version(s) from bucket $b - empty it by hand" >&2
      return 1
    fi
  done
  echo "  COULD NOT EMPTY bucket $b in $i rounds - empty it by hand" >&2
  return 1
}
aws_teardown() {
  set +e
  set +u
  local f b
  for f in $FLAVOURS; do
    b="$(bucket_of "$f")"
    if ! aws s3api head-bucket --bucket "$b" >/dev/null 2>&1; then
      echo "  no bucket $b to remove"
      continue
    fi
    sse_empty_bucket "$b"
    aws s3api delete-bucket --bucket "$b" >/dev/null 2>&1 && echo "  removed bucket $b" || echo "  COULD NOT REMOVE bucket $b - remove it by hand" >&2
  done
  if [ -n "$KEY_ID" ]; then
    aws kms schedule-key-deletion --key-id "$KEY_ID" --pending-window-in-days 7 >/dev/null 2>&1 \
      && echo "  scheduled KMS key $KEY_ID for deletion in 7 days (the minimum; it costs nothing while pending)" \
      || echo "  COULD NOT SCHEDULE deletion of KMS key $KEY_ID - do it by hand" >&2
  fi
}
trap 'set +e; set +u; aws_teardown; cleanup' EXIT

step "1. one customer managed key, and a bucket per flavour"
# SMOKE_KMS_KEY_ARN reuses a key you already have, and then this scenario
# leaves it alone. Without it a key is created and scheduled for deletion at
# the end - and every run would leave another one pending for seven days.
if [ -n "${SMOKE_KMS_KEY_ARN:-}" ]; then
  KEY_ARN="$SMOKE_KMS_KEY_ARN"
  aws kms describe-key --key-id "$KEY_ARN" --query KeyMetadata.KeyState --output text | grep -qx Enabled \
    || fail "sseflavours" "SMOKE_KMS_KEY_ARN=$KEY_ARN is not an enabled key"
  echo "reusing $KEY_ARN, which this scenario will not delete" | evidence
else
  KEY_ID="$(aws kms create-key --description "choudoufu smoke claim 33, safe to delete" --tags TagKey=choudoufu-smoke,TagValue=claim-33 --query KeyMetadata.KeyId --output text)" \
    || fail "sseflavours" "could not create the KMS key"
  KEY_ARN="$(aws kms describe-key --key-id "$KEY_ID" --query KeyMetadata.Arn --output text)"
fi
sse_config() {
  case "$1" in
    sse-s3)      echo '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}' ;;
    sse-kms-aws) echo '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms"},"BucketKeyEnabled":true}]}' ;;
    sse-kms-cmk) echo '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms","KMSMasterKeyID":"'"$KEY_ARN"'"},"BucketKeyEnabled":true}]}' ;;
    dsse-kms)    echo '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms:dsse","KMSMasterKeyID":"'"$KEY_ARN"'"}}]}' ;;
  esac
}
PAIRS=""
for f in $FLAVOURS; do
  b="$(bucket_of "$f")"
  aws s3api create-bucket --bucket "$b" --create-bucket-configuration "LocationConstraint=$AWS_REGION" >/dev/null || fail "sseflavours" "could not create $b"
  aws s3api put-bucket-encryption --bucket "$b" --server-side-encryption-configuration "$(sse_config "$f")" || fail "sseflavours" "could not set $f on $b"
  # The bucket contract (claim 29): an apply asserts these before it writes.
  aws s3api put-bucket-versioning --bucket "$b" --versioning-configuration Status=Enabled
  aws s3api put-bucket-lifecycle-configuration --bucket "$b" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":1}}]}' >/dev/null
  aws s3api put-public-access-block --bucket "$b" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
  # The key goes with the flavours that were configured with one, so the test
  # can compare what S3 reports against what was asked for rather than against
  # an alias HeadObject never returns (#1379).
  case "$f" in
    sse-kms-cmk|dsse-kms) PAIRS="${PAIRS:+$PAIRS,}$f=$b=$KEY_ARN" ;;
    *)                    PAIRS="${PAIRS:+$PAIRS,}$f=$b" ;;
  esac
  echo "$f -> $b  ($(aws s3api get-bucket-encryption --bucket "$b" --query 'ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm' --output text))" | evidence
done
proof "four buckets, four default-encryption flavours, read back from S3."

if [ "${BREAK:-0}" != "1" ]; then
  step "2. the conditional-write suite, against each"
  explain \
    "The same conformance suite every record store backend is held to:" \
    "If-None-Match creates, If-Match updates and deletes, a 412 becomes a" \
    "version conflict naming both versions. Before it runs, each flavour" \
    "is checked for what it is: the object's ServerSideEncryption, and" \
    "whether its ETag equals the payload's MD5. It does under SSE-S3 and" \
    "does not under any KMS flavour, so the KMS runs are known to have" \
    "exercised an ETag that is not a content hash."
  cmd "CHOUDOUFU_SSE_BUCKETS=... go test ./internal/live/staterecord/ -run TestS3StoreCASUnderEverySSEFlavour -v"
  T_OUT="$(cd "$ROOT" && CHOUDOUFU_SSE_BUCKETS="$PAIRS" env -u PWD go test ./internal/live/staterecord/ -run TestS3StoreCASUnderEverySSEFlavour -count=1 -v 2>&1)" && T_RC=0 || T_RC=$?
  grep -E 'ServerSideEncryption=' <<< "$T_OUT" | sed 's/^ *s3_sse_live_test.go:[0-9]*: //' | evidence
  grep -q -- "--- SKIP: TestS3StoreCASUnderEverySSEFlavour" <<< "$T_OUT" && fail "sseflavours" "the test SKIPPED, so nothing was measured: $T_OUT"
  for f in $FLAVOURS; do
    grep -qE -- "--- PASS: TestS3StoreCASUnderEverySSEFlavour/$f " <<< "$T_OUT" \
      || fail "sseflavours" "the conditional-write suite did not pass under $f: $(grep -E -- '--- FAIL|s3_sse_live_test|conformance_test' <<< "$T_OUT" | head -8)"
    echo "$f: $(grep -cE -- "--- PASS: TestS3StoreCASUnderEverySSEFlavour/$f/" <<< "$T_OUT") conformance case(s) passed" | evidence
  done
  [ "$T_RC" = "0" ] || fail "sseflavours" "go test exited $T_RC: $(tail -5 <<< "$T_OUT")"
  proof "identical behaviour under all four, including the three whose ETag is not an MD5."
fi

step "3. an estate's whole life, under each"
explain \
  "The suite is the store alone. This is choudoufu end to end: create," \
  "update under If-Match, plan empty, destroy - with the counts checked" \
  "at every step, because a run that says complete is not a run that" \
  "did everything."
BROKE=""; HELD=""
for f in $FLAVOURS; do
  b="$(bucket_of "$f")"; d="$SMOKE_WORK/$f"; mkdir -p "$d"
  write_estate() { cat > "$d/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-sse"

    record_store "s3" {
      bucket = "$b"
      region = "$AWS_REGION"
    }
  }
}

resource "terraform_data" "effect" {
  for_each = toset(["one", "two"])
  input    = "\${each.key}-$1"
}
TFEOF
  }
  write_estate v1
  ( cd "$d" && "$RUN_BIN" init -input=false -no-color >/dev/null 2>&1 ) || fail "sseflavours" "[$f] init failed"
  A_OUT="$(cd "$d" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" && A_RC=0 || A_RC=$?
  if [ "${BREAK:-0}" = "1" ]; then
    if [ "$A_RC" = "0" ]; then
      HELD="$HELD $f"
      echo "$f: apply succeeded" | evidence
    else
      # The failure has to be THE failure. An apply that broke for any other
      # reason - a throttle, a permission - would make this arm pass while
      # showing nothing about ETags.
      WHY="$(tr '\n' ' ' <<< "$A_OUT" | sed 's/│/ /g' | tr -s ' ' | grep -oE 'BREAK: the ETag [^ ]+ is not the MD5' | head -1)"
      [ -n "$WHY" ] || fail "sseflavours" "[$f] the broken binary's apply failed, but not on its ETag check, so this control shows nothing: $A_OUT"
      BROKE="$BROKE $f"
      echo "$f: apply FAILED - $WHY" | evidence
    fi
    continue
  fi
  [ "$A_RC" = "0" ] && grep -q "Resources: 2 added, 0 changed, 0 destroyed" <<< "$A_OUT" || fail "sseflavours" "[$f] create: $A_OUT"
  write_estate v2
  U_OUT="$(cd "$d" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" || fail "sseflavours" "[$f] the update under If-Match failed: $U_OUT"
  grep -q "Resources: 0 added, 2 changed, 0 destroyed" <<< "$U_OUT" || fail "sseflavours" "[$f] update: $U_OUT"
  rm -f "$d/.terraform/choudoufu-cache.tfstate"
  P_OUT="$(cd "$d" && "$RUN_BIN" plan -input=false -no-color 2>&1)" || fail "sseflavours" "[$f] plan: $P_OUT"
  grep -q "No changes." <<< "$P_OUT" || fail "sseflavours" "[$f] the replan from the records alone was not empty: $P_OUT"
  D_OUT="$(cd "$d" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "sseflavours" "[$f] destroy: $D_OUT"
  destroyed_exactly sseflavours 2 "$D_OUT"
  LEFT="$(aws s3api list-objects-v2 --bucket "$b" --prefix tofu-records/smoke-sse/terraform_data/ --query 'length(Contents || `[]`)' --output text)"
  [ "$LEFT" = "0" ] || fail "sseflavours" "[$f] $LEFT record(s) left after the destroy"
  echo "$f: 2 added, 2 changed under If-Match, replan empty, 2 destroyed, no record left" | evidence
done

if [ "${BREAK:-0}" = "1" ]; then
  [ "$(echo $HELD)" = "sse-s3" ] && [ "$(echo $BROKE)" = "sse-kms-aws sse-kms-cmk dsse-kms" ] \
    || fail "sseflavours" "a store that reads the ETag as an MD5 should work under SSE-S3 alone. It worked under [$HELD ] and failed under [$BROKE ], so this control does not show what it claims to"
  proof "caught - the binary that interprets the ETag works under SSE-S3 and breaks under all three KMS flavours. Treating the ETag as opaque is what makes the other three work, and the ordinary run is what shows they do."
  exit 0
fi
proof "every flavour carried a full lifecycle, counts checked."

step "4. teardown"
proof "the exit handler empties and removes the buckets, and schedules the key for deletion unless SMOKE_KMS_KEY_ARN supplied it. Its lines follow."

echo "  What you watched: the record store's whole conditional-write contract"
echo "  and an estate's whole life, under four kinds of encryption at rest,"
echo "  three of which hand back an ETag that is not a hash of anything."
