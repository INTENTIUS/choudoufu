#!/usr/bin/env bash
# The statement a customer managed key's KEY POLICY needs, for the roles that
# use a record store bucket encrypted under it.
#
#   render-key-statement.sh <principal-arn>...
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
set -euo pipefail

[ $# -ge 1 ] || { sed -n '5p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
for arn in "$@"; do
  [[ "$arn" =~ ^arn:aws[a-z-]*:iam::[0-9]{12}:(role|user)/[A-Za-z0-9+=,.@_/-]+$ ]] \
    || { echo "not a role or user ARN: $arn" >&2; exit 2; }
done

printf '%s\n' "$@" | jq -R . | jq -s '{
  Sid: "RecordStoreBucketUsers",
  Effect: "Allow",
  Principal: { AWS: . },
  Action: ["kms:Decrypt", "kms:GenerateDataKey"],
  Resource: "*"
}'
