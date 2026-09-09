#!/usr/bin/env bash
# The five Ops, actually run (issue #1026).
#
# Everything else about this example proves what the generated YAML says.
# This runs the Ops the YAML names, in the order the pipeline runs them,
# against the pinned floci emulator, and reads each run's own `--json`
# status rather than its exit code.
#
# One verdict line per Op, greppable:
#
#     SMOKE op=<name> verdict=<pass|fail> status=<json status>
#
# ## Why a scratch repository rather than this worktree
#
# chant's gate ledger is an orphan git branch, `chant/lifecycle`, and
# `pushLifecycle` (chant's lifecycle/git.ts) pushes it to the first
# configured remote after every gate write, force-with-lease, swallowing
# the failure. Run in place and a local smoke writes commits to whatever
# `origin` the checkout has - which for this repository is
# INTENTIUS/choudoufu itself. So the run happens in a throwaway repository
# with no remote: `git remote` comes back empty, `pushLifecycle` returns
# false without spawning a push, and the ledger stays on disk where the
# assertions below can read it. It is also how the README says a consumer
# lays this out - the chant project at the repository root.
#
# ## Why CHANT_FINDING_MODE=report
#
# This smoke's job is plan/gate/apply behaviour, not the posting path: the
# scratch repository above has no remote and no pull request, and the CI
# smoke (.github/workflows/ci-pipelines-smoke.yml) dispatches on
# workflow_dispatch, which carries no pull request either. Every posting
# mode (`comment`, `issue`) is `reconcilePr`, and `reconcilePr` throws when a
# run carries none of those - there is nowhere to post. Since chant #2291
# lifted Forgejo's `comment` refusal, no CHANT_FORGE value is left whose two
# reporting Ops both default to `report` on their own, so this script sets
# `CHANT_FINDING_MODE=report` (src/forge.ts) to force both `live-plan` and
# `live-discover` into `report` mode regardless of which forge CHANT_FORGE
# below picks. That variable is never set when the three committed trees are
# generated - see the doc comment on `FINDING_MODE_OVERRIDE` in
# src/forge.ts, and the currency guard in tests/pipelines.test.ts that checks
# a regeneration under it would differ from what is committed.
#
# Env:
#   CHOUDOUFU_BIN   an existing choudoufu binary; default builds ./cmd/choudoufu
#   SMOKE_ENDPOINT  an emulator that is already up (CI service container).
#                   Set it and this script starts no container and needs no
#                   docker at all - which is how .github/workflows/ci-pipelines-smoke.yml
#                   runs the identical sequence against a `services:` floci.
#   FLOCI_PORT      host port for the emulator it starts itself (default 4570)
#   FLOCI_IMAGE     override the pin in live/floci-image
#   KEEP            1 leaves the scratch repository and the container up
#   BREAK           1 breaks two assertions on purpose, to prove they can fail

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT="$(cd "$EXAMPLE_DIR/../.." && pwd)"

FLOCI_PORT="${FLOCI_PORT:-4570}"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
CONTAINER="ci-pipelines-smoke-$$"
WORK=""
FAILURES=0
PASSES=0

log()  { echo "  $*"; }
step() { echo; echo "=== $* ==="; echo; }
die()  { echo "SMOKE fatal: $*" >&2; cleanup; exit 1; }

cleanup() {
  if [ -n "${SMOKE_ENDPOINT:-}" ]; then
    [ -n "$WORK" ] && [ "${KEEP:-0}" != "1" ] && rm -rf "$WORK"
    return
  fi
  if [ "${KEEP:-0}" = "1" ]; then
    echo "KEEP=1: container $CONTAINER and scratch repo $WORK left up." >&2
    return
  fi
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  [ -n "$WORK" ] && rm -rf "$WORK"
}
trap cleanup EXIT

# verdict <op> <expected-status> <actual-status> [extra]
verdict() {
  local op="$1" want="$2" got="$3" extra="${4:-}"
  if [ "$want" = "$got" ]; then
    PASSES=$((PASSES + 1))
    echo "SMOKE op=$op verdict=pass status=$got${extra:+ $extra}"
  else
    FAILURES=$((FAILURES + 1))
    echo "SMOKE op=$op verdict=fail status=$got want=$want${extra:+ $extra}"
  fi
}

# The run record chant writes as the last JSON line of `--json` output.
# Read the status out of it; anything that is not a run record is a
# verdict of its own ("no-run-record"), never a silent pass.
run_status() {
  local file="$1"
  node -e '
    const fs = require("fs");
    const lines = fs.readFileSync(process.argv[1], "utf8").split("\n");
    for (let i = lines.length - 1; i >= 0; i--) {
      const line = lines[i].trim();
      if (!line.startsWith("{")) continue;
      try {
        const r = JSON.parse(line);
        if (r && r.op && r.status) { console.log(r.status); process.exit(0); }
      } catch { /* not the record */ }
    }
    console.log("no-run-record");
  ' "$file"
}

