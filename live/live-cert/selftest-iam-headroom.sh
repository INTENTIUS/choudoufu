#!/usr/bin/env bash
set -uo pipefail

# live/live-cert/selftest-iam-headroom.sh: proof for issue #1230 (#1150's
# third item), without a live-cert run.
#
# The defect: IAM role quota exhausted from OUTSIDE a run - a leaked estate,
# another estate's live-cert, a console session - surfaced as cold_deploy
# failing with LimitExceeded, which the record reads as "choudoufu could not
# deploy this estate". The fix: before cold_deploy, terralith-scale.sh reads
# Roles/RolesQuota from `aws iam get-account-summary`, asks terralith-gen
# how many aws_iam_role instances SCALE creates, and on insufficient room
# emits `GAUNTLET refused=1 scale=N needed=N limit=N unit=iam-roles` and
# exits 2 with nothing created. If the check itself cannot run it fails
# OPEN, loudly, and cold_deploy proceeds.
#
# This drives two spans EXTRACTED from live/live-cert/terralith-scale.sh
# rather than running that script, which no selftest may do (#1380): the
# check block (iam_roles_needed + iam_role_headroom_check, between
# '# >>> iam role headroom check' and '# <<< iam role headroom check') and
# the gate that calls it before cold_deploy (between '# >>> iam role
# headroom gate' and '# <<< iam role headroom gate'). The driver appends a
# fake cold_deploy AFTER the gate - an echo and a `terraform apply` against
# a recording stub - so "nothing else ran" is observed, not assumed. The
# real gauntlet_refused (live/e2e/lib/gauntlet.sh) and livecert_aws
# (live/live-cert/lib/live-cert.sh) are sourced, so the line asserted here
# is the line tools/gauntlet parses; iam_roles_needed is stubbed, because
# the generator side is pinned by tools/terralith-gen's own
# TestIAMRoleInstancesMatchGeneratedHCL and needs a go build this selftest
# does not have.
#
# What it proves:
#   1. no headroom: exactly one GAUNTLET refused=1 line with scale, needed,
#      limit and unit=iam-roles; exit 2; cold_deploy never reached; aws was
#      called once, for get-account-summary, and terraform never
#   2. enough headroom: no refusal, cold_deploy reached
#   3. need == headroom is enough (the comparison is >, not >=)
#   4. aws fails: the loud could-not-run line names the error; proceeds
#   5. garbage from aws ("None", words, nothing): could-not-run; proceeds
#   6. garbage from the generator: could-not-run; proceeds, aws never asked
#   7. roles above quota: refused with limit=0, never a negative number
#   8. RESUMED=1: the gate does not run the check at all
#
# Usage: bash live/live-cert/selftest-iam-headroom.sh
#   TERRALITH_SCALE_SH=<path> to extract from a different revision, e.g. the
#   pre-fix content via process substitution:
#     TERRALITH_SCALE_SH=<(git show main:live/live-cert/terralith-scale.sh) \
#       bash live/live-cert/selftest-iam-headroom.sh
# Needs nothing but bash: no docker, no AWS CLI, no terraform, no go build.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC_ARG="${TERRALITH_SCALE_SH:-$ROOT/live/live-cert/terralith-scale.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
pass=1
log() { printf '%s\n' "$*"; }

# ── 0. extract both spans ───────────────────────────────────────────────
# Materialize SRC_ARG into a plain file first: it may be a process
# substitution, and it is read twice below. The copy is read by sed only,
# never executed (#1380).
SRC="$WORK/source.sh"
cat "$SRC_ARG" > "$SRC"
CHECK="$WORK/check.sh"
GATE="$WORK/gate.sh"
sed -n '/^# >>> iam role headroom check$/,/^# <<< iam role headroom check$/p' "$SRC" > "$CHECK"
sed -n '/^# >>> iam role headroom gate$/,/^# <<< iam role headroom gate$/p' "$SRC" > "$GATE"

