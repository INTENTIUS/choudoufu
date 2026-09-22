#!/usr/bin/env bash
set -uo pipefail

# live/smoke/selftest-teardown.sh: proof for issue #1378.
#
# The real-AWS smoke scenarios tear down in an EXIT trap, and smoke.sh runs
# them under `set -euo pipefail`. A trap body is ordinary code there: the
# first command that fails ends the whole trap and every step after it is
# skipped, with nothing printed. Five lines reproduce it:
#
#   set -euo pipefail
#   t() { echo start; x="$(false)"; echo MUST-PRINT; }
#   trap 't; echo cleanup' EXIT
#   exit 1        # prints "start" and nothing else
#
# In claim 37 that shape meant one throttled list-object-versions skipped
# `just down`, the role deletion, the deletion of a created KMS key and the
# restore of a BORROWED key's policy, leaving real resources behind and
# somebody else's key carrying this run's policy.
#
# This self-test runs the SHIPPED teardown bodies - sourced from
# bucket-iam.sh, or extracted verbatim from the scenario files - against a
# stub `aws` and a stub `just`, in a harness shell under `set -euo
# pipefail`, and reads the stub's call log to check that every teardown
# step was ATTEMPTED and that the failing one printed a COULD NOT line. No
# AWS call is made and no account is touched; nothing here needs
# SMOKE_REAL_AWS.
#
# The verdict is the per-case lines below, not the exit code. Every case
# prints "ok:" per property it confirmed and "FAIL:" per property it did
# not, and the script exits non-zero if any FAIL was printed.
#
# Usage:
#   bash live/smoke/selftest-teardown.sh
#   SMOKE_SRC=<a copy of live/smoke> bash live/smoke/selftest-teardown.sh
#     runs the same cases against another tree - which is how the red was
#     shown: against live/smoke as of main before this issue, every case
#     below fails.
#   bash live/smoke/selftest-teardown.sh --only <case>   one case, by name.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SMOKE_SRC="${SMOKE_SRC:-$HERE}"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"

ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

WORK="$(mktemp -d)"
# The status is saved and re-raised, and a run that stops before its last
# case is refused (#1421, the shape #1419 gave selftest-oidc-bootstrap.sh).
# Measured on bash 3.2.57: an unbound-variable death under `set -e` hands
# the EXIT trap $?=0 and the script exits 0, which is how that one read as
# a pass when it died part way. This script runs without -e and such a
# death already exits 1; the flag is for the other way a run can stop early
# with status 0 - an exit reached inside code run in this shell, or an -e
# added later - and it turns that into a FAIL line and exit 1 as well.
selftest_finished=0
# shellcheck disable=SC2154 # selftest_rc is assigned on the trap's first line
trap 'selftest_rc=$?
      rm -rf "$WORK"
      if [ "$selftest_finished" != 1 ]; then
        echo "FAIL: this selftest stopped before its last case, so most of it never ran. Read the output above for where." >&2
        exit 1
      fi
      exit $selftest_rc' EXIT
PASS=1

log() { printf '%s\n' "$*"; }
ok() { log "  ok: $*"; }
bad() { log "  FAIL: $*"; PASS=0; }
wanted() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }

# ── the stubs ───────────────────────────────────────────────────────────
# `aws` logs every invocation and answers the handful of calls a teardown
# makes. AWS_FAIL_GLOB / AWS_FAIL_GLOB2 name calls it should refuse, which
# is how a middle step is made to fail.
write_stubs() { # <bin dir>
  mkdir -p "$1"
  cat > "$1/aws" <<'AWSEOF'
#!/usr/bin/env bash
args="$*"
printf '%s\n' "$args" >> "$AWS_LOG"
for glob in "${AWS_FAIL_GLOB:-}" "${AWS_FAIL_GLOB2:-}"; do
  [ -n "$glob" ] || continue
  # shellcheck disable=SC2254  # the glob is the point
  case "$args" in
    $glob) echo "stub aws: refusing '$args' on purpose (AWS_FAIL_GLOB)" >&2; exit 254 ;;
  esac
