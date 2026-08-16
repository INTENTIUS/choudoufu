#!/usr/bin/env bash
set -euo pipefail

# Stateless mode E2E harness.
#
# This is the feature's demo as much as its test — run it and watch: a real
# estate stands up against a local AWS emulator with plain local state, the
# state file is deleted in front of you (`adopt`, nothing else happens), and
# then the claims stateless mode makes about that same live estate get proven
# live, one by one — empty plans against markers alone, exact drift (one
# mutation per estate type), foreign-resource protection, exact removal
# (delete a whole block, exactly its live resource goes), count scale-down
# with no churn, rename with no churn, a plain `choudoufu plan`/`apply` against a
# `live` block with no live-prefixed command anywhere in sight,
# a receipt cycle (break an effect's memory out of band, watch the plan
# re-arm it), and a drift-reconverge pass: three drifts of three different
# shapes injected out of band render on one plain plan as exactly two
# in-place updates and one create, and one untargeted apply reconverges
# all three. Nothing here is simulated or
# mocked: the emulator is a real AWS API surface, and every step is a real
# `choudoufu` binary built from this checkout.
#
#   bash live/e2e/run.sh
#
# This harness is also written to run from day one, before most of the
# features it exercises exist. Steps whose command surface (`choudoufu
# live-plan`, `choudoufu live-mv`) is not wired yet detect that and print
# `NOT IMPLEMENTED (phase N)` instead of failing — the harness is a progress
# bar across the roadmap's phases, not a wall that blocks every step on the
# last one. `standup` and `adopt` are real and green from the start; they use
# only stock `choudoufu init`/`apply` and file/AWS-CLI assertions.
#
# The estate fixture (live/e2e/estate/, task P0.1) is a separate
# deliverable. Until it exists in this checkout, `standup` fails fast naming
# the missing directory — that is the correct, expected result today.
#
# Every step that edits the estate's config (removal-exact, count-scale-down,
# rename-no-churn) works on its own mktemp copy. The checkout at
# live/e2e/estate is never written to; the copy applied against Floci
# (`$MAIN` below) is itself already a copy, made once at standup.
#
# This script never touches a git remote.
#
# Env overrides:
#   FLOCI_PORT   emulator port (default 4601; change it if that port is
#                taken on your machine)
#   FLOCI_NAME   container name (default includes $$ for uniqueness)
#   FLOCI_IMAGE  the emulator image (default: the pinned digest in
#                live/floci-image, the single source of truth —
#                lex00/floci's `latest` as of 2026-08-12, commit b2548a0 —
#                see the note above `docker run` in step 1 for why this fork
#                replaced upstream floci/floci:latest)
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`
#   LIVE_E2E_EXACTNESS  gates the drift-exact/removal-exact/receipt-cycle/
#                receipt-cycle-existence steps (6, 8, 12, 12b) on top of their
#                own -estate/live-mv probes; default 1 (P5.2 flipped it from
#                0 — P5.1's exactness work is proven in and this is a real run
#                against it, not a progress-bar placeholder). Set to 0 to
#                force those four back to NOT IMPLEMENTED for a
#                pre-exactness bisect, or to reproduce what `--expect 4`
#                verified before the exactness work landed (README.md,
#                "The reproduction contract").
#   LIVE_E2E_JSON=1     same as passing --json (see below).
#
# Flags:
#   --json           emit exactly one JSON object as the last line of stdout:
#                     {"steps":[{"name","status":pass|not_implemented|fail,
#                     "phase"}],"overall":"pass|fail"}. Human output is
#                     unchanged and still precedes it on stdout; see
#                     live/e2e/README.md for the parse contract.
#   --expect <phase>  exit 0 iff every step at or below <phase> is pass and
#                     every step above it is not_implemented; any fail exits
#                     nonzero regardless. `run.sh --expect <phase>` is this
#                     branch's reproducible claim; the exit code is the
#                     verdict (README.md, "The reproduction contract").
#
# Exit codes: 0 pass (or --expect met) or cleanly skipped (no Docker / aws
# CLI / go); non-zero on a real failure, or on --expect not being met. SKIP
# is for missing tooling; a step that runs and finds its claim false is FAIL,
# not SKIP.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ESTATE_SRC="$ROOT/live/e2e/estate"
BLOCK_SRC="$ROOT/live/e2e/estate-block"
FLOCI_PORT="${FLOCI_PORT:-4601}"
FLOCI_NAME="${FLOCI_NAME:-tofu-stateless-e2e-$$}"
# See the note above docker run in step 1 for why this is lex00/floci, not
# upstream floci/floci, and why it is pinned by digest. The pin's single
# source of truth is live/floci-image (#98); internal/live/flocitest and the
# Makefile read the same file.
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://localhost:${FLOCI_PORT}"

# ── CLI flags: --json / --expect <phase> (task PE.2) ────────────────────────
# LIVE_E2E_JSON=1 is the env-var spelling of --json (live/e2e/README.md).
JSON_MODE=0
EXPECT_PHASE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --json) JSON_MODE=1; shift ;;
    --expect) EXPECT_PHASE="${2:-}"; shift 2 ;;
    --expect=*) EXPECT_PHASE="${1#*=}"; shift ;;
    -h|--help)
      echo "usage: run.sh [--json] [--expect <phase>]"
      echo "see live/e2e/README.md for the full contract"
      exit 0 ;;
    *) echo "unknown argument: $1 (supported: --json, --expect <phase>)" >&2; exit 2 ;;
  esac
done
[ "${LIVE_E2E_JSON:-0}" = "1" ] && JSON_MODE=1
if [ -n "$EXPECT_PHASE" ]; then
  case "$EXPECT_PHASE" in
    ''|*[!0-9]*) echo "invalid --expect value: '$EXPECT_PHASE' (must be a non-negative integer phase)" >&2; exit 2 ;;
  esac
fi

# ── Step-result recording (task PE.2) ───────────────────────────────────────
# Every step records its outcome here -- from a success tail (`record_step
# name pass`), from not_implemented() (`record_step name not_implemented`),
# or from fail() (`record_step name fail`, for registry names only) -- so one
# JSON object can summarize the whole run regardless of how it ends. phase_of_
# step's numbers are the --expect grouping
# (standup/adopt/init/migration=0, empty-plan-named=1, empty-plan-full/
# foreign=2, scale-down/rename=3, plain-plan-works=4, drift/removal=5) -- a
# DIFFERENT scale than the "(phase N)" roadmap-task numbers already embedded
# in the NOT IMPLEMENTED messages below, which name which P<N>.x task
# unblocks a step. A plain function, not an associative array: the macOS
# system bash this harness targets is 3.2, which has no `declare -A`.
STEP_NAMES=()
STEP_STATUSES=()
STEP_PHASES=()
HARNESS_FAILED=0

phase_of_step() {
  case "$1" in
    standup|adopt|tofu-init|slot-migration|receipt-adoption) printf '0' ;;
    empty-plan-named) printf '1' ;;
    empty-plan-full|foreign-protected) printf '2' ;;
    count-scale-down|rename-no-churn) printf '3' ;;
    plain-plan-works) printf '4' ;;
    drift-exact|removal-exact|receipt-cycle|receipt-cycle-existence|drift-reconverge|lint-rejects) printf '5' ;;
    *) printf '' ;;
  esac
}

# SKIPPED records that the run ended in skip() rather than reaching the end.
# A skipped run verified nothing, which is a different thing from a run whose
# every claim held, and --expect and the JSON summary both have to say so.
SKIPPED=0

record_step() {
  local name="$1" status="$2" ph
  ph="$(phase_of_step "$name")"
  STEP_NAMES+=("$name")
  STEP_STATUSES+=("$status")
  STEP_PHASES+=("${ph:-null}")
}