# ---------------------------------------------------------------- preflight

if [ -z "${SMOKE_ENDPOINT:-}" ]; then
  command -v docker >/dev/null 2>&1 || die "docker is not installed"
  docker info >/dev/null 2>&1 || die "the docker daemon is not running (start Docker Desktop)"
fi
command -v node >/dev/null 2>&1 || die "node is not installed"
command -v git  >/dev/null 2>&1 || die "git is not installed"

step "the binary under test"
if [ -n "${CHOUDOUFU_BIN:-}" ]; then
  [ -x "$CHOUDOUFU_BIN" ] || die "CHOUDOUFU_BIN=$CHOUDOUFU_BIN is not executable"
  CHOUDOUFU_BIN="$(cd "$(dirname "$CHOUDOUFU_BIN")" && pwd)/$(basename "$CHOUDOUFU_BIN")"
  log "CHOUDOUFU_BIN=$CHOUDOUFU_BIN"
else
  command -v go >/dev/null 2>&1 || die "no CHOUDOUFU_BIN and no go toolchain to build one"
  BIN_DIR="$(mktemp -d)"
  log "building ./cmd/choudoufu from this worktree"
  ( cd "$ROOT" && env -u PWD go build -o "$BIN_DIR/choudoufu" ./cmd/choudoufu ) \
    || die "go build ./cmd/choudoufu failed"
  CHOUDOUFU_BIN="$BIN_DIR/choudoufu"
fi
log "$("$CHOUDOUFU_BIN" version | head -1)"

# ------------------------------------------------------- the scratch project

step "the project, at the root of a repository with no remote"
WORK="$(mktemp -d)"
mkdir -p "$WORK/repo"
tar -C "$EXAMPLE_DIR" -cf - \
  --exclude=node_modules --exclude=.terraform --exclude='*.tfstate*' --exclude='*.tfplan' \
  . | tar -C "$WORK/repo" -xf -
if [ -d "$EXAMPLE_DIR/node_modules" ]; then
  cp -R "$EXAMPLE_DIR/node_modules" "$WORK/repo/node_modules"
else
  ( cd "$WORK/repo" && npm ci --no-audit --no-fund >/dev/null ) || die "npm ci failed"
fi
(
  cd "$WORK/repo"
  git init -q .
  git config user.email smoke@example.invalid
  git config user.name "ci-pipelines smoke"
  printf 'node_modules/\n.terraform/\n*.tfstate*\n*.tfplan\n' > .gitignore
  git add -A >/dev/null && git commit -qm "the ci-pipelines example, for the smoke"
) || die "could not stand up the scratch repository"
git -C "$WORK/repo" remote | grep -q . && die "the scratch repository has a remote; the ledger would be pushed"
log "$WORK/repo (no remote: chant's pushLifecycle cannot reach anything)"

# ------------------------------------------------------------- the emulator

step "the emulator"
if [ -n "${SMOKE_ENDPOINT:-}" ]; then
  ENDPOINT="$SMOKE_ENDPOINT"
  log "using the emulator already up at $ENDPOINT (started no container)"
else
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker run -d --name "$CONTAINER" -p "127.0.0.1:$FLOCI_PORT:4566" "$FLOCI_IMAGE" >/dev/null \
    || die "could not start $FLOCI_IMAGE"
  ENDPOINT="http://localhost:$FLOCI_PORT"
  log "$FLOCI_IMAGE starting at $ENDPOINT"
fi
ready=0
for _ in $(seq 1 90); do
  if curl -fsS "$ENDPOINT/_localstack/health" >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
[ "$ready" = "1" ] || die "floci never answered on $ENDPOINT/_localstack/health"
log "emulator ready: $(curl -fsS "$ENDPOINT/_localstack/health" | head -c 120)"

export PATH="$(dirname "$CHOUDOUFU_BIN"):$PATH"
export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1
export TF_VAR_aws_region=us-east-1
export CHANT_FORGE=forgejo
export CHANT_FINDING_MODE=report
export CHECKPOINT_DISABLE=1

OUT="$WORK/out"; mkdir -p "$OUT"
cd "$WORK/repo" || die "cannot enter the scratch repository"

# op <name> <expected-status> [extra chant args...]
op() {
  local name="$1" want="$2"; shift 2
  step "chant run $name"
  npx chant run "$name" --json "$@" > "$OUT/$name.$want.log" 2>&1
  local got; got="$(run_status "$OUT/$name.$want.log")"
  verdict "$name" "$want" "$got"
  [ "$got" = "$want" ] || tail -25 "$OUT/$name.$want.log" | sed 's/^/    /'
}

