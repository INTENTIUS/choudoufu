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
# The account oidc-bootstrap.sh works in. Read here as a literal, the way the
# estate and the bucket are: a value taken from the script under test proves
# only that the script agrees with itself.
EXPECT_ACCOUNT="354867293429"
# The sidecar names one estate and one bucket, and the cases below check the
# file against these two literals rather than reading whatever is there. They
# are declared here, at the top, and not inside the case that first uses them:
# under `set -u` a later case reading an unset one kills the whole script, and
# see the EXIT trap below for what that used to look like from outside.
EXPECT_ESTATE="ci-pipelines-example"
EXPECT_BUCKET="choudoufu-records-354867293429-us-east-1"
PLAIN_SUBJECT="repo:${REPO}:*"
IMMUTABLE_PREFIX="repo:INTENTIUS@259705176/choudoufu@1332291567"
IMMUTABLE_SUBJECT="${IMMUTABLE_PREFIX}:*"

WORK="$(mktemp -d)"
# The status is saved and re-raised. An EXIT trap whose last command is `rm`
# hands `rm`'s status to whoever ran this script, so a selftest killed by
# `set -u` or `set -e` part way through - which is what happens when a case
# leaves a later one reading a variable it never got to set - exited 0 and
# read as a pass. Measured while writing #1381's cases.
# selftest_finished is set on the last line that runs when every case has run.
# Without it this script exited 0 when it was killed part way through - bash
# 3.2 hands the EXIT trap a status of 0 for a `set -u` or `set -e` abort, and
# the trap's own `rm` then finishes with 0 as well, so a selftest that never
# reached half its cases read as a pass from outside. Measured while writing
# #1381's cases, where a bootstrap that refused early left a later case
# reading a variable that case never got to set.
selftest_finished=0
# shellcheck disable=SC2154 # selftest_rc is assigned on the trap's first line
trap 'selftest_rc=$?
      rm -rf "$WORK"
      if [ "$selftest_finished" != 1 ]; then
        echo "FAIL: this selftest stopped before its last case, so most of it never ran. Read the output above for where." >&2
        exit 1
      fi
      exit $selftest_rc' EXIT
STUBDIR="$WORK/bin"
mkdir -p "$STUBDIR"

# aws stub: the only calls oidc-bootstrap.sh makes unconditionally (i.e. not
# behind its --dry-run "run()" wrapper) are the provider lookup, the record
# store bucket's head-bucket and, per role, a get-role existence check. Everything that would mutate
# AWS (create-role, put-role-policy, update-assume-role-policy) is behind
# `run()` and, in --dry-run, is only ever printed - the stub errors loudly
# if it is ever invoked, since that would mean --dry-run stopped being
# dry.
{
  echo '#!/usr/bin/env bash'
  # The log path is baked in rather than inherited, so the stub records even
  # when oidc-bootstrap.sh is sourced from a subshell with a trimmed
  # environment.
  echo "AWS_CALL_LOG=\"$WORK/aws-calls.log\""
  # What get-bucket-location answers, as the CLI would print it with
  # --output text --query LocationConstraint. A case that wants a bucket in
  # another region writes that region here. "None" is what a us-east-1 bucket
  # answers, since its LocationConstraint is null.
  echo "BUCKET_LOCATION_FILE=\"$WORK/bucket-location\""
  cat <<'AWSEOF'
printf '%s\n' "$*" >> "$AWS_CALL_LOG"
case "$1 $2" in
  "iam list-open-id-connect-providers")
    echo '{"OpenIDConnectProviderList":[{"Arn":"arn:aws:iam::354867293429:oidc-provider/token.actions.githubusercontent.com"}]}'
    exit 0
    ;;
  "s3api head-bucket")
    exit 0 # the record store bucket exists; the script only reads it
    ;;
  "s3api get-bucket-location")
    cat "$BUCKET_LOCATION_FILE"
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
} > "$STUBDIR/aws"
chmod +x "$STUBDIR/aws"
: > "$WORK/aws-calls.log"
# The bucket this example names is in us-east-1, which is the one region
# get-bucket-location does not name: its LocationConstraint is null, printed
# as "None" by --output text. Every case below runs against that answer
# unless it overwrites this file, so the ordinary path is the one with the
# empty-answer special case in it.
echo "None" > "$WORK/bucket-location"

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

