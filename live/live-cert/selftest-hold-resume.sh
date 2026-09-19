#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-hold-resume.sh: proof for issue #1032.
#
# The maintainer's problem, verbatim: "its not about compute its about the
# time it takes and it slows down my development." Three scale-50 cycles in
# one night each spent 35 min on cold_deploy, 40 on migrate and 40 on
# teardown to look at ONE plan. terralith-scale.sh now has a held-estate mode
# so a real-AWS iteration on one stage costs minutes, not a full
# deploy-and-destroy cycle:
#
#   LIVECERT_HOLD=1                 skips teardown, prints where everything
#                                    is and how to get back to it.
#   LIVECERT_RESUME=<work dir>      skips cold_deploy and migrate against a
#                                    held work dir, runs from index_wait on.
#   terralith-scale.sh teardown <work dir>  (or LIVECERT_TEARDOWN_ONLY=<work
#                                    dir>) tears a held work dir down on its
#                                    own.
#
# This self-test proves all three, following the same discipline
# selftest-teardown-timeout.sh and selftest-index-wait.sh already do: no AWS
# calls, no docker, no terraform, no go build. Case 1 and case 2 extract
# teardown() and resume_verify() VERBATIM out of a real terralith-scale.sh
# (default: the one shipped beside this script) and drive them in a minimal
# stubbed harness; case 3 extracts the teardown-only dispatch the same way
# and runs that.
#
# Case 3 used to EXECUTE terralith-scale.sh, on the argument that the
# dispatch exits before "0. tools" ever builds a binary or starts a floci
# container. That argument holds only while the dispatch's own `exit 0`
# does: a mutation of it falls through into stage 0 with the marker's
# TARGET=aws, and the PATH stubs are then all that stands between this
# self-test and a real cold deploy. Issue #1380; it is not hypothetical,
# the same shape started one on 2026-09-18. The extracted span ends at the
# dispatch, so there is nothing after it to fall into.
#
# Usage: bash live/live-cert/selftest-hold-resume.sh
#   TERRALITH_SCALE_SH=<path> to extract from a different revision, e.g. the
#   pre-fix content via process substitution:
#     TERRALITH_SCALE_SH=<(git show main:live/live-cert/terralith-scale.sh) \
#       bash live/live-cert/selftest-hold-resume.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
pass=1
log() { printf '%s\n' "$*"; }

# Materialize SRC_ARG into a plain file: it may be a process substitution,
# and it gets read (and grepped) more than once below.
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"

log "=== selftest-hold-resume: extracting teardown()/resume_verify()/livecert_marker_get() from $SRC_ARG ==="