# Emits exactly one JSON object on one line: {"steps":[...],"overall":...}.
# "overall" is a step-status signal, deliberately NOT redefined by --expect;
# --expect's verdict is the process exit code, not this field (README.md).
#
# $1 is the process's real exit status, and it is what makes the difference
# between a run that checked its claims and a run that died. Four values:
#
#   pass     every recorded step passed or legitimately reported
#            not_implemented, and the harness reached its own end.
#   fail     a claim was checked and found false (fail() ran, or a step
#            recorded "fail").
#   error    the harness did not finish and no fail() explains why - a signal,
#            a `set -e` abort, a crash. This is the case that used to emit
#            "pass": every one of the 18 `RC=$?` guards below fail()'s reach
#            was dead under `set -euo pipefail`, so a genuinely broken run
#            died silently at a command substitution and this function, seeing
#            no recorded failure, called it a pass (the audit's F9).
#   skipped  the run ended in skip() - missing Docker, aws or go. Nothing was
#            verified, which is not the same as everything having held.
emit_json() {
  local rc="${1:-0}" i n=${#STEP_NAMES[@]} sep="" overall="pass"
  [ "$HARNESS_FAILED" -eq 1 ] && overall="fail"
  for ((i = 0; i < n; i++)); do
    [ "${STEP_STATUSES[$i]}" = "fail" ] && overall="fail"
  done
  if [ "$overall" = "pass" ]; then
    if [ "$SKIPPED" -eq 1 ]; then
      overall="skipped"
    elif [ "$rc" -ne 0 ]; then
      overall="error"
    fi
  fi
  printf '{"steps":['
  for ((i = 0; i < n; i++)); do
    printf '%s{"name":"%s","status":"%s","phase":%s}' "$sep" "${STEP_NAMES[$i]}" "${STEP_STATUSES[$i]}" "${STEP_PHASES[$i]}"
    sep=","
  done
  printf '],"overall":"%s"}\n' "$overall"
}

# --expect <phase>: exit 0 iff every recorded step at or below the phase is
# pass AND every step above it is not_implemented. Called only after the
# script reaches its normal end (any fail() elsewhere already exits nonzero
# first, satisfying "any fail anywhere is exit nonzero regardless" without
# this function's help).
evaluate_expect() {
  local i n=${#STEP_NAMES[@]} nm st ph blockers=""
  for ((i = 0; i < n; i++)); do
    nm="${STEP_NAMES[$i]}"; st="${STEP_STATUSES[$i]}"; ph="${STEP_PHASES[$i]}"
    if [ "$ph" -le "$EXPECT_PHASE" ]; then
      [ "$st" = "pass" ] || blockers="$blockers $nm(phase=$ph,status=$st)"
    else
      [ "$st" = "not_implemented" ] || blockers="$blockers $nm(phase=$ph,status=$st)"
    fi
  done
  if [ -n "$blockers" ]; then
    echo "EXPECT $EXPECT_PHASE: FAILED -- blocked by:$blockers" >&2
    exit 1
  fi
  echo "EXPECT $EXPECT_PHASE: OK -- every step phase<=$EXPECT_PHASE is pass, every step phase>$EXPECT_PHASE is not_implemented"
}

# skip is for missing tooling, never for a claim that failed. Without
# --expect it is a clean exit: the harness is a progress bar, and no Docker
# on the box is not a false claim about stateless mode. With --expect the
# caller asked for a verdict on a phase and did not get one, so the exit code
# must not say the expectation was met - it exits 2 instead, distinct from
# both 0 (met) and 1 (checked and false). Before this, `--expect 5` on a box
# without Docker exited 0 and read as a full green run (the audit's F9).
skip() {
  echo "SKIP: $1"
  SKIPPED=1
  if [ -n "$EXPECT_PHASE" ]; then
    echo "EXPECT $EXPECT_PHASE: NOT VERIFIED -- the run was skipped before any step could run ($1)" >&2
    exit 2
  fi
  exit 0
}
fail() {
  local step="$1" msg="$2"
  echo "FAIL [$step]: $msg"
  HARNESS_FAILED=1
  [ -n "$(phase_of_step "$step")" ] && record_step "$step" fail
  exit 1
}
not_implemented() {
  local name="$1" roadmap_phase="$2" msg="$3"
  echo "  NOT IMPLEMENTED (phase $roadmap_phase) -- $msg"
  record_step "$name" not_implemented
}

# The EXIT trap is registered before preconditions so a --json/LIVE_E2E_
# JSON=1 run still emits its (empty-steps) JSON object on a SKIP exit, not
# only on PASS/FAIL. Guarded on $WORK so it is safe to fire before WORK is
# assigned below.
on_exit() {
  # Capture the real exit status BEFORE running anything else in this trap,
  # and restore it via an explicit `exit` at the end: an EXIT trap's own
  # final command otherwise silently becomes the process's exit status (the
  # classic trap gotcha) -- e.g. `[ "$JSON_MODE" -eq 1 ] && emit_json` would
  # itself exit nonzero, and clobber a real PASS, whenever --json is off.
  local rc=$?
  if [ -n "${WORK:-}" ]; then
    docker rm -f "${FLOCI_NAME:-}" >/dev/null 2>&1 || true
    rm -rf "$WORK"
  fi
  if [ "$JSON_MODE" -eq 1 ]; then
    emit_json "$rc"
  fi
  exit "$rc"
}
trap on_exit EXIT

# ── Preconditions ────────────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || skip "docker not installed"
docker info >/dev/null 2>&1 || skip "docker daemon not reachable"
command -v aws >/dev/null 2>&1 || skip "aws CLI not installed"
if [ -z "${TOFU_BIN:-}" ]; then
  command -v go >/dev/null 2>&1 || skip "go not installed (needed to build choudoufu from source; set TOFU_BIN to skip the build)"
fi
# git was required here while the observational snapshot's branch carrier
# existed (removed by issue #109); the only git left in this harness is the
# cosmetic branch-name line after the build, which already tolerates a
# missing git on its own, so there is nothing to gate on anymore.

WORK="$(mktemp -d)"
MAIN="$WORK/estate"

# ── Helpers ──────────────────────────────────────────────────────────────────
awsl() { aws --endpoint-url "$ENDPOINT" "$@"; }
tf() { "$TOFU" "$@"; }

# run_tf runs the built binary in directory $1 with the remaining arguments,
# and leaves its combined output in TF_OUT and its exit status in TF_RC.
#
# The set +e / set -e bracket is the entire reason this function exists.
# Every call site in this harness used to be written
#
#     OUT="$(cd "$dir" && tf live-plan ... 2>&1)"
#     RC=$?
#     [ "$RC" -eq 0 ] || fail "step" "live-plan exited $RC: $OUT"
#
# and under `set -euo pipefail` a nonzero exit from that command substitution
# aborts the script AT THE ASSIGNMENT. The RC line never runs, the guard
# never runs, fail() never runs: a genuinely broken live-plan killed the
# harness with no diagnostic naming the step, and (with --json) emit_json
# then reported "overall":"pass". Eighteen sites, every one of them dead
# (the audit's F9). Wrapping the call is what makes those guards live.
run_tf() {
  local dir="$1"
  shift
  set +e
  TF_OUT="$(cd "$dir" && "$TOFU" "$@" 2>&1)"
  TF_RC=$?
  set -e
}

# live_plan is the common case: a full-estate live-plan in $1, failing step $2
# with the command's own output when it exits nonzero. Leaves the output in
# TF_OUT for the caller to assert against.
live_plan() {
  local dir="$1" step="$2"
  shift 2
  run_tf "$dir" live-plan -input=false -no-color "$@"
  [ "$TF_RC" -eq 0 ] || fail "$step" "live-plan exited $TF_RC: $TF_OUT"
  [ -n "$TF_OUT" ] || fail "$step" "live-plan exited 0 and printed nothing at all"
}

# in_list <needle> <space-separated haystack>. The addresses this harness
# passes through it never contain a space; the ones that could
# (aws_subnet.this["a"]) are only ever passed one at a time, as an argument.
in_list() {
  local needle="$1" item
  for item in $2; do
    [ "$item" = "$needle" ] && return 0
  done
  return 1
}

# Comments out the first resource block whose opening line matches ERE
# pattern $1, across the .tf files in dir $2. Brace-depth tracked so nested
# blocks (e.g. `tags = {...}`) don't confuse the boundary. Fails step $3 if
# nothing matched.
comment_out_resource() {
  local pattern="$1" dir="$2" step="$3" f matched=0
  for f in "$dir"/*.tf; do
    grep -qE "$pattern" "$f" 2>/dev/null || continue
    matched=1
    awk -v pat="$pattern" '
      BEGIN { commenting = 0; depth = 0 }
      {
        if (!commenting && $0 ~ pat) { commenting = 1; depth = 0 }
        if (commenting) {
          print "#" $0
          o = gsub(/{/, "{")
          c = gsub(/}/, "}")
          depth += o - c
          if (depth <= 0) commenting = 0
        } else {
          print $0
        }
      }
    ' "$f" > "$f.tmp" && mv "$f.tmp" "$f"
  done
  [ "$matched" -eq 1 ] || fail "$step" "pattern '$pattern' matched no .tf file in $dir"
}

# Extracts one resource's diff block from live-plan output $1: from its
# "# <addr> will be ..." header line to the resource's own closing brace -
# four-space indent, exactly ("    }"), the same anchor
# assert_estate_plan uses for the same reason. Deliberately NOT
# "the next line that trims down to a lone closing brace": a nested map
# argument (tags = {...}) closes at a deeper indent and also trims to "}",
# so that naive match stops at the FIRST such line - the tags sub-object's
# own close - and silently drops every attribute printed after it (tags_all,
# value, version, ...). Empty (nothing printed) if the address has no diff at
# all.
plan_block() {
  local out="$1" addr="$2" start matches
  # Two steps, not "grep ... | head -n1 | cut -d: -f1": that pipes grep's
  # output straight into a live process (head) that closes its stdin after
  # the first line. If the header matches more than once (an adversarial or
  # just very large plan), head's early exit signals grep with SIGPIPE while
  # grep still has output queued, and the pipeline dies at exit 141 under
  # pipefail (#232). Command substitution below reads grep to EOF with no
  # consumer able to close it early, so the herestring-fed head after it has
  # no upstream process left to kill.
  matches="$(grep -n -F -- "# $addr will be" <<< "$out")"
  start="$(head -n1 <<< "$matches" | cut -d: -f1)"
  [ -n "$start" ] || return 0
  tail -n "+$start" <<< "$out" | awk '
    NR == 1 { print; next }
    stopped { next }
    { print; if ($0 ~ /^    }$/) stopped = 1 }
  '
}

# ── The standing residue, retired ───────────────────────────────────────────
#
# Through #26, a full-estate live-plan here was never literally empty on
# floci, and the whole reason was one emulator read gap plus one thing
# downstream of it, tolerated under the names RESIDUE_UNOWNED and
# RESIDUE_CHANGED below (see git history for the full account):
#
#   RESIDUE_UNOWNED="aws_iam_role.app" — upstream floci's iam:GetRole omitted
#   Tags (chant/test/floci-gaps.md #5), so the role read back carrying no
#   tofu-estate marker: unverifiable ownership, so it stayed out of the prior
#   state, reported as an [UNOWNED] omission with an adoption hint, planned
#   as a create. Fixed by lex00/floci#24 (iam:GetRole/GetUser/GetPolicy now
#   return Tags).
#
#   RESIDUE_CHANGED="aws_s3_bucket_policy.data" — a bucket policy carries no
#   tags of its own; it is admitted as a named singleton child of its
#   bucket, and its document interpolates aws_iam_role.app's ARN. With the
#   role unowned and planned as a create, that ARN was unknown until apply,
#   so the policy re-planned too. Downstream of the role gap, and it
#   disappeared with it — no separate fix needed.
#
# Both retired in the same change that moved FLOCI_IMAGE from upstream
# floci/floci:latest to lex00/floci's fork build carrying #24 (and #25, the
# unrelated ECR fix this harness does not directly exercise). A full-estate
# plan against this fixture is now expected to be genuinely empty — assert_
# estate_plan below tolerates no standing omission or in-place update at all
# any more, only a step's own declared expect_absent/extra_changes.
#
# Not fixed by this switch: floci's ssm:PutParameter still drops the inline
# tag set a parameter is created with (chant/test/floci-gaps.md #10,
# unrelated to #24/#25), which is why step 3d below still adopts both SSM
# receipts by hand before any plan assertion runs — that workaround stays.

# plan_addrs echoes, one per line, the addresses in live-plan output $1 whose
# diff header ends with $2 — the literal tail of the header line, e.g.
# 'will be created', 'will be destroyed', 'will be updated in-place',
# 'must be replaced'.
plan_addrs() {
  printf '%s\n' "$1" | sed -n "s/^  # \\(.*\\) $2\$/\\1/p"
}

# count_lines counts non-empty lines in $1. Its own `|| n=0` matters: under
# `set -e` a bare `grep -c` on empty input exits 1 and would abort the run.
count_lines() {
  local n
  n="$(printf '%s\n' "$1" | grep -c .)" || n=0
  printf '%s' "$n"
}

# omission_section is the "Not read from the live system" block, header to
# the horizontal rule that ends it. Extracted rather than grepped over the
# whole output, so a bracketed word in some other section cannot be misread
# as an omission.
omission_section() {
  printf '%s\n' "$1" | awk '
    stopped { next }
    /^Not read from the live system:/ { on = 1 }
    on && /^─────/ { stopped = 1; next }
    on { print }
  '
}

# omitted_instances echoes "<address> <REASON>" for every instance the plan
# says it could not read.
omitted_instances() {
  omission_section "$1" | sed -n 's/^ *\([^ ][^ ]*\) \[\([A-Z_]*\)\] *$/\1 \2/p'
}

# unowned_omissions echoes just the addresses reported [UNOWNED]. #26
# retired this fixture's standing unowned residue, so this is expected to
# echo nothing on every step now; still used by empty-plan-full (step 5) to
# compute how many declared instances materialized.
unowned_omissions() {
  omitted_instances "$1" | sed -n 's/ UNOWNED$//p'
}

# assert_estate_plan is the single definition of "this full-estate plan is
# what it should be". Every step that takes a full-estate plan goes through
# it, so there is one place to be right.
#
#   $1  live-plan output
#   $2  step name, for fail()
#   $3  addresses this step legitimately expects to be CHANGED, space
#       separated, may be empty. Their own diff blocks are the caller's
#       business; this function only accepts that they appear.
#   $4  addresses this step legitimately expects to be DESTROYED, space
#       separated, may be empty.
#   $5  a label for messages.
#   $6  addresses this step legitimately expects to be reported [ABSENT] —
#       genuinely gone from the live system, as opposed to unreadable-but-
#       present (RA.6: a receipt deleted out of band to re-arm its effect) —
#       space separated, may be empty. An ABSENT address is the only
#       omission any step may still declare; its create is accepted the
#       same way.
#
# What it checks:
#
#  1. Every omitted instance is [ABSENT] naming one of $6. Any other omission
#     — including [UNOWNED], now that #26 retired this fixture's standing
#     unowned residue — is a real gap in reading the live system, not a
#     tolerated one.
#  2. Every create header names an address the plan also reports as an
#     expected-absent omission (from $6). A create with no omission behind
#     it means something is being minted for a resource nobody explained —
#     the shape finding C1 named.
#  3. Every in-place update names one of $3. Nothing else may move.
#  4. Every destroy names one of $4, and a REPLACEMENT fails everywhere,
#     always. A replacement prints "must be replaced", which neither the
#     "will be created" nor the "will be destroyed" pattern matches — which
#     is how this harness printed PASS while a plan replaced an unrelated
#     resource (the audit's F8).
#  5. The "Plan: A to add, C to change, D to destroy." line agrees with the
#     headers counted above. The summary and the headers come from different
#     code paths in the renderer, so requiring them to agree catches
#     anything that slips past a header pattern entirely. This cross-check
#     is what F8 found missing everywhere except assert_full_estate_clean.
assert_estate_plan() {
  local out="$1" step="$2" extra_changes="$3" expect_destroys="$4" label="$5" expect_absent="${6:-}"
  local creates changes destroys replaces omitted
  local addr reason n_create n_change n_destroy

  [ -n "$out" ] || fail "$step" "$label: live-plan produced no output at all"
  grep -qE '^(Plan:|No changes\.)' <<< "$out" \
    || fail "$step" "$label: plan output carries neither a 'Plan:' line nor 'No changes.' — refusing to trust any assertion against it: $out"

  # 1. Omissions. #26 retired this fixture's standing unowned residue (the
  # image switch to lex00/floci fixed the iam:GetRole tags gap), so an
  # [ABSENT] naming one of $6 is the only omission any step may still see.
  omitted="$(omitted_instances "$out")"
  while IFS=' ' read -r addr reason; do
    [ -n "$addr" ] || continue
    if [ "$reason" = "ABSENT" ] && in_list "$addr" "$expect_absent"; then
      continue
    fi
    fail "$step" "$label: $addr could not be read from the live system ([$reason]); this fixture carries no standing unowned residue any more (retired by #26's image switch), so only this step's own expected-absent addresses ($expect_absent) are tolerated: $(omission_section "$out")"
  done <<< "$omitted"

  # 2. Creates.
  creates="$(plan_addrs "$out" 'will be created')"
  for addr in $creates; do
    if in_list "$addr" "$expect_absent"; then
      continue
    fi
    fail "$step" "$label: the plan proposes creating $addr, which it does not report as an expected-absent omission — nothing may be minted without the ownership check having said why: $out"
  done
  # Symmetric with the destroy check below: an address this step claims is
  # expected-absent must actually show up that way, both as the omission and
  # as the create it causes — an expectation nothing in the plan bears out is
  # the test asserting nothing at all.
  for addr in $expect_absent; do
    [ -n "$addr" ] || continue
    in_list "$addr" "$creates" \
      || fail "$step" "$label: this step expects $addr to be [ABSENT] and proposed as a create, and the plan proposes no such create: $out"
  done

  # 3. In-place updates. #26 retired this fixture's standing changed residue
  # (the bucket policy downstream of the unowned role), so only this step's
  # own declared $extra_changes may move.
  changes="$(plan_addrs "$out" 'will be updated in-place')"
  for addr in $changes; do
    in_list "$addr" "$extra_changes" \
      || fail "$step" "$label: the plan proposes changing $addr, which is not this step's own expected change ($extra_changes) — this fixture carries no standing residue any more (retired by #26's image switch): $(plan_block "$out" "$addr")"
  done

  # 4. Destroys, and replacements.
  replaces="$(plan_addrs "$out" 'must be replaced')"
  [ -z "$replaces" ] \
    || fail "$step" "$label: the plan proposes replacing $(echo "$replaces" | tr '\n' ' ')— a replacement is neither a create nor a destroy header, and is never part of a clean estate: $out"
  destroys="$(plan_addrs "$out" 'will be destroyed')"
  for addr in $destroys; do
    in_list "$addr" "$expect_destroys" \
      || fail "$step" "$label: the plan proposes destroying $addr; this step expects to destroy [$expect_destroys]: $out"
  done
  for addr in $expect_destroys; do
    in_list "$addr" "$destroys" \
      || fail "$step" "$label: this step expects $addr to be destroyed and the plan does not propose it: $out"
  done

  # 5. The summary line, cross-checked against the headers.
  n_create="$(count_lines "$creates")"
  n_change="$(count_lines "$changes")"
  n_destroy="$(count_lines "$destroys")"
  if grep -qE '^No changes\. Your infrastructure matches the configuration\.$' <<< "$out"; then
    { [ "$n_create" -eq 0 ] && [ "$n_change" -eq 0 ] && [ "$n_destroy" -eq 0 ]; } \
      || fail "$step" "$label: the plan says 'No changes.' and prints $n_create create / $n_change change / $n_destroy destroy header(s): $out"
  else
    grep -qE "^Plan: $n_create to add, $n_change to change, $n_destroy to destroy\\.\$" <<< "$out" \
      || fail "$step" "$label: the plan summary disagrees with its own diff headers (headers: $n_create to add, $n_change to change, $n_destroy to destroy) — $(grep -E '^Plan:' <<< "$out" || echo 'no Plan: line at all')"
  fi
}

# assert_full_estate_clean: no step-specific change, no create, no destroy
# at all. #26 retired the standing residue that used to make "clean" mean
# "empty modulo one tolerated create and one tolerated change", so a clean
# plan against this fixture is now a genuinely empty one.
assert_full_estate_clean() {
  local out="$1" step="$2"
  assert_estate_plan "$out" "$step" "" "" "full-estate plan"
  # assert_estate_plan above already required every create/change/destroy to
  # be one of this call's (empty) tolerances, so reaching a non-"No changes."
  # plan here would mean it accepted a diff it should not have — a mechanism
  # failure in assert_estate_plan itself, not a residue to describe.
  grep -qE '^No changes\. Your infrastructure matches the configuration\.$' <<< "$out" \
    || fail "$step" "full-estate plan: assert_estate_plan accepted a non-empty plan with no residue to explain it (mechanism failure): $out"
  echo "  fully empty plan"
}

# assert_drift_case is the shared body of every case in step 6's drift
# matrix: one attribute of one live resource has been mutated out of band,
# and a full-estate live-plan must show exactly that resource changed — its
# own diff limited to the mutated attribute (or the tags maps, for a tag
# mutation) plus any of that type's own documented unserved attributes — and
# nothing else. Never a create, never a destroy, never a replacement. Mirrors
# lifecycle/exactness_test.go's per-case assertions
# (internal/live/lifecycle), which prove the same claim directly against
# the package underneath this harness's built binary, on a smaller fixture
# that never carried the read gap #26 retired here.
#
# args: plan output, step, address, mutated attribute, nontags (0/1, whether
# the attribute is a top-level argument rather than a tag key), unserved
# (space-separated extra allowed attribute names, may be empty), case label
# (for the one-line PASS log).
assert_drift_case() {
  local out="$1" step="$2" addr="$3" attr="$4" nontags="$5" unserved="$6" label="$7"
  local header block allowed u bad

  # The whole-plan shape first — including the Plan:/header cross-check,
  # which is what makes a stray replacement or an unexplained create fatal
  # here rather than invisible.
  assert_estate_plan "$out" "$step" "$addr" "" "the $label drift"

  header="  # ${addr} will be updated in-place"
  grep -qF "$header" <<< "$out" \
    || fail "$step" "the $label drift is not visible in the plan (no '$header' line): $out"

  block="$(plan_block "$out" "$addr")"
  [ -n "$block" ] || fail "$step" "could not extract the diff block for $addr (mechanism failure)"
  grep -q "$attr" <<< "$block" || fail "$step" "the $label diff does not mention $attr: $block"

  allowed='tags|tags_all'
  [ "$nontags" = "1" ] && allowed="$allowed|$attr"
  for u in $unserved; do allowed="$allowed|$u"; done
  bad="$(grep -E '^ {6}[~+-] [A-Za-z0-9_]+' <<< "$block" \
    | sed -E "s/^ {6}[~+-] ([A-Za-z0-9_]+).*/\\1/" | grep -vE "^($allowed)\$")" || true
  [ -z "$bad" ] || fail "$step" "the $label diff touches attribute(s) beyond $allowed: $bad"

  echo "  $label: $attr PASS"
}

# ── 0. choudoufu binary (from THIS repo, unless TOFU_BIN overrides) ────────
echo "=== 0. choudoufu binary ==="
if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "build" "TOFU_BIN=$TOFU_BIN is not an executable file"
  echo "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) || fail "build" "go build ./cmd/choudoufu failed from $ROOT"
  echo "  built $TOFU from $ROOT (branch $(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '?'))"
fi

# ── 0b. probe for stateless-mode subcommands ────────────────────────────────
# GATING: steps 5-9 used to gate on bare HAVE_LIVE_PLAN,
# so landing live-plan before its later dependencies (P2.4's foreign
# classification, P3.3's live-mv, P5.1's exactness work) made those steps
# attempt real runs against unimplemented machinery instead of reporting
# NOT IMPLEMENTED. Every later step's gate below is a probe of the specific
# surface it needs, never the bare presence of live-plan alone.
echo "=== 0b. probing for stateless-mode subcommands ==="
HAVE_LIVE_PLAN=0
"$TOFU" live-plan -help >/dev/null 2>&1 && HAVE_LIVE_PLAN=1
HAVE_LIVE_MV=0
"$TOFU" live-mv -help >/dev/null 2>&1 && HAVE_LIVE_MV=1
# -estate lands with P2.4 (foreign classification + protection): its presence
# in live-plan's help text is the probe for "the full-estate path
# (discovery, binding, foreign classification) exists", as opposed to
# HAVE_LIVE_PLAN alone, which only means the P1.4 command surface exists.
LIVE_PLAN_HELP="$("$TOFU" live-plan -help 2>&1 || true)"
HAVE_LIVE_ESTATE=0
grep -q -- '-estate' <<< "$LIVE_PLAN_HELP" && HAVE_LIVE_ESTATE=1
LIVE_E2E_EXACTNESS="${LIVE_E2E_EXACTNESS:-1}"

# P4.3's probe: the "live" block that puts plain plan/apply into
# stateless mode (P4.1) is a config-decoder feature, not a subcommand, so
# none of the probes above tell us whether this build supports it. The
# clean, cheap check: decode a minimal config carrying nothing but the block
# (no provider, no resources -- there is nothing here for `choudoufu validate` to
# need `choudoufu init` for, so this needs no Docker and runs before Floci even
# starts) with `choudoufu validate`. Stock choudoufu treats "live" as an
# unrecognized block inside "terraform" and fails with "Unsupported block
# type"; a build carrying P4.1 decodes it and validates clean instead.
BLOCK_PROBE_DIR="$(mktemp -d)"
cat > "$BLOCK_PROBE_DIR/main.tf" <<'PROBEEOF'
terraform {
  live {
    estate = "probe"
  }
}
PROBEEOF
BLOCK_PROBE_OUT="$(cd "$BLOCK_PROBE_DIR" && "$TOFU" validate -no-color 2>&1)" || true
rm -rf "$BLOCK_PROBE_DIR"
HAVE_LIVE_BLOCK=1
grep -q "Unsupported block type" <<< "$BLOCK_PROBE_OUT" && HAVE_LIVE_BLOCK=0

echo "  live-plan:        $([ "$HAVE_LIVE_PLAN" -eq 1 ] && echo present || echo absent)"
echo "  live-plan -estate: $([ "$HAVE_LIVE_ESTATE" -eq 1 ] && echo present || echo absent)"
echo "  live-mv:          $([ "$HAVE_LIVE_MV" -eq 1 ] && echo present || echo absent)"
echo "  live block (plain plan/apply, P4.1): $([ "$HAVE_LIVE_BLOCK" -eq 1 ] && echo present || echo absent)"
echo "  LIVE_E2E_EXACTNESS: $LIVE_E2E_EXACTNESS"

# ── 1. Floci ─────────────────────────────────────────────────────────────────
# lex00/floci, not upstream floci/floci: the fork carries #24 (iam:GetRole/
# GetUser/GetPolicy return Tags — what retired this fixture's standing
# unowned residue below) and #25 (ecr:CreateRepository no longer needs a
# Docker daemon), neither merged upstream yet. Pinned by digest rather than
# `:latest` so a later push to the fork's main cannot silently change what
# this harness runs against; FLOCI_IMAGE overrides it (see the header).
echo "=== 1. Floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "floci" "docker run for $FLOCI_NAME failed"
# Captured before grep, not "curl | grep -q": grep -q exits (and closes its
# stdin) the instant it finds a match, same early-exit-consumer shape as the
# head/awk sites in #232, so a large enough health response could SIGPIPE
# curl. The response here is a small fixed JSON object well under one pipe
# buffer in practice, but capturing it first costs nothing and removes the
# live pipe entirely.
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)"
  grep -q '"ec2"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)"
grep -q '"ec2"' <<< "$HEALTH" \
  || fail "floci" "floci did not come up healthy (ec2) at $ENDPOINT"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# ── 2. standup — stock init+apply, plain local state ────────────────────────
echo "=== 2. standup — init + apply with plain local state ==="
[ -d "$ESTATE_SRC" ] \
  || fail "standup" "estate fixture missing at $ESTATE_SRC (task P0.1 not merged into this worktree yet)"
mkdir -p "$MAIN"
cp -R "$ESTATE_SRC/." "$MAIN/"
( cd "$MAIN" && "$TOFU" init -input=false >/dev/null && "$TOFU" apply -input=false -auto-approve >/dev/null ) \
  || fail "standup" "choudoufu init/apply against floci did not succeed"
[ -f "$MAIN/terraform.tfstate" ] \
  || fail "standup" "apply produced no terraform.tfstate — standup is supposed to be stock plain-local-state apply"
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
  --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] \
  || fail "standup" "no VPC tagged tofu-estate=stateless-e2e found via the AWS CLI after apply"
