/**
 * Generate this project's CI, one forge per process.
 *
 *   CHANT_FORGE=github  node --import tsx generate.ts
 *   CHANT_FORGE=forgejo node --import tsx generate.ts
 *   CHANT_FORGE=gitlab  node --import tsx generate.ts
 *
 * or `npm run generate`, which runs all three. The forge comes from the
 * environment rather than from an argument because `src/forge.ts` reads it at
 * module load, and the Op files read `src/forge.ts`: one process can only
 * build the Ops one way, since a second `import()` of the same file is served
 * from the ESM module cache. That is also why the generated workflow sets
 * `CHANT_FORGE` in its own `env:` - the Op the runner builds has to be the Op
 * the workflow was generated from.
 *
 * Everything below is a `ScheduledOpSpec` per Op plus one options object per
 * forge. Nothing about the pipeline's behaviour lives here: the finding mode
 * is baked into each Op, the gate is a phase inside `live-apply`, and this
 * file only says what triggers each Op, what the job installs first, and
 * which credentials it may reach for.
 *
 * `specs()`, `options()`, `CHOUDOUFU_VERSION` and `CHOUDOUFU_SHA256` are
 * exported, and `main()` below runs only when this file is the process entry
 * point, so `tests/pipelines.test.ts` can `import` them and read the one
 * table this project's triggers and pin come from, without also generating a
 * tree as a side effect of importing it.
 */

import { mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { generateOpsPipeline } from "@intentius/chant/op";
import type { ComponentPipelineOptions, ScheduledOpSpec } from "@intentius/chant/lexicon";
import { forge, discoverFindingMode, planFindingMode, type Forge } from "./src/forge";

const projectDir = dirname(fileURLToPath(import.meta.url));

/** Where the three generated trees land. Overridable so a test can generate into a scratch directory and diff. */
const outDir = resolve(process.env.CHANT_PIPELINE_OUT_DIR ?? projectDir);

/**
 * The choudoufu release the pipelines run, pinned by version and by the
 * SHA256 the release's own `SHA256SUMS` asset publishes.
 *
 * A pinned version rather than "latest": a generated workflow is committed
 * once and re-run unattended, often against a role that can write to the
 * account, so "whatever was released last" is not a version. The checksum is
 * the other half - a tag can be moved, a release asset can be replaced, and
 * the run would never notice.
 *
 * chant's terraform lexicon refuses a choudoufu older than v0.14.0, which is
 * the release that made `live-plan -json` reachable on a configuration
 * declaring its own estate - the shape every chant live root has.
 */
export const CHOUDOUFU_VERSION = "v0.16.0";
export const CHOUDOUFU_SHA256 = "1e9d540893c6f30fad47530614cf0b1935672e991167a175aca940ebe7b3c7ae";
const CHOUDOUFU_ASSET = `choudoufu_${CHOUDOUFU_VERSION}_linux_amd64.tar.gz`;
const INSTALL_CHOUDOUFU =
  `curl -fsSL -o /tmp/${CHOUDOUFU_ASSET} ` +
  `https://github.com/INTENTIUS/choudoufu/releases/download/${CHOUDOUFU_VERSION}/${CHOUDOUFU_ASSET} && ` +
  `echo "${CHOUDOUFU_SHA256}  /tmp/${CHOUDOUFU_ASSET}" | sha256sum -c - && ` +
  `tar -xzf /tmp/${CHOUDOUFU_ASSET} -C /usr/local/bin choudoufu && choudoufu version`;

/**
 * `gh`, for the two GitHub jobs whose finding mode shells out to it.
 *
 * `issue` is always `gh issue create`, and `comment` is `gh` only on GitHub -
 * on GitLab the same finding mode goes over a plain `fetch` to GitLab's own
 * REST API (chant #2268), so a GitLab job never needs this install line. A
 * GitHub-hosted runner carries `gh`; the job here runs inside a container
 * image, which does not. So only the GitHub jobs whose mode needs it install
 * it - a per-Op `setup` entry rather than a generator-wide `beforeScript`
 * line for exactly that reason.
 */
const GH_VERSION = "2.100.0";
const GH_SHA256 = "e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be";
const GH_ASSET = `gh_${GH_VERSION}_linux_amd64.tar.gz`;
const INSTALL_GH =
  `curl -fsSL -o /tmp/${GH_ASSET} https://github.com/cli/cli/releases/download/v${GH_VERSION}/${GH_ASSET} && ` +
  `echo "${GH_SHA256}  /tmp/${GH_ASSET}" | sha256sum -c - && ` +
  `tar -xzf /tmp/${GH_ASSET} -C /tmp && ` +
  `install /tmp/gh_${GH_VERSION}_linux_amd64/bin/gh /usr/local/bin/gh && gh --version`;

/**
 * The job image. `node:22-slim`, the generator's own default, carries neither
 * curl nor git, so neither the install line above nor `actions/checkout`'s
 * git path works in it. `node:22` carries curl, git, tar and sha256sum, and
 * still no `gh` - see above.
 */
const IMAGE = "node:22";

/**
 * AWS by OIDC: the run mints a short-lived identity token, something exchanges
 * it for role credentials that expire with the job, and the repository stores
 * no long-lived key. Three roles, not one, which is why `setup` is a per-Op
 * option: the pull-request and scheduled halves only read, and only the two
 * push jobs should ever hold a role that can write. Set each role's trust
 * policy to this repository, and the write roles additionally to the ref
 * their push trigger fires on.
 *
 * The shape of the exchange is per forge, because `setup` steps are (#2242):
 * GitHub gets a `uses:` marketplace action, which GitLab CI has no dialect
 * for at all. GitLab's own OIDC surface is a job-level `id_tokens:`
 * declaration - `permissions: OIDC` below maps to it (chant #2257) - which
 * lands the JWT in the `$CHANT_ID_TOKEN` job variable rather than a file, so
 * the `{ run }` branch writes it to one and points the two environment
 * variables every AWS SDK's own "web identity" credential provider already
 * reads at that file: no `aws` CLI, no hand-rolled STS call, no signature -
 * `AssumeRoleWithWebIdentity` is the one STS action that takes no SigV4
 * signing, by design, since the token itself is the credential being
 * exchanged. Forgejo gets neither branch: its dialect drops `permissions:`
 * and `environment:` outright (no OIDC token, no environment object), so
 * `assumeRole` is never called for it and its jobs fall back to the static
 * key pair in `options()` below - unverified, same as it always was.
 */
function assumeRole(roleVariable: string): NonNullable<ScheduledOpSpec["setup"]> {
  if (forge === "gitlab") {
    return [
      {
        run:
          `echo "$CHANT_ID_TOKEN" > "$CI_PROJECT_DIR/.chant-id-token.jwt" && ` +
          `export AWS_WEB_IDENTITY_TOKEN_FILE="$CI_PROJECT_DIR/.chant-id-token.jwt" && ` +
          `export AWS_ROLE_ARN="$${roleVariable}" && ` +
          `export AWS_ROLE_SESSION_NAME="gitlab-ci-$CI_PIPELINE_ID"`,
      },
    ];
  }
  return [
    {
      uses: "aws-actions/configure-aws-credentials@v6",
      with: {
        "role-to-assume": `\${{ vars.${roleVariable} }}`,
        "aws-region": "${{ vars.AWS_REGION }}",
      },
    },
  ];
}

const OIDC: ScheduledOpSpec["permissions"] = { "id-token": "write" };

/**
 * The five Ops, as the jobs a governance policy can name.
 *
 * The per-environment dial is which Op an environment runs, not three copies
 * of the root: dev observes on every pull request (`live-check`, `live-plan`),
 * staging reconciles behind its gate (`live-adopt`, on a push to `staging`),
 * production applies behind its gate (`live-apply`, on a push to `main`), and
 * `live-discover` sweeps the account on its own cron regardless.
 */
export function specs(): ScheduledOpSpec[] {
  const github = forge === "github";
  // GitLab's Op generator now expresses pull_request/push triggers and a
  // per-job id_tokens declaration (chant #2268, #2257), so its jobs get the
  // same per-Op role and OIDC wiring GitHub's do - only the *shape* of the
  // setup step differs, in assumeRole() above. Forgejo has neither surface
  // (no OIDC token, no environment object), so it keeps the flat static key
  // in options() below, unverified as it always was.
  const oidcForge = github || forge === "gitlab";
  // `gh` is a GitHub-only dependency: GitLab's own "comment" mode needs no
  // install (see INSTALL_GH's comment above), and forgejo never gets here.
  const ghSetup = github ? [{ run: INSTALL_GH }] : [];
  return [
    {
      // No credentials of any kind: `live-check` makes no cloud calls, reads
      // no state and needs no estate. No setup step, no permissions.
      name: "live-check",
      trigger: { kind: "pull_request", branches: ["main"] },
      findingMode: "report",
    },
    {
      name: "live-plan",
      trigger: { kind: "pull_request", branches: ["main"] },
      // "comment" now generates for GitLab too (chant #2268's merge-request
      // note activity); Forgejo has no posting activity at all, so it stays
      // "report". See ./src/forge.ts for the reasoning behind this value.
      findingMode: planFindingMode,
      ...(oidcForge
        ? { setup: [...assumeRole("CHOUDOUFU_PLAN_ROLE_ARN"), ...ghSetup], permissions: OIDC }
        : {}),
    },
    {
      name: "live-adopt",
      trigger: { kind: "push", branches: ["staging"] },
      findingMode: "report",
      ...(oidcForge ? { setup: assumeRole("CHOUDOUFU_ADOPT_ROLE_ARN"), permissions: OIDC } : {}),
    },
    {
      name: "live-apply",
      trigger: { kind: "push", branches: ["main"] },
      findingMode: "report",
      // Binds the job to the forge's own deployment-environment reviewer
      // (chant #2264/#2257) - GitHub and GitLab both express it; Forgejo has
      // no environments at all and its dialect drops the key with a header
      // note rather than silently losing the gate. See live-apply.op.ts and
      // examples/pipeline-governance/github/governance.yml for what the
      // reviewer adds on top of chant's own gate below.
      environment: { name: "production" },
      ...(oidcForge ? { setup: assumeRole("CHOUDOUFU_APPLY_ROLE_ARN"), permissions: OIDC } : {}),
    },
    {
      // No `trigger` and no `schedule`: this Op declares its own cron, and
      // `generateOpsPipeline` copies it onto the spec before rendering.
      name: "live-discover",
      // "issue" only on GitHub; GitLab's own "comment" path needs a merge
      // request a cron job never has. See ./src/forge.ts.
      findingMode: discoverFindingMode,
      ...(oidcForge
        ? { setup: [...assumeRole("CHOUDOUFU_PLAN_ROLE_ARN"), ...ghSetup], permissions: OIDC }
        : {}),
    },
  ];
}

/**
 * Per-forge options.
 *
 * `beforeScript` lines become one `run:` step each, in order, after the
 * checkout and the setup steps: install the project's own dependencies, then
 * choudoufu. `runCommand` goes through `npx` because `chant` is a dependency
 * of the project rather than something on the image.
 *
 * `variables` becomes the workflow's top-level `env:` (GitHub, Forgejo) or
 * top-level `variables:` (GitLab - every job's environment already carries a
 * GitLab CI/CD variable, which is why the credentials below are not repeated
 * there the way they are for Forgejo). On GitHub and GitLab that is the forge
 * marker, the region and nothing else - the credentials are minted per job by
 * the setup step (`assumeRole`, above). Forgejo's dialect drops
 * `permissions:` and has no `id_tokens:` equivalent at all, so its jobs carry
 * the credentials themselves; no OIDC path off Forgejo to AWS is verified
 * anywhere in this organization, and the README says so rather than dressing
 * it up.
 */
export function options(): ComponentPipelineOptions {
  const gitlab = forge === "gitlab";
  const region = gitlab ? "$AWS_REGION" : "${{ vars.AWS_REGION }}";
  return {
    image: IMAGE,
    runCommand: ["npx", "chant", "run", "{name}"],
    beforeScript: ["npm ci --no-audit --no-fund", INSTALL_CHOUDOUFU],
    variables: {
      CHANT_FORGE: forge,
      // The provider reads `var.aws_region`; choudoufu's own tagging-API
      // sweep reads the SDK's `AWS_REGION`. On GitHub and Forgejo that value
      // is a repository variable this file has to alias into both names, so
      // the two can never disagree. On GitLab, AWS_REGION is already a
      // project CI/CD variable in every job's own environment - restating it
      // here would set it to the literal string "$AWS_REGION" before the
      // pipeline's own variable substitution has anything to resolve it
      // against, so only the Terraform-side alias is declared.
      ...(gitlab ? {} : { AWS_REGION: region }),
      TF_VAR_aws_region: region,
      ...(forge === "forgejo"
        ? {
            AWS_ACCESS_KEY_ID: "${{ secrets.AWS_ACCESS_KEY_ID }}",
            AWS_SECRET_ACCESS_KEY: "${{ secrets.AWS_SECRET_ACCESS_KEY }}",
          }
        : {}),
    },
  };
}

/**
 * Where each forge wants its workflow files, relative to a repository root.
 *
 * All three forges land here since chant #2268 taught the gitlab Op
 * generator `pull_request`/`push` triggers - GitLab's own generator returns
 * one file for every job rather than one file per Op (its trigger is
 * job-scoped rather than workflow-scoped, so there is nothing to split), and
 * `main` below writes whatever `result.files` names without caring how many
 * there are. `Partial` is kept rather than tightened to `Record`: a forge
 * whose Op generator refuses one of this project's Ops again in some future
 * chant version has somewhere to fall back to (drop it from this map and
 * hand-write the pipeline the way GitLab's was written before #2268), and
 * `main` refuses to generate for a forge missing here by name rather than
 * silently writing nothing.
 */
const WORKFLOW_DIR: Partial<Record<Forge, string>> = {
  github: join("github", ".github", "workflows"),
  forgejo: join("forgejo", ".forgejo", "workflows"),
  gitlab: join("gitlab"),
};

/**
 * The generator inputs, relative to this directory: everything a regeneration
 * reads. `live/ci_pipelines_test.go`'s `ciPipelineGeneratorInputs` is the same
 * list, and a guard there fails when the two disagree.
 */
const GENERATOR_INPUTS = ["chant.config.ts", "generate.ts", "package-lock.json", "package.json", "src"];

/**
 * The stamp: which input state the checked-in trees were generated from.
 *
 * The currency question is "are the committed workflows what today's inputs
 * produce", and answering it needs the generator, so the guard that runs where
 * node does not have had to ask a proxy instead - was any input committed
 * after the workflows. That proxy has a false answer built into it: an input
 * change that moves no output byte leaves nothing to commit, and the proxy
 * then reports stale forever with a remedy (`npm run generate` and commit the
 * result) that produces no commit. #807's third forge value is exactly that
 * change - `src/forge.ts` grew a value neither generated forge reads.
 *
 * So the run records what it read. The stamp moves whenever an input moves,
 * whether or not the emitted YAML does, which gives the proxy something real
 * to compare and turns it into a content check: a machine with no node can
 * hash the inputs itself and see whether the generator has been run since they
 * last changed. That also closes the blind spot the ordering check documents,
 * on the input side - a source change and a workflow edit landing in one
 * commit are no longer indistinguishable from a regeneration.
 */
const STAMP_FILE = "generated-from.json";

/** Every file under one input entry, relative to the project, slash-separated and sorted. */
async function inputFiles(entry: string): Promise<string[]> {
  const full = join(projectDir, entry);
  const stats = await stat(full);
  if (!stats.isDirectory()) return [entry];

  const found: string[] = [];
  for (const child of (await readdir(full)).sort()) {
    found.push(...(await inputFiles(`${entry}/${child}`)));
  }
  return found;
}

/** The stamp's bytes: sorted paths, one SHA256 each, stable across runs and machines. */
async function stampBody(): Promise<string> {
  const paths: string[] = [];
  for (const entry of GENERATOR_INPUTS) paths.push(...(await inputFiles(entry)));
  paths.sort();

  const inputs: Record<string, string> = {};
  for (const path of paths) {
    inputs[path] = createHash("sha256").update(await readFile(join(projectDir, path))).digest("hex");
  }

  return (
    JSON.stringify(
      {
        note: [
          "Written by examples/ci-pipelines/generate.ts on every run. DO NOT EDIT; run `npm run generate`.",
          "It records the generator inputs the checked-in workflows were produced from, so a machine with no",
          "node can still tell a regenerated tree from a stale one - which commit order alone cannot, when an",
          "input change moves no output byte. live/ci_pipelines_test.go re-hashes these files and compares.",
        ],
        inputs,
      },
      null,
      2,
    ) + "\n"
  );
}

/**
 * The banner every generated file carries. It names the command that rewrites
 * the file, so a reader who found the workflow first has somewhere to go.
 *
 * GitHub and Forgejo emit one file per Op, named after it, so the third line
 * names that one Op's source. GitLab emits every Op's job into the one file
 * (its trigger is job-scoped rather than workflow-scoped, so there is nothing
 * to split into separate files), and that file's own name carries no Op name
 * to derive a source path from - so the third line names the whole directory
 * instead, and the generated header inside the file (chant's own, not this
 * banner) names each job's Op, cron or trigger, and finding mode individually.
 */
function banner(name: string): string {
  const opLine =
    forge === "gitlab"
      ? "# Each job below runs the Op its own name is: examples/ci-pipelines/src/<job>.op.ts."
      : `# The Op this job runs is examples/ci-pipelines/src/${name.replace(/\.ya?ml$/, "")}.op.ts.`;
  return [
    `# ${name} - generated by examples/ci-pipelines/generate.ts. DO NOT EDIT.`,
    `# Regenerate with: CHANT_FORGE=${forge} npm run generate`,
    opLine,
    "",
    "",
  ].join("\n");
}

async function main(): Promise<void> {
  const workflowDir = WORKFLOW_DIR[forge];
  if (!workflowDir) {
    throw new Error(
      `${forge} is a forge this project builds Ops for, but not one this generator emits a tree for: ` +
        `chant's Op generator for it cannot express these Ops. Generate for ${Object.keys(WORKFLOW_DIR).join(" or ")}.`,
    );
  }

  const result = await generateOpsPipeline(specs(), forge, options(), projectDir);
  if (!result.success || !result.files) {
    throw new Error(`generating the ${forge} pipeline failed: ${result.error ?? "no files and no error"}`);
  }

  const dir = join(outDir, workflowDir);
  await mkdir(dir, { recursive: true });

  // Remove a file for an Op that no longer exists, rather than leaving it
  // behind for the currency guard to find later.
  const written = new Set(result.files.map((file) => file.name));
  for (const existing of await readdir(dir).catch(() => [])) {
    if (!written.has(existing)) await rm(join(dir, existing));
  }

  for (const file of result.files) {
    await writeFile(join(dir, file.name), banner(file.name) + file.yaml, "utf8");
  }

  // Last, so a failed generation leaves no stamp claiming a tree it did not
  // write. Every forge writes the same bytes - the stamp is about the inputs,
  // not about which forge read them - so `npm run generate` running both in
  // sequence is idempotent here.
  await writeFile(join(outDir, STAMP_FILE), await stampBody(), "utf8");

  console.log(`${forge}: wrote ${result.files.length} workflow(s) to ${dir}`);
  for (const job of result.jobs ?? []) {
    console.log(`  ${job.jobName}: ${job.trigger.kind} findingMode=${job.findingMode}`);
  }
  console.log(`  ${STAMP_FILE}: the input state this was generated from`);
}

// Run only when this file is the process entry point (`node --import tsx
// generate.ts`, which is what both `npm run generate:*` and
// TestCIPipelineWorkflowsRegenerate/TestCIPipelineGitLabRegenerates invoke),
// not when something imports it - `tests/pipelines.test.ts` imports `specs`
// for the trigger-parity table, and an import that wrote a tree and a stamp
// as a side effect would make running the tests mutate the working copy.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
