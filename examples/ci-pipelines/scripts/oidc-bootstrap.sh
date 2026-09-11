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
# The trust policy's subject condition accounts for GitHub's immutable-subject
# setting (repos/OWNER/NAME/actions/oidc/customization/sub): when a repo has
# it on, every token's `sub` claim carries the numeric-id form
# (repo:OWNER@<id>/NAME@<id>:...) instead of the plain repo:OWNER/NAME:...
# form, and a trust policy that only lists the plain form then refuses every
# AssumeRoleWithWebIdentity call. This script reads that setting and keeps
# both forms in the StringLike condition.
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
# The leaf ARN: GetParameter, PutParameter, DeleteParameter and the batch
# GetParameters/DeleteParameters all take a parameter NAME and are
# authorized resource-level against that name's own ARN. Every record or
# hint key this example's estate writes is
# "/tofu-records/ci-pipelines-example/..." or
# "/tofu-hints/ci-pipelines-example/..." (internal/live/projection/record.go's
# recordNamespaceRoot + RecordKeyPrefix, internal/live/projection/hint_store.go's
# hintNamespaceRoot + HintKey) - the "tofu-*" segment covers both roots at
# once, "ci-pipelines-example*" keeps every write scoped to this estate.
SSM_RESOURCE_ARN="arn:aws:ssm:${REGION}:${ACCOUNT_ID}:parameter/tofu-*/ci-pipelines-example*"
# The path ARN: GetParametersByPath is authorized against the ARN built
# from its own Path argument, never against the leaf pattern above -
# issue #807's run 34636502021 is exactly this: live-plan's own error
# named "arn:...:parameter/tofu-records" verbatim, not the estate-scoped
# leaf. That Path argument is not this estate's own prefix either; it is
# always one directory entry short of it, because
# internal/live/staterecord/ssm.go's List and GetAll both compute the
# GetParametersByPath folder by trimming the LAST "/"-segment off the
# keyPrefix they are asked for (GetParametersByPath matches whole
# hierarchy segments, not an arbitrary string prefix - see that file's
# "List's approximation" doc). Two call shapes reach it, both rooted here:
#   - internal/live/discovery/recordorphan_read.go's "Listing the record
#     store to find untaggable resources whose configuration block was
#     removed failed" (the run's own error text) lists
#     projection.RecordKeyPrefix(estate), i.e. "tofu-records/ci-pipelines-example"
#     with no trailing slash, so the last segment trimmed off is
#     "ci-pipelines-example" itself and the folder queried is the bare,
#     account-wide "/tofu-records" - the namespace root every estate
#     shares, not this one alone. GetParametersByPath has no way to filter
#     its own listing to one estate; that filtering happens client-side
#     in Go after the call, which is why this grant cannot be narrowed
#     past the shared root.
#   - internal/live/projection/store.go's provisionStoreSentinel lists
#     recordStoreKeyPrefix(rs, estate) + "/" (a trailing slash), so the
#     last segment trimmed off is empty and the folder queried is one
#     level DEEPER: "/tofu-records/ci-pipelines-example" - a child of the
#     root above, needing the "/*" form.
SSM_RECORD_PATH_ARN="arn:aws:ssm:${REGION}:${ACCOUNT_ID}:parameter/tofu-records"

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

# ------------------------------------------------ the OIDC subject pattern(s)

