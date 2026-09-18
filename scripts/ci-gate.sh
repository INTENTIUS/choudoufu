#!/usr/bin/env bash
# scripts/ci-gate.sh: run the CI gate and leave behind a result that cannot
# be mistaken for a stale one. Fixes #519.
#
# The old idiom, spelled out by hand in every worker's session:
#   { just ci; } > ci.out 2>&1; echo $? > ci.rc
# is one shell command end to end. Kill the shell between the two halves, or
# before either runs - a SIGTERM from a tool timeout under load, say - and an
# EARLIER run's ci.rc survives untouched, reading exactly like a fresh pass.
# Found for real on 2026-08-28/29 by two workers independently, one of them
# resolving PR #506. Same family as #413 and #509: a stamp that reads as
# evidence without being one.
#
# This script closes it two ways:
#   1. `run` deletes ci.rc/ci.out/ci.meta BEFORE starting the gate, so a kill
#      anywhere in the run leaves no readable gate at all - never a stale
#      one. Cheapest correct fix, and it is enough on its own for "killed
#      mid-run".
#   2. `run` also records the HEAD sha the run actually tested, in ci.meta,
#      written LAST and atomically (temp file + rename). `check` refuses
#      unless ci.meta exists AND names the CURRENT HEAD, so a `ci.rc` that
#      is genuinely complete but for an OLDER commit in the same worktree -
#      more work landed, nobody re-ran the gate - is rejected too. Deleting
#      first does not catch that case; this does.
#
# `wait` (#1307) closes the third: the gap between deciding to wait and the
# run getting far enough to delete anything. CLAUDE.md used to document
#   while [ ! -f ci.rc ]; do sleep 15; done; echo "ci.rc=$(cat ci.rc)"
# on the strength of point 1 above. Point 1 holds only once `run` has
# started. Launch that loop alongside `run` rather than after it - which is
# the only reason to launch it at all - and in the window before `run`'s
# `rm -f` the loop matches the PREVIOUS run's ci.rc and returns a stale
# green on its first iteration. Gate files are routinely left in a worktree
# on purpose (every worker is told to leave them for the orchestrator), so
# the file is usually there. Hit for real while landing #1141 (PR #1298),
# and caught only because `check` was run afterwards and said NO GATE.
#
# Deleting the files earlier, or telling the waiter to `rm -f ci.rc` first,
# only narrows that window: the race is between two processes and existence
# still cannot say whose file it read. `wait` keys on identity instead - the
# same discrimination `check` already makes - and returns only for a gate
# that names the current HEAD *and* is not the one that was already sitting
# there when the wait began.
#
# Usage:
#   scripts/ci-gate.sh run [-- CMD...]
#       Delete any existing ci.rc/ci.out/ci.meta, run CMD (default: `just
#       ci`), and write a fresh gate. Exits with CMD's own exit code, so
#       `scripts/ci-gate.sh run` in the foreground behaves exactly like
#       the old `{ just ci; } > ci.out 2>&1; echo $? > ci.rc` did, plus the
#       new files. Many minutes - wait for it in the foreground.
#   scripts/ci-gate.sh check
#       Verify the gate in the current worktree: ci.rc exists, ci.meta
#       exists, and ci.meta's sha matches `git rev-parse HEAD` right now.
#       Prints one line saying which and why. Exit 0 only for a fresh,
#       passing gate (ci.rc=0 at the current HEAD); exit 1 for anything
#       else - no gate, an incomplete one, a stale one, or a fresh red one.
#       Never infers a pass from a command's exit code; always reads the
#       files' content, per HANDOFF's rule.
#   scripts/ci-gate.sh wait [--timeout SECONDS] [--interval SECONDS]
#       Block until a gate written by a run that started no earlier than
#       this wait is complete for the current HEAD, then print `check`'s
#       verdict and exit with `check`'s code. Meant to be run alongside a
#       `run` that is already going (or about to), in ONE foreground call -
#       nothing wakes a subagent that ends its turn. Defaults: timeout
#       7200s (longer than any `just ci`), interval 15s. A timeout is a
#       refusal, exit 1, and says what it was still waiting for.
set -uo pipefail

