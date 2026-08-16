# Handoff

Rewritten 2026-08-16 after a 97-commit session. Read
`.claude/agents/live-markers.md` before touching anything — it now carries the
verification budget, the defect-provenance record, and the two stall rules,
all of which were learned expensively today.

**Work lives in the tracker.** `gh issue list -R INTENTIUS/choudoufu` — a bare
`gh` hits opentofu/opentofu. This file carries only what rots within a
session. Everything durable is on an issue. Do not put findings here; a prior
version of this file was wrong four separate times.

## The ladder — recompute it, never carry the number forward

    just corpus && python3 -c "import json;print({c['class']:c['configs'] for c in json.load(open('live/corpus-refusals.json'))['ladder']['classes']})"

**Every number below is read from the `live/corpus-refusals.json` committed in
this same commit.** An earlier version of this file quoted numbers from an
uncommitted regeneration and two rows were wrong at the moment they were
written - including a fabricated zero that sent a reader after a same-day
close. Commit the artifact and the file together, or the numbers cannot be
checked.

| rung | session start | now |
|---|---|---|
| clean | 0 | **25** |
| backend-only | 25 | 0 |
| admissions-only | 17 | 20 |
| data-read-eligible | 23 | 33 |
| language-blocked | 79 | **66** |
| unreadable | 1 | 1 |

Both sum to 145.

### What "clean" does and does not mean

`checked_layers` is now **lint, identity, dataread, stamp**. `discovery` and
`projection` remain unchecked. So `clean` means "nothing in four of six
layers refused it", not "this applies end to end".

Two things a reader must know before quoting 25:

