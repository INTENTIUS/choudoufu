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
 * and what it does not.
 */

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";

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
});
