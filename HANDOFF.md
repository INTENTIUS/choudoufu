# Handoff

Written 2026-08-15 night, at `9abd4fde9c` plus this file. Working tree
clean, `just ci` green, drift and validate tiers green, everything
pushed, nothing in flight. Read `.claude/agents/live-markers.md` before
touching anything; the work lives in the issue tracker
(`gh issue list -R INTENTIUS/choudoufu`). Do not copy issue content into
this file.

## The ruling that reframes the queue

**Type parity with stock OpenTofu is the bar** (maintainer, 2026-08-15,
now in the brief's product-frame section). Every entry in
`tools/row-gen/rejected.json`, every needs-hand-separator and
evidence-only bucket, every unadmitted type, and plan-and-create-only
schema-fallback support is debt with an obligation to build the missing
vocabulary or extraction. The one sanctioned exclusion is credential
material. "Hold the reversal" and "leave it rejected" were offered as
options and rejected as failure; do not offer them again.

## Maintainer decisions taken 2026-08-15 night, ready to execute

1. **Ratify all 23 cleanly-evidenced types.** The evidence tables (spot
   check contract steps 1-2 done per type, doc sections quoted) are in
   the #175 comment thread. The 16 PROPOSE emissions with clean evidence
   plus the 7 that were held for #176 (now safe as printed - re-run
   `-propose` and take the current output, which carries the corrected
   ImportSyntax strings). Full contract per type: paste unedited, cohort
   fixture, tests, floci probe. The five with unbuildable fixtures
   (four ssoadmin types need an Identity Center instance, s3control
   bucket policy needs a physical Outpost) are NOT exempt from parity -
   ratify their rows and record the fixture gap with its reason where
   the drift tables record gaps.
2. **aws_appstream_directory_config: rejected** on the credential
   ground (Required plaintext AD password persisted in config/state;
   aws_ivs_playback_key_pair precedent). Add the rejected.json entry
   with this reason - the one class where rejection is the correct end
   state.
3. **Reverse both ledger vetoes by doing the work they were blocked
   on.** aws_ecs_service: the rejection cites an import shape
   (`ecs-svc/DEPLOYMENTID,...`) the current 6.59.0 cached page no longer
   shows (`cluster-name/service-name`); re-derive against the pin and
   write the reversal reason into the ledger. aws_cloudwatch_event_rule:
   the blocker is table vocabulary - a literal fallback for the omitted
   default event bus (or a ruling that the provider's name-only identity
   schema is the row); build it. Parity says these are work items, not
   holds. aws_cloudwatch_event_target follows its parent as an ordinary
   composite row (NOT foldParentTypes - its target_id is the "one
   further argument" that mechanism excludes; entry shape sketched in
   the #175 thread).
4. **The floci lane is open: work `~/checkouts/floci` directly** - code,
   image build, GHCR publish to lex00/floci, and the pin bump in
   `live/floci-image` with the recorded-exception update - **but touch
   nothing of floci upstream**: no PRs, pushes, or issues to the
   upstream floci project; everything stays in the lex00 fork. The work
   queue is lex00/floci#50; the two cheap large recoveries are router
   slash-normalization (likely recovers IoT, API Gateway v2, Backup
   wholesale - real AWS normalizes doubled slashes, floci's router does
   not) and the five real-Docker waiter hangs (OpenSearch, ElastiCache,
   ECR repository, RDS instance, MSK). After a pin bump, re-run the
   acceptance tier and re-split; 26 of 28 failures are floci-side today
   and the round-trip number (3 of 31) is floci-bound until this moves.

## The work order

1. The ratification batch (decision 1) plus the two reversals
   (decision 3) - every admission moves real estates, and the
   usage-weighted demand table now regenerates inside
   `live/corpus-refusals.json` (`ladder.unadmitted_demand`).
2. #177 - three extraction gaps in importdocs-gen, each unblocking
   named #174 instances and further #136 retirements.
3. The floci lane (decision 4), which can run in parallel with 1-2 in
   agent worktrees - floci files never collide with choudoufu files.
4. The language wall: 114 of 145 real published estates are blocked by
   static-evaluability refusals (dynamic-value-in-static-context alone
   hits 74). No issue exists yet for a campaign; opening one with the
   per-refusal breakdown from the ladder profiles is the first step.
   This is the largest product number in the tree.

#154 strictly last (maintainer). Open issues: #136 (382 override
entries remain; harness below), #150 (tracker), #154, #175 (execute
decisions 1-3 against it, then it can close), #177.

## Where #136 stands

427 -> 382 entries on 2026-08-15, five batches, all at the
byte-identical bar the maintainer held. The un-tracked measurement
harness is `tools/estate-gen/retire_measure_test.go`
(`ESTATE_RETIRE_MEASURE=1` measures every override in ~30s;
`ESTATE_REGEN_ALL=1` bulk-regenerates all cohorts through
recordedRegenTypes). Re-run it after ANY seed/extractor/rule change;
retire what newly measures byte-identical. `aws_dms_s3_endpoint` has
measured retirable three times and stays: its "target" pairs with
aws_dms_endpoint's "source" (cohort-composition knowledge; ruling on
the issue). The residue classification by blocking cause is on #136.

## Session traps confirmed today, worth not relearning

- The subtest checkmark is not the verdict; grep the
  `name: pass/fail (phase ...)` line (acceptance tier).
- `grep -c FAIL log` exits 1 on a green run and poisons a compound
  command's exit; read `echo $?` from its own line.
- A background acceptance run reads the working tree as it goes; never
  mutate fixtures under a live measurement.
- Crash cohorts' failed_resources lists are nondeterministic; do not
  diff them as signal.
- An identity-bound sibling reference must land on the target's
  IdentityAttrs, and only when that identity IS the wanted value;
  assembled-identity parents pair by matching seed literals instead
  (gen.go refAttr/pairedSeedLiteral, tests pin all three shapes).
- Agents that stop "waiting on background work" have no live children;
  send them a message to finish from what they already have.

## Instruments, all regenerated and consistent at this commit

`live/corpus-refusals.json` (ladder + per-estate profiles + demand
table; the honest state: 0 clean, 11 backend-only, 18 admissions-only,
114 language-blocked, 1 unreadable of 145 real deployments),
`live/rowgen-convergence.json` (715/822 adopted, 107 mismatches all
ruled with exits, ledger 128 and down-only),
`live/cohort-acceptance.json` (3/31, split 26 emulator / 2 fixture),
`live/rowgen-buckets.json`, and the retirement report in the session
scratchpad. Recompute before quoting any of them.