# DECLARED_INSTANCES is read off the fixture itself, not hardcoded (#48): the
# plain-state apply above is the one point in this run where a state file
# lists every instance the estate fixture declares, count/for_each expansion
# included, before step 3 deletes it. Every later step's "N of $DECLARED_INSTANCES
# materialized" arithmetic uses this instead of a literal that has to be
# hand-updated whenever the estate fixture grows.
DECLARED_INSTANCES="$(cd "$MAIN" && "$TOFU" state list | wc -l | tr -d ' ')"
[ "$DECLARED_INSTANCES" -gt 0 ] 2>/dev/null \
  || fail "standup" "could not read the estate's declared instance count off its own state ($MAIN)"
echo "  applied; VPC $VPC_ID carries tofu-estate=stateless-e2e; $DECLARED_INSTANCES declared instances"
record_step "standup" pass

# ── 3. adopt — delete the state; nothing else changes ───────────────────────
echo "=== 3. adopt — delete terraform.tfstate(.backup); the non-event is the demo ==="
BEFORE="$(cd "$MAIN" && find . -type f ! -name 'terraform.tfstate*' | sort)"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
AFTER="$(cd "$MAIN" && find . -type f ! -name 'terraform.tfstate*' | sort)"
[ "$BEFORE" = "$AFTER" ] || fail "adopt" "files besides the state changed during adopt"
[ ! -f "$MAIN/terraform.tfstate" ] || fail "adopt" "terraform.tfstate is still present after adopt"
echo "  adoption is the deletion of terraform.tfstate(.backup) and nothing else — the estate itself never moved"
record_step "adopt" pass

# ── 3b. choudoufu init — the registry-host note ──────────────────────────────────
# P1.4's live run: terraform's apply populates registry.terraform.io provider
# paths, choudoufu resolves registry.opentofu.org. Every command below is already
# run through $TOFU (never terraform), but the work copy earns its own init
# right after adopt so this step never depends on what standup happened to
# use to get here.
echo "=== 3b. choudoufu init — the work copy resolves providers via choudoufu, not terraform ==="
( cd "$MAIN" && "$TOFU" init -input=false >/dev/null ) \
  || fail "tofu-init" "choudoufu init in the work copy did not succeed after adopt"
echo "  choudoufu init re-run against the state-less work copy"
record_step "tofu-init" pass

# ── 3c. slot migration — aws_eip.pool takes its stable tofu-slot markers ────
# P3.1's note: the estate's EIPs were applied by stock terraform, before
# slots existed, so they carry tofu-address tags and no tofu-slot. Left
# alone, empty-plan-full (step 5) would see a real migration plan (0 add / 4
# change / 0 destroy) instead of the empty plan it asserts, and count-
# scale-down would see the compat path report an orphan rather than a
# destroy. This sub-step is the one-time migration an apply would perform:
# read the slot the compatibility path proposes for each member (bind by
# address, slot i for index i — no create, no destroy), and write it onto
# the live allocation via the AWS CLI, since this fork has no apply yet.
echo "=== 3c. slot migration — aws_eip.pool takes its stable tofu-slot markers ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ]; then
  not_implemented "slot-migration" 2 "count slot markers (P3.1) need full-estate discovery+binding (-estate probe, P2.1-P2.4) to see the EIP pool at all; wired green in P3.5"
else
  run_tf "$MAIN" live-plan -input=false -no-color
  OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "slot-migration" "live-plan exited $RC: $OUT"

  # One unfiltered read of every live address, tags included: floci's
  # ec2:DescribeAddresses ignores tag filters (floci-gaps #8, confirmed
  # 2026-08-11 — a --filters call here would silently return the whole
  # account), so estate and address are matched client-side below instead.
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  LIVE_EIPS="$(awsl ec2 describe-addresses \
    --query 'Addresses[].[AllocationId,Tags[?Key==`tofu-estate`]|[0].Value,Tags[?Key==`tofu-address`]|[0].Value]' \
    --output text 2>/dev/null)" || true

  for i in 0 1 2; do
    ADDR="aws_eip.pool[$i]"
    BLOCK="$(plan_block "$OUT" "$ADDR")"
    [ -n "$BLOCK" ] || fail "slot-migration" "no diff for $ADDR in the pre-migration plan: $OUT"
    grep -q "will be created" <<< "$BLOCK" \
      && fail "slot-migration" "$ADDR is proposed as a create -- the pre-slot compatibility path should bind it by address, not mint it: $BLOCK"
    grep -q "will be destroyed" <<< "$BLOCK" \
      && fail "slot-migration" "$ADDR is proposed as a destroy: $BLOCK"

    # Same two-step shape as plan_block above and for the same reason
    # (#232): the first grep can match more than once inside a resource
    # block, so piping it straight into "head -n1 | grep ..." risks SIGPIPE
    # on that first grep once head closes early. Capture its full output via
    # command substitution first, then feed head/grep from a herestring.
    SLOT_MATCHES="$(grep -oE '"tofu-slot"[[:space:]]*=[[:space:]]*"[0-9]+"' <<< "$BLOCK")"
    SLOT="$(head -n1 <<< "$SLOT_MATCHES" | grep -oE '[0-9]+')"
    [ -n "$SLOT" ] || fail "slot-migration" "$ADDR's diff proposes no tofu-slot tag: $BLOCK"

    ALLOC_ID="$(awk -v est="stateless-e2e" -v want="aws_eip.pool:$i" '$2==est && $3==want {print $1; exit}' <<< "$LIVE_EIPS")"
    [ -n "$ALLOC_ID" ] \
      || fail "slot-migration" "no live EIP tagged tofu-estate=stateless-e2e tofu-address=aws_eip.pool:$i"

    awsl ec2 create-tags --resources "$ALLOC_ID" --tags "Key=tofu-slot,Value=$SLOT" >/dev/null \
      || fail "slot-migration" "could not write tofu-slot=$SLOT onto $ALLOC_ID ($ADDR)"
    echo "  $ADDR -> $ALLOC_ID takes tofu-slot=$SLOT"
  done

  run_tf "$MAIN" live-plan -input=false -no-color
  REOUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "slot-migration" "re-plan after migration exited $RC: $REOUT"
  grep -qE '^  # aws_eip\.pool\[[0-9]+\] will be' <<< "$REOUT" \
    && fail "slot-migration" "re-plan still proposes a change to the EIP pool after migration: $REOUT"
  echo "  re-plan proposes no further change to the EIP pool"
  record_step "slot-migration" pass
fi

# ── 3d. receipt adoption — both receipts take their markers by hand ─────────
# The estate fixture declares tofu-estate/tofu-address on both SSM-parameter
# receipts (aws_ssm_parameter.demo_effect, the hash flavor, and
# aws_ssm_parameter.demo_existence, the existence flavor — RA.6), but floci's
# ssm:PutParameter silently drops the inline tag set a parameter is created
# with (chant/test/floci-gaps.md #10), so both read back with no marker and
# are therefore, correctly, not this estate's: every plan would report each
# [UNOWNED] and propose creating it.
#
# That is adoption, and adoption here is exactly what the docs say it is — a
# tag you write yourself, with your own cloud tools, on a resource you already
# own. floci's ssm:AddTagsToResource works and its ListTagsForResource returns
# what was written, so two AWS CLI calls per receipt close the gap for the
# whole run and every later step gets to assert against genuinely owned
# receipts rather than tolerate a standing diff. Step 12's receipt cycle in
# particular needs this: "breaking the receipt re-arms the effect" is a claim
# about an in-place value change on an owned resource, and an unowned receipt
# would render the same break as part of a create.
#
# The IAM role's identical-looking gap (#5) used to be closeable only by
# writing tags that iam:GetRole then still would not read back — the
# standing residue #26 retired instead, by switching FLOCI_IMAGE to a fork
# build carrying lex00/floci#24. #10 (this step's SSM gap) is unrelated and
# still open, which is why the by-hand adoption below stays.
RECEIPT_PARAM="/tofu-receipts/stateless-e2e/demo-effect"
EXISTENCE_PARAM="/tofu-receipts/stateless-e2e/demo-existence"

# adopt_ssm_receipt writes and verifies the ownership markers on one SSM
# parameter receipt: $1 its parameter name, $2 its resource address. Shared
# by both receipts so the two flavors go through identically-shaped adoption.
adopt_ssm_receipt() {
  local param="$1" addr="$2"

  # The gap first: assert the inline tags really are missing before writing
  # them. If floci ever fixes #10 this prints so and writes nothing, rather
  # than quietly performing a no-op nobody notices.
  local tags_before
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  tags_before="$(awsl ssm list-tags-for-resource --resource-type Parameter \
    --resource-id "$param" --query 'TagList[?Key==`tofu-estate`]|[0].Value' \
    --output text 2>/dev/null || echo None)"
  if [ "$tags_before" = "stateless-e2e" ]; then
    echo "  $addr already carries tofu-estate (floci-gaps #10 appears fixed); nothing to adopt"
  else
    awsl ssm add-tags-to-resource --resource-type Parameter --resource-id "$param" \
      --tags "Key=tofu-estate,Value=stateless-e2e" "Key=tofu-address,Value=$addr" >/dev/null \
      || fail "receipt-adoption" "could not write the ownership markers onto $param"
    echo "  wrote tofu-estate/tofu-address onto $param (floci-gaps #10: PutParameter dropped the inline set)"
  fi

  # Read it back through the AWS CLI, never through choudoufu: the claim is that
  # the ownership record is on the resource.
  local estate_tag addr_tag
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  estate_tag="$(awsl ssm list-tags-for-resource --resource-type Parameter \
    --resource-id "$param" --query 'TagList[?Key==`tofu-estate`]|[0].Value' --output text 2>/dev/null || echo None)"
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  addr_tag="$(awsl ssm list-tags-for-resource --resource-type Parameter \
    --resource-id "$param" --query 'TagList[?Key==`tofu-address`]|[0].Value' --output text 2>/dev/null || echo None)"
  [ "$estate_tag" = "stateless-e2e" ] \
    || fail "receipt-adoption" "the live tofu-estate tag on $param reads '$estate_tag', want stateless-e2e"
  [ "$addr_tag" = "$addr" ] \
    || fail "receipt-adoption" "the live tofu-address tag on $param reads '$addr_tag', want $addr"
}

echo "=== 3d. receipt adoption — both SSM-parameter receipts take their markers ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ]; then
  not_implemented "receipt-adoption" 2 "adopting the receipts only matters once full-estate discovery reports it (-estate probe, P2.1-P2.4); the receipts themselves are PE.3 (hash flavor) and RA.6 (existence flavor)"
else
  adopt_ssm_receipt "$RECEIPT_PARAM" "aws_ssm_parameter.demo_effect"
  adopt_ssm_receipt "$EXISTENCE_PARAM" "aws_ssm_parameter.demo_existence"

  # And the adoption took: the next plan no longer reports either receipt as
  # unowned, and no longer proposes creating either. That is the whole point
  # of the step, and it is asserted rather than assumed.
  live_plan "$MAIN" "receipt-adoption"
  ADOPT_OUT="$TF_OUT"
  for ADOPT_ADDR in aws_ssm_parameter.demo_effect aws_ssm_parameter.demo_existence; do
    grep -qF "  $ADOPT_ADDR [UNOWNED]" <<< "$ADOPT_OUT" \
      && fail "receipt-adoption" "$ADOPT_ADDR is still reported unowned after its markers were written: $(omission_section "$ADOPT_OUT")"
    grep -qF "# $ADOPT_ADDR will be created" <<< "$ADOPT_OUT" \
      && fail "receipt-adoption" "$ADOPT_ADDR is still planned as a create after adoption: $ADOPT_OUT"
  done

  echo "  both receipts adopted: markers verified live via the AWS CLI, and the next plan binds them"
  record_step "receipt-adoption" pass
fi

# readopt_receipt re-writes the receipt's markers after an out-of-band value
# write. Step 12 needs it, and the reason is a second facet of the same
# emulator gap: floci's ssm:PutParameter with --overwrite DROPS the existing
# tag set (confirmed 2026-08-11 — list-tags-for-resource returns nothing
# afterwards). Real AWS preserves tags across an overwrite, and in fact
# rejects Tags and Overwrite together precisely because the tags are not
# yours to restate on that call. So an Op writing a receipt on real AWS never
# touches its ownership, and the harness restoring the markers here is
# faithful to what the step is demonstrating, not a fudge of it. Logged as a
# floci gap candidate alongside #10.
readopt_receipt() {
  local step="$1"
  awsl ssm add-tags-to-resource --resource-type Parameter --resource-id "$RECEIPT_PARAM" \
    --tags "Key=tofu-estate,Value=stateless-e2e" "Key=tofu-address,Value=aws_ssm_parameter.demo_effect" >/dev/null \
    || fail "$step" "could not restore the receipt's ownership markers after an out-of-band value write"
}

# ── 4. empty-plan-named ──────────────────────────────────────────────────────
echo "=== 4. empty-plan-named ==="
if [ "$HAVE_LIVE_PLAN" -eq 0 ]; then
  not_implemented "empty-plan-named" 1 "choudoufu live-plan does not exist yet (P1.4); wired green in P1.5"
else
  # CONCRETE-only target scope. Historically (P1.3, pre-P2.3) this scope was
  # load-bearing: no PARENT_DERIVED instance could materialize at all, so
  # untargeted resources stayed omitted. P2.3/P2.4 close that gap — discovery
  # runs over the whole config regardless of -target (see the omissions
  # check below and empty-plan-full) — so today -target only narrows what
  # the plan body itself covers; this step still targets the CONCRETE set to
  # keep its plan-diff assertions scoped to the client-named/attachment
  # subset distinct from empty-plan-full's whole-estate plan. The resource is
  # targeted, not the index: -target=aws_cloudwatch_log_group.optional covers
  # its one instance without naming count.index.
  CONCRETE_TARGETS=(
    aws_s3_bucket.data
    aws_s3_bucket_policy.data
    aws_s3_bucket_versioning.data
    aws_s3_bucket_public_access_block.data
    aws_s3_bucket_server_side_encryption_configuration.data
    aws_s3_bucket_lifecycle_configuration.data
    aws_iam_role.app
    aws_iam_role_policy.app
    aws_iam_role_policy_attachment.app
    aws_kms_alias.main
    aws_cloudwatch_log_group.app
    aws_cloudwatch_log_group.optional
    aws_cloudwatch_metric_alarm.cpu
    aws_dynamodb_table.events
    aws_ecs_cluster.app
  )
  TARGET_ARGS=()
  for addr in "${CONCRETE_TARGETS[@]}"; do
    TARGET_ARGS+=(-target="$addr")
  done

  # -json is rejected by live-plan v0 (omissions have no JSON shape
  # yet); -no-color keeps the human output free of escape codes so the
  # assertions below can grep it directly.
  run_tf "$MAIN" live-plan -input=false -no-color "${TARGET_ARGS[@]}"
  OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "empty-plan-named" "live-plan exited $RC: $OUT"

  # Omissions are NOT filtered by -target: the section always
  # covers the whole estate. P1.5's note predicted this transition exactly:
  # "Route and associations correctly propose create until phase 2." Now
  # that P2.3/P2.4 are merged, identity.Resolve's needs-discovery list for
  # the whole config is closed by discovery+binding on every run, regardless
  # of -target — so, with #26's image switch retiring the standing unowned
  # role residue, every instance materializes.
  #
  # assert_estate_plan owns the whole shape: no omission, no create, no
  # change, nothing destroyed or replaced, and the Plan: line agrees with
  # the headers. Deliberately the SAME assertion as the full-estate steps
  # use, rather than a hand-rolled second copy of it — this step's own
  # subject is the -target scope, not a different idea of clean.
  assert_estate_plan "$OUT" "empty-plan-named" "" "" "the CONCRETE targeted set"

  [ ! -f "$MAIN/terraform.tfstate" ] \
    || fail "empty-plan-named" "terraform.tfstate exists after live-plan — it must never be read or written"

  echo "  fully empty plan over the CONCRETE targeted set"
  record_step "empty-plan-named" pass
