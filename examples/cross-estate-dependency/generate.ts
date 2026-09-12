/**
 * Generate this project's CI, one forge per process.
 *
 *   CHANT_FORGE=github  node --import tsx generate.ts
 *   CHANT_FORGE=forgejo node --import tsx generate.ts
 *   CHANT_FORGE=gitlab  node --import tsx generate.ts
 *
 * or `npm run generate`, which runs all three. The forge comes from the
 * environment rather than an argument because `src/forge.ts` reads it at
 * module load and the Op files are imported by the generator: one process can
 * only build the Ops one way. That is also why each generated workflow sets
 * `CHANT_FORGE` in its own `env:`.
 *
 * Nothing about what the pipeline *does* lives here. The ordering that is the
 * point of this example - network, then service - is phase order inside
 * `src/estates-apply.op.ts`, and a forge generator renders one job per Op,
 * not one per phase. So no YAML in the generated trees expresses the
 * dependency at all; the job runs `chant run estates-apply`, and the Op
 * carries the order. That is the shape worth noticing: a forge's own
 * `needs:`/`stage:` edge would have to be re-stated per forge and would not
 * exist at all for someone running `chant run` locally.
 *
 * `specs()` and `options()` are exported, and `main()` runs only when this
 * file is the process entry point, so `tests/pipelines.test.ts` can import the
 * one table this project's triggers come from without generating a tree as a
 * side effect.
 */

import { mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { generateOpsPipeline } from "@intentius/chant/op";
import type { ComponentPipelineOptions, ScheduledOpSpec } from "@intentius/chant/lexicon";
import { forge, type Forge } from "./src/forge";

const projectDir = dirname(fileURLToPath(import.meta.url));

/** Where the three generated trees land. Overridable so a test can generate into a scratch directory and diff. */
const outDir = resolve(process.env.CHANT_PIPELINE_OUT_DIR ?? projectDir);

/**
 * The choudoufu release the pipelines run, pinned by version and by the
 * SHA256 its release's own `SHA256SUMS` asset publishes. A generated workflow
 * is committed once and re-run unattended against a role that can write to the
 * account, so "whatever was released last" is not a version, and a tag can be
 * moved or an asset replaced without the run noticing.
 */
export const CHOUDOUFU_VERSION = "v0.17.0";
export const CHOUDOUFU_SHA256 = "f12d2975474b103be6f0f29a3dae6e81bbc876e70b5b95b49cc444ffbacb1fa5";
const CHOUDOUFU_ASSET = `choudoufu_${CHOUDOUFU_VERSION}_linux_amd64.tar.gz`;
const INSTALL_CHOUDOUFU =
  `curl -fsSL -o /tmp/${CHOUDOUFU_ASSET} ` +
  `https://github.com/INTENTIUS/choudoufu/releases/download/${CHOUDOUFU_VERSION}/${CHOUDOUFU_ASSET} && ` +
  `echo "${CHOUDOUFU_SHA256}  /tmp/${CHOUDOUFU_ASSET}" | sha256sum -c - && ` +
  `tar -xzf /tmp/${CHOUDOUFU_ASSET} -C /usr/local/bin choudoufu && choudoufu version`;

/** `node:22-slim`, the generator's own default, carries neither curl nor git. `node:22` carries both. */
const IMAGE = "node:22";

/**
 * AWS by OIDC, where the forge has it: the run mints a short-lived identity
 * token, something exchanges it for role credentials that expire with the
 * job, and the repository stores no long-lived key. Two roles, not one - the
 * pull-request job only reads, and only the push job should ever hold a role
 * that can write to both estates.
 *
 * The shape of the exchange is per forge. GitHub takes a `uses:` marketplace
 * action; GitLab's OIDC surface is a job-level `id_tokens:` declaration that
 * lands the JWT in `$CHANT_ID_TOKEN`, so the token is written to a file and
 * the two environment variables every AWS SDK's web-identity provider already
 * reads are pointed at it. `examples/ci-pipelines/generate.ts` carries the
 * full reasoning; this is the same code with three roles collapsed to two.
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
 * A Forgejo job's own static key pair: Forgejo Actions mints no OIDC token
 * and its dialect drops `permissions:` outright, so `assumeRole` is never
 * called for it. Per Op rather than forge-wide, so the read job and the write
 * job hold different credentials - a workflow-scoped pair would put the write
 * credential on the pull-request job too.
 */
function forgejoKeyPair(name: "PLAN" | "APPLY"): NonNullable<ScheduledOpSpec["variables"]> {
  return {
    AWS_ACCESS_KEY_ID: `\${{ secrets.CHOUDOUFU_${name}_ACCESS_KEY_ID }}`,
    AWS_SECRET_ACCESS_KEY: `\${{ secrets.CHOUDOUFU_${name}_SECRET_ACCESS_KEY }}`,
  };
}

/**
 * The two Ops, as the jobs a branch-protection rule can name.
 *
 * Neither carries a `findingMode`, so both generate at chant's default
 * `"report"`: they print what they found and post nothing. See
 * `src/estates-plan.op.ts` for why this project does not bring
 * ci-pipelines' per-forge posting table along.
 */
export function specs(): ScheduledOpSpec[] {
  const oidcForge = forge === "github" || forge === "gitlab";
  const forgejo = forge === "forgejo";
  return [
    {
      name: "estates-plan",
      trigger: { kind: "pull_request", branches: ["main"] },
      ...(oidcForge
        ? { setup: assumeRole("CHOUDOUFU_PLAN_ROLE_ARN"), permissions: OIDC }
        : forgejo
          ? { variables: forgejoKeyPair("PLAN") }
          : {}),
    },
    {
      name: "estates-apply",
      trigger: { kind: "push", branches: ["main"] },
      ...(oidcForge
        ? { setup: assumeRole("CHOUDOUFU_APPLY_ROLE_ARN"), permissions: OIDC }
        : forgejo
          ? { variables: forgejoKeyPair("APPLY") }
          : {}),
    },
  ];
}

/**
 * Per-forge options. `beforeScript` lines become one `run:` step each, after
 * the checkout and the setup steps. `runCommand` goes through `npx` because
 * chant is a dependency of the project rather than something on the image.
 *
 * `variables` becomes the workflow's top-level `env:` (github, forgejo) or
 * top-level `variables:` (gitlab). The forge marker and the region, and no
 * credential of any kind: github and gitlab mint theirs per job in `setup`,
 * and forgejo's per-Op pair is on the spec itself.
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
      // Both roots read `var.aws_region`, and choudoufu's own tagging-API
      // sweep reads the SDK's `AWS_REGION`. On github and forgejo that value
      // is a repository variable this file aliases into both names so the two
      // can never disagree. On gitlab `AWS_REGION` is already a project CI/CD
      // variable in every job's environment, and restating it here would set
      // it to the literal string before the pipeline had anything to resolve
      // it against.
      ...(gitlab ? {} : { AWS_REGION: region }),
      TF_VAR_aws_region: region,
    },
  };
}

/** Where each forge wants its workflow files, relative to a repository root. */
const WORKFLOW_DIR: Partial<Record<Forge, string>> = {
  github: join("github", ".github", "workflows"),
  forgejo: join("forgejo", ".forgejo", "workflows"),
  gitlab: join("gitlab"),
};

/**
 * The generator inputs, relative to this directory: everything a regeneration
 * reads. `live/cross_estate_dependency_test.go` holds the same list and fails
 * when the two disagree.
 */
const GENERATOR_INPUTS = ["chant.config.ts", "generate.ts", "package-lock.json", "package.json", "src"];

/**
 * The stamp: which input state the checked-in trees were generated from.
 *
 * The currency question is "are the committed workflows what today's inputs
 * produce", and answering it needs the generator. choudoufu's Go CI has no
 * node, so the guard that runs there has to ask a proxy instead - and "was
 * any input committed after the workflows" has a false answer built into it,
 * because an input change that moves no output byte leaves nothing to commit
 * and the proxy then reports stale forever with a remedy that produces no
 * commit. So the run records what it read, and the Go side re-hashes the same
 * files and compares. Copied from examples/ci-pipelines, whose
 * `generated-from.json` doc comment carries the longer version.
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
          "Written by examples/cross-estate-dependency/generate.ts on every run. DO NOT EDIT; run `npm run generate`.",
          "It records the generator inputs the checked-in workflows were produced from, so a machine with no",
          "node can still tell a regenerated tree from a stale one - which commit order alone cannot, when an",
          "input change moves no output byte. live/cross_estate_dependency_test.go re-hashes these and compares.",
        ],
        inputs,
      },
      null,
      2,
    ) + "\n"
  );
}

/** The banner every generated file carries, naming the command that rewrites it. */
function banner(name: string): string {
  const opLine =
    forge === "gitlab"
      ? "# Each job below runs the Op its own name is: examples/cross-estate-dependency/src/<job>.op.ts."
      : `# The Op this job runs is examples/cross-estate-dependency/src/${name.replace(/\.ya?ml$/, "")}.op.ts.`;
  return [
    `# ${name} - generated by examples/cross-estate-dependency/generate.ts. DO NOT EDIT.`,
    `# Regenerate with: CHANT_FORGE=${forge} npm run generate`,
    opLine,
    "# The network-then-service order is phase order inside the Op, not an edge in this file.",
    "",
    "",
  ].join("\n");
}

async function main(): Promise<void> {
  const workflowDir = WORKFLOW_DIR[forge];
  if (!workflowDir) {
    throw new Error(
      `${forge} is a forge this project builds Ops for, but not one this generator emits a tree for. ` +
        `Generate for ${Object.keys(WORKFLOW_DIR).join(" or ")}.`,
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
  // not about which forge read them.
  await writeFile(join(outDir, STAMP_FILE), await stampBody(), "utf8");

  console.log(`${forge}: wrote ${result.files.length} workflow(s) to ${dir}`);
  for (const job of result.jobs ?? []) {
    console.log(`  ${job.jobName}: ${job.trigger.kind} findingMode=${job.findingMode}`);
  }
  console.log(`  ${STAMP_FILE}: the input state this was generated from`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