echo "== case: each role's record store policy is the renderer's, full for apply and --read-only for plan and adopt (#1346, #1370) =="
# Until GitHub issue #1346 the record store was Parameter Store and this case
# pinned two hand-derived ssm: statements. The store is a bucket now, and its
# policy has one source: examples/record-store-bucket/iam/render-policy.sh,
# measured against real AWS in #1342. So what is pinned here is that the
# bootstrap did not grow a second copy:
#   - the apply policy carries the renderer's statements, every one, equal to
#     what the renderer prints for the estate and bucket the SIDECAR names
#     (read here independently of the script under test);
#   - the plan and adopt policies carry the --read-only rendering's
#     statements, every one, equal to what the renderer prints with that flag
#     (GitHub issue #1370). They used to carry no record store statement at
#     all, so a `live-plan` job that assumed the plan role would have failed
#     on its first record call; the real-AWS smoke never caught it because it
#     assumes the apply role for every job. Neither role writes a record:
#     `live-plan` reads the store and records are written after an apply
#     (internal/live/projection/writeback.go), and what the adopt Op writes is
#     two tags on the live resource, which WriteTheMarker already grants.
#   - and they carry none of the statements only the full rendering has, so
#     the read-only rendering being given to the plan role is not the full one
#     under another name;
#   - no policy grants ANY s3: or kms: action under a Sid the renderer did
#     not emit. Until #1379 this case compared only the statements whose Sid
#     the renderer emits, so a hand-written
#     {"Sid":"HandWrittenCopy","Action":["s3:*"],"Resource":"*"} appended to
#     the apply policy, and s3:GetObject/s3:GetObjectVersion on
#     arn:aws:s3:::*/* added to the plan policy, both passed;
#   - no policy grants an ssm: action any more;
#   - each policy fits IAM's 10,240-character inline limit, which the
#     rendered statements brought the apply policy closer to.
write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
runner="$WORK/run-recordstore.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run >"$WORK/recordstore.out" 2>"$WORK/recordstore.err"
cp "\$PLAN_POLICY"  "$WORK/recordstore.plan.json"
cp "\$ADOPT_POLICY" "$WORK/recordstore.adopt.json"
cp "\$APPLY_POLICY" "$WORK/recordstore.apply.json"
RUNEOF
chmod +x "$runner"
if ! PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL (recordstore): oidc-bootstrap.sh --dry-run exited non-zero. stderr:" >&2
  cat "$WORK/recordstore.err" >&2 2>/dev/null || true
  FAILURES=$((FAILURES + 1))