fi

# ── 5. empty-plan-full ───────────────────────────────────────────────────────
echo "=== 5. empty-plan-full ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ]; then
  not_implemented "empty-plan-full" 2 "full-estate plan needs marker stamping + discovery + foreign classification (P2.1-P2.4, probed via -estate in live-plan -help); wired green in P2.5"
else
  # Full-estate plan: no -target, and no -estate flag either — the estate
  # name derives from the fixture's own tofu-estate tags (P2.4's contract for
  # P2.5's harness wiring). -no-color keeps the human output free of escape
  # codes so the assertions below can grep it directly.
  STEP5_T0=$(date +%s)
  run_tf "$MAIN" live-plan -input=false -no-color
  OUT="$TF_OUT"
  RC="$TF_RC"
  STEP5_T1=$(date +%s)
  [ "$RC" -eq 0 ] || fail "empty-plan-full" "live-plan exited $RC: $OUT"

  # Every one of DECLARED_INSTANCES (read off the fixture's own state at
  # standup, #48 — not a hardcoded literal) materializes: the client-named
  # ones as always, and every server-ID/parent-derived one via P2.3's
  # discovery + P2.4's binding, which close the whole config regardless of
  # scope. #26's image switch retired the standing residue that used to
  # leave the role omitted here, so assert_full_estate_clean now requires a
  # genuinely empty plan: no omission, no create, no change, nothing
  # destroyed or replaced.
  #
  # The foreign section (floci's unmarked default-VPC resources) is expected
  # to appear here; its presence is not itself a failure, and its line shapes
  # never collide with the plan-diff patterns the helper checks.
  MATERIALIZED=$((DECLARED_INSTANCES - $(count_lines "$(unowned_omissions "$OUT")")))
  assert_full_estate_clean "$OUT" "empty-plan-full"

  [ ! -f "$MAIN/terraform.tfstate" ] \
    || fail "empty-plan-full" "terraform.tfstate exists after live-plan — it must never be read or written"

  echo "  empty plan over the full estate ($MATERIALIZED/$DECLARED_INSTANCES materialized); $((STEP5_T1 - STEP5_T0))s"
  record_step "empty-plan-full" pass
fi

# ── 6. drift-exact ───────────────────────────────────────────────────────────
# P5.1's drift matrix (internal/live/lifecycle/
# lifecycle/exactness_test.go), mirrored here against the full P0.1
# estate instead of that test's own smaller fixture: one out-of-band mutation
# per estate type (VPC, subnet, security group, EIP, bucket, log group tag;
# log group retention as the non-tag case), each asserted as touching exactly
# that resource and exactly that attribute, corrected, and reconfirmed clean.
echo "=== 6. drift-exact — one mutation per estate type, each exactly one attribute ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ] || [ "$HAVE_LIVE_MV" -eq 0 ] || [ "$LIVE_E2E_EXACTNESS" != "1" ]; then
  not_implemented "drift-exact" 5 "drift exactness is P5.1, gated on -estate + live-mv existing and LIVE_E2E_EXACTNESS=1 (P5.2 flips the default to 1)"
else
  STEP6_T0=$(date +%s)

  D_VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ -n "$D_VPC_ID" ] && [ "$D_VPC_ID" != "None" ] || fail "drift-exact" "could not find the estate's VPC"
  D_SUBNET_ID="$(awsl ec2 describe-subnets \
    --filters "Name=tag:tofu-estate,Values=stateless-e2e" "Name=tag:tofu-address,Values=aws_subnet.this:a" \
    --query 'Subnets[0].SubnetId' --output text 2>/dev/null || echo None)"
  [ -n "$D_SUBNET_ID" ] && [ "$D_SUBNET_ID" != "None" ] \
    || fail "drift-exact" "could not find aws_subnet.this[\"a\"] via its tofu-address tag"
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literal, not shell interpolation
  D_SG_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'SecurityGroups[?GroupName!=`default`]|[0].GroupId' --output text 2>/dev/null || echo None)"
  [ -n "$D_SG_ID" ] && [ "$D_SG_ID" != "None" ] \
    || fail "drift-exact" "could not find the estate's security group via its tofu-estate tag"
  # Unfiltered read, matched client-side: floci's ec2:DescribeAddresses
  # ignores --filters (floci-gaps #8, the same reason the slot-migration and
  # count-scale-down steps read every address and match by hand).
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  D_LIVE_EIPS="$(awsl ec2 describe-addresses \
    --query 'Addresses[].[AllocationId,Tags[?Key==`tofu-estate`]|[0].Value,Tags[?Key==`tofu-address`]|[0].Value]' \
    --output text 2>/dev/null)" || true
  D_EIP_ID="$(awk -v est="stateless-e2e" -v want="aws_eip.pool:0" '$2==est && $3==want {print $1; exit}' <<< "$D_LIVE_EIPS")"
  [ -n "$D_EIP_ID" ] || fail "drift-exact" "could not find aws_eip.pool[0] via its tofu-address tag"

  DRIFT_NAMES=(vpc subnet sg eip bucket log-group-tag log-group-retention)
  DRIFT_ADDRS=(
    "aws_vpc.main"
    'aws_subnet.this["a"]'
    "aws_security_group.main"
    "aws_eip.pool[0]"
    "aws_s3_bucket.data"
    "aws_cloudwatch_log_group.app"
    "aws_cloudwatch_log_group.app"
  )
  DRIFT_ATTRS=(Owner Drifted Drifted Drifted Drifted Drifted retention_in_days)
  DRIFT_NONTAGS=(0 0 0 0 0 0 1)
  # revoke_rules_on_delete: a tofu-side behaviour flag EC2 does not store, so
  # no read can return it - the SDK fills its default in whenever the
  # security group changes at all. Same shape as a stock "terraform import"
  # of a security group followed by the same tag drift; see
  # lifecycle/exactness_test.go and live/LIMITATIONS.md.
  DRIFT_UNSERVED=("" "" revoke_rules_on_delete "" "" "" "")

  for i in 0 1 2 3 4 5 6; do
    D_NAME="${DRIFT_NAMES[$i]}"
    D_ADDR="${DRIFT_ADDRS[$i]}"
    D_ATTR="${DRIFT_ATTRS[$i]}"
    D_NONTAGS="${DRIFT_NONTAGS[$i]}"
    D_UNSERVED="${DRIFT_UNSERVED[$i]}"

    case "$D_NAME" in
      vpc)
        awsl ec2 create-tags --resources "$D_VPC_ID" --tags "Key=$D_ATTR,Value=someone-else" >/dev/null \
          || fail "drift-exact" "could not tag the VPC out of band" ;;
      subnet)
        awsl ec2 create-tags --resources "$D_SUBNET_ID" --tags "Key=$D_ATTR,Value=yes" >/dev/null \
          || fail "drift-exact" "could not tag the subnet out of band" ;;
      sg)
        awsl ec2 create-tags --resources "$D_SG_ID" --tags "Key=$D_ATTR,Value=yes" >/dev/null \
          || fail "drift-exact" "could not tag the security group out of band" ;;
      eip)
        awsl ec2 create-tags --resources "$D_EIP_ID" --tags "Key=$D_ATTR,Value=yes" >/dev/null \
          || fail "drift-exact" "could not tag the EIP out of band" ;;
      bucket)
        awsl s3api put-bucket-tagging --bucket tofu-stateless-e2e-data --tagging \
          '{"TagSet":[{"Key":"tofu-estate","Value":"stateless-e2e"},{"Key":"tofu-address","Value":"aws_s3_bucket.data"},{"Key":"Drifted","Value":"yes"}]}' >/dev/null \
          || fail "drift-exact" "could not tag the bucket out of band" ;;
      log-group-tag)
        awsl logs tag-log-group --log-group-name /stateless-e2e/app --tags Drifted=yes >/dev/null \
          || fail "drift-exact" "could not tag the log group out of band" ;;
      log-group-retention)
        awsl logs put-retention-policy --log-group-name /stateless-e2e/app --retention-in-days 7 >/dev/null \
          || fail "drift-exact" "could not set the log group retention out of band" ;;
    esac

    run_tf "$MAIN" live-plan -input=false -no-color
    D_OUT="$TF_OUT"
    D_RC="$TF_RC"
    [ "$D_RC" -eq 0 ] || fail "drift-exact" "live-plan failed after the $D_NAME drift: $D_OUT"

    assert_drift_case "$D_OUT" "drift-exact" "$D_ADDR" "$D_ATTR" "$D_NONTAGS" "$D_UNSERVED" "$D_NAME"

    # Correct it: no live-apply command exists, and $MAIN carries no
    # "live" block (standup/adopt's whole point is a plain-state
    # estate), so the only real correction path is the same AWS CLI
    # convention foreign-protected's cleanup and count-scale-down's
    # convergence already use - reverse the exact write the mutation made.
    case "$D_NAME" in
      vpc)     awsl ec2 delete-tags --resources "$D_VPC_ID" --tags "Key=$D_ATTR" >/dev/null \
                 || fail "drift-exact" "could not revert the VPC tag" ;;
      subnet)  awsl ec2 delete-tags --resources "$D_SUBNET_ID" --tags "Key=$D_ATTR" >/dev/null \
                 || fail "drift-exact" "could not revert the subnet tag" ;;
      sg)      awsl ec2 delete-tags --resources "$D_SG_ID" --tags "Key=$D_ATTR" >/dev/null \
                 || fail "drift-exact" "could not revert the security group tag" ;;
      eip)     awsl ec2 delete-tags --resources "$D_EIP_ID" --tags "Key=$D_ATTR" >/dev/null \
                 || fail "drift-exact" "could not revert the EIP tag" ;;
      bucket)  awsl s3api put-bucket-tagging --bucket tofu-stateless-e2e-data --tagging \
                 '{"TagSet":[{"Key":"tofu-estate","Value":"stateless-e2e"},{"Key":"tofu-address","Value":"aws_s3_bucket.data"}]}' >/dev/null \
                 || fail "drift-exact" "could not revert the bucket tag" ;;
      log-group-tag)       awsl logs untag-log-group --log-group-name /stateless-e2e/app --tags "$D_ATTR" >/dev/null \
                 || fail "drift-exact" "could not revert the log group tag" ;;
      log-group-retention) awsl logs put-retention-policy --log-group-name /stateless-e2e/app --retention-in-days 1 >/dev/null \
                 || fail "drift-exact" "could not revert the log group retention" ;;
    esac

    run_tf "$MAIN" live-plan -input=false -no-color
    D_BACK="$TF_OUT"
    D_RC="$TF_RC"
    [ "$D_RC" -eq 0 ] || fail "drift-exact" "re-plan after correcting the $D_NAME drift failed: $D_BACK"
    assert_full_estate_clean "$D_BACK" "drift-exact"
  done

  # The live identities must be the ones the matrix started with: a drift
  # that was corrected by replacing a resource would satisfy every check
  # above and still be a bug.
  D_VPC_ID_AFTER="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literal, not shell interpolation
  D_SG_ID_AFTER="$(awsl ec2 describe-security-groups --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'SecurityGroups[?GroupName!=`default`]|[0].GroupId' --output text 2>/dev/null || echo None)"
  [ "$D_VPC_ID_AFTER" = "$D_VPC_ID" ] && [ "$D_SG_ID_AFTER" = "$D_SG_ID" ] \
    || fail "drift-exact" "the drift matrix replaced a resource: VPC $D_VPC_ID -> $D_VPC_ID_AFTER, SG $D_SG_ID -> $D_SG_ID_AFTER"

  STEP6_T1=$(date +%s)
  echo "  drift matrix: 7 cases, each exactly one resource / one attribute, corrected, reconverged; live IDs unchanged; $((STEP6_T1 - STEP6_T0))s"
  record_step "drift-exact" pass
fi

# ── 7. foreign-protected ─────────────────────────────────────────────────────
echo "=== 7. foreign-protected — an unmanaged SG is reported, never proposed for delete ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ]; then
  not_implemented "foreign-protected" 2 "foreign classification is P2.3/P2.4 (tag-filtered discovery + protection, probed via -estate in live-plan -help); wired green in P2.5"
else
  VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "foreign-protected" "could not find the estate's VPC"
  FOREIGN_SG="$(awsl ec2 create-security-group --group-name "stateless-e2e-foreign-$$" \
    --description "unmanaged, no tofu-estate marker" --vpc-id "$VPC_ID" --query 'GroupId' --output text)"
  [ -n "$FOREIGN_SG" ] || fail "foreign-protected" "could not create the unmarked security group"

  STEP7_T0=$(date +%s)
  OUT="$(cd "$MAIN" && tf live-plan -input=false -no-color 2>&1)" || fail "foreign-protected" "live-plan failed: $OUT"
  STEP7_T1=$(date +%s)

  # Sanity check 1: the assertion mechanism only means something if $OUT is
  # non-empty and carries recognizable plan structure. Without this, a
  # blank or garbled $OUT would make every negative grep below pass
  # vacuously -- the empty-pipe-is-a-pass shape the audit flagged.
  [ -n "$OUT" ] || fail "foreign-protected" "live-plan produced no output at all"
  grep -qE '^(Plan:|No changes\.)' <<< "$OUT" \
    || fail "foreign-protected" "plan output has none of the expected structural markers (Plan:/No changes.) -- refusing to trust any grep against it: $OUT"

  # Sanity check 2: the foreign SG id must actually appear somewhere in the
  # output before any assertion (positive or negative) built on it is
  # trustworthy -- otherwise a plan that never even considered the resource
  # would look identical to one that correctly protected it.
  grep -q "$FOREIGN_SG" <<< "$OUT" \
    || fail "foreign-protected" "the foreign security group id $FOREIGN_SG does not appear anywhere in plan output -- the plan never saw it: $OUT"

  # It must be reported, specifically, as foreign.
  grep -qi "foreign" <<< "$OUT" || fail "foreign-protected" "the unmarked security group was not reported as foreign"

  # It must be reported specifically inside the Foreign section's item list
  # (views/live_plan.go's "  <type> <live-id>( (<display>))?" shape),
  # not merely somewhere in the output -- e.g. not only in the "Not swept"
  # or "Adoptable" sub-sections, whose item lines start differently.
  FOREIGN_HEADER="$(grep -E '^Foreign resources: [0-9]+ live resources? not owned by estate ' <<< "$OUT")" || true
  [ -n "$FOREIGN_HEADER" ] \
    || fail "foreign-protected" "no 'Foreign resources: N live resource(s) not owned by estate ...' header in plan output: $OUT"
  echo "  $FOREIGN_HEADER"
  grep -qE "^  aws_security_group ${FOREIGN_SG}( \(|\$)" <<< "$OUT" \
    || fail "foreign-protected" "the foreign security group id $FOREIGN_SG does not appear as a Foreign-section item line: $OUT"

  # Zero destroys anywhere in the plan, not merely a check that this one
  # resource's id is absent from destroy lines -- the full-estate plan run
  # here must never propose deleting anything.
  #
  # This used to count lines starting with "-", which stopped being a proxy
  # for "a destroy" the moment the plan legitimately carried an in-place
  # change to a jsonencode() argument: the bucket policy's old document
  # renders as eleven removal lines INSIDE an update. The structural check
  # is the right one and it is stronger anyway -- no destroy header, no
  # replacement header, and a Plan: line that agrees with both.
  assert_estate_plan "$OUT" "foreign-protected" "" "" "the plan with an unowned neighbour present"

  # Sanity check 3 (the audit's specific finding): before, this was
  # `grep -E '^[[:space:]]*-' | grep -q "$FOREIGN_SG"`. If the first grep
  # matched zero lines -- which is exactly what colorized output does to a
  # line-anchored pattern, since the ANSI escape for a destroy line lands
  # between the leading whitespace and the "-" -- the pipe fed `grep -q` an
  # empty string, which reports no-match, and the guarded `fail` after `&&`
  # could never fire. The check "passed" whether or not the plan proposed
  # deleting the foreign SG. -no-color above removes the escape codes, but
  # the mechanism itself must also prove it can match before its silence on
  # real output is trusted: assert it finds the marker on a synthetic
  # destroy line first.
  SYNTHETIC_DESTROY_LINE="  - resource \"aws_security_group\" \"synthetic-$FOREIGN_SG\" {"
  grep -qE '^[[:space:]]*-' <<< "$SYNTHETIC_DESTROY_LINE" \
    || fail "foreign-protected" "destroy-line grep pattern '^[[:space:]]*-' does not even match a synthetic destroy line -- the assertion mechanism itself is broken, independent of what live-plan printed"

  DESTROY_LINES="$(grep -E '^[[:space:]]*-' <<< "$OUT")" || true
  grep -q "$FOREIGN_SG" <<< "$DESTROY_LINES" \
    && fail "foreign-protected" "the foreign security group was proposed for deletion: $DESTROY_LINES"

  # Cleanup: remove the out-of-band SG so a rerun of the harness (a fresh
  # Floci container reuses nothing from this one today, but real AWS or a
  # reused container would) stays deterministic instead of accumulating
  # foreign resources across runs.
  awsl ec2 delete-security-group --group-id "$FOREIGN_SG" >/dev/null \
    || fail "foreign-protected" "could not delete the out-of-band security group $FOREIGN_SG during cleanup"

  echo "  foreign security group reported, never proposed for deletion; cleaned up; $((STEP7_T1 - STEP7_T0))s"
  record_step "foreign-protected" pass
