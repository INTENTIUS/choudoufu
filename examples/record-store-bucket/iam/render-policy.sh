#!/usr/bin/env bash
# The IAM policy for one estate's role, against one record store bucket.
#
#   render-policy.sh <estate> <bucket> [--account <account-id>] [--kms <key-arn>]
#                    [--partition aws|aws-us-gov|aws-cn] [--key-prefix <prefix>]
#                    [--reads-outputs-of <other-estate>]...
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

usage() { sed -n '2,6p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
[ $# -ge 2 ] || usage
estate="$1"; bucket="$2"; shift 2
kms=""; account=""; partition="aws"; key_prefix=""; others=()
while [ $# -gt 0 ]; do
  case "$1" in
    --account) account="${2:?--account needs a 12-digit AWS account id}"; shift 2 ;;
    --kms) kms="${2:?--kms needs a key ARN}"; shift 2 ;;
    --partition) partition="${2:?--partition needs aws, aws-us-gov or aws-cn}"; shift 2 ;;
    --key-prefix) key_prefix="${2:?--key-prefix needs the key_prefix the record_store block sets}"; shift 2 ;;
    --reads-outputs-of) others+=("${2:?--reads-outputs-of needs an estate name}"); shift 2 ;;
    *) usage ;;
  esac
done
# An estate name is ^[a-z][a-z0-9-]{0,127}$ (markers.ValidEstateName). Refusing
# anything else here keeps a stray "*" or "/" out of a Resource ARN.
for name in "$estate" ${others[@]+"${others[@]}"}; do
  [[ "$name" =~ ^[a-z][a-z0-9-]{0,127}$ ]] || { echo "not an estate name: $name" >&2; exit 2; }
done
for name in ${others[@]+"${others[@]}"}; do
  [ "$name" != "$estate" ] || { echo "--reads-outputs-of names this estate itself: $name" >&2; exit 2; }
done
# The bucket and the key go into Resource ARNs as they are, so they are held
# to what they are supposed to be. Until GitHub issue #1381 only the estate
# names were: a bucket of "*" rendered a policy for every bucket, "b/*" and
# a pasted ARN rendered nonsense with exit 0, and "\${aws:username}" reached
# the policy as a live IAM policy variable.
[[ "$bucket" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ]] || { echo "not a bucket name: $bucket" >&2; exit 2; }
# The partition is the second field of every ARN this script writes, and it
# used to be the literal "aws" (GitHub issue #1381), so an estate in GovCloud
# or in China got a policy naming resources in the commercial partition, which
# match nothing and deny everything. Three names exist and none of them is
# derivable from the bucket, so it is a flag with the commercial default.
case "$partition" in
  aws|aws-us-gov|aws-cn) ;;
  *) echo "not an AWS partition: $partition (want aws, aws-us-gov or aws-cn)" >&2; exit 2 ;;
esac
if [ -n "$kms" ]; then
  # Held to the same three partitions rather than to "aws[a-z-]*", which
  # accepted "arn:awsevil:..." and rendered it into a Resource (#1381).
  [[ "$kms" =~ ^arn:(aws|aws-us-gov|aws-cn):kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-f-]{36}$ ]] || { echo "not a KMS key ARN: $kms" >&2; exit 2; }
  kms_partition="$(echo "$kms" | cut -d: -f2)"
  kms_region="$(echo "$kms" | cut -d: -f4)"
  [ "$kms_partition" = "$partition" ] || {
    echo "--kms names a key in the $kms_partition partition and --partition says $partition; a key and the bucket that uses it are in one partition, so one of the two is wrong: $kms" >&2
    exit 2
  }
fi
# The key_prefix an estate sets moves its RECORDS and nothing else: the hint
# and the root outputs keep their own namespaces. That is
# internal/live/projection.BucketNamespaces, which returns exactly
# [recordStoreKeyPrefix(rs, estate), HintKeyPrefix(estate),
# RootOutputKeyPrefix(estate)], and a Go test ties the three ARNs below to
# what that function returns for the same prefix and estate so the two cannot
# drift (GitHub issue #1381). Without the flag the renderer could not express
# such an estate at all and its first run was denied.
if [ -n "$key_prefix" ]; then
  # Held to what an S3 key may safely be built from, for the bucket name and
  # the estate name reasons: this string goes into a Resource ARN and into an
  # s3:prefix condition as it stands, so a "*" or a leading "/" here is the
  # whole defence gone. No empty segment, because
  # staterecord.NamespacePrefix would leave it in the key.
  [[ "$key_prefix" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9._-]+)*/?$ ]] || {
    echo "not a record_store key_prefix: $key_prefix" >&2; exit 2
  }
  case "$key_prefix" in
    *..*) echo "not a record_store key_prefix: $key_prefix" >&2; exit 2 ;;
  esac
  # staterecord.NamespacePrefix: exactly one trailing delimiter, whichever
  # way the configuration spelled it. Both spellings mean one namespace and
  # neither is a mistake, and the delimiter is what stops "team/prod" from
  # also meaning "team/prod-eu" (#1335).
  key_prefix="${key_prefix%/}/"
fi
# The account pins the bucket OWNER (GitHub issue #1381). A bucket name is
# global and a free name can be taken by anyone, so nothing about the name
# says which account the bucket is in. With --account, every Allow below also
# requires aws:ResourceAccount, and a bucket of the right name in someone
# else's account matches no statement in this policy at all.
#
# It is optional so that every invocation written before it keeps working, and
# a render without it says on stderr what is missing. The warning goes to
# stderr and never to stdout: stdout is the policy, compared byte for byte
# against the documentation page.
if [ -n "$account" ]; then
  [[ "$account" =~ ^[0-9]{12}$ ]] || { echo "not an AWS account id (want exactly 12 digits): $account" >&2; exit 2; }