- **All of them still ship a `backend` or `cloud` block.** `state-backend` was
  demoted from a fatal refusal to a warning (#214, maintainer decision), so
  `ClassifyOnboarding`'s `backendOnly` branch can structurally never be true
  again. `live-plan` never reads `mod.Backend` — the backend is orthogonal to
  the live path by construction, not stubbed. But "clean" and "backend-only"
  collapsed into one bucket, and 25 configs needing zero attention is not the
  same claim the old 25-needing-one-edit rung made.
- **Stamp only became a checked layer today** (#224), which took `clean`
  26 → 24 late in the session; later identity work took it back to 25. `catalog.go` had claimed stamp's refusals
  need a live object; false for all four of them, and that false claim is why
  the layer went unchecked.

The supportable sentence is *"25 of 145 pass the four offline checks this
instrument runs"* — not "work as published".

## Burndown

19 wall-class issues closed: #185 #186 #188 #190 #191 #192 #194 #195 #198
#199 #200 #201 #202 #203 #205 #206 #207 #208 #210, and #189. Plus #211 #213
#215 #216 #217 #218 #222.

**#214 is OPEN**, not closed - the state-backend severity decision. Its body
reads as a finished record, but its own "Known follow-ups" section lists
unresolved dead-code and rung-retirement items. An earlier version of this
file listed it as closed; it was not.

Still open. **Deliberately no per-issue counts here.** An earlier version of
this file carried a burndown table; it was stale within twenty minutes twice,
and once carried two rows that were wrong at the moment they were written. The
numbers move every time an agent lands, so a table in a handoff is a trap by
construction — the file's own first rule is recompute, never carry forward.

Get them from the artifact:

    python3 -c "import json;d=json.load(open('live/corpus-refusals.json'));[print(f\"{r['configs']:>4} cfg {r['sites']:>6} sites  {r['id']}\") for r in sorted(d['refusals'],key=lambda x:-x['configs'])[:12]]"

## Ranking — assign from #178, not from config counts

**The assignment order is the greedy marginal-cover table on #178.** It was
recomputed against the committed artifact by independently re-implementing
`ClassifyOnboarding` in Python (0 mismatches against the Go classifier), over
the **56 winnable estates** — the 65 language-blocked minus 9 that stock
OpenTofu refuses identically for missing tfvars, which is parity, not a
defect. Do not rank by corpus-wide config count: that counts the 105 fixtures
that are not rate-capable published deployments, and it counts a class as
valuable even when everything it touches is blocked by four other things.

**Sole-blocker count is also the wrong order, and this is the subtle one.**
`Module output not supported in static context` frees 1 estate alone and
**+10** at greedy step 10 — ranked by sole blockers it looks worthless. That
is the same failure mode as the retired greedy-cover table this replaces.
Use the marginal ordering.

Two facts that reframe the campaign, both on #178:

- **The open tracker frees zero estates.** Every wall issue that was open when
  this was computed is sole blocker on 0. Every class that *is* someone's sole
  blocker was untracked; #233 and #234 now exist for them.
- **#233 is step 1, +9** — `Unmarked apply of a marker-only resource`, the
  largest sole-blocker count in the campaign. It entered the ladder only when
  stamp became a checked layer (#224, `2ffa5f33b2`), so it predates no triage.
  `ServerAssigned` types carrying no `tags` argument, where `mustStamp` is
  fatal because the marker is the only handle. Two escapes already sit in the
  generated rows' own `Reason` text.

**#236 is the process defect**: eight wall issues closed COMPLETED with no
measured improvement, holding 6 sole blockers between them. A closure needs a
measured before/after, not an argument.

**#204 and #209 are open against refusals that fire nowhere** — 0 configs, 0
sites across all 250 fixtures.

## The instrument overstates, and by how much

Agents measure with an offline `check.Load`/`check.Analyze` probe because
`just corpus` stalls them. That probe runs **without provider schemas**: it
sees the sites a fix clears and cannot see the ones that surface underneath.
It is a systematic upper bound, not an error, and every agent number carries
it.

The conditional fix (#196) is the worked example. Its branch measured 11
sites / 5 configs cleared. Schema-backed regeneration:

    Identity not resolvable from configuration  22 cfg/118 sites -> 17/104
    Unresolvable identity                       44 cfg/169 sites -> 45/183
    total refusal sites 10046 -> 10046, ladder unchanged

Fourteen sites relabelled, one config *gained* a refusal, no estate moved.
Only the schema-backed regeneration counts. Run it at merge, never before.

**#230 is the highest-priority open bug**: the stamp gate checks
`len(Schemas) > 0` across all providers, so a partial schema acquisition
fabricates hard refusals. Reproduced with a test. 33 of 250 corpus entries
already show partial acquisition.

Filed today and not yet triaged: #212 #219 #220 #221 #223 #225 #226 #227
#228 #229 #230 #231 #232. Several are `bug`, not `enhancement`.

## Binding rules (maintainer directives)

- **Parity is the bar.** Match stock OpenTofu, go no further.
- **Everything must be derived.** A fix naming a concrete type in control
  flow buys one cohort. Make every brief report how many OTHER things it
  moved. Two fixes today failed this test after landing — `count.index`
  enumerated unsafe shapes and defaulted to safe, and `parentRef` named the
  literal `"name"` and missed 17 sibling cases.
- **Fable is forbidden for subagents.** Never spawn one without asking.
- **Pass an explicit `model` on every spawn.**
- **A closed issue needs a closing comment with the number that changed.**
  Eight closures this session had none; the fixes were real but a verifier
  had to reconstruct them with `git log --grep`.

## What the audits are for

Seven adversarial audits ran today. They found **fifteen** real defects in work
that was green, committed and believed finished — including a duplicate
marker, a fabricated refusal, a marker-grammar collision that could point
`live-mv` at the wrong live resource, and two commits whose own messages made
claims their authors had reproduced as true.

**CI caught none of them.** Tests here are a regression gate, not a
defect-finder. Budget accordingly: an extra audit pass buys more than an
extra CI run.

## Pins

floci (fork lane, lex00/floci, checkout `/Users/alex/Documents/checkouts/floci`):

- fork main `05573b5e`; published image
  **`sha256:1362e856baf70b1fc848ce302c308dfa8ad39a30187812e855bc295e77a9d933`**.
- **Re-pinned.** All three batch-3 fixes were verified live with before/after
  against both images rather than read from commit messages. Acceptance moved
  3/31 → 4/31, the one flip attributable to the CBOR fix.
- Two items on lex00/floci#50 were misdiagnosed: the ACM PCA and SageMaker
  "crashes" are provider-side panics in `expand*` functions that run before
  any request reaches floci. Not fixable in the emulator.
- **New:** floci's tagging index returns empty for tagged resources —
  reproduced with raw AWS CLI, zero choudoufu code involved (#229). Already
  recorded for `aws_iam_role`; now confirmed for EBS volumes too.

## Traps (all live, several burned this session)

- `env -u PWD` for every go command (symlink trap).
- Never pipe a generator into `head` — SIGPIPE. The e2e harness had **58**
  producer-side instances and two consumer-side ones; all are fixed, and
  `just demo` now exits 0 end to end through step 14.
- Read CI and corpus results from a file, never through a pipe or a trailing
  `echo` after a semicolon. I hit this myself: the wrapper reported success
  while the file said `exit: 1`.
- **Run a generator twice and diff before trusting an artifact.** `just
  corpus` was silently nondeterministic — go-plugin's handshake fails
  sporadically across ~75 subprocess spawns. Fixed, but two verbatim copies
  of that acquisition code exist in `tools/survey-gen` and `tools/estate-gen`
  and only got the fix later.
- Cohort ownership split is enforced: `GENERATED.md` and `.tf` are
  estate-gen's; `README.md` is hand-owned ratification evidence.
- `live/rowgen-convergence.json` is NOT coverage.
- **Two agent branches can merge cleanly in text and still be semantically
  incompatible** — one wrote a test against a function signature the other
  changed. The merge gate caught it; a per-branch green does not.
