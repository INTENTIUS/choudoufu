# Handoff

Rewritten 2026-08-16 at `0a8dc1c053`, working tree clean, full `just ci` green
(read from a file, not a trailing echo). Read `.claude/agents/live-markers.md`
before touching anything.

**Work lives in the tracker.** `gh issue list -R INTENTIUS/choudoufu` — a bare
`gh` hits opentofu/opentofu. This file carries only what rots within a session:
model rules, budget, what is mid-flight, and the pins. Everything durable is on
an issue. Do not put findings here; they have been wrong four times.

## The only number that matters

**0 of 145 real published deployments work as published.** That has been true
every day of this campaign and is still true.

145 rate-capable estates, from `live/corpus-refusals.json`'s ladder:

| rung | means | count |
|---|---|---|
| clean | works as published | **0** |
| backend-only | works after deleting one `backend` block | 25 |
| admissions-only | needs our type admissions, no user edit | 17 |
| data-read-eligible | needs our pre-plan read, no user edit | 23 |
| **language-blocked** | **the user must rewrite their config** | **79** |
| unreadable | loader cannot read it | 1 |

The wall is the product. 114 → 87 → 79 over the campaign; 87 → 79 on
2026-08-15/16. Recompute it, never quote it:

    just corpus && python3 -c "import json;print({c['class']:c['configs'] for c in json.load(open('live/corpus-refusals.json'))['ladder']['classes']})"

## Burndown

Every blocking class is now its own issue, label `wall-class`, **#184–#210**,
27 of them, each carrying its estate list, site count, sole-blocked count and
acceptance criterion. #178 is the parent campaign.

    gh issue list -R INTENTIUS/choudoufu --label wall-class --state open

Ranked by estates freed in greedy-cover order at `0a8dc1c053` — 9 classes free
44 of the 79, and **35 estates need something outside those 9**:

| free | class | issue |
|---|---|---|
| 10 | Unresolvable identity (rides on others; no machinery of its own) | #184 |
| 8 | Non-static identity argument (mostly unmeasurable, see below) | #186 |
| 6 | Non-static count expression | #194 |
| 6 | Non-static for_each expression | #187 |
| 4 | module-providers (parity gap, core already merged) | #188 |
| 4 | Identity argument not set | #190 |
| 2 | Two resources with the same identity | #200 |
| 2 | moved-block | #198 |
| 2 | count-index | #192 |

Highest-value next spend is **#188**: the resolution core (`internal/live/providerscope`)
is built, tested and merged; four call sites must be threaded **in one change**
because `providerConfigAddr` and `inScope` are consistently wrong together and
fixing either alone breaks scoping. Details on the issue.

Read #186 before spending on it. Its population is largely unset-variable
artifact, and what survives is govuk-aws, which ships no tfvars at all.

## Binding rules (maintainer directives)

- **Parity is the bar.** Match stock OpenTofu, go no further. If OpenTofu
  refuses the same construct, matching that refusal is correct and the class
  closes as a documented limitation. If OpenTofu accepts what we refuse, that
  is a defect. This killed four hand-written `live/corpus-vars/` files whose
  values were composed from backend-config filenames; estates are now measured
  with the tfvars they themselves ship, and three estates got *worse* as a
  result. That was the right trade and the regression was reported, not offset.
- **Everything must be derived.** The AWS provider has ~1700 types. A fix that
  names a concrete type in generator control flow buys one cohort and nothing
  else. The test that catches this: **make every brief report how many OTHER
  things the change moves.** A fix that moves exactly its motivating case is a
  special case wearing a rule's clothes. It caught one detour this wave (a
  hand-wired `capacity_provider_arns`), and the replacement rule wired 12
  arguments across 7 cohorts.
- **Fable is forbidden for subagents.** Never spawn one without asking the
  maintainer by name. One evening of Fable agents cost 129M cache-read tokens.
- **Pass an explicit `model` on every spawn.** `live-markers` has no model pin,
  so an omitted override silently inherits the session's model. Sonnet for
  implementation and scoping; Haiku for compiled work orders.
