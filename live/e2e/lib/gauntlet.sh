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
  CURRENT_STAGE=""
  printf 'GAUNTLET protocol=1\n'
}

# gauntlet_begin_stage <id>: marks the start of stage <id>'s own work, for a
# script's fail() to blame if something goes wrong before the stage reports
# its own verdict. Sets the CURRENT_STAGE variable every crossing script's
# own fail() already reads (`if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage
# "$CURRENT_STAGE" fail "$*"; fi`) - no new mechanism, just a name for the
# assignment every script was already spelling out by hand.
#
# Pair every gauntlet_begin_stage with either a gauntlet_stage call for that
# same id (which clears CURRENT_STAGE itself, see below) or an explicit
# gauntlet_end_stage, so the setup for whatever comes NEXT never runs while
# CURRENT_STAGE still names a stage that has already finished (issue #555:
# a script that assigns CURRENT_STAGE=X once and only reassigns it when
# stage Y begins leaves every line in between - including setup work for Y
# that has nothing to do with X - attributed to X if it fails).
gauntlet_begin_stage() {
  CURRENT_STAGE="$1"
}

# gauntlet_end_stage: clears CURRENT_STAGE, so a failure from this point on -
# setup for the next stage, teardown, an oracle comparison that never itself
# reports a verdict - has no stage to blame. fail()'s own guard
# (`if [ -n "$CURRENT_STAGE" ]`) already treats an empty CURRENT_STAGE as
# "no verdict to record," so this is enough to make an unattributed window
# genuinely unattributed instead of silently inheriting whatever stage ran
# last.
#
# gauntlet_stage (below) already calls this once a verdict is actually
# recorded, so most scripts never need to call it directly - it exists for a
# block of stage-labelled work that does NOT end in its own gauntlet_stage
# call, e.g. an oracle computation run under `gauntlet_begin_stage day2_X`
# well before day2_X's own real verdict is reported much later in the
# script (corpus-hongbomiao-labelbox and corpus-iam-policy's own STAGE
# 1.5/1.5.5/1.5.6 oracle blocks are exactly this shape).
gauntlet_end_stage() {
  CURRENT_STAGE=""
}

# gauntlet_stage <id> <verdict> [detail...]
# The detail may contain spaces and '=' freely; it runs to end of line, so
# duration_s is emitted before it. Newlines in the detail are replaced by
# spaces so the line stays one line.
#
# Clears CURRENT_STAGE before returning (see gauntlet_end_stage): once a
# stage's verdict has been recorded, anything that runs afterwards - even in
# the same script, even one line later - is by definition no longer that
# stage's own work, so a failure there must never be attributed to a stage
# that already reported its verdict (issue #555).
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
  gauntlet_end_stage
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