fi

# ── 8. removal-exact ─────────────────────────────────────────────────────────
# P5.1's removal case (lifecycle/exactness_test.go, part 4), mirrored
# rather than re-derived: one whole block is deleted and exactly its live
# resource goes. The test's own fixture deletes its security group; here the
# block is aws_ebs_volume.data instead, because the estate's security group
# grew per-rule dependents in #20's third slice
# (aws_vpc_security_group_ingress_rule/_egress_rule reference
# aws_security_group.main.id, and EC2 deletes a group's rules with the
# group), and a removal step's whole point is ONE destroy. The volume is the
# same shape the SG was chosen for - taggable, marker path, no known-gap
# interference of its own, and nothing else references it. With #26's image
# switch retiring the estate's standing role residue (and the SSM-parameter
# receipts already adopted by hand in step 3d), the full-estate plan here is
# the bare single-resource result: "1 to destroy" and nothing else.
echo "=== 8. removal-exact — deleting one whole block destroys exactly its live resource ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ] || [ "$HAVE_LIVE_MV" -eq 0 ] || [ "$LIVE_E2E_EXACTNESS" != "1" ]; then
  not_implemented "removal-exact" 5 "removal exactness is P5.1, gated on -estate + live-mv existing and LIVE_E2E_EXACTNESS=1 (P5.2 flips the default to 1)"
else
  STEP8_T0=$(date +%s)
  COPY="$(mktemp -d)"
  cp -R "$MAIN/." "$COPY/"

  R_VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ -n "$R_VPC_ID" ] && [ "$R_VPC_ID" != "None" ] || fail "removal-exact" "could not find the estate's VPC"
  R_VOL_ID="$(awsl ec2 describe-volumes --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Volumes[0].VolumeId' --output text 2>/dev/null || echo None)"
  [ -n "$R_VOL_ID" ] && [ "$R_VOL_ID" != "None" ] || fail "removal-exact" "could not find the estate's EBS volume"

  comment_out_resource "resource[[:space:]]+\"aws_ebs_volume\"" "$COPY" "removal-exact"

  run_tf "$COPY" live-plan -input=false -no-color
  OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "removal-exact" "live-plan failed after removing the EBS volume's block: $OUT"

  # 1. The whole plan shape in one assertion: exactly one destroy and it is
  # the deleted block, no create, no change, nothing is replaced, and the
  # Plan: line agrees with all of that. The destroy is passed in as this
  # step's own expectation, so a SECOND destroy - the thing a removal step
  # exists to rule out - fails on the address rather than on a count.
  assert_estate_plan "$OUT" "removal-exact" "" "aws_ebs_volume.data" "the removal plan"

  # 2. "Owned and undeclared" names the removal and the live ID - the
  # legitimacy claim, not merely the destroy header.
  grep -q "Owned and undeclared: 1 live resource will be destroyed" <<< "$OUT" \
    || fail "removal-exact" "the plan does not say why destroying aws_ebs_volume.data is legitimate: $OUT"
  grep -q "$R_VOL_ID" <<< "$OUT" \
    || fail "removal-exact" "the plan does not name the live resource $R_VOL_ID anywhere: $OUT"

  # 4. Apply it. No live-apply command exists - $MAIN carries no
  # "live" block, standup/adopt's whole point is a plain-state estate -
  # so the real teardown path is the same AWS CLI convention foreign-
  # protected's cleanup and count-scale-down's convergence already use. Then
  # confirm the deletion the way this harness always confirms an AWS-side
  # claim: read it back with the AWS CLI, never through choudoufu.
  awsl ec2 delete-volume --volume-id "$R_VOL_ID" >/dev/null \
    || fail "removal-exact" "could not delete the EBS volume $R_VOL_ID"
  # Real AWS errors describe-volumes for an unknown volume id; floci may
  # instead answer 200 with an empty Volumes list (the same shape its
  # describe-security-groups fallback has), so "gone" is read from the query
  # result, not from the exit code -- the same fallback
  # exSecurityGroupExists uses in lifecycle/exactness_test.go.
  R_VOL_AFTER="$(awsl ec2 describe-volumes --volume-ids "$R_VOL_ID" \
    --query 'Volumes[0].VolumeId' --output text 2>/dev/null || echo None)"
  [ -z "$R_VOL_AFTER" ] || [ "$R_VOL_AFTER" = "None" ] \
    || fail "removal-exact" "the EBS volume $R_VOL_ID is still live after the removal apply (describe returned $R_VOL_AFTER)"

  # 5. The rest of the estate is untouched.
  R_VPC_ID_AFTER="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ "$R_VPC_ID_AFTER" = "$R_VPC_ID" ] \
    || fail "removal-exact" "the removal disturbed the rest of the estate: VPC $R_VPC_ID -> $R_VPC_ID_AFTER"

  # 6. Re-plan: genuinely clean, nothing left to propose destroying.
  run_tf "$COPY" live-plan -input=false -no-color
  CONVERGED="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "removal-exact" "live-plan failed after the removal apply: $CONVERGED"
  assert_full_estate_clean "$CONVERGED" "removal-exact"

  # 7. No state file, ever.
  [ ! -f "$COPY/terraform.tfstate" ] \
    || fail "removal-exact" "terraform.tfstate exists in the work copy after the removal"

  rm -rf "$COPY"
  echo "  aws_ebs_volume.data removed: exactly one destroy, Owned-and-undeclared names $R_VOL_ID, rest of the estate untouched, re-plan clean"

  # 8. Restore: $MAIN's own config still declares aws_ebs_volume.data -
  # only $COPY's config had the block commented out - so a live volume has
  # to exist again for every later step's full-estate plan to stay clean.
  # Same convention count-scale-down (step 9) already uses to put back what
  # it released.
  # The markers ride on the create call itself: floci's ec2:CreateTags
  # silently drops tags on volume resources (probed 2026-08-12 — the call
  # succeeds and describe-volumes returns Tags: []), while tag
  # specifications at create time round-trip fine, which is also the path
  # the provider itself takes.
  R_NEW_VOL_ID="$(awsl ec2 create-volume --availability-zone "us-east-1a" --size 1 \
    --tag-specifications 'ResourceType=volume,Tags=[{Key=tofu-estate,Value=stateless-e2e},{Key=tofu-address,Value=aws_ebs_volume.data}]' \
    --query 'VolumeId' --output text)"
  [ -n "$R_NEW_VOL_ID" ] && [ "$R_NEW_VOL_ID" != "None" ] \
    || fail "removal-exact" "could not recreate the EBS volume to restore \$MAIN's live estate"
  # create-volume returns while the volume is still "creating", and the
  # provider's volume read waits for "available" (the probe's create took
  # ~10s against floci for exactly this reason), so the restore plan below
  # would miss a still-creating volume. Poll it available first.
  R_VOL_STATE=""
  for _ in $(seq 1 30); do
    R_VOL_STATE="$(awsl ec2 describe-volumes --volume-ids "$R_NEW_VOL_ID" \
      --query 'Volumes[0].State' --output text 2>/dev/null || echo unknown)"
    [ "$R_VOL_STATE" = "available" ] && break
    sleep 2
  done
  [ "$R_VOL_STATE" = "available" ] \
    || fail "removal-exact" "the replacement EBS volume $R_NEW_VOL_ID never became available (last state: $R_VOL_STATE)"
  run_tf "$MAIN" live-plan -input=false -no-color
  RESTORE_OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "removal-exact" "live-plan failed after restoring \$MAIN's live estate: $RESTORE_OUT"
  assert_full_estate_clean "$RESTORE_OUT" "removal-exact"

  STEP8_T1=$(date +%s)
  echo "  restored via $R_NEW_VOL_ID so \$MAIN's live estate still matches its declared config; $((STEP8_T1 - STEP8_T0))s"
  record_step "removal-exact" pass
fi

# ── 9. count-scale-down ──────────────────────────────────────────────────────
echo "=== 9. count-scale-down — 3 -> 2 EIPs is one delete, no churn ==="
if [ "$HAVE_LIVE_MV" -eq 0 ]; then
  not_implemented "count-scale-down" 3 "count slot markers + set matcher are P3.1, harness step wired green together with rename-no-churn in P3.5 (gated on live-mv existing)"
