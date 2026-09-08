/**
 * The currency guard: the checked-in workflows are what `generate.ts` emits
 * from the Ops beside them, byte for byte.
 *
 * This is the rule every rendered artifact in this repository lives under. A
 * generated file that drifts from its generator is worse than no generated
 * file: it reads as authoritative and describes a pipeline nobody has.
 *
 * It regenerates into a scratch directory rather than over the tree, so a
 * failure leaves the working tree alone and the diff it prints is the whole
 * answer. Both directions are checked - a file that should no longer exist
 * fails as loudly as one that changed.
 *
 * What it cannot see: it runs under node, with this example's dependencies
 * installed. choudoufu's Go CI has neither, so `live/ci_pipelines_test.go` is
 * the backstop that runs there; read its doc comment for what that one proves
 * and what it does not. `generated-from.json` is what carries a piece of this
 * proof across: the run records a SHA256 per generator input, and the Go side
 * re-hashes them, which is how a machine with no node can tell a regenerated
 * tree from a stale one even when an input change moves no emitted byte.
 *
 * The third tree, `gitlab/`, is the exception this file also has to state,
 * because "regenerate and diff" is the wrong question there: chant's gitlab Op
 * generator is cron-only and refuses four of the five Ops by name, so
 * `gitlab/.gitlab-ci.yml` is hand-written (#807, sub-issue (b)) and
 * `npm run generate` does not write it. The second describe below holds that -
 * that regenerating produces no gitlab tree at all, and that the committed
 * file stays the one scheduled job it is allowed to be.
 */

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";
import { parseYAML } from "@intentius/chant/yaml";
import { FORGES } from "../src/forge";

const exampleDir = join(dirname(fileURLToPath(import.meta.url)), "..");

const FORGE_WORKFLOWS = {
  github: join("github", ".github", "workflows"),
  forgejo: join("forgejo", ".forgejo", "workflows"),
} as const;

type Forge = keyof typeof FORGE_WORKFLOWS;

/**
 * Run the generator for one forge into `outDir`. One process per forge on
 * purpose: `src/forge.ts` is read at module load and the Op files read it, so
 * a second generation in the same process would be served the first forge's
 * Ops out of the ESM module cache.
 */
function generate(forge: Forge, outDir: string): void {
  execFileSync(process.execPath, ["--import", "tsx", join(exampleDir, "generate.ts")], {
    cwd: exampleDir,
    env: { ...process.env, CHANT_FORGE: forge, CHANT_PIPELINE_OUT_DIR: outDir },
    stdio: "pipe",
    encoding: "utf8",
  });
}

function filesIn(dir: string): string[] {
  return readdirSync(dir).sort();
}

describe("the checked-in workflows are current", () => {
  const scratch = mkdtempSync(join(tmpdir(), "choudoufu-ci-pipelines-"));

  for (const [forge, workflowDir] of Object.entries(FORGE_WORKFLOWS) as [Forge, string][]) {
    const committed = join(exampleDir, workflowDir);
    const regenerated = join(scratch, workflowDir);

    it(`${forge}: regenerating produces the same files`, () => {
      generate(forge, scratch);
      assert.deepEqual(
        filesIn(committed),
        filesIn(regenerated),
        `${relative(exampleDir, committed)} does not hold exactly the files the generator emits. ` +
          `Run \`npm run generate\` and commit the result.`,
      );
    });

    it(`${forge}: and the same bytes`, () => {
      generate(forge, scratch);
      for (const name of filesIn(regenerated)) {
        const want = readFileSync(join(regenerated, name), "utf8");
        const got = readFileSync(join(committed, name), "utf8");
        assert.equal(
          got,
          want,
          `${relative(exampleDir, join(committed, name))} is not what generate.ts emits today. ` +
            `Run \`npm run generate\` and commit the result; never edit a generated workflow by hand.`,
        );
      }
    });
  }

  // The stamp is what carries this proof to a machine that cannot run the
  // generator: live/ci_pipelines_test.go re-hashes the inputs and compares.
  // Here it is checked the same way a workflow is - regenerate, diff - so a
  // committed stamp that no run would write fails on the node side too.
  it("and the stamp records the inputs today's run reads", () => {
    generate("github", scratch);
    assert.equal(
      readFileSync(join(exampleDir, "generated-from.json"), "utf8"),
      readFileSync(join(scratch, "generated-from.json"), "utf8"),
      "generated-from.json is not what generate.ts writes today, so the checked-in workflows were produced " +
        "from an input state that is no longer on disk. Run `npm run generate` and commit the result.",
    );
  });
});

