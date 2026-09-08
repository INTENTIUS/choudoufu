/**
 * Generate this project's CI, one forge per process.
 *
 *   CHANT_FORGE=github  node --import tsx generate.ts
 *   CHANT_FORGE=forgejo node --import tsx generate.ts
 *
 * or `npm run generate`, which runs both. The forge comes from the
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
 */

import { mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { generateOpsPipeline } from "@intentius/chant/op";
import type { ComponentPipelineOptions, ScheduledOpSpec } from "@intentius/chant/lexicon";
import { forge, type Forge } from "./src/forge";

const projectDir = dirname(fileURLToPath(import.meta.url));

/** Where the two generated trees land. Overridable so a test can generate into a scratch directory and diff. */
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
const CHOUDOUFU_VERSION = "v0.15.0";
const CHOUDOUFU_SHA256 = "c5a40433bd159c1bb2596fbd8005ee4886f144a150f96c7219ac1e23298c50d3";
const CHOUDOUFU_ASSET = `choudoufu_${CHOUDOUFU_VERSION}_linux_amd64.tar.gz`;
const INSTALL_CHOUDOUFU =
  `curl -fsSL -o /tmp/${CHOUDOUFU_ASSET} ` +
  `https://github.com/INTENTIUS/choudoufu/releases/download/${CHOUDOUFU_VERSION}/${CHOUDOUFU_ASSET} && ` +
  `echo "${CHOUDOUFU_SHA256}  /tmp/${CHOUDOUFU_ASSET}" | sha256sum -c - && ` +
  `tar -xzf /tmp/${CHOUDOUFU_ASSET} -C /usr/local/bin choudoufu && choudoufu version`;

/**
 * `gh`, for the two GitHub jobs whose finding mode posts something.
 *
 * chant's posting modes are all the `reconcilePr` activity, which shells to
 * `gh`. A GitHub-hosted runner carries it; the job runs inside a container
 * image, which does not. So the two Ops that post install it, and the three
 * that do not, do not - which is a per-Op `setup` entry rather than a
 * generator-wide `beforeScript` line for exactly that reason.
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
 * AWS on GitHub: the run mints an OIDC token, the action exchanges it for
 * credentials that expire with the job, and the repository stores no
 * long-lived key. `role-to-assume` reads a repository *variable*, because a
 * role ARN is not a secret - it is an account number and a role name - so a
 * fork of this example needs variables set and no secret at all.
 *
 * Three roles, not one, which is why `setup` is a per-Op option: the
 * pull-request and scheduled halves only read, and only the two push jobs
 * should ever hold a role that can write. Set each role's trust policy to
 * this repository, and the write roles additionally to the ref their push
 * trigger fires on.
 */
function assumeRole(roleVariable: string): NonNullable<ScheduledOpSpec["setup"]> {
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
function specs(): ScheduledOpSpec[] {
  const github = forge === "github";
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
      findingMode: github ? "comment" : "report",
      ...(github ? { setup: [...assumeRole("CHOUDOUFU_PLAN_ROLE_ARN"), { run: INSTALL_GH }], permissions: OIDC } : {}),
    },
    {
      name: "live-adopt",
      trigger: { kind: "push", branches: ["staging"] },
      findingMode: "report",
      ...(github ? { setup: assumeRole("CHOUDOUFU_ADOPT_ROLE_ARN"), permissions: OIDC } : {}),
    },
    {
      name: "live-apply",
      trigger: { kind: "push", branches: ["main"] },
      findingMode: "report",
      ...(github ? { setup: assumeRole("CHOUDOUFU_APPLY_ROLE_ARN"), permissions: OIDC } : {}),
    },
    {
      // No `trigger` and no `schedule`: this Op declares its own cron, and
      // `generateOpsPipeline` copies it onto the spec before rendering.
      name: "live-discover",
      findingMode: github ? "issue" : "report",
      ...(github ? { setup: [...assumeRole("CHOUDOUFU_PLAN_ROLE_ARN"), { run: INSTALL_GH }], permissions: OIDC } : {}),
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
 * `variables` becomes the workflow's top-level `env:`. On GitHub that is the
 * forge marker, the region and nothing else - the credentials are minted per
 * job by the setup step. On Forgejo it is also the credentials themselves,
 * because no OIDC path off Forgejo to AWS is verified anywhere in this
 * organization; that is stated in the README rather than dressed up.
 */
function options(): ComponentPipelineOptions {
  const region = "${{ vars.AWS_REGION }}";
  return {
    image: IMAGE,
    runCommand: ["npx", "chant", "run", "{name}"],
    beforeScript: ["npm ci --no-audit --no-fund", INSTALL_CHOUDOUFU],
    variables: {
      CHANT_FORGE: forge,
      // The provider reads `var.aws_region`; choudoufu's own tagging-API
      // sweep reads the SDK's `AWS_REGION`. One repository variable, so the
      // two can never disagree.
      AWS_REGION: region,
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
 * This is the generated half of `FORGES`, and it is deliberately the shorter
 * list. A forge can be one this project builds Ops for without being one this
 * project generates a tree for: where chant's Op generator for that forge
 * cannot express these Ops, the pipeline is hand-written and committed as it
 * is, and `main` below refuses to generate for it by name rather than writing
 * something over it. The build still has to know the forge - the hand-written
 * job runs `chant run`, which loads `src/forge.ts` the same way a generated
 * one does.
 */
const WORKFLOW_DIR: Partial<Record<Forge, string>> = {
  github: join("github", ".github", "workflows"),
  forgejo: join("forgejo", ".forgejo", "workflows"),
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
 */
function banner(name: string): string {
  return [
    `# ${name} - generated by examples/ci-pipelines/generate.ts. DO NOT EDIT.`,
    `# Regenerate with: CHANT_FORGE=${forge} npm run generate`,
    `# The Op this job runs is examples/ci-pipelines/src/${name.replace(/\.ya?ml$/, "")}.op.ts.`,
    "",
    "",
  ].join("\n");
}

async function main(): Promise<void> {
  const workflowDir = WORKFLOW_DIR[forge];
  if (!workflowDir) {
    throw new Error(
      `${forge} is a forge this project builds Ops for, but not one this generator emits a tree for: ` +
        `chant's Op generator for it cannot express these Ops, so its pipeline is hand-written and committed ` +
        `as it stands. Generate for ${Object.keys(WORKFLOW_DIR).join(" or ")}.`,
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

await main();
