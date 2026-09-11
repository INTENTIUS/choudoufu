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

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "PASS: $SCRIPT_PATH's trust policy carries both subject forms under an immutable subject and only the plain form otherwise."
  exit 0
else
  echo "FAIL: $FAILURES assertion(s) failed against $SCRIPT_PATH."
  exit 1
fi