else
  STEP9_T0=$(date +%s)
  COPY="$(mktemp -d)"
  cp -R "$MAIN/." "$COPY/"

  # The live slot -> allocation mapping, read before anything is planned:
  # the destroy this step expects is checked against the cloud's own answer
  # for "what holds slot 2 today", not inferred from the plan proposing to
  # delete it. Slot 0 and 1's IDs are needed too, for the no-removal-line
  # check on the survivors below. One unfiltered read (floci-gaps #8, see
  # the slot-migration sub-step), matched client-side.
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  LIVE_SLOTS="$(awsl ec2 describe-addresses \
    --query 'Addresses[].[AllocationId,Tags[?Key==`tofu-estate`]|[0].Value,Tags[?Key==`tofu-slot`]|[0].Value]' \
    --output text 2>/dev/null)" || true
  SLOT0_ID="$(awk -v est="stateless-e2e" '$2==est && $3=="0" {print $1; exit}' <<< "$LIVE_SLOTS")"
  SLOT1_ID="$(awk -v est="stateless-e2e" '$2==est && $3=="1" {print $1; exit}' <<< "$LIVE_SLOTS")"
  SLOT2_ID="$(awk -v est="stateless-e2e" '$2==est && $3=="2" {print $1; exit}' <<< "$LIVE_SLOTS")"
  [ -n "$SLOT0_ID" ] && [ -n "$SLOT1_ID" ] && [ -n "$SLOT2_ID" ] \
    || fail "count-scale-down" "could not find all three live slot-tagged EIPs (slots 0, 1, 2) for estate stateless-e2e -- the slot-migration sub-step must have run first"

  FOUND_EIP=0
  for f in "$COPY"/*.tf; do
    grep -q 'resource "aws_eip"' "$f" 2>/dev/null || continue
    FOUND_EIP=1
    # Line-anchored, not \b: BSD sed (macOS, this task's target platform)
    # has no \b word-boundary support -- the GNU-only \b this line used to
    # use matched nothing, so the count edit silently no-op'd. This step
    # never actually ran that codepath before (it was NOT IMPLEMENTED until
    # this task), which is how the bug stayed dormant.
    sed -i.bak -E 's/^([[:space:]]*count[[:space:]]*=[[:space:]]*)3[[:space:]]*$/\12/' "$f"
    rm -f "$f.bak"
  done
  [ "$FOUND_EIP" -eq 1 ] || fail "count-scale-down" "no aws_eip resource found in the estate"

  run_tf "$COPY" live-plan -input=false -no-color
  OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "count-scale-down" "live-plan failed after scale-down: $OUT"

  # 1. The whole plan shape, with the surplus slot named as the only destroy
  # this step expects: a second destroy, an unexplained create, a
  # replacement, or a Plan: line that disagrees with the headers all fail
  # here. Surplus is always the highest slot, never chosen by count.index
  # position, and naming the address is how that is asserted.
  assert_estate_plan "$OUT" "count-scale-down" "" 'aws_eip.pool[2]' "the scale-down plan"

  # 2. The destroyed block names the allocation whose LIVE tofu-slot is 2,
  # read from the cloud above -- not merely whichever id the plan chose to
  # print.
  DESTROY_BLOCK="$(plan_block "$OUT" "aws_eip.pool[2]")"
  grep -q "$SLOT2_ID" <<< "$DESTROY_BLOCK" \
    || fail "count-scale-down" "the destroy of aws_eip.pool[2] does not name $SLOT2_ID, the allocation the cloud says holds slot 2: $DESTROY_BLOCK"

  # 3. Zero churn: no header of any kind (create/destroy/update) for
  # pool[0] or pool[1].
  grep -qE '^  # aws_eip\.pool\[[01]\] will be' <<< "$OUT" \
    && fail "count-scale-down" "pool[0] or pool[1] has a diff header -- scale-down must not touch a survivor: $OUT"

  # 4. The survivors' allocation IDs appear on no removal line anywhere in
  # the plan.
  REMOVAL_LINES="$(grep -E '^[[:space:]]*-' <<< "$OUT")" || true
  for SID in "$SLOT0_ID" "$SLOT1_ID"; do
    grep -q "$SID" <<< "$REMOVAL_LINES" \
      && fail "count-scale-down" "survivor $SID appears on a removal line: $REMOVAL_LINES"
  done

  # 5. A scale-down never creates an EIP. assert_estate_plan above already
  # required every create to be one this step declared (none), so this is
  # the check specific to this step: no member of the pool is minted.
  grep -qE '^  # aws_eip\.pool(\[[0-9]+\])? will be created$' <<< "$OUT" \
    && fail "count-scale-down" "scale-down proposes creating an EIP: $OUT"

  echo "  3 -> 2 EIPs: exactly one delete ($SLOT2_ID, slot 2), zero churn on the survivors ($SLOT0_ID slot 0, $SLOT1_ID slot 1)"

  # 6. Convergence: release the destroyed allocation, as an apply would, and
  # re-plan. The estate should settle genuinely clean.
  awsl ec2 release-address --allocation-id "$SLOT2_ID" >/dev/null \
    || fail "count-scale-down" "could not release $SLOT2_ID to converge the scale-down"
  run_tf "$COPY" live-plan -input=false -no-color
  CONVERGE_OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "count-scale-down" "live-plan failed after convergence: $CONVERGE_OUT"
  assert_full_estate_clean "$CONVERGE_OUT" "count-scale-down"

  # Restore: the release above is a real mutation on the one floci account
  # every step in this harness shares, but $MAIN's own config still
  # declares count = 3 and every step after this one plans against $MAIN's
  # live estate expecting the full three-member set. Leaving the account
  # down a member here would break those steps for the same reason an
  # unmigrated estate breaks empty-plan-full (step 3c's fix) -- $MAIN's live
  # state has to keep matching $MAIN's own config. Allocate a replacement
  # and hand it the slot the CLI released, the same convention foreign-
  # protected (step 7) already uses to clean up the resource it created.
  NEW_ALLOC_ID="$(awsl ec2 allocate-address --domain vpc --query 'AllocationId' --output text)"
  [ -n "$NEW_ALLOC_ID" ] && [ "$NEW_ALLOC_ID" != "None" ] \
    || fail "count-scale-down" "could not allocate a replacement EIP to restore \$MAIN's live estate to its declared count of 3"
  awsl ec2 create-tags --resources "$NEW_ALLOC_ID" --tags \
    "Key=tofu-estate,Value=stateless-e2e" "Key=tofu-address,Value=aws_eip.pool:2" "Key=tofu-slot,Value=2" >/dev/null \
    || fail "count-scale-down" "could not tag the replacement EIP $NEW_ALLOC_ID"
  run_tf "$MAIN" live-plan -input=false -no-color
  RESTORE_OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "count-scale-down" "live-plan failed after restoring \$MAIN's live estate: $RESTORE_OUT"
  assert_full_estate_clean "$RESTORE_OUT" "count-scale-down"

  rm -rf "$COPY"
  STEP9_T1=$(date +%s)
  echo "  converged after releasing $SLOT2_ID; restored via $NEW_ALLOC_ID so \$MAIN's live estate still matches its declared count=3; $((STEP9_T1 - STEP9_T0))s"
  record_step "count-scale-down" pass
fi

# ── 10. rename-no-churn ──────────────────────────────────────────────────────
# P3.3's recipe: aws_cloudwatch_log_group.app, the client-named path, not the
# bucket (floci-gaps #9 — S3 Control TagResource replaces the tag set
# instead of merging, so a tags-only rewrite drops every other tag). The IAM
# role was excluded here too before #26's image switch, for the same reason
# it carried the standing residue — iam:GetRole omitted Tags, so its marker
# was unreadable through the path a rename would use to confirm it; #24
# fixed the read, but this step was not re-scoped to use the role instead,
# so the log group stays the fixture.
echo "=== 10. rename-no-churn — rename an address, live-mv, empty plan ==="
if [ "$HAVE_LIVE_MV" -eq 0 ]; then
  not_implemented "rename-no-churn" 3 "needs live-plan (P1.4) and live-mv marker rewrite (P3.3); harness step wired green together with count-scale-down in P3.5 (gated on live-mv existing)"
else
  STEP10_T0=$(date +%s)
  COPY="$(mktemp -d)"
  cp -R "$MAIN/." "$COPY/"

  OLD_ADDR="aws_cloudwatch_log_group.app"
  NEW_ADDR="aws_cloudwatch_log_group.renamed"
  LOG_GROUP_NAME="/stateless-e2e/app"

  # Both the resource label and every hand-written tofu-address value naming
  # the old address have to move, or stamping reports a marker conflict and
  # live-mv exits 1 before the rename is even considered.
  RENAMED=0
  for f in "$COPY"/*.tf; do
    if grep -q 'resource "aws_cloudwatch_log_group" "app"' "$f" 2>/dev/null; then
      sed -i.bak 's/resource "aws_cloudwatch_log_group" "app"/resource "aws_cloudwatch_log_group" "renamed"/' "$f"
      rm -f "$f.bak"
      RENAMED=1
    fi
    if grep -q 'aws_cloudwatch_log_group\.app' "$f" 2>/dev/null; then
      sed -i.bak 's/aws_cloudwatch_log_group\.app/aws_cloudwatch_log_group.renamed/g' "$f"
      rm -f "$f.bak"
    fi
  done
  [ "$RENAMED" -eq 1 ] || fail "rename-no-churn" "no aws_cloudwatch_log_group.app resource block found in $COPY to rename"

  run_tf "$COPY" live-mv -no-color "$OLD_ADDR" "$NEW_ADDR"
  MV_OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "rename-no-churn" "live-mv $OLD_ADDR -> $NEW_ADDR exited $RC: $MV_OUT"
  grep -q "This was a cloud write." <<< "$MV_OUT" \
    || fail "rename-no-churn" "live-mv did not report a cloud write: $MV_OUT"

  # The check that does not trust this fork's own reads: the live tag, read
  # back straight through the AWS CLI.
  LIVE_ADDR="$(awsl logs list-tags-log-group --log-group-name "$LOG_GROUP_NAME" --query 'tags."tofu-address"' --output text 2>/dev/null)"
  [ "$LIVE_ADDR" = "$NEW_ADDR" ] \
    || fail "rename-no-churn" "the live tofu-address tag on $LOG_GROUP_NAME (via aws logs list-tags-log-group) reads '$LIVE_ADDR', want $NEW_ADDR"

  run_tf "$COPY" live-plan -input=false -no-color
  PLAN_OUT="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "rename-no-churn" "live-plan failed after the rename: $PLAN_OUT"
  assert_full_estate_clean "$PLAN_OUT" "rename-no-churn"

  [ ! -f "$COPY/terraform.tfstate" ] \
    || fail "rename-no-churn" "terraform.tfstate exists in the work copy after the rename -- live-mv must never read or write one"

  rm -rf "$COPY"

  # Restore: $MAIN's own config still declares aws_cloudwatch_log_group.app
  # (only $COPY's config was ever renamed), so a later full-estate plan
  # against $MAIN needs the live marker back at the old address -- the same
  # convention count-scale-down (step 9) and removal-exact (step 8) already
  # use to put back what they changed. A direct tag write, not a second
  # live-mv: the config that would need to declare .renamed for a
  # reverse rename to run against no longer exists now that $COPY is gone,
  # and log-group tagging merges rather than replaces (unlike S3's, floci-
  # gaps #9), so this one write is enough.
  awsl logs tag-log-group --log-group-name "$LOG_GROUP_NAME" --tags "tofu-address=$OLD_ADDR" >/dev/null \
    || fail "rename-no-churn" "could not restore the live tofu-address tag to $OLD_ADDR"
  RESTORE_ADDR="$(awsl logs list-tags-log-group --log-group-name "$LOG_GROUP_NAME" --query 'tags."tofu-address"' --output text 2>/dev/null)"
  [ "$RESTORE_ADDR" = "$OLD_ADDR" ] \
    || fail "rename-no-churn" "the live tofu-address tag did not restore to $OLD_ADDR (reads $RESTORE_ADDR)"
  run_tf "$MAIN" live-plan -input=false -no-color
  RESTORE_PLAN="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "rename-no-churn" "live-plan failed after restoring \$MAIN's live estate: $RESTORE_PLAN"
  assert_full_estate_clean "$RESTORE_PLAN" "rename-no-churn"

  STEP10_T1=$(date +%s)
  echo "  renamed $OLD_ADDR -> $NEW_ADDR via live-mv (live tag verified via aws logs list-tags-log-group); plan clean; restored so \$MAIN's live estate still matches its declared config; $((STEP10_T1 - STEP10_T0))s"
  record_step "rename-no-churn" pass
fi

# ── 11. plain-plan-works ─────────────────────────────────────────────────────
# Plain "choudoufu plan"/"choudoufu apply" -- no live-prefixed
# subcommand anywhere below -- against an estate whose config carries a
# terraform { live { estate = "..." } } block (P4.1). The estate-block
# fixture (live/e2e/estate-block/, its own README explains why it is a
# separate directory from $ESTATE_SRC) is used instead of $MAIN because
# adding the block to the main estate would make its own standup (step 2)
# stateless and stop it from producing the terraform.tfstate that step 2/3
# demonstrate adopting.
echo "=== 11. plain-plan-works — plain choudoufu plan/apply against a live-block estate ==="
if [ "$HAVE_LIVE_BLOCK" -eq 0 ]; then
  not_implemented "plain-plan-works" 4 "the config-level \"live\" block (P4.1) is not decoded by this build (probed via 'choudoufu validate' on a minimal live-block-only config, section 0b); wired green in P4.3"
else
  [ -d "$BLOCK_SRC" ] || fail "plain-plan-works" "estate-block fixture missing at $BLOCK_SRC"

  STEP11_T0=$(date +%s)
  WORK11="$(mktemp -d)"
  cp -R "$BLOCK_SRC/." "$WORK11/"

  # Baseline: the main estate's own resources, read before this step touches
  # anything, so "the estate-block apply did not touch the main estate" is a
  # before/after comparison, not an assumption. Its own estate name
  # (stateless-e2e) is distinct from estate-block's (stateless-e2e-block),
  # so neither apply/plan below should ever see the other's resources.
  MAIN_VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ -n "$MAIN_VPC_ID" ] && [ "$MAIN_VPC_ID" != "None" ] \
    || fail "plain-plan-works" "could not find the main estate's VPC before step 11"
  MAIN_EIP_COUNT_BEFORE="$(awsl ec2 describe-addresses \
    --query "length(Addresses[?Tags[?Key=='tofu-estate' && Value=='stateless-e2e']])" --output text)"

  # 1. choudoufu init, then a plain "choudoufu apply -auto-approve" -- no -estate flag
  #    (it does not exist on plain apply; P4.1's contract), no state file.
  set +e
  INIT_OUT="$(cd "$WORK11" && "$TOFU" init -input=false -no-color 2>&1)"
  INIT_RC=$?
  set -e
  [ "$INIT_RC" -eq 0 ] || fail "plain-plan-works" "choudoufu init failed in the estate-block work copy: $INIT_OUT"

  set +e
  APPLY_OUT="$(cd "$WORK11" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)"
  APPLY_RC=$?
  set -e
  [ "$APPLY_RC" -eq 0 ] || fail "plain-plan-works" "plain choudoufu apply failed: $APPLY_OUT"
  grep -qE '^Apply complete! Resources: 7 added, 0 changed, 0 destroyed\.$' <<< "$APPLY_OUT" \
    || fail "plain-plan-works" "expected 'Apply complete! Resources: 7 added, 0 changed, 0 destroyed.' (vpc, subnet, sg, bucket, log group, 2 eips): $APPLY_OUT"
  [ ! -f "$WORK11/terraform.tfstate" ] \
    || fail "plain-plan-works" "terraform.tfstate exists after a plain choudoufu apply -- a live-block estate must never write one"

  # The main estate is untouched: same VPC id, same live EIP count.
  MAIN_VPC_ID_AFTER="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ "$MAIN_VPC_ID_AFTER" = "$MAIN_VPC_ID" ] \
    || fail "plain-plan-works" "the main estate's VPC id changed after the estate-block apply: $MAIN_VPC_ID -> $MAIN_VPC_ID_AFTER"
  MAIN_EIP_COUNT_AFTER="$(awsl ec2 describe-addresses \
    --query "length(Addresses[?Tags[?Key=='tofu-estate' && Value=='stateless-e2e']])" --output text)"
  [ "$MAIN_EIP_COUNT_AFTER" = "$MAIN_EIP_COUNT_BEFORE" ] \
    || fail "plain-plan-works" "the main estate's EIP count changed after the estate-block apply: $MAIN_EIP_COUNT_BEFORE -> $MAIN_EIP_COUNT_AFTER"
  echo "  plain apply: 7 added; main estate untouched (VPC $MAIN_VPC_ID, EIP count $MAIN_EIP_COUNT_AFTER unchanged); no state file"

  # 2. Live markers, read via the AWS CLI, never via choudoufu: the claim is that
  # the ownership record is on the resource, so asking choudoufu to confirm its
  # own story would prove nothing. At least the VPC and one EIP slot.
  BLOCK_VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ -n "$BLOCK_VPC_ID" ] && [ "$BLOCK_VPC_ID" != "None" ] \
    || fail "plain-plan-works" "the estate-block VPC was not created"
  VPC_ESTATE_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$BLOCK_VPC_ID" "Name=key,Values=tofu-estate" \
    --query 'Tags[0].Value' --output text)"
  VPC_ADDR_TAG="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$BLOCK_VPC_ID" "Name=key,Values=tofu-address" \
    --query 'Tags[0].Value' --output text)"
  [ "$VPC_ESTATE_TAG" = "stateless-e2e-block" ] \
    || fail "plain-plan-works" "$BLOCK_VPC_ID carries tofu-estate=$VPC_ESTATE_TAG live, want stateless-e2e-block"
  [ "$VPC_ADDR_TAG" = "aws_vpc.main" ] \
    || fail "plain-plan-works" "$BLOCK_VPC_ID carries tofu-address=$VPC_ADDR_TAG live, want aws_vpc.main"

  # Unfiltered read, matched client-side (floci-gaps #8: ec2:DescribeAddresses
  # ignores --filters, the same reason step 3c/9 read every address).
  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  BLOCK_EIPS="$(awsl ec2 describe-addresses \
    --query 'Addresses[].[AllocationId,Tags[?Key==`tofu-estate`]|[0].Value,Tags[?Key==`tofu-slot`]|[0].Value]' \
    --output text 2>/dev/null)" || true
  BLOCK_EIP_SLOT0="$(awk -v est="stateless-e2e-block" '$2==est && $3=="0" {print $1; exit}' <<< "$BLOCK_EIPS")"
  [ -n "$BLOCK_EIP_SLOT0" ] || fail "plain-plan-works" "no live EIP for estate-block carrying tofu-slot=0: $BLOCK_EIPS"
  echo "  live markers confirmed via aws CLI: VPC $BLOCK_VPC_ID (tofu-address=aws_vpc.main), EIP slot 0 -> $BLOCK_EIP_SLOT0"

  # 3. Plain "choudoufu plan": a genuinely empty plan. This fixture (separate
  # from $ESTATE_SRC) has excluded the IAM role since before #26's image
  # switch, back when floci-gaps #5 made its tags unreadable and left it and
  # its downstream bucket policy as the standing residue elsewhere in this
  # harness (README.md, "Subset chosen"); it was not re-scoped to include
  # the role once #24 fixed the read.
  set +e
  PLAN_OUT="$(cd "$WORK11" && "$TOFU" plan -input=false -no-color 2>&1)"
  PLAN_RC=$?
  set -e
  [ "$PLAN_RC" -eq 0 ] || fail "plain-plan-works" "plain choudoufu plan failed: $PLAN_OUT"
  grep -qE '^No changes\. Your infrastructure matches the configuration\.$' <<< "$PLAN_OUT" \
    || fail "plain-plan-works" "expected a genuinely empty plan: $PLAN_OUT"
  [ ! -f "$WORK11/terraform.tfstate" ] \
    || fail "plain-plan-works" "terraform.tfstate exists after a plain choudoufu plan"
  echo "  plain choudoufu plan: genuinely empty, no known-gap tolerance needed"

  # 4. -detailed-exitcode: 2 after an out-of-band tag mutation, 0 after the
  # corrective apply puts it back.
  awsl ec2 create-tags --resources "$BLOCK_VPC_ID" --tags Key=Owner,Value=someone-else >/dev/null \
    || fail "plain-plan-works" "could not mutate the estate-block VPC out of band"

  set +e
  DRIFT_OUT="$(cd "$WORK11" && "$TOFU" plan -input=false -no-color -detailed-exitcode 2>&1)"
  DRIFT_RC=$?
  set -e
  [ "$DRIFT_RC" -eq 2 ] \
    || fail "plain-plan-works" "-detailed-exitcode after the out-of-band tag write: want 2, got $DRIFT_RC: $DRIFT_OUT"
  grep -q "Owner" <<< "$DRIFT_OUT" \
    || fail "plain-plan-works" "the drift plan does not mention the out-of-band tag: $DRIFT_OUT"

  set +e
  CORRECT_OUT="$(cd "$WORK11" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1)"
  CORRECT_RC=$?
  set -e
  [ "$CORRECT_RC" -eq 0 ] || fail "plain-plan-works" "the corrective apply failed: $CORRECT_OUT"
  grep -qE '^Apply complete! Resources: 0 added, 1 changed, 0 destroyed\.$' <<< "$CORRECT_OUT" \
    || fail "plain-plan-works" "expected the corrective apply to change exactly one resource: $CORRECT_OUT"

  set +e
  CLEAN_OUT="$(cd "$WORK11" && "$TOFU" plan -input=false -no-color -detailed-exitcode 2>&1)"
  CLEAN_RC=$?
  set -e
  [ "$CLEAN_RC" -eq 0 ] \
    || fail "plain-plan-works" "-detailed-exitcode after the corrective apply: want 0, got $CLEAN_RC: $CLEAN_OUT"
  echo "  -detailed-exitcode: 2 after the out-of-band tag write, 0 after the corrective apply"

  # 5. Rejected-flag spot check: named errors, not silent ignoring or a
  # generic flag-parse failure.
  set +e
  OUT_REJECT="$(cd "$WORK11" && "$TOFU" plan -input=false -no-color -out=x 2>&1)"
  OUT_REJECT_RC=$?
  set -e
  [ "$OUT_REJECT_RC" -ne 0 ] || fail "plain-plan-works" "choudoufu plan -out=x should have been rejected, exited 0: $OUT_REJECT"
  grep -q "Saved plan files are not available under live resource markers" <<< "$OUT_REJECT" \
    || fail "plain-plan-works" "choudoufu plan -out=x did not name the expected error: $OUT_REJECT"
  [ ! -e "$WORK11/x" ] || fail "plain-plan-works" "choudoufu plan -out=x wrote a plan file despite being rejected"

  set +e
  REFRESH_REJECT="$(cd "$WORK11" && "$TOFU" refresh -input=false -no-color 2>&1)"
  REFRESH_REJECT_RC=$?
  set -e
  [ "$REFRESH_REJECT_RC" -ne 0 ] || fail "plain-plan-works" "choudoufu refresh should have been rejected, exited 0: $REFRESH_REJECT"
  grep -q "Refresh is not available under live resource markers" <<< "$REFRESH_REJECT" \
    || fail "plain-plan-works" "choudoufu refresh did not name the expected error: $REFRESH_REJECT"
  echo "  rejected-flag spot check: plan -out and refresh both refused with their named errors"

  # 6. Teardown via the AWS CLI: "choudoufu apply -destroy" is a named rejection
  # under a live block in v0 (statelessRejections, internal/command/
  # live_mode.go), and emptying the config hits the whole-block-
  # removal gap (a deleted block leaves the live resource standing).
  # The AWS CLI is the only correct
  # v0 teardown story, documented the same way in estate-block/README.md.
  for ALLOC in $(awsl ec2 describe-addresses \
    --query "Addresses[?Tags[?Key=='tofu-estate' && Value=='stateless-e2e-block']].AllocationId" --output text); do
    awsl ec2 release-address --allocation-id "$ALLOC" >/dev/null \
      || fail "plain-plan-works" "teardown: could not release EIP $ALLOC"
  done
  awsl logs delete-log-group --log-group-name "/stateless-e2e-block/app" >/dev/null 2>&1 \
    || fail "plain-plan-works" "teardown: could not delete the log group"
  awsl s3api delete-bucket --bucket "tofu-stateless-e2e-block-data" >/dev/null 2>&1 \
    || fail "plain-plan-works" "teardown: could not delete the bucket"
  BLOCK_SG_ID="$(awsl ec2 describe-security-groups --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" \
    --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || echo None)"
  [ -n "$BLOCK_SG_ID" ] && [ "$BLOCK_SG_ID" != "None" ] \
    && { awsl ec2 delete-security-group --group-id "$BLOCK_SG_ID" >/dev/null \
      || fail "plain-plan-works" "teardown: could not delete the security group"; }
  BLOCK_SUBNET_ID="$(awsl ec2 describe-subnets --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" \
    --query 'Subnets[0].SubnetId' --output text 2>/dev/null || echo None)"
  [ -n "$BLOCK_SUBNET_ID" ] && [ "$BLOCK_SUBNET_ID" != "None" ] \
    && { awsl ec2 delete-subnet --subnet-id "$BLOCK_SUBNET_ID" >/dev/null \
      || fail "plain-plan-works" "teardown: could not delete the subnet"; }
  awsl ec2 delete-vpc --vpc-id "$BLOCK_VPC_ID" >/dev/null \
    || fail "plain-plan-works" "teardown: could not delete the VPC"

  GONE_CHECK="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e-block" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ "$GONE_CHECK" = "None" ] || fail "plain-plan-works" "teardown: the estate-block VPC is still live: $GONE_CHECK"

  rm -rf "$WORK11"
  STEP11_T1=$(date +%s)
  echo "  torn down via the AWS CLI (v0 has no apply -destroy under a live block); $((STEP11_T1 - STEP11_T0))s"
  record_step "plain-plan-works" pass
fi

# ── 12. receipt-cycle ────────────────────────────────────────────────────────
# PE.3 + live/RECEIPTS.md: the estate's HASH-flavor receipt
# (aws_ssm_parameter.demo_effect) is a re-run signal keyed to the effect's
# declared inputs changing. This step breaks that flavor the way an
# already-owned value-bearing record breaks: an out-of-band value write, not
# a delete. The sibling flavor - aws_ssm_parameter.demo_existence, RA.6 - gets
# its own step right after this one, because breaking it is a different
# operation entirely (delete/create, not overwrite); see step 12b's own
# header comment.
echo "=== 12. receipt-cycle — breaking the receipt out of band re-arms the effect ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ] || [ "$LIVE_E2E_EXACTNESS" != "1" ]; then
  not_implemented "receipt-cycle" 5 "the receipt-cycle demo needs full-estate live-plan (-estate probe, PE.3's receipt fixture) and P5.1's exactness work (LIVE_E2E_EXACTNESS=1, P5.2 flips the default to 1)"
else
  STEP12_T0=$(date +%s)
  # Same parameter step 3d adopted; named again here so this step reads on
  # its own.
  RECEIPT_NAME="$RECEIPT_PARAM"

  # 1. After standup+adopt the receipt already exists with a 64-char sha256
  # hash (RECEIPTS.md guard 2) - read with the AWS CLI so the claim is about
  # the cloud, not about this fork's own idea of what it wrote.
  ORIG_VALUE="$(awsl ssm get-parameter --name "$RECEIPT_NAME" --query 'Parameter.Value' --output text 2>/dev/null || echo "")"
  [ -n "$ORIG_VALUE" ] && [ "$ORIG_VALUE" != "None" ] \
    || fail "receipt-cycle" "the receipt parameter $RECEIPT_NAME does not exist"
  grep -qE '^[0-9a-f]{64}$' <<< "$ORIG_VALUE" \
    || fail "receipt-cycle" "the receipt's value is not a 64-character hex sha256 hash: $ORIG_VALUE"
  echo "  receipt exists: $RECEIPT_NAME = $ORIG_VALUE (64-char hash: the HASH flavor)"

  # A clean baseline first, or nothing below means anything.
  run_tf "$MAIN" live-plan -input=false -no-color
  BASELINE="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "receipt-cycle" "live-plan failed before breaking the receipt: $BASELINE"
  assert_full_estate_clean "$BASELINE" "receipt-cycle"

  # 2. Break it out of band: a write that does not match what the config's
  # hash would produce - exactly the effect-drift the receipt exists to
  # catch. No live-apply command exists for this (block-less) estate,
  # so this CLI write also plays the part RECEIPTS.md's guard 3 gives to
  # "the layer above": the receipt is written directly, the way an Op would
  # after running (or, here, breaking) the effect.
  BROKEN_VALUE="$(printf '%064d' 0)"
  awsl ssm put-parameter --name "$RECEIPT_NAME" --type String --value "$BROKEN_VALUE" --overwrite >/dev/null \
    || fail "receipt-cycle" "could not overwrite the receipt out of band"
  readopt_receipt "receipt-cycle"

  # 3. The plan re-arms the effect: the receipt's value change is the
  # reviewable "effect will fire" signal (RECEIPTS.md guard 3). The receipt
  # was adopted in step 3d, so this is an in-place update on a resource this
  # estate genuinely owns - which is what makes "re-armed" mean anything. An
  # unowned receipt would render the same break as part of a create, and the
  # step would be asserting nothing about the receipt cycle at all.
  live_plan "$MAIN" "receipt-cycle"
  ARMED="$TF_OUT"

  # The whole-plan shape, with the receipt named as this step's own expected
  # change: an unexplained create, any destroy, any replacement, or a Plan:
  # line that disagrees with the headers all fail here.
  assert_estate_plan "$ARMED" "receipt-cycle" "aws_ssm_parameter.demo_effect" "" "the broken-receipt plan"

  RECEIPT_HEADER="  # aws_ssm_parameter.demo_effect will be updated in-place"
  grep -qF "$RECEIPT_HEADER" <<< "$ARMED" \
    || fail "receipt-cycle" "the broken receipt did not re-arm a plan on aws_ssm_parameter.demo_effect: $ARMED"

  RECEIPT_BLOCK="$(plan_block "$ARMED" "aws_ssm_parameter.demo_effect")"
  [ -n "$RECEIPT_BLOCK" ] || fail "receipt-cycle" "could not extract the receipt's diff block"
  grep -qE '(value|value_wo)' <<< "$RECEIPT_BLOCK" \
    || fail "receipt-cycle" "the receipt's diff does not mention its value at all: $RECEIPT_BLOCK"
  # value's own companions: value_wo (a write-only attribute the provider
  # cannot read back and re-sends on any in-place update) and version, which
  # the AWS provider bumps as a side effect of any value write and which a
  # real value change therefore always carries with it.
  RECEIPT_BAD="$(grep -E '^ {6}[~+-] [A-Za-z0-9_]+' <<< "$RECEIPT_BLOCK" \
    | sed -E 's/^ {6}[~+-] ([A-Za-z0-9_]+).*/\1/' | grep -vE '^(tags(_all)?|value|value_wo|version)$')" || true
  [ -z "$RECEIPT_BAD" ] \
    || fail "receipt-cycle" "the receipt's diff touches attribute(s) beyond value/version/tags: $RECEIPT_BAD"
  echo "  broken receipt re-armed: aws_ssm_parameter.demo_effect changed in place (value present), nothing else"

  # 4. Corrective write, played by the AWS CLI for the same reason step 2's
  # break was: write back the value the config itself computes.
  awsl ssm put-parameter --name "$RECEIPT_NAME" --type String --value "$ORIG_VALUE" --overwrite >/dev/null \
    || fail "receipt-cycle" "could not restore the receipt's value"
  readopt_receipt "receipt-cycle"

  run_tf "$MAIN" live-plan -input=false -no-color
  CLEAN="$TF_OUT"
  RC="$TF_RC"
  [ "$RC" -eq 0 ] || fail "receipt-cycle" "live-plan failed after the corrective write: $CLEAN"
  assert_full_estate_clean "$CLEAN" "receipt-cycle"

  [ ! -f "$MAIN/terraform.tfstate" ] \
    || fail "receipt-cycle" "terraform.tfstate exists after the receipt cycle"

  STEP12_T1=$(date +%s)
  echo "  receipt cycle complete: broken, re-armed, corrected, re-plan clean; $((STEP12_T1 - STEP12_T0))s"
  record_step "receipt-cycle" pass
