# Agent brief template

The shape every implementation brief in this repo takes. Fill the bracketed
parts; keep everything else close to verbatim, because each line of it was
paid for by a defect.

The verification budget below was measured on 2026-08-16 and changed
deliberately. Before that, an implementer's median run was 26 minutes with
about 13 of them spent on `just corpus` and `just ci` loops. Cutting the
baseline recompute and the pre-commit full-CI run gets a verification-heavy
agent to about 9 minutes and a balanced one to about 19, with no change to
the merge gate — the main session runs full CI after every merge regardless.

---

## Template

> Worktree `<absolute path>`, branch `<branch>`, based on main at `<sha>`.
> Confirm your base with `git log --oneline -1` before anything else — the
> orchestrator lands merges continuously and your worktree may predate some.
> Do not cd anywhere else. Never merge, rebase, push, or touch `main`.
>
> ## The work: <issue> — <one line on what it costs>
>
>     gh issue view <n> -R INTENTIUS/choudoufu
>
> **The issue is your authority. Where this brief and the issue disagree, the
> issue wins. Where the issue and the code disagree, the code wins — say so.**
>
> <the scoping: real sites, the mechanism, file:line for every claim>
>
> **Verify every claim above before building on it.** This brief was written
> from an agent's report, not from the maintainer. Three briefs written with
> confident scoping on 2026-08-16 were each partly wrong, and the agents that
> scanned the corpus instead of trusting the brief are what caught it.
> Refuting a claim here is a success, not a delay.
>
> ## Your surface
>
> WRITE: <exact files>
>
> FORBIDDEN, other agents hold them right now: <exact files>
>
> If the real fix lives outside your surface, **do not reach for it.** Report
> where it lives and what it would take. That report becomes the next brief
> and is worth more than a change smuggled across a boundary.
>
> DO NOT COMMIT `live/corpus-refusals.json` or `live/LIMITATIONS.md`.
> Regenerate for measurement, then `git checkout --` them. The orchestrator
> regenerates on the merged tree. `live/LIMITATIONS.md` mixes generated and
> hand-written sections; resolving it with `--ours` plus regeneration silently
> discards the hand-written half and pushed CI red once.
>
> ## The bar
>
> **Everything must be derived. Hand wiring is cheating.** The AWS provider
> has ~1700 types and grows every release. If your fix names a concrete
> `aws_*` type, provider, or estate in control flow, it is the wrong fix — go
> up a level. **Report how many OTHER things your change moves.** A change
> that moves exactly its motivating case is a special case wearing a rule's
> clothes and is rejected on merge.
>
> **Parity is the bar.** Match stock OpenTofu, go no further. This repo IS an
> OpenTofu fork, so upstream's handling is in your tree — cite file:line. If
> stock refuses the same construct, matching that refusal IS correct and the
> class closes as a documented limitation. If stock accepts what we refuse,
> that is a defect.
>
> <the adversarial questions that fit this surface — always include:>
> - **What does this newly accept that used to be refused, and could any of
>   it be wrong rather than merely permitted?**
> - **Did this change turn any warning into silence?**
> - **A false "you are fine" is worse than a false refusal**, because the
>   user finds out at apply time. When uncertain, refuse.
>
> ## Verification
>
> Green before commit, in this order:
>
>     env -u PWD go build ./...
>     env -u PWD go test ./<your packages>/...
>
> **Do NOT run `just ci` before committing.** The orchestrator runs full CI
> after every merge; running it here costs ~3 minutes and moves no gate.
>
> **Do NOT run a baseline `just corpus`.** The ladder on your base is below.
> Assert your base with `git log --oneline -1` instead.
>
> The after-fix `just corpus` IS mandatory — it is the measurement, and it is
> the number the merge is made on:
>
>     just corpus > /tmp/corpus-<name>.log 2>&1
>     echo "exit: $?"
>     python3 -c "import json;print({c['class']:c['configs'] for c in json.load(open('live/corpus-refusals.json'))['ladder']['classes']})"
>
> Then `git checkout -- live/` before committing.
>
> Ladder on your base: `<paste it>`. If your after-run disagrees with this in
> a way your change does not explain, that is the most important line in your
> report.
>
> ## Traps — each has burned this repo
>
> - Every go command: `env -u PWD go ...`. A symlinked checkout makes
>   `os.Getwd()` honour `PWD`, giving ~10 false failures in `local-exec` and
>   `TestFmt*`.
> - Never read a verification result through a pipe or a trailing `echo`
>   after a semicolon. `just ci > log; echo exit: $?` reports the echo's
>   success; a red main got pushed that way. Redirect, `echo "exit: $?"` on
>   the NEXT line, read the file, `grep '^FAIL'` it.
> - Never pipe a generator into `head` — SIGPIPE kills it before it writes
>   its artifact and it looks exactly like a no-op run.
> - `just corpus` needs the sandbox disabled and
>   `TF_PLUGIN_CACHE_DIR=~/.terraform.d/plugin-cache`. Schema loading fails
>   under the default sandbox with "Failed to read any lines from plugin's
>   stdout". Warm cache is about two minutes. `just corpus` BEFORE
>   `just limits`. After any admission also `just survey-render`.
> - Build to scratch with `-o`. A bare `go build ./tools/<name>` writes an
>   executable beside the source tree; an 8.9MB binary reached main that way.
> - `gh` needs `-R INTENTIUS/choudoufu`; bare `gh` hits opentofu/opentofu.
> - zsh eats `$c:live` as a modifier — brace variables before colons.
> - `internal/live/refusalscan` fails the build when a diagnostic exists with
>   no registry entry.
> - `internal/command` is upstream's package with 21 fork-added files, and it
>   has sat outside CI three times (#156, #164, #171) because those checks'
>   unit is the directory while this fork's unit is the file.
> - Cohort files have an enforced ownership split: `GENERATED.md` and `.tf`
>   are estate-gen's, `README.md` is hand-owned ratification evidence.
> - Check `git show --stat` before each commit, not your `git add` list.
>   `git commit` commits the index, so a file staged earlier rides along.
> - `live/rowgen-convergence.json` is NOT coverage. The user-facing numbers
>   are the ladder classes only.
>
> **NOTHING WILL WAKE YOU.** Do not start a background job and wait on it. Do
> not end your turn expecting to be resumed. **Do not spawn subagents** — one
> did on 2026-08-16 and clobbered its parent's edits three times. Your final
> report is the only artifact that survives.
>
> ## Report
>
> 1. Each claim in this brief: confirmed or refuted, with file:line.
> 2. The rule you encoded, stated without naming a resource type or estate.
> 3. How many OTHER estates, sites and types it moves — computed, with the
>    command.
> 4. After-fix ladder, recomputed by you.
> 5. Parity verdict with an upstream file:line citation.
> 6. What this newly accepts, and why none of it can be wrong.
> 7. What you did NOT fix and why, with estate names and quoted HCL.
> 8. Branch and commit SHAs. Do not push.

---

## Notes for the orchestrator

**Tests are a regression gate, not a defect-finder.** Every defect that
mattered on 2026-08-16 — a `count.index % 3` duplicate marker, a tainted bit
with nowhere to live, a cascade reporting "no edit needed" over a broken
argument, a 234-site premise that could not exist — was green and committed
when found, and every one was found by adversarial reading or a corpus scan.
CI caught none of them. Spend the verification time saved above on more audit
passes instead.

**A wave is bounded by its slowest agent**, not its median. On 2026-08-16 the
spread was 11 to 47 minutes. Trimming verification across ten agents moved
wave wall-clock about 10%; splitting the largest surface would have moved it
far more.

**Read-only auditors are cheap.** They finished in 6–15 minutes against 25–47
for implementers, because they run no generators. Two of them found
regressions in work that was already merged and believed finished.
