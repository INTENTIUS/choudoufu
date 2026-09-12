/**
 * Two guards over the generated half.
 *
 * The currency guard is the rule every rendered artifact in this repository
 * lives under: the checked-in workflows are what `generate.ts` emits from the
 * Ops beside them, byte for byte. A generated file that drifts from its
 * generator is worse than no generated file, because it reads as
 * authoritative and describes a pipeline nobody has. It regenerates into a
 * scratch directory rather than over the tree, so a failure leaves the
 * working tree alone and the diff it prints is the whole answer.
 *
 * The ordering guard is this example's own content. No byte of generated YAML
 * says the network estate applies before the service estate - a forge
 * generator emits one job per Op, and the order is phase order inside
 * `src/estates-apply.op.ts`. So the assertion has to read the built Op rather
 * than the workflow: the phases, in order, with the root each one drives.
 *
 * `live/cross_estate_dependency_test.go` is the backstop that runs where node
 * does not, over `generated-from.json`.
 */

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { before, describe, it } from "node:test";
import { specs } from "../generate";

const exampleDir = join(dirname(fileURLToPath(import.meta.url)), "..");

const FORGE_WORKFLOWS = {
  github: join("github", ".github", "workflows"),
  forgejo: join("forgejo", ".forgejo", "workflows"),
  gitlab: join("gitlab"),
} as const;

type Forge = keyof typeof FORGE_WORKFLOWS;

/**
 * Run the generator for one forge into `outDir`. One process per forge: the
 * forge is read at module load, so a second generation in the same process
 * would be served the first forge's Ops out of the ESM module cache.
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
  const scratch = mkdtempSync(join(tmpdir(), "choudoufu-cross-estate-"));

  for (const [forge, workflowDir] of Object.entries(FORGE_WORKFLOWS) as [Forge, string][]) {
    const committed = join(exampleDir, workflowDir);
    const regenerated = join(scratch, workflowDir);

    it(`${forge}: regenerating produces the same files, and the same bytes`, () => {
      generate(forge, scratch);
      assert.deepEqual(
        filesIn(committed),
        filesIn(regenerated),
        `${relative(exampleDir, committed)} does not hold exactly the files the generator emits. ` +
          "Run `npm run generate` and commit the result.",
      );
      for (const name of filesIn(regenerated)) {
        assert.equal(
          readFileSync(join(committed, name), "utf8"),
          readFileSync(join(regenerated, name), "utf8"),
          `${relative(exampleDir, join(committed, name))} is not what generate.ts emits today. ` +
            "Run `npm run generate` and commit the result; never edit a generated workflow by hand.",
        );
      }
    });
  }

  it("and the stamp records the inputs today's run reads", () => {
    generate("github", scratch);
    assert.equal(
      readFileSync(join(exampleDir, "generated-from.json"), "utf8"),
      readFileSync(join(scratch, "generated-from.json"), "utf8"),
      "generated-from.json is not what generate.ts writes today, so the checked-in workflows were " +
        "produced from an input state that is no longer on disk. Run `npm run generate` and commit it.",
    );
  });
});

describe("the two Ops are the two jobs, and only the apply job can write", () => {
  it("every spec names an Op file that exists, and vice versa", () => {
    const declared = specs()
      .map((s) => s.name)
      .sort();
    const onDisk = readdirSync(join(exampleDir, "src"))
      .filter((f) => f.endsWith(".op.ts"))
      .map((f) => f.replace(/\.op\.ts$/, ""))
      .sort();
    assert.deepEqual(declared, onDisk);
  });

  it("estates-plan runs on a pull request and estates-apply on a push to main", () => {
    const byName = new Map(specs().map((s) => [s.name, s]));
    assert.deepEqual(byName.get("estates-plan")!.trigger, { kind: "pull_request", branches: ["main"] });
    assert.deepEqual(byName.get("estates-apply")!.trigger, { kind: "push", branches: ["main"] });
  });

  it("neither spec declares a finding mode, so both jobs report and neither posts", () => {
    for (const spec of specs()) assert.equal(spec.findingMode, undefined);
  });
});

describe("the ordering lives in the Op, because nothing else can carry it", () => {
  before(() => {
    execFileSync("npx", ["chant", "build"], { cwd: exampleDir, stdio: "pipe", encoding: "utf8" });
  });

  function opJson(name: string): any {
    return JSON.parse(readFileSync(join(exampleDir, "dist", "ops", name, "op.json"), "utf8"));
  }

  /** Every root a phase's activity steps name, in step order. */
  function rootsOf(phase: any): string[] {
    return phase.steps.filter((s: any) => s.kind === "activity" && s.args?.root).map((s: any) => s.args.root);
  }

  it("estates-apply is one Op over two roots: Network first, Service second", () => {
    const op = opJson("estates-apply");
    assert.deepEqual(
      op.phases.map((p: any) => p.name),
      ["Network", "Service"],
      "the phase order IS the dependency. terraformRootSchema is {dir, workspace, varFiles, " +
        "backendConfig, delete} and there is no dependsOn in the terraform lexicon, so nothing else " +
        "in this project says the producer applies first.",
    );
    assert.deepEqual(rootsOf(op.phases[0]), ["network", "network", "network"]);
    assert.deepEqual(rootsOf(op.phases[1]), ["service", "service", "service"]);
  });

  it("each phase applies the plan file its own plan step wrote", () => {
    const op = opJson("estates-apply");
    for (const [phase, step] of [
      [op.phases[0], "network-plan"],
      [op.phases[1], "service-plan"],
    ] as const) {
      const apply = phase.steps.find((s: any) => s.fn === "terraformApply");
      assert.deepEqual(apply.args.planFile, { kind: "step-output-ref", step, path: "planFile" });
    }
  });

  it("estates-apply carries an onFailure phase, because phase order alone does not say what is left behind", () => {
    const op = opJson("estates-apply");
    assert.ok(Array.isArray(op.onFailure) && op.onFailure.length === 1, "expected exactly one onFailure phase");
    const cmd = op.onFailure[0].steps[0].args.cmd as string;
    assert.match(cmd, /Nothing was rolled back/);
    assert.match(cmd, /the Service phase never started/);
  });

  it("estates-plan runs the same two roots in the same order, and writes nothing", () => {
    const op = opJson("estates-plan");
    assert.deepEqual(
      op.phases.map((p: any) => p.name),
      ["Network", "Service"],
    );
    assert.deepEqual(rootsOf(op.phases[0]), ["network"]);
    assert.deepEqual(rootsOf(op.phases[1]), ["service"]);
    const fns = op.phases.flatMap((p: any) => p.steps.map((s: any) => s.fn));
    assert.deepEqual(fns, ["choudoufuLivePlan", "choudoufuLivePlan"]);
  });
});
