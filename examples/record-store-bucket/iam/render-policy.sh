#!/usr/bin/env bash
# The IAM policy for one estate's role, against one record store bucket.
#
#   render-policy.sh <estate> <bucket> [--kms <key-arn>] [--reads-outputs-of <other-estate>]...
#
# This script is the single source of that policy (GitHub issue #1342). The
# documentation's IAM page shows its output and a test holds the two together,
# and anything else in this repository that needs the policy runs this. A
# second, hand-written copy is how the half-right version gets published.
#
# Every statement below is the shape MEASURED against real AWS, not the shape
# that reads naturally. The measurements are in #1342. Three of them decide
# what is here, and each is the opposite of what the first draft assumed:
#
#   - s3:ExistingObjectTag does not work on s3:DeleteObject at all, and a
#     PutObject carrying If-Match is also authorized as s3:GetObject WITHOUT
#     the object's tags. So an ALLOW conditioned on the existing tag can create
#     records and can never update or delete one. Reads and deletes are
#     therefore allowed by prefix, and the tag does its work as a DENY.
#   - A PutObject that carries tags needs s3:PutObjectTagging as well, and
#     every write choudoufu makes carries tags.
#   - The ListBucket statement is what makes a key that does not exist answer
#     404 instead of AccessDenied. choudoufu reads keys that do not exist yet
#     for every new resource, so that statement is not only for listing.
set -euo pipefail

usage() { sed -n '2,4p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
[ $# -ge 2 ] || usage
estate="$1"; bucket="$2"; shift 2
kms=""; others=()
while [ $# -gt 0 ]; do
  case "$1" in
    --kms) kms="${2:?--kms needs a key ARN}"; shift 2 ;;
    --reads-outputs-of) others+=("${2:?--reads-outputs-of needs an estate name}"); shift 2 ;;
    *) usage ;;
  esac
done
# An estate name is ^[a-z][a-z0-9-]{0,127}$ (markers.ValidEstateName). Refusing
# anything else here keeps a stray "*" or "/" out of a Resource ARN.
for name in "$estate" ${others[@]+"${others[@]}"}; do
  [[ "$name" =~ ^[a-z][a-z0-9-]{0,127}$ ]] || { echo "not an estate name: $name" >&2; exit 2; }
done

others_json="$(printf '%s\n' ${others[@]+"${others[@]}"} | jq -R . | jq -s 'map(select(. != ""))')"

jq -n --arg estate "$estate" --arg bucket "$bucket" --arg kms "$kms" --argjson others "$others_json" '
  ("arn:aws:s3:::" + $bucket) as $b
  # Every prefix ends in "/". S3 matches a prefix as a plain string, so
  # "tofu-records/prod" would also be "tofu-records/prod-eu" (#1335), and for a
  # LIST, a write and a delete this prefix is the whole defence.
  | ["tofu-records/", "tofu-hints/", "tofu-outputs/"] as $roots
  | ($roots | map(. + $estate + "/")) as $own
  | ($others | map("tofu-outputs/" + . + "/")) as $theirs
  | {
      Version: "2012-10-17",
      Statement: ([
        {
          Sid: "ListOwnNamespaces",
          Effect: "Allow",
          Action: "s3:ListBucket",
          Resource: $b,
          Condition: { StringLike: { "s3:prefix": (($own + $theirs) | map(. + "*")) } }
        },
        {
          Sid: "ReadAndDeleteByPrefix",
          Effect: "Allow",
          Action: ["s3:GetObject", "s3:DeleteObject"],
          Resource: ($own | map($b + "/" + . + "*"))
        },
        {
          Sid: "WriteOnlyObjectsTaggedAsThisEstate",
          Effect: "Allow",
          Action: ["s3:PutObject", "s3:PutObjectTagging"],
          Resource: ($own | map($b + "/" + . + "*")),
          Condition: { StringEquals: { "s3:RequestObjectTag/tofu-estate": $estate } }
        }
      ]
      + (if ($theirs | length) > 0 then [{
          Sid: "ReadDeclaredDependenciesOutputs",
          Effect: "Allow",
          Action: "s3:GetObject",
          Resource: ($theirs | map($b + "/" + . + "*"))
        }] else [] end)
      + [
        {
          Sid: "DenyReadingAnotherEstatesObjects",
          Effect: "Deny",
          Action: ["s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectTagging"],
          Resource: ($b + "/*"),
          Condition: {
            StringNotEquals: { "s3:ExistingObjectTag/tofu-estate": ([$estate] + $others) },
            Null: { "s3:ExistingObjectTag/tofu-estate": "false" }
          }
        },
        {
          Sid: "ReadTheBucketsAssertedSettings",
          Effect: "Allow",
          Action: ["s3:GetBucketVersioning", "s3:GetLifecycleConfiguration", "s3:GetBucketPublicAccessBlock"],
          Resource: $b
        }
      ]
      + (if $kms != "" then [{
          Sid: "UseTheBucketsKey",
          Effect: "Allow",
          Action: ["kms:Decrypt", "kms:GenerateDataKey"],
          Resource: $kms
        }] else [] end))
    }'
