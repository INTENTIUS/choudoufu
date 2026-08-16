# Handoff

Rewritten 2026-08-16 after a 200-commit session. Read
`.claude/agents/live-markers.md` before touching anything — it carries the
verification budget, the two stall rules, and the splitting rule, all of
which were learned expensively.

**Work lives in the tracker.** `gh issue list -R INTENTIUS/choudoufu` — a
bare `gh` hits opentofu/opentofu. This file carries only what rots within a
session. Do not put findings here; a prior version was wrong four separate
times.

## Recompute, never carry forward

    just corpus && python3 -c "import json;print({c['class']:c['configs'] for c in json.load(open('live/corpus-refusals.json'))['ladder']['classes']})"

**Deliberately no ladder table in this file.** Two earlier versions carried
one; it was stale within twenty minutes both times, and once carried two
rows that were wrong at the moment they were written.

Two caveats that must travel with any ladder number:

- `checked_layers` is **lint, identity, dataread, stamp**. `discovery` and
  `projection` are unchecked. `clean` means "nothing in four of six layers
  refused it", not "this applies end to end".
- Every `clean` estate still ships a `backend` or `cloud` block.
  `state-backend` was demoted to a warning (#214), so `backendOnly` can
  structurally never be true again.

The supportable sentence is *"N of 145 pass the four offline checks this
instrument runs"*.

## Ranking — assign from #178's greedy table

Recomputed by re-implementing `ClassifyOnboarding` in Python (0 mismatches
against the Go classifier) over the **56 winnable estates**: the 65
language-blocked minus 9 that stock OpenTofu refuses identically for missing
tfvars, which is parity, not defect.

**Do not rank by corpus-wide config count** — it counts the 105 fixtures
that are not rate-capable published deployments. **Do not rank by
sole-blocker count either**: `Module output not supported in static context`
frees 1 estate alone and **+10** at greedy step 10. Use the marginal
ordering.

Two facts that reframe the campaign:

- **When it was computed, every open wall issue was sole blocker on 0.**
  Every class that *was* someone's sole blocker was untracked. #233 and #234
  now exist for them.
- **#233 is step 1, +9** — the largest sole-blocker count in the campaign.
  It entered the ladder only when stamp became a checked layer, so it
  predates no triage.

**Every wall-issue title still says "blocks N of 79".** Those were honest
when written — an agent recovered the LB=79 artifact and recomputed all 26,
and every one matched. The population has since moved. Do not read a title
as a current number.

**#236 is the process defect**: eight issues closed COMPLETED with no
measured improvement, holding 6 sole blockers. One re-verification (#186)
upheld its closure, by falsification rather than by accepting the
attribution. The rest did not.

## The instrument overstates, and by how much

Agents measure with **`tools/refusal-probe`** (built this session so nobody
rebuilds it a thirteenth time):

    go run ./tools/refusal-probe -out before.json      # 19.6s, all 250 entries
    go run ./tools/refusal-probe -diff before.json,after.json
    go run ./tools/refusal-probe -entry .corpus/vpc -v

It writes where you point it, so several agents can measure concurrently in
one tree — `just corpus` cannot. It reports per-entry and per-refusal-ID
deltas and flags entries that got **worse**.

It runs **without provider schemas**: it sees the sites a fix clears and not
the ones that surface underneath. Worked examples from one session:

- #196's first half claimed 11 sites cleared. Schema-backed: 10046 → 10046,
  fourteen sites merely relabelled into another refusal ID.
- Its second half claimed 60 from a **per-entry probe against real schemas
  from the warm plugin cache**, and delivered exactly 60.

The difference is the instrument, not luck. Sweep offline; verify the
entries you care about with real schemas before reporting a number as
anything but an upper bound.

Two probe blind spots found the hard way: a rule that returns false when
`schemas == nil` is **invisible** to it (its zero is not evidence), and an
entry with registry module calls is measuring roughly a sixth of its refusal
surface — one went 59 sites → 394 once its modules were installed.

## Binding rules (maintainer directives)

- **Parity is the bar.** Match stock OpenTofu, go no further. If upstream
  accepts what we refuse, that is a defect.
- **Everything must be derived.** A fix naming a concrete type in control
  flow buys one cohort. Make every brief report how many OTHER things it
  moved.
- **A type in `rejected.json` is debt.** Sanctioned exclusions are exactly
  four: `aws_iam_access_key`, `aws_iot_certificate`,
  `aws_ivs_playback_key_pair`, `aws_appstream_directory_config`.
  `aws_secretsmanager_secret_version` was removed from that list by ruling —
  the marker goes in a tag, not in the secret.
- **Non-AWS is refused explicitly** (#243), derived from the marker grammar
  rather than a provider check.
- **Fable is forbidden for subagents.** Pass an explicit `model` on every
  spawn.
- **A closed issue needs a closing comment with the number that changed.**

## What the audits are for

Ten adversarial audits have found **twenty-five real defects in work that
was green, committed and believed finished**. **CI caught none of them.**
Tests here are a regression gate, not a defect-finder. An extra audit pass
buys more than an extra CI run.

The two worst this session, both found by audit and neither by CI: four
`sensitive = true` variables that **crashed the run** out of `check.Dir`,
and a for-comprehension over a list that silently resolved two instances
with fabricated keys while reporting `readable=true blocked=false` and zero
findings.

`internal/live/marksafe` now derives the mark-unsafe cty surface by
reflection and requires every call site to carry a proof. It caught two real
regressions in its first day. **Any new call to `AsString`, `True`, `False`,
`ElementIterator` and friends needs a guard**, and a new package under
`internal/live` must be classified.

## Open, highest value first

- **#244 — wrong marker, the most serious open defect.** `checkOwnership`
  reads only `tofu-estate` and never compares `tofu-address`; discovery
  skips a declared address deferring to a projection check that does not
  exist. Result: an instance adopts another instance's live object, the plan
  rewrites that object's address marker, and the displaced object is leaked.
  Stock destroys and recreates; nothing leaks. Fix must use
  `markers.AddressMatches` and must not break `moved` blocks, which
  legitimately leave the old address on the object until the tag is
  rewritten — `moved.Origins(moved.Honoured(cfg), addr)` is the acceptable
  set.
- **#233 step 1 (+9 estates).** Bucket E stays refused; both escape hatches
  were refuted by the provider's own data. CLOUD's severity needs the
  account ID, which has no source today.
- **#245 — the admission denominator is 1699, not 86.** 944 admitted + 86
  vetoed + 669 unreached, exact. The schema fallback rescues 60, so 609 are
  hard errors. `rejected.json` is a veto set, not a coverage ledger.
- **#187 bucket 2** — 22 ACM sites, 3 sole-blocked, the whole of greedy
  step 2.
- **#193 stays open**: stock accepts all 13 sites. Shapes C and D (7 of 13)
  have no offline answer.

## Pins

floci (fork lane, lex00/floci, `/Users/alex/Documents/checkouts/floci`):

- fork main `6f030e96`; published image
  **`sha256:a1c729f445a96fce8858ac45318d5188b5c2afc76a06e819f234326d52e6bd5f`**,
  which is what `live/floci-image` now pins.
- **#229 is fixed and the fix is verified from this side.** The failure mode
  was *not indexed*: `ResourceGroupsTaggingService` read a map only 2 of 64
  services wrote to. The fix unions it with a live read of every service's
  stores through `StorageFactory`. Re-probed on the new digest 2026-08-16:
  `floci-capability-gen -mode=tagging` is 7/7 implemented (was 0/7), and
  `TestTaggingSweepAgainstFloci` now asserts its bind instead of skipping -
  `listed=1 source=tagging`, orphan found, `other-estate=0`.
- **The union index honours `TagFilters`**, which the floci-side oracle could
  not show because it probes unfiltered. Direct probe across two estates:
  3 hits unfiltered, 2 for `tofu-estate=alpha`, 1 for `beta`, 0 for an absent
  value, 3 key-only, 0 for an absent key, and `ResourceTypeFilters` narrows
  too. So `sweepViaTagging` sees no foreign-estate ARNs and cannot raise
  spurious `ProblemUnsweepableOwnedType`/`ProblemUnresolvedTaggedARN`.
- **`live/cohort-acceptance.json` has NOT been re-measured** and still records
  `sha256:1362e856…`. It is listed in `staleFlociMeasurements` with that
  reason; the re-measure is its own slot.
- `live/cohort-acceptance.json` is **4 pass / 27 fail of 31**, not 3/28, and
  all 27 failures are `phase: "apply"`. Those runs predate the
  `!isEmulatorEndpoint` gate, so they had `TaggingSweep=true` against a blind
  index. The verdicts stand; the 4 passes' removal leg was inert.
- `TaggingSweep=false` does **not** disable the removal leg — it chooses how
  candidates are gathered, one `GetResources` versus ~950 per-type List
  calls. The gate's cost is time and coverage, not correctness.

## Traps (all live, several burned this session)

- `env -u PWD` for every go command (symlink trap).
- **Read every exit code from a file.** A background wrapper reported exit 0
  today while its log said 1 — the compound command's status was the
  trailing `echo`'s.
- Never pipe a generator into `head` — SIGPIPE.
- **Run a generator twice and diff before trusting an artifact.**
- **Verify a closure with `git merge-base --is-ancestor`, not `git log
  --grep`.** #227 was closed citing a commit that was never on main; a
  matching grep only proves the commit exists somewhere.
- **A generated artifact conflicts on merge — regenerate, never hand-merge.**
  `live/rowgen-convergence.json` and `live/survey-full.json` both did.
- `.gitignore`'s `/.corpus/` does **not** match a symlink named `.corpus`.
- A new tool binary needs a `.gitignore` entry; `TestEveryToolHasAGitignoreEntry`
  enforces it.
- Cohort ownership split is enforced: `GENERATED.md` and `.tf` are
  estate-gen's; `README.md` is hand-owned.
- `live/rowgen-convergence.json` is NOT coverage.
- **Two agent branches can merge cleanly in text and still be semantically
  incompatible.** One wrote a test against a signature the other changed;
  another carried expectations that predated a classifier fix.
- **A test nothing runs is not a test.** `live/e2e/record-store/` was red on
  main and wired to no recipe, no CI step, no README mention.
