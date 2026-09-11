#!/usr/bin/env bash
# Self-test for oidc-bootstrap.sh's trust-policy subject condition (#807).
#
# The bug: GitHub's immutable-subject setting
# (repos/OWNER/NAME/actions/oidc/customization/sub) rewrites every OIDC
# token's `sub` claim to repo:OWNER@<id>/NAME@<id>:... instead of the plain
# repo:OWNER/NAME:.... A trust policy whose StringLike condition only ever
# held the plain form then refuses every AssumeRoleWithWebIdentity call once
# a repo turns the setting on - which is what happened to the three
# choudoufu-ci-pipelines-* roles.
#
# This stubs `gh` and `aws` on PATH so no real network or AWS call happens,
# runs the target script under --dry-run (never for real), and inspects the
# trust document it composes:
#   - stub reports use_immutable_subject: true  -> StringLike must hold BOTH
#     the immutable-form pattern and the plain repo:OWNER/NAME:* pattern.
#   - stub reports use_immutable_subject: false (or the gh api call fails)
#     -> StringLike must hold ONLY the plain form.
#
# Usage: selftest-oidc-bootstrap.sh [path-to-oidc-bootstrap.sh]
#   Defaults to the sibling oidc-bootstrap.sh. Pass an explicit path to run
#   this same test against another revision of the script (e.g. main's, to
#   show the test is RED there).

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_PATH="${1:-$HERE/oidc-bootstrap.sh}"
[ -f "$SCRIPT_PATH" ] || { echo "no such script: $SCRIPT_PATH" >&2; exit 2; }

REPO="INTENTIUS/choudoufu"
PLAIN_SUBJECT="repo:${REPO}:*"
IMMUTABLE_PREFIX="repo:INTENTIUS@259705176/choudoufu@1332291567"
IMMUTABLE_SUBJECT="${IMMUTABLE_PREFIX}:*"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STUBDIR="$WORK/bin"
mkdir -p "$STUBDIR"

# aws stub: the only two calls oidc-bootstrap.sh makes unconditionally
# (i.e. not behind its --dry-run "run()" wrapper) are the provider lookup
# and, per role, a get-role existence check. Everything that would mutate
# AWS (create-role, put-role-policy, update-assume-role-policy) is behind
# `run()` and, in --dry-run, is only ever printed - the stub errors loudly
# if it is ever invoked, since that would mean --dry-run stopped being
# dry.
cat > "$STUBDIR/aws" <<'AWSEOF'
#!/usr/bin/env bash
case "$1 $2" in
  "iam list-open-id-connect-providers")
    echo '{"OpenIDConnectProviderList":[{"Arn":"arn:aws:iam::354867293429:oidc-provider/token.actions.githubusercontent.com"}]}'
    exit 0
    ;;
  "iam get-role")
    exit 1 # not found -> script takes the create-role branch, still under run()
    ;;
  *)
    echo "selftest aws stub: unexpected call, --dry-run should never reach AWS: $*" >&2
    exit 1
    ;;
esac
AWSEOF
chmod +x "$STUBDIR/aws"

