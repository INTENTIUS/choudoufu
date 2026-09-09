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
 * All three trees are generated now (#807, sub-issue (e)): chant #2268 taught
 * the gitlab Op generator `pull_request`/`push` triggers, which is what let
 * `gitlab/.gitlab-ci.yml` - hand-written through sub-issue (b), because that
 * generator previously refused four of the five Ops by name - retire in
 * favour of a generated file. GitLab's own generator returns one combined
 * file rather than one per Op (its trigger is job-scoped, not
 * workflow-scoped, so there is nothing to split into separate files), which
 * is why `FORGE_WORKFLOWS.gitlab` names a directory holding one file rather
 * than many: the loop below does not care how many files a forge writes, only
 * that the committed set and the regenerated set agree, file for file and
 * byte for byte.
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
  gitlab: join("gitlab"),
} as const;

type Forge = keyof typeof FORGE_WORKFLOWS;

/**
 * Run the generator for one forge into `outDir`. One process per forge on
 * purpose: `src/forge.ts` is read at module load and the Op files read it, so
 * a second generation in the same process would be served the first forge's
 * Ops out of the ESM module cache.
 *
 * `extraEnv` lets a caller add to the child's environment - used below to set
 * `CHANT_FINDING_MODE`, the smoke's escape hatch (src/forge.ts), without
 * touching the default generation every other test in this file relies on.
 */
function generate(forge: Forge, outDir: string, extraEnv: Record<string, string> = {}): void {
  execFileSync(process.execPath, ["--import", "tsx", join(exampleDir, "generate.ts")], {
    cwd: exampleDir,
    env: { ...process.env, CHANT_FORGE: forge, CHANT_PIPELINE_OUT_DIR: outDir, ...extraEnv },
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
 * `CHANT_FINDING_MODE=report` (src/forge.ts) exists for a run with nowhere
 * to post a finding - `scripts/smoke.sh` and the CI smoke it drives set it,
 * so `live-plan` and `live-discover` stay in `report` mode regardless of
 * forge. It must never be set when the three committed trees are generated,
 * or a repository that copies them would silently lose the pull-request
 * comment / issue behaviour this project otherwise ships.
 *
 * This does not assert the override is *absent* from the checked-in trees
 * directly - there is no byte in a `report`-mode workflow that says "not
 * generated under the override", since `report` is also Forgejo's own
 * `live-discover` default. It instead asserts the override actually changes
 * output for a forge/Op where it would matter (`forgejo`'s `live-plan`,
 * which is `comment` by default and `report` under the override), and that
 * the committed file matches the default rather than the override - so a
 * tree that had been (mis)generated with `CHANT_FINDING_MODE=report` set
 * would fail the ordinary currency tests above, and this test is what
 * explains why: it is not currency drift, it is the override having leaked.
 */
describe("the smoke's CHANT_FINDING_MODE=report override never reaches the committed trees", () => {
  const scratch = mkdtempSync(join(tmpdir(), "choudoufu-ci-pipelines-finding-mode-"));

  it("changes forgejo's live-plan (comment -> report), proving it is live", () => {
    generate("forgejo", scratch, { CHANT_FINDING_MODE: "report" });
    const overridden = readFileSync(join(scratch, "forgejo", ".forgejo", "workflows", "live-plan.yml"), "utf8");
    const committed = readFileSync(
      join(exampleDir, "forgejo", ".forgejo", "workflows", "live-plan.yml"),
      "utf8",
    );
    assert.notEqual(
      overridden,
      committed,
      "CHANT_FINDING_MODE=report produced the same forgejo/live-plan.yml as without it - the override is " +
        "not doing anything, so it cannot be trusted to keep the smoke's finding mode out of the committed trees.",
    );
  });

  it("the committed forgejo/live-plan.yml is the default (comment), not the override (report)", () => {
    generate("forgejo", scratch); // no CHANT_FINDING_MODE: the default this repository commits
    const withoutOverride = readFileSync(
      join(scratch, "forgejo", ".forgejo", "workflows", "live-plan.yml"),
      "utf8",
    );
    const committed = readFileSync(
      join(exampleDir, "forgejo", ".forgejo", "workflows", "live-plan.yml"),
      "utf8",
    );
    assert.equal(
      committed,
      withoutOverride,
      "forgejo/live-plan.yml does not match a generation with CHANT_FINDING_MODE unset - the committed tree " +
        "may have been generated with the smoke's override set. Regenerate with `npm run generate`, in an " +
        "environment with no CHANT_FINDING_MODE, and commit the result.",
    );
  });
});