if [ ! -s "$CHECK" ]; then
  log "FAIL: no IAM role headroom check found in $SRC_ARG between '# >>> iam role headroom check' and '# <<< iam role headroom check'."
  log "      If the markers were renamed, rename them here too; if the check was removed, #1230 is open again:"
  log "      an account out of IAM roles fails cold_deploy and the record blames the product."
  log "=== selftest-iam-headroom: FAIL - see above ==="
  exit 1
fi
if [ ! -s "$GATE" ] || ! grep -q '^# <<< iam role headroom gate$' "$GATE"; then
  log "FAIL: no complete IAM role headroom gate found in $SRC_ARG between '# >>> iam role headroom gate' and '# <<< iam role headroom gate'."
  log "      The check exists but nothing before cold_deploy calls it, or the span does not close."
  log "=== selftest-iam-headroom: FAIL - see above ==="
  exit 1
fi
for want in iam_roles_needed iam_role_headroom_check get-account-summary gauntlet_refused; do
  grep -q "$want" "$CHECK" || { log "FAIL: the extracted check does not mention $want"; pass=0; }
done
grep -q 'iam_role_headroom_check' "$GATE" || { log "FAIL: the extracted gate does not call iam_role_headroom_check"; pass=0; }
grep -q 'RESUMED' "$GATE" || { log "FAIL: the extracted gate does not consult RESUMED, so a resumed run would be checked for roles it already holds"; pass=0; }
log "=== 0. extracted $(wc -l < "$CHECK" | tr -d ' ') line(s) of check and $(wc -l < "$GATE" | tr -d ' ') line(s) of gate from $SRC_ARG ==="

# ── the stubs ───────────────────────────────────────────────────────────
BIN="$WORK/bin"; mkdir -p "$BIN"
# aws: records every invocation, answers get-account-summary per
# FAKE_AWS_MODE. The shapes are what the real CLI prints for
# `--query 'SummaryMap.[Roles,RolesQuota]' --output text`: two integers,
# tab-separated; "None" for a key the projection did not find.
cat > "$BIN/aws" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE_AWS_LOG"
case "${FAKE_AWS_MODE:-}" in
  noroom)   printf '990\t1000\n' ;;
  room)     printf '10\t1000\n' ;;
  exact)    printf '967\t1000\n' ;;
  over)     printf '1010\t1000\n' ;;
  fail)     printf 'An error occurred (Throttling) when calling the GetAccountSummary operation: Rate exceeded\n' >&2; exit 254 ;;
  none)     printf 'None\tNone\n' ;;
  words)    printf 'banana\n' ;;
  empty)    printf '\n' ;;
  *)        printf 'selftest: FAKE_AWS_MODE=%s unknown\n' "${FAKE_AWS_MODE:-}" >&2; exit 99 ;;
esac
EOF
# terraform: must never be reached on the refusal arm.
cat > "$BIN/terraform" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE_TF_LOG"
EOF
chmod +x "$BIN/aws" "$BIN/terraform"

# run_case <name> <aws mode> <need stub output> <RESUMED> -> writes
# $WORK/<name>.out and $WORK/<name>.rc, $WORK/<name>.aws, $WORK/<name>.tf
run_case() {
  local name="$1" mode="$2" need="$3" resumed="$4"
  local awslog="$WORK/$name.aws" tflog="$WORK/$name.tf"
  : > "$awslog"; : > "$tflog"
  cat > "$WORK/$name.sh" <<EOF
set -uo pipefail
ROOT="$ROOT"
WORK="$WORK/$name.work"; mkdir -p "\$WORK"
SCALE=3
REGION=us-east-2
ENDPOINT=""
log() { printf '%s\n' "\$*"; }
source "$ROOT/live/e2e/lib/gauntlet.sh"
source "$ROOT/live/live-cert/lib/live-cert.sh"
source "$CHECK"
# The generator side, stubbed: what \`terralith-gen -scale 3 -iam-roles\`
# prints. The real one is pinned by tools/terralith-gen's own test.
iam_roles_needed() { printf '%s\n' "$need"; }
RESUMED=$resumed
source "$GATE"
echo "REACHED_COLD_DEPLOY"
terraform apply -input=false -auto-approve
EOF
  ( cd "$WORK" && PATH="$BIN:$PATH" FAKE_AWS_MODE="$mode" FAKE_AWS_LOG="$awslog" FAKE_TF_LOG="$tflog" \
      bash "$WORK/$name.sh" > "$WORK/$name.out" 2>&1 )
  echo $? > "$WORK/$name.rc"
}