fi

# ── 12b. receipt-cycle-existence ─────────────────────────────────────────────
# RA.6 + RECEIPTS.md, "Two flavors, prefer the simpler": the estate's other
# receipt (aws_ssm_parameter.demo_existence) is the EXISTENCE flavor, the
# default recommendation — its value is the constant "done", so existence is
# the entire bit. Breaking it is a different operation from step 12's hash
# flavor: there is no changed value to overwrite, so the out-of-band break is
# a genuine delete, and the re-arm signal is the parameter's own create
# header reappearing — "will be created" meaning "will fire".
#
# Gap #11 (chant/test/floci-gaps.md — PutParameter --overwrite drops the tag
# set) is what step 12 works around by re-adopting after every value write;
# this step's break sidesteps it, because delete/create has no --overwrite
# call for that gap to touch. But the CREATE half of this cycle runs straight
# into gap #10 (PutParameter drops inline tags on create) instead: once the
# harness plays the Op and recreates the parameter, it reads back untagged —
# the same shape step 3d's very first adoption handled — so this step
# re-adopts with the same two-AWS-CLI-call pattern before trusting the next
# plan.
echo "=== 12b. receipt-cycle-existence — deleting the receipt out of band re-arms the effect ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ] || [ "$LIVE_E2E_EXACTNESS" != "1" ]; then
  not_implemented "receipt-cycle-existence" 5 "same gating as receipt-cycle: needs full-estate live-plan (-estate probe, RA.6's second receipt fixture) and P5.1's exactness work (LIVE_E2E_EXACTNESS=1, P5.2 flips the default to 1)"
else
  STEP12B_T0=$(date +%s)
  EXISTENCE_PARAM="/tofu-receipts/stateless-e2e/demo-existence"
  EXISTENCE_ADDR="aws_ssm_parameter.demo_existence"

  # 1. The receipt exists with its constant value (RECEIPTS.md's existence
  # flavor: the value carries no information by design) — read with the AWS
  # CLI so the claim is about the cloud, not this fork's own idea of what it
  # wrote.
  EXISTENCE_VALUE="$(awsl ssm get-parameter --name "$EXISTENCE_PARAM" --query 'Parameter.Value' --output text 2>/dev/null || echo "")"
  [ "$EXISTENCE_VALUE" = "done" ] \
    || fail "receipt-cycle-existence" "the existence receipt's value reads '$EXISTENCE_VALUE', want the constant 'done'"
  echo "  receipt exists: $EXISTENCE_PARAM = done (constant: the EXISTENCE flavor)"

  # A clean baseline first, or nothing below means anything.
  run_tf "$MAIN" live-plan -input=false -no-color
  BASELINE="$TF_OUT"
  [ "$TF_RC" -eq 0 ] || fail "receipt-cycle-existence" "live-plan failed before breaking the receipt: $BASELINE"
  assert_full_estate_clean "$BASELINE" "receipt-cycle-existence"

  # 2. Break it THE EXISTENCE WAY: delete the parameter out of band. No
  # live-apply command exists for this (block-less) estate, so the AWS CLI
  # plays the part RECEIPTS.md guard 3 gives to "the layer above" for both
  # halves of this cycle — the delete here, and the recreate below.
  awsl ssm delete-parameter --name "$EXISTENCE_PARAM" >/dev/null \
    || fail "receipt-cycle-existence" "could not delete the existence receipt out of band"

  # 3. The plan re-arms the effect: the receipt no longer exists at all, so
  # the projection reports it [ABSENT] — genuinely gone, as opposed to an
  # unreadable-but-present resource — and proposes creating it again.
  # assert_estate_plan's sixth argument is exactly this: one address this
  # step expects [ABSENT], accepted as a create, without loosening the check
  # for anything else in the estate (still no other create, change, destroy
  # or replacement tolerated, same as every other step).
  live_plan "$MAIN" "receipt-cycle-existence"
  ARMED="$TF_OUT"
  assert_estate_plan "$ARMED" "receipt-cycle-existence" "" "" "the deleted-receipt plan" "$EXISTENCE_ADDR"

  ARMED_HEADER="  # $EXISTENCE_ADDR will be created"
  grep -qF "$ARMED_HEADER" <<< "$ARMED" \
    || fail "receipt-cycle-existence" "the deleted receipt did not re-arm a create on $EXISTENCE_ADDR: $ARMED"
  grep -qF "$EXISTENCE_ADDR [ABSENT]" <<< "$ARMED" \
    || fail "receipt-cycle-existence" "$EXISTENCE_ADDR is not reported [ABSENT] after being deleted out of band: $(omission_section "$ARMED")"

  EXISTENCE_BLOCK="$(plan_block "$ARMED" "$EXISTENCE_ADDR")"
  [ -n "$EXISTENCE_BLOCK" ] || fail "receipt-cycle-existence" "could not extract the existence receipt's diff block"
  grep -qE '(value|value_wo)' <<< "$EXISTENCE_BLOCK" \
    || fail "receipt-cycle-existence" "the existence receipt's create diff does not mention its value at all: $EXISTENCE_BLOCK"
  echo "  deleted receipt re-armed: $EXISTENCE_ADDR proposed as a create, nothing else"

  # 4. Corrective write, played by the AWS CLI the way step 3d's initial
  # adoption was: put-parameter recreates it (the layer above playing the
  # effect this receipt stands for), which runs straight into gap #10 the
  # same way step 3d's very first creation did — so this re-adopts with the
  # same two AWS CLI calls before trusting the next plan.
  awsl ssm put-parameter --name "$EXISTENCE_PARAM" --type String --value "done" >/dev/null \
    || fail "receipt-cycle-existence" "could not recreate the existence receipt out of band"

  # shellcheck disable=SC2016 # single-quoted: JMESPath backtick literals, not shell interpolation
  EXISTENCE_TAGS_AFTER="$(awsl ssm list-tags-for-resource --resource-type Parameter \
    --resource-id "$EXISTENCE_PARAM" --query 'TagList[?Key==`tofu-estate`]|[0].Value' \
    --output text 2>/dev/null || echo None)"
  if [ "$EXISTENCE_TAGS_AFTER" = "stateless-e2e" ]; then
    echo "  the recreated receipt already carries tofu-estate (floci-gaps #10 appears fixed); nothing to adopt"
  else
    awsl ssm add-tags-to-resource --resource-type Parameter --resource-id "$EXISTENCE_PARAM" \
      --tags "Key=tofu-estate,Value=stateless-e2e" "Key=tofu-address,Value=$EXISTENCE_ADDR" >/dev/null \
      || fail "receipt-cycle-existence" "could not restore the recreated receipt's ownership markers"
    echo "  wrote tofu-estate/tofu-address onto the recreated $EXISTENCE_PARAM (floci-gaps #10: PutParameter dropped the inline set)"
  fi

  run_tf "$MAIN" live-plan -input=false -no-color
  CLEAN="$TF_OUT"
  [ "$TF_RC" -eq 0 ] || fail "receipt-cycle-existence" "live-plan failed after the corrective recreate: $CLEAN"
  assert_full_estate_clean "$CLEAN" "receipt-cycle-existence"

  [ ! -f "$MAIN/terraform.tfstate" ] \
    || fail "receipt-cycle-existence" "terraform.tfstate exists after the existence receipt cycle"

  STEP12B_T1=$(date +%s)
  echo "  existence receipt cycle complete: deleted, re-armed, recreated, re-plan clean; $((STEP12B_T1 - STEP12B_T0))s"
  record_step "receipt-cycle-existence" pass
fi

# ── 13. drift-reconverge — three simultaneous drifts under plain plan/apply ──
# Issue #109 removed the observational snapshot this step used to pair its
# drift matrix with: the tofu-snapshots/<estate> git branch, its commits,
# and every git log / git diff assertion went with the subsystem (the live
# system is authoritative and readable at any time, so a stored snapshot
# was a stale copy of what every run re-derives). What survives, rehomed
# here rather than lost, is the matrix itself: three drifts of three
# different shapes injected out of band — (a) an attribute the plan reads
# back, (b) a plain tag beside the markers on a marked resource, (c) a
# whole marked, taggable resource deleted — must render on ONE plain
# "choudoufu plan" as exactly two in-place updates and one create, nothing
# else, and one untargeted apply must reconverge all three in the same
# breath. drift-exact (step 6) proves each shape alone, one at a time,
# under live-plan; this step proves them together, under the plain-command
# path (P4.1's "no live-prefixed command anywhere in sight").
#
# Plain plan/apply need a "live" block, and $MAIN must stay free of one:
# adding it to live/e2e/estate/ would make standup's own apply (step 2)
# stateless and stop it from producing the terraform.tfstate adopt (step 3)
# exists to delete. So this step works against $DO_DIR, a mktemp copy of
# $MAIN's current on-disk config plus one additional file adding the live
# block — a phase-local estate copy, never a second standup: $DO_DIR names
# the SAME estate ($MAIN's "stateless-e2e") and declares the SAME resources
# $MAIN already applied, so its own baseline apply below adopts what
# standup already created rather than creating anything new. The AWS CLI
# mutations below land on those same shared live resources, so every other
# step that plans against $MAIN after this one still needs to see a clean,
# converged estate — which is why this step's last acts are a full,
# untargeted apply that undoes every drift it injected and a clean
# live-plan against $MAIN itself, before moving on.
echo "=== 13. drift-reconverge — three simultaneous drifts under plain plan/apply ==="
if [ "$HAVE_LIVE_ESTATE" -eq 0 ] || [ "$HAVE_LIVE_BLOCK" -eq 0 ] || [ "$LIVE_E2E_EXACTNESS" != "1" ]; then
  not_implemented "drift-reconverge" 5 "needs full-estate discovery (-estate probe) to adopt the standing estate, the config-level live block (P4.1, probed via 'choudoufu validate' in section 0b) for the plain plan/apply path, and P5.1's exactness work (LIVE_E2E_EXACTNESS=1, P5.2 flipped the default to 1)"
else
  STEP13_T0=$(date +%s)
  DO_DIR="$(mktemp -d)"
  cp -R "$MAIN/." "$DO_DIR/"

  # The one addition that turns $DO_DIR's plain "choudoufu plan"/"apply"
  # stateless: a second terraform{} block — merges fine alongside the
  # copied versions.tf's own terraform{} block, the same way a real module
  # splitting required_providers from a live block across files would —
  # naming the SAME estate $MAIN already owns.
  cat > "$DO_DIR/live_block.tf" <<'DOEOF'
