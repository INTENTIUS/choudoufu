#!/usr/bin/env bash
# The IAM bindings for one estate's identity, against one record_store "gcs"
# bucket.
#
#   render-grant.sh <estate> <bucket> <member> [--key-prefix <prefix>]
#
# <member> is an IAM principal as a policy names it, for example
# serviceAccount:prod-apply@my-project.iam.gserviceaccount.com.
#
# It prints two bindings to merge into the bucket's IAM policy (policy
# version 3, which conditions need):
#
#   - roles/storage.objectUser on the estate's three prefixes only, by an IAM
#     condition on the object name. That is the fence: this identity reads,
#     writes and deletes objects under tofu-records/<estate>/,
#     tofu-hints/<estate>/ and tofu-outputs/<estate>/ and nowhere else.
#   - roles/storage.legacyBucketReader on the bucket, unconditioned:
#     storage.buckets.get for the contract's read and storage.objects.list.
#
# The listing is not fenced. GCS checks storage.objects.list against the
# bucket, not against an object name, so a condition on the name cannot
# narrow it, and an estate's identity can list every key in the bucket. The
# keys are type names and encoded addresses; the records' contents stay
# behind the conditioned binding.
#
# Not measured against real GCS or an emulator. live/STORAGE.md says so too.
set -euo pipefail

usage() { sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
[ $# -ge 3 ] || usage
estate="$1"; bucket="$2"; member="$3"; shift 3
key_prefix=""
while [ $# -gt 0 ]; do
  case "$1" in
    --key-prefix) key_prefix="${2:?--key-prefix needs the prefix the record_store block sets}"; shift 2 ;;
    *) usage ;;
  esac
done

# Each of these goes into a CEL string or a JSON string as it is, so each is
# held to what it is supposed to be (render-policy.sh's rule, GitHub issue
# #1381).
[[ "$estate" =~ ^[a-z][a-z0-9-]{0,127}$ ]] || { echo "not an estate name: $estate" >&2; exit 2; }
[[ "$bucket" =~ ^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$ ]] || { echo "not a bucket name: $bucket" >&2; exit 2; }
[[ "$member" =~ ^(user|serviceAccount|group|principal|principalSet):[A-Za-z0-9@._/:+-]+$ ]] || { echo "not an IAM member: $member" >&2; exit 2; }
records="tofu-records/$estate/"
if [ -n "$key_prefix" ]; then
  [[ "$key_prefix" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9._-]+)*/?$ ]] || { echo "not a key prefix: $key_prefix" >&2; exit 2; }
  records="${key_prefix%/}/"
fi

# Every prefix ends in "/", for the reason live/STORAGE.md gives: without
# it, estate "prod" would be granted "prod-eu"'s objects too.
objects="projects/_/buckets/$bucket/objects"
expr=""
for p in "$records" "tofu-hints/$estate/" "tofu-outputs/$estate/"; do
  [ -z "$expr" ] || expr="$expr || "
  expr="${expr}resource.name.startsWith(\\\"$objects/$p\\\")"
done

cat <<JSON
{
  "bindings": [
    {
      "role": "roles/storage.objectUser",
      "members": ["$member"],
      "condition": {
        "title": "choudoufu-estate-$estate",
        "description": "Objects under estate $estate's three record store prefixes",
        "expression": "$expr"
      }
    },
    {
      "role": "roles/storage.legacyBucketReader",
      "members": ["$member"]
    }
  ]
}
JSON
