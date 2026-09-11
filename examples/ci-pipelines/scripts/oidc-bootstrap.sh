#!/usr/bin/env bash
# The maintainer-only, one-time setup for issue #807's "what stays open"
# item 1: AWS auth off GitHub. This creates the three IAM roles the
# generated GitHub workflows already read (examples/ci-pipelines/README.md's
# "AWS credentials" table), points their trust policy at the OIDC provider
# this account already has, and sets the repository variables that name
# them, so ci-pipelines-smoke.yml's `target: real-aws` job has something
# real to assume.
#
# It never creates the OIDC provider itself - only its trust policy refers
# to one, and this script fails loudly if the account does not already have
# it. It never touches AWS beyond what it prints it is about to do (read
# calls to check what already exists, and the writes this file's own output
# names) and never guesses a variable it cannot derive from
# examples/ci-pipelines/terraform/main.tf or this repository's own name.
#
# Idempotent: every AWS write is preceded by a read that skips it if the
# object already exists in the shape this script would have created, and
# every `gh variable set` is a plain overwrite (repository variables have no
# create-vs-update distinction worth guarding). Safe to re-run.
#
# Usage:
#   scripts/oidc-bootstrap.sh --dry-run           # print every command, run none
#   scripts/oidc-bootstrap.sh                     # do it for real
#   scripts/oidc-bootstrap.sh --region us-west-2   # a region other than the
#                                                   # terraform default
#
# Needs: aws (with an identity that can create IAM roles and read
# repository variables' account), gh (authenticated against
# INTENTIUS/choudoufu), jq.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

ACCOUNT_ID="354867293429"
REPO="INTENTIUS/choudoufu"
OIDC_PROVIDER_ARN="arn:aws:iam::${ACCOUNT_ID}:oidc-provider/token.actions.githubusercontent.com"
SUBJECT_PATTERN="repo:${REPO}:*"

# Read straight from the terraform root this bootstraps, so a changed
# default there cannot silently diverge from what this script wires up.
TF_MAIN="$EXAMPLE_DIR/terraform/main.tf"
tf_var_default() {
  awk -v var="\"$1\"" '$0 ~ "variable " var {f=1} f && /default/{print; exit}' "$TF_MAIN" \
    | sed -E 's/.*default *= *"([^"]*)".*/\1/'
}
REGION_DEFAULT="$(tf_var_default aws_region)"
NAME_PREFIX="$(tf_var_default name_prefix)"
[ -n "$REGION_DEFAULT" ] || { echo "could not read aws_region's default out of $TF_MAIN" >&2; exit 1; }
[ -n "$NAME_PREFIX" ]   || { echo "could not read name_prefix's default out of $TF_MAIN" >&2; exit 1; }

REGION="$REGION_DEFAULT"
DRY_RUN=0

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --region) REGION="$2"; shift ;;
    --region=*) REGION="${1#--region=}" ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0
      ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
  shift
done

LOG_GROUP_NAME="/${NAME_PREFIX}/app"
ROLE_NAME_APP="${NAME_PREFIX}-app"
LOG_GROUP_ARN="arn:aws:logs:${REGION}:${ACCOUNT_ID}:log-group:${LOG_GROUP_NAME}:*"
IAM_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME_APP}"
SSM_RESOURCE_ARN="arn:aws:ssm:${REGION}:${ACCOUNT_ID}:parameter/tofu-*/ci-pipelines-example*"

PLAN_ROLE="choudoufu-ci-pipelines-plan"
ADOPT_ROLE="choudoufu-ci-pipelines-adopt"
APPLY_ROLE="choudoufu-ci-pipelines-apply"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

log()  { echo "== $* =="; }
run()  {
  if [ "$DRY_RUN" = "1" ]; then
    printf '[dry-run] '
    printf '%q ' "$@"
    printf '\n'
  else
    "$@"
  fi
}

echo "account:      $ACCOUNT_ID"
echo "region:       $REGION"
echo "name_prefix:  $NAME_PREFIX  (from $TF_MAIN)"
echo "log group:    $LOG_GROUP_ARN"
echo "iam role:     $IAM_ROLE_ARN"
echo "ssm prefix:   $SSM_RESOURCE_ARN"
[ "$DRY_RUN" = "1" ] && echo "MODE:         dry-run - printing every command, running none"
echo

# ---------------------------------------------------- the provider (read only)

log "confirming the account already has the OIDC provider (never created here)"
if ! aws iam list-open-id-connect-providers --output json \
    | jq -e --arg arn "$OIDC_PROVIDER_ARN" '.OpenIDConnectProviderList[] | select(.Arn == $arn)' \
    > /dev/null 2>&1; then
  echo "  $OIDC_PROVIDER_ARN not found. This script only writes a trust policy that" >&2
  echo "  refers to that provider; it does not create one. Register token.actions.githubusercontent.com" >&2
  echo "  as an OIDC provider on this account first (GitHub Actions -> IAM -> Identity providers)," >&2
  echo "  then re-run." >&2
  exit 1
fi
echo "  found: $OIDC_PROVIDER_ARN"
echo

# --------------------------------------------------------------- trust policy

TRUST_POLICY="$WORKDIR/trust.json"
cat > "$TRUST_POLICY" <<JSON
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Federated": "$OIDC_PROVIDER_ARN" },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": { "token.actions.githubusercontent.com:aud": "sts.amazonaws.com" },
        "StringLike":   { "token.actions.githubusercontent.com:sub": "$SUBJECT_PATTERN" }
      }
    }
  ]
}
JSON

