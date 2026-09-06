# choudoufu releases

choudoufu tags its own `v0.x` line on top of an upstream OpenTofu version. Both numbers appear in `choudoufu version` and in every release's notes: the fork tag, and the OpenTofu release the tree is built from. This section is the fork's changelog; upstream's own, for that OpenTofu version, follows below under "OpenTofu" and is left in upstream's shape.

**Fork work is recorded here, not in upstream's section.** An entry filed under upstream's `1.13.0 (Unreleased)` heading says "unreleased" about something that shipped, which is how four tagged releases came to have no changelog entry naming any of them. To cut a release: date the `(Unreleased)` heading below, open an empty one above it, and take the board movement from `go run ./tools/gauntlet notes live/history/<previous>.json live/history/<new>.json` against the snapshot `go run ./tools/gauntlet snapshot <version>` writes, rather than retyping a count by hand.

## choudoufu v0.14.0 (Unreleased)

Nothing recorded yet.

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
- Schema-first identity (#387). Where the provider's own resource identity schema reproduces a hand-ratified table row (134 of 161 rows with a schema at aws 6.59.0), the schema wins at runtime; `live/rowgen-convergence.json` carries the measurement.
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