else
  SIDECAR="$(dirname "$SCRIPT_PATH")/../terraform/estate.chdf.hcl"
  RENDERER="$(dirname "$SCRIPT_PATH")/../../record-store-bucket/iam/render-policy.sh"
  # Read independently of oidc-bootstrap.sh. Until #1379 this used that
  # script's own sed expression character for character, so one broken regex
  # satisfied both sides and the comparison compared nothing. It splits on
  # the quote with awk instead, and then checks the two values against
  # EXPECT_ESTATE and EXPECT_BUCKET at the top of this file: the sidecar names
  # one estate and one bucket, and if either changes, that is where a reader
  # is told about it.
  WANT_ESTATE="$(awk -F'"' '$1 ~ /^estate[[:space:]]*=[[:space:]]*$/ {print $2; exit}' "$SIDECAR")"
  WANT_BUCKET="$(awk -F'"' '$1 ~ /^[[:space:]]*bucket[[:space:]]*=[[:space:]]*$/ {print $2; exit}' "$SIDECAR")"
  echo "  sidecar: estate=$WANT_ESTATE bucket=$WANT_BUCKET"
  if [ "$WANT_ESTATE" != "$EXPECT_ESTATE" ] || [ "$WANT_BUCKET" != "$EXPECT_BUCKET" ]; then
    echo "FAIL: $SIDECAR reads estate=$WANT_ESTATE bucket=$WANT_BUCKET, and this test expects estate=$EXPECT_ESTATE bucket=$EXPECT_BUCKET." >&2
    echo "  If the sidecar changed on purpose, change EXPECT_ESTATE/EXPECT_BUCKET here too." >&2
    FAILURES=$((FAILURES + 1))
  fi
  if [ -z "$WANT_ESTATE" ] || [ -z "$WANT_BUCKET" ]; then
    echo "FAIL: $SIDECAR does not name an estate and a record_store \"s3\" bucket" >&2
    FAILURES=$((FAILURES + 1))
  else
    # Rendered WITH --account, because that is what oidc-bootstrap.sh does
    # (#1381). Rendering without it here would make this comparison fail the
    # moment the bootstrap pins the account, which is the opposite of what
    # this case is for.
    WANT="$("$RENDERER" "$WANT_ESTATE" "$WANT_BUCKET" --account "$EXPECT_ACCOUNT" 2>/dev/null | jq -cS '.Statement')"
    SIDS="$(jq -c '[.[].Sid]' <<< "$WANT")"
    if [ "$(jq 'length' <<< "$SIDS")" -lt 5 ]; then
      echo "FAIL: the renderer printed fewer than five statements, so the comparisons below would prove little: $SIDS" >&2
      FAILURES=$((FAILURES + 1))
    fi
    # The read-only rendering (#1370), read the same way and just as
    # independently of the script under test.
    WANT_RO="$("$RENDERER" "$WANT_ESTATE" "$WANT_BUCKET" --account "$EXPECT_ACCOUNT" --read-only 2>/dev/null | jq -cS '.Statement')"
    SIDS_RO="$(jq -c '[.[].Sid]' <<< "$WANT_RO")"
    if [ "$(jq 'length' <<< "$SIDS_RO")" -lt 3 ]; then
      echo "FAIL: the --read-only renderer printed fewer than three statements, so the comparisons below would prove little: $SIDS_RO" >&2
      FAILURES=$((FAILURES + 1))
    fi
    # The two renderings have to actually differ, or "the plan role gets the
    # read-only one" is a sentence about nothing and every comparison below
    # passes whichever policy the bootstrap wrote.
    if [ "$WANT_RO" = "$WANT" ]; then
      echo "FAIL: the renderer prints the same policy with and without --read-only, so nothing below distinguishes the plan role's policy from the apply role's" >&2
      FAILURES=$((FAILURES + 1))
    fi
    # The Sids the FULL rendering has and the read-only one does not: what a
    # plan role must not be carrying.
    SIDS_WRITE_ONLY="$(jq -c --argjson ro "$SIDS_RO" '[.[] | select(. as $s | $ro | index($s) | not)]' <<< "$SIDS")"
    if [ "$(jq 'length' <<< "$SIDS_WRITE_ONLY")" = "0" ]; then
      echo "FAIL: every Sid the full rendering emits is also in the read-only one, so the check for write-only statements below cannot fail" >&2
      FAILURES=$((FAILURES + 1))
    fi
    GOT="$(jq -cS --argjson sids "$SIDS" '[.Statement[] | select(.Sid as $s | $sids | index($s))]' "$WORK/recordstore.apply.json")"
    echo "  rendered Sids:           $SIDS"
    echo "  --read-only Sids:        $SIDS_RO"
    echo "  full-rendering-only Sids: $SIDS_WRITE_ONLY"
    if [ "$GOT" != "$WANT" ]; then
      echo "FAIL: the apply policy's record store statements are not the renderer's output for $WANT_ESTATE / $WANT_BUCKET." >&2
      echo "  want: $WANT" >&2
      echo "  got:  $GOT" >&2
      FAILURES=$((FAILURES + 1))
    fi
    for role in plan adopt; do
      GOT_RO="$(jq -cS --argjson sids "$SIDS_RO" '[.Statement[] | select(.Sid as $s | $sids | index($s))]' "$WORK/recordstore.$role.json")"
      if [ "$GOT_RO" != "$WANT_RO" ]; then
        echo "FAIL: the $role policy's record store statements are not the renderer's --read-only output for $WANT_ESTATE / $WANT_BUCKET (#1370)." >&2
        echo "  A plan job under this role opens the estate's record store, and without these statements its first record call is denied." >&2
        echo "  want: $WANT_RO" >&2
        echo "  got:  $GOT_RO" >&2
        FAILURES=$((FAILURES + 1))
      fi
      n="$(jq --argjson sids "$SIDS_WRITE_ONLY" '[.Statement[] | select(.Sid as $s | $sids | index($s))] | length' "$WORK/recordstore.$role.json")"
      if [ "$n" != "0" ]; then
        echo "FAIL: the $role policy carries $n statement(s) the --read-only rendering leaves out ($SIDS_WRITE_ONLY); only the apply role writes records" >&2
        FAILURES=$((FAILURES + 1))
      fi
    done
    # And the plainest form of the same thing, stated in actions rather than
    # in Sids: neither role may be allowed a write, a delete or a tag write
    # on the bucket under ANY Sid.
    for role in plan adopt; do
      WRITES="$(jq -c '[ .Statement[]
        | select(.Effect == "Allow")
        | [.Action] | flatten | .[]
        | select(type == "string" and (. == "s3:PutObject" or . == "s3:PutObjectTagging" or . == "s3:DeleteObject" or . == "s3:DeleteObjectVersion" or . == "kms:GenerateDataKey"))
      ] | unique' "$WORK/recordstore.$role.json")"
      echo "  $role policy: record store write actions allowed: $WRITES"
      if [ "$(jq 'length' <<< "$WRITES")" != "0" ]; then
        echo "FAIL: the $role policy allows $WRITES on the record store; a role that plans changes nothing in the bucket (#1370)" >&2
        FAILURES=$((FAILURES + 1))
      fi
    done
    # The control for the line above: the apply policy DOES allow them, so
    # the assertion is measuring the difference between the two renderings
    # and not a list of actions nothing ever grants.
    APPLY_WRITES="$(jq -c '[ .Statement[]
      | select(.Effect == "Allow")
      | [.Action] | flatten | .[]
      | select(type == "string" and (. == "s3:PutObject" or . == "s3:DeleteObject"))
    ] | unique' "$WORK/recordstore.apply.json")"
    echo "  apply policy: record store write actions allowed: $APPLY_WRITES"
    if [ "$(jq 'length' <<< "$APPLY_WRITES")" != "2" ]; then
      echo "FAIL: the apply policy does not allow both s3:PutObject and s3:DeleteObject ($APPLY_WRITES), so the two checks above are not measuring anything" >&2
      FAILURES=$((FAILURES + 1))
    fi

    # The other direction, and the one #1379 found missing: no policy may
    # grant an s3: or kms: action under any Sid but the renderer's. Comparing
    # only the Sids the renderer emits leaves every other statement in the
    # file unread, which is how a hand-written "s3:*" on "*" passed. The
    # apply role may carry the full rendering's Sids; plan and adopt may
    # carry the read-only rendering's and no other.
    for role in plan adopt apply; do
      case "$role" in
        apply) allowed="$SIDS" ;;
        *)     allowed="$SIDS_RO" ;;
      esac
      STRAY="$(jq -c --argjson sids "$allowed" '
        [ .Statement[]
          | select([.Action] | flatten | map(select(type == "string" and (startswith("s3:") or startswith("kms:")))) | length > 0)
          | select((.Sid // "") as $s | ($sids | index($s)) == null)
        ]' "$WORK/recordstore.$role.json")"
      STRAY_N="$(jq 'length' <<< "$STRAY")"
      echo "  $role policy: $STRAY_N s3:/kms: statement(s) outside the renderer's Sids"
      if [ "$STRAY_N" != "0" ]; then
        echo "FAIL: the $role policy grants s3: or kms: actions under $STRAY_N Sid(s) the renderer did not emit for $WANT_ESTATE / $WANT_BUCKET." >&2
        echo "  The record store policy has one source, examples/record-store-bucket/iam/render-policy.sh." >&2
        echo "  stray: $STRAY" >&2
        FAILURES=$((FAILURES + 1))
      fi
    done
  fi
  for role in plan adopt apply; do
    if jq -e '[.Statement[].Action | if type == "array" then .[] else . end | select(startswith("ssm:"))] | length > 0' "$WORK/recordstore.$role.json" > /dev/null; then
      echo "FAIL: the $role policy still grants an ssm: action. Parameter Store is retired as a record store (#1346) and this estate manages no parameter." >&2
      FAILURES=$((FAILURES + 1))
    fi
    size="$(jq -c . "$WORK/recordstore.$role.json" | tr -d '[:space:]' | wc -c | tr -d ' ')"
    echo "  $role policy: $size characters (IAM's inline limit is 10240)"
    if [ "$size" -gt 10240 ]; then
      echo "FAIL: the $role policy is $size characters, over IAM's 10,240 inline limit; put-role-policy would refuse it" >&2
      FAILURES=$((FAILURES + 1))
    fi
  done
fi

echo "== case: the bucket owner is pinned, on the head-bucket and in the policy (#1381) =="
# A bucket name is global, and this one embeds the account id in plain sight,
# so if the real bucket is ever deleted anyone can create the name in their
# own account, admit the apply role with a bucket policy, satisfy the three
# asserted settings, and receive the next apply's records. The records hold
# secrets. Two things stop it and this case holds both:
#   - the head-bucket that decides whether to proceed carries
#     --expected-bucket-owner, so a bucket owned by anyone else answers 403
#     and the bootstrap stops;
#   - every Allow the apply policy carries requires aws:ResourceAccount, so
#     the role reaches no bucket outside the account even if it is pointed at
#     one.
# The literal below is tied to the bucket name, which is derived from the same
# account, so the two cannot drift apart silently.
case "$EXPECT_BUCKET" in
  *"$EXPECT_ACCOUNT"*) ;;
  *)
    echo "FAIL: EXPECT_ACCOUNT ($EXPECT_ACCOUNT) is not the account in EXPECT_BUCKET ($EXPECT_BUCKET)" >&2
    FAILURES=$((FAILURES + 1))
    ;;