# gh stub: the only unconditional call is the oidc customization read.
# `gh variable set` is behind run() and must never actually execute either.
write_gh_stub() {
  local body="$1"
  cat > "$STUBDIR/gh" <<GHEOF
#!/usr/bin/env bash
if [ "\$1" = "api" ] && [[ "\$2" == repos/*/actions/oidc/customization/sub ]]; then
  cat <<'BODY'
$body
BODY
  exit 0
fi
echo "selftest gh stub: unexpected call, --dry-run should never reach gh writes: \$*" >&2
exit 1
GHEOF
  chmod +x "$STUBDIR/gh"
}

# run_case <name> <gh-body> -> prints the trust document's StringLike sub
# value (as compact JSON: a quoted string or a JSON array of strings).
run_case() {
  local name="$1" body="$2"
  write_gh_stub "$body"

  local runner="$WORK/run-$name.sh"
  cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run >"$WORK/$name.out" 2>"$WORK/$name.err"
jq -c '.Statement[0].Condition.StringLike."token.actions.githubusercontent.com:sub"' "\$TRUST_POLICY" > "$WORK/$name.sub"
RUNEOF
  chmod +x "$runner"

  if ! PATH="$STUBDIR:$PATH" bash "$runner"; then
    echo "FAIL ($name): oidc-bootstrap.sh --dry-run exited non-zero. stderr:" >&2
    cat "$WORK/$name.err" >&2 2>/dev/null || true
    return 1
  fi
  cat "$WORK/$name.sub"
}

FAILURES=0

echo "== case: use_immutable_subject: true =="
SUB_IMMUTABLE="$(run_case immutable '{"use_default":true,"use_immutable_subject":true,"sub_claim_prefix":"'"$IMMUTABLE_PREFIX"'"}')" || SUB_IMMUTABLE=""
echo "  StringLike sub: $SUB_IMMUTABLE"
if ! echo "$SUB_IMMUTABLE" | jq -e --arg s "$IMMUTABLE_SUBJECT" 'type == "array" and index($s) != null' > /dev/null 2>&1; then
  echo "FAIL: immutable case does not contain the immutable-form subject ($IMMUTABLE_SUBJECT): $SUB_IMMUTABLE" >&2
  FAILURES=$((FAILURES + 1))
fi
if ! echo "$SUB_IMMUTABLE" | jq -e --arg s "$PLAIN_SUBJECT" 'type == "array" and index($s) != null' > /dev/null 2>&1; then
  echo "FAIL: immutable case does not also keep the plain-form subject ($PLAIN_SUBJECT): $SUB_IMMUTABLE" >&2
  FAILURES=$((FAILURES + 1))
fi

echo "== case: use_immutable_subject: false =="
SUB_PLAIN="$(run_case plain '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}')" || SUB_PLAIN=""
echo "  StringLike sub: $SUB_PLAIN"
if ! echo "$SUB_PLAIN" | jq -e --arg s "$PLAIN_SUBJECT" '. == $s' > /dev/null 2>&1; then
  echo "FAIL: plain case's StringLike sub should be exactly the string $PLAIN_SUBJECT, got: $SUB_PLAIN" >&2
  FAILURES=$((FAILURES + 1))
fi

echo "== case: gh api call fails (customization endpoint unreachable/404) =="
write_gh_stub_failure() {
  cat > "$STUBDIR/gh" <<'GHEOF'
#!/usr/bin/env bash
if [ "$1" = "api" ] && [[ "$2" == repos/*/actions/oidc/customization/sub ]]; then
  exit 1
fi
echo "selftest gh stub: unexpected call, --dry-run should never reach gh writes: $*" >&2
exit 1
GHEOF
  chmod +x "$STUBDIR/gh"
}
write_gh_stub_failure
runner="$WORK/run-failcase.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run >"$WORK/failcase.out" 2>"$WORK/failcase.err"
jq -c '.Statement[0].Condition.StringLike."token.actions.githubusercontent.com:sub"' "\$TRUST_POLICY" > "$WORK/failcase.sub"
RUNEOF
chmod +x "$runner"
if PATH="$STUBDIR:$PATH" bash "$runner"; then
  SUB_FAILCASE="$(cat "$WORK/failcase.sub")"
  echo "  StringLike sub: $SUB_FAILCASE"
  echo "  stderr said:"
  sed 's/^/    /' "$WORK/failcase.err"
  if ! echo "$SUB_FAILCASE" | jq -e --arg s "$PLAIN_SUBJECT" '. == $s' > /dev/null 2>&1; then
    echo "FAIL: gh-api-failure case's StringLike sub should be exactly the string $PLAIN_SUBJECT, got: $SUB_FAILCASE" >&2
    FAILURES=$((FAILURES + 1))
  fi
  if ! grep -q "falls back to the plain subject form" "$WORK/failcase.err"; then
    echo "FAIL: gh-api-failure case did not say on stderr that it fell back" >&2
    FAILURES=$((FAILURES + 1))
  fi
else
  echo "FAIL: oidc-bootstrap.sh --dry-run exited non-zero when the oidc customization endpoint failed; it must fall back, not abort. stderr:" >&2
  cat "$WORK/failcase.err" >&2 2>/dev/null || true
  FAILURES=$((FAILURES + 1))
fi

