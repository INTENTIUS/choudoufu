#!/usr/bin/env bash
# The statement a customer managed key's KEY POLICY needs, for the roles that
# use a record store bucket encrypted under it.
#
#   render-key-statement.sh [--key <key-arn>] <principal-arn>...
#
# This prints ONE statement, not a key policy. The key is yours and so is the
# rest of its policy - who administers it, who may schedule its deletion -
# and nothing here has an opinion on that. Add this statement to it.
#
# Why it exists (GitHub issue #1345): render-policy.sh --kms puts kms:Decrypt
# and kms:GenerateDataKey in the ROLE's policy, and with a customer managed
# key that is half of what is needed. A key is usable only by the principals
# its key policy allows. A key policy that leaves the estate's role out is the
# usual reason a first run against a new bucket fails, and S3 reports it as
# AccessDenied on PutObject, which sends people to the bucket.
#
# The two actions are the two S3 makes on a caller's behalf: GenerateDataKey
# for a PutObject and Decrypt for a GetObject. Measured on real AWS (smoke
# claim 37): an estate's whole life, and an operator recovering a deleted
# record, needed no other.
#
# Name principals, not the account. "arn:aws:iam::<acct>:root" as the
# principal here hands the decision to every IAM policy in the account, and
# deciding this yourself is the reason to have your own key.
#
# --key <key-arn> is the key this statement is going into. It is optional so
# that every invocation written before it keeps working, and the only thing
# it changes is the kms:ViaService condition below - which needs the key
# region, and there is nowhere else to read it from. See that condition.
set -euo pipefail

usage() { sed -n '5p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }

key=""; principals=()
while [ $# -gt 0 ]; do
  case "$1" in
    --key) key="${2:?--key needs a KMS key ARN}"; shift 2 ;;
    -*) usage ;;
    *) principals+=("$1"); shift ;;
  esac
done
[ "${#principals[@]}" -ge 1 ] || usage

# Until GitHub issue #1381 this was "arn:aws[a-z-]*" over
# "[A-Za-z0-9+=,.@_/-]+", which accepted "arn:awsevil:iam::...", a partition
# that does not exist, and "role//", whose empty path segment names no role.
# Three partitions exist. A role path is a sequence of non-empty segments and
# a role name is one more, both over the character set IAM allows: alphanumerics
# plus _+=,.@- and no "*".
#
# One key lives in one partition, so principals from two of them cannot all be
# using it. Named rather than silently rendered, because such a statement looks
# right in the key policy and refuses half of the principals it names.
principal_partition=""
for arn in "${principals[@]}"; do
  [[ "$arn" =~ ^arn:(aws|aws-us-gov|aws-cn):iam::[0-9]{12}:(role|user)/([A-Za-z0-9_+=,.@-]+/)*[A-Za-z0-9_+=,.@-]+$ ]] \
    || { echo "not a role or user ARN: $arn" >&2; exit 2; }
  p="$(echo "$arn" | cut -d: -f2)"
  if [ -z "$principal_partition" ]; then
    principal_partition="$p"
  elif [ "$p" != "$principal_partition" ]; then
    echo "the principals name two partitions, $principal_partition and $p; one key lives in exactly one of them" >&2
    exit 2
  fi
done

via_service=""
if [ -n "$key" ]; then
  [[ "$key" =~ ^arn:(aws|aws-us-gov|aws-cn):kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-f-]{36}$ ]] \
    || { echo "not a KMS key ARN: $key" >&2; exit 2; }
  key_partition="$(echo "$key" | cut -d: -f2)"
  key_region="$(echo "$key" | cut -d: -f4)"
  [ "$key_partition" = "$principal_partition" ] || {
    echo "--key names a key in the $key_partition partition and the principals are in $principal_partition; one of the two is wrong: $key" >&2
    exit 2
  }
  case "$key_partition" in
    aws-cn) via_service="s3.${key_region}.amazonaws.com.cn" ;;
    *)      via_service="s3.${key_region}.amazonaws.com" ;;
  esac
else
  echo "warning: rendering without --key, so this statement does not confine the grant to S3." >&2
  echo "  A principal named here can then use the key directly, on any ciphertext made" >&2
  echo "  under it, from anywhere. With the flag the statement also requires" >&2
  echo "  kms:ViaService = the S3 endpoint in the key region." >&2
  echo "  Add: --key <key ARN>" >&2
fi

# kms:ViaService confines the two actions to the one caller they exist for:
# S3, asking the key on a principal behalf. Without it, kms:Decrypt on this
# key is kms:Decrypt on this key, usable directly by every principal named
# above against any ciphertext it holds.
#
# There is deliberately no kms:EncryptionContext:aws:s3:arn condition beside
# it. S3 sets that context to the BUCKET ARN with S3 Bucket Keys on and to
# the OBJECT ARN with them off, so a literal here is wrong for half of the
# buckets a key serves, and a wrong one denies every write - the first one
# included. GitHub issue #1381.
printf '%s\n' "${principals[@]}" | jq -R . | jq -s --arg via "$via_service" '{
  Sid: "RecordStoreBucketUsers",
  Effect: "Allow",
  Principal: { AWS: . },
  Action: ["kms:Decrypt", "kms:GenerateDataKey"],
  Resource: "*"
} + (if $via == "" then {} else { Condition: { StringEquals: { "kms:ViaService": $via } } } end)'