esac
HEAD_CALLS="$(grep -c '^s3api head-bucket' "$WORK/aws-calls.log" || true)"
echo "  head-bucket calls: $HEAD_CALLS, first: $(grep -m1 '^s3api head-bucket' "$WORK/aws-calls.log" || echo '<none>')"
if [ "$HEAD_CALLS" = "0" ]; then
  echo "FAIL: oidc-bootstrap.sh made no head-bucket call at all, so there is nothing to pin" >&2
  FAILURES=$((FAILURES + 1))
else
  UNPINNED="$(grep '^s3api head-bucket' "$WORK/aws-calls.log" | grep -vc -- "--expected-bucket-owner $EXPECT_ACCOUNT" || true)"
  if [ "$UNPINNED" != "0" ]; then
    echo "FAIL: $UNPINNED head-bucket call(s) carried no --expected-bucket-owner $EXPECT_ACCOUNT." >&2
    echo "  Without it head-bucket succeeds against a bucket of this name in any account," >&2
    echo "  and this script then writes the apply role's policy against that bucket." >&2
    FAILURES=$((FAILURES + 1))
  fi
fi
# All three policies, not just the apply one: since #1370 the plan and adopt
# roles carry s3: Allow statements too, and a read that is not pinned to the
# owner reads a stranger's bucket of the same name just as happily as a write
# fills one.
for role in plan adopt apply; do
  ACCOUNTLESS="$(jq -c '[.Statement[] | select(.Effect == "Allow") | select([.Action] | flatten | map(select(type == "string" and startswith("s3:"))) | length > 0) | select(.Condition.StringEquals["aws:ResourceAccount"] != "'"$EXPECT_ACCOUNT"'") | .Sid]' "$WORK/recordstore.$role.json")"
  PINNED_N="$(jq '[.Statement[] | select(.Effect == "Allow") | select([.Action] | flatten | map(select(type == "string" and startswith("s3:"))) | length > 0)] | length' "$WORK/recordstore.$role.json")"
  echo "  $role policy: $PINNED_N s3: Allow statement(s), unpinned: $ACCOUNTLESS"
  # A policy with no s3: Allow statement at all would pass the check below
  # with nothing in it, which is exactly what the plan policy looked like
  # before #1370.
  if [ "$PINNED_N" = "0" ]; then
    echo "FAIL: the $role policy has no s3: Allow statement at all, so the owner pin below is checked over nothing. Every one of the three roles opens the estate's record store." >&2
    FAILURES=$((FAILURES + 1))
  fi
  if [ "$(jq 'length' <<< "$ACCOUNTLESS")" != "0" ]; then
    echo "FAIL: the $role policy has s3: Allow statement(s) that do not require aws:ResourceAccount = $EXPECT_ACCOUNT: $ACCOUNTLESS" >&2
    FAILURES=$((FAILURES + 1))
  fi
