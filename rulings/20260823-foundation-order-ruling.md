# Foundation Order Ruling

Date: 2026-08-23. Issues: #364 (universal record, scope widened), #365
(toggles, two added), #387 (schema-first table), #388 (the plan-node seam).

On 2026-08-23 the maintainer asked for an architecture review of how
per-resource behaviour is normalised in this fork, then ruled on the
review's proposals. This document records what was measured, what was
ruled, the order the work is to land in, and what it retires. Every figure
names the commit it was computed at. The review itself is not restated;
its measurements are summarised so the order can be checked against them
later.

## What was measured (at 14d6027d2e unless noted)

**Where per-type knowledge lives.** Type literals in control flow are
nearly gone (`live/derivation_guard_test.go` pins 437 data / 128 code
literals, almost all in e2e fixture generators). Per-type facts live in
about twenty generated dimensions (`internal/live/identity/*_generated.go`,
`internal/live/lint/*_generated.go`, `live/mapping.json`,
`live/tag-verbs.json`, and the rest) fed by generators whose fragile
parts are the hand-ratified ledger (`tools/row-gen/ratified.json`, 1023
rows; `annotations.json`, 151 rulings), the prose parser over provider
docs (`tools/importdocs-gen/parse.go`), and `tools/mapping-gen`'s
PascalCase regex with its alias overlay.

**Where the variability actually is.** Not per type: per configuration
shape. `internal/live/identity` is ~20k non-test, non-generated lines, a
second HCL evaluator restricted to what is static from `var`/`local`/
`path`/`terraform`, with `count`/`for_each` expansion reimplemented;
`internal/live/lint` adds 6.5k lines of refusals for the shapes it cannot
do; `internal/live/stamp` rewrites HCL bodies to inject markers. Of the 206
refusal kinds `check.AllRefusals()` enumerates (at 1bc96a8880), 97 are the
identity stage: 42 its own, 55 pass-through diagnostics from HCL and
`internal/configs`.

**Why.** Prior state is rebuilt before the graph walk and injected through
one seam (`internal/backend/local`'s `StatelessRun`, five upstream files),
so identity must be computable from configuration text with no values.
OpenTofu itself never needs a static subset because it evaluates in
dependency order with real values.

**The roster's physics (provider 6.59.0, `live/survey-full.json`, 1699
types).** A marker can carry the identity of 786 types; the declaration
re-derives 217 more (client-named, parent-derived, account-derived,
unique-name); 696 are untaggable and server-assigned, and nothing but a
record (or stock's state) can hold them. In the corpus (11835 `aws_*`
declarations) the genuinely record-only types people write are few and
known: `aws_iam_access_key`, `aws_vpc_ipv4_cidr_block_association`,
`aws_iam_user_ssh_key`, `aws_secretsmanager_secret_version`,
`aws_eip_association`, `aws_efs_mount_target`, `aws_kms_grant`,
`aws_sns_topic_subscription`, `aws_cloudfront_origin_access_control`,
`aws_cognito_user_pool_client`, `aws_api_gateway_deployment`,
`aws_lambda_layer_version`. Ten of the twelve are already on the
record-located route (`identity.LocatedType`); two are still behind
`unadmitted-type`; one is behind a hand ruling (below).

**What the schema can replace.** Of the 575 config-identified table rows,
414 have no provider identity schema; of the 161 that do, 136 are
reproducible from it by same-name mapping and 25 are not (ARN-shaped
identities assembled from region/account/name, optional trailing
segments, any-of arguments). Schema-first shrinks the ledger by ~136 rows
today and grows only as hashicorp/aws ships identity schemas (449 wire
schemas of 1699 at 6.59.0).

**The promise population (`live/gauntlet.json` at 1bc96a8880).** 25
estates, 19 clear, 6 failing at `test_plan`. From the migrate summary
lines (free text, approximate), roughly 360 instances received a marker
and roughly 225 were skipped as untaggable, so about 40% of the migrated
population is re-derived from configuration on every plan; by the recorded
details, five of the six failures are in that path, on identities the
state file held at migrate time. The orchestrator's own wind-down note of
the same night: two thirds of merged units did not move a bar, and the
three that did were walls recorded as external that were not, found by
reading the API directly. Both the recorded details and that note are
leads, and the first action on each failing estate is to re-read it
against the API on the current image.