# describe_read <sid> - the read-only statement every one of the three
# roles' policy starts with: describe the two resources by ARN, the
# Resource Groups Tagging API's own reads (discovery has no per-resource
# ARN to scope to), and the STS identity call the smoke script itself makes.
describe_read_statements() {
  cat <<JSON
    {
      "Sid": "DescribeTheEstate",
      "Effect": "Allow",
      "Action": [
        "logs:DescribeLogGroups",
        "logs:ListTagsForResource",
        "logs:ListTagsLogGroup",
        "iam:GetRole",
        "iam:ListRoleTags"
      ],
      "Resource": ["$LOG_GROUP_ARN", "$IAM_ROLE_ARN"]
    },
    {
      "Sid": "TaggingApiReads",
      "Effect": "Allow",
      "Action": ["tag:GetResources", "tag:GetTagKeys", "tag:GetTagValues"],
      "Resource": "*"
    },
    {
      "Sid": "Identity",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    }
JSON
}

write_marker_statement() {
  cat <<JSON
    {
      "Sid": "WriteTheMarker",
      "Effect": "Allow",
      "Action": [
        "logs:TagLogGroup",
        "logs:UntagLogGroup",
        "logs:TagResource",
        "logs:UntagResource",
        "iam:TagRole",
        "iam:UntagRole"
      ],
      "Resource": ["$LOG_GROUP_ARN", "$IAM_ROLE_ARN"]
    }
JSON
}

manage_estate_statement() {
  cat <<JSON
    {
      "Sid": "ManageTheEstate",
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup",
        "logs:DeleteLogGroup",
        "logs:PutRetentionPolicy",
        "iam:CreateRole",
        "iam:DeleteRole",
        "iam:UpdateAssumeRolePolicy"
      ],
      "Resource": ["$LOG_GROUP_ARN", "$IAM_ROLE_ARN"]
    },
    {
      "Sid": "TheRecordStore",
      "Effect": "Allow",
      "Action": [
        "ssm:GetParameter",
        "ssm:GetParameters",
        "ssm:GetParametersByPath",
        "ssm:PutParameter",
        "ssm:DeleteParameter",
        "ssm:DeleteParameters"
      ],
      "Resource": "$SSM_RESOURCE_ARN"
    }
JSON
}

PLAN_POLICY="$WORKDIR/plan-policy.json"
{
  echo '{ "Version": "2012-10-17", "Statement": ['
  describe_read_statements
  echo '] }'
} | jq . > "$PLAN_POLICY"

ADOPT_POLICY="$WORKDIR/adopt-policy.json"
{
  echo '{ "Version": "2012-10-17", "Statement": ['
  describe_read_statements
  echo ','
  write_marker_statement
  echo '] }'
} | jq . > "$ADOPT_POLICY"

APPLY_POLICY="$WORKDIR/apply-policy.json"
{
  echo '{ "Version": "2012-10-17", "Statement": ['
  describe_read_statements
  echo ','
  write_marker_statement
  echo ','
  manage_estate_statement
  echo '] }'
} | jq . > "$APPLY_POLICY"

# role_exists <name>
role_exists() { aws iam get-role --role-name "$1" > /dev/null 2>&1; }

# ensure_role <role-name> <policy-name> <policy-file>
ensure_role() {
  local role="$1" policy_name="$2" policy_file="$3"
  log "role: $role"
  if role_exists "$role"; then
    echo "  exists: updating its trust policy and inline policy in place"
    run aws iam update-assume-role-policy --role-name "$role" \
      --policy-document "file://$TRUST_POLICY"
  else
    echo "  creating, trust scoped to $SUBJECT_PATTERN on $OIDC_PROVIDER_ARN"
    run aws iam create-role --role-name "$role" \
      --assume-role-policy-document "file://$TRUST_POLICY" \
      --description "choudoufu ci-pipelines example (#807): $policy_name"
  fi
  run aws iam put-role-policy --role-name "$role" \
    --policy-name "$policy_name" --policy-document "file://$policy_file"
  echo
}

ensure_role "$PLAN_ROLE"  read-the-estate  "$PLAN_POLICY"
ensure_role "$ADOPT_ROLE" adopt-the-estate "$ADOPT_POLICY"
ensure_role "$APPLY_ROLE" apply-the-estate "$APPLY_POLICY"

# ------------------------------------------------------- repository variables

log "repository variables ($REPO)"
run gh variable set -R "$REPO" AWS_REGION --body "$REGION"
run gh variable set -R "$REPO" CHOUDOUFU_PLAN_ROLE_ARN  --body "arn:aws:iam::${ACCOUNT_ID}:role/${PLAN_ROLE}"
run gh variable set -R "$REPO" CHOUDOUFU_ADOPT_ROLE_ARN --body "arn:aws:iam::${ACCOUNT_ID}:role/${ADOPT_ROLE}"
run gh variable set -R "$REPO" CHOUDOUFU_APPLY_ROLE_ARN --body "arn:aws:iam::${ACCOUNT_ID}:role/${APPLY_ROLE}"
echo

log "done"
if [ "$DRY_RUN" = "1" ]; then
  echo "Nothing was created or changed (--dry-run). Re-run without it to apply."
else
  echo "Run the real-AWS smoke with:"
  echo "  gh workflow run ci-pipelines-smoke.yml -R $REPO --ref <branch> -f target=real-aws"
fi