echo "== case: DiscoverTheAccount is present, with the same seven actions, in all three policies (#807) =="
# The account-wide read statement issue #807's real-AWS dispatch (run
# 34632345663) found missing: `live-plan` failed with `iam:ListPolicies`,
# `iam:ListRoles`, `logs:DescribeLogGroups` and Cloud Control's own
# `cloudformation:ListResources`/`GetResource` all implicitly denied. Every
# action here is cited to a file in oidc-bootstrap.sh's own
# discover_account_statement doc comment - this test only proves the
# generator actually emits what that comment promises, in all three
# policies (describe_read_statements is shared by all three), on
# Resource "*".
DISCOVER_ACTIONS='["cloudformation:ListResources","cloudformation:GetResource","iam:ListRoles","iam:ListPolicies","iam:GetPolicy","iam:GetPolicyVersion","logs:DescribeLogGroups"]'
write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
runner="$WORK/run-discover.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run >"$WORK/discover.out" 2>"$WORK/discover.err"
jq -c '.Statement[] | select(.Sid=="DiscoverTheAccount")' "\$PLAN_POLICY"  > "$WORK/discover.plan.json"  || true
jq -c '.Statement[] | select(.Sid=="DiscoverTheAccount")' "\$ADOPT_POLICY" > "$WORK/discover.adopt.json" || true
jq -c '.Statement[] | select(.Sid=="DiscoverTheAccount")' "\$APPLY_POLICY" > "$WORK/discover.apply.json" || true
RUNEOF
chmod +x "$runner"
if ! PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL (discover): oidc-bootstrap.sh --dry-run exited non-zero. stderr:" >&2
  cat "$WORK/discover.err" >&2 2>/dev/null || true
  FAILURES=$((FAILURES + 1))
else
  for role in plan adopt apply; do
    stmt="$(cat "$WORK/discover.$role.json" 2>/dev/null || true)"
    echo "  $role DiscoverTheAccount: ${stmt:-<absent>}"
    if [ -z "$stmt" ]; then
      echo "FAIL: the $role policy carries no Sid==\"DiscoverTheAccount\" statement" >&2
      FAILURES=$((FAILURES + 1))
      continue
    fi
    if ! echo "$stmt" | jq -e --argjson want "$DISCOVER_ACTIONS" \
        '.Effect == "Allow" and .Resource == "*" and ((.Action | sort) == ($want | sort))' \
        > /dev/null 2>&1; then
      echo "FAIL: the $role policy's DiscoverTheAccount statement does not match Effect=Allow, Resource=\"*\", Action=$DISCOVER_ACTIONS exactly: $stmt" >&2
      FAILURES=$((FAILURES + 1))
    fi
  done
fi

echo "== case: the record store's GetParametersByPath is scoped to the path ARN, not the leaf (#807) =="
# issue #807's run 34636502021: live-plan failed with `ssm:GetParametersByPath`
# denied on "arn:...:parameter/tofu-records" - the account-wide record
# namespace root, not this estate's own leaf pattern. internal/live/staterecord/ssm.go's
# List and GetAll both authorize that call against the ENCLOSING FOLDER of
# the keyPrefix they are asked for, never against the leaf parameter name,
# so a policy that only grants the leaf ARN (the shape every other SSM
# record-store action uses) refuses it no matter how the leaf pattern is
# widened. Prove APPLY_POLICY's TheRecordStorePathListing statement grants
# GetParametersByPath on both the bare path ARN and its "/*" child, and
# that TheRecordStore (the leaf statement) does NOT also claim
# GetParametersByPath - if it did, the leaf-scoped policy would look
# sufficient by itself and this exact bug would still ship silently.
write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
runner="$WORK/run-recordstore.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run >"$WORK/recordstore.out" 2>"$WORK/recordstore.err"
jq -c '.Statement[] | select(.Sid=="TheRecordStorePathListing")' "\$APPLY_POLICY" > "$WORK/recordstore.pathlisting.json" || true
jq -c '.Statement[] | select(.Sid=="TheRecordStore")'            "\$APPLY_POLICY" > "$WORK/recordstore.leaf.json"        || true
echo "\$SSM_RECORD_PATH_ARN" > "$WORK/recordstore.patharn"
echo "\$SSM_RESOURCE_ARN"    > "$WORK/recordstore.leafarn"
RUNEOF
chmod +x "$runner"
if ! PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL (recordstore): oidc-bootstrap.sh --dry-run exited non-zero. stderr:" >&2
  cat "$WORK/recordstore.err" >&2 2>/dev/null || true
  FAILURES=$((FAILURES + 1))