done

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

echo "== case: the bucket's own region decides, and a mismatch stops the run (#1381) =="
# --region took whatever it was given and nothing compared it to the bucket.
# The AWS_REGION repository variable it sets is what every generated workflow
# runs in, and the AWS SDK for Go does not follow the redirect S3 answers a
# cross-region request with, so the first record call of the first run fails
# with an error about an endpoint and nothing about this variable.
#
# expect_region_refusal <bucket-location> <--region argument>: run the
# bootstrap under the stubs and require it to stop, saying both regions.
expect_region_refusal() {
  local location="$1" asked="$2" name="$3"
  echo "$location" > "$WORK/bucket-location"
  write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
  local runner="$WORK/run-$name.sh"
  cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run --region $asked >"$WORK/$name.out" 2>"$WORK/$name.err"
RUNEOF
  chmod +x "$runner"
  if PATH="$STUBDIR:$PATH" bash "$runner"; then
    echo "FAIL: the bucket answered $location and the run asked for --region $asked, and the bootstrap went ahead anyway." >&2
    echo "  It would have set AWS_REGION to a region the record store bucket is not in." >&2
    FAILURES=$((FAILURES + 1))
    return
  fi
  local said; said="$(cat "$WORK/$name.err" 2>/dev/null || true)"
  echo "  $location vs --region $asked: refused, saying:"
  sed 's/^/    /' "$WORK/$name.err" | tail -8
  # Both regions by name. A refusal that names neither leaves the reader
  # guessing which of the two to change.
  local want_bucket="$location"
  [ "$want_bucket" != "None" ] || want_bucket="us-east-1"
  if ! grep -q "$want_bucket" <<< "$said" || ! grep -q "$asked" <<< "$said"; then
    echo "FAIL: the refusal does not name both $want_bucket (the bucket) and $asked (the variable it was about to set)" >&2
    FAILURES=$((FAILURES + 1))
  fi
}
# The bucket is in us-east-1 (a null LocationConstraint) and the run asks for
# us-west-2. This is the reported shape.
expect_region_refusal None us-west-2 regionmismatch
# And the other way round, so the us-east-1 special case is not the only path
# tested: a bucket that names its region, against the terraform default.
expect_region_refusal eu-west-1 us-east-1 regionmismatch2
# The control. Put back the answer the real bucket gives and pass the region
# it is in explicitly: the bootstrap has to go through. Without this line a
# bootstrap that refused every region at all would pass the two above.
echo "None" > "$WORK/bucket-location"
write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
runner="$WORK/run-regionok.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
source "$SCRIPT_PATH" --dry-run --region us-east-1 >"$WORK/regionok.out" 2>"$WORK/regionok.err"
RUNEOF
chmod +x "$runner"
if PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "  control: --region us-east-1 against a us-east-1 bucket went through"
else
  echo "FAIL: the bootstrap refused --region us-east-1 against a bucket that is in us-east-1. stderr:" >&2
  cat "$WORK/regionok.err" >&2 2>/dev/null || true
  FAILURES=$((FAILURES + 1))