done
case "$args" in
  *list-object-versions*)
    # One version on the first listing, none after the delete: an emptying
    # loop that never sees a non-empty bucket would exercise nothing.
    n=0
    [ -f "$AWS_STATE/listed" ] && n="$(cat "$AWS_STATE/listed")"
    if [ "$n" = "0" ]; then
      echo 1 > "$AWS_STATE/listed"
      printf '{"Objects":[{"Key":"tofu-records/smoke/x","VersionId":"v1"}]}\n'
    else
      printf '{"Objects":null}\n'
    fi
    ;;
  *get-caller-identity*) printf '123456789012\n' ;;
esac
exit 0
AWSEOF
  cat > "$1/just" <<'JUSTEOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$JUST_LOG"
if [ -n "${JUST_FAIL_GLOB:-}" ]; then
  # shellcheck disable=SC2254
  case "$*" in
    $JUST_FAIL_GLOB) echo "stub just: refusing '$*' on purpose" >&2; exit 3 ;;
  esac
fi
exit 0
JUSTEOF
  chmod +x "$1/aws" "$1/just"
}

# sandbox <name>: a case's own bin, logs, work root and stub project.
sandbox() {
  SB="$WORK/$1"
  mkdir -p "$SB/bin" "$SB/state" "$SB/project" "$SB/root" "$SB/work"
  write_stubs "$SB/bin"
  AWS_LOG="$SB/aws.log"; JUST_LOG="$SB/just.log"
  : > "$AWS_LOG"; : > "$JUST_LOG"
  AWS_FAIL_GLOB=""; AWS_FAIL_GLOB2=""; JUST_FAIL_GLOB=""
  HARNESS="$SB/harness.sh"
  : > "$HARNESS"
  export AWS_LOG JUST_LOG
  log ""
  log "=== $1 ==="
}