# The pending gate the last run left on the ledger branch, and the
# approval that resolves it. Both are read out of git rather than out of
# the run's own summary: the ledger is the approval of record.
#
# gate <op> <gate-name>
gate() {
  local name="$1" gatename="$2"
  step "the gate $gatename is a pending fact on chant/lifecycle"
  local pending
  pending="$(git -C "$WORK/repo" show "chant/lifecycle:_gates/$name.jsonl" 2>/dev/null || true)"
  if grep -q '"kind":"pending"' <<<"$pending"; then
    verdict "$name/gate-pending" recorded recorded
  else
    verdict "$name/gate-pending" recorded absent
  fi
  log "$pending"

  step "chant approve $name $gatename"
  if [ "${BREAK:-0}" = "1" ] && [ "$name" = "live-apply" ]; then
    log "BREAK=1: not approving, so the status assertion on the next run has to fail"
    verdict "$name/approve" resolved not-approved
    return
  fi
  npx chant approve "$name" "$gatename" --approver ci-pipelines-smoke > "$OUT/approve-$name.log" 2>&1
  if grep -q "resolved by ci-pipelines-smoke" "$OUT/approve-$name.log"; then
    verdict "$name/approve" resolved resolved
  else
    verdict "$name/approve" resolved "$(head -1 "$OUT/approve-$name.log")"
  fi
}

# ------------------------------------------------------------- the sequence
#
# The order the pipeline runs them, and the status each one is supposed to
# reach. Both push Ops stop at their own gate on the first run and apply on
# the second, which is the whole shape `--gated-exit 0` exists for.

op live-check ok
op live-plan  ok

op live-apply gated --gated-exit 0
gate live-apply approve-live-apply
op live-apply ok --gated-exit 0

op live-adopt gated --gated-exit 0
gate live-adopt approve-live-adopt
op live-adopt ok --gated-exit 0

op live-discover ok

# ------------------------------------- the refusal that guards a saved plan

# The refusal `live-apply`'s Apply step can raise: `apply <planfile>` on a
# live root re-plans against the live system and refuses at exit 3 when the
# fresh plan and the approved one disagree. This is the pair the Op runs
# (`plan -out=chant.tfplan`, then `apply chant.tfplan`), driven by hand so
# the tamper can land between them.
step "the tamper: a renamed resource between plan -out and apply"
cd "$WORK/repo/terraform" || die "no terraform root in the scratch repository"
choudoufu init -input=false -no-color > "$OUT/tamper-init.log" 2>&1 || die "init failed in the tamper"
choudoufu plan -input=false -no-color -out=approved.tfplan > "$OUT/tamper-plan.log" 2>&1 \
  || die "plan -out failed: $(tail -5 "$OUT/tamper-plan.log")"
cp main.tf main.tf.orig
if [ "${BREAK:-0}" = "1" ]; then
  log "BREAK=1: not tampering, so the refusal assertion has to fail"
else
  sed -e 's/^resource "aws_iam_role" "app" {/resource "aws_iam_role" "app_renamed" {/' \
      main.tf.orig > main.tf
fi
choudoufu apply -input=false -no-color approved.tfplan > "$OUT/tamper-apply.log" 2>&1
TAMPER_RC=$?
mv main.tf.orig main.tf
if grep -q "The approved plan no longer matches the live system" "$OUT/tamper-apply.log" \
   && [ "$TAMPER_RC" = "3" ]; then
  verdict apply-refusal exit3 exit3
else
  verdict apply-refusal exit3 "exit$TAMPER_RC"
fi
sed -n '/Error: The approved plan/,$p' "$OUT/tamper-apply.log" | head -20 | sed 's/^/    /'
rm -f approved.tfplan
cd "$WORK/repo" || die "cannot re-enter the scratch repository"

# ------------------------------------------------- issue #980's warnings

step "#980: the \"no orphan recovery\" warnings"
ORPHAN="$(cat "$OUT"/*.log | grep -c "no orphan recovery" || true)"
PER_RUN="$(grep -c "no orphan recovery" "$OUT/live-plan.ok.log" || true)"
echo "SMOKE op=orphan-warnings verdict=measured count=$ORPHAN per-live-plan=$PER_RUN"
grep -h -m2 "no orphan recovery" "$OUT"/*.log | sed 's/^/    /' || true

# --------------------------------------------------------------- the tally

step "tally"
echo "SMOKE total pass=$PASSES fail=$FAILURES"
[ "$FAILURES" = "0" ] || exit 1