# GitHub's immutable-subject setting rewrites the `sub` claim every token
# carries: instead of "repo:OWNER/NAME:ref:refs/heads/main" it becomes
# "repo:OWNER@<id>/NAME@<id>:ref:refs/heads/main", and a trust policy
# StringLike-matching only the plain form then refuses every token with
# "Not authorized to perform sts:AssumeRoleWithWebIdentity" - the setting
# changed the token, not the policy, so nothing but the trust policy has to
# know about it. Keep both forms in the StringLike list regardless, so a
# repo that later turns the setting back off still matches.
SUBJECT_PATTERNS=("$SUBJECT_PATTERN")
if OIDC_SUB_JSON="$(gh api "repos/${REPO}/actions/oidc/customization/sub" 2>/dev/null)"; then
  USE_IMMUTABLE="$(printf '%s' "$OIDC_SUB_JSON" | jq -r '.use_immutable_subject // empty' 2>/dev/null || true)"
  if [ "$USE_IMMUTABLE" = "true" ]; then
    SUB_CLAIM_PREFIX="$(printf '%s' "$OIDC_SUB_JSON" | jq -r '.sub_claim_prefix // empty' 2>/dev/null || true)"
    if [ -n "$SUB_CLAIM_PREFIX" ]; then
      SUBJECT_PATTERNS=("${SUB_CLAIM_PREFIX}:*" "$SUBJECT_PATTERN")
    else
      echo "gh api repos/${REPO}/actions/oidc/customization/sub reported use_immutable_subject=true with no sub_claim_prefix; trust policy falls back to the plain subject form $SUBJECT_PATTERN only" >&2
    fi
  fi
else
  echo "could not query repos/${REPO}/actions/oidc/customization/sub (gh api failed); trust policy falls back to the plain subject form $SUBJECT_PATTERN only" >&2
fi

if [ "${#SUBJECT_PATTERNS[@]}" -eq 1 ]; then
  SUBJECT_CONDITION="$(jq -n --arg s "${SUBJECT_PATTERNS[0]}" '$s')"
else
  SUBJECT_CONDITION="$(printf '%s\n' "${SUBJECT_PATTERNS[@]}" | jq -R . | jq -s .)"
fi

echo "account:      $ACCOUNT_ID"
echo "region:       $REGION"
echo "name_prefix:  $NAME_PREFIX  (from $TF_MAIN)"
echo "log group:    $LOG_GROUP_ARN"
echo "iam role:     $IAM_ROLE_ARN"
echo "ssm prefix:   $SSM_RESOURCE_ARN"
echo "ssm path:     $SSM_RECORD_PATH_ARN (+ /*)"
echo "subject(s):   ${SUBJECT_PATTERNS[*]}"
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
jq -n \
  --arg provider "$OIDC_PROVIDER_ARN" \
  --arg aud "sts.amazonaws.com" \
  --argjson sub "$SUBJECT_CONDITION" \
  '{
    Version: "2012-10-17",
    Statement: [
      {
        Effect: "Allow",
        Principal: { Federated: $provider },
        Action: "sts:AssumeRoleWithWebIdentity",
        Condition: {
          StringEquals: { "token.actions.githubusercontent.com:aud": $aud },
          StringLike:   { "token.actions.githubusercontent.com:sub": $sub }
        }
      }
    ]
  }' > "$TRUST_POLICY"