root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "ci-gate: not inside a git worktree" >&2
  exit 2
}
cd "$root" || exit 2

cmd_run() {
  local cmd=(just ci)
  if [ "${1:-}" = "--" ]; then
    shift
    cmd=("$@")
  fi
  if [ "${#cmd[@]}" -eq 0 ]; then
    echo "ci-gate run: empty command after --" >&2
    return 2
  fi

  # A fresh `git worktree add` does not check out the hugo-book submodule,
  # and the docs step in `just ci` then fails with `template for shortcode
  # "hint" not found` - a red gate caused by the worktree, not by whatever
  # is being worked on (issue #1031). scripts/contribute.sh already does
  # this before its worker starts; do it here too so no worker needs to
  # know it by hand. Idempotent and silent when already initialised.
  git submodule update --init site/themes/hugo-book >/dev/null 2>&1 || true

  # Delete first: a kill at any point from here on leaves no ci.rc, which
  # `check` already treats as "no completed run" rather than a pass.
  rm -f ci.rc ci.out ci.meta ci.meta.tmp

  local sha start end rc id
  sha="$(git rev-parse HEAD)"
  start="$(date -u +%FT%TZ)"
  # A per-run identity, so that two runs at the SAME sha are still
  # distinguishable from each other. `check` does not read it - a gate's
  # freshness is about the commit, not about which run produced it - but
  # `wait` does: without it, re-gating an unchanged HEAD (a flake, a
  # re-measure) leaves the previous run's ci.meta byte-identical to the one
  # the wait is waiting for, and the sha test alone cannot tell them apart.
  # Seconds + pid + $RANDOM rather than a uuid tool this repo cannot assume
  # is installed.
  id="$(date -u +%s)-$$-${RANDOM}"

  { "${cmd[@]}"; } >ci.out 2>&1
  rc=$?

  echo "$rc" >ci.rc
  end="$(date -u +%FT%TZ)"
  {
    printf 'sha=%s\n' "$sha"
    printf 'run=%s\n' "$id"
    printf 'start=%s\n' "$start"
    printf 'end=%s\n' "$end"
  } >ci.meta.tmp
  mv ci.meta.tmp ci.meta

  return "$rc"
}

cmd_check() {
  if [ ! -f ci.rc ]; then
    echo "NO GATE: ci.rc does not exist in $root - no completed run to trust (killed mid-run, or never started)"
    return 1
  fi
  if [ ! -f ci.meta ]; then
    echo "INCOMPLETE GATE: ci.rc exists but ci.meta does not - the run was killed after its exit code was written but before its identity was recorded; do not trust it"
    return 1
  fi

  local meta_sha head_sha rc
  meta_sha="$(sed -n 's/^sha=//p' ci.meta)"
  head_sha="$(git rev-parse HEAD)"
  if [ -z "$meta_sha" ]; then
    echo "INCOMPLETE GATE: ci.meta has no sha= line - do not trust it"
    return 1
  fi
  if [ "$meta_sha" != "$head_sha" ]; then
    echo "STALE GATE: ci.rc was written for $meta_sha, HEAD is now $head_sha - re-run: scripts/ci-gate.sh run"
    return 1
  fi

  rc="$(tr -d '[:space:]' <ci.rc)"
  local meta_line
  meta_line="$(tr '\n' ' ' <ci.meta)"
  if [ "$rc" != "0" ]; then
    echo "RED: ci.rc=$rc at $head_sha (fresh, but failing) - $meta_line"
    return 1
  fi
  echo "GREEN: ci.rc=0 at $head_sha (fresh) - $meta_line"
  return 0
}

