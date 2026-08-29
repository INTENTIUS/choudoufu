#!/usr/bin/env bash
# live/e2e/lib/gauntlet.sh: the stage protocol a crossing script speaks so
# tools/gauntlet can record its verdicts. Source it, call gauntlet_begin
# once, then gauntlet_stage <id> <pass|fail|not_run> [detail] per stage, and
# gauntlet_end last. Every line it prints starts with "GAUNTLET " on stdout
# and is greppable by a human; everything else the script prints is ignored
# by the runner. live/GAUNTLET.md documents the grammar; the stage ids are
# tools/gauntlet/stages.go's.
#
# A script that sources this library but never calls gauntlet_begin is not
# speaking the protocol, and the runner treats it as legacy.

gauntlet_begin() {
  # _GAUNTLET_LAST_T anchors the first stage's duration_s: the wall-clock
  # elapsed since gauntlet_begin ran, not since some other clock. Every
  # crossing script already does its real work strictly between one
  # gauntlet_stage call and the next (issue #434 verified this against every
  # current script before relying on it), so the delta between consecutive
  # GAUNTLET timestamps is that stage's wall-clock time - computed here in
  # the shell, once per call, rather than as a raw timestamp the Go side
  # would have to subtract, so a script's own stdout stays self-describing.
  _GAUNTLET_LAST_T=$(date +%s)
  printf 'GAUNTLET protocol=1\n'
}

# gauntlet_stage <id> <verdict> [detail...]
# The detail may contain spaces and '=' freely; it runs to end of line, so
# duration_s is emitted before it. Newlines in the detail are replaced by
# spaces so the line stays one line.
gauntlet_stage() {
  local id="$1" verdict="$2"
  shift 2 || true
  case "$verdict" in
    pass|fail|not_run) ;;
    *) printf 'gauntlet_stage: verdict %q for stage %s must be pass, fail or not_run\n' "$verdict" "$id" >&2; exit 2 ;;
  esac
  local now dur
  now=$(date +%s)
  dur=$(( now - ${_GAUNTLET_LAST_T:-$now} ))
  _GAUNTLET_LAST_T=$now
  if [ "$#" -gt 0 ]; then
    local detail
    detail="$(printf '%s' "$*" | tr '\n\r' '  ')"
    printf 'GAUNTLET stage=%s verdict=%s duration_s=%s detail=%s\n' "$id" "$verdict" "$dur" "$detail"
  else
    printf 'GAUNTLET stage=%s verdict=%s duration_s=%s\n' "$id" "$verdict" "$dur"
  fi
}

gauntlet_end() {
  printf 'GAUNTLET end=1\n'
}

# gauntlet_stage_from_exit <id> <exit-code> [detail...]
# Convenience for the common shape "run a check, report pass on 0, fail
# otherwise" without the script having to branch itself.
gauntlet_stage_from_exit() {
  local id="$1" code="$2"
  shift 2 || true
  if [ "$code" -eq 0 ]; then
    gauntlet_stage "$id" pass "$@"
  else
    gauntlet_stage "$id" fail "$@"
  fi
}