refusal_lines() { grep -c '^GAUNTLET refused=1 ' "$WORK/$1.out" || true; }
reached()       { grep -q '^REACHED_COLD_DEPLOY$' "$WORK/$1.out"; }
could_not_run() { grep -q 'IAM ROLE HEADROOM CHECK COULD NOT RUN' "$WORK/$1.out"; }
dump()          { sed 's/^/    /' "$WORK/$1.out"; }

# ── 1. no headroom: refused, nothing else ran ───────────────────────────
log ""
log "=== 1. no headroom (990 of 1000 roles used, scale=3 needs 33): refused, cold_deploy never reached ==="
run_case noroom noroom 33 0
N="$(refusal_lines noroom)"
LINE="$(grep '^GAUNTLET refused=1 ' "$WORK/noroom.out" | head -1)"
if [ "$N" = "1" ] && printf '%s\n' "$LINE" | grep -qE '^GAUNTLET refused=1 scale=3 needed=33 limit=10 unit=iam-roles detail=.+$'; then
  log "  exactly one refusal line, with the arithmetic tools/gauntlet parses (#1151):"
  log "    $LINE"
else
  log "FAIL: expected exactly one 'GAUNTLET refused=1 scale=3 needed=33 limit=10 unit=iam-roles detail=...' line, got $N:"
  dump noroom; pass=0
fi
if printf '%s\n' "$LINE" | grep -q '990' && printf '%s\n' "$LINE" | grep -q '1000'; then
  log "  the reason names the account's own numbers (990 roles, 1000 quota)"
else
  log "FAIL: the refusal's detail does not name the account's roles and quota, so a reader cannot check it: $LINE"; pass=0
fi
if [ "$(cat "$WORK/noroom.rc")" = "2" ]; then
  log "  exit 2"
else
  log "FAIL: exit $(cat "$WORK/noroom.rc"), want 2 (non-zero, and not fail()'s 1)"; dump noroom; pass=0
fi
if ! reached noroom; then
  log "  cold_deploy was not reached"
else
  log "FAIL: the driver reached cold_deploy after a refusal - the gate did not exit:"; dump noroom; pass=0
fi
if [ ! -s "$WORK/noroom.tf" ]; then
  log "  terraform was never invoked"
else
  log "FAIL: terraform was invoked after a refusal:"; sed 's/^/    /' "$WORK/noroom.tf"; pass=0
fi
if [ "$(grep -c . "$WORK/noroom.aws")" = "1" ] && grep -q 'iam get-account-summary' "$WORK/noroom.aws"; then
  log "  aws was called exactly once, for iam get-account-summary"
else
  log "FAIL: aws calls were not exactly one get-account-summary:"; sed 's/^/    /' "$WORK/noroom.aws"; pass=0
fi
if grep -q '^GAUNTLET stage=' "$WORK/noroom.out"; then
  log "FAIL: a stage line was spoken on the refusal path; a refusal is the whole record and fail() must not be involved:"; dump noroom; pass=0
else
  log "  no GAUNTLET stage= line: the refusal is the run's only protocol line"
fi

# ── 2. enough headroom: proceeds ────────────────────────────────────────
log ""
log "=== 2. enough headroom (10 of 1000 used): no refusal, cold_deploy reached ==="
run_case room room 33 0
if [ "$(refusal_lines room)" = "0" ] && reached room && [ "$(cat "$WORK/room.rc")" = "0" ]; then
  log "  proceeded: $(grep '  ok:' "$WORK/room.out")"
else
  log "FAIL: expected no refusal and REACHED_COLD_DEPLOY (rc=$(cat "$WORK/room.rc")):"; dump room; pass=0
fi

