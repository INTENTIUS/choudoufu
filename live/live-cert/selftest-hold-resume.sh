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
# stubbed harness; case 3 runs the real script's own teardown-only dispatch
# directly, which is safe to do because that dispatch is unconditionally
# reached and exits before "0. tools" ever builds a binary or starts a
# floci container - the whole point of a held work dir carrying its own
# markers is that a later invocation never has to pay for either.
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
# - runs the real script's own dispatch directly (it exits before "0.
# tools", so this needs no go build and no docker), against a stubbed aws
# and a stubbed terraform, and checks it both destroys and verifies.
########################################################################
log ""
log "=== case 3: teardown-only destroys and verifies ==="
if ! grep -qE 'LIVECERT_TEARDOWN_ONLY|TEARDOWN_ONLY_DIR' "$SRC"; then
  log "FAIL: $SRC_ARG has no LIVECERT_TEARDOWN_ONLY/teardown dispatch at all - this is the RED result on the pre-fix script: the 'teardown' argument and LIVECERT_TEARDOWN_ONLY are both unknown to it, so it proceeds toward a full cold_deploy run (needing real terralith-gen output, a real TARGET, the whole pipeline) instead of a bounded destroy"
  pass=0
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

  # Run the actual shipped script at its real repo path, not the copy at
  # $SRC: it computes ROOT/LIB from its own ${BASH_SOURCE[0]}, and needs
  # that to resolve to the real checkout so it can source
  # live/e2e/lib/gauntlet.sh and live/live-cert/lib/live-cert.sh (verify_empty
  # and teardown() need the latter's livecert_aws/livecert_rgta_count) - the
  # RED grep above already covers a different revision's source without
  # needing to execute it at all.
  CASE3_OUT="$(PATH="$CASE3_DIR/fakebin:$PATH" \
    TF_COLD_BIN="$CASE3_DIR/fakebin/terraform" \
    TOFU_BIN="$CASE3_DIR/fakebin/choudoufu" \
    LIVECERT_TEARDOWN_ONLY="$CASE3_DIR" \
    LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY=yes \
    LIVECERT_KEEP_WORK=1 \
    bash "$ROOT/live/live-cert/terralith-scale.sh" 2>&1)"
  CASE3_RC=$?
  printf '%s\n' "$CASE3_OUT" | sed 's/^/  /'

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
