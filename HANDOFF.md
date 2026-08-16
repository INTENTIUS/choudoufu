# Handoff

Rewritten 2026-08-15 late night, at `f59df2a9e5`, working tree clean,
fast tier green (real exit read), everything pushed. The orchestrating
session moves to Opus from here. Read `.claude/agents/live-markers.md`
before touching anything. Work lives in the tracker
(`gh issue list -R INTENTIUS/choudoufu` - a bare `gh` hits
opentofu/opentofu).

## Model and budget rules (maintainer directives, 2026-08-15, binding)

- **Fable is forbidden for subagents.** Never spawn a Fable agent for
  any reason without asking the maintainer first, by name. The one
  evening of Fable agents cost 129M cache-read tokens and most of a
  Max-plan session; the maintainer stopped the fleet over it.
- **Pass an explicit `model` on every single spawn.** The live-markers
  definition has no model pin, so an omitted override silently
  inherits the main session's model. Sonnet for implementation and
  scoping; Haiku for compiled work orders (all context inlined, exact
  commands, expected outputs, STOP-on-mismatch - the calibration run
  proved Haiku executes these perfectly and stops honestly).
- **Agent budget: 12 per wave.** Report a spent/remaining ledger.
  Merges and audits happen in the main session and cost zero agents.
  Keep the pipeline full - the maintainer wants the budget consumed
  with non-colliding work, not hoarded. At wave end, audit the wave's
  merged work; a passing audit raises the budget to 15 and authorizes
  continuing into the next wave without waiting.
- The orchestration protocol that kept 15 agents from derailing is in
  the session memory (agent-orchestration-protocol). Its core: closed
  briefs with the issue as authority and forbidden surfaces named;
  agents never merge or push; main session merges only after reading
  the fast tier's REAL exit from the log (a trailing echo masks it -
  this burned once); conflicted generated artifacts are regenerated on
  the merged tree, never side-picked; one agent per file-surface;
  stopped-waiting agents get a message to finish, never a respawn;
  worktrees pruned immediately after merge.

## Where the product stands

Ladder (`live/corpus-refusals.json`, recomputed at this commit):
0 clean / 24 backend-only / 11 admissions-only / 22 data-read-eligible
/ **87 language-blocked** / 1 unreadable of 145. The wall went 122 ->
87 on 2026-08-15. The backend-plus-remote-state rung no longer exists
(folded by data-read stage 3).

Open issues: #136 (override burn-down, 5,610 lines, 14 retired today),
#150 (phase-7 tracker; carries the fixture-debt ledger), #154
(IGNORED, maintainer directive, strictly last), #178 (the language-wall
campaign - the product number lives here; two scoping comments and two
progress comments record the method). Closed today: #175, #177, #179,
#180, #181, #182, #183.

The maintainer's burndown dashboard is a Claude artifact
(claude.ai/code/artifact/86030525-1707-4357-84f1-544ebef043e1);
republish it with fresh numbers after landings.

floci (fork lane, lex00/floci, checkout at
/Users/alex/Documents/checkouts/floci): fork main 43e81403, published
image sha256:f122a580 pinned and re-measured (3/31, ratchet holds),
tonight's re-split is the LAST comment on lex00/floci#50 and ranks the
queue: crash-class first (security's ACM PCA CreateCertificateAuthority
response panics the provider; sagemaker's CreateFeatureGroup ditto; rds
echoes engine wrong; route53-cloudfront flips block counts), then
messaging's CBOR timestamp bug (single cheapest cohort flip).

## Interrupted work (the maintainer stopped all agents mid-wave)

Three partial states exist. Check each before resuming; none is merged:

1. Worktree `.claude/worktrees/agent-a4139e0bed2b9ae7c` - the
   acceptance fixture-debt fixes (lambda's missing S3 layer sibling,
   ec2-core's undocumented volume placeholder, ec2-networking's
   nat-gateway subnet_id ownership question) per #150's ledger
   comment. Partial, uncommitted. Inspect git status there; resume by
   a fresh Sonnet agent continuing in that worktree, or reset it.
2. Worktree `.claude/worktrees/agent-a1dd7b6dc9582f768` - admitting
   aws_ecs_capacity_provider + aws_ecs_daemon_task_definition (the
   ecs-eks fixture-cap from the re-split; also type parity). Partial.
   Useful finding already made: aws_ecs_capacity_provider carries a
   STALE rejected.json ledger row (recovered_from names a file that no
   longer exists) - the reversal must do the work the veto named.
   The placeholder ARNs exist because the types are absent from the
   roster, not because of IdentityAttrs.
3. floci checkout, branch `issue-50-batch-3` (may be partial or
   absent) - the crash-class queue above. Check
   `git -C /Users/alex/Documents/checkouts/floci status` and branch
   list first; it also may have left main checked out on a branch.

Also: worktree `agent-abd72f8b9cc056ae5` is merged but was locked by a
dead agent pid at prune time - `git worktree remove -f -f` it when the
lock clears.

The two scoping passes (for-each-key, non-static-identity-argument)
were killed read-only with findings unreported; re-run them fresh -
they are the highest-value next spends (for-each-key: 19 estates, 6
sole - best direct payoff on the board; NSIA: 26 estates, 5 sole, plus
the unset-var-only slice that the now-trustworthy accounting and
live/corpus-vars var-file machinery can measure supplied).

## The queue, in order

1. Re-run the two scopings (Sonnet; model them on #178's two scoping
   comments - real sites read from HCL, buckets by shape, ceilings
   recomputed never predicted).
2. Resume or redo the fixture-debt and ECS-admission worktrees.
3. floci batch 3 (crash-class + CBOR queue), then image bake via the
   fork's own ghcr-publish CI on push to main (that is how sha256:f122a580
   was built - push to fork main triggers it; digest from the workflow),
   re-pin, re-probe capabilities (tools/floci-capability-gen; the four
   hand rows re-probe by hand with evidence), acceptance re-measure
   (background, ~20 min, never touch live/e2e/estates during it),
   re-split to #50.
4. Implement what the scopings rank, wall rules in greedy-cover order.
5. #136 continuing debt; #154 stays ignored.

## Traps (all live, several burned this session)

- `env -u PWD` for every go command (symlink trap). zsh eats `$c:live`
  as a modifier - brace variables before colons.
- Read CI results as the RECIPE's exit code plus `grep '^FAIL'` on the
  log. `just ci > log; echo exit: $?` where the echo follows a
  semicolon reports the echo's success, not the recipe's - a red tree
  got pushed that way once tonight.
- Never pipe a generator into head (SIGPIPE kills it silently).
- Regen order: `just corpus` BEFORE `just limits`. After ANY admission
  also `just survey-render` (the untaggable-admitted span lives there,
  not in limits-gen; missing it fails the stamp tests).
- TF_PLUGIN_CACHE_DIR=~/.terraform.d/plugin-cache exported before any
  validate loop; rm -rf .terraform after each cohort (700MB provider,
  disk filled once).
- A background acceptance run reads the working tree; never mutate
  fixtures under it. Crash cohorts' failed_resources lists are
  nondeterministic; do not diff them.
- Row landing recipe: the closed #175 thread. Fixed point = row-gen
  -emit twice, zero diff. Annotations need reason/evidence/exit plus a
  reviewed ratchet bump. Taggability pins from live/survey-full.json,
  never guessed. Check rejected.json for vetoes; reversing one means
  doing the work it named.
- live/rowgen-convergence.json is NOT coverage; the user-facing
  numbers are the corpus ladder classes only.
