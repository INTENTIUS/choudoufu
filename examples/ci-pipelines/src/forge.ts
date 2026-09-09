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
 * ## Why forgejo posts a `live-plan` comment now, and what still only reports
 *
 * Every posting mode chant has - `comment`, `issue`, `pull-request`,
 * `merge-request` - is the `reconcilePr` activity, and it shells to `gh`.
 * The forgejo Op generator used to refuse `comment` by name on the premise
 * that chant had no way to point `gh` at a Forgejo instance. Chant #2291
 * checked that premise against a real instance for #1027 and found it false:
 * the failure was `gh api` resolving a *relative* path against `/api/v3`,
 * which Forgejo does not serve, not an unreachable forge.
 *
 * What exactly fails, and what does not, was settled on a real instance for
 * #1027, Forgejo 12.0.4+gitea-1.22.0 with `forgejo-runner` v9.1.1. It is the
 * URL, not the forge:
 *
 *  - `gh api repos/{owner}/{repo}/issues/{n}/comments` with `GH_HOST` set to
 *    the instance requests `https://$GH_HOST/api/v3/...`. Forgejo serves
 *    `/api/v1` and answers `/api/v3` with 404, for GET and POST alike. `gh`
 *    also forces https, so a plain-HTTP instance fails earlier still, with
 *    "server gave HTTP response to HTTPS client".
 *  - Handed a full URL, the same `gh` works:
 *    `gh api http://host/api/v1/repos/{owner}/{repo}/issues/{n}/comments`
 *    listed comments and created one, over plain HTTP and over TLS.
 *  - Forgejo's own `/api/v1` takes the GitHub-shaped sticky-comment calls
 *    unchanged: GET and POST on `issues/{n}/comments`, PATCH on
 *    `issues/comments/{id}` to edit in place.
 *  - Inside a Forgejo job, `github.api_url` is already
 *    `http://host/api/v1` and `${{ github.token }}` authenticates both calls
 *    (GET 200, POST 201, the comment authored by the actions bot).
 *
 * chant #2291 built `reconcilePr`'s `comment` mode from `GITHUB_API_URL`
 * rather than a bare path, which both GitHub Actions and Forgejo Actions
 * already set correctly, so `comment` now crosses over unchanged (see
 * `@intentius/chant-lexicon-forgejo`'s `generate-op-pipeline.ts` module doc).
 * `live-plan` runs on a pull request, so it gets `comment` on Forgejo exactly
 * as it does on GitHub and GitLab: `planFindingMode` below is no longer
 * forge-conditional.
 *
 * `issue` (`gh issue create`) was not part of that verification - only the
 * comment endpoints were exercised on the real instance - so it remains
 * un-refused-but-unverified on Forgejo, the same state GitHub's `issue` mode
 * was in before this. `discoverFindingMode` below stays `report` on Forgejo
 * for exactly that reason: no build-time refusal fires, but nothing has
 * proven the call actually reaches a Forgejo issue.
 *
 * ## Why gitlab now reaches an issue with `live-discover`, not just a comment with `live-plan`
 *
 * `comment` is the one posting mode that never shelled to `gh`: on GitHub it
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
 * `live-discover` runs on a cron, and `comment` needs a merge request to post
 * on - refused at build time by both Op generators when the trigger has none
 * (chant #2231, and gitlab's own equivalent check since #2268). Chant #2292
 * gave `reconcilePr`'s `issue` mode a GitLab path over the same `fetch` and
 * token resolution `comment` uses, rather than shelling to `gh`, so
 * `live-discover` now reaches an issue on GitLab too. `merge-request` is not
 * build-time refused on gitlab, but it is still `reconcilePr` shelling to
 * `gh`, which cannot reach a GitLab instance; nothing here uses it.
 * `live-discover` stays `report` on Forgejo, the one forge #2292 did not
 * touch and whose `issue` mode remains unverified per the section above.
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
 * An escape hatch for a build with nowhere to post a finding.
 *
 * Every posting mode below - `comment`, `issue` - is `reconcilePr`, and
 * `reconcilePr` throws when the run it is in carries no pull request, no
 * merge request and no forge to open an issue on
 * (`noPullRequestContextMessage`, chant's `op/activities/reconcile.ts`).
 * `scripts/smoke.sh` runs the five Ops end to end against floci, and its job
 * is plan/gate/apply behaviour, not the posting path: the local run is a
 * scratch repository with no remote and no pull request, and the CI smoke
 * (`.github/workflows/ci-pipelines-smoke.yml`) dispatches on
 * `workflow_dispatch`, which carries no pull request either. Since chant
 * #2291 lifted Forgejo's `comment` refusal, no forge value is left whose two
 * reporting Ops both default to `report` (the property the smoke used to
 * lean on by picking `CHANT_FORGE=forgejo`), so the smoke needs its own way
 * to say "nowhere to post" that does not depend on which forge it happens to
 * build for.
 *
 * Setting `CHANT_FINDING_MODE=report` forces both `planFindingMode` and
 * `discoverFindingMode` to `"report"` regardless of forge. It is a build-time
 * environment variable, read only here, and it is never set when the three
 * committed trees are generated: `npm run generate` always runs with it
 * unset, so `github/`, `forgejo/` and `gitlab/` reflect the forge-derived
 * defaults below and nothing else. `tests/pipelines.test.ts`'s currency guard
 * checks that a regeneration with the override set would produce different
 * bytes, so a tree accidentally committed under the override would be
 * caught rather than silently accepted as current.
 */
const FINDING_MODE_OVERRIDE = process.env.CHANT_FINDING_MODE;
if (FINDING_MODE_OVERRIDE !== undefined && FINDING_MODE_OVERRIDE !== "report") {
  throw new Error(
    `CHANT_FINDING_MODE is "${FINDING_MODE_OVERRIDE}", and the only value this project reads is "report" ` +
      `(or unset, for the forge-derived default below). It exists for a run with nowhere to post a finding.`,
  );
}
const FORCE_REPORT = FINDING_MODE_OVERRIDE === "report";

/**
 * How `live-plan` reports the plan it read: one comment on the pull request
 * (GitHub, and Forgejo since chant #2291) or the equivalent note on the merge
 * request (GitLab, since chant #2268) that triggered the run. All three are
 * the same `comment` finding mode and the same hidden-marker edit-in-place
 * recipe - `reconcilePr` picks the API by which CI variable the run itself
 * carries, and on GitHub/Forgejo that is `GITHUB_API_URL`, which both set
 * correctly. No longer forge-conditional: see the module doc above for why
 * Forgejo stopped being the exception. `CHANT_FINDING_MODE=report` (see
 * above) overrides this to `"report"` for a run with nowhere to post.
 */
export const planFindingMode = FORCE_REPORT ? ("report" as const) : ("comment" as const);

/**
 * How `live-discover` reports the adoption ledger its nightly sweep built.
 * `issue` on GitHub and, since chant #2292, on GitLab too - GitLab's path is
 * its own REST call rather than `gh issue create`, so it needs no merge
 * request a cron job never has. Forgejo stays `report`: `issue` is not
 * build-time refused there, but only `comment`'s endpoints were verified
 * against a real instance (#1027), so `issue` remains
 * un-refused-but-unverified and this project does not turn it on.
 * `CHANT_FINDING_MODE=report` (see above) overrides this to `"report"`
 * regardless of forge, for a run with nowhere to post.
 */
export const discoverFindingMode = FORCE_REPORT
  ? ("report" as const)
  : forge === "github" || forge === "gitlab"
    ? ("issue" as const)
    : ("report" as const);
