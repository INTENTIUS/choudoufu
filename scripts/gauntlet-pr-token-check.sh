#!/usr/bin/env bash
# Issue #496: can GAUNTLET_PR_TOKEN push to this repository?
#
# Run by .github/workflows/gauntlet.yml before the five-hour measurement.
#
# The question is asked the way the PR step's push asks it: a dry-run push
# of HEAD to a probe ref, which makes git request the git-receive-pack
# service - GitHub's push authorization - and sends nothing, creates
# nothing. Nothing weaker answers it. The first version of this script
# asked the repository what permission the token's LOGIN holds
# (collaborators/<login>/permission), and a login with admin behind a token
# that cannot push returned "usable" - on 2026-09-22, runs 35684452776 and
# 35705549497, both refused on the push itself two steps later.
#
# Two details keep the probe honest:
#   - actions/checkout leaves http.https://github.com/.extraheader set to
#     GITHUB_TOKEN's basic auth, and that header wins over credentials in
#     the URL, so an unusable PAT probes as GITHUB_TOKEN and passes. An
#     empty -c value resets the list; proven both ways on 2026-09-22.
#   - credential.helper is blanked so nothing cached answers instead.
#
# Writes two GitHub Actions outputs:
#   usable=true|false   the PR step uses the secret only when true
#   reason=...          one line, quoted in the PR body and the job summary
#
# Always exits 0: an unusable token is a fact the workflow routes around
# (GITHUB_TOKEN fallback), not a reason to skip the measurement. The one
# thing this script must never do is print `usable=true` for a token the
# push would refuse; live/gauntlet_pr_token_test.go drives every branch
# against stubbed gh and git to hold it to that.
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
  emit false "the token does not authenticate: $(printf '%s' "$login" | grep -oE 'HTTP [0-9]{3}[^"]*|"message": *"[^"]*"' | head -1)"
if [ -z "$login" ]; then
  emit false "the token authenticates but gh api user returned no login"
fi

probe_err="$(GIT_TERMINAL_PROMPT=0 git -c credential.helper= -c http.https://github.com/.extraheader= \
  push --dry-run --porcelain "https://x-access-token:${PAT}@github.com/${repo}.git" \
  HEAD:refs/heads/gauntlet/token-probe 2>&1 >/dev/null)"
probe_rc=$?
# The token is in the URL; never let it reach a log line.
probe_err="$(printf '%s' "$probe_err" | sed "s#${PAT}#***#g" | grep -v '^\s*$' | head -1)"

if [ "$probe_rc" -eq 0 ]; then
  emit true "authenticates as $login and GitHub authorizes a push to $repo (dry-run push, git-receive-pack)"
fi
emit false "the token authenticates as $login but GitHub refuses it a push to $repo (dry-run push: ${probe_err:-exit $probe_rc}). lex00 having write on the repository does not help: the token itself must grant Contents write on $repo"
