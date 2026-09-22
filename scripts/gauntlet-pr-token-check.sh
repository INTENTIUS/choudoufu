#!/usr/bin/env bash
# Issue #496: can GAUNTLET_PR_TOKEN push to this repository?
#
# Run by .github/workflows/gauntlet.yml before the five-hour measurement.
# The secret's second failure (2026-09-13 to 2026-09-21) was a fine-grained
# PAT issued with the USER as resource owner rather than the INTENTIUS
# organization: it authenticates as lex00 and is refused on the push with
# HTTP 403. `gh api user` cannot tell that token from a good one; asking the
# repository what permission that login holds can, and does so in seconds.
#
# Writes two GitHub Actions outputs:
#   usable=true|false   the PR step uses the secret only when true
#   reason=...          one line, quoted in the PR body and the job summary
#
# Always exits 0: an unusable token is a fact the workflow routes around
# (GITHUB_TOKEN fallback), not a reason to skip the measurement. The one
# thing this script must never do is print `usable=true` for a token the
# push would refuse; live/gauntlet_pr_token_test.go drives every branch
# below against a stubbed `gh` to hold it to that.
set -u

out="${GITHUB_OUTPUT:-/dev/stdout}"
summary="${GITHUB_STEP_SUMMARY:-}"
repo="${GITHUB_REPOSITORY:-INTENTIUS/choudoufu}"

emit() {
  # $1 usable, $2 reason
  printf 'usable=%s\n' "$1" >> "$out"
  printf 'reason=%s\n' "$2" >> "$out"
  if [ "$1" = true ]; then
    printf 'GAUNTLET_PR_TOKEN: usable (%s)\n' "$2"
  else
    printf '::warning title=GAUNTLET_PR_TOKEN is not usable::%s - the verdicts PR will be opened with GITHUB_TOKEN and its CI run will need a human approval (issue #948). Fix: a fine-grained PAT with resource owner INTENTIUS, repository access to %s, Contents and Pull requests read/write; then `gh secret set GAUNTLET_PR_TOKEN -R %s` (issue #496).\n' "$2" "$repo" "$repo"
    if [ -n "$summary" ]; then
      printf '## GAUNTLET_PR_TOKEN is not usable\n\n%s\n\nThe verdicts PR is opened with GITHUB_TOKEN instead; approve its CI run by hand (#948) until the secret is reissued with resource owner INTENTIUS (#496).\n' "$2" >> "$summary"
    fi
  fi
  exit 0
}

if [ -z "${PAT:-}" ]; then
  emit false "GAUNTLET_PR_TOKEN is not set"
fi

login="$(GH_TOKEN="$PAT" gh api user -q .login 2>&1)" || \
  emit false "the token does not authenticate: $(printf '%s' "$login" | head -1)"
if [ -z "$login" ]; then
  emit false "the token authenticates but gh api user returned no login"
fi

perm="$(GH_TOKEN="$PAT" gh api "repos/$repo/collaborators/$login/permission" -q .permission 2>&1)" || \
  emit false "the token authenticates as $login but cannot read $repo ($(printf '%s' "$perm" | head -1)); a fine-grained PAT issued under the user rather than the INTENTIUS organization looks exactly like this"

case "$perm" in
  admin|maintain|write) emit true "authenticates as $login with '$perm' on $repo" ;;
  *) emit false "the token authenticates as $login with '$perm' on $repo; pushing gauntlet/nightly needs write" ;;
esac