# describe_read <sid> - the read-only statements every one of the three
# roles' policy starts with.
#
# issue #807's real-AWS dispatch (run 34632345663) got past `live-check` and
# then failed `live-plan` with no evidence in the log; `aws iam
# simulate-principal-policy` on the apply role showed why:
# `iam:ListPolicies`, `iam:ListRoles`, `logs:DescribeLogGroups` and Cloud
# Control's own `cloudformation:ListResources`/`GetResource` all came back
# implicitly denied. Every action below is one this example's two resource
# types, or the sweep discovery runs across the account to find them, is
# proven to call - never guessed:
#
#   - `cloudformation:ListResources`, `cloudformation:GetResource` - choudoufu's
#     OWN calls, not the provider's: this is the Cloud Control fallback
#     transport internal/live/cloudcontrol/client.go speaks (see its own doc.go
#     and site/content/docs/use/reference.md's "Permissions a run needs" table,
#     which names these two actions for that file with no ARN scoping - the
#     account had granted this example's roles *none* of them before this fix,
#     which is the root cause `live-plan` never got past). List-style AWS
#     actions take no resource identifier to scope to, and GetResource is
#     called during discovery against identifiers this run does not know
#     ahead of time (an unowned candidate somewhere else in the account), so
#     both stay on Resource "*" rather than the two ARNs below.
#   - `iam:ListRoles` - live/registry-schema-facts.json's AWS::IAM::Role entry
#     lists this as its "list" handler_permissions entry, and GitHub issue
#     #1039 is the account-wide-unfiltered-list finding by name: the
#     provider's own list resource for aws_iam_role carries no filter
#     argument (also internal/live/discovery/discovery.go's comments at
#     "one iam:ListRoles-shaped call" and "IAM has no ListServiceLinkedRoles,
#     so iam:ListRoles returns both" - the ordinary roles and any
#     service-linked ones together). Every role in the account, not just
#     $IAM_ROLE_ARN, so Resource "*".
#   - `iam:ListPolicies`, `iam:GetPolicy`, `iam:GetPolicyVersion` -
#     live/registry-schema-facts.json's AWS::IAM::ManagedPolicy entry lists
#     `iam:ListPolicies` as its "list" permission and `iam:GetPolicy`,
#     `iam:GetPolicyVersion` (metadata, then the version's document) as its
#     "read" pair; GitHub issue #1039 names aws_iam_policy as the second of
#     the three types whose provider list resource has no filter argument
#     and measures the cost in exactly this shape: "every call of that
#     growth is GetPolicyVersion" once per policy in the whole account.
#     None of these policies are this estate's own (main.tf attaches none to
#     $IAM_ROLE_ARN), so there is no ARN to scope any of the three to.
#   - `logs:DescribeLogGroups` - live/registry-schema-facts.json's
#     AWS::Logs::LogGroup entry lists this as its "list" permission too.
#     Unlike the read-only pair below, DescribeLogGroups is what enumerates
#     the account's log groups in the first place (no log-group-name
#     argument narrows a list call to one group's ARN), so the ARN-scoped
#     grant this line used to carry never did anything; moved here.
#
# The pair below stays scoped to the two ARNs because both DO support
# resource-level permissions once an object is already identified by name:
# live/registry-schema-facts.json's AWS::IAM::Role "read" entry also lists
# `iam:GetRolePolicy`, `iam:ListAttachedRolePolicies` and
# `iam:ListRolePolicies` alongside `iam:GetRole` - added here too, since a
# role's own read is incomplete without them even though this example's role
# attaches nothing today - and its AWS::Logs::LogGroup "read"/"list" entries
# both carry `logs:ListTagsForResource` beside `logs:ListTagsLogGroup`,
# the older, still-live alias the AWS provider itself calls.
discover_account_statement() {
  cat <<JSON
    {
      "Sid": "DiscoverTheAccount",
      "Effect": "Allow",
      "Action": [
        "cloudformation:ListResources",
        "cloudformation:GetResource",
        "iam:ListRoles",
        "iam:ListPolicies",
        "iam:GetPolicy",
        "iam:GetPolicyVersion",
        "logs:DescribeLogGroups"
      ],
      "Resource": "*"
    }
JSON
}

describe_read_statements() {
  cat <<JSON
    {
      "Sid": "DescribeTheEstate",
      "Effect": "Allow",
      "Action": [
        "logs:ListTagsForResource",
        "logs:ListTagsLogGroup",
        "iam:GetRole",
        "iam:ListRoleTags",
        "iam:GetRolePolicy",
        "iam:ListAttachedRolePolicies",
        "iam:ListRolePolicies"
      ],
      "Resource": ["$LOG_GROUP_ARN", "$IAM_ROLE_ARN"]
    },
    $(discover_account_statement),
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
        "ssm:PutParameter",
        "ssm:DeleteParameter",
        "ssm:DeleteParameters"
      ],
      "Resource": "$SSM_RESOURCE_ARN"
    },
    {
      "Sid": "TheRecordStorePathListing",
      "Effect": "Allow",
      "Action": "ssm:GetParametersByPath",
      "Resource": ["$SSM_RECORD_PATH_ARN", "$SSM_RECORD_PATH_ARN/*"]
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
    echo "  creating, trust scoped to ${SUBJECT_PATTERNS[*]} on $OIDC_PROVIDER_ARN"
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
