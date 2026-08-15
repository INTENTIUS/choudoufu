# Handoff

Written 2026-08-15 night, at `a837574ed9`. Working tree clean, `just ci`
green, everything pushed, nothing in flight.

This is a session handoff, not a second work queue. The work lives in the
issue tracker; the operational brief is `.claude/agents/live-markers.md`.
If you find yourself copying issue content into here, stop.

## What landed this session

Two runs. The first closed #132, #139, #147, #149, #162 and filed
#172-#175. The second closed #172, #173, #174, #176 and filed #177:
six rule-level generator capabilities (sibling references with
identity-bound discipline, assembled-template proposals, per-segment ID
attribution, instance-count block seeding, the CFN required channel,
doc-derived ImportSyntax), five override-retirement batches (427 -> 382,
all at the byte bar), convergence 638 -> 715 with the ledger at 128
rulings each carrying its exit, the onboarding ladder regenerating
inside the corpus artifact, and live-check's one-edit verdict.

Maintainer rulings, all recorded on the issues: #136's retirement bar
stays byte-identical; #147 is option 1; #154 goes strictly last; #170
stays untouched; full-tree verification is batched to the push boundary
(targeted tests per landing, one fix pass before pushing).

## Where #136 stands

382 override entries remain. The classification of the residue by
blocking cause is on the issue; the next levers are #177's three
extraction gaps, each of which unblocks named deferred instances from
#174. The kept measured-retirable override (`aws_dms_s3_endpoint`, kept
three times now) is a deliberate ruling - read its entry before retiring
it. The untracked harness at `tools/estate-gen/retire_measure_test.go`
(ESTATE_RETIRE_MEASURE=1 / ESTATE_REGEN_ALL=1) is the batch loop;
`tools/corpus-gen/blockers_scratch_test.go` is retired - the ladder now
regenerates inside `live/corpus-refusals.json`.

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

1. **#175's rulings** - the ratification evidence review is on the
   issue: ~16 clean approvals, the seven held pastes are unblocked now
   that #176 landed, one credential ruling, two rejected.json reversal
   decisions. Every approval moves real estates.
2. **#177** - the extraction gaps; each unblocks named #174 instances
   and further #136 retirements.
3. **lex00/floci#50** - 26 of 28 acceptance failures live there; route
   normalization and five real-Docker waiter hangs are the cheap large
   recoveries. The round-trip number is floci-bound until then.
4. **The language wall** - 114 of 145 real estates; dynamic-value-in-
   static-context alone hits 74. The biggest product number there is.

#154 strictly last, per the maintainer.
