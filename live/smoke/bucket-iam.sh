# Shared by the scenarios that run estates under the published IAM policy
# against REAL AWS (GitHub issue #1343). Sourced by a scenario, never run. It
# is not under scenarios/ because it is not one.
#
# Why real AWS: the pinned emulator does not evaluate s3:ExistingObjectTag or
# s3:RequestObjectTag at all (measured, recorded on #1343), so there a policy
# that works and a policy that bricks an estate look identical. A claim about
# IAM that passes where IAM is not evaluated is a check that cannot fail.
#
# Everything is set up with the caller's own credentials and ACTED with an
# assumed role, so what a scenario measures is what the role may do.

POLICY_RENDERER="$ROOT/examples/record-store-bucket/iam/render-policy.sh"
REAL_ROLES=()
REAL_BUCKETS=()
MARKER_N=0

# real_aws_begin <scenario>: refuse unless asked, settle the account and the
# region, and make sure whatever gets created is removed on any exit. The
# SMOKE_REAL_AWS test itself stays in each scenario, where the claims guard
# looks for it.
real_aws_begin() {
  unset AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3
  export AWS_REGION="${AWS_REGION:-us-east-2}"
  ACCOUNT="$(aws sts get-caller-identity --query Account --output text)" || fail "$1" "no usable AWS credentials"
  SUFFIX="${ACCOUNT: -4}-$(date +%H%M%S)"
  # lib.sh's awsl points at the emulator. Here it is the real CLI.
  awsl() { aws "$@"; }
  trap 'set +e; set +u; real_aws_teardown; cleanup' EXIT
  echo "account ...${ACCOUNT: -4}, region $AWS_REGION" | evidence
}

# Teardown runs under smoke.sh's `set -euo pipefail`, where a trap body is
# no different from any other code: the first command that fails ends the
# whole trap, silently, and every step after it is skipped (#1378). One
# throttled list-object-versions used to skip the role deletion, the stack
# and a borrowed key's policy with nothing printed. So every teardown body
# in this file and in the scenarios that source it starts by turning
# errexit and nounset OFF, every step that can fail prints its own line
# naming the resource, and the last step is always reached.
real_aws_teardown() {
  set +e
  set +u
  local r b
  for r in ${REAL_ROLES[@]+"${REAL_ROLES[@]}"}; do
    aws iam delete-role-policy --role-name "$r" --policy-name estate >/dev/null 2>&1
    aws iam delete-role --role-name "$r" >/dev/null 2>&1 && echo "  removed role $r" || echo "  COULD NOT REMOVE role $r - remove it by hand" >&2
  done
  for b in ${REAL_BUCKETS[@]+"${REAL_BUCKETS[@]}"}; do
    if ! aws s3api head-bucket --bucket "$b" >/dev/null 2>&1; then
      echo "  no bucket $b to remove"
      continue
    fi
    empty_bucket "$b"
    # Attempted even when the emptying failed: the failure may have been a
    # listing that timed out on an already empty bucket, and a delete that
    # is refused says so in its own line.
    aws s3api delete-bucket --bucket "$b" >/dev/null 2>&1 && echo "  removed bucket $b" || echo "  COULD NOT REMOVE bucket $b - remove it by hand" >&2
  done
}