else
  PATH_ARN="$(cat "$WORK/recordstore.patharn")"
  LEAF_ARN="$(cat "$WORK/recordstore.leafarn")"
  PATHLISTING_STMT="$(cat "$WORK/recordstore.pathlisting.json" 2>/dev/null || true)"
  LEAF_STMT="$(cat "$WORK/recordstore.leaf.json" 2>/dev/null || true)"
  echo "  TheRecordStorePathListing: ${PATHLISTING_STMT:-<absent>}"
  echo "  TheRecordStore:            ${LEAF_STMT:-<absent>}"

  if [ -z "$PATHLISTING_STMT" ]; then
    echo "FAIL: APPLY_POLICY carries no Sid==\"TheRecordStorePathListing\" statement" >&2
    FAILURES=$((FAILURES + 1))
  else
    if ! echo "$PATHLISTING_STMT" | jq -e --arg arn "$PATH_ARN" \
        '.Effect == "Allow" and (.Action == "ssm:GetParametersByPath" or (.Action | type == "array" and index("ssm:GetParametersByPath") != null)) and (.Resource | type == "array") and (.Resource | index($arn) != null) and (.Resource | index($arn + "/*") != null)' \
        > /dev/null 2>&1; then
      echo "FAIL: TheRecordStorePathListing does not grant ssm:GetParametersByPath on both $PATH_ARN and $PATH_ARN/*: $PATHLISTING_STMT" >&2
      FAILURES=$((FAILURES + 1))
    fi
  fi

  if [ -z "$LEAF_STMT" ]; then
    echo "FAIL: APPLY_POLICY carries no Sid==\"TheRecordStore\" statement" >&2
    FAILURES=$((FAILURES + 1))
  else
    if ! echo "$LEAF_STMT" | jq -e --arg arn "$LEAF_ARN" \
        '.Effect == "Allow" and (.Action | index("ssm:GetParametersByPath")) == null and .Resource == $arn' \
        > /dev/null 2>&1; then
      echo "FAIL: TheRecordStore should grant the leaf ARN $LEAF_ARN with no ssm:GetParametersByPath in its actions: $LEAF_STMT" >&2
      FAILURES=$((FAILURES + 1))
    fi
  fi
fi

echo "== case: the CloudWatch Logs tag actions are granted on both log-group ARN forms (#807) =="
# Issue #807's run 34640702934: live-apply's fatal error named the log group
# ARN with no trailing ":*" verbatim -
#   "AccessDeniedException: ... is not authorized to perform:
#    logs:ListTagsForResource on resource:
#    arn:aws:logs:us-east-1:354867293429:log-group:/choudoufu-ci-pipelines-example/app"
# - while the policy only ever granted the ":*" form
# (LOG_GROUP_ARN). Prove DescribeTheEstate (shared by all three policies)
# and WriteTheMarker (adopt + apply) both carry LOG_GROUP_ARN_BASE (the bare
# form) alongside LOG_GROUP_ARN, and that ManageTheEstate - whose logs:
# actions are true log-group actions, never the tagging trio - was left on
# the ":*" form alone.
write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
runner="$WORK/run-tagarn.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run >"$WORK/tagarn.out" 2>"$WORK/tagarn.err"
jq -c '.Statement[] | select(.Sid=="DescribeTheEstate")' "\$PLAN_POLICY"  > "$WORK/tagarn.plan.describe.json"  || true
jq -c '.Statement[] | select(.Sid=="DescribeTheEstate")' "\$APPLY_POLICY" > "$WORK/tagarn.apply.describe.json" || true
jq -c '.Statement[] | select(.Sid=="WriteTheMarker")'    "\$APPLY_POLICY" > "$WORK/tagarn.apply.marker.json"   || true
jq -c '.Statement[] | select(.Sid=="ManageTheEstate")'   "\$APPLY_POLICY" > "$WORK/tagarn.apply.manage.json"   || true
echo "\$LOG_GROUP_ARN"             > "$WORK/tagarn.starform"
echo "\${LOG_GROUP_ARN_BASE:-}"    > "$WORK/tagarn.bareform"
RUNEOF
chmod +x "$runner"
if ! PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL (tagarn): oidc-bootstrap.sh --dry-run exited non-zero. stderr:" >&2
  cat "$WORK/tagarn.err" >&2 2>/dev/null || true
  FAILURES=$((FAILURES + 1))
