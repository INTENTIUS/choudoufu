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
 * ## Why forgejo reports rather than posting
 *
 * Every posting mode chant has - `comment`, `issue`, `pull-request`,
 * `merge-request` - is the `reconcilePr` activity. `issue` and `pull-request`
 * shell to `gh` against the GitHub API; chant carries no Forgejo client and no
 * way to point `gh` at a Forgejo instance, which is why the forgejo Op
 * generator refuses `comment` by name. `issue` is not refused there, but it
 * is the same `gh issue create`, so an Op carrying it on Forgejo would
 * generate cleanly and fail at its Report step on every run. This example
 * does not ship that: on Forgejo both reporting Ops run in `report` mode,
 * where the finding is the run's own log and its step summary, and the
 * README says so rather than the pipeline discovering it.
 *
 * ## Why gitlab now posts a `live-plan` comment but still reports `live-discover`
 *
 * `comment` is the one posting mode that does not shell to `gh`: on GitHub it
 * PATCHes/POSTs a pull-request comment through the GitHub REST API, and since
 * chant #2268 it does the equivalent over GitLab's own REST API when the run
 * is a `merge_request_event` pipeline - a plain `fetch`, no `gh`, no `glab`.
 * It needs a `GITLAB_TOKEN` CI/CD variable, masked, scope `api`; a project
 * access token is the least-privilege form of that. The job's own
 * `CI_JOB_TOKEN` is read as a fallback (chant's `gitlabNoteTokenFrom`), but it
 * only reaches the notes API on an instance whose job-token allowlist covers
 * it, which is not every GitLab. `live-plan` runs on a merge request, so it
 * gets `comment` on GitLab exactly as it does on GitHub - provided the token
 * is actually there: a *protected* CI/CD variable is not exposed to a
 * merge-request pipeline built from an unprotected branch, so an unprotected
 * feature branch's `live-plan` run silently loses `GITLAB_TOKEN` along with
 * the three `CHOUDOUFU_*_ROLE_ARN` variables if those are marked protected.
 * Push-to-`main`/`staging` jobs are unaffected; only merge-request jobs read
 * these variables in a pipeline that could run from a protected or an
 * unprotected branch.
 *
 * `live-discover` runs on a cron. `comment` needs a merge request to post on
 * and is refused at build time by both Op generators when the trigger has
 * none (chant #2231, and gitlab's own equivalent check since #2268); `issue`
 * and `merge-request` are not build-time refused on gitlab, but they are
 * still `reconcilePr` shelling to `gh`, which cannot reach a GitLab instance.
 * So `live-discover` stays `report` on every forge but GitHub - the one
 * finding mode gitlab's own comment path cannot reach, because there is no
 * merge request for a nightly sweep to comment on.
 *
 * ## Why gitlab is a forge here
 *
 * chant's gitlab Op generator was cron-only through 0.59.0, refusing four of
 * this project's five Ops by name; `gitlab/.gitlab-ci.yml` was hand-written
 * for exactly that reason (#807, sub-issue (b)). chant #2268 (0.60.0) taught
 * it `pull_request` and `push` triggers, so `generate.ts` now emits a GitLab
 * tree the same way it emits github's and forgejo's, and this file's forge
 * value is what a generated GitLab job builds its Ops with, the same way a
 * generated GitHub or Forgejo job does.
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
 * the pull request that triggered the run; on GitLab, since chant #2268, the
 * equivalent note on the merge request that triggered it. Both are the same
 * `comment` finding mode and the same hidden-marker edit-in-place recipe -
 * `reconcilePr` picks the API by which CI variable the run itself carries.
 * Forgejo reports, for the reason above: chant has no Forgejo client.
 */
export const planFindingMode = forge === "forgejo" ? ("report" as const) : ("comment" as const);

/**
 * How `live-discover` reports the adoption ledger its nightly sweep built.
 * `issue` only on GitHub: it is `gh issue create`, which only GitHub's `gh`
 * can do, and GitLab's own `comment` path needs a merge request a cron job
 * never has.
 */
export const discoverFindingMode = forge === "github" ? ("issue" as const) : ("report" as const);