fi
# And the read itself is pinned to the owner, for head-bucket's reason: a
# bucket of this name in someone else's account would otherwise answer with
# its own region and this comparison would pass on a stranger's bucket.
LOC_CALLS="$(grep -c '^s3api get-bucket-location' "$WORK/aws-calls.log" || true)"
echo "  get-bucket-location calls: $LOC_CALLS"
if [ "$LOC_CALLS" = "0" ]; then
  echo "FAIL: oidc-bootstrap.sh never read the bucket's region, so nothing compared it to AWS_REGION" >&2
  FAILURES=$((FAILURES + 1))
else
  UNPINNED_LOC="$(grep '^s3api get-bucket-location' "$WORK/aws-calls.log" | grep -vc -- "--expected-bucket-owner $EXPECT_ACCOUNT" || true)"
  if [ "$UNPINNED_LOC" != "0" ]; then
    echo "FAIL: $UNPINNED_LOC get-bucket-location call(s) carried no --expected-bucket-owner $EXPECT_ACCOUNT" >&2
    FAILURES=$((FAILURES + 1))
  fi
fi

echo "== case: a renderer that fails stops the bootstrap, by name (#1381) =="
# The renderer used to run inside a command substitution nested in the
# heredoc that builds the apply policy. An exit there kills that subshell and
# nothing else, so the bootstrap carried on with no record store statements
# and was stopped - when it was stopped at all - by jq failing to parse an
# empty string, which says nothing about the renderer. The stub below is a
# renderer that fails the way the real one does for a bad argument.
RENDERER_STUB="$STUBDIR/failing-render-policy.sh"
cat > "$RENDERER_STUB" <<'RENDEOF'
#!/usr/bin/env bash
echo "not a bucket name: $2" >&2
exit 2
RENDEOF
chmod +x "$RENDERER_STUB"
write_gh_stub '{"use_default":true,"use_immutable_subject":false,"sub_claim_prefix":null}'
runner="$WORK/run-brokenrenderer.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
export POLICY_RENDERER="$RENDERER_STUB"
source "$SCRIPT_PATH" --dry-run >"$WORK/brokenrenderer.out" 2>"$WORK/brokenrenderer.err"
RUNEOF
chmod +x "$runner"
if PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL: the record store policy renderer exited 2 and the bootstrap finished anyway." >&2
  echo "  It would have written the apply role a policy with no record store statements in it." >&2
  FAILURES=$((FAILURES + 1))