# prologue: what smoke.sh and lib.sh would have provided, and nothing else.
# bucket-iam.sh is SOURCED from the tree under test, so what runs below is
# the shipped teardown and not a copy of it.
prologue() {
  cat >> "$HARNESS" <<'PROEOF'
set -euo pipefail
ROOT="$SELFTEST_REPO_ROOT"
SMOKE_DIR="$SELFTEST_SMOKE_SRC"
SMOKE_WORKROOT="$SELFTEST_SB/root"
SMOKE_WORK="$SELFTEST_SB/work"
PROJECT="$SELFTEST_SB/project"
export AWS_REGION=us-east-2
ACCOUNT=123456789012
SUFFIX="9999-000000"
fail() { echo "SCENARIO-FAILED [$1]: $2" >&2; exit 1; }
step() { echo "=== $* ==="; }
note() { echo "  $*"; }
explain() { while [ $# -gt 0 ]; do echo "  $1"; shift; done; }
cmd() { echo "  \$ $*"; }
evidence() { sed 's/^/      /'; }
proof() { echo "  -> $*"; }
mask() { cat; }
flat() { tr '\n' ' '; }
# smoke.sh's own last step. Every trap body in the scenarios ends with it,
# so "cleanup ran" is exactly "the trap reached its last step".
cleanup() { echo "CLEANUP-RAN"; }
# shellcheck source=bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"
PROEOF
}

# extract_func <file> <name>: the function verbatim out of the scenario.
# Verbatim matters: a copy in this file would go on passing after the
# scenario changed, which is the class of defect this repository keeps
# finding.
extract_func() {
  awk -v want="$2() {" '$0 == want {p=1} p {print} p && $0 == "}" {exit}' "$1"
}

# extract_range <file> <start regex> <end regex>: the lines of a scenario
# from the first match of one to the first match of the other, inclusive.
extract_range() {
  awk -v s="$2" -v e="$3" '$0 ~ s {p=1} p {print} p && $0 ~ e && NR > 1 && $0 !~ s {exit}' "$1"
}

run_harness() {
  SELFTEST_REPO_ROOT="$REPO_ROOT" SELFTEST_SMOKE_SRC="$SMOKE_SRC" SELFTEST_SB="$SB" \
  AWS_LOG="$AWS_LOG" AWS_STATE="$SB/state" JUST_LOG="$JUST_LOG" \
  AWS_FAIL_GLOB="$AWS_FAIL_GLOB" AWS_FAIL_GLOB2="$AWS_FAIL_GLOB2" JUST_FAIL_GLOB="$JUST_FAIL_GLOB" \
  PATH="$SB/bin:$PATH" \
    bash "$HARNESS" > "$SB/out" 2>&1 || true
}

has() { # <haystack file> <needle> <what it would mean>
  if grep -qF -- "$2" "$1"; then ok "$3"; else
    bad "$3 - nothing matching '$2' in $(basename "$1")"
    sed 's/^/        | /' "$1" | head -40
  fi
}
hasnt() { # <haystack file> <needle> <what it would mean>
  if grep -qF -- "$2" "$1"; then
    bad "$3 - '$2' is present in $(basename "$1")"
  else ok "$3"; fi
}

# ── case: a failing step inside real_aws_teardown ───────────────────────
# bucket-iam.sh's own teardown, sourced, with one role and one bucket
# registered and a scenario that dies the way a scenario dies.
teardown_case() { # <name> <AWS_FAIL_GLOB> <description>
  sandbox "$1"
  AWS_FAIL_GLOB="$2"
  log "  a scenario fails mid-run; the trap must still attempt every step ($3)"
  prologue
  {
    echo 'REAL_ROLES=("smoke-selftest-role-$SUFFIX")'
    echo 'REAL_BUCKETS=("chdf-selftest-bucket-$SUFFIX")'
    # The trap line as real_aws_begin installs it, read out of the tree
    # under test rather than written here.
    grep -m1 "trap .*real_aws_teardown" "$SMOKE_SRC/bucket-iam.sh"
    echo 'echo SCENARIO-RAN'
    echo 'BOOM="$(false)"   # the #1378 reproduction, inside a real scenario'
    echo 'echo NOT-REACHED'
  } >> "$HARNESS"
  run_harness
  has "$SB/out" "SCENARIO-RAN" "the scenario body ran"
  hasnt "$SB/out" "NOT-REACHED" "the scenario died where it was meant to"
  has "$AWS_LOG" "iam delete-role-policy" "the role's policy deletion was attempted"
  has "$AWS_LOG" "iam delete-role --role-name" "the role deletion was attempted"
  has "$AWS_LOG" "s3api list-object-versions" "the bucket's version listing was attempted"
  has "$AWS_LOG" "s3api delete-bucket" "the bucket deletion was attempted"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
}

if wanted teardown-survives-a-failed-role-deletion; then
  teardown_case teardown-survives-a-failed-role-deletion 'iam delete-role --role-name*' "the role deletion is refused"
  has "$SB/out" "COULD NOT REMOVE role" "the refused role deletion named itself"
  has "$AWS_LOG" "s3api delete-objects" "the bucket was still emptied after it"
fi

if wanted teardown-survives-a-failed-listing; then
  teardown_case teardown-survives-a-failed-listing 's3api list-object-versions*' "the version listing is throttled, as on the run that prompted #1378"
  has "$SB/out" "COULD NOT LIST the object versions" "the failed listing named the bucket"
fi

if wanted teardown-empties-then-deletes; then
  teardown_case teardown-empties-then-deletes '' "nothing is refused, and the bucket holds a version"
  has "$AWS_LOG" "s3api delete-objects" "the object version was deleted"
  has "$SB/out" "removed bucket" "the bucket removal reported success"
  hasnt "$SB/out" "COULD NOT" "nothing printed a COULD NOT line on a clean teardown"
fi

# ── case: claim 37's teardown, extracted verbatim ───────────────────────
secure_harness() { # writes the claim 37 teardown harness; caller sets the vars
  prologue
  {
    echo 'BUCKET="chdf-smoke-secure-$SUFFIX"'
    echo 'KEY_ARN="arn:aws:kms:us-east-2:123456789012:key/11111111-2222-3333-4444-555555555555"'
    echo 'REAL_ROLES=("smoke-secure-estate-$SUFFIX")'
    echo 'REAL_BUCKETS=()'
    cat
    extract_func "$SMOKE_SRC/scenarios/the-recommended-secure-configuration.sh" secure_teardown
    grep -m1 "trap .*secure_teardown" "$SMOKE_SRC/scenarios/the-recommended-secure-configuration.sh"
    echo 'echo SCENARIO-RAN'
    echo 'BOOM="$(false)"'
    echo 'echo NOT-REACHED'
  } >> "$HARNESS"
}

if wanted claim37-teardown-reaches-the-key; then
  sandbox claim37-teardown-reaches-the-key
  log "  the borrowed key's policy is restored and the saved copy removed, although just down failed"
  JUST_FAIL_GLOB='down*'
  POLICY_FILE="$SB/keypolicy.json"
  printf '{"Version":"2012-10-17","Statement":[{"Sid":"TheOriginal"}]}\n' > "$POLICY_FILE"
  secure_harness <<HEOF
STACK_UP=1
CREATED_KEY=""
ORIGINAL_KEY_POLICY='{"Version":"2012-10-17","Statement":[{"Sid":"TheOriginal"}]}'
KEY_POLICY_FILE="$POLICY_FILE"
HEOF
  run_harness
  has "$AWS_LOG" "s3api list-object-versions" "the bucket was emptied before just down"
  has "$AWS_LOG" "s3api delete-objects" "the object version was deleted"
  has "$JUST_LOG" "down" "just down was attempted"
  has "$SB/out" "COULD NOT REMOVE stack" "the failed just down named the stack"
  has "$AWS_LOG" "s3api delete-bucket" "the retained bucket was still deleted, although just down failed"
  has "$AWS_LOG" "kms put-key-policy" "the borrowed key's policy was restored after it"
  has "$AWS_LOG" "--policy file://" "the restore read the policy from the file on disk, not a shell variable"
  has "$SB/out" "restored the key's original policy" "the restore reported success"
  has "$AWS_LOG" "iam delete-role" "the role deletion was reached"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
  if [ -e "$POLICY_FILE" ]; then
    bad "the saved policy file is removed once the key policy is back - $POLICY_FILE is still there"
  else
    ok "the saved policy file is removed once the key policy is back"
  fi
fi

if wanted claim37-keeps-the-policy-file-when-the-restore-fails; then
  sandbox claim37-keeps-the-policy-file-when-the-restore-fails
  log "  a failed restore keeps the borrowed key's policy on disk and prints the command to put it back"
  AWS_FAIL_GLOB='kms put-key-policy*'
  POLICY_FILE="$SB/keypolicy.json"
  printf '{"Version":"2012-10-17","Statement":[{"Sid":"TheOriginal"}]}\n' > "$POLICY_FILE"
  secure_harness <<HEOF
STACK_UP=0
CREATED_KEY=""
ORIGINAL_KEY_POLICY='{"Version":"2012-10-17","Statement":[{"Sid":"TheOriginal"}]}'
KEY_POLICY_FILE="$POLICY_FILE"
HEOF
  run_harness
  has "$AWS_LOG" "kms put-key-policy" "the restore was attempted"
  has "$SB/out" "COULD NOT RESTORE the key policy" "the failed restore said so"
  has "$SB/out" "$POLICY_FILE" "the failure printed the path of the saved policy"
  has "$SB/out" "aws kms put-key-policy --key-id" "the failure printed the command that puts it back"
  has "$AWS_LOG" "iam delete-role" "the role deletion was reached after the failed restore"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
  if [ -e "$POLICY_FILE" ] && grep -qF "TheOriginal" "$POLICY_FILE"; then
    ok "the borrowed key's original policy is still on disk, unchanged"
  else
    bad "the borrowed key's original policy is gone from $POLICY_FILE after a failed restore"
  fi
fi

if wanted claim37-schedules-a-created-key-after-a-failed-emptying; then
  sandbox claim37-schedules-a-created-key-after-a-failed-emptying
  log "  a key this run created is still scheduled for deletion when the emptying is refused"
  AWS_FAIL_GLOB='s3api list-object-versions*'
  secure_harness <<'HEOF'
STACK_UP=1
CREATED_KEY="11111111-2222-3333-4444-555555555555"
ORIGINAL_KEY_POLICY=""
KEY_POLICY_FILE=""
HEOF
  run_harness
  has "$SB/out" "COULD NOT LIST the object versions" "the refused listing named the bucket"
  has "$JUST_LOG" "down" "just down was still attempted"
  has "$AWS_LOG" "kms schedule-key-deletion" "the created key was still scheduled for deletion"
  has "$AWS_LOG" "iam delete-role" "the role deletion was still reached"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
fi

# ── case: STACK_UP is set before `just up`, not after ───────────────────
# The `just up` block of each scenario, verbatim, with a `just` that fails
# the way a half-finished deploy does. The stack teardown has to run.
stack_up_case() { # <name> <scenario> <teardown fn> <start regex> <what the teardown calls>
  sandbox "$1"
  log "  a deploy that fails must still be torn down ($2)"
  JUST_FAIL_GLOB='up*'
  prologue
  {
    echo 'BUCKET="chdf-smoke-selftest-$SUFFIX"'
    echo 'KEY_ARN="arn:aws:kms:us-east-2:123456789012:key/1111"'
    echo 'CREATED_KEY=""; ORIGINAL_KEY_POLICY=""; KEY_POLICY_FILE=""'
    echo 'REAL_ROLES=(); REAL_BUCKETS=()'
    echo 'STACK_UP=0'
    extract_func "$SMOKE_SRC/scenarios/$2" "$3"
    grep -m1 "trap .*$3" "$SMOKE_SRC/scenarios/$2"
    extract_range "$SMOKE_SRC/scenarios/$2" "$4" '^cmd "just verify '
    echo 'echo NOT-REACHED'
  } >> "$HARNESS"
  grep -q 'STACK_UP=1' "$HARNESS" || bad "the extraction found no STACK_UP=1 in $2; this case would prove nothing"
  grep -q 'just up' "$HARNESS" || bad "the extraction found no 'just up' in $2; this case would prove nothing"
  run_harness
  has "$JUST_LOG" "up" "just up was attempted"
  hasnt "$SB/out" "NOT-REACHED" "the scenario stopped on the failed deploy"
  has "$SB/out" "SCENARIO-FAILED" "the scenario failed by name"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
}

if wanted claim4-tears-down-a-failed-deploy; then
  stack_up_case claim4-tears-down-a-failed-deploy backend-sets-itself-up.sh auto_teardown '^cmd "just up '
  has "$AWS_LOG" "cloudformation" "the stack teardown ran, so STACK_UP was set BEFORE just up"
  has "$AWS_LOG" "s3api delete-bucket" "the bucket the stack retains was deleted after it"
  has "$SB/out" "removed the retained bucket" "the bucket removal reported success"
fi

if wanted claim37-tears-down-a-failed-deploy; then
  stack_up_case claim37-tears-down-a-failed-deploy the-recommended-secure-configuration.sh secure_teardown '^cmd ".*just up '
  has "$JUST_LOG" "down" "just down ran, so STACK_UP was set BEFORE just up"
  has "$AWS_LOG" "s3api delete-bucket" "the bucket the stack retains was deleted after it"
  has "$SB/out" "removed the retained bucket" "the bucket removal reported success"
fi

# ── case: the stack RETAINS its bucket (#1382) ──────────────────────────
# examples/record-store-bucket's bucket carries DeletionPolicy Retain, so a
# deleted stack leaves the bucket in the account. A teardown that stops at
# the stack leaks one bucket per run and says it did not.
if wanted claim4-says-so-when-the-retained-bucket-stays; then
  sandbox claim4-says-so-when-the-retained-bucket-stays
  log "  a retained bucket that cannot be deleted is named, and the trap carries on"
  AWS_FAIL_GLOB='s3api delete-bucket*'
  prologue
  {
    echo 'BUCKET="chdf-smoke-selftest-$SUFFIX"'
    echo 'REAL_ROLES=(); REAL_BUCKETS=()'
    echo 'STACK_UP=1'
    extract_func "$SMOKE_SRC/scenarios/backend-sets-itself-up.sh" auto_teardown
    grep -m1 "trap .*auto_teardown" "$SMOKE_SRC/scenarios/backend-sets-itself-up.sh"
    echo 'echo SCENARIO-RAN'
    echo 'BOOM="$(false)"'
    echo 'echo NOT-REACHED'
  } >> "$HARNESS"
  run_harness
  has "$AWS_LOG" "s3api delete-bucket" "the deletion was attempted"
  has "$SB/out" "COULD NOT REMOVE the retained bucket" "the failed deletion named the bucket"
  hasnt "$SB/out" "removed stack and bucket" "nothing claims the bucket went with the stack"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
fi

# ── case: a missing stack and a missing bucket are tolerated ────────────
if wanted claim4-tolerates-a-stack-that-never-appeared; then
  sandbox claim4-tolerates-a-stack-that-never-appeared
  log "  a deploy that created nothing at all leaves a teardown with nothing to do, and it says so"
  AWS_FAIL_GLOB='s3api head-bucket*'
  AWS_FAIL_GLOB2='cloudformation describe-stacks*'
  prologue
  {
    echo 'BUCKET="chdf-smoke-selftest-$SUFFIX"'
    echo 'REAL_ROLES=(); REAL_BUCKETS=()'
    echo 'STACK_UP=1'
    extract_func "$SMOKE_SRC/scenarios/backend-sets-itself-up.sh" auto_teardown
    grep -m1 "trap .*auto_teardown" "$SMOKE_SRC/scenarios/backend-sets-itself-up.sh"
    echo 'echo SCENARIO-RAN'
    echo 'BOOM="$(false)"'
    echo 'echo NOT-REACHED'
  } >> "$HARNESS"
  run_harness
  has "$SB/out" "no bucket" "the missing bucket was reported, not treated as a failure"
  has "$SB/out" "no stack" "the missing stack was reported, not treated as a failure"
  hasnt "$SB/out" "COULD NOT" "nothing that was never created produced a COULD NOT line"
  has "$SB/out" "CLEANUP-RAN" "the trap reached its last step"
fi

# ── case: role names ────────────────────────────────────────────────────
if wanted role-names-carry-the-run-suffix; then
  sandbox role-names-carry-the-run-suffix
  log "  a role name belongs to one run, and one too long for IAM is refused"
  prologue
  {
    echo 'set +e'
    echo 'echo "NAME=$(role_name smoke-secure-estate)"'
    # The brackets are not decoration: written LONG_RC=1 this matched the
    # 127 of a role_name that does not exist yet, which made the case pass
    # against the very tree it was written to redden.
    echo 'LONG="$(role_name aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)"; echo "LONG_RC=[$?]"'
    echo 'echo "LONG=$LONG"'
  } >> "$HARNESS"
  run_harness
  has "$SB/out" "NAME=smoke-secure-estate-9999-000000" "the role name carries the run's suffix"
  has "$SB/out" "LONG_RC=[1]" "a name over IAM's 64 characters is refused"
  has "$SB/out" "IAM allows 64" "the refusal says why"
fi

if wanted role-with-policy-refuses-a-leaked-role; then
  sandbox role-with-policy-refuses-a-leaked-role
  log "  a role this run did not create is refused rather than adopted"
  prologue
  {
    echo 'REAL_ROLES=(); REAL_BUCKETS=()'
    echo 'set +e'
    echo 'role_with_policy "smoke-secure-estate-$SUFFIX" "{}" "chdf-selftest-bucket"'
    echo 'echo "RWP_RC=[$?]"'
  } >> "$HARNESS"
  run_harness
  has "$SB/out" "RWP_RC=[1]" "role_with_policy refused"
  has "$SB/out" "already exists and this run did not create it" "the refusal says what is wrong"
  hasnt "$AWS_LOG" "iam put-role-policy" "no policy was written over the leaked role's"
  hasnt "$AWS_LOG" "s3api put-object" "the function stopped before it did anything else"
fi

if wanted role-with-policy-reuses-its-own-role; then
  sandbox role-with-policy-reuses-its-own-role
  log "  a role the SAME run created is reused, which is what claim 37 needs"
  prologue
  {
    echo 'REAL_ROLES=("smoke-secure-estate-$SUFFIX"); REAL_BUCKETS=()'
    echo 'set +e'
    echo 'role_with_policy "smoke-secure-estate-$SUFFIX" "{}" "chdf-selftest-bucket"'
    echo 'echo "RWP_RC=[$?]"'
  } >> "$HARNESS"
  run_harness
  hasnt "$SB/out" "already exists and this run did not create it" "its own role was not refused"
  hasnt "$AWS_LOG" "iam create-role" "its own role was not created twice"
  has "$AWS_LOG" "s3api put-object" "it went on to install the policy"
fi

# ── case: this check can fail ───────────────────────────────────────────
# The cases above pass against the shipped tree. That is worth nothing
# unless they fail against a tree without the fix, so one is re-run here
# against a copy whose `set +e` has been taken back out of
# real_aws_teardown. If the copy passes, the check is scenery.
if wanted "" && [ -z "$ONLY" ]; then
  log ""
  log "=== the-check-can-fail ==="
  MUT="$WORK/mutant"
  cp -R "$SMOKE_SRC" "$MUT"
  python3 - "$MUT/bucket-iam.sh" <<'PYEOF'
import re, sys
p = sys.argv[1]
src = open(p).read()
start = src.index("real_aws_teardown() {")
head, tail = src[:start], src[start:]
mutated = tail.replace("  set +e\n", "", 1).replace("  set +u\n", "", 1)
assert mutated != tail, "real_aws_teardown has no `set +e` to remove; this mutation proves nothing"
open(p, "w").write(head + mutated)
PYEOF
  if [ $? -ne 0 ]; then
    bad "could not build the mutant tree, so nothing here shows the check can fail"
  else
    # The trap line is mutated too: leaving it would let the trap's own
    # `set +e` stand in for the one removed from the function.
    python3 - "$MUT/bucket-iam.sh" <<'PYEOF'
import sys
p = sys.argv[1]
src = open(p).read()
old = "trap 'set +e; set +u; real_aws_teardown; cleanup' EXIT"
if old in src:
    open(p, "w").write(src.replace(old, "trap 'real_aws_teardown; cleanup' EXIT"))
PYEOF
    if SMOKE_SRC="$MUT" bash "$0" --only teardown-survives-a-failed-listing > "$WORK/mutant.out" 2>&1; then
      bad "the same case PASSED against a teardown with no 'set +e', so it does not measure what it claims to"
      sed 's/^/        | /' "$WORK/mutant.out" | head -30
    else
      ok "the same case fails against a teardown with no 'set +e' (the pre-#1378 shape)"
      grep -m3 "FAIL:" "$WORK/mutant.out" | sed 's/^/        mutant | /'
    fi
  fi
fi

log ""
selftest_finished=1
if [ "$PASS" = "1" ]; then
  log "PASS: selftest-teardown - every teardown reached its last step, and each failing step named itself (#1378)"
  exit 0
fi
log "FAIL: selftest-teardown - see the FAIL lines above (#1378)"
exit 1
