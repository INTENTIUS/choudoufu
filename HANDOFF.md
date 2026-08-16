# Handoff

Rewritten 2026-08-15 night, at `0bf50cdfa5` plus the floci re-pin in
flight. Read `.claude/agents/live-markers.md` before touching anything.
The work lives in the issue tracker (`gh issue list -R
INTENTIUS/choudoufu` - a bare `gh` hits opentofu/opentofu). Five issues
open: #136, #150, #154 (ignored, maintainer directive 2026-08-15), #178,
#179. #175 and #177 closed with their obligations executed.

## The finish line, unchanged

The product is done when a person with an existing OpenTofu
configuration runs it under live markers and it works:

- **The corpus ladder** (`live/corpus-refusals.json`): 145 published
  estates, at this commit 0 clean / 19 backend-only / 1
  backend-plus-remote-state / 11 admissions-only / 113 language-blocked
  / 1 unreadable. Finish means the language-blocked class is empty.
  This is THE number. #178 is the campaign; work it by refusal rule in
  greedy-cover order, recompute the ladder after every landing.
- **Type parity** (maintainer ruling on #175, now closed): every
  hand/evidence bucket and rejected row outside the credential class is
  debt. `tools/row-gen/rejected.json` holds 160 vetoes to re-derive or
  justify; `live/rowgen-buckets.json` still carries hand-separator and
  evidence-only buckets. #136 continues the override retirement.
- **The acceptance tier** (`live/cohort-acceptance.json`): 31 cohorts
  round-trip. 3/31 at the last measure; a re-measure against the new
  floci image (`sha256:f122a580...`) is the in-flight item below.

## In flight at handoff time

1. **Acceptance re-measure** against `ghcr.io/lex00/floci@sha256:f122a580...`
   (floci batch 2: sagemaker service, opensearch container-IP fix,
   ecr/lambda/cloudwatch/ecs daemon ops). The pin bump and re-probed
   capability manifest are UNCOMMITTED on main by design - they commit
   together with the fresh acceptance artifact so the fast tier never
   sees a mismatched pin. When the run lands: commit all three,
   re-split failures emulator/fixture, post the split to lex00/floci#50.
   Watch: s3/aps/media must hold; ecs-eks, iam-ecr, lambda, messaging
   and sagemaker are the flip candidates; aws_emr_cluster's
   release_label reverted to "placeholder" under the new variant
   preference - if it breaks apply, that is fixture debt, not emulator.
2. **#179 stage 1** (same-stack data-read phase) in implementation in a
   worktree. Audit for derivation cheats before merging (the standing
   contract: general rules, generators run twice to fixed point, no
   per-type wiring). Ceiling <=20 estates - the largest single lever.
3. **Unresolvable-identity scoping** (rule 2 of #178, 37 estates, 0
   sole) in a worktree, mapping which buckets #179 covers for free.

## The queue after those close, in order

1. **#179 stages 2 and 3** per the design's own staging: tfe_outputs
   (auth + sensitivity), then terraform_remote_state (backend read,
   RuleRemoteState narrows then retires, rung fold). The maintainer
   ruled 2026-08-15: one design covers both cross-stack flavors; the
   design issue is the authority. Recompute ceilings from the artifact
   at each stage, never predict.
2. **The remaining wall rules** by the greedy-cover order in #178's
   table: non-static-identity-argument (its unset-var-only slice is the
   cheapest sub-bucket), for-each-key, count-index, module-providers,
   module-output. Each rule gets the same treatment that worked for
   rule 1: scope from real sites (the artifact now carries per-refusal
   subject categories), bucket by expression shape, implement general
   rules, re-measure.
3. **Floci queue** (lex00/floci#50, fork-only lane): the fresh split
   drives it - next-cheapest cohorts by distinct missing operations,
   the doubled-slash absent-operation families (iot, apigateway v2
   routes, storage/Backup, ai-location/Bedrock), and the opensearch
   data plane verified with the docker socket mounted.
4. **Parity debt**: #136's remaining unread machine sources; the
   rejected.json re-derivations; the hand/evidence bucket burn-down.
5. #154 strictly ignored until the maintainer says otherwise.

## Mechanics and traps (all confirmed again this session)

- Row landing, generator fixed points, taggability pins from
  `live/survey-full.json`, `just tables/corpus/survey-render/limits`:
  the recipe is unchanged from the previous handoff and lives in the
  closed #175 thread. After ANY admission also run `just survey-render`
  - the untaggable-admitted span in LIMITATIONS.md is rendered by
  survey-gen, not limits-gen; missing it fails the fast tier.
- `env -u PWD go test ...` always. Read CI logs with the recipe's REAL
  exit status; `...; echo exit: $?` masks failure and a green-looking
  tail can hide FAIL lines - grep '^FAIL' too. This trap fired once
  this session and a red tree got pushed before fix-forward.
- Never pipe a generator into `head` (SIGPIPE). Disk: export
  `TF_PLUGIN_CACHE_DIR=~/.terraform.d/plugin-cache` before validate
  loops, delete `.terraform` dirs after.
- A background acceptance run reads the working tree; never mutate
  `live/e2e/estates/` while one runs.
- Merge from the main session only, after reading uncached fast-tier
  output. Conflicted generated artifacts get regenerated on the merged
  tree, never side-picked. Agents that stop "waiting on background
  work": message them to continue.
- Subagents are audited for derivation purity before merge: general
  rules not per-type wiring, `-emit` twice to zero diff, annotation
  rulings genuine, pins from the survey. No cheats found in six audits
  this session; three real defects were found and fixed by derivation.
