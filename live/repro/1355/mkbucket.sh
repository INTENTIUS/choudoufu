#!/usr/bin/env bash
# mkbucket.sh <endpoint> <bucket>: a bucket meeting the record-store contract.
set -euo pipefail
EP="$1" B="$2"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
a() { aws --endpoint-url "$EP" "$@"; }
a s3api create-bucket --bucket "$B" >/dev/null
a s3api put-bucket-versioning --bucket "$B" --versioning-configuration Status=Enabled
a s3api put-bucket-lifecycle-configuration --bucket "$B" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
a s3api put-public-access-block --bucket "$B" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