else
  echo "  refused, saying:"
  sed 's/^/    /' "$WORK/brokenrenderer.err" | tail -8
  if ! grep -q "failing-render-policy.sh" "$WORK/brokenrenderer.err"; then
    echo "FAIL: the refusal does not name the renderer, so a reader has to guess which of the several things this script runs failed" >&2
    FAILURES=$((FAILURES + 1))
  fi
  # A jq parse error is what used to stop this, and it is not an answer.
  if grep -qi "jq: error\|parse error" "$WORK/brokenrenderer.err"; then
    echo "FAIL: the bootstrap stopped on a jq parse error rather than on the renderer's own exit status" >&2
    FAILURES=$((FAILURES + 1))
  fi
fi
# The second half, and the one only the exit status catches: a renderer that
# prints a whole, parseable policy and THEN fails. Nothing downstream can tell
# that document from a good one - jq parses it, it has statements in it, it
# would go straight into the apply role's policy - so if the exit status is
# not read, a renderer that died half way through its work is indistinguishable
# from one that finished.
cat > "$RENDERER_STUB" <<'RENDEOF'
#!/usr/bin/env bash
echo '{"Version":"2012-10-17","Statement":[{"Sid":"ListOwnNamespaces","Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::b"}]}'
echo "render-policy.sh: ran out of something half way through" >&2
exit 2
RENDEOF
chmod +x "$RENDERER_STUB"
runner="$WORK/run-lyingrenderer.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
export POLICY_RENDERER="$RENDERER_STUB"
source "$SCRIPT_PATH" --dry-run >"$WORK/lyingrenderer.out" 2>"$WORK/lyingrenderer.err"
RUNEOF
chmod +x "$runner"
if PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL: the renderer printed a parseable policy and exited 2, and the bootstrap took the policy anyway." >&2
  echo "  Only the exit status distinguishes that from a renderer that finished." >&2
  FAILURES=$((FAILURES + 1))
