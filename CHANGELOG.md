# choudoufu releases

choudoufu tags its own `v0.x` line on top of an upstream OpenTofu version. Both numbers appear in `choudoufu version` and in every release's notes: the fork tag, and the OpenTofu release the tree is built from. This section is the fork's changelog; upstream's own, for that OpenTofu version, follows below under "OpenTofu" and is left in upstream's shape.

**Fork work is recorded here, not in upstream's section.** An entry filed under upstream's `1.13.0 (Unreleased)` heading says "unreleased" about something that shipped, which is how four tagged releases came to have no changelog entry naming any of them. To cut a release: date the `(Unreleased)` heading below, open an empty one above it, and take the board movement from `go run ./tools/gauntlet notes live/history/<previous>.json live/history/<new>.json` against the snapshot `go run ./tools/gauntlet snapshot <version>` writes, rather than retyping a count by hand.

## choudoufu v0.10.0 (Unreleased)

Nothing recorded yet.

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