# Same brace-depth/heredoc-aware extractor selftest-teardown-timeout.sh and
# selftest-index-wait.sh use, copied rather than shared, so this self-test
# has no import of its own to keep in sync.
extract_func() {
  local name="$1" f="$2"
  awk -v want="${name}() {" '
    BEGIN { grab = 0; depth = 0; heredoc = "" }
    {
      line = $0
      if (heredoc != "") {
        print line
        if (line == heredoc) heredoc = ""
        next
      }
      if (!grab) {
        if (line == want) { grab = 1; depth = 1; print line }
        next
      }
      if (match(line, /<<-?["\x27]?[A-Za-z_][A-Za-z0-9_]*["\x27]?/)) {
        tok = substr(line, RSTART, RLENGTH)
        sub(/^<<-?/, "", tok)
        gsub(/["\x27]/, "", tok)
        heredoc = tok
      }
      print line
      n = line; gsub(/[^{]/, "", n); depth += length(n)
      n = line; gsub(/[^}]/, "", n); depth -= length(n)
      if (depth == 0) exit
    }
  ' "$f"
}

BIN="$WORK/bin"; mkdir -p "$BIN"

########################################################################
# Case 1: LIVECERT_HOLD=1 - teardown() prints the loud block and skips the
# real destroy, even when a trusted stock state is sitting right there
# ready to be torn down.
########################################################################
log ""
log "=== case 1: LIVECERT_HOLD=1 skips teardown ==="
TEARDOWN_SRC="$(extract_func teardown "$SRC")"
if [ -z "$TEARDOWN_SRC" ]; then
  log "FAIL: could not find teardown() in $SRC_ARG"
  pass=0
else
  CASE1_DIR="$WORK/case1"
  mkdir -p "$CASE1_DIR/cold"
  : > "$CASE1_DIR/cold/terraform.tfstate"   # a real, ready-to-destroy stock state
  MARKER1="$CASE1_DIR/trusted_destroy_ran"
  cat > "$CASE1_DIR/fake-terraform" <<EOF
#!/usr/bin/env bash
# the TRUSTED destroy's own binary - if this ever runs, LIVECERT_HOLD did
# NOT hold.
touch "$MARKER1"
exit 0
EOF
  chmod +x "$CASE1_DIR/fake-terraform"

  RUNNER1="$CASE1_DIR/runner.sh"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -uo pipefail'
    printf '%s\n' "$TEARDOWN_SRC"
    printf '%s\n' 'log() { printf "  [harness] %s\n" "$*"; }'
    printf '%s\n' 'verify_empty() { echo "  [harness] verify_empty called - should not happen when held"; return 0; }'
    printf '%s\n' 'sweep() { echo "  [harness] sweep called - should not happen when held"; }'
    printf 'TEARDOWN_DONE=0\n'
    printf 'MIGRATE_DONE=0\n'
    printf 'LIVECERT_HOLD=1\n'
    printf 'ESTATE=tl-livecert-selftest\n'
    printf 'PREFIX=selftest\n'
    printf 'RUN_ID=selftest-hold-%s\n' "$$"
    printf 'SCALE=1\n'
    printf 'TARGET=aws\n'
    printf 'REGION=us-east-1\n'
    printf 'ENDPOINT=\n'
    printf 'RECORD_STORE_BACKEND=local\n'
    printf 'COLD_DIR=%q\n' "$CASE1_DIR/cold"
    printf 'ADOPTED_DIR=%q\n' "$CASE1_DIR/adopted"
    printf 'WORK=%q\n' "$CASE1_DIR"
    printf 'TF_COLD=%q\n' "$CASE1_DIR/fake-terraform"
    printf 'UNTRUSTED_TEARDOWN_TIMEOUT_S=2\n'
    printf 'LIVECERT_KEEP_WORK=1\n'
    printf '%s\n' 'teardown'
    # shellcheck disable=SC2016 # meant to expand in the RUNNER script below, not here
    printf '%s\n' 'echo "TEARDOWN_DONE_AFTER=$TEARDOWN_DONE"'
  } > "$RUNNER1"

  CASE1_OUT="$(bash "$RUNNER1" 2>&1)"; CASE1_RC=$?
  printf '%s\n' "$CASE1_OUT" | sed 's/^/  /'

  if [ "$CASE1_RC" -ne 0 ]; then
    log "FAIL: teardown() exited $CASE1_RC under LIVECERT_HOLD=1 - it must return 0"
    pass=0
  fi
  if ! grep -qE 'LIVECERT_HOLD=1: teardown SKIPPED' <<< "$CASE1_OUT"; then
    log "FAIL: no hold banner in case 1's output"
    pass=0
  else
    log "  confirmed: the hold banner printed"
  fi
  for needle in "work dir : $CASE1_DIR" "estate   : tl-livecert-selftest" "prefix   : selftest" "LIVECERT_RESUME=$CASE1_DIR" "LIVECERT_TEARDOWN_ONLY=$CASE1_DIR"; do
    if ! grep -qF "$needle" <<< "$CASE1_OUT"; then
      log "FAIL: hold banner is missing '$needle'"
      pass=0
    fi
  done
  if [ -f "$MARKER1" ]; then
    log "FAIL: the trusted stock destroy ran anyway (marker present) - LIVECERT_HOLD=1 did not actually skip it"
    pass=0
  else
    log "  confirmed: the trusted stock destroy did NOT run"
  fi
  if grep -qE 'verify_empty called|sweep called' <<< "$CASE1_OUT"; then
    log "FAIL: verify_empty/sweep ran anyway - a held teardown must return before either"
    pass=0
  fi
  if ! grep -qE '^TEARDOWN_DONE_AFTER=1$' <<< "$CASE1_OUT"; then
    log "FAIL: TEARDOWN_DONE was not left at 1 after a held teardown() call (a re-entrant call - e.g. a second signal - would re-run the whole thing)"
    pass=0
  else
    log "  confirmed: TEARDOWN_DONE=1 after the held call"
  fi
fi

########################################################################
# Case 2: LIVECERT_RESUME - resume_verify() logs both stages skipped when
# the work dir's markers agree with the environment, and refuses (via
# fail(), before touching anything) on a PREFIX mismatch.
########################################################################
log ""
log "=== case 2: LIVECERT_RESUME skips cold_deploy/migrate, refuses on a prefix mismatch ==="
RESUME_SRC="$(extract_func resume_verify "$SRC")"
MARKER_GET_SRC="$(extract_func livecert_marker_get "$SRC")"
if [ -z "$RESUME_SRC" ]; then
  log "FAIL: could not find resume_verify() in $SRC_ARG - LIVECERT_RESUME does not exist here (this is the RED result on the pre-fix script: a resume attempt would proceed to a full cold_deploy+migrate cycle instead of skipping either)"
  pass=0
elif [ -z "$MARKER_GET_SRC" ]; then
  log "FAIL: could not find livecert_marker_get() in $SRC_ARG"
  pass=0
else
  # run_resume_case writes cold/migrate marker fixtures under a fresh dir,
  # then runs resume_verify() with the given ENVIRONMENT prefix/scale
  # (which may deliberately disagree with what the markers themselves
  # record) and prints the result. Args: <dir> <marker_prefix> <marker_scale>
  #   <env_prefix> <env_scale> <expected> <verified>
  run_resume_case() {
    local dir="$1" m_prefix="$2" m_scale="$3" e_prefix="$4" e_scale="$5" expected="$6" verified="$7"
    mkdir -p "$dir/cold"
    : > "$dir/cold/terraform.tfstate"
    {
      printf 'PREFIX=%s\n' "$m_prefix"
      printf 'SCALE=%s\n' "$m_scale"
      printf 'TARGET=aws\n'
      printf 'REGION=us-east-1\n'
      printf 'RUN_ID=selftest-resume\n'
      printf 'RECORD_STORE_BACKEND=local\n'
      printf 'EXPECTED=%s\n' "$expected"
      printf 'TIMESTAMP=2026-01-01T00:00:00Z\n'
    } > "$dir/.livecert-cold-state"
    {
      printf 'PREFIX=%s\n' "$m_prefix"
      printf 'SCALE=%s\n' "$m_scale"
      printf 'ESTATE=tl-livecert-%s\n' "$m_prefix"
      printf 'EXPECTED=%s\n' "$expected"
      printf 'VERIFIED=%s\n' "$verified"
      printf 'TIMESTAMP=2026-01-01T00:00:00Z\n'
    } > "$dir/.livecert-migrate-state"

    local runner="$dir/runner.sh"
    {
      printf '%s\n' '#!/usr/bin/env bash'
      printf '%s\n' 'set -uo pipefail'
      printf '%s\n' 'log() { printf "%s\n" "$*"; }'
      printf '%s\n' 'fail() { printf "FAIL: %s\n" "$*" >&2; exit 1; }'
      printf '%s\n' "$MARKER_GET_SRC"
      printf '%s\n' "$RESUME_SRC"
      printf 'PREFIX=%q\n' "$e_prefix"
      printf 'SCALE=%q\n' "$e_scale"
      printf 'EXPECTED=%s\n' "$expected"
      printf 'VERIFIED=%s\n' "$verified"
      printf 'WORK=%q\n' "$dir"
      printf 'COLD_DIR=%q\n' "$dir/cold"
      printf 'LIVECERT_RESUME=%q\n' "$dir"
      printf 'RESUMED=0\n'
      printf '%s\n' 'resume_verify'
      # shellcheck disable=SC2016 # meant to expand in the RUNNER script below, not here
      printf '%s\n' 'echo "RESUMED_AFTER=$RESUMED"'
    } > "$runner"
    bash "$runner" 2>&1
  }

  log "  case 2a: markers agree with the environment (prefix=resume2a scale=3)"
  CASE2A_DIR="$WORK/case2a"
  CASE2A_OUT="$(run_resume_case "$CASE2A_DIR" resume2a 3 resume2a 3 227 104)"
  CASE2A_RC=$?
  printf '%s\n' "$CASE2A_OUT" | sed 's/^/  /'
  if [ "$CASE2A_RC" -ne 0 ]; then
    log "FAIL: resume_verify() exited $CASE2A_RC on matching markers - it must succeed"
    pass=0
  fi
  if ! grep -qF "stage=cold_deploy verdict=skipped detail=resumed from $CASE2A_DIR" <<< "$CASE2A_OUT"; then
    log "FAIL: no 'stage=cold_deploy verdict=skipped' line in case 2a's output"
    pass=0
  fi
  if ! grep -qF "stage=migrate verdict=skipped detail=resumed from $CASE2A_DIR" <<< "$CASE2A_OUT"; then
    log "FAIL: no 'stage=migrate verdict=skipped' line in case 2a's output"
    pass=0
  fi
  if ! grep -qE '^RESUMED_AFTER=1$' <<< "$CASE2A_OUT"; then
    log "FAIL: RESUMED was not left at 1 after a matching resume_verify() call"
    pass=0
  else
    log "  confirmed: both stages logged skipped, RESUMED=1"
  fi

  log "  case 2b: environment PREFIX disagrees with what the work dir recorded"
  CASE2B_DIR="$WORK/case2b"
  CASE2B_OUT="$(run_resume_case "$CASE2B_DIR" resume2b-original 1 resume2b-typo 1 79 35)"
  CASE2B_RC=$?
  printf '%s\n' "$CASE2B_OUT" | sed 's/^/  /'
  if [ "$CASE2B_RC" -eq 0 ]; then
    log "FAIL: resume_verify() exited 0 on a prefix mismatch - it must refuse"
    pass=0
  else
    log "  confirmed: resume_verify() exited nonzero ($CASE2B_RC) on the mismatch"
  fi
  if ! grep -qE '^FAIL: LIVECERT_RESUME=.*was recorded under prefix' <<< "$CASE2B_OUT"; then
    log "FAIL: no prefix-mismatch refusal message in case 2b's output"
    pass=0
  fi
  if grep -qE 'verdict=skipped' <<< "$CASE2B_OUT"; then
    log "FAIL: case 2b logged a stage skipped before refusing - a refusal must happen before anything is trusted"
    pass=0
  else
    log "  confirmed: refused before logging either stage skipped"
  fi
fi

########################################################################
# Case 3: `terralith-scale.sh teardown <work dir>` / LIVECERT_TEARDOWN_ONLY
# - runs the dispatch, EXTRACTED between its marker comments (#1380),
# against a stubbed aws and a stubbed terraform, and checks it both
# destroys and verifies.
#
# The span runs from part 1's opening marker to part 2's closing marker, so
# it carries the marker read, the whole variable cascade those values feed,
# every function teardown() needs, and the dispatch itself - the same code
# an invocation would run, minus any way to continue past it. Only the
# three lines the harness would otherwise inherit from the top of the file
# (ROOT, LIB and the two `source`s) are supplied here.
########################################################################
log ""
log "=== case 3: teardown-only destroys and verifies ==="

# extract_dispatch prints that span out of a terralith-scale.sh, or fails.
#
# The tail check is the load-bearing half: sed's range prints to EOF when
# the closing address never matches, so a missing part 2 marker would hand
# back the ENTIRE harness - stage 0, cold_deploy, test_apply - as "the
# dispatch", to be run with a marker that says TARGET=aws. That is the
# accident this case was rewritten to make impossible, so an extraction
# that does not end exactly at the closing marker is not an extraction and
# nothing gets run.
DISPATCH_END='# <<< teardown-only dispatch part 2'
extract_dispatch() {
  local out
  out="$(sed -n '/^# >>> teardown-only dispatch part 1$/,/^# <<< teardown-only dispatch part 2$/p' "$1")"
  [ -n "$out" ] || return 1
  [ "$(printf '%s\n' "$out" | tail -1)" = "$DISPATCH_END" ] || return 1
  printf '%s\n' "$out"
}

DISPATCH_SRC="$(extract_dispatch "$SRC")" || DISPATCH_SRC=""

if ! grep -qE 'LIVECERT_TEARDOWN_ONLY|TEARDOWN_ONLY_DIR' "$SRC"; then
  log "FAIL: $SRC_ARG has no LIVECERT_TEARDOWN_ONLY/teardown dispatch at all - this is the RED result on the pre-fix script: the 'teardown' argument and LIVECERT_TEARDOWN_ONLY are both unknown to it, so it proceeds toward a full cold_deploy run (needing real terralith-gen output, a real TARGET, the whole pipeline) instead of a bounded destroy"
  pass=0
elif [ -z "$DISPATCH_SRC" ]; then
  # A revision from before #1380 has the dispatch but not the markers. The
  # grep above is then the whole check for it: the alternative is executing
  # it, which is what #1380 forbids.
  if [ "$SRC_ARG" = "$ROOT/live/live-cert/terralith-scale.sh" ]; then
    log "FAIL: the shipped terralith-scale.sh has no complete '# >>> teardown-only dispatch part 1' ... '$DISPATCH_END' span to extract, so this case cannot run the dispatch without executing the harness, which #1380 forbids. Restore the markers (they bracket comments only) rather than restoring the execution."
    pass=0
  else
    log "  $SRC_ARG carries no marked dispatch span (a revision from before #1380); the grep above is the whole check for it, because running it would mean executing the harness"
  fi
else
  CASE3_DIR="$WORK/case3"
  mkdir -p "$CASE3_DIR/cold"
  : > "$CASE3_DIR/cold/terraform.tfstate"
  {
    printf 'PREFIX=selftest3\n'
    printf 'SCALE=1\n'
    printf 'TARGET=aws\n'
    printf 'REGION=us-east-1\n'
    printf 'RUN_ID=selftest-teardown-only\n'
    printf 'RECORD_STORE_BACKEND=local\n'
    printf 'EXPECTED=79\n'
    printf 'TIMESTAMP=2026-01-01T00:00:00Z\n'
  } > "$CASE3_DIR/.livecert-cold-state"

  mkdir -p "$CASE3_DIR/fakebin"
  MARKER3="$CASE3_DIR/trusted_destroy_ran"
  cat > "$CASE3_DIR/fakebin/aws" <<'FAKEEOF'
#!/usr/bin/env bash
# every listing this dispatch's verify_empty needs comes back empty and
# successful - stands in for an account already clean, no real AWS calls.
exit 0
FAKEEOF
  cat > "$CASE3_DIR/fakebin/terraform" <<EOF
#!/usr/bin/env bash
# the TRUSTED destroy's own binary.
touch "$MARKER3"
exit 0
EOF
  cat > "$CASE3_DIR/fakebin/choudoufu" <<'FAKEEOF'
#!/usr/bin/env bash
# not used by teardown-only (MIGRATE_DONE=0 there); present only so
# TOFU_BIN points at something, matching how a real held work dir's
# teardown would be invoked.
echo "fake choudoufu: not used by teardown-only" >&2
exit 1
FAKEEOF
  chmod +x "$CASE3_DIR/fakebin/aws" "$CASE3_DIR/fakebin/terraform" "$CASE3_DIR/fakebin/choudoufu"

  # The runner: ROOT and LIB by hand (the extracted span starts below the
  # top of the file, where the real script computes them from
  # ${BASH_SOURCE[0]}), the two libraries the real script sources - teardown()
  # and verify_empty() need live-cert.sh's livecert_aws/livecert_rgta_count -
  # then the span verbatim.
  #
  # The last line is a tripwire. It is unreachable while the dispatch ends
  # in `exit 0`, and printing it is the only way this case can observe that
  # the dispatch fell through instead - which on the real script is the
  # step before stage 0 and a cold deploy.
  RUNNER3="$CASE3_DIR/dispatch.sh"
  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf '%s\n' 'set -uo pipefail'
    printf 'ROOT=%q\n' "$ROOT"
    printf 'LIB=%q\n' "$ROOT/live/live-cert/lib"
    printf '%s\n' 'source "$ROOT/live/e2e/lib/gauntlet.sh"'
    printf '%s\n' 'source "$LIB/live-cert.sh"'
    printf '%s\n' "$DISPATCH_SRC"
    printf '%s\n' 'printf "DISPATCH-FELL-THROUGH\n"'
  } > "$RUNNER3"

  # The credentials scrub is belt-and-braces, not the safety argument: the
  # safety argument is that the text above stops at the dispatch. It is here
  # because teardown() and verify_empty() do reach for `aws`, and if the
  # stub ahead of it on PATH were ever missed, the commands they run
  # (s3 rm --recursive, ssm delete-parameter) are destructive in whatever
  # account the ambient chain resolves to. Invalid keys, no metadata
  # service, no config files, and every endpoint pointed at a closed port.
  CASE3_OUT="$(PATH="$CASE3_DIR/fakebin:$PATH" \
    env -u AWS_PROFILE -u AWS_DEFAULT_PROFILE -u AWS_SESSION_TOKEN -u AWS_SECURITY_TOKEN \
      AWS_ACCESS_KEY_ID=invalid AWS_SECRET_ACCESS_KEY=invalid \
      AWS_EC2_METADATA_DISABLED=true \
      AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null \
      AWS_ENDPOINT_URL=http://127.0.0.1:9 \
      TF_COLD_BIN="$CASE3_DIR/fakebin/terraform" \
      TOFU_BIN="$CASE3_DIR/fakebin/choudoufu" \
      LIVECERT_TEARDOWN_ONLY="$CASE3_DIR" \
      LIVECERT_KEEP_WORK=1 \
      bash "$RUNNER3" 2>&1)"
  CASE3_RC=$?
  printf '%s\n' "$CASE3_OUT" | sed 's/^/  /'

  if grep -qF 'DISPATCH-FELL-THROUGH' <<< "$CASE3_OUT"; then
    log "FAIL: the teardown-only dispatch did not exit - on the real script the next statement is \"0. tools\", and after that a cold deploy with the marker's TARGET=aws"
    pass=0
  else
    log "  confirmed: the dispatch exited rather than continuing (the tripwire line after it never printed)"
  fi

  if [ "$CASE3_RC" -ne 0 ]; then
    log "FAIL: teardown-only exited $CASE3_RC, want 0"
    pass=0
  fi
  if ! grep -qF "teardown-only: $CASE3_DIR" <<< "$CASE3_OUT"; then
    log "FAIL: no 'teardown-only: $CASE3_DIR' line in the output"
    pass=0
  fi
  if [ ! -f "$MARKER3" ]; then
    log "FAIL: the trusted stock destroy never ran (no marker) - teardown-only did not actually tear anything down"
    pass=0
  else
    log "  confirmed: the trusted stock destroy ran"
  fi
  if ! grep -qE 'VERIFIED EMPTY' <<< "$CASE3_OUT"; then
    log "FAIL: no 'VERIFIED EMPTY' verification line in the output"
    pass=0
  else
    log "  confirmed: verify-empty reported clean"
  fi
  if ! grep -qF "teardown-only: done" <<< "$CASE3_OUT"; then
    log "FAIL: no 'teardown-only: done' completion line"
    pass=0
  else
    log "  confirmed: teardown-only completed and exited"
  fi
fi

log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-hold-resume: PASS ==="
else
  log "=== selftest-hold-resume: FAIL - see above ==="
fi
exit $((1 - pass))
