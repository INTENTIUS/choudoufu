#!/usr/bin/env bash
# scripts/contribute.sh: one gauntlet worker run, under your own key.
#
#   just contribute [max-usd]
#
# Picks the next unit (`go run ./tools/gauntlet next`), makes a fresh worktree
# off local main, and runs Claude Code headless under the gauntlet-worker
# brief (.claude/agents/gauntlet-worker.md) with a dollar ceiling. The worker
# opens a pull request against INTENTIUS/choudoufu and stops; it never merges.
#
# Needs: claude (Claude Code CLI) with ANTHROPIC_API_KEY or a logged-in
# session, gh (logged in to an account that can push to your fork), Docker,
# the AWS CLI, a stock terraform on PATH, Go, and a materialised .corpus
# (`just corpus-fetch`). Set CONTRIBUTE_REMOTE to the git remote the PR branch
# is pushed to (default: origin).
#
# Env:
#   CONTRIBUTE_MAX_USD   dollar ceiling for the run (default 25; the first arg overrides)
#   CONTRIBUTE_MODEL     model for the worker (default: Claude Code's default)
#   CONTRIBUTE_REMOTE    git remote to push the PR branch to (default origin)
#   CONTRIBUTE_UNIT      take this unit id (<estate>/<stage>) instead of the next one
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MAX_USD="${1:-${CONTRIBUTE_MAX_USD:-25}}"
REMOTE="${CONTRIBUTE_REMOTE:-origin}"

for tool in claude gh docker aws go; do
  command -v "$tool" >/dev/null 2>&1 || { echo "contribute: $tool is not on PATH" >&2; exit 2; }
done
command -v terraform >/dev/null 2>&1 || command -v tofu >/dev/null 2>&1 || { echo "contribute: a stock terraform or tofu binary is needed for cold deploys" >&2; exit 2; }
docker info >/dev/null 2>&1 || { echo "contribute: docker is not running" >&2; exit 2; }

cd "$ROOT"
UNIT_JSON="$(go run ./tools/gauntlet next -json -n 1)"
if [ -z "$UNIT_JSON" ] || [ "$UNIT_JSON" = "nothing to do: every estate in the set is clear" ]; then
  echo "contribute: nothing to do, every estate is clear"; exit 0
fi
UNIT="${CONTRIBUTE_UNIT:-$(printf '%s' "$UNIT_JSON" | python3 -c 'import json,sys; print(json.loads(sys.stdin.readline())["id"])')}"
ESTATE="${UNIT%%/*}"; STAGE="${UNIT##*/}"
BRANCH="gauntlet/${ESTATE}-${STAGE}"
WT="$ROOT/../wt/contribute-${ESTATE}-${STAGE}"

if git -C "$ROOT" show-ref --verify --quiet "refs/heads/$BRANCH"; then
  echo "contribute: branch $BRANCH already exists locally; finish or delete it first" >&2; exit 2
fi
git -C "$ROOT" worktree add "$WT" -b "$BRANCH" main >/dev/null
git -C "$WT" submodule update --init site/themes/hugo-book >/dev/null 2>&1 || true
echo "contribute: unit $UNIT in $WT (branch $BRANCH), ceiling \$$MAX_USD"

PROMPT="You are the gauntlet worker. Read .claude/agents/gauntlet-worker.md and follow it exactly for unit ${UNIT}. You are already in a worktree on branch ${BRANCH}; do not create another. Push the branch to the git remote '${REMOTE}' and open the pull request against INTENTIUS/choudoufu main with gh. Stop when the pull request exists or when the brief says to stop."

ARGS=(--print --dangerously-skip-permissions --max-budget-usd "$MAX_USD" --agent gauntlet-worker)
if [ -n "${CONTRIBUTE_MODEL:-}" ]; then ARGS+=(--model "$CONTRIBUTE_MODEL"); fi

( cd "$WT" && claude "${ARGS[@]}" "$PROMPT" )
rc=$?
echo "contribute: worker exited $rc; branch $BRANCH, worktree $WT (remove with: git worktree remove $WT)"
exit "$rc"