# ── 3. need == headroom is enough ───────────────────────────────────────
log ""
log "=== 3. exact fit (967 of 1000 used, room for 33, needs 33): proceeds ==="
run_case exact exact 33 0
if [ "$(refusal_lines exact)" = "0" ] && reached exact; then
  log "  proceeded on need == headroom: the comparison is >, not >="
else
  log "FAIL: an exact fit was refused or did not proceed:"; dump exact; pass=0
fi

# ── 4. aws fails: fail open, loudly ─────────────────────────────────────
log ""
log "=== 4. aws iam get-account-summary fails: could-not-run line names the error, cold_deploy reached ==="
run_case fail fail 33 0
if [ "$(refusal_lines fail)" = "0" ] && could_not_run fail && reached fail && grep -q 'Rate exceeded' "$WORK/fail.out"; then
  log "  failed open: $(grep 'COULD NOT RUN' "$WORK/fail.out")"
else
  log "FAIL: expected no refusal, a COULD NOT RUN line naming 'Rate exceeded', and REACHED_COLD_DEPLOY:"; dump fail; pass=0
fi

# ── 5. garbage from aws ─────────────────────────────────────────────────
log ""
log "=== 5. garbage from aws (None/None, a word, nothing): could-not-run, cold_deploy reached ==="
for mode in none words empty; do
  run_case "garbage_$mode" "$mode" 33 0
  if [ "$(refusal_lines "garbage_$mode")" = "0" ] && could_not_run "garbage_$mode" && reached "garbage_$mode"; then
    log "  $mode: $(grep 'COULD NOT RUN' "$WORK/garbage_$mode.out")"
  else
    log "FAIL: mode=$mode: expected no refusal, a COULD NOT RUN line and REACHED_COLD_DEPLOY:"; dump "garbage_$mode"; pass=0
  fi
done

# ── 6. garbage from the generator ───────────────────────────────────────
log ""
log "=== 6. the generator prints a non-integer: could-not-run, cold_deploy reached, aws never asked ==="
run_case genbad room "terralith-gen: -scale must be >= 1" 0
if [ "$(refusal_lines genbad)" = "0" ] && could_not_run genbad && reached genbad && [ ! -s "$WORK/genbad.aws" ]; then
  log "  failed open before touching aws: $(grep 'COULD NOT RUN' "$WORK/genbad.out")"
else
  log "FAIL: expected no refusal, a COULD NOT RUN line, REACHED_COLD_DEPLOY and no aws call:"; dump genbad; sed 's/^/    aws: /' "$WORK/genbad.aws"; pass=0
fi

# ── 7. roles above quota: limit=0 ───────────────────────────────────────
log ""
log "=== 7. roles above quota (1010 of 1000): refused with limit=0, never a negative number ==="
run_case over over 33 0
LINE="$(grep '^GAUNTLET refused=1 ' "$WORK/over.out" | head -1)"
if [ "$(refusal_lines over)" = "1" ] && printf '%s\n' "$LINE" | grep -qE '^GAUNTLET refused=1 scale=3 needed=33 limit=0 unit=iam-roles detail=' && ! reached over; then
  log "  $LINE"
else
  log "FAIL: expected one refusal with limit=0 and no REACHED_COLD_DEPLOY:"; dump over; pass=0
fi

# ── 8. RESUMED=1 skips the check ────────────────────────────────────────
log ""
log "=== 8. RESUMED=1: the gate does not run the check ==="
run_case resumed noroom 33 1
if [ "$(refusal_lines resumed)" = "0" ] && reached resumed && [ ! -s "$WORK/resumed.aws" ] && ! grep -q '0c. iam role headroom' "$WORK/resumed.out"; then
  log "  a resumed run went straight past the gate: no check, no aws call, no refusal (an account out of room would have refused a fresh run)"
else
  log "FAIL: a resumed run ran the headroom check (aws calls: $(grep -c . "$WORK/resumed.aws")):"; dump resumed; pass=0
fi

log ""
if [ "$pass" = "1" ]; then
  log "=== selftest-iam-headroom: PASS ==="
else
  log "=== selftest-iam-headroom: FAIL - see above ==="
fi
exit $((1 - pass))
