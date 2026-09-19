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
  trap 'real_aws_teardown; cleanup' EXIT
  echo "account ...${ACCOUNT: -4}, region $AWS_REGION" | evidence
}

real_aws_teardown() {
  local r b del
  for r in ${REAL_ROLES[@]+"${REAL_ROLES[@]}"}; do
    aws iam delete-role-policy --role-name "$r" --policy-name estate >/dev/null 2>&1 || true
    aws iam delete-role --role-name "$r" >/dev/null 2>&1 && echo "  removed role $r" || echo "  COULD NOT REMOVE role $r - remove it by hand" >&2
  done
  for b in ${REAL_BUCKETS[@]+"${REAL_BUCKETS[@]}"}; do
    aws s3api head-bucket --bucket "$b" >/dev/null 2>&1 || continue
    while :; do
      del="$(aws s3api list-object-versions --bucket "$b" --max-items 500 --query '{Objects: [Versions, DeleteMarkers][] | [?@ != `null`] | [].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null)"
      [ "$(python3 -c 'import json,sys; print(len((json.load(sys.stdin) or {}).get("Objects") or []))' <<< "$del")" = "0" ] && break
      aws s3api delete-objects --bucket "$b" --delete "$del" >/dev/null 2>&1 || break
    done
    aws s3api delete-bucket --bucket "$b" >/dev/null 2>&1 && echo "  removed bucket $b" || echo "  COULD NOT REMOVE bucket $b - remove it by hand" >&2
  done
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
role_with_policy() {
  local role="$1" policy="$2" bucket="$3" trust marker i
  if ! aws iam get-role --role-name "$role" >/dev/null 2>&1; then
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
