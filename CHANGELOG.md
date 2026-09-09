# choudoufu releases

choudoufu tags its own `v0.x` line on top of an upstream OpenTofu version. Both numbers appear in `choudoufu version` and in every release's notes: the fork tag, and the OpenTofu release the tree is built from. This section is the fork's changelog; upstream's own, for that OpenTofu version, follows below under "OpenTofu" and is left in upstream's shape.

**Fork work is recorded here, not in upstream's section.** An entry filed under upstream's `1.13.0 (Unreleased)` heading says "unreleased" about something that shipped, which is how four tagged releases came to have no changelog entry naming any of them. To cut a release: date the `(Unreleased)` heading below, open an empty one above it, and take the board movement from `go run ./tools/gauntlet notes live/history/<previous>.json live/history/<new>.json` against the snapshot `go run ./tools/gauntlet snapshot <version>` writes, rather than retyping a count by hand.

## choudoufu v0.17.0 (Unreleased)

Nothing recorded yet.

## choudoufu v0.16.0 (2026-09-09)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.16.0.json`](live/history/v0.16.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.15.0.json live/history/v0.16.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

Two headline stages activated, `day2_crash` and `day2_teardown` (#804,
#805, PRs #998, #1004, #1009), and they are the first stages ever
activated on tier-1 fixtures - `live/e2e/crash-interrupt/run.sh` and
`live/e2e/destroy-teardown/run.sh` - rather than on 26 hand-written
per-estate sections. What made that possible is #999's ruling: a
`not_run` verdict on a `Tier1Gated` stage is neutral for `clear` rather
than a miss, because #491 and #643 retired the sweep model that used to
supply those sections, so an estate carrying no section for such a stage
is not an estate that fails it - it is one nothing has asked. On the old
rule the flip was unaffordable, and PR #998 deliberately held it:
activating `day2_teardown` alone would have taken both bars from 27/27 to
0/27. A genuine `fail` still fails such a stage, and a genuine per-estate
`pass` still counts, which is why `day2_crash` reads 1 pass and 26
`not_run` - reference-ec2-vpc's own, a survivor of the retired model.

`BehaviorsProven` did not move: it still reads 1 of 14. Both fixtures
pass, but each is short of the identity kinds #522 makes mandatory for an
identity-touching stage - both carry `server-minted` alone, where the
ruling wants `deterministic` and `none` beside it - and `crash-interrupt`
is scalar-only where the three mandatory shapes are `count`, `for_each`
and `module-nested`. Activation cleared the tier-1 gate; it did not clear
the proof bar, and the two are deliberately separate.

For the first time the board is measured entirely against its own
declared oracle. Every row in v0.15.0's snapshot recorded terraform
1.15.8 while `live/oracle-versions.json` declared 1.16.0; all 27 rows now
record 1.16.1, which is what the pin now declares. The pin moved because
1.16.0 makes the ORACLE itself nondeterministic: hashicorp/terraform#39089,
a spurious `Error: Cycle` on an acyclic configuration, measured at 9
cycles in 20 runs of `corpus-rds-complete-postgres`'s own `day2_replace`
stock oracle plan against one frozen `cold_deploy` state, where 1.16.1
gave 0 in 20. hashicorp/terraform#39076, a planned destroy-then-create
applied create-before-destroy with the destroy silently dropped, lands at
the same commit (038c6f72, PR #38840) and is fixed by the same one (PR
#39091, 1.16.1). That is #1010, off #947 and #1005, and it is why the
previous board's 26/26 was not what it appeared: every row was a verdict
against a version the pin did not name.
`TestOraclePinIsNotAKnownBrokenRelease` now refuses a pin that walks back
onto 1.16.0.

The re-measure is PR #1011, all 27 estates on the 1.16.1 oracle, which
landed at core 25/26 and all 26/27 with `corpus-alb-complete` short; PR
#1015 repinned the emulator to floci's ELBv2 concurrency fix (#1005) and
that estate cleared, restoring 26/26 and 27/27. The snapshot's emulator
digest moves with the repin, from `sha256:a39185cc...` to
`sha256:d9207de1...`.

FORK WORK:

- **`choudoufu version -json` carries the fork tag** (#968). The fork's
  own release version was readable by a machine only out of the human
  first line (`choudoufu v0.15.0 (based on OpenTofu v1.13.0-dev)`) or out
  of `live-plan -json`, which needs a configuration and a cloud call to
  produce. `version -json` itself named only the upstream base version, as
  `terraform_version`. It now prints `choudoufu_version` beside it - the
  same key, from the same `tfversion.Fork`, that `live-plan -json` already
  carries:

  ```json
  {
    "choudoufu_version": "v0.15.0",
    "terraform_version": "1.13.0-dev",
    "platform": "darwin_arm64",
    "provider_selections": {}
  }
  ```

  On a development build the value is `""`, and the key is still written -
  `live-plan -json` made the same choice, and it is the one that matters
  to the caller this was filed for. INTENTIUS/behold checks a version
  floor before it spawns any of the four `-json` verbs, because a binary
  older than v0.14.0 answers an unknown verb with an exit code it would
  otherwise have to pattern-match. Without `omitempty` that caller can
  tell a development build (key present, empty) from a binary too old to
  have the field (key absent); with it, both read as absent and the caller
  is back to parsing the human line. `terraform_version` keeps its name
  and its meaning, so anything written against stock still reads it.

- **Every `live-plan -json` `bound[]` row carries the live identity it was
  matched on** (#967). The row's `identity` was read off the
  pre-projection `identity.Resolution`, which holds one only for the
  paths that settle an identity before anything reads the live system. So
  a marker-bound row - the one shape that was FOUND by its live identity -
  arrived as `{addr, type, source}` with no identity at all, and a reader
  (behold, chant #2104) joining a plan row to a `live-ls` item fell back
  to `addr`. Two other shapes were affected the same way: a
  parent-derived row, whose formula only renders once its parents are
  materialized, arrived with `identity_values` and no `identity`; and a
  record-located one, whose id lives in the record store by design. The
  id now comes from the projection - what the build actually imported and
  read back, recorded per address in `builder.materialize` and read
  through `projection.Result.BoundIdentity` - so `identity` is populated
  wherever a live object was bound, whichever admission path found it.
  `identity_values` is unchanged, and no other key moves.

  `identity` is now ALWAYS on the wire: it lost its `omitempty`, so a row
  with no live id renders `"identity": ""` rather than dropping the key.
  Exactly one materialize path has no live id - a record-backed instance
  (#73, `identity.ClassRecordBacked`), whose values ARE the record and
  which names no cloud object - and it says so rather than inventing one.
  Lint refuses that class today, so no run produces such a row yet;
  `TestLivePlanDocument_topLevelShapeIsPinned` carries one by hand so a
  consumer cannot come to read the key's presence as a fact about the
  estate. Proven red first: a fake-backed command test with one row per
  reachable source (`derived`, `marker`, `record`) asserting each identity
  by literal value fails on both the marker and the record row before the
  fix; `cache` is unreachable from this pipeline, which never sets
  `projection.Options.StateCache`.

  One thing found and deliberately not moved: `source` is classified from
  the PRE-sweep needs-discovery set, so an instance the estate-wide sweep
  failed to bind - a real account's tag index lags a write by minutes -
  is reported as `marker` even when #364's record-first read is what
  materialized it. The identity such a row carries is the record's and
  names the same live object; only the provenance is approximate.
  Narrowing `source` changes what an existing key means and wants its own
  issue.

## choudoufu v0.15.0 (2026-09-08)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.15.0.json`](live/history/v0.15.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.14.0.json live/history/v0.15.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The board is carried forward from v0.14.0 unchanged: its 27 rows still
date from PR #939 at 7841ac337f, before the stamp retirement (#644, PR
#944) and before the two fixes below merged. Since v0.14.0 the stamp
retirement is measured clear on `corpus-eks-basic` (Gauntlet runs
34155541362 and a local run) and `corpus-rds-complete-postgres` (run
34148414213) only; nothing has re-measured the rest, because the nightly's
verdicts PR still cannot land (#948). This release exists to ship #962 to
its consumer, not to move the board.

Live-certified: `gauntlet live-cert reference-ec2-vpc -target aws` in
us-east-2 at 4d72968cdb, all four stages pass (cold_deploy, migrate,
test_plan, test_apply), 5 objects, under the 5 USD ceiling. The verdict is
recorded in `live/gauntlet.json`'s `live_cert` row and in the snapshot.

FORK WORK:

- **The estate-wide sweep signs as each provider configuration's
  principal** (#957). The Cloud Control and Tagging clients the sweep
  builds for a provider configuration were built with no credentials at
  all, so region was the only thing read out of a provider block for
  them, and against an endpoint override they were never signed. On a
  two-account estate (claim 19) the tag index was therefore fetched once
  per pass as the same nobody, and the emulator, which resolves the
  calling account from the access key id on the wire, filed both fetches
  under its default account; the per-provider plugin legs covered for it,
  so the board stayed green and the second account's sweep was simply
  never measured. The sweep clients now resolve the block's own principal
  the way the provider does - static `access_key`/`secret_key`/`token`,
  then `profile`, with an `assume_role` block layered on either or on the
  default chain, all deferred to first use - and sign with it even against
  an endpoint override when the block names one, so the account travels
  on the wire. A block that names nothing keeps the old behaviour. The
  client's own `HTTP Request Sent` line gains `signed_as=<access key id>`
  (or `unsigned`), read back off the request's Authorization header, and
  claim 19 counts the tag-index fetches per account off it: one signed as
  each account and none unsigned, in both the bind and the recovery
  steps. Proven red against the v0.14.0 binary, which fails that step
  with "the estate-wide tag index was never fetched signed as account
  000000000000", and by a fake-backed command test whose two provider
  configurations must produce two `GetResources` calls signed as two
  different key ids ("[unsigned]" before the change).

- **`live-plan -json` carries the content match** (#962). A declared
  resource whose identity the server assigns, an `aws_vpc` say, is an
  omission (`NEEDS_DISCOVERY`) in the document, and the live object the
  estate-wide sweep matched to it by content - the row the `-adoption-only`
  human render prints under "Adoptable", with the arguments it matched on
  and the two marker values that adopt it - appeared nowhere in the
  document. INTENTIUS/chant's terraform lexicon, which proposes claims
  straight off the document, measured that on the v0.14.0 binary against
  the pinned emulator and found no way to the row: `-adoption-only` is
  refused beside `-json`, and `TOFU_LIVE_COLLECT_UNCLAIMED=1` ran the sweep
  and dropped its answer. The document now has two more top-level sections.
  `adoptable` is one row per content match: `addr`, `type`, `identity`,
  `matched` (a list of `attribute`/`value` pairs, the "matched on:" line),
  `adopt_tofu_estate`, `adopt_tofu_address`, and the same `adopt_command`
  the human render prints. `swept` names every resource type the sweep
  listed in full, so an empty `adoptable` reads as "looked and found
  nothing" when the declared type is in `swept` and as "this run did not
  ask" when `swept` is empty; the run asks under `-adoption-only` or
  `TOFU_LIVE_COLLECT_UNCLAIMED=1`, as before. `unowned` is unchanged and
  disjoint from it: an object read at an identity the configuration
  declares, against one the sweep found for a declaration that declares
  none. `TestLivePlanDocument_topLevelShapeIsPinned` pins the new shape by
  value; chant #2168 is the consumer told.

## choudoufu v0.14.0 (2026-09-07)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.14.0.json`](live/history/v0.14.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.13.0.json live/history/v0.14.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The headline counts did not move, but the board was re-measured rather than
carried forward. Stage 12, plan approval, went from planned to active (#888,
PR #903), which took both bars to 0/26 and 0/27 until every estate had been
re-run carrying the new leg; the catch-up landed in PRs #923, #925, #926,
#927 and #931. All 27 rows now carry a 2026-09-07 run, and `plan_approval`
reads 27/27 pass where v0.13.0's snapshot read 27/27 `not_run`.

Those 27 rows were measured at 7841ac337f (PR #939), before the stamp and
`module_prefix` retirement (#644, PR #944) merged, so the board carries
that change forward unmeasured on 25 estates. It is measured on two: after
#944, `corpus-eks-basic` reads clear on the runner (Gauntlet run
34155541362) and on a laptop, and `corpus-rds-complete-postgres` reads clear
on the runner (run 34148414213). The nightly that would have re-measured
the rest cannot land its verdicts yet (#948).

Live-certified: `gauntlet live-cert reference-ec2-vpc -target aws` in
us-east-2 at e6a9698161, all four stages pass (cold_deploy, migrate,
test_plan, test_apply), 5 objects, under the 5 USD ceiling. The verdict is
recorded in `live/gauntlet.json`'s `live_cert` row and in the snapshot.

ENGINE WORK:

- **A tombstone records only what this estate's own apply destroyed** (#854,
  PR #900; #908, PR #913; #901, PR #920; #938, PR #943). v0.13.0's replace
  tombstone was written from one fact about the record: its identity changed
  at an address the final state still has. An `import` block pointing an
  address at a second live object, a `live-mv` onto an address that already
  held a record, and a `lifecycle.destroy = false` create all produce that
  same fact and destroy nothing, so a displaced object still wearing the old
  marker was pruned as "destroyed by an earlier apply of this estate" while
  it was running. The signal now comes from the plan: `StatelessRun.WriteBack`
  is handed the addresses whose action is `DeleteThenCreate` or
  `CreateThenDelete`, an entry is written only when the plan names the
  address and the identity moved, and import and live-mv write nothing, so a
  displaced live object is refused as a collision rather than described as
  destroyed. The superseded-claimant detail now says which cases write an
  entry and which do not.

  That plumbing shipped inert. `replacedInstances(plan)` was evaluated after
  `Core.Apply` had drained the applied changes out of `plan.Changes`, so the
  replace set reaching the write side was empty on every real run
  (`BEFORE-APPLY replacedInstances=[aws_instance.web]`, then `AFTER-APPLY
  replacedInstances=[]`), no replace recorded a tombstone, and one ForceNew
  replace blocked the estate on its next plan. The set is now read before the
  apply goroutine starts, and `TestWriteBackSeesTheReplaceSetAfterApply`
  observes the value the call site actually passes after a real apply rather
  than the function against a synthetic plan nothing drains.

  Two shapes on the deposed path were then settled. A `create_before_destroy`
  replace whose destroy leg fails leaves the old object deposed and alive
  while the plan still says `CreateThenDelete`; the write side now asks the
  address's final state whether the superseded identity may still be one of
  its deposed objects and writes no entry when it may. The recovery apply
  that later destroys that deposed object schedules a deposed destroy, not a
  replace, so neither fact held and the identity this estate terminated was
  recorded nowhere; its lingering tags then made it a second claimant, and
  `reference-ec2-vpc`'s `day2_crash` refused with `Two live resources
  claiming one address`. `WriteBack` now also takes the plan's deposed-key
  `Delete`s and writes the entry once the key has left the final state's
  deposed set. `day2_crash` moved from `verdict=fail` (`the post-recovery
  plan exited 1`) to `verdict=pass` with the other twelve stages unchanged.
  `pruneSupersededEntry`'s deposed-over-tombstone ordering stays as the
  compatibility leg for records an older build wrote, and is now pinned by a
  case that sets both for one object.

- **An ordinary ForceNew replace is not a displaced marker** (#885, PR #902).
  `displacedFrom` compared the identity the configuration computes for a
  declared address against the one the cloud attached to the live object
  wearing that address's marker, and reported any mismatch as `Live resource
  displaced from the address it is marked for`. Those two strings also differ
  in the plan before a ForceNew replace, so on
  `corpus-giantswarm-crossplane`'s `day2_replace` the warning asserted "Two
  different live resources therefore answer to one address" with exactly one
  role in the account, and "Nothing is proposed for this resource" directly
  beneath the same plan's own `must be replaced` line. The estate's own
  current-identity record now tells the two apart: a record naming this
  object is the estate's statement that the address still owns it, and the
  mismatch is a pending change the ordinary diff owns. That verdict is silent
  and hands out no cache vouch. A record naming a different object, no
  record, an unreadable record and no record store at all warn exactly as
  before, and the `BREAK=replace` control still refuses by name. The
  warning's detail no longer describes the plan, which a scan cannot see;
  `TestOwnershipAddress_forceNewReplaceIsNotDisplacement` and
  `TestOwnershipAddress_displacedDetailDoesNotDescribeThePlan` were both red
  against the old sources.

- **A multi-provider estate keeps the tag-index vouch for client-named
  instances** (#905, PR #912). `discovery.Merge` concatenated every other
  field a pass produces and dropped `VerifiedDeclared`, #692's vouch for a
  CONCRETE declared instance. `MarkerVerified()` is built from it, so in any
  estate with more than one provider configuration the `-refresh=false`
  state-cache hit could never fire for a client-named instance;
  needs-discovery instances were unaffected because their vouch rides
  `Bindings`. A straight append is sound because a declared address belongs
  to exactly one resource block and so to one provider configuration, and a
  repeated entry from a global service's sweep collapses in the address-keyed
  map. Measured on claim 16's two-region estate against the pinned emulator:
  with the fix reverted, 2 of 5 instances were served from the cache and
  per-pass requests read `aws.east (us-east-1) 43, aws.west (us-west-2) 15`;
  restored, all 5 hit and the requests read `26` and `13`. The scenario's
  step 2 now asserts the CONCRETE log group's cache hit beside the VPC's.

- **A region change refuses instead of abandoning the old region's marked
  object** (#906, PR #914). Repointing a block from `aws.west` to `aws.east`
  planned the create in us-east-1 and said nothing about the VPC still in
  us-west-2 carrying `tofu-estate` and `tofu-address` for that address, and
  the coverage line beside the create promised "Marker discovery will find
  it", which cannot come true because discovery for that address now lists
  the region the object is not in. The sighting classifiers now file which
  provider configuration saw a marked object, and `Merge` refuses every
  address whose object was sighted only by configurations that do not declare
  it, with `Marked resource outside its address's provider configuration`,
  naming the object, both regions and three remedies. An object also sighted
  by its own pass (an account-global list such as IAM or S3) is untouched,
  and a single-provider estate cannot produce the finding. Per the 2026-09-06
  ruling the refusal sits behind a fourth `strict` toggle, `provider_change =
  "refuse" | "recreate"`, pinnable at `"refuse"`; under `"recreate"` the plan
  proceeds as stock does and warns, `Marked resource abandoned by a provider
  configuration change`, while the coverage line reads "Marker discovery will
  NOT find it". Claim 16's step 5 now asserts the refusal by value, and
  `TestLivePlan_needsDiscoveryDoesNotBindAcrossProviders`, which had asserted
  the abandoning create since #283, is split into a refusing default arm and a
  `"recreate"` arm carrying its original assertions.

- **One live object seen by two enumeration legs is one claimant** (#928, PR
  #935). Three legs file claimants on a declared instance and only
  `Discover`'s own scan loop deduplicated; the Cloud Control scan and the
  estate-wide tagging sweep appended unconditionally. A declared type in a
  service the Resource Groups Tagging API does not index (#692) and that the
  provider cannot list natively (#881) is scanned by both, so
  `corpus-overture-tiles`'s `day2_rename` refused a moved block with `Two
  live resources claiming one address` and printed the one instance
  profile's identity twice as both sides of the collision. The guard now
  lives on the entry as `addClaimant`, keyed by import identity, and all
  three legs go through it; a claimant with no identity is never
  deduplicated. Measured against hashicorp/aws 6.59.0 the exact routing
  reaches 2 admitted types today (`aws_iam_instance_profile` and
  `aws_iam_service_linked_role`) and the guard covers all 1027
  cloud-observable admitted types.
  `TestTwoDifferentLiveObjectsAcrossTheTwoLegsStillCollide` is the control:
  two different objects at one address still refuse and the refusal still
  names both. The stage reads `verdict=pass` with 0 add, 0 destroy and 16 tag
  rewrites; the board already recorded the row clear, and it is now true.

- **The HCL-rewriting stamp and `module_prefix` are retired** (#644, PR
  #944). `internal/live/stamp` still carried the pass that wrote markers by
  rewriting configuration bodies, dead on the default path since
  `CHOUDOUFU_NODE_RESOLVE` defaulted on (#451, 2026-08-25), and
  `${tofu.marker_module_prefix}` existed for that rewrite alone, because
  several instances of one module call share one `*hclsyntax.Body`. The
  ruling on #644 was to delete rather than split: `stamp.go` (2633 lines),
  `perinstance.go`, `sharedbody.go` and the 21 test files that drove them go,
  along with `markers.ModulePrefix*`, the `GetTerraformAttr` arms in
  `internal/configs/static_scope.go` and `internal/tofu/evaluate.go`,
  `StaticEvaluator.WithModuleInstance`, lint's `RuleReservedSymbol` and its
  fixture. The package goes from 15,758 lines across 62 files to 5,593 across
  39; the branch is 929 insertions and 12,109 deletions over 59 files. The
  static evaluator stays, as HANDOFF item 3 says it must.

  Five of LayerStamp's eight refusals retire with the mechanism that raised
  them; `Ownership marker conflict`, `Ownership markers not stamped` and
  `Unmarked apply of a marker-only resource` stay, and `live/LIMITATIONS.md`
  goes from 223 refusals to 215 and 28 lint rules to 27. The deletion forced
  one fix: with the rewrite gone, `CHOUDOUFU_NODE_RESOLVE=0` would have run
  with no marker writer at all and created every resource unmarked, silently.
  `NodeResolver` is now installed as `tofu.ConfigValueAdjuster` on every run
  and the flag governs only identity resolution; `TestLivePlan_identityFatal`
  caught the one call site that had used the resolver's presence as a stand-in
  for the flag. Two things this removes are named rather than dropped:
  `declared_tagged = "untag"`'s marker suppression, inert since 08-25 and
  still without a node-path port (`live_policy.go` carries the note), and the
  plan-time `Unstamped marker-only resource` error, which `live-check` still
  reports offline at the same 55 sites.

  The refusal registry was re-measured with `refusal-probe -schemas
  -allow-partial-corpus` before and after, per entry: 8247 sites, 4632
  instances, 193 blocked over 228 corpus entries, and not one of the 27
  entries moved. The issue's older baseline (10363, 4912, 203) does not
  reproduce because the corpus manifest has moved since, which is why the new
  baseline was taken on the unmodified tree first. The eight registry entries
  deleted here measured zero sites before the change. The core-set gauntlet
  was not re-run for this change; the board line is whatever the release
  re-measure records.

- **One config-subset evaluator, in `internal/live/staticeval`** (#826, PR
  #934). Six packages each carried their own copy of the "what can be
  evaluated statically" subset: identity's `evalPure` and `isSymbolic`,
  lint's `staticCount` and `staticForEachKeys`, dataread's `staticEvalExpr`,
  discovery's `staticArgumentValue` and foreign's `staticString`, about 290
  lines together, with nothing in the tree comparing what they accepted, and
  two doc comments arguing for the copies on import-cycle grounds. The new
  package imports nothing under `internal/live` itself (`markerkey` and
  `markers` arrive only through `configs`, checked with `go list -deps`), so
  no consumer can cycle back into it. It exports `Allowed`, `Evaluable`,
  `FirstDisallowed`, `Evaluate`, `EvaluateOK`, `Scoped`, `Count`,
  `ForEachKeys` and `Argument`; all six copies and five more instances of the
  same five-root switch inside identity now call it, with each behavioural
  difference kept explicit rather than averaged away. `Allowed` (the five
  roots the static scope answers) and `Evaluable` (those plus `count`,
  `module`, `data`, `self`) stay two predicates because only identity's
  symbolic check wants the second, and one refusal sentence in discovery
  gains an "a" so foreign's stays byte-identical.

  The identity golden did not move: 1810 rendered identities across 654
  configuration directories. The allowlist is pinned by value and the recover
  was proved load-bearing by deleting it and watching the test process crash.
  One finding is left as its own change: `Argument` has no recover because
  neither copy it replaced had one, so discovery's content match and
  foreign's classification can still crash on an ancestor's `each.key`
  reaching through a `local.*`, where lint, dataread and identity degrade to
  a refusal. stamp's five copies were not migrated; they died with #644.

- **Every package that branches on `identity.Class` has a handler table and
  an exhaustiveness guard** (#810, PRs #932 and #937). Adding a class was a
  change across 34 non-test files that nothing failed loudly on; the gauntlet
  found the misses. `internal/live/identity/classes.go` now exports
  `AllClasses()` and `ClassTableGaps`, with `TestAllClassesMatchesTheConstBlock`
  parsing `identity.go`'s const block so the list cannot drift from the
  declarations. Each consuming package has a `classes.go`
  (`internal/command`'s is `live_classes.go`) holding one
  `map[identity.Class]handler` with a field per decision and the old branch
  bodies moved verbatim: projection (4 sites), command (5), mv (3),
  liveimport (1) and discovery (12 comparisons at 10 sites across 7 files),
  each with a `TestClassTableIsTotal` that was proved red by deleting a row.
  Fallbacks are each site's own: where a site compared for equality against
  one named class, a plain map index with the zero handler is the old answer;
  projection's `classFor` keeps the needs-discovery fallback that
  `orderWork`'s `default:` arm used to supply. Not converted, and measured
  with a `go/ast` scan rather than grep: lint's three sites are `lint.Class`,
  #73's logical-type axis, and `tools/refusal-probe/schemas.go` holds one
  comparison outside the named packages. The identity golden did not move.

FORK WORK:

- **`live/rowgen-convergence.json` is retired** (#695). Its headline,
  adopted-unchanged, is the metric on record as not predicting onboarding
  success -- three sessions read it as coverage and planned work around
  raising it -- and measured against its readers, one per-type fact and four
  counts out of a 516KB artifact were load-bearing. Those are now
  `live/rowgen-mismatches.json` (`row-gen -mismatches`), and the separate
  #387 measurement it had absorbed is `live/schema-precedence.json`
  (`row-gen -schema-precedence`); both are value-identical to what the old
  artifact recorded. The ratio, the per-service breakdown of the same ratio,
  and seven per-row fields nothing read are gone.
  `live/artifact_readers_test.go` is the new guard: no generator under
  `tools/` may write a committed artifact nothing outside it reads.

- **`live-plan -json` is reachable on a configuration that declares its own
  estate** (#894). `#788`'s document could be produced only through
  `live-plan -estate=NAME -json`, and that flag is refused beside a `live`
  block or an `estate.chdf.hcl` sidecar -- so the shape the docs recommend
  was the one shape the document could not be produced for. `choudoufu plan
  -json` and `choudoufu live-plan -json` now print it there too, from the
  same pipeline and byte-identically to the `-estate` form. `-estate` beside
  a declared estate is still refused; an `apply -json` still has no document
  and still says so.

- **`-json` keeps stdout to the document** (#894). The plan graph's own UI
  hooks wrote to stdout, so a configuration with a data source printed
  `data.x.y: Reading...` ahead of the document and piping stdout into a
  parser failed. Progress and diagnostics now go to stderr, as stock `plan
  -json` does. `live-check -json` was checked for the same defect and does
  not have it.

- **Stage 12, plan approval, is active and measured on every estate** (#888
  and #903; PRs #904, #923, #925, #926, #927, #931, #936 and #939). The
  stage's `Proves` line had described #878's mechanism since before that
  mechanism existed, and it was `Headline: true`, but `live/gauntlet.json`
  read `plan_approval: not_run` on all 27 rows: the behaviour a consumer gates
  on (INTENTIUS/chant#2081) was proved by one smoke scenario on one fixture
  and by unit tests, which is evidence for the mechanism and not for the
  board. Every `live/e2e/<estate>/run.sh` now carries a `PART P` leg between
  `drift_reconverge` and the day-2 parts: edit exactly one argument reaching
  exactly one instance, `plan -out=approved.tfplan` asserting the change set
  is that instance alone, move the world out of band on a different object
  through the AWS CLI (each estate's own `drift_reconverge` mutation, lifted),
  `apply approved.tfplan` asserting exit 3, the refusal by name, the extra row
  by address and by the live identity it was computed against, and, read back
  through the CLI, that the reviewed change did not land; then put the world
  back and apply the same file, which must succeed. `BREAK_APPROVAL=1` runs
  the stage's recorded Break line literally and must fail, and did on all 27.

  The leg landed on `corpus-giantswarm-crossplane` and `corpus-iam-policy`
  first, then on the other 25 estates in five batches, then one line in
  `tools/gauntlet/stages.go` flipped the status, and a re-measure of all 27
  estates at `-parallel 4` (44m11s) wrote the rows. The flip alone took both
  bars to zero, because no row had been re-measured since the leg was
  written; the number the re-measure produced is in the board movement
  section. Writing 27 legs taught a few things now recorded in the scripts: a
  resource with a dependent data source cannot carry the reviewed edit (the
  bucket policy joins the change set), `corpus-leynos-monitoring`'s `-target`
  scoping needs no exemption because `apply <planfile>` re-plans from the
  apply's own arguments, an `aws_db_instance`'s identity is its
  `DbiResourceId` and a Route 53 record's carries no trailing dot.

  The re-measure exposed one regression, `reference-ec2-vpc`'s `day2_crash`
  (a planned stage, so neither bar moved): #920 stopped tombstoning a deposed
  object whose destroy had not run, and nothing wrote the tombstone when that
  destroy finally did, so the next plan refused with `Two live resources
  claiming one address`. It was left in the artifact as measured rather than
  signed off in `live/gauntlet/regressions.json`, and fixed separately (#938).

- **The boundary holds across provider configurations, measured on two
  regions and then on two accounts** (#845, PR #909; #907, PR #921). No smoke
  claim ran a second provider configuration, so the first real multi-region
  defect (#745, fixed in PR #837) was proven by fake-backed unit tests only
  and the questions a two-region estate raises had no answer anyone could
  run. Claim 16, `just smoke the-boundary-holds-across-regions`, declares
  `aws.east` and `aws.west` under one `tofu-estate` marker and one record
  store, with a log group of the same name in each region. It reads both
  objects back with the AWS CLI, plans empty, and prints the per-pass request
  count off the SigV4 credential scope (43 for us-east-1, 15 for us-west-2)
  alongside the listing count per type: the mirrored type listed once per
  region, the east-only account-global bucket once across both passes.
  Deleting the west log group out of band produces a plan that names
  `aws_cloudwatch_log_group.west` and nothing else while east's instances
  stay served from the cache; deleting the cache and the record store yields
  an identical change set with a different coverage report. Two answers are
  pinned as they are: the unit of the sweep is the provider configuration,
  so dropping a region's last declaration drops the region from the sweep
  and its marked objects sit there with `No changes.`, and a region change
  is a replace whose old object is left behind (#906, filed). `BREAK=1`
  strips the surviving object's markers and points `aws.west` at us-east-1,
  which makes the deleted instance read `state cache hit ... ownership
  record-attested` for an object that no longer exists, and step 2 fails on
  that line. 48s measured against a 2 min budget. Writing it also found
  #905: `discovery.Merge` drops `VerifiedDeclared`, so the tag index's vouch
  is empty for every concrete instance in any multi-provider estate.

  The cross-account leg was the one question the region scenario asserted by
  construction. floci turns out to present two account ids, either by
  reading a 12-digit access key id as the account or through `assume-role`,
  and its stores are partitioned by account, so claim 19, `just smoke
  the-boundary-holds-across-accounts`, measures the same estate with
  `aws.home` and `aws.other_account` in one region: 13 requests signed as
  each account, a delete in account 111111111111 planned as one create there
  and nothing in 000000000000, and an empty plan after losing the cache and
  the record store. Its `BREAK=1` swaps the second alias's credential for
  the home account's and catches the same silent unchanged read on the
  account axis. 32s. Two things were left for the maintainer: EC2 in the
  emulator stamps every object's `OwnerId` with the default account
  (lex00/floci#196), and the sweep's own tagging and Cloud Control clients
  are built from the process environment's credentials for every provider
  configuration, so the estate-wide tag index is fetched from one account.

- **A record-only identity survives cache loss, and losing the record itself
  proposes exactly one create** (#852, PR #910). PR #851's located-fallback
  fix for a wire-composite identity was proven against fakes only. Claim 17,
  `just smoke record-only-survives-cache-loss`, applies an `aws_iam_group`
  and an `aws_iam_group_policy` with no `name` argument, so the policy's
  two-part identity is assigned by the provider and no tag, listing or
  configuration expression carries it; the scenario prints the recorded
  group and policy name by value, wipes the cache and `.terraform`, and
  requires `No changes.` together with a `GetGroupPolicy` read in the debug
  log that used exactly the recorded pair. `BREAK=1` also deletes the
  identity record before that re-plan and must see `Plan: 1 to add` naming
  `aws_iam_group_policy.app`. The resource is not one of the 27 types PR
  #851 named, and the claims page says why: the three that reach the wire
  fallback are not implemented by the pinned emulator, and the other 24
  carry ratified identity rows that rebuild the same identity from
  configuration with or without a record, measured by applying
  `aws_lb_target_group_attachment` and re-planning with its record deleted.

- **A replaced object's shadow is pruned by tombstone, a live duplicate
  refuses, and a refused destroy writes no tombstone** (#850, PR #911; #919,
  PR #942). PR #849's tombstone mechanism had fake-backed tests only, and
  before it the live-duplicate case warned and exited 0, so a control
  written against the old behaviour would have passed while proving
  nothing. Claim 18, `just smoke a-shadow-is-not-a-claimant`, replaces an
  `aws_instance` twice through a ForceNew `subnet_id`, reads each terminated
  instance back with the CLI still wearing `tofu-estate` and `tofu-address`,
  and asserts by value that the record's `tombstone` is a list holding both
  destroyed ids under the live `import_id`. The next plan exits 0 with both
  dead identities in `Live resource displaced from the address it is marked
  for` warnings that propose nothing. `BREAK=1` manufactures a second
  running instance carrying the survivor's markers with nothing recorded as
  having destroyed it, and the plan must refuse with `Two live resources
  claiming one address` naming both ids. Writing the scenario found that no
  replace on merged main recorded a tombstone at all: `backend_apply.go`
  read `replacedInstances(plan)` after `Core.Apply` had drained the changes
  (#908, fixed by PR #913).

  Step 7 covers the write-side change from #901: a `create_before_destroy`
  replace whose destroy leg fails must not record the deposed object as
  destroyed. Of the two mechanisms the issue proposed, only an IAM deny of
  `ec2:TerminateInstances` under `FLOCI_IAM_ENFORCEMENT=true` works on the
  pinned image (`disable_api_termination` is accepted and ignored), so the
  step proves the fence on a throwaway instance first, then applies a third
  replace under a `no-terminate` role. The apply exits non-zero with the
  replacement created, the CLI lists both instances `running`, the record
  names the old one under `deposed[]` and not under `tombstone[]`, and the
  next plan proposes `aws_instance.web (deposed object ...) will be
  destroyed` rather than pruning it. `BREAK=1` now runs both arms on one
  estate; the second patches the record to list the running deposed
  instance as a tombstone, which is what the pre-#901 write side produced,
  and the read must refuse it. The table's budget is 3 min.

- **The head-of-line fixes are measured on real AWS** (#867, PR #917). #683
  and #839 split the read pass's and the sweep's single bounded channel into
  an in-flight bound and a buffered-answers bound, proven against fakes,
  with the real-AWS number owed. Three steady-state plans of the 745-instance
  terralith in us-east-2 on provider 6.59.0 span 50.0s to 57.1s with 6% to
  20% idle and a largest stall of 4.68s; three stock plans of the same
  estate in the same session read 0% to 42% idle with a largest stall of
  8.04s. In #683's session the fork idled 49% to 56% against stock's 20%,
  and the comparison that survives is the fork's share next to stock's in
  one session, since an account does not throttle the same way twice. Every
  one of the nineteen stalls on both sides ends in a `retrying request`
  line, and all nine of the fork's read-pass stalls fall in the last quarter
  of their run, which is the shape the split predicts. The sweep showed two
  throttled list calls across three runs, 1.23s and 1.51s, and this estate
  cannot test `DefaultSweepBufferFactor = 10`: 32 of its swept types are
  answered by one estate-filtered `GetResources` and only three take the
  per-type list path, so a factor of one would have produced the identical
  run. The doc comment cites the negative result and names the estate shape
  that would test it.

  The instrument had to be recovered first, because #683's
  `live/wallclock-trace` branch no longer exists. It is now
  `live/live-cert/wallclock-gaps.py`, with two corrections: the gap window
  is closed on the right, since the SDK's retry line lands in the same
  millisecond as the request that ends the stall and the old bound dropped
  it (12 of 12 of #683's own gaps are retry-closed, where it had read 6 of
  12), and stalls are attributed by `tf_rpc` rather than by the clock,
  because the sweep's client-side-filtered `aws_iam_policy` listing is still
  arriving at t=24s. `terralith-scale.sh` gains `WALLCLOCK_TRACE=1`, and
  `site/content/docs/model/plan-cost.md` gains a stamped "What the split was
  worth, measured" subsection in place of the sentence saying read-side
  throttling had never been measured: 43 to 46 throttled requests per
  steady-state plan at width ten, every one retried and answered. The
  live-cert harness's `ssm` record store cannot open against real AWS
  because its `key_prefix` starts with `/` (#916, filed); the run used
  `RECORD_STORE_BACKEND=local`, which is what #683 used.

- **`gauntlet notes` no longer prints `readiness.json`'s whole types array**
  (#897, PR #899). The release procedure at the top of this file points at
  `go run ./tools/gauntlet notes`, and for v0.12.0 to v0.13.0 that command
  produced 51,800 lines and 1,559,007 bytes, of which the board movement was
  the first 17. `diffReadiness` was a shallow key compare written before
  `live/readiness.json` existed, and the file's top level is one `types` key
  holding 1699 entries, so any change to any type rendered both arrays as a
  single bullet. The `types` key is now diffed by each entry's `type`
  against its `tier` and rendered as one bounded line, with named movers
  capped at 10 and a `+N more` tail; the other top-level keys keep the
  before-and-after render. The same pair now produces 16 lines and 381
  bytes, and because the only difference between those two snapshots is
  `aws_wafv2_api_key`'s `rejected_reason` wording, the Readiness section is
  omitted rather than invented. Three tests in `tools/gauntlet/notes_test.go`
  hold the size bound and the omission, each proved red against the old
  code.

- **`live/LIMITATIONS.md`'s refusal sections are rendered from
  `check.AllRefusals()`** (#698, PR #924). Hand-typed refusal prose is the
  class the repository keeps catching stale. Measured before the change, the
  document was 378397 bytes with 59.1% inside a generated span, and of the
  catalog's 223 refusals 188 had a generated entry and 35 did not: the 28
  lint rules and 7 non-lint refusals that defer to a hand-written entry.
  `tools/limits-gen`, which has owned the `refusal-table` and
  `refusal-entries` spans since #110, now writes a third span,
  `lint-roster`, at the head of "Enforced today" with one row per rule, its
  severity, the entry that documents it and that entry's fixture directory
  under `live/e2e/limits/` (25 of 28; the three receipt rules are specified
  in `live/RECEIPTS.md`), and the entries span widens from 188 to all 195
  non-lint refusals, the 7 deferring ones getting a section that names the
  fuller hand-written entry. The render itself now refuses and writes
  nothing when a refusal it would give an entry to has no description, when
  a lint rule's heading has no fixture directory, or when a refusal cites a
  heading nobody wrote; each was proved red by breaking the tree, and a
  hand edit inside a span still fails `TestSpansAreCurrent`. `just limits`
  regenerates it and is idempotent. The page grows to 388039 bytes rather
  than shrinking, because the roster and the widened entries are new
  content; the shrink the issue asks for arrives when the 28 lint rules'
  prose moves into their doc strings, which is a doc-string change left for
  the maintainer, and the #613 unmigrate guard is still outside the registry
  and so uncovered.

- **The `ssm` and `s3` record stores applied `key_prefix` twice** (#916, PR
  #918). `newRecordStore` handed the prefix to the backend as its own
  namespace, and every key it was then asked for already began with it, so
  against real AWS `record_store "ssm" { key_prefix = "chdf916probe/e1" }`
  wrote `/chdf916probe/e1/chdf916probe/e1/.store-sentinel`, `s3` did the
  same, and the default derived from the estate landed at
  `/tofu-records/<estate>/tofu-records/<estate>/...`. Nothing inside the
  package could see it, because both halves of every round trip went through
  the doubled name and agreed with each other. What broke was everything
  outside it: an operator's IAM policy, `aws ssm get-parameters-by-path`, the
  reference page's example and the live-cert harness's own teardown, all of
  which name the prefix once. The backends are now built with no prefix of
  their own, `NewSSMStore` no longer turns an empty prefix into `"/"` (which
  rendered a `//<key>` name real SSM rejects), and
  `TestRecordStoreRendersTheKeyPrefixExactlyOnce` asserts the rendered name
  against a literal rather than through a round trip. The `local` backend,
  whose `path` is a directory, was never affected. The harness's `key_prefix`
  loses the leading `/` that #688 refuses, so `TARGET=aws` live-cert can open
  its store again. Every probe resource was deleted and verified gone.

- **`TestMkConfigDir_new` asserts the mode `mkConfigDir` actually promises**
  (#895, PR #898). `mkConfigDir` creates the directory with `os.ModePerm`,
  which the kernel masks with the process's umask, and the test asserted
  `0755`, true only under `umask 022`. On a developer machine with `umask
  077` `scripts/ci-gate.sh` read red on an unmodified checkout (`Expected
  mode: 0755, but got: 0700`), which is how it surfaced while gating the
  v0.13.0 release. Nothing in the tree reads that directory expecting a fixed
  mode and the code is upstream's unchanged shape, so the test now computes
  `0777 &^ currentUmask()`, with the helper split by build tag the way
  `signal_unix.go` and `signal_windows.go` already are. It was proven
  load-bearing by changing `mkConfigDir`'s base mode to `0700` under `umask
  022`, where the old assertion would have passed by coincidence, and
  watching it fail.

- **estate-gen cohorts render at run time; the committed cohorts retire**
  (#699, PR #929). `live/e2e/estates` held 32 committed estate-gen cohorts,
  generator output kept in git: every working copy grew an ignored
  `.terraform/` in each of the 31 rendered directories, `.gitignore` carried
  an exception block to manage exactly that, and a regeneration was a
  213-file diff nobody read. `tools/terralith-gen` had already chosen the
  other model. `internal/live/cohorts` now holds the 31-entry roster (each
  cohort's pinned `-types` list and the supporting types the generator adds),
  `tools/estate-gen -all` renders the whole roster into `-out/<cohort>`
  sharing one schema acquisition (all 31 in 13 seconds; `-out` is required
  now, since the old default would re-create the deleted tree), and
  `flocitest.GenerateCohorts` shells out to it into `t.TempDir()`. The 32
  `README.md` files stay: 9161 lines of hand-written ratification evidence
  and emulator findings that a dozen files cite by path. The
  `live/e2e/estates/*` glob leaves `live/corpus-manifest.json`, which is the
  writer that put `.terraform/modules` there in the first place.

  A full render was diffed against the committed tree before anything was
  deleted: 30 of 31 cohorts byte-identical, and `route53-cloudfront` one line
  apart (`vpc_region = "placeholder"`, a generator force-fill the committed
  copy predated), so that is generator drift and the ruling moved to the
  cohort's README. The identity golden lost 704 rows and changed none: 1810
  identities across 654 directories to 1106 across 623, every removed row
  under `live/e2e/estates/`. The cost is said in the pin's own note: the
  cohorts' rendered identities are no longer pinned by value anywhere, and
  what covers them is the acceptance tier's apply-and-replan over freshly
  rendered trees. `TestEstatesHoldsNoConfiguration` and
  `TestGeneratedCohortsMatchTheRecordedRoster` are the new guards, both
  proved red. Left for a follow-up: `tools/estate-gen/files.go` still writes
  the old `-out` path into each rendered `GENERATED.md`, and
  `live/fork-surface.json` and `live/cohort-triage.json` still name the
  deleted files until their own generators next run.

- **`corpus-fetch` is guarded against reaching an estate-gen cohort** (#940,
  PR #941). Right after #699 merged, worktrees still showed 31 untracked
  `live/e2e/estates/*/.terraform/` entries, and the issue read that as
  `just corpus-fetch` still writing there. Measured, the writer was already
  gone: #699 had removed the glob, and `corpus-fetch`'s module pass writes
  `.terraform/modules/modules.json` only into directories `Manifest.Resolve`
  returns. The stray directories were written by a `corpus-fetch` run in a
  worktree created before #699 and survived the rebase onto it, and the
  primary checkout's copies date from August. What this adds is the guard
  the issue asked for: `TestManifestReachesNoEstateGenCohort` in
  `tools/corpus-fetch` resolves the real manifest against a temp root where
  every roster cohort holds a `main.tf`, so a glob cannot hide behind
  `Resolve`'s empty-directory skip; re-adding the old glob fails it naming 32
  directories. A worktree still carrying the leftovers clears them with
  `git clean -fd -- live/e2e/estates`.

## choudoufu v0.13.0 (2026-09-06)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.13.0.json`](live/history/v0.13.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.12.0.json live/history/v0.13.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none
- Emulator: repinned from `ghcr.io/lex00/floci@sha256:c55d74e13e96c8b132056677337dba0084bb0b427cb039be2dbf9a8b7efc0948` to `ghcr.io/lex00/floci@sha256:a39185cc3971d0188663d61043cb038dff1260d8a975b1aa72c4e2bb1feac3cb`

Unlike v0.12.0, this board was re-measured rather than carried forward: the
emulator was repinned (PR #862), the whole board was then swept against the
new pin and read 19/26 with eight regressions filed (PR #876), and every one
of those was repaired and re-measured back to 26/26 and 27/27 (PRs #886, #893).

ENGINE WORK:

- **One slow call no longer stops the window sliding** (#683, PR #840; #839,
  PR #860). The read pass's prefetch and the sweep's each used one bounded
  channel as both the concurrency limit and the backpressure, so an answer
  that had already landed went on holding a slot until the consuming loop
  reached it, and a stalled call stopped the launcher outright. Each is now
  two bounds: an in-flight count the worker releases when its call returns,
  and a buffer of fetched-but-unconsumed answers the consumer releases.
  `TOFU_LIVE_READ_PARALLELISM` and `TOFU_LIVE_SWEEP_PARALLELISM` keep their
  names, defaults and refusals; the buffer follows the width. Both are `Refs`
  rather than `Closes`, because the share of wall clock is proven only
  against a fake: floci never throttles, so the emulator cannot produce the
  straggler.

- **A cache vouch belongs to the pass that saw it** (#745, PR #837).
  `CacheVouchSightings` was keyed by type and import identity and unioned
  across every pass. In a multi-region estate mirroring one client-chosen
  name into two regions, region B's sighting therefore vouched for an object
  deleted out of band in region A, and the `-refresh=false` plan reported the
  dead instance unchanged. Sightings now carry the provider configuration
  their pass listed through and are looked up under it, and the vouch pass
  skips a type no in-scope block declares.

- **A record-only composite identity binds, and one object can have two
  names** (#746, PR #851; #855, PR #877; #879, PR #892). The located fallback
  skipped every record with an empty `ImportID`, which is exactly what a
  wire-identity composite is written as, so an instance the estate had
  already recorded went unbound and the plan proposed a second copy of a live
  object. It now binds from the record's components, never a joined string.
  Measured against hashicorp/aws 6.59.0 the exposed population is 27 types,
  three of which reach the fallback, and none carries a sensitive identity
  attribute. Separately, a type whose wire identity is `family`+`revision`
  while its documented import string is a whole ARN was recorded only one
  way, so a replace's tombstone could never be matched against the destroyed
  object's lingering tag; `LocatedRecord.SecondaryID` now records both, read
  only to let a claimant leave a collision set.

- **A replace tombstones what it destroyed, and orphan recovery reads the
  record first** (#670, PR #849; #872, PR #883; #875, PR #891; #881, PR
  #890). Pruning a superseded claimant on "the record names a different
  object" could not tell a terminated tag shadow from a second genuinely live
  object wearing the address's marker. A replace now records the identity it
  overwrote in the same tombstone member a destroy already writes, bounded at
  eight per address, and a claimant is pruned only when it matches one. Three
  orphan-recovery defects the board sweep exposed are fixed alongside it: a
  declared parent answered only by the record store counted as unheld and
  withheld its untaggable children (#872), the parent-read legs ran ahead of
  the record store and minted a child's address from the live object's own
  name (#875), and an unserved-service type the provider cannot list was
  routed to the native leg and so never enumerated at all (#881).

- **`plan -out` and `apply <planfile>` are an approval gate** (#878, PR
  #889). Both were refused under live markers, so the shape a pipeline
  actually runs in (plan on the pull request, a human approves, apply exactly
  what was approved) had nothing to hold. Apply now reads the file for its
  change set and its estate and then drops it, plans live the ordinary way,
  and compares the two: address, action, the identity of the live object the
  change was computed against, and the planned values on both sides,
  canonically rendered, with unknowns excluded and sensitive values compared
  as a digest. A difference refuses by name, `The approved plan no longer
  matches the live system` (or the sibling `The approved plan belongs to a
  different estate`), and exits **3**, which is neither an ordinary failure
  nor `-detailed-exitcode`'s 2. Claim 15,
  `live/smoke/scenarios/apply-what-was-approved.sh`, proves it; its `BREAK=1`
  arm is inverted, because the risk here is a comparison that refuses every
  plan file rather than one that never fires.

FORK WORK:

- **The emulator is repinned** (#672, PRs #847 and #862; lex00/floci#190 and
  #191). floci's `CreateSubnet` accepted a CIDR conflicting with an existing
  subnet in the same VPC, which real EC2 refuses as `InvalidSubnet.Conflict`.
  `CreateSubnet` carries no idempotency token, so an SDK transport retry
  created a second live subnet, and that is `corpus-vpc-complete`'s 18-vs-19
  greenfield flake. floci gained the conflict check, `live/floci-image` moved
  to `sha256:a39185cc...`, and `live/floci-capabilities.json` was regenerated
  for the new digest. `plan-budget.json`, `cohort-acceptance.json` and
  `cohort-triage.json` are recorded as measured against the old pin rather
  than silently re-measured.

- **`internal/command/e2etest` now gates something** (#755, PRs #836 and
  #856). `TestStaticPlanVariables` was red on main: `unlock.go` called the
  stateless guard before parsing variables, so the guard's own config load
  could not see a `-var` the backend depended on, while the other four
  guarded commands parse first. A four-day-old fork defect sat behind a
  package no tier ran, so per the maintainer's 2026-09-05 ruling the package
  joined the fast tier in both `.github/workflows/ci.yml` and the `justfile`,
  at a measured 42s and no `TF_ACC`.

- **A nightly that fails says why** (#496, PR #842). Every nightly gauntlet
  run since 08-21 computed real verdicts and then failed to open the verdicts
  pull request, with the reason at the bottom of a 300-line log. That step now
  runs under `continue-on-error` followed by one that emits a `::error`
  annotation naming the issue and the two likely causes, and `scripts/pickup.sh`
  prints the workflow's own state and the artifact's last measured date. The
  org-level Actions permission is still owed and the workflow stays disabled.

- **Published figures carry their provenance** (#679, PR #857). Eleven ranked
  site figures, `live/SURVEY.md`'s hand-typed provider-wide paragraph, and
  `live/COVERAGE.md`'s "Admitted" row and "Other providers" section were
  uncited, stale, or both. `forkdiff-gen`, `readiness-gen` and `survey-gen`
  grew build stamps, every remaining figure got a commit or a "Stale" stamp,
  and `TestSiteContentMeasuredFiguresCarryProvenance` fails a number followed
  by a unit word with no sha, no "Stale" and no anchored link anywhere in its
  heading section.

- **The terralith ceiling table is re-measured** (#708, PR #841; #838, PR
  #866). The bench's fixture loader stopped on any module call, so it could
  not run at all against a `terralith-gen` that has emitted a module-nested
  bucket since #574; it now resolves a local module source the way
  `check.LoadOverlay` does. All six tiers then ran against the new pin: 79 to
  5925 resources, 113 to 7697 API calls, apply settling to about 0.28s per
  resource from scale=4 on. Peak memory is the one finding that changed shape:
  the old flat 225-300MB band is real, gradual, sub-linear growth, 1.50x for
  the harness and 1.59x for floci across a 75x range of resource counts.

- **The record store's sentinel is not a record** (#861, PR #865).
  `.store-sentinel` sits under every record store's key namespace and was
  swept up by every crossing script's `find`, so every record-file assertion
  read one file too many and `corpus-security-group-complete` could not reach
  `day2_replace` at all. A shared `gauntlet_record_count` helper spells the
  name once, 24 scripts and 29 call sites route through it, and
  `TestNoScriptCopiesTheSentinelBlindFind` bans the raw pattern. That guard is
  what found `corpus-mastino-dns`, which `grep -r` had been skipping as binary.

- **Prose held against code, and denominators against their artifacts**
  (#658, PR #844; #843, PR #859; #853, PR #884). Five sites stated a rule the
  code does not enforce or named a remedy that does nothing: the child-module
  diagnostic refused a `count.index` read the analyzer admits, two projection
  refusals named a `record_store` block implied since #364, a doc comment
  cited a file that does not exist, and `live/GAUNTLET.md`'s and
  `iamref-gen`'s hand-typed totals had drifted from their own artifacts.
  reach.md's "17 of 180 services" divided by a population including 17
  services never checked; a new `checked-count` shortcode field renders 17 of
  157 out of the same filtered slice the named/unnamed split already uses.
  `live/COVERAGE.md`'s #427 breakdown summed to 77 against a 76-member bucket.
  Two pages that led with a withdrawn figure before stating the current one
  were reordered (PRs #798, #800), and claim 14
  (`plan-cost-tracks-the-estate`) was published: the scenario was fully built,
  unlisted, and carrying a header number that collides with claim 10 (PR
  #802).

## choudoufu v0.12.0 (2026-09-04)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.12.0.json`](live/history/v0.12.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.11.0.json live/history/v0.12.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The board was not re-measured against this release's engine changes;
the snapshot carries forward the last run's verdicts.

ENGINE WORK:

- **`live-ls`** (#789): a new verb, an inventory read for what an estate
  holds under live markers -- no plan, no apply, just a listing.
- **`live-check -json`** (#790) and **`live-plan -json`** (#788): both
  offline/diagnostic commands gain a machine-readable document (the
  declared roster and cross-estate references for check, the
  bound/omissions/unowned breakdown for plan) beside their existing
  human-readable report.
- **`live-mv -json`** (#791): the move as one JSON document -- the live
  resource, the address and marker on each side, the followers that move
  without a write of their own, and `found_by` as the admission path's own
  `LIST` or `IDENTITY`. Printed on a refusal as well as a success, with the
  refusal's stable code beside its prose, so a preview or a receipt reader
  has one parse target rather than a reconstruction of the human report's
  rows. (Recorded here after the release: it shipped in `e4e897de8c`,
  `ce79fbf43b`, `7072d2cab3` and `dc3e48487c` and this section named the
  other three of the four.)

FORK WORK:

- **The example moves to `examples/live-mv-workbench`**, named for the tool
  it is becoming: a phased live-mv workflow with the terralith as its demo
  seed. The page is `workbench.py`; the package stays `tlmig` for now.
  Paths in the v0.11.0 entry below are as shipped.

- **A live, watchable demo** (`demo.py`, PR #796): a single-screen page and
  a two-container compose stack (`just up`) that splits the terralith by
  retagging against a pinned floci, live, with no credentials and nothing
  to clean up. Picked up from an in-progress handoff and taken through a
  full watched run: the container now defaults to the pinned choudoufu
  release rather than always building from source (`just up source` opts
  into a source build), receipt stops burning its full two-minute
  CloudTrail-lag budget against an emulator that never logs it, and a run
  of UX fixes made from actually watching it -- a pulsing "working"
  indicator and a live elapsed-seconds counter, a ghosted preview of the
  planned split before Move makes it real, a payoff line for every phase
  (four of eight had none), a warning before Move's second, undocumented
  act, and a real scene for the read-only receipt phase instead of a
  static, unmoving map. `.claude/skills/live-mv-demo/SKILL.md` and updated
  docs (the example's README and its docs-site page, whose CLI examples
  had drifted onto verb names the CLI no longer answers to) point at it.

## choudoufu v0.11.0 (2026-09-03)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.11.0.json`](live/history/v0.11.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.10.1.json live/history/v0.11.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The engine is unchanged since v0.10.1. This release adds the
terralith-migration example; the board was last measured at
`bb45512c9b` (2026-08-31) and was not re-run.

FORK WORK:

- **The terralith-migration example** (PR #770): `examples/terralith-migration`,
  a uv project whose package `tlmig` runs the blog's decomposition story
  against a real account, nine phases behind one command
  (`uv run tlmig <phase> --run <id>`, or `all`): `preflight`, `setup`,
  `slow-plan`, `decompose`, `fast-plan`, `carve`, `guard`, `receipt`,
  `teardown`, plus `status` and `reset`. The story is a monolith one
  estate owns; the slow plan that refreshes all of it; the decomposition
  that retags each team's resources into its own estate with
  `live-mv -from-estate`, no state surgery; the fast plan of one estate
  served from cache; a team dissolving, its role carved into another; and
  a governance guard proving the carve left nothing behind. Every phase is
  a standalone beat that reads its inputs from the run directory, so the
  notebook can run them one cell at a time and a rehearsal can run them
  in sequence.

- **Fenced execution** (`tlmig/guard.py`). Every command the example runs
  goes through one place: preflight asserts the caller's account and the
  pinned binary before anything is touched; a destructive `choudoufu`
  call must run inside the run's own tree and a raw `aws` delete must name
  a resource carrying the run's prefix; a human confirms every
  destructive call unless `--auto`. The fences decide what can be
  destroyed, and the confirmation is only the last stop. Teardown works
  from the manifest setup wrote and refuses to call the run clean while
  anything carrying the run's prefix or estate tags remains.

- **The event feed and the receipt** (`tlmig/events.py`, `tlmig/receipt.py`,
  `tlmig/measure.py`, `tlmig/govern.py`). Every command, phase, inventory
  read, measurement, fact and verdict lands in `runs/<id>/events.jsonl`,
  append-only, with captured output filed beside it. Plan cost is measured
  live under `TF_LOG=debug` by counting provider requests and state-cache
  hits, and shown beside the claim smoke's reproducible receipt as a
  separately labelled panel, never dressed up as the same measurement. The
  payoff beat settles the tagging index before it measures the fast plan,
  and a repeated measurement no longer double-counts, because the log it
  counts is unlinked before each plan (PR #772). The governance guard
  reads the moved role's tags, inline policies and attachments through the
  plain CLI and grades both estates' plans by their text.

- **The stage and the renderer** (`migration.py`, `tlmig/stage.py`,
  `tlmig/viz.py`). The marimo notebook is the stage
  (`uv run --extra viz marimo run migration.py`): a cell per phase carries
  the beat's narration and a button that runs
  `python -m tlmig.cli <phase> --run <id> --auto` as a background
  subprocess, one phase at a time, with the phase's own ledger rows and
  its picture as the phase left it; a live picture at the top follows
  `events.jsonl` on a timer. Replay mode plays a recorded run with no
  account. The renderer, stdlib only, draws a run directory as one
  picture: a phase strip, an estate-ownership map with every resource as
  a cell keyed by ARN and coloured by the estate its live `tofu-estate`
  tag names, untaggable children coloured by their parent role's tag, a
  ledger of every command and the platform's answer, and the plan-cost
  bars beside the receipt. Two recorded runs ship under `tests/fixtures`,
  one a synthetic walk of every phase and one written by the real
  emitters.

- **The pin.** The example pins `CHOUDOUFU_VERSION` to the release its
  numbers were measured against and refuses a drifted binary at
  preflight. A pinned release that is not cached is fetched the way the
  smoke harness fetches it, and `CHOUDOUFU_VERSION=local` builds the
  checkout's own binary, stamped with its git describe, when the release
  lags the engine.

## choudoufu v0.10.1 (2026-09-03)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.10.1.json`](live/history/v0.10.1.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.10.0.json live/history/v0.10.1.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The board was last measured at `bb45512c9b` (2026-08-31) and was not
re-run for this release; the fix below is proven by its own unit tests,
by the two claim smokes whose subject it touches, and by a binary rerun
of the plan that showed the defect.

FORK WORK:

- **A declared inline policy is no longer reported as a removal** (PR
  #768). Every plan of an estate with a declared `aws_iam_role_policy`
  printed `[WILL BE DESTROYED]` for that policy in the parent-read
  section under a `No changes.` summary, with a `[SUPERSEDED]` entry at
  an address minted from the policy's name. Moved or not, `-refresh=false`
  or not. The parent-list leg keyed its declared set on
  `Resolution.ImportID`, which the schema-aware resolver leaves empty on
  purpose for an identity-object-only type (several identity attributes,
  no documented separator): with the provider's identity schema in hand,
  which is every plan the command runs, `aws_iam_role_policy` is such a
  type, its identity lives in `IdentityValues`, and the declared set read
  as one empty string nothing could match. The three parent-read legs now
  key declared children by the same composed identity a list result's
  attributes compose to (`declaredChildImportIDs`), so the two sides agree
  by construction. The unit harness had never seen the shape because it
  resolves without schemas; the fake cloud can now serve a provider
  identity schema, and the new tests prove the plan's shape red then
  green with the stray policy still found.

## choudoufu v0.10.0 (2026-09-03)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.10.0.json`](live/history/v0.10.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.9.0.json live/history/v0.10.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The board was last measured at `bb45512c9b` (2026-08-31) and was not
re-run for this release; the engine changes below are proven by their own
claim smokes and unit tests, not by a fresh gauntlet pass.

FORK WORK:

- **A cross-estate move: `live-mv -from-estate`** (PR #760). The rename
  verb finds a resource by (estate, old address) and writes (estate, new
  address). With `-from-estate` it finds by (source estate, old address)
  and writes (this estate, new address), which may be the same address:
  the split `live/MARKERS.md` describes as a tag rewrite, performed
  through the same tags-only provider apply with the same refusals. It
  runs in the destination's configuration after the block moves there,
  one resource per call; the source's record for the resource stays
  behind. Refusals added: an invalid or same-as-destination source
  estate, a resource owned by a third estate, and a move that already
  ran.

- **The live tag decides** (maintainer ruling 2026-09-03; PRs #759, #760,
  #763). A parent whose live `tofu-estate` names another estate never
  anchors a child read for this one, whatever a left-behind record says.
  The parent-read legs record the tag the sweep saw and skip such a
  parent (#759, which also adds parent-list-recovered orphan recovery
  for inline IAM policies); the record-orphan leg reads that same map
  first and, for a parent whose tag was never read, skips the child
  unless something in the pass holds the parent (#760, #763). Found by
  the carve claim: after a role moved estates, the source's next plan
  proposed destroying the role's inline policy and attachments.

- **Claims 11, 12 and 13** (PRs #761, #760, #762; #764). Three more
  narrated smoke scenarios with BREAK controls proven to catch:
  `count-is-a-fungible-set` (a pool scales down by slot and rebuilds
  nothing; BREAK strips a slot and the run refuses by name),
  `carve-by-retag` (a stock terralith is adopted with one command, then
  carved into estates by tag writes, every side plans clean, and the
  six-resource team estate plans for 39 requests against the monolith's
  166; BREAK moves the blocks and skips the retag, and both sides show
  the two-ledger window), and `the-tag-is-the-boundary` (two roles fenced
  to halves of one estate by a condition on the ownership tag, denied by
  the platform when they reach across, and the carve itself refused for
  the role that may not make it; BREAK drops the condition and the
  denial vanishes). The smoke stack gains `FLOCI_IAM_ENFORCEMENT=true`,
  which a scenario exports before `stack_up` to turn the emulator's IAM
  enforcement on for its run, and a portable `sed_i` helper after the
  BSD-only form died on Linux. Claim 13 also carries a real-account
  receipt (PR #765): the same carve run in us-east-2 under two assumed
  roles, with all five `CreateTags` calls read back from CloudTrail event
  history, two of them `Client.UnauthorizedOperation` naming the role
  session and the instance, recorded on the claims page and in
  `live/smoke/evidence/the-tag-is-the-boundary.cloudtrail.json`.

- **The cache serves the whole estate** (#692 increment 3; PR #758).
  On `-refresh=false` every converged instance is served from the state
  cache, server-assigned needs-discovery types included, so one estate
  of a terralith plans without re-reading the cloud; claim 10 pins it,
  and its BREAK deletes a resource out of band and the plan must surface
  it rather than serve a gone object.

- **Count scale-down surplus rule pinned** (#756; PR #757): the highest
  slot orphans, whatever its address says.

- **Discovery splits verdicts from reports** (#751; PR #754), and the
  dead surface goes with it. **The model docs tell the whole truth**
  (#752; PR #753): the slot marker, the record store's four jobs, and a
  page for the disposable cache. The architecture review's three
  verified falsehoods are corrected in docs and comments (PR #749).

## choudoufu v0.9.0 (2026-09-02)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.9.0.json`](live/history/v0.9.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.8.0.json live/history/v0.9.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

FORK WORK:

- **The claim smokes: nine runnable claims, and the docs lead with them**
  (#713, #727, #732; PRs #718-#726, #729-#731, #733, #735, #737, #739).
  Every promise the fork makes is now a narrated smoke scenario with a
  BREAK control proven to catch the corruption it guards against:
  no-silent-orphans, no-self-managed-locks, staleness-costs-reads,
  backend-sets-itself-up, recovery-is-a-rerun, roundtrip,
  identity-is-a-tag, stock-when-you-need-it, and unchanged-is-free (the
  last needs no Docker, no emulator, and no credentials). The docs site
  gains a claims page walking each one phase by phase with a
  paste-and-go agent prompt, and the smoke harness gains per-run
  isolation, scenario listing, and narration throughout. Building claim
  2 surfaced and fixed a gap: `force-unlock` now refuses with the true
  reason - there is no lock to force open.

- **Record-envelope vouching, and reads that actually vanish** (#692
  increment 2; PRs #734, #744). On the `-refresh=false` path,
  record-attested ownership may stand in for the per-instance tags read
  when existence and identity are proven by the same run's listing pass
  (maintainer ruling on #692), and the prefetch launcher now skips
  planning wire reads for cache-served instances - a hit used to leave
  its read in flight anyway. Measured on real AWS where the tagging API
  does not index IAM: `plan -refresh=false` fell from 13 requests and 0
  hits to 5 requests with all 3 instances served; the staleness smoke's
  own fixture fell from 45 requests to 33. An adversarial review of the
  new surface then made the vouch pass hermetic: its failures degrade
  to reading instead of aborting the plan, and none of its products can
  make a run's output depend on whether a cache file was present.

- **The reads toggle** (#732; PR #737). The live block accepts
  `reads = "selective"` (the default) or `"full"`, with
  `CHOUDOUFU_READS` as the per-run override: full makes every plan pay
  every read regardless of flags. The toggle prices the plan and never
  changes it - the unchanged-is-free smoke asserts the outputs are
  byte-identical under both policies. Default plans read fully either
  way; drift detection never depends on this setting.

- **Migration gets its end state, and the trap before it a breadcrumb**
  (#716; PR #741). The documented migration now ends by turning the
  live block on, after which the ordinary commands run the live backend
  - and a stock-mode plan that would create marker-stamped resources
  from an empty state warns, naming the estate and both readings
  (bootstrap: proceed; mid-migration: turn the block on). A warning and
  never a refusal, silent whenever the prior state holds any managed
  resource. Guard warnings now print BEFORE the apply acts, including
  under -auto-approve and saved plans.

- **Number identity components record, and their records match** (#671;
  PRs #742, #744). An identity component the resource block carries as
  a number - an ECS task definition's revision - no longer skips the
  instance's record silently (the terralith's 78-of-79), and the
  superseded-claimant, tombstone and deposed matchers now compare
  number attributes through the same canonical rendering the writer
  uses. Write-back's unrecordable branch says so out loud once per
  type, because silent and deliberate must never look the same.

- **Leaving is documented, and the guard that makes it deliberate**
  (#659; PR #740). migrate.md now carries the deliberate exit (the
  roundtrip: one file, one edit, stock strips the markers) beside the
  unmigrate guard's exact boundary and its `CHOUDOUFU_UNMIGRATE`
  override - an unmigrated estate never meets the guard, which is what
  keeps the stock-parity claim's measurement intact.

- **The doc site's prose passed its own linter** (PR #728). Every
  hand-written page linted with the `sentences` trope linter and
  reworked: 332 findings down to 195 with genuine tropes at zero, and
  every claim scenario's narration held to score ~0 since.

## choudoufu v0.8.0 (2026-09-01)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.8.0.json`](live/history/v0.8.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.7.0.json live/history/v0.8.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

FORK WORK:

- **Fix: a fresh state cache no longer hides drift on default plans**
  (#712, PR #714). v0.6.0's cache hit rule substituted the per-instance
  read whenever the estate sweep vouched for an instance's marker, and
  the read is drift detection - an out-of-band attribute change on a
  verified instance was invisible while the cache was fresh. Affected:
  v0.6.0 and v0.7.0. Now a default plan reads every instance, restoring
  plan parity with stock's default, and only `-refresh=false` serves
  sweep-verified instances from the cache - the same trade stock's own
  flag names, made cheaper and safer here because the sweep verified
  existence and ownership moments earlier, where stock's flag verifies
  nothing. The unit guards gained the drift condition the equality
  guard could not see, red-proved from the failure itself.
- The user-path smoke test passes end to end again (#712, PR #714).
  `just demo` had been failing at its foreign-resource step since the
  account-inventory question became opt-in (#604), and - with nothing
  in CI running it - every later step was unreachable, which is how the
  drift regression above shipped twice. The harness now tests the #604
  narrowing both ways, asserts the cache half of the #685 ruling
  (written by plain apply, deleting it changes nothing), and its lint
  tables carry all 33 fixtures. Reviving it is what caught the fix
  above; making it un-rottable is #713, the versioned docker-compose
  smoke stack.

## choudoufu v0.7.0 (2026-09-01)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.7.0.json`](live/history/v0.7.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.6.0.json live/history/v0.7.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The board did not move; this release is the repository telling the truth
about itself. No resource-facing behavior changed.

FORK WORK:

- The stale-state ruling is pinned where it cannot drift (PR #701).
  HANDOFF's foundation section opens with the maintainer's ruling - the
  cache is never consulted for ownership, live wins any disagreement,
  losing it costs a slower run and nothing else - and
  `live/stale_state_ruling_test.go` fails if the lines are ever edited
  away.
- The prose-authority mechanism is dissolved (PR #701). The rulings/
  directory (previously rfc/, renamed once and regrown within two days)
  is gone: every decision document was re-homed verbatim onto the
  tracker issue its own header names, all 126 code and doc citations now
  name the issue, guard or fixture that holds the decision, and
  `live/decision_authority_guard_test.go` refuses a tracked rfc/,
  rulings/ or decisions/ directory, any dated decision-document
  citation, and any rfc/* or rulings/* branch. Upstream's website/ tree
  (448 files, never published by this fork) went with it.
- The language layer speaks v0.6.0's present tense (PR #701). The eleven
  live-mode refusal texts and meta_backend's diagnostic now refuse for
  the true reason - these commands operate on an AUTHORITATIVE state
  file, which a live block deliberately does not have - instead of
  claiming no file exists while the cache sits in the data dir. The
  refusals themselves are unchanged. storage.md becomes the first
  user-facing page to document the default cache: the file name, the
  `CHOUDOUFU_STATE_CACHE` override, and the `off` switch.
- The against-a-real-service test tier gates something (PR #704, #691).
  `floci-tier.yml` runs `make test-floci` nightly with no `|| true`; the
  recipe's scope now covers the seven gated files under tools/ it
  silently missed; and `live/floci_tier_gate_test.go` derives the gated
  roster from `flocitest.Gate` call sites, failing when a gated package
  escapes the recipe or the workflow disappears. The staterecord
  conformance run that would have caught v0.6.0's silent-List defect
  before it shipped is in that tier.

## choudoufu v0.6.0 (2026-09-01)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.6.0.json`](live/history/v0.6.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.5.0.json live/history/v0.6.0.json`):

- Core estates: 26/26 clear -> 26/26 clear (0)
- All estates: 27/27 clear -> 27/27 clear (0)
- Newly cleared: none
- Regressed: none

The board did not move because this release changes what runs underneath
it: every row's evidence predates the cache default below and stands at
the commit its own `last_run` records. The next sweep re-measures with
the cache on.

FORK WORK:

- The state file is back, as a cache (#685, PR #705). The maintainer's
  ruling - the cache is never consulted for ownership, live wins any
  disagreement, losing it costs a slower run and nothing else - is pinned
  by `live/stale_state_ruling_test.go`, and the cache now writes by
  default to `choudoufu-cache.tfstate` under the data dir (`.terraform`,
  or `TF_DATA_DIR`), the directory every OpenTofu gitignore already
  covers. `CHOUDOUFU_STATE_CACHE` overrides the path; the literal value
  `off` disables persistence. A plan serves an instance from the cache
  only when the estate sweep vouched for its marker in the same run.
- The guard that keeps the ruling true (PR #706):
  `TestCacheConditionsPlanIdentically` proves a fresh cache, a stale
  cache claiming the world is empty, and no cache at all plan
  byte-identically, with a built-in negative control that fails the test
  itself if the comparison goes vacuous.
- Record-store reliability, from a real failure (#688, #689, #693; PRs
  #702, #703). A leading-slash key was accepted then handled three
  different ways across the local, SSM and S3 stores - writes succeeded
  while List returned empty, which read as an empty estate and surfaced
  as a plan proposing to re-create live resources. Keys are refused
  loudly at validation now, conformance pins the contract across all
  three stores, and every store provisions a sentinel at construction
  and reads it back through List, so a store whose List is broken
  refuses the run instead of shaping the plan.
- Sweep vouching and IAM visibility (#692, PR #709). A sighting of a
  live object carrying a declared address's marker now vouches that
  instance for the cache instead of being discarded; IAM routes through
  the native sweep leg (the tagging API does not index IAM, probed
  against real AWS), which also closes a real hole: a second marked
  object carrying a declared IAM address was previously invisible to
  every leg. `Request.CacheVouchTypes` lists cache-candidate types once
  per run so their sightings reach the classifiers.
- Real-AWS wall-clock work (#654, #666, #683, PRs #676-#687): the
  plan-side throttle analyzer, choudoufu's own client request logging in
  the provider's wording, the scale-10 timeline, and the withdrawal of
  every earlier wall-clock ratio that compared a cached stock plan
  against an uncached choudoufu one (PR #684) - superseded by v0.5.0's
  like-for-like call-count figures.
- Cleanup (#694, #700; PRs #694, #707): three orphan tools and the
  artifact nothing read are gone; one `just demo-run <name>` recipe
  replaces 54 hand-cloned demo recipes, with every retired recipe's
  comment moved into its estate's own `run.sh` (line-level audit, zero
  lost lines).

## choudoufu v0.5.0 (2026-08-31)

Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.5.0.json`](live/history/v0.5.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.4.0.json live/history/v0.5.0.json`):

- Core estates: 25/25 clear -> 26/26 clear (+1)
- All estates: 26/26 clear -> 27/27 clear (+1)
- Newly cleared: none. Both sets were already whole at v0.4.0; the +1 is `terralith-scale`, a new core estate that clears on entry.
- Regressed: none
- Emulator repinned from `ghcr.io/lex00/floci@sha256:1c6450b8fe3618fca892ba5c2847f65e8d5ac29fe07f6eb497487b708ca85844` to `ghcr.io/lex00/floci@sha256:c55d74e13e96c8b132056677337dba0084bb0b427cb039be2dbf9a8b7efc0948`

PERFORMANCE:

A steady-state `choudoufu plan` on an adopted estate now costs what a stock plan costs, to within a handful of API calls, and issues them with the same concurrency. Measured on real AWS at 745 resources: 1399 calls against stock's 1392. Five changes, each measured with call counts held constant so the gain is overlap rather than doing less work:

- The estate-wide sweep no longer enumerates the whole admission table on an ordinary plan. It narrows to types the estate has evidence of, and takes the full universe when there is no record store, when the store will not list, or when its listing is empty - so a fresh or mid-migration estate still pays in full. `-adoption-only` and `TOFU_LIVE_COLLECT_UNCLAIMED` turn it back on.
- The read pass, the sweep's list calls, and `live-import`'s stamping all run concurrently, bounded by `TOFU_LIVE_READ_PARALLELISM`, `TOFU_LIVE_SWEEP_PARALLELISM` and `live-import -parallelism`, each defaulting to stock's 10.
- The record store is read once per run instead of per instance. A scale-1 plan made 377 round trips; it now makes one.
- A migrated estate's reads were still serialised after all of the above, because the record-first path intercepted them before the concurrent phase began. Provider requests now overlap ten-wide where they previously went one at a time.

BUG FIXES:

- A stateful plan on a migrated estate proposed removing every marker, and applying it silently un-migrated the estate. That plan is now refused, with `CHOUDOUFU_UNMIGRATE=<estate>` for a deliberate revert.
- A failed import proposed creating a duplicate of a live resource the run had listed alive seconds earlier. It now refuses when the provider's own enumeration saw the object, and still proposes the rebuild when only the tag index did.
- A `count.index` identity inside a module expanded with `for_each` was refused although stock plans it and the rendered names are distinct.
- A declared address refused after a replace because the destroyed object's tags stayed readable; the fix for that then pruned the deposed object a crash recovery needed.
- `aws_customer_gateway` was misclassified as not listable, so scaling a count down proposed no destroy at all.
- `live-plan` and `plan` under a live block had two separate refusal lists that had already drifted apart on `-destroy`, and the help text described neither.

DOCUMENTATION:

- Twenty-one claims on the compatibility reference were checked against source and twenty were stale, every one understating what the tool accepts - `for_each` keys containing `.` or `:`, identity arguments reading data sources, module outputs or functions, `count` on a module call, provisioners, `random_*` and `tls_*`, `local_file`, provider aliasing, and `live-import`'s module traversal. Two tests now hold that page to the constants and the linter.
- The record store's contents are stated plainly: it may hold any value the state file would have held, including secrets, unless `strict { secrets = "refuse" }` is set. `values.md` and its diagram said the opposite.
- The marker specification called a count-expanded module address "spec-only" while this fork writes it. A pin now holds that claim across all four pages that make it.
- Design records moved to `rulings/`, and the inherited upstream RFC directory and its process documents are gone.

## choudoufu v0.4.0 (2026-08-26)

[Release, notes and binaries](https://github.com/INTENTIUS/choudoufu/releases/tag/v0.4.0). Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.4.0.json`](live/history/v0.4.0.json).

BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/v0.3.0.json live/history/v0.4.0.json`):

- Core estates: 20/25 clear -> 25/25 clear (+5)
- All estates: 21/26 clear -> 26/26 clear (+5)
- Newly cleared: `corpus-alb-complete`, `corpus-autoscaling-complete`, `corpus-ecs-fargate`, `corpus-eks-basic`, `corpus-rds-complete-postgres`
- Regressed: none
- Emulator repinned from `ghcr.io/lex00/floci@sha256:a9dc5342c8f1ade656cd1c0cbd258dcadffda953fd1e33ecd345f1217085c79d` to `ghcr.io/lex00/floci@sha256:1c6450b8fe3618fca892ba5c2847f65e8d5ac29fe07f6eb497487b708ca85844`

ENHANCEMENTS:

- Four gauntlet crossing scripts (the security-group lookup in `corpus-autoscaling-complete`, the instance lookup in `corpus-sumaform-aws`, and the EIP lookups in `corpus-ec2-instance-complete` and `corpus-xancloud-iac`) no longer trust an AWS CLI server-side filter that floci's emulator silently ignores and pick the first result back. Each now lists candidates unfiltered where the filter cannot be trusted, matches the distinguishing attribute exactly on the client side, and fails loudly with the full candidate list if it finds zero or more than one match, instead of guessing.

BUG FIXES:

- `choudoufu live-mv` now moves the renamed resource's own record in the local record store on every rename, not only when the rename also crosses a module boundary. A same-module rename (for example, renaming `aws_sqs_queue.this` to `aws_sqs_queue.this_renamed` with no module step differing) previously left that resource's record filed under its old address forever, even though the live marker itself was rewritten correctly.
- The gauntlet runner now warns, instead of staying silent, when a crossing script speaks its protocol line but dies before reporting a single stage result. Previously the estate's entire prior stage row - including a full pass - was silently carried forward untouched and re-stamped with the new run's commit and exit code, so a genuine failure could be indistinguishable from an unrelated pass; a new test now asserts that a nonzero exit code always leaves visible evidence in the stage table.
- The public progress page and its homepage summary no longer claim a single "measured at commit X" instant for the whole board of estates. No procedure ever produced that fact honestly, since one gauntlet run measures a single estate, not the whole board, and rendering never advanced it either; they now show the pinned emulator image every estate ran against and the true range of each estate's own last-run dates instead.

## choudoufu v0.3.0 (2026-08-24)

[Release, notes and binaries](https://github.com/INTENTIUS/choudoufu/releases/tag/v0.3.0). Built on OpenTofu 1.13.0. Board snapshot: [`live/history/v0.3.0.json`](live/history/v0.3.0.json), taken at commit `9520a21de6` against emulator `ghcr.io/lex00/floci@sha256:a9dc5342c8f1ade656cd1c0cbd258dcadffda953fd1e33ecd345f1217085c79d`. There is no `v0.2.0.json` to diff it against, so the board figures below are the release's own, not a generated delta: 20 of 25 core estates clear, 21 of 26 overall, up from 16 of 24 core at v0.2.0's close.

ENHANCEMENTS:

- Record-primary identity (#364). Every managed instance now has one record - a single per-instance envelope holding identity, the arguments the provider never echoes back, sensitivity, provisioner taint and the managing provider - written by `live-import` and by every apply, and read first on the next plan, verified against the ownership marker, with a stale record falling back loudly rather than binding wrong. `terraform { live {} }` alone implies a local record store, the way stock implies local state.
- Schema-first identity (#387). Where the provider's own resource identity schema reproduces a hand-ratified table row (134 of 161 rows with a schema at aws 6.59.0), the schema wins at runtime; `live/rowgen-convergence.json` carries the measurement (retired in #695; the measurement is `live/schema-precedence.json` now).
- The plan-node seam (#388), experimental and off by default. Behind `CHOUDOUFU_NODE_RESOLVE=1`, identity is resolved during the plan walk - record, then marker index, then the identity table over the instance's real evaluated values - which resolves configuration shapes the static pass must refuse.
- Strict toggles (#365). `strict { secrets }` governs `aws_iam_access_key` and `aws_iot_certificate` (stored by default, the way stock stores them; refused under the toggle), and the new `strict { no_source_create }` picks refuse-or-create for an instance with no record, no marker and no derivable identity (default: refuse).
- Day-2 rename evidence (#357). The `day2_rename` stage passes on two estates: a `moved` block and `choudoufu live-mv` both rename with zero churn, the marker rewritten in place, stock's plan as the oracle.
- `choudoufu live-plan` now prints an unobtrusive discovery progress heartbeat to stderr - "discovering: N types scanned, M live resources found" - while it sweeps a large estate, instead of going silent for the whole scan. It never appears on stdout, so it cannot land in anything a script reads from the command.
- Tagged releases now also publish Windows binaries (amd64 and arm64), as `.zip` archives alongside the existing macOS/Linux `.tar.gz` ones.
- The `overlong-address` lint refusal now reports the exact split between a resource's module path and its own address, plus concrete remedies (shorter module names, flattening a level of nesting, a shorter label or `for_each` key, or `choudoufu live-mv`), instead of only the total character count.
- The pinned floci image is now built from the fork's own `main`, adding EC2 launch-template/metadata, autoscaling-policy, CloudWatch-alarm, SSM public-AMI-parameter and RDS fixes.

## choudoufu v0.2.0 (2026-08-12)

[Release, notes and binaries](https://github.com/INTENTIUS/choudoufu/releases/tag/v0.2.0). Built on OpenTofu 1.13.0. No board snapshot: the gauntlet did not exist yet. macOS and Linux binaries, amd64 and arm64.

UPGRADE NOTES:

- The Go module path is now `github.com/intentius/choudoufu`, not `github.com/opentofu/opentofu`, and the fork's own tree moved from `internal/stateless` and `stateless/` to `internal/live` and `live/`. Every path and import in this repository and in the docs moved with it; `tools/rename-phase/rename.sh`, in git history at `492490cc2`, records the transformation.

ENHANCEMENTS:

- Release binaries embed the tag they were built at, so `choudoufu version` names the fork release and the upstream OpenTofu version it is built on.
- The admitted AWS type list grew over four batches (#19): KMS keys and aliases, Route 53 zones and records, the four S3 bucket children, CloudWatch metric alarms, IAM role policies, SNS topics, and the ELBv2 chain with the account-derived pair that goes with it. One place counts the admitted types and a test holds it there, instead of a number repeated across pages.
- The identity table is checked against the provider's own served identity schemas, and the AWS admission survey is generated from those schemas rather than hand-maintained.
- A docs site, a logo, and an install path that points at the release binaries.

## choudoufu v0.1.0 (2026-08-12)

[Release and binaries](https://github.com/INTENTIUS/choudoufu/releases/tag/v0.1.0). Built on OpenTofu 1.13.0. The first tagged build of the fork; macOS and Linux, amd64 and arm64. Its GitHub release carries no notes, so this entry is written from the tree at the tag.

UPGRADE NOTES:

- The built binary in this fork is named `choudoufu`, not `tofu`. Build with `go build ./cmd/choudoufu`; every command's help and usage text names the binary accordingly.

EXPERIMENTS:

- **Live resource markers** - fork-only, experimental: no state file, backend, or lock; prior state is rebuilt from the live system each run via ownership tags (tofu-estate/tofu-address/tofu-slot). Opt in with a `live` block; new `choudoufu live-plan` and `choudoufu live-mv` commands, EXPERIMENTAL in their help. At v0.1.0 this covered AWS only, 16 types, and the root module only; the admitted list and the module shapes have grown in every release since. The current limits are [`live/LIMITATIONS.md`](live/LIMITATIONS.md); the marker format is [`live/MARKERS.md`](live/MARKERS.md); the [documentation site](https://intentius.io/choudoufu/docs/) is the narrative version. (Through v0.4.0 this entry said "stateless mode" and pointed at a "Stateless Mode docs page" that has never existed under either name.)
- Unowned live resources are rendered as their own section of the plan, rather than being invisible.
- The marker lint refusals shipped with the release: the 256-character marker address cap, receipt hash-only values and secrets discipline, and the unadmitted-type rule.

# OpenTofu

Everything below this line is upstream OpenTofu's changelog for the version this fork is built on. Fork changes are recorded in the choudoufu section above.

The v1.13.x release series is supported until **August 1 2027**.

## 1.13.0 (Unreleased)

UPGRADE NOTES:

- The "winrm" connection type for provisioners is no longer supported. ([#4012](https://github.com/opentofu/opentofu/pull/4012))

    This connection type was deprecated in OpenTofu v1.12, and now removed in v1.13. Some of the upstream libraries OpenTofu was using to implement these features are no longer maintained, so it's not viable for us to offer this anymore.

    [Modern Windows versions now support OpenSSH](https://learn.microsoft.com/en-us/windows-server/administration/openssh/openssh_install_firstuse), and so we suggest that anyone currently relying on WinRM plan to migrate to using SSH instead.

- There are various minor changes to the robustness of file format and wire format parsers in the SSH client implementation used for remote provisioners.

    This may cause certain invalid input that was previously accepted to now be rejected, in an attempt to better match the expectations of other implementations of these protocols and formats.

ENHANCEMENTS:

- The `gcp_kms` key provider now supports an optional `additional_authenticated_data` as part of the encryption and decryption operations. ([#4287](https://github.com/opentofu/opentofu/pull/4287))
- The AWS KMS key provider for state encryption now supports an `encryption_context` field, allowing key-value string pairs to be passed to AWS KMS with every `GenerateDataKey` and `Decrypt` call. ([#4298](https://github.com/opentofu/opentofu/pull/4298))
- The `cidrsubnets` function now supports prefix extensions greater than 32 bits when the base CIDR block uses an IPv6 address. ([#4042](https://github.com/opentofu/opentofu/pull/4042))
- The `local-exec` provisioner now automatically sets the `TRACEPARENT` environment variable in child processes when OpenTelemetry tracing is active, following the W3C Trace Context specification. ([#4014](https://github.com/opentofu/opentofu/issues/4014))
- When OpenTelemetry trace collection is active, OpenTofu now copies any log lines generated by the OpenTelemetry libraries into its own debug log stream that you can activate using the `TF_LOG` environment variable. ([#4285](https://github.com/opentofu/opentofu/issues/4285))
- When installing provider and module packages from OCI Distribution registries, OpenTofu now tracks separate transient credentials for each repository to support registry implementations that issue repository-scoped tokens.  ([#3316](https://github.com/opentofu/opentofu/issues/3316))
- The `providers lock` command now supports the argument `-oci-mirror`. The functionality mimics that of the field `repository_template` of `oci_mirror`-block in [`provider_installation`](https://opentofu.org/docs/cli/config/config-file/#provider-installation) with the exception of using a URI template instead of a HCL one.
- The OpenBao key provider accepts a new `associated_data` (known as AAD) argument, allowing a base64-encoded value to be passed to OpenBao on every data key generation and decryption call. ([#4365](https://github.com/opentofu/opentofu/pull/4365))
- `tofu plan` no longer prints the explanatory paragraph that followed the "No changes. Your infrastructure matches the configuration." message, since it only restated that message in more words. ([#4340](https://github.com/opentofu/opentofu/issues/4340))

BUG FIXES:

- `tofu workspace new` now includes a hint to use `tofu workspace select` when the given workspace name already exists, instead of just reporting that it already exists. ([#4428](https://github.com/opentofu/opentofu/issues/4428))
- `tofu apply -json` now emits periodic `apply_progress` heartbeat messages for the full duration of a resource operation, instead of stopping after the first one. ([#4107](https://github.com/opentofu/opentofu/pull/4318))
- The built-in function `contains` now accepts `null` as its second argument, to test whether a collection contains any null values. ([#4043](https://github.com/opentofu/opentofu/issues/4043))
- The built-in function `merge` no longer fails when its only argument is a null value of an object type. ([#4043](https://github.com/opentofu/opentofu/issues/4043))
- The built-in function `cidrhost` no longer returns a "panic" error when called with an out-of-range host number represented in more than 64 bits. ([#4056](https://github.com/opentofu/opentofu/pull/4056))
- provisioner output is no longer suppressed when `-show-sensitive` is passed. ([#3927](https://github.com/opentofu/opentofu/issues/3927))
- In the `azurerm` backend's OpenID Connect authorization method, when `audience` is provided as a query parameter in the URL, it will be passed through instead of being overwritten by a default value. ([#4037](https://github.com/opentofu/opentofu/pull/4037))
- Using `-backend=false` during `tofu init` now skips reading the local encrypted state ([#4077](https://github.com/opentofu/opentofu/pull/4077))
- Fixed span error status not being set on module fetch failure path during `tofu init`, so observability tools now correctly identify failed spans. ([#4169](https://github.com/opentofu/opentofu/issues/4169))
- Fixed TRACESTATE log message incorrectly printing the TRACEPARENT value instead. ([#4168](https://github.com/opentofu/opentofu/issues/4168))
- Fix rendering of plans where a nested block's replacement is unknown. ([#4256](https://github.com/opentofu/opentofu/issues/4256))
- `errored.tfstate` is now produced during a go runtime panic. This file will be a partial state and is intended for aiding in recovery from a hard crash. ([#4064](https://github.com/opentofu/opentofu/pull/4064))
- `removed` blocks with an invalid `from` address and a destroy provisioner now report a configuration error instead of crashing. ([#4321](https://github.com/opentofu/opentofu/pull/4321))
- `tofu plan -out` no longer fails when the plan includes a resource with `lifecycle { destroy = false }` that needs replacement, which previously errored with `invalid change action ForgetThenCreate`. ([#4324](https://github.com/opentofu/opentofu/issues/4324))
- `connection.script_path` is escaped correctly not allowing anymore additional commands to be executed on the remote host together with the script path indicated by the argument. ([#4330](https://github.com/opentofu/opentofu/pull/4330))
- `tofu plan`: Fixed Incorrect warnings produced during plan -replace ([#4368](https://github.com/opentofu/opentofu/issues/4368))

## Previous Releases

For information on prior major and minor releases, refer to their changelogs:

- [v1.12](https://github.com/opentofu/opentofu/blob/v1.12/CHANGELOG.md)
- [v1.11](https://github.com/opentofu/opentofu/blob/v1.11/CHANGELOG.md)
- [v1.10](https://github.com/opentofu/opentofu/blob/v1.10/CHANGELOG.md)
- [v1.9](https://github.com/opentofu/opentofu/blob/v1.9/CHANGELOG.md)
- [v1.8](https://github.com/opentofu/opentofu/blob/v1.8/CHANGELOG.md)
- [v1.7](https://github.com/opentofu/opentofu/blob/v1.7/CHANGELOG.md)
- [v1.6](https://github.com/opentofu/opentofu/blob/v1.6/CHANGELOG.md)
