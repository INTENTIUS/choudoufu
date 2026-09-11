#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-teardown-timeout.sh: proof for issue #1048.
#
# terralith-scale.sh's teardown() ran an untrusted "choudoufu's own destroy
# path" step BEFORE the trusted `terraform destroy` against cold_deploy's
# own untouched state, with no timeout on the untrusted step and a
# TEARDOWN_DONE re-entry guard set at function ENTRY rather than at the
# trusted destroy's completion. A hang in the untrusted step therefore
# blocked the trusted destroy indefinitely; left alone it would have run to
# this script's own 25,200s process ceiling with the trusted destroy never
# reached, and a signal arriving during that hang would have hit the guard
# (already 1 from entry) and made a re-entrant teardown() call skip the
# trusted destroy outright. On the 2026-09-11 scale-50 run the untrusted
# step hung ~40 minutes on CreatePolicy/EntityAlreadyExists and was
# unblocked only by a hand SIGTERM.
#
# This self-test extracts teardown() and provider_block() VERBATIM out of
# a real terralith-scale.sh (default: the one shipped beside this script)
# and runs them in a minimal, fully-stubbed harness: no AWS calls, no
# docker, no terraform, no go build. The untrusted step's own binary (TOFU)
# is a fake that sleeps (simulating the hang); the trusted step's binary
# (TF_COLD) is a fake that writes a marker file - the one thing this test
# actually checks for. verify_empty/sweep are stubbed out entirely: whether
# they themselves work is issue #1047's territory (see
# selftest-pagination.sh), not this one's.
#
# The self-test's own OUTER bound (further below) exists only so that
# running this against the UNFIXED script - which has no inner timeout at
# all - fails in seconds rather than hanging this self-test for the fake
# step's own sleep duration.
#
# Usage: bash live/live-cert/selftest-teardown-timeout.sh
#   TERRALITH_SCALE_SH=<path> to extract from a different revision, e.g.
#   the pre-fix content via process substitution:
#     TERRALITH_SCALE_SH=<(git show HEAD~1:live/live-cert/terralith-scale.sh) \
#       bash live/live-cert/selftest-teardown-timeout.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

command -v timeout >/dev/null 2>&1 || { echo "FAIL: timeout is not on PATH - this self-test cannot run"; exit 1; }

# Materialize SRC_ARG into a plain file: it may be a process substitution
# (e.g. TERRALITH_SCALE_SH=<(git show ...)), and a FIFO can only be read
# once, while extract_func below reads it twice.
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"

echo "=== selftest-teardown-timeout: extracting teardown()/provider_block() from $SRC_ARG ==="

# Anchored on the exact signature both functions have always had ("<name>()
# {" on its own line) and tracks brace depth (skipping the body of any
# heredoc, which can itself contain a bare "}" line - teardown()'s own
# heredoc writes an HCL block whose closing brace sits at column 0, which a
# naive "first bare } line" extractor matches and truncates on) so it
# closes on the function's OWN closing brace, not the first one anywhere
# inside it. A source file missing the function (a move/rename) fails
# loudly here rather than silently testing an empty function.
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

TEARDOWN_SRC="$(extract_func teardown "$SRC")"
PROVIDER_SRC="$(extract_func provider_block "$SRC")"
[ -n "$TEARDOWN_SRC" ] || { echo "FAIL: could not find teardown() in $SRC"; exit 1; }
[ -n "$PROVIDER_SRC" ] || { echo "FAIL: could not find provider_block() in $SRC"; exit 1; }

BIN="$WORK/bin"; mkdir -p "$BIN"
ADOPTED_DIR_PATH="$WORK/adopted"; COLD_DIR_PATH="$WORK/cold"
mkdir -p "$ADOPTED_DIR_PATH" "$COLD_DIR_PATH"
MARKER="$WORK/trusted_destroy_ran"

cat > "$BIN/fake-tofu" <<'EOF'
#!/usr/bin/env bash
# the UNTRUSTED step's binary: hangs, exactly like the real
# CreatePolicy/EntityAlreadyExists hang observed 2026-09-11. Finite (not
# forever) purely so a run against the unfixed script - which has nothing
# to kill this with - cannot leave a permanent orphan process behind.
sleep 30
EOF
cat > "$BIN/fake-terraform" <<EOF
#!/usr/bin/env bash
# the TRUSTED step's binary: the one thing this self-test actually checks.
touch "$MARKER"
exit 0
EOF
chmod +x "$BIN/fake-tofu" "$BIN/fake-terraform"
: > "$COLD_DIR_PATH/terraform.tfstate"   # trusted destroy's own gate in teardown()

RUNNER="$WORK/runner.sh"
{
  printf '%s\n' '#!/usr/bin/env bash'
  printf '%s\n' 'set -uo pipefail'
  printf '%s\n' "$TEARDOWN_SRC"
  printf '%s\n' "$PROVIDER_SRC"
  # The harness: everything else teardown() touches, stubbed.
  printf '%s\n' 'log() { printf "  [harness] %s\n" "$*"; }'
  printf '%s\n' 'verify_empty() { return 0; }'
  printf '%s\n' 'sweep() { :; }'
  printf 'TEARDOWN_DONE=0\n'
  printf 'MIGRATE_DONE=1\n'
  printf 'UNTRUSTED_TEARDOWN_TIMEOUT_S=%q\n' "${UNTRUSTED_TEARDOWN_TIMEOUT_S:-2}"
  printf 'ESTATE=tl-livecert-selftest\n'
  printf 'RECORD_STORE_BACKEND=local\n'
  printf 'RECORD_STORE_ARGS=%q\n' '      path = ".tofu-records"'
  printf 'PREFIX=selftest\n'
  printf 'RUN_ID=selftest-teardown-%s\n' "$$"
  printf 'SCALE=1\n'
  printf 'TARGET=aws\n'
  printf 'ENDPOINT=\n'
  printf 'REGION=us-east-1\n'
  printf 'ADOPTED_DIR=%q\n' "$ADOPTED_DIR_PATH"
  printf 'COLD_DIR=%q\n' "$COLD_DIR_PATH"
  printf 'WORK=%q\n' "$WORK"
  printf 'TOFU=%q\n' "$BIN/fake-tofu"
  printf 'TF_COLD=%q\n' "$BIN/fake-terraform"
  printf 'LIVECERT_KEEP_WORK=1\n'
  printf '%s\n' 'teardown'
} > "$RUNNER"

# Outer bound: comfortably more than the fixed script's own (short)
# UNTRUSTED_TEARDOWN_TIMEOUT_S so the fix has time to finish, comfortably
# LESS than fake-tofu's own sleep so the unfixed script fails this test in
# seconds rather than in 30s.
OUTER_BOUND_S=15
echo "=== selftest-teardown-timeout: running teardown() with a hung untrusted step (fake-tofu sleeps 30s), outer self-test bound ${OUTER_BOUND_S}s ==="
START=$(date +%s)
timeout "${OUTER_BOUND_S}s" bash "$RUNNER"
RUNNER_RC=$?
END=$(date +%s)
echo "  runner exited ${RUNNER_RC} after $((END - START))s"

if [ -f "$MARKER" ]; then
  echo "=== selftest-teardown-timeout: PASS - the trusted destroy (fake-terraform) ran and left its marker, even though the untrusted step (fake-tofu) hung ==="
  exit 0
else
  echo "=== selftest-teardown-timeout: FAIL - the trusted destroy NEVER ran within ${OUTER_BOUND_S}s: the untrusted step's hang blocked (or the TEARDOWN_DONE guard skipped) the trusted path ==="
  exit 1
fi
