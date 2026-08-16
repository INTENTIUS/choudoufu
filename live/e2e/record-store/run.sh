#!/usr/bin/env bash
set -euo pipefail

# GitHub issue #73's record-backed behavioral e2e: the LOCAL store variant.
#
# Unlike live/e2e/run.sh, this fixture needs no AWS emulator at all - the
# four RECORD_ADMITTED types it exercises (null_resource, terraform_data,
# time_static, random_pet) are all cloud-free providers, so the entire
# create / clean re-plan / trigger-a-replace / destroy lifecycle runs
# against a plain local directory. Nothing here is simulated: every step is
# a real `choudoufu` binary built from this checkout, talking to the real
# hashicorp/null, hashicorp/time and hashicorp/random provider plugins (and
# the builtin terraform provider, for terraform_data) fetched from the
# registry.
#
#   bash live/e2e/record-store/run.sh
#
# Env overrides:
#   TOFU_BIN   path to a prebuilt choudoufu binary; skips the `go build`.
#
# Exit codes: 0 on a real pass; non-zero on a real failure. This script
# reads actual command output, exit codes, and the persisted record files'
# content at every step - never a timeout - to decide pass or fail.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE_SRC="$ROOT/live/e2e/record-store"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# ── 0. choudoufu binary ─────────────────────────────────────────────────────
log "=== 0. choudoufu binary ==="
if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU from $ROOT"
fi

# ── 1. copy the fixture into a scratch working copy ─────────────────────────
log "=== 1. copying the fixture ==="
COPY="$WORK/estate"
mkdir -p "$COPY"
cp "$FIXTURE_SRC/main.tf" "$COPY/main.tf"
cd "$COPY"

RECORDS_DIR="$COPY/.tofu-records"

# The store directory holds two disjoint namespaces, and only one of them is
# resource records. RecordKeyPrefix (internal/live/projection) puts every
# resource record under "tofu-records/<estate>/...", while HintKey
# (internal/live/projection/hint_store.go) puts guided discovery's cached
# type roster under "tofu-hints/<estate>/guided" - deliberately disjoint, so
# that orphan discovery listing the record namespace can never mistake the
# hint for a resource. count_records counts only the first namespace. A
# whole-directory count used to give the same number and silently stopped
# doing so once guided discovery started writing its hint into the same
# store, which is why assert_hint_not_a_record below checks the split rather
# than leaving it assumed.
count_records() {
  find "$RECORDS_DIR/tofu-records" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' '
}

count_all_store_files() {
  find "$RECORDS_DIR" -type f ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | wc -l | tr -d ' '
}

# assert_hint_not_a_record proves the namespace split is real rather than
# assumed: whatever else is in the store, nothing under tofu-hints/ is
# counted as a resource record, and nothing under tofu-records/ is a hint.
assert_hint_not_a_record() {
  local hints resources
  hints="$(find "$RECORDS_DIR/tofu-hints" -type f 2>/dev/null | wc -l | tr -d ' ')"
  resources="$(count_records)"
  if [ "$hints" != "0" ] && [ "$resources" != "$(( $(count_all_store_files) - hints ))" ]; then
    find "$RECORDS_DIR" -type f
    fail "the record and hint namespaces overlap: $resources resource records, $hints hints, $(count_all_store_files) files in total"
  fi
}

# record_file finds the one persisted record file whose path contains
# /<type>/, e.g. record_file random_pet. RecordKey (internal/live/projection)
# names each record "<prefix>/<type>/<hash>", so the type name is always a
# whole path segment.
record_file() {
  find "$RECORDS_DIR/tofu-records" -type f -path "*/$1/*" ! -name '*.lock' ! -name '*.tmp-*' 2>/dev/null | head -n1
}

# ── 2. init ──────────────────────────────────────────────────────────────
log "=== 2. init ==="
INIT_OUT="$("$TOFU" init -input=false -no-color 2>&1)" || {
  printf '%s\n' "$INIT_OUT"
  fail "init failed"
}
log "  init ok"

# ── 3. apply (create) ───────────────────────────────────────────────────
log "=== 3. apply (create) ==="
APPLY_OUT="$("$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$APPLY_OUT"
  fail "create apply failed"
}
printf '%s\n' "$APPLY_OUT" | grep -q "4 added" || {
  printf '%s\n' "$APPLY_OUT"
  fail "create apply did not report 4 added (all four record-backed resources)"
}
log "  apply ok: $(printf '%s\n' "$APPLY_OUT" | grep -E 'Apply complete')"

[ -d "$RECORDS_DIR" ] || fail "no .tofu-records directory was written by the apply"
N="$(count_records)"
[ "$N" = "4" ] || fail "expected 4 persisted records after create, found $N under $RECORDS_DIR/tofu-records"
assert_hint_not_a_record
log "  4 records persisted under $RECORDS_DIR/tofu-records ($(count_all_store_files) files in the store in all, the rest being guided discovery's hint)"