else
  STAR_ARN="$(cat "$WORK/tagarn.starform")"
  BARE_ARN="$(cat "$WORK/tagarn.bareform")"
  echo "  LOG_GROUP_ARN:      $STAR_ARN"
  echo "  LOG_GROUP_ARN_BASE: $BARE_ARN"
  if [ "$BARE_ARN" = "$STAR_ARN" ] || [ "${BARE_ARN}:*" != "$STAR_ARN" ]; then
    echo "FAIL: LOG_GROUP_ARN_BASE ($BARE_ARN) is not the bare form of LOG_GROUP_ARN ($STAR_ARN)" >&2
    FAILURES=$((FAILURES + 1))
  fi

  DESCRIBE_PLAN="$(cat "$WORK/tagarn.plan.describe.json" 2>/dev/null || true)"
  DESCRIBE_APPLY="$(cat "$WORK/tagarn.apply.describe.json" 2>/dev/null || true)"
  MARKER_APPLY="$(cat "$WORK/tagarn.apply.marker.json" 2>/dev/null || true)"
  MANAGE_APPLY="$(cat "$WORK/tagarn.apply.manage.json" 2>/dev/null || true)"
  echo "  plan DescribeTheEstate:  ${DESCRIBE_PLAN:-<absent>}"
  echo "  apply DescribeTheEstate: ${DESCRIBE_APPLY:-<absent>}"
  echo "  apply WriteTheMarker:    ${MARKER_APPLY:-<absent>}"
  echo "  apply ManageTheEstate:   ${MANAGE_APPLY:-<absent>}"

  for label_json in "plan DescribeTheEstate:$DESCRIBE_PLAN" "apply DescribeTheEstate:$DESCRIBE_APPLY" "apply WriteTheMarker:$MARKER_APPLY"; do
    label="${label_json%%:*}"
    stmt="${label_json#*:}"
    if [ -z "$stmt" ]; then
      echo "FAIL: $label statement is absent" >&2
      FAILURES=$((FAILURES + 1))
      continue
    fi
    if ! echo "$stmt" | jq -e --arg star "$STAR_ARN" --arg bare "$BARE_ARN" \
        '(.Resource | type == "array") and (.Resource | index($star) != null) and (.Resource | index($bare) != null)' \
        > /dev/null 2>&1; then
      echo "FAIL: $label does not grant both $STAR_ARN and $BARE_ARN: $stmt" >&2
      FAILURES=$((FAILURES + 1))
    fi
  done

  if [ -z "$MANAGE_APPLY" ]; then
    echo "FAIL: apply ManageTheEstate statement is absent" >&2
    FAILURES=$((FAILURES + 1))
  else
    if ! echo "$MANAGE_APPLY" | jq -e --arg bare "$BARE_ARN" \
        '(.Resource | type == "array") and (.Resource | index($bare) == null)' \
        > /dev/null 2>&1; then
      echo "FAIL: apply ManageTheEstate unexpectedly carries the bare ARN $BARE_ARN too: $MANAGE_APPLY" >&2
      FAILURES=$((FAILURES + 1))
    fi
  fi
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "PASS: $SCRIPT_PATH's trust policy carries both subject forms under an immutable subject and only the plain form otherwise, all three policies carry the DiscoverTheAccount statement, the record store's GetParametersByPath is scoped to the path ARN rather than the leaf, and the CloudWatch Logs tag actions are granted on both log-group ARN forms."
  exit 0
else
  echo "FAIL: $FAILURES assertion(s) failed against $SCRIPT_PATH."
  exit 1
fi