**ec2.** terraform-aws-modules/ec2-instance, the most-downloaded module on
the registry, was in neither the corpus nor the gauntlet. `aws_instance`
is on the marker path and declared in 41 corpus directories; the gap is in
the measurement set.

## The rulings

1. **Record-primary identity.** `live-import` and every apply record every
   instance's identity in the estate's record store; a plan reads the
   record first and verifies it against the marker; the marker sweep and
   static resolution become the recovery paths (no record, or record and
   marker disagree). The marker stays authoritative for ownership wherever
   a tag can exist; a record is never read as permission to delete (#270).
   This is #364 with one word changed: every instance, not every instance
   that cannot be derived.

2. **Schema-first table.** Where the provider's identity schema reproduces
   a ratified row, the schema wins and the row is dropped from the ledger;
   the ledger keeps only what the schema cannot say. Cleanup; no bar moves.

3. **The plan-node seam.** Identity is resolved at
   `NodePlannableResourceInstance` (`internal/tofu/node_resource_plan_instance.go`,
   the branch that already chooses `importState` or
   `readResourceInstanceState`) from record, then marker index, then the
   provider identity schema over the *evaluated* configuration; markers
   are set on the evaluated tags value at the same node. One hook inside
   the engine, two uses. The static evaluator and the HCL-rewriting stamp
   retire when the gauntlet holds without them. **The "engine untouched"
   property is a cost to weigh, not a rule**; treating it as a rule early
   was too strong, and it is retired as of this ruling.

4. **No-source instances are configurable, default refuse.** When an
   instance has no record, no marker and an identity nothing can derive,
   the default is today's refusal; a toggle in the `live` schema (#365)
   selects stock's behaviour of planning a create. The toggle is documented
   with the others: default, doc string, fixture.

5. **`aws_iam_access_key` moves under `strict { secrets }`.** The hand
   exclusion in `internal/live/identity/located.go`
   (`sanctionedCredentialExclusion`) predates the compatible-by-default
   decision of 2026-08-21. Stored by default, the way stock stores it;
   refused under `strict { secrets = "refuse" }`. The same applies to the
   other entry in that map unless a separate ruling says otherwise.

6. **ec2-instance joins the core set.** terraform-aws-modules/ec2-instance
   `examples/complete`, pinned by tag, with the standard core reason. The
   bars drop until it clears; that is the point.

## The order

Units continue from `go run ./tools/gauntlet next` throughout; the items
below are foundation units, landed by workers once their issue names files
and changes, in this sequence:

1. #364 + ruling 1 (record-primary). Write side first, then read side.
2. Ruling 2 (schema-first table). Independent; may run in parallel.
3. Ruling 3 (the plan-node seam), after 1 holds in the gauntlet, because 1
   takes 40% of the migrated population off the path 3 replaces, and 3's
   identity chain starts with 1's record.
4. Rulings 4 and 5 ride on #365 (toggles) and land with it or before.
5. Ruling 6 is a manifest entry and lands with this document.

## What this retires

- "The engine is unmodified" as a design rule (`rulings/20260814-projection-nativeness-audit.md`
  measured it; it remains a measured cost, not a constraint).
- The hand exclusion of `aws_iam_access_key` outside the toggles.
- Treating the config-language subset as a permanent property of the
  mode: it is a property of the static evaluator, which ruling 3 retires.

## What this does not decide

- The record's single shape after #364's collapse (which namespace carries
  the identity, how residue, taint and deposed ride with it). That is
  #364's design, and the implementer writes it in the issue before coding.
- Whether ruling 3 happens at all if the gauntlet clears without it. The
  order says after 1 holds; the maintainer decides then.

## Where the work is filed

- #364: scope widened to every instance; the write and read halves named
  (comment of 2026-08-23).
- #365: rulings 4 and 5 added as toggles (comment of 2026-08-23).
- #387: schema-first table, with the named changes and the acceptance
  criterion (the identity golden does not move).
- #388: the plan-node seam, with the hook point, the two uses and the
  retirement rule.
- Ruling 6 landed with this document: `corpus-ec2-instance-complete` in
  `live/gauntlet/estates.json` (core, v6.4.0) and its fetch in
  `live/corpus-manifest.json`; `go run ./tools/gauntlet next` names it
  once the six open `test_plan` units are ahead of it.

`bash scripts/pickup.sh` lists the open ones under "tracker".
