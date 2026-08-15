# Handoff

Written 2026-08-15 evening, at `7ee71441ae` plus this file. Working tree
clean, `just ci` green, everything pushed, nothing in flight.

This is a session handoff, not a second work queue. The work lives in the
issue tracker; the operational brief is `.claude/agents/live-markers.md`.
If you find yourself copying issue content into here, stop.

## What landed this session

Closed: #132 (extraction sweep 638→708 adopted, then the emit gate plus a
135-ruling ledger with exits and two down-only ratchets), #139 (COVERAGE's
layers table is a generated span; `live/rowgen-buckets.json` is new),
#147 (the published-deployment corpus population; first honest rate: 144
of 145 real deployment roots blocked), #149 (acceptance re-measured, split
final at 26 emulator / 2 fixture), #162 (all six specs done).

Filed: #172 (ARN/URL-template proposal shape), #173 (unwired sibling
references in estate-gen), #174 (schema-optional API-required members).

Maintainer rulings, all recorded on the issues: #136's retirement bar
stays byte-identical; #147 is option 1; #154 goes strictly last; #170
stays untouched; full-tree verification is batched to the push boundary
(targeted tests per landing, one fix pass before pushing).

## Where #136 stands

410 override entries remain (batches 2 and 3 retired 17, measured, plus
the incomplete-may-replace-a-placeholder seed rule). The complete
classification of the 410 by blocking cause is on the issue; the ceiling
for the doc-example line is roughly 180 types, and 141 are cross-resource
wiring that is permanently hand per the issue's own scope. The kept
measured-retirable override (`aws_dms_s3_endpoint`) is a deliberate
ruling - read its entry before retiring it.

An untracked measurement harness sits at
`tools/estate-gen/retire_measure_test.go` (env-gated:
`ESTATE_RETIRE_MEASURE=1` measures all overrides in ~30s;
`ESTATE_REGEN_ALL=1` bulk-regenerates every cohort through
`recordedRegenTypes`). Deliberately not committed, per the temporary-
harness convention; re-create or reuse it for the next batch.

## Things measured this session that contradict what was written down

- **The #132 issue comment's "82 extraction gaps" was 84** in the
  artifact itself. Recompute before planning against any count.
- **`grep -c FAIL log` after a green run exits 1** (zero matches), and a
  compound command's exit is then grep's. The pipe rule's sibling: read a
  verification's exit from its own `echo $?` line, never from a chained
  grep. A green tier was nearly re-run as red this way.
- **Crash cohorts' `failed_resources` lists are nondeterministic** -
  whatever is in flight when the plugin dies gets cancelled. Do not diff
  those lists as signal (recorded on #149).
- **A background acceptance run reads the working tree as it goes.** One
  was started, the fixtures changed under it, and it had to be killed and
  re-run. Sequence tree-mutating work around any live measurement.

## The acceptance conclusion, so nobody re-derives it

26 of 28 failures are emulator-attributable; the two fixture ones are
filed as rule classes (#173, #174). The cheapest large recoveries are on
lex00/floci#50: router slash normalization (may recover IoT, API Gateway
v2, Backup wholesale) and five real-Docker waiter hangs (OpenSearch,
ElastiCache, ECR repository, RDS instance, MSK). Until some of that
lands, the round-trip number (3 of 31) is a floci ceiling, not a fixture
quality measure.

## What I would pick up next

1. **#173** - highest product value: every instance also fails on real
   AWS, and the fix is the sibling-reference rule estate-gen almost has.
2. **#174** - pairs with #173; note its stated interaction with #136's
   Incomplete gate before designing.
3. **#172** - the one genuine extraction class behind the #132 ledger.
4. **#136 batches** - re-run the harness after any seed/extractor change;
   retire what newly clears the byte bar.

#170 and #154 are the maintainer's, in that order, #154 strictly last.