- **15 agents per wave**, ledger reported. Merges and audits are free. Keep the
  pipeline full with non-colliding work.

## Orchestration, and what it caught this wave

The protocol is in session memory (`agent-orchestration-protocol`). Core:
closed briefs with the issue as authority and forbidden surfaces named; one
agent per file-surface; agents never merge or push; the main session merges;
stopped-waiting agents get a message to finish, never a respawn; worktrees
pruned after merge.

Three additions earned this wave:

- **A branch's numbers are never the measurement.** Every agent reported
  against a baseline that had moved underneath it. Recompute on the merged
  tree, always.
- **`live/LIMITATIONS.md` mixes generated and hand-written sections.**
  Resolving its conflict with `--ours` plus regeneration restores the generated
  half and silently discards the hand-written half. It pushed CI red once.
  Splice the hand-written section explicitly.
- **Agents stop mid-flight waiting on their own background jobs.** Twice. Tell
  them in the brief that nothing will wake them and the report is the only
  artifact that survives.

## Pins

floci (fork lane, lex00/floci, checkout `/Users/alex/Documents/checkouts/floci`):

- fork main `05573b5e` — batch 3 landed: rds engine echo, CloudFront
  `TenantConfig` round-trip, CBOR `Date`-suffix timestamp tagging.
- published image **`sha256:1362e856baf70b1fc848ce302c308dfa8ad39a30187812e855bc295e77a9d933`**
  (ghcr-publish on push to fork main; digest from the workflow).
- **NOT yet re-pinned in choudoufu.** `live/floci-capabilities.json` and
  `live/cohort-acceptance.json` still carry `sha256:f122a580`. Re-pin needs:
  `tools/floci-capability-gen` re-probe (the four hand rows re-probe by hand
  with evidence), then the acceptance re-measure (~20 min, background, never
  mutate `live/e2e/estates` during it), then re-split to lex00/floci#50.
- Batch 3 found two items on #50 were **misdiagnosed**: the ACM PCA and
  SageMaker "crashes" are provider-side panics in `expand*` functions that run
  before any request reaches floci. Not fixable in the emulator. Recorded so
  nobody re-attempts them.

## Other open issues

#136 (override burn-down), #150 (phase-7 tracker; carries the fixture-debt
ledger), #154 (IGNORED, maintainer directive, strictly last), #178 (parent
campaign for #184–#210).

Two general extraction gaps found by doing other work, not yet filed:
`importdocs-gen` extracts only the **first** `## Example Usage` block per doc
page (~1699 pages; this blocked the lambda fixture and an unknown number of
others), and `applyIdentitySchemaAttrsCorrection` only *corrects* an existing
`IdentityAttrs` guess, never populates from scratch — 69 admitted types share
that gap.

## Traps (all live, several burned this session)

- `env -u PWD` for every go command (symlink trap, ~10 false failures).
- zsh eats `$c:live` as a modifier — brace variables before colons.
- Read CI as the recipe's own exit code plus `grep '^FAIL'` on the log.
  `just ci > log; echo exit: $?` where the echo follows a semicolon reports the
  echo's success. A red tree got pushed that way.
- Never pipe a generator into `head` — SIGPIPE kills it silently.
- Regen order: `just corpus` BEFORE `just limits`. After ANY admission also
  `just survey-render`.
- `TF_PLUGIN_CACHE_DIR=~/.terraform.d/plugin-cache` before any validate loop;
  `rm -rf .terraform` after each cohort. Provider schema loading fails under
  the default sandbox with "Failed to read any lines from plugin's stdout";
  it succeeds with the sandbox disabled.
- A background acceptance run reads the working tree; never mutate fixtures
  under it. Crash cohorts' `failed_resources` lists are nondeterministic.
- Cohort ownership split is enforced: `GENERATED.md` and `.tf` are estate-gen's;
  `README.md` is hand-owned and carries ratification evidence.
- `live/rowgen-convergence.json` is NOT coverage. The user-facing numbers are
  the ladder classes only.
