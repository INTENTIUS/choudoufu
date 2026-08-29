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

  # Delete first: a kill at any point from here on leaves no ci.rc, which
  # `check` already treats as "no completed run" rather than a pass.
  rm -f ci.rc ci.out ci.meta ci.meta.tmp

  local sha start end rc
  sha="$(git rev-parse HEAD)"
  start="$(date -u +%FT%TZ)"

  { "${cmd[@]}"; } >ci.out 2>&1
  rc=$?

  echo "$rc" >ci.rc
  end="$(date -u +%FT%TZ)"
  {
    printf 'sha=%s\n' "$sha"
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

case "${1:-}" in
run)
  shift
  cmd_run "$@"
  ;;
check)
  cmd_check
  ;;
*)
  echo "usage: $(basename "$0") run [-- CMD...] | check" >&2
  exit 2
  ;;
esac