# empty_bucket <bucket>: remove every version and delete marker, so a
# versioned bucket can be deleted. It never lets a failure out: each way it
# can stop prints a line naming the bucket and returns 1, because its
# callers are trap bodies that still have work after it.
#
# The round cap is not a performance budget. The loop's exit condition is
# "AWS said zero objects", and a listing that keeps answering while the
# deletes are refused would spin in a trap forever.
empty_bucket() {
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

# role_name <base>: the name this RUN gives a role. Fixed role names made
# two overlapping runs share one role, where the second overwrites the
# first's policy and a must_deny then passes for a reason that has nothing
# to do with what it claims to measure (#1378). $SUFFIX is the account's
# last four digits and the start time, set by real_aws_begin.
#
# IAM allows 64 characters. A name that would be truncated is refused here
# rather than silently colliding with the next one that truncates the same.
role_name() {
  local n="$1-$SUFFIX"
  if [ "${#n}" -gt 64 ]; then
    echo "the role name '$n' is ${#n} characters and IAM allows 64" >&2
    return 1
  fi
  printf '%s\n' "$n"
}

# bucket_up <bucket>: a bucket that satisfies the bucket contract (claim 29),
# so an apply is not refused for a reason that has nothing to do with IAM.
bucket_up() {
  aws s3api create-bucket --bucket "$1" --create-bucket-configuration "LocationConstraint=$AWS_REGION" >/dev/null || return 1
  REAL_BUCKETS+=("$1")
  aws s3api put-bucket-versioning --bucket "$1" --versioning-configuration Status=Enabled
  aws s3api put-bucket-lifecycle-configuration --bucket "$1" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":1}}]}' >/dev/null
  aws s3api put-public-access-block --bucket "$1" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
}

# role_with_policy <role> <policy-json> <bucket>: make <policy-json> the role's
# only policy, and do not return until it is PROVEN live.
#
# IAM is eventually consistent. A policy just written is not the policy a
# request is judged under for some seconds, and the first attempt to measure
# this (#1342) tested the previous policy that way and believed the result. So
# every policy installed here carries one extra statement: read access to a
# marker object no other policy ever granted. The role is polled until it can
# read that marker. Only then is the policy under test the policy in force.
#
# A role this run has not created is REFUSED (#1378). The old code reused
# whatever role already carried the name, which meant a role leaked by an
# earlier run was silently adopted, given a new policy, and then never
# deleted, because teardown only removes what REAL_ROLES says this run
# made. A role the run created earlier is a different thing and is reused:
# that is what REAL_ROLES is consulted for.
role_with_policy() {
  local role="$1" policy="$2" bucket="$3" trust marker i r mine=0
  for r in ${REAL_ROLES[@]+"${REAL_ROLES[@]}"}; do
    if [ "$r" = "$role" ]; then mine=1; fi
  done
  if [ "$mine" = "0" ]; then
    if aws iam get-role --role-name "$role" >/dev/null 2>&1; then
      echo "  the role $role already exists and this run did not create it." >&2
      echo "  Its policy is some earlier run's, this run would overwrite it, and teardown would leave it behind." >&2
      echo "  Remove it by hand (aws iam delete-role-policy --role-name $role --policy-name estate; aws iam delete-role --role-name $role) and run again." >&2
      return 1
    fi
    trust="{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::$ACCOUNT:root\"},\"Action\":\"sts:AssumeRole\"}]}"
    aws iam create-role --role-name "$role" --assume-role-policy-document "$trust" >/dev/null || return 1
    REAL_ROLES+=("$role")
  fi
  MARKER_N=$((MARKER_N+1)); marker="markers/$role-$MARKER_N"
  echo marker > "$SMOKE_WORK/marker"
  aws s3api put-object --bucket "$bucket" --key "$marker" --body "$SMOKE_WORK/marker" >/dev/null || return 1
  policy="$(jq --arg r "arn:aws:s3:::$bucket/$marker" '.Statement += [{"Sid":"ProofThisPolicyIsLive","Effect":"Allow","Action":"s3:GetObject","Resource":$r}]' <<< "$policy")" || return 1
  aws iam put-role-policy --role-name "$role" --policy-name estate --policy-document "$policy" || return 1
  for i in $(seq 1 60); do
    if as_role "$role" aws s3api get-object --bucket "$bucket" --key "$marker" "$SMOKE_WORK/marker.out" >/dev/null 2>&1; then
      echo "  ($role: policy proven live after ~$((i*3))s)"
      return 0
    fi
    sleep 3
  done
  echo "  the policy on $role never went live" >&2
  return 1
}

# as_role <role> <command...>: run the command with the role's session
# credentials and nothing else in the environment that could answer instead.
as_role() {
  local role="$1" c; shift
  c="$(aws sts assume-role --role-arn "arn:aws:iam::$ACCOUNT:role/$role" --role-session-name "smoke" \
        --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text 2>/dev/null)" || return 1
  ( export AWS_ACCESS_KEY_ID="$(cut -f1 <<< "$c")" AWS_SECRET_ACCESS_KEY="$(cut -f2 <<< "$c")" AWS_SESSION_TOKEN="$(cut -f3 <<< "$c")"
    unset AWS_PROFILE
    "$@" )
}

# denied <output>: the platform's own refusal, as opposed to any other failure.
denied() { grep -qE 'AccessDenied|\(403\)' <<< "$1"; }

# write_bucket_estate <dir> <estate> <bucket> <input>: two record-backed
# instances and one root output, so the estate writes under all three of its
# namespaces.
write_bucket_estate() {
  mkdir -p "$1"
  cat > "$1/main.tf" <<TFEOF
terraform {
  live {
    estate = "$2"

    record_store "s3" {
      bucket = "$3"
      region = "$AWS_REGION"
    }
  }
}

resource "terraform_data" "effect" {
  for_each = toset(["one", "two"])
  input    = "\${each.key}-$4"
}

output "shared" {
  value = "$2-$4"
}
TFEOF
}

# mask hides the account id in anything AWS said, so a run's output can be
# pasted into a pull request as it stands.
mask() { sed -E 's/[0-9]{8}([0-9]{4})/...\1/g'; }

# flat undoes the CLI's word wrap before a sentence is matched.
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }
