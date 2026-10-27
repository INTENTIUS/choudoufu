#!/usr/bin/env bash
# `just test-sse`: TestS3StoreCASUnderEverySSEFlavour against real S3, one
# bucket per server-side encryption flavour (GitHub issue #2288). REAL AWS,
# maintainer-run, and only when pkg/staterecord/s3.go changes (ruling Q3 on
# #2288). It refuses to start without SMOKE_REAL_AWS=1.
#
# An emulator's object store does not reproduce the ETag semantics the test
# is about, so there is no local form of it. Until #2288 the smoke scenario
# cas-holds-under-every-sse-flavour made these buckets and ran the test as
# its step 2; this is that setup on its own.
#
# What it does, in the account your credentials name (AWS_REGION, default
# us-east-2):
#   1. creates one customer managed KMS key, or borrows SMOKE_KMS_KEY_ARN
#      and leaves it alone;
#   2. creates four buckets, one each for SSE-S3, SSE-KMS under the
#      AWS-managed key, SSE-KMS under the customer managed key and DSSE-KMS,
#      each with versioning, a noncurrent-version lifecycle and the
#      public-access block the bucket contract asserts;
#   3. runs `go test ./pkg/staterecord -run '^TestS3StoreCASUnderEverySSEFlavour$' -v`
#      with CHOUDOUFU_SSE_BUCKETS naming them, and keeps the log;
#   4. on every exit, empties and deletes the four buckets and schedules the
#      key it created for deletion in seven days (the minimum; a pending key
#      costs nothing).
#
# Read the verdict line at the end, then record the log with
# `go run ./tools/gotest-record -log <log>`.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] || {
  echo "test-sse: this runs against real AWS and creates four S3 buckets and a KMS key in the account your credentials name. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it." >&2
  exit 1
}
for tool in aws go python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "test-sse: $tool is not installed" >&2; exit 1; }
done
unset AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3
export AWS_REGION="${AWS_REGION:-us-east-2}"
ACCOUNT="$(aws sts get-caller-identity --query Account --output text)" || { echo "test-sse: no usable AWS credentials" >&2; exit 1; }
SUFFIX="${ACCOUNT: -4}-$(date +%H%M%S)"
FLAVOURS="sse-s3 sse-kms-aws sse-kms-cmk dsse-kms"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/choudoufu-test-sse.XXXXXX")"
LOG="${TEST_SSE_LOG:-$WORK/test-sse.log}"
echo "test-sse: account ...${ACCOUNT: -4}, region $AWS_REGION"

bucket_of() { echo "chdf-test-sse-$1-$SUFFIX"; }
KEY_ID=""
CREATED_BUCKETS=""

empty_bucket() { # <bucket>: every version and delete marker, or a line saying why not
  local b="$1" del n i=0
  while [ "$i" -lt 500 ]; do
    i=$((i+1))
    del="$(aws s3api list-object-versions --bucket "$b" --max-items 500 --query '{Objects: [Versions, DeleteMarkers][] | [?@ != `null`] | [].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null)" \
      || { echo "  COULD NOT LIST the object versions of bucket $b - empty it by hand" >&2; return 1; }
    n="$(python3 -c 'import json,sys; print(len((json.load(sys.stdin) or {}).get("Objects") or []))' <<< "$del" 2>/dev/null)" \
      || { echo "  COULD NOT READ the object-version listing of bucket $b - empty it by hand" >&2; return 1; }
    [ "$n" = "0" ] && return 0
    aws s3api delete-objects --bucket "$b" --delete "$del" >/dev/null 2>&1 \
      || { echo "  COULD NOT DELETE $n object version(s) from bucket $b - empty it by hand" >&2; return 1; }
  done
  echo "  COULD NOT EMPTY bucket $b in $i rounds - empty it by hand" >&2
  return 1
}

# Every step is attempted and says what it did: errexit goes off first, so
# one failed call does not skip the resources after it.
teardown() {
  set +e
  local b
  for b in $CREATED_BUCKETS; do
    empty_bucket "$b"
    aws s3api delete-bucket --bucket "$b" >/dev/null 2>&1 && echo "  removed bucket $b" || echo "  COULD NOT REMOVE bucket $b - remove it by hand" >&2
  done
  if [ -n "$KEY_ID" ]; then
    aws kms schedule-key-deletion --key-id "$KEY_ID" --pending-window-in-days 7 >/dev/null 2>&1 \
      && echo "  scheduled KMS key $KEY_ID for deletion in 7 days" \
      || echo "  COULD NOT SCHEDULE deletion of KMS key $KEY_ID - do it by hand" >&2
  fi
}
trap teardown EXIT

if [ -n "${SMOKE_KMS_KEY_ARN:-}" ]; then
  KEY_ARN="$SMOKE_KMS_KEY_ARN"
  aws kms describe-key --key-id "$KEY_ARN" --query KeyMetadata.KeyState --output text | grep -qx Enabled \
    || { echo "test-sse: SMOKE_KMS_KEY_ARN=$KEY_ARN is not an enabled key" >&2; exit 1; }
  echo "test-sse: borrowing $KEY_ARN, which this recipe will not delete"
else
  KEY_ID="$(aws kms create-key --description "choudoufu test-sse, safe to delete" --tags TagKey=choudoufu-test,TagValue=test-sse --query KeyMetadata.KeyId --output text)"
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
  aws s3api create-bucket --bucket "$b" --create-bucket-configuration "LocationConstraint=$AWS_REGION" >/dev/null
  CREATED_BUCKETS="$CREATED_BUCKETS $b"
  aws s3api put-bucket-encryption --bucket "$b" --server-side-encryption-configuration "$(sse_config "$f")"
  aws s3api put-bucket-versioning --bucket "$b" --versioning-configuration Status=Enabled
  aws s3api put-bucket-lifecycle-configuration --bucket "$b" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":1}}]}' >/dev/null
  aws s3api put-public-access-block --bucket "$b" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
  # The key goes with the flavours configured with one, so the test compares
  # what S3 reports against what was asked for (#1379).
  case "$f" in
    sse-kms-cmk|dsse-kms) PAIRS="${PAIRS:+$PAIRS,}$f=$b=$KEY_ARN" ;;
    *)                    PAIRS="${PAIRS:+$PAIRS,}$f=$b" ;;
  esac
  echo "test-sse: $f -> $b"
done

echo "test-sse: go test ./pkg/staterecord -run '^TestS3StoreCASUnderEverySSEFlavour$' -v   (log: $LOG)"
RC=0
( cd "$ROOT" && CHOUDOUFU_SSE_BUCKETS="$PAIRS" env -u PWD go test ./pkg/staterecord -run '^TestS3StoreCASUnderEverySSEFlavour$' -count=1 -v ) > "$LOG" 2>&1 || RC=$?

MISSING=""
for f in $FLAVOURS; do
  grep -qE -- "--- PASS: TestS3StoreCASUnderEverySSEFlavour/$f " "$LOG" || MISSING="$MISSING $f"
done
if [ "$RC" = "0" ] && [ -z "$MISSING" ] && grep -qE '^--- PASS: TestS3StoreCASUnderEverySSEFlavour ' "$LOG"; then
  echo "TEST-SSE GREEN: the conditional-write suite passed under all four flavours. Record it: go run ./tools/gotest-record -log $LOG"
else
  echo "TEST-SSE RED: go test exited $RC; no pass under:${MISSING:- (none missing, see the log)}. Log: $LOG" >&2
  exit 1
fi