else
  echo "  parseable-then-failed render: refused, saying:"
  sed 's/^/    /' "$WORK/lyingrenderer.err" | tail -6
  if ! grep -q "failing-render-policy.sh" "$WORK/lyingrenderer.err"; then
    echo "FAIL: the refusal does not name the renderer" >&2
    FAILURES=$((FAILURES + 1))
  fi
  if ! grep -q "ran out of something half way through" "$WORK/lyingrenderer.err"; then
    echo "FAIL: the refusal does not pass on what the renderer said, which is the only account of what went wrong" >&2
    FAILURES=$((FAILURES + 1))
  fi
fi
# The third: a renderer that exits 0 and prints a document with no
# statements in it. The exit status alone would let that through, and the
# apply role would come out with no access to the record store at all.
cat > "$RENDERER_STUB" <<'RENDEOF'
#!/usr/bin/env bash
echo '{"Version":"2012-10-17","Statement":[]}'
RENDEOF
chmod +x "$RENDERER_STUB"
runner="$WORK/run-emptyrenderer.sh"
cat > "$runner" <<RUNEOF
#!/usr/bin/env bash
set -euo pipefail
export POLICY_RENDERER="$RENDERER_STUB"
source "$SCRIPT_PATH" --dry-run >"$WORK/emptyrenderer.out" 2>"$WORK/emptyrenderer.err"
RUNEOF
chmod +x "$runner"
if PATH="$STUBDIR:$PATH" bash "$runner"; then
  echo "FAIL: the renderer printed a policy with no statements and the bootstrap finished anyway." >&2
  FAILURES=$((FAILURES + 1))
else
  echo "  empty render: refused, saying:"
  sed 's/^/    /' "$WORK/emptyrenderer.err" | tail -4
  if ! grep -q "failing-render-policy.sh" "$WORK/emptyrenderer.err"; then
    echo "FAIL: the empty-render refusal does not name the renderer" >&2
    FAILURES=$((FAILURES + 1))
  fi
fi
# The control for both halves is the record store case further up: it runs
# with POLICY_RENDERER unset, so it is the real renderer going through, and
# it compares the apply policy statement for statement against that
# renderer's own output. Without it a bootstrap that refused every render
# would pass the two cases here.
rm -f "$RENDERER_STUB"

selftest_finished=1

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "PASS: $SCRIPT_PATH's trust policy carries both subject forms under an immutable subject and only the plain form otherwise, all three policies carry the DiscoverTheAccount statement, the apply role's record store policy is the renderer's output for the sidecar's estate and bucket and the plan and adopt roles' is its --read-only output with no write, delete or tag-write in it, the bucket owner is pinned on the head-bucket and on every s3: Allow of all three, the CloudWatch Logs tag actions are granted on both log-group ARN forms, a region that is not the bucket's own stops the run with both regions named, and a renderer that fails or prints nothing stops it by name."
  exit 0
else
  echo "FAIL: $FAILURES assertion(s) failed against $SCRIPT_PATH."
  exit 1
fi
