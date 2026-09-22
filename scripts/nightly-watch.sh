#!/usr/bin/env bash
# Issue #1316: keep one open issue per red nightly workflow.
#
# Driven by .github/workflows/nightly-watch.yml on workflow_run completion,
# with the run described in the environment (REPO, WF_NAME, RUN_ID, RUN_URL,
# CONCLUSION, HEAD_SHA; RUN_EVENT is informational). Every GitHub call goes
# through `gh` on PATH, which is what lets live/nightly_watch_test.go stand
# a stub in front of it and assert what this script would do for each
# conclusion, without a repository to do it to.
#
#   failure (or timed_out / startup_failure), no open issue  -> issue create
#   failure, open issue                                     -> issue comment
#   success, open issue                                     -> comment + close
#   success, no open issue                                  -> nothing
#   anything else (cancelled, skipped, ...)                 -> nothing
#
# The issue title is "nightly red: <workflow name>" and the label is
# nightly-red; both are how the next run finds the issue again, so neither
# is free to change without the search below changing with it.
set -euo pipefail

: "${REPO:?}" "${WF_NAME:?}" "${RUN_ID:?}" "${RUN_URL:?}" "${CONCLUSION:?}" "${HEAD_SHA:?}"

label="nightly-red"
title="nightly red: $WF_NAME"
today="$(date -u +%Y-%m-%d)"
short_sha="${HEAD_SHA:0:10}"

# Exact-title match among open issues carrying the label. `--search` is
# GitHub's text search and matches loosely; the jq select is what makes it
# exact, so two workflows whose names share a word cannot share an issue.
existing="$(gh issue list -R "$REPO" --state open --label "$label" --search "\"$title\" in:title" \
  --json number,title --jq ".[] | select(.title == \"$title\") | .number" | head -1 || true)"

case "$CONCLUSION" in
  success)
    if [ -z "$existing" ]; then
      echo "$WF_NAME is green and no nightly-red issue is open; nothing to do"
      exit 0
    fi
    gh issue comment "$existing" -R "$REPO" --body "Green again on $today: $RUN_URL ($short_sha). Closed by nightly-watch."
    gh issue close "$existing" -R "$REPO" --reason completed
    echo "closed #$existing: $WF_NAME is green again"
    ;;
  failure|timed_out|startup_failure)
    # The failed jobs and their failed step names, so the issue says where
    # to look without anyone opening the log first. Best effort: a run the
    # token cannot read still gets an issue, with the link.
    failed="$(gh run view "$RUN_ID" -R "$REPO" --json jobs \
      --jq '.jobs[] | select(.conclusion != "success" and .conclusion != "skipped" and .conclusion != null) | "- job " + .name + ": " + .conclusion + (([.steps[]? | select(.conclusion == "failure") | .name] | if length > 0 then " (failed step: " + join(", ") + ")" else "" end))' 2>/dev/null || true)"
    [ -n "$failed" ] || failed="- (the run's jobs could not be read; open the run)"
    entry="$today: $WF_NAME run [$RUN_ID]($RUN_URL) concluded **$CONCLUSION** on $short_sha.

$failed

Read the run's own verdict lines, not this note. This issue stays open while the workflow is red and is closed by nightly-watch on its next green."
    if [ -n "$existing" ]; then
      gh issue comment "$existing" -R "$REPO" --body "$entry"
      echo "commented on #$existing: $WF_NAME red again"
      exit 0
    fi
    gh label create "$label" -R "$REPO" --force \
      --description "a scheduled workflow is failing; opened and closed by nightly-watch (#1316)" \
      --color B60205 > /dev/null
    intro="The scheduled workflow **$WF_NAME** failed. Opened by nightly-watch (.github/workflows/nightly-watch.yml, issue #1316) so that a red nightly is a thing the issue list says, not only a thing the Actions page knows. Each further red night adds a comment; the next green closes this."
    url="$(gh issue create -R "$REPO" --title "$title" --label "$label" --body "$intro

$entry")"
    echo "opened $url: $WF_NAME is red"
    ;;
  *)
    echo "$WF_NAME concluded $CONCLUSION; nothing to report"
    ;;
esac