else
  echo "warning: rendering without --account, so this policy does not pin the bucket owner." >&2
  echo "  A bucket name is global. If a bucket of this name is ever created in another" >&2
  echo "  account, every statement here matches it too, and the records hold secrets." >&2
  echo "  Add: --account <12-digit account id>" >&2
fi

# kms:ViaService is what keeps the grant below to what it is for: S3 asking
# the key on this role's behalf. Without it, kms:Decrypt on the key is
# kms:Decrypt on the key, and the role can decrypt any ciphertext made under
# it from anywhere. The value is the S3 endpoint in the key's own region, and
# in China the service principal ends in ".com.cn" rather than ".com".
#
# There is deliberately NO kms:EncryptionContext:aws:s3:arn condition beside
# it. S3 sets that context to the BUCKET ARN when S3 Bucket Keys are on and
# to the OBJECT ARN when they are off, so either literal is wrong for half of
# the buckets people will point this at, and a wrong one denies every write -
# including the first, which is where a new estate would meet it. The bucket
# this project stands up turns Bucket Keys on, but the renderer is used
# against buckets it did not create. GitHub issue #1381.
via_service=""
if [ -n "$kms" ]; then
  case "$partition" in
    aws-cn) via_service="s3.${kms_region}.amazonaws.com.cn" ;;
    *)      via_service="s3.${kms_region}.amazonaws.com" ;;
  esac
fi

others_json="$(printf '%s\n' ${others[@]+"${others[@]}"} | jq -R . | jq -s 'map(select(. != ""))')"

jq -n --arg estate "$estate" --arg bucket "$bucket" --arg kms "$kms" --arg account "$account" \
      --arg partition "$partition" --arg keyprefix "$key_prefix" --arg viaservice "$via_service" \
      --argjson others "$others_json" '
  ("arn:" + $partition + ":s3:::" + $bucket) as $b
  # Every prefix ends in "/". S3 matches a prefix as a plain string, so
  # "tofu-records/prod" would also be "tofu-records/prod-eu" (#1335), and for a
  # LIST, a write and a delete this prefix is the whole defence.
  #
  # The three are projection.BucketNamespaces in order: the records namespace,
  # which --key-prefix moves, then the hint and the root outputs, which it
  # does not.
  | [(if $keyprefix == "" then "tofu-records/" + $estate + "/" else $keyprefix end),
     "tofu-hints/" + $estate + "/",
     "tofu-outputs/" + $estate + "/"] as $own
  | ($others | map("tofu-outputs/" + . + "/")) as $theirs
  | ["s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectTagging", "s3:GetObjectVersionTagging", "s3:GetObjectAcl", "s3:GetObjectVersionAcl"] as $reads
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
        ({
          Sid: "DenyReadingAnotherEstatesObjects",
          Effect: "Deny",
          Action: $reads,
          Condition: {
            StringNotEquals: { "s3:ExistingObjectTag/tofu-estate": $estate },
            Null: { "s3:ExistingObjectTag/tofu-estate": "false" }
          }
        } + (if ($theirs | length) > 0
             then { NotResource: ($theirs | map($b + "/" + . + "*")) }
             else { Resource: ($b + "/*") } end))
      ]
      # A declared dependency opens the OUTPUTS of the other estate and nothing
      # else of it. Its tag is accepted only under its outputs prefix. It used
      # to be accepted bucket-wide, which left the records of that estate on
      # the prefix alone (#1381).
      + (if ($theirs | length) > 0 then [{
          Sid: "DenyReadingOtherTagsUnderDeclaredOutputs",
          Effect: "Deny",
          Action: $reads,
          Resource: ($theirs | map($b + "/" + . + "*")),
          Condition: {
            StringNotEquals: { "s3:ExistingObjectTag/tofu-estate": ([$estate] + $others) },
            Null: { "s3:ExistingObjectTag/tofu-estate": "false" }
          }
        }] else [] end)
      + [
        # Measured on real AWS (#1381): s3:RequestObjectTag constrains only the
        # tag being SENT, so a role whose prefix was widened by mistake could
        # PutObjectTagging an object of a neighbour as its own and then read it.
        # The tag is only a defence if it cannot be rewritten. This does not
        # reach a tagged PutObject over a foreign object: AWS does not evaluate
        # s3:ExistingObjectTag for PutObject, so overwrite and delete stay on
        # the prefix alone.
        {
          Sid: "DenyRelabellingAnotherEstatesObjects",
          Effect: "Deny",
          Action: ["s3:PutObjectTagging", "s3:DeleteObjectTagging", "s3:PutObjectVersionTagging", "s3:DeleteObjectVersionTagging"],
          Resource: ($b + "/*"),
          Condition: {
            StringNotEquals: { "s3:ExistingObjectTag/tofu-estate": $estate },
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
          Resource: $kms,
          # See the via_service comment in the shell above, including why
          # there is no encryption-context condition here.
          Condition: { StringEquals: { "kms:ViaService": $viaservice } }
        }] else [] end))
    }
  # The owner pin, applied to the finished document so that every Allow gets
  # it, including one added later by someone who never read this line. It is
  # MERGED into whatever condition a statement already carries: the write
  # statement has a StringEquals on the tag being sent, and IAM takes one
  # StringEquals object per statement, so a second one would replace the
  # first and the tag requirement would vanish.
  #
  # Allow only. A Deny that also required the account would stop applying the
  # moment the account was wrong, which is the case it exists for.
  | if $account == "" then . else
      .Statement |= map(
        if .Effect == "Allow"
        then .Condition = ((.Condition // {})
               | .StringEquals = ((.StringEquals // {}) + {"aws:ResourceAccount": $account}))
        else . end)
    end'