# cmd_wait: block until this worktree holds a COMPLETE gate, for the current
# HEAD, that was not already sitting there when the wait began - then hand
# the verdict to cmd_check and exit with its code.
#
# The three conditions are each load-bearing, and dropping any one of them
# reproduces a false green somebody has actually recorded:
#
#   ci.rc exists           - the old recipe's whole test, and #1307: on its
#                            own it matches a leftover file from last week.
#   ci.meta names HEAD     - #519's discrimination. Rules out a genuinely
#                            complete gate for a commit already moved past.
#   ci.meta is not the one
#   that was here at entry - what sha alone cannot do. Re-run the gate at an
#                            UNCHANGED HEAD and the leftover names the right
#                            sha; only its identity says it is the old run.
#
# Deliberately NOT here: any attempt to find the running gate's process.
# `pgrep -f "just ci"` matches the pgrep itself and loops forever, and that
# has already cost a session (CLAUDE.md records it). The files carry enough
# identity on their own.
cmd_wait() {
  local timeout=7200 interval=15
  while [ "$#" -gt 0 ]; do
    case "$1" in
    --timeout)
      timeout="${2:-}"
      shift 2 || return 2
      ;;
    --interval)
      interval="${2:-}"
      shift 2 || return 2
      ;;
    *)
      echo "ci-gate wait: unknown argument $1" >&2
      return 2
      ;;
    esac
  done
  case "$timeout" in '' | *[!0-9]*)
    echo "ci-gate wait: --timeout wants whole seconds, got '$timeout'" >&2
    return 2
    ;;
  esac
  case "$interval" in '' | *[!0-9]* | 0)
    echo "ci-gate wait: --interval wants a positive whole number of seconds, got '$interval'" >&2
    return 2
    ;;
  esac

  # The gate that is already here, if any. Read ONCE, before waiting: this
  # is the file #1307's stale green came from, and it is disqualified for
  # the rest of this wait no matter how many times it is re-read.
  local entry_meta="" entry_desc="none"
  if [ -f ci.meta ]; then
    entry_meta="$(cat ci.meta)"
    entry_desc="$(tr '\n' ' ' <ci.meta)"
  fi
  local entry_rc="absent"
  [ -f ci.rc ] && entry_rc="$(tr -d '[:space:]' <ci.rc)"

  echo "ci-gate wait: waiting for a gate at $(git rev-parse HEAD) newer than the one already here (ci.rc=$entry_rc, ci.meta=$entry_desc)" >&2

  local waited=0 head_sha meta_sha now_meta
  while :; do
    head_sha="$(git rev-parse HEAD)"
    if [ -f ci.rc ] && [ -f ci.meta ]; then
      now_meta="$(cat ci.meta)"
      meta_sha="$(sed -n 's/^sha=//p' ci.meta)"
      if [ "$meta_sha" = "$head_sha" ] && [ "$now_meta" != "$entry_meta" ]; then
        cmd_check
        return $?
      fi
    fi
    if [ "$timeout" -gt 0 ] && [ "$waited" -ge "$timeout" ]; then
      echo "TIMED OUT: no gate for $head_sha appeared within ${timeout}s that differs from the one present when the wait started (ci.rc=$entry_rc, ci.meta=$entry_desc). Either no run was started, or it is still going, or it ran at a different commit - this is NOT a pass. Check with: scripts/ci-gate.sh check"
      return 1
    fi
    sleep "$interval"
    waited=$((waited + interval))
    # A heartbeat roughly every four intervals, so a long wait in a
    # foreground call is visibly alive rather than indistinguishable from a
    # wedged one.
    if [ $((waited % (interval * 4))) -eq 0 ]; then
      echo "ci-gate wait: ${waited}s elapsed, still no fresh gate for $head_sha" >&2
    fi
  done
}

case "${1:-}" in
run)
  shift
  cmd_run "$@"
  ;;
check)
  cmd_check
  ;;
wait)
  shift
  cmd_wait "$@"
  ;;
*)
  echo "usage: $(basename "$0") run [-- CMD...] | check | wait [--timeout SECONDS] [--interval SECONDS]" >&2
  exit 2
  ;;
esac