/**
 * The hand-written half. `generate.ts` knows two forges and emits into two
 * trees; the assertions here are the ones that survive when currency is not
 * available - the file is not generated, is not claiming to be, and still says
 * the one thing chant's gitlab generator would let it say.
 *
 * Handed this project's five specs, that generator throws on the first one:
 * `pull_request` and `push` have no event model on GitLab (chant #2084), and
 * `live-plan`'s "comment" finding mode has no merge-request note activity
 * behind it (chant #2231). `live-discover` is the only Op that crosses.
 */
describe("the GitLab pipeline is hand-written", () => {
  const scratch = mkdtempSync(join(tmpdir(), "choudoufu-ci-pipelines-gitlab-"));
  const committed = join(exampleDir, "gitlab", ".gitlab-ci.yml");

  it("regenerating both forges writes no gitlab tree", () => {
    for (const forge of Object.keys(FORGE_WORKFLOWS) as Forge[]) generate(forge, scratch);
    assert.equal(
      existsSync(join(scratch, "gitlab")),
      false,
      "`npm run generate` wrote a gitlab tree. If the generator now emits GitLab, this file should diff it " +
        "byte for byte like the other two forges, and live/ci_pipelines_test.go's hand-written guards should go.",
    );
  });

  it("and the committed file does not claim to be generated", () => {
    const body = readFileSync(committed, "utf8");
    for (const marker of ["DO NOT EDIT", "generated by examples/ci-pipelines/generate.ts"]) {
      assert.equal(
        body.includes(marker),
        false,
        `gitlab/.gitlab-ci.yml carries "${marker}", but nothing regenerates it: a banner sending a reader to ` +
          "`npm run generate` would send them to a command that leaves this file exactly as it found it.",
      );
    }
  });

  it("and it is one scheduled job, running the one Op GitLab can express", () => {
    const doc = parseYAML(readFileSync(committed, "utf8")) as unknown as Record<string, unknown>;

    const jobs = Object.keys(doc).filter((key) => !["stages", "variables", "workflow", "default"].includes(key));
    assert.deepEqual(jobs, ["live-discover"], "GitLab gets the cron Op and nothing else");

    const job = doc["live-discover"] as { rules?: { if?: string }[]; script?: string[] };
    assert.deepEqual(
      job.rules,
      [{ if: '$CI_PIPELINE_SOURCE == "schedule" && $CHANT_SCHEDULED_OP == "live-discover"' }],
      "GitLab has no in-file cron, so the Pipeline Schedule and its selector variable are the whole trigger, " +
        "and this rule is what keeps the job off every other pipeline the project runs.",
    );

    const script = job.script ?? [];
    assert.ok(
      script.some((line) => line.includes("npx chant run live-discover")),
      "the job does not run its Op",
    );
    const install = script.find((line) => line.includes("choudoufu_v"));
    assert.ok(install, "the job installs no choudoufu");
    assert.match(
      install,
      /releases\/download\/v\d+\.\d+\.\d+\/choudoufu_v\d+\.\d+\.\d+_linux_amd64\.tar\.gz/,
      "a scheduled job re-runs unattended, so its install is pinned to a release rather than floating",
    );
    assert.ok(install.includes("sha256sum -c -"), "the pinned release is downloaded without checking its SHA256");
  });

  it("and it builds the report-only Ops, because GitLab has no posting activity", () => {
    const doc = parseYAML(readFileSync(committed, "utf8")) as unknown as { variables?: Record<string, string> };
    const value = doc.variables?.CHANT_FORGE;

    // The job hands this string to `chant run`, which loads src/forge.ts,
    // which throws on a value it does not know - at module load, before any
    // Op is built, on every scheduled run. So the YAML is checked against the
    // forge list itself rather than against a literal repeated here.
    assert.ok(
      value !== undefined && (FORGES as readonly string[]).includes(value),
      `gitlab/.gitlab-ci.yml builds its Ops with CHANT_FORGE=${JSON.stringify(value)}, which src/forge.ts does ` +
        `not accept (it takes ${FORGES.map((f) => JSON.stringify(f)).join(", ")}). ` +
        "`chant run` throws there at module load, so every scheduled run fails before it starts.",
    );
    assert.equal(
      value,
      "gitlab",
      "Every chant finding mode is the `reconcilePr` activity shelling to `gh` and chant has no GitLab " +
        "merge-request note activity (chant #2256), so GitLab can only report - but it reports as itself. " +
        "Unset, CHANT_FORGE defaults to github, whose live-discover opens a GitHub issue and fails on every " +
        "scheduled run; borrowing forgejo's value makes the job claim a forge it does not run on.",
    );
  });
});