PET_FILE1="$(record_file random_pet)"
TIME_FILE1="$(record_file time_static)"
NULL_FILE1="$(record_file null_resource)"
TFDATA_FILE1="$(record_file terraform_data)"
for f in "$PET_FILE1" "$TIME_FILE1" "$NULL_FILE1" "$TFDATA_FILE1"; do
  [ -n "$f" ] && [ -f "$f" ] || fail "expected a record file, found none (got '$f')"
done
PET_CONTENT1="$(cat "$PET_FILE1")"
TIME_CONTENT1="$(cat "$TIME_FILE1")"
NULL_CONTENT1="$(cat "$NULL_FILE1")"

# ── 4. clean re-plan ────────────────────────────────────────────────────
# The claim this step proves: hydration read the prior state back from the
# record store correctly enough that a plan against unchanged configuration
# proposes nothing at all - not "re-create because nothing was found",
# which is what a hydration bug looks like.
log "=== 4. clean re-plan ==="
set +e
PLAN_OUT="$("$TOFU" plan -input=false -no-color -detailed-exitcode 2>&1)"
PLAN_RC=$?
set -e
if [ "$PLAN_RC" != "0" ]; then
  printf '%s\n' "$PLAN_OUT"
  fail "clean re-plan reported changes (exit $PLAN_RC), want exit 0 (no changes)"
fi
printf '%s\n' "$PLAN_OUT" | grep -q "No changes" || {
  printf '%s\n' "$PLAN_OUT"
  fail "clean re-plan output does not say No changes"
}
log "  clean re-plan: no changes, as expected"

N="$(count_records)"
[ "$N" = "4" ] || fail "record count changed across a plan (no apply ran): now $N, want 4"

# ── 5. trigger a change that forces a replace ───────────────────────────
log "=== 5. trigger a replace ==="
REPLAN_OUT="$("$TOFU" plan -input=false -no-color -var trigger_input=v2 -var replace_trigger=v2 2>&1)" || {
  printf '%s\n' "$REPLAN_OUT"
  fail "replace plan failed"
}
printf '%s\n' "$REPLAN_OUT" | grep -q "forces replacement" || {
  printf '%s\n' "$REPLAN_OUT"
  fail "replace plan does not show a forces-replacement change"
}
log "  replace plan shows the expected forced replacement"

REAPPLY_OUT="$("$TOFU" apply -input=false -auto-approve -no-color -var trigger_input=v2 -var replace_trigger=v2 2>&1)" || {
  printf '%s\n' "$REAPPLY_OUT"
  fail "replace apply failed"
}
printf '%s\n' "$REAPPLY_OUT" | grep -qE '[1-9][0-9]* added, 0 changed, [1-9][0-9]* destroyed' || {
  printf '%s\n' "$REAPPLY_OUT"
  fail "replace apply did not report the expected added/destroyed counts"
}
log "  replace apply ok: $(printf '%s\n' "$REAPPLY_OUT" | grep -E 'Apply complete')"

N="$(count_records)"
[ "$N" = "4" ] || fail "expected 4 persisted records after replace, found $N"

PET_FILE2="$(record_file random_pet)"
TIME_FILE2="$(record_file time_static)"
NULL_FILE2="$(record_file null_resource)"
PET_CONTENT2="$(cat "$PET_FILE2")"
TIME_CONTENT2="$(cat "$TIME_FILE2")"
NULL_CONTENT2="$(cat "$NULL_FILE2")"

[ "$PET_CONTENT2" = "$PET_CONTENT1" ] || fail "random_pet's persisted record changed despite nothing touching it"
[ "$TIME_CONTENT2" = "$TIME_CONTENT1" ] || fail "time_static's persisted record changed despite nothing touching it"
[ "$NULL_CONTENT2" != "$NULL_CONTENT1" ] || fail "null_resource's persisted record is unchanged despite its triggers changing and forcing a replace"
log "  untouched records (random_pet, time_static) are byte-identical; the replaced one (null_resource) changed"

# ── 6. destroy, by removing the resource blocks and re-applying ─────────
# `choudoufu destroy` (-destroy planning mode) is refused outright under
# live resource markers today (statelessRejections, internal/command/
# live_mode.go): "removing a resource from the configuration is the tested
# way to have it destroyed." So that is what this step does, exactly the
# way live/e2e/run.sh's own removal steps do: emptying the resource blocks
# out of the configuration and applying is a normal plan proposing an
# ordinary delete of every orphaned instance, record-backed or not.
log "=== 6. destroy (remove the resource blocks, apply) ==="
cat > main.tf <<'EOF'
terraform {
  live {
    estate = "record-store-e2e"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}
EOF

DESTROY_OUT="$("$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$DESTROY_OUT"
  fail "the removal apply failed"
}
printf '%s\n' "$DESTROY_OUT" | grep -q "4 destroyed" || {
  printf '%s\n' "$DESTROY_OUT"
  fail "the removal apply did not report 4 destroyed"
}
log "  removal apply ok: $(printf '%s\n' "$DESTROY_OUT" | grep -E 'Apply complete')"

N="$(count_records)"
[ "$N" = "0" ] || fail "expected 0 persisted records after destroy, found $N still under $RECORDS_DIR/tofu-records"
assert_hint_not_a_record
log "  all 4 records removed from the store"

log "=== PASS ==="