terraform {
  live {
    estate = "stateless-e2e"
  }
}
DOEOF

  ( cd "$DO_DIR" && "$TOFU" init -input=false >/dev/null ) \
    || fail "drift-reconverge" "choudoufu init in the live-block copy did not succeed"

  # Baseline apply: $DO_DIR declares exactly what $MAIN already applied and
  # names the estate $MAIN's live resources already carry, so this adopts
  # the standing estate rather than creating anything — "no second
  # standup".
  run_tf "$DO_DIR" apply -auto-approve -input=false -no-color
  BASE_APPLY_OUT="$TF_OUT"
  [ "$TF_RC" -eq 0 ] || fail "drift-reconverge" "the baseline apply against the standing estate failed: $BASE_APPLY_OUT"
  grep -qE '^Apply complete! Resources: 0 added, 0 changed, 0 destroyed\.$' <<< "$BASE_APPLY_OUT" \
    || fail "drift-reconverge" "the baseline apply should adopt the standing estate with no changes at all: $BASE_APPLY_OUT"
  echo "  baseline apply adopted the standing estate with no changes"

  # ── Inject three drifts out of band, one of each shape: (a) an
  # attribute the plan reads back, (b) a plain tag beside the marker on a
  # marked resource, (c) a whole marked, taggable resource deleted.
  DO_LOG_NAME="/stateless-e2e/app"
  DO_ALARM_NAME="tofu-stateless-e2e-cpu"
  DO_VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=stateless-e2e" \
    --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo None)"
  [ -n "$DO_VPC_ID" ] && [ "$DO_VPC_ID" != "None" ] || fail "drift-reconverge" "could not find the estate's VPC"

  # (a) attribute drift: the log group's retention, the same mechanism
  # drift-exact's log-group-retention case (step 6) already proves reads
  # back and renders — reused rather than reinvented, on the same address,
  # once step 6 has already run and reconverged it back to 1.
  awsl logs put-retention-policy --log-group-name "$DO_LOG_NAME" --retention-in-days 7 >/dev/null \
    || fail "drift-reconverge" "could not set the log group retention out of band"

  # (b) tag drift: a plain tag beside the two markers on the VPC — never
  # tofu-estate or tofu-address themselves, which is the "not the marker
  # itself" this step's own brief asks for.
  awsl ec2 create-tags --resources "$DO_VPC_ID" --tags Key=DriftObserved,Value=present >/dev/null \
    || fail "drift-reconverge" "could not tag the VPC out of band"

  # (c) whole-resource drift: the CloudWatch alarm, deleted entirely.
  # Client-named (its identity is alarm_name, already in config), taggable,
  # and untouched by any other step in this harness.
  awsl cloudwatch delete-alarms --alarm-names "$DO_ALARM_NAME" >/dev/null \
    || fail "drift-reconverge" "could not delete the CloudWatch alarm out of band"

  echo "  drift injected: log group retention -> 7, VPC tagged DriftObserved=present, the CloudWatch alarm deleted"

  # ── The next plan renders all three, and nothing else. Plain "choudoufu
  # plan" here, never "live-plan": under a live block that IS the whole
  # point (P4.1), and it is the stock renderer
  # (internal/command/jsonformat/plan.go) doing the per-resource diff
  # blocks either way — the same "# <addr> will be ..." header shape
  # plan_addrs/plan_block already parse for live-plan's output.
  set +e
  DRIFT_PLAN_OUT="$(cd "$DO_DIR" && "$TOFU" plan -input=false -no-color -detailed-exitcode 2>&1)"
  DRIFT_PLAN_RC=$?
  set -e
  [ "$DRIFT_PLAN_RC" -eq 2 ] \
    || fail "drift-reconverge" "-detailed-exitcode after the three out-of-band drifts: want 2, got $DRIFT_PLAN_RC: $DRIFT_PLAN_OUT"

  # (a) in-place update naming the attribute the plan read back.
  grep -qF "  # aws_cloudwatch_log_group.app will be updated in-place" <<< "$DRIFT_PLAN_OUT" \
    || fail "drift-reconverge" "the log group retention drift is not visible in the plan: $DRIFT_PLAN_OUT"
  DRIFT_LOG_BLOCK="$(plan_block "$DRIFT_PLAN_OUT" "aws_cloudwatch_log_group.app")"
  grep -q "retention_in_days" <<< "$DRIFT_LOG_BLOCK" \
    || fail "drift-reconverge" "the log group diff does not mention retention_in_days: $DRIFT_LOG_BLOCK"

  # (b) current design, per drift-exact's own "vpc" case (step 6): a plain
  # tag beside the markers gets no special marker-adjacent handling of its
  # own. It is read back as an ordinary attribute like any other, compared
  # against the declared tags map (which does not have it), and rendered as
  # an in-place update to tags/tags_all that converges by REMOVING the
  # extra key — the same "declared config wins" answer this harness's whole
  # drift-exact matrix already gives every out-of-band tag, marker-adjacent
  # or not. There is no separate "foreign tag" category: the marker keys
  # are simply excluded from the diff (they are not part of any resource's
  # own declared tags to begin with — MARKERS.md), and everything else is
  # fair game like this one.
  grep -qF "  # aws_vpc.main will be updated in-place" <<< "$DRIFT_PLAN_OUT" \
    || fail "drift-reconverge" "the VPC tag drift is not visible in the plan: $DRIFT_PLAN_OUT"
  DRIFT_VPC_BLOCK="$(plan_block "$DRIFT_PLAN_OUT" "aws_vpc.main")"
  grep -q "DriftObserved" <<< "$DRIFT_VPC_BLOCK" \
    || fail "drift-reconverge" "the VPC diff does not mention the out-of-band DriftObserved tag: $DRIFT_VPC_BLOCK"

  # (c) markers are the record, not the live resource: the alarm vanished,
  # so the plan proposes recreating exactly what config still declares — a
  # create, the same answer receipt-cycle-existence (step 12b) already
  # proves for a deleted SSM parameter. No [ABSENT] annotation here (that
  # is live-plan's own renderer, its omissions machinery); a plain plan
  # under a live block has no broader "whole estate" reconciliation view to
  # omit anything FROM, so the resource simply reads as new, the same as
  # any resource's first apply.
  grep -qF "  # aws_cloudwatch_metric_alarm.cpu will be created" <<< "$DRIFT_PLAN_OUT" \
    || fail "drift-reconverge" "the deleted alarm did not re-arm a create in the plan: $DRIFT_PLAN_OUT"

  # And nothing else moved: exactly these three addresses have a diff, and
  # the summary line agrees.
  DRIFT_CHANGED="$(plan_addrs "$DRIFT_PLAN_OUT" 'will be updated in-place')"
  DRIFT_CREATED="$(plan_addrs "$DRIFT_PLAN_OUT" 'will be created')"
  DRIFT_DESTROYED="$(plan_addrs "$DRIFT_PLAN_OUT" 'will be destroyed')"
  { [ "$(count_lines "$DRIFT_CHANGED")" -eq 2 ] \
    && in_list "aws_cloudwatch_log_group.app" "$DRIFT_CHANGED" \
    && in_list "aws_vpc.main" "$DRIFT_CHANGED"; } \
    || fail "drift-reconverge" "expected exactly 2 in-place updates (the log group, the VPC), got: $DRIFT_CHANGED"
  { [ "$(count_lines "$DRIFT_CREATED")" -eq 1 ] \
    && in_list "aws_cloudwatch_metric_alarm.cpu" "$DRIFT_CREATED"; } \
    || fail "drift-reconverge" "expected exactly 1 create (the alarm), got: $DRIFT_CREATED"
  [ -z "$DRIFT_DESTROYED" ] \
    || fail "drift-reconverge" "expected no destroys, got: $DRIFT_DESTROYED"
  grep -qE '^Plan: 1 to add, 2 to change, 0 to destroy\.$' <<< "$DRIFT_PLAN_OUT" \
    || fail "drift-reconverge" "the plan summary disagrees with its own headers: $(grep -E '^Plan:' <<< "$DRIFT_PLAN_OUT" || echo 'no Plan: line at all')"
  echo "  plan renders all three: log group retention in-place, VPC tags in-place, alarm re-armed as a create — nothing else"

  # ── Reconverge: a real, untargeted apply corrects all three drifts in one
  # pass — 2 changed (the log group, the VPC), 1 added (the alarm).
  run_tf "$DO_DIR" apply -auto-approve -input=false -no-color
  CONVERGE_APPLY_OUT="$TF_OUT"
  [ "$TF_RC" -eq 0 ] || fail "drift-reconverge" "the reconverging apply failed: $CONVERGE_APPLY_OUT"
  grep -qE '^Apply complete! Resources: 1 added, 2 changed, 0 destroyed\.$' <<< "$CONVERGE_APPLY_OUT" \
    || fail "drift-reconverge" "expected the reconverging apply to add 1 (the alarm) and change 2 (the log group, the VPC): $CONVERGE_APPLY_OUT"

  # ── Convergence, confirmed by a plan too, not only by the apply's own
  # report — the same double-check drift-exact and the receipt-cycle steps
  # already give every correction.
  set +e
  CLEAN_PLAN_OUT="$(cd "$DO_DIR" && "$TOFU" plan -input=false -no-color -detailed-exitcode 2>&1)"
  CLEAN_PLAN_RC=$?
  set -e
  [ "$CLEAN_PLAN_RC" -eq 0 ] \
    || fail "drift-reconverge" "-detailed-exitcode after reconverging: want 0, got $CLEAN_PLAN_RC: $CLEAN_PLAN_OUT"

  # And $MAIN itself — the estate every later step plans against — is clean
  # too: this step's AWS CLI mutations landed on $MAIN's own live
  # resources, so its own convergence is what every step after this one is
  # relying on, the same restoration obligation removal-exact,
  # count-scale-down and rename-no-churn already carry.
  live_plan "$MAIN" "drift-reconverge"
  assert_full_estate_clean "$TF_OUT" "drift-reconverge"

  rm -rf "$DO_DIR"
  STEP13_T1=$(date +%s)
  echo "  reconverged; \$MAIN's own live-plan is clean again; $((STEP13_T1 - STEP13_T0))s"
  record_step "drift-reconverge" pass
fi

# ── 14. lint-rejects ─────────────────────────────────────────────────────────
# PE.1's missing half. That task built live/e2e/limits/ — one minimal
# configuration per banned or bounded construct — and the lint unit suite that
# walks it (internal/live/lint/limits_test.go), but the e2e step its own
# spec promised, "walking every limits dir and asserting the exact rule fires
# (and nothing else does)", was never written: until now the harness contained
# no reference to the limits wing at all.
#
# The unit suite calls Check() directly. This step is the black-box half, and
# it is a different claim: that the SHIPPED BINARY refuses the configuration,
# with the rule named in what an operator actually reads. Lint runs before
# anything touches a provider or a cloud, so each case is a bare directory and
# a single command — no init, no floci, no credentials.
#
# Two tables, and every directory must be in exactly one of them. The second
# is the honest one: PE.1 asked for constructs with no rule yet to be
# ASSERTED as unenforced rather than quietly skipped, so an enforcement gap is
# visible in the harness output instead of hidden by omission. The day a rule
# lands for one of these, this step fails and the fix is to move the entry, not
# to relax the assertion.
echo "=== 14. lint-rejects — every limits fixture is refused by its own named rule ==="
if [ "$HAVE_LIVE_PLAN" -eq 0 ]; then
  not_implemented "lint-rejects" 1 "choudoufu live-plan does not exist yet (P1.4), and lint runs inside it"
else
  LIMITS_DIR="$ROOT/live/e2e/limits"
  [ -d "$LIMITS_DIR" ] || fail "lint-rejects" "the limits wing is missing at $LIMITS_DIR (PE.1)"

  # dir:rule, one pair per line. The rule is the value Issue.Rule renders
  # into the diagnostic's "Rule: <rule>." line, not a prose fragment.
  LINT_ENFORCED="local-exec:provisioner
remote-exec:provisioner
null-resource:logical-resource
local-file:logical-resource
random-password:logical-resource
time-sleep:logical-resource
remote-state:remote-state
moved-block:moved-block
child-module:child-module
backend-block:state-backend
cloud-block:state-backend
unadmitted-type:unadmitted-type
count-index-in-tag:count-index
foreach-invalid-key:for-each-key
overlong-address:overlong-address"

  # Directories no LINT rule catches. Each still has to be REFUSED — by
  # something — and each is asserted to produce no "Rule:" line, which is
  # what makes the gap visible rather than assumed.
  #   duplicate-identity  rejected at identity resolution
  #                       (internal/live/identity), not by lint. The
  #                       named error is asserted below.
  LINT_TODO="duplicate-identity"

  # Completeness: every subdirectory of the limits wing appears in exactly
  # one table. Without this a new fixture directory would be silently
  # skipped, which is the failure mode a table-driven step is prone to.
  LINT_LISTED=""
  while IFS=: read -r LDIR _; do
    [ -n "$LDIR" ] || continue
    LINT_LISTED="$LINT_LISTED $LDIR"
  done <<< "$LINT_ENFORCED"
  LINT_LISTED="$LINT_LISTED $LINT_TODO"
  for LPATH in "$LIMITS_DIR"/*/; do
    LDIR="$(basename "$LPATH")"
    in_list "$LDIR" "$LINT_LISTED" \
      || fail "lint-rejects" "live/e2e/limits/$LDIR is in neither table — add it to LINT_ENFORCED with its rule, or to LINT_TODO with why nothing catches it yet"
  done
  for LDIR in $LINT_LISTED; do
    [ -d "$LIMITS_DIR/$LDIR" ] \
      || fail "lint-rejects" "the tables name live/e2e/limits/$LDIR, which does not exist"
  done

  LINT_WORK="$WORK/limits"
  LINT_N=0
  while IFS=: read -r LDIR LRULE; do
    [ -n "$LDIR" ] || continue

    # A copy per case: lint refuses before init, but a stray .terraform or
    # lock file written into the checkout would dirty it, and the harness
    # never writes to $ROOT. The copy is recursive because one fixture
    # (child-module) is a tree - a module call needs something to call.
    rm -rf "$LINT_WORK"
    mkdir -p "$LINT_WORK"
    cp -R "$LIMITS_DIR/$LDIR"/. "$LINT_WORK/" \
      || fail "lint-rejects" "$LDIR: could not copy the fixture"

    # The one fixture that needs anything before live-plan. A module block
    # has to be installed before the configuration loads at all, so without
    # this the refusal would be "Module not installed" rather than the rule.
    # "choudoufu get" installs local module sources and touches no network,
    # no provider and no backend.
    if [ "$LDIR" = "child-module" ]; then
      run_tf "$LINT_WORK" get -no-color
      [ "$TF_RC" -eq 0 ] \
        || fail "lint-rejects" "$LDIR: choudoufu get could not install the local module: $TF_OUT"
    fi

    run_tf "$LINT_WORK" live-plan -input=false -no-color
    [ "$TF_RC" -ne 0 ] \
      || fail "lint-rejects" "$LDIR: live-plan exited 0 — the fixture is supposed to be refused by rule '$LRULE': $TF_OUT"

    # The rule fires, it is an ERROR rather than a warning, and it is the
    # ONLY rule that fires. "and nothing else does" is PE.1's own wording
    # and it is the half that catches a rule that got too broad.
    LINT_RULES="$(echo "$TF_OUT" | sed -n 's/^Rule: \([a-z-]*\)\..*$/\1/p' | sort -u | tr '\n' ' ')"
    LINT_RULES="${LINT_RULES% }"
    [ "$LINT_RULES" = "$LRULE" ] \
      || fail "lint-rejects" "$LDIR: expected exactly rule '$LRULE' and no other, got [$LINT_RULES]: $TF_OUT"
    grep -qE '^Error: ' <<< "$TF_OUT" \
      || fail "lint-rejects" "$LDIR: rule '$LRULE' fired but nothing was reported as an Error: $TF_OUT"

    LINT_N=$((LINT_N + 1))
    echo "  $LDIR -> $LRULE PASS"
  done <<< "$LINT_ENFORCED"

  for LDIR in $LINT_TODO; do
    rm -rf "$LINT_WORK"
    mkdir -p "$LINT_WORK"
    cp -R "$LIMITS_DIR/$LDIR"/. "$LINT_WORK/" \
      || fail "lint-rejects" "$LDIR: could not copy the fixture"

    run_tf "$LINT_WORK" live-plan -input=false -no-color
    [ "$TF_RC" -ne 0 ] \
      || fail "lint-rejects" "$LDIR: live-plan exited 0 — the construct is out of the subset and something must refuse it: $TF_OUT"
    LINT_RULES="$(echo "$TF_OUT" | sed -n 's/^Rule: \([a-z-]*\)\..*$/\1/p' | sort -u | tr '\n' ' ')"
    [ -z "${LINT_RULES% }" ] \
      || fail "lint-rejects" "$LDIR: a lint rule now fires here ([${LINT_RULES% }]) — move it into LINT_ENFORCED and update live/LIMITATIONS.md and internal/live/lint/limits_test.go in the same change"

    case "$LDIR" in
      duplicate-identity)
        grep -q "Two resources with the same identity" <<< "$TF_OUT" \
          || fail "lint-rejects" "$LDIR: expected the identity-resolution error naming the collision: $TF_OUT"
        echo "  $LDIR -> TODO (no lint rule; refused at identity resolution instead)" ;;
      *)
        echo "  $LDIR -> TODO (documented, not yet enforced by any rule; refused for another reason)" ;;
    esac
  done
  rm -rf "$LINT_WORK"

  echo "  $LINT_N limits fixtures refused by exactly their named rule; $(echo "$LINT_TODO" | wc -w | tr -d ' ') asserted as not-yet-enforced"
  record_step "lint-rejects" pass
fi

echo
echo "PASS: stateless-mode E2E harness reached the end."

# --expect's verdict (task PE.2): reached only if nothing above called fail()
# (which already exits nonzero on its own, satisfying "any fail anywhere is
# exit nonzero regardless" without this needing to check HARNESS_FAILED).
if [ -n "$EXPECT_PHASE" ]; then
  evaluate_expect
fi
