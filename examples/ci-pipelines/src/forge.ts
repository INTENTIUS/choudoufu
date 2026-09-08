/**
 * Which forge this build targets, and what a finding can mean there.
 *
 * An Op's finding mode is baked into the Op at build time - `chant run` reads
 * the same Op file the generator did - so it cannot be substituted per forge
 * from the outside. `generateOpsPipeline`'s `findingMode` on a
 * `ScheduledOpSpec` decides the token and permission surface the generated
 * job gets, not what the Op does with a finding. If the two disagree, the
 * workflow reads as granted and the Op fails on every run.
 *
 * So the forge is an input to the build, read here, and it travels two ways
 * that always agree:
 *
 *  - `generate.ts` is run once per forge, in its own process (a second
 *    `import()` of an Op file in the same process is served from the ESM
 *    module cache, so one process cannot build the same Op two ways);
 *  - the generated workflow sets `CHANT_FORGE` in its own top-level `env:`,
 *    so the Op the runner builds is the Op the workflow was generated from.
 *
 * ## Why forgejo and gitlab report rather than posting
 *
 * Every posting mode chant has - `comment`, `issue`, `pull-request` - is the
 * `reconcilePr` activity, and that activity shells to `gh` against the GitHub
 * API. chant carries no Forgejo client and no way to point `gh` at a Forgejo
 * instance, which is why the forgejo Op generator refuses `comment` by name.
 * `issue` is not refused there, but it is the same `gh issue create`, so an
 * Op carrying it on Forgejo would generate cleanly and fail at its Report
 * step on every run. This example does not ship that: on Forgejo both
 * reporting Ops run in `report` mode, where the finding is the run's own log
 * and its step summary, and the README says so rather than the pipeline
 * discovering it.
 *
 * GitLab reports for that reason and one more on top of it: chant has no
 * GitLab merge-request note activity at all, so there is nothing there for a
 * finding to be posted to. Its gitlab Op generator refuses `comment` by name
 * and says so (INTENTIUS/chant#2231); INTENTIUS/chant#2256 is the issue that
 * would give it that activity, along with the `pull_request` and `push`
 * triggers it also lacks. Until that lands, both reporting Ops run in
 * `report` mode on GitLab too.
 *
 * ## Why gitlab is a forge here although nothing generates it
 *
 * `generate.ts` emits a tree for github and for forgejo. GitLab's one job is
 * hand-written in `gitlab/.gitlab-ci.yml`, because that same generator is
 * cron-only and refuses four of this project's five Ops by name (#807,
 * sub-issue (b)).
 *
 * That is a fact about generation, not about the build. The hand-written job
 * still runs `chant run`, which still loads this file, which still has to
 * decide a finding mode - so GitLab is a forge this project builds for even
 * though it is not one this project generates for, and the two lists are
 * different lists. Naming it here is what lets that job say which forge it is
 * running on: before it existed the job set `forgejo`, the only report-only
 * value on offer, and nothing downstream could tell that deliberate choice
 * from a line copied off the wrong tree.
 */

export type Forge = "github" | "forgejo" | "gitlab";

export const FORGES: readonly Forge[] = ["github", "forgejo", "gitlab"];

function isForge(raw: string): raw is Forge {
  return (FORGES as readonly string[]).includes(raw);
}

function readForge(): Forge {
  const raw = process.env.CHANT_FORGE ?? "github";
  if (!isForge(raw)) {
    throw new Error(
      `CHANT_FORGE is "${raw}", which is not a forge this project builds for. ` +
        `Set it to ${FORGES.map((f) => `"${f}"`).join(" or ")}, or leave it unset for github.`,
    );
  }
  return raw;
}

/** The forge this build is for. Defaults to github when nothing says otherwise. */
export const forge: Forge = readForge();

/**
 * How `live-plan` reports the plan it read. On GitHub that is one comment on
 * the pull request that triggered the run, re-edited on the next push, found
 * again by the hidden marker `reconcilePr` writes as its first line. Every
 * other forge reports, for the reason above: GitHub is where `gh` works.
 */
export const planFindingMode = forge === "github" ? ("comment" as const) : ("report" as const);

/** How `live-discover` reports the adoption ledger its nightly sweep built. */
export const discoverFindingMode = forge === "github" ? ("issue" as const) : ("report" as const);
