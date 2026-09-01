# full
# The comprehensive harness: every step of live/e2e/run.sh (greenfield,
# adoption, drift, foreign protection, removal, count scaling, rename,
# receipts, reconvergence, and every lint fixture), driven end to end.
# Roughly six minutes.

step "delegating to live/e2e/run.sh --expect 5"
TOFU_BIN="$TOFU" FLOCI_PORT="${FLOCI_PORT}" bash "$ROOT/live/e2e/run.sh" --expect 5 \
  || fail "full" "the comprehensive harness reported a failure - its own output above names the step"
