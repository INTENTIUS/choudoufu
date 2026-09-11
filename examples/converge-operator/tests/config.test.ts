/**
 * What this project builds, read back field by field, plus one red-then-
 * green proof that `chant lint`'s OPS014 check actually guards the shape
 * this example's own safety argument depends on.
 *
 * `examples/ci-pipelines/tests/currency.test.ts` regenerates a checked-in
 * tree and diffs it; this project has no generated tree (no forge output,
 * nothing checked in that a generator produces), so there is nothing to
 * regenerate. What can rot silently instead is the *shape* of the two Ops —
 * `dev-converge`'s rule table and dial, `dev-apply`'s gate and verb class —
 * so that is what this file asserts against `chant build`'s own output,
 * and it tampers with a scratch copy of the project to prove the build-time
 * refusal this example's whole `dial: "apply"` argument leans on is real,
 * not assumed.
 *
 * Needs no floci and no docker: `chant build`/`chant lint` read source only.
 * `scripts/demo.sh` is the other half — the live run these Ops describe.
 */

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, before, describe, it } from "node:test";

const exampleDir = join(dirname(fileURLToPath(import.meta.url)), "..");

function readOpJson(dir: string, opName: string): any {
  return JSON.parse(readFileSync(join(dir, "dist", "ops", opName, "op.json"), "utf8"));
}

function build(cwd: string): { status: number; output: string } {
  try {
    const output = execFileSync("npx", ["chant", "build"], { cwd, encoding: "utf8", stdio: "pipe" });
    return { status: 0, output };
  } catch (err: any) {
    return { status: typeof err.status === "number" ? err.status : 1, output: `${err.stdout ?? ""}${err.stderr ?? ""}` };
  }
}

function lint(cwd: string): { status: number; output: string } {
  try {
    const output = execFileSync("npx", ["chant", "lint"], { cwd, encoding: "utf8", stdio: "pipe" });
    return { status: 0, output };
  } catch (err: any) {
    return { status: typeof err.status === "number" ? err.status : 1, output: `${err.stdout ?? ""}${err.stderr ?? ""}` };
  }
}

/** Every `convergeTick` activity step's `args.rules`, read off a built ConvergeOp. */
function rulesOf(opJson: any): any[] {
  for (const phase of opJson.phases) {
    for (const step of phase.steps) {
      if (step.kind === "activity" && step.fn === "convergeTick") return step.args.rules;
    }
  }
  throw new Error("no convergeTick step found");
}

describe("this project builds the two Ops issue #1033 asks for", () => {
  before(() => {
    const r = build(exampleDir);
    assert.equal(r.status, 0, `chant build failed:\n${r.output}`);
  });

  it("dev-converge is a ConvergeOp on env dev, dial apply, with no schedule", () => {
    const op = readOpJson(exampleDir, "dev-converge");
    assert.equal(op.labels.Converge, "true");
    assert.equal(op.labels.Env, "dev");
    assert.equal(op.labels.Dial, "apply");
    assert.equal(op.schedule, undefined, "no cron: chant operator should tick this Op every round, per its own doc comment");
  });

  it("dev-converge's rule table has exactly the two rules this example measured as reachable", () => {
    const rules = rulesOf(readOpJson(exampleDir, "dev-converge"));
    assert.equal(rules.length, 2);

    const recreate = rules.find((r) => r.id === "recreate-deleted");
    assert.ok(recreate, "expected a rule named recreate-deleted");
    assert.deepEqual(recreate.when, { kind: "field-comparison", field: "createCount", op: "gt", value: 0 });
    assert.deepEqual(recreate.then, { kind: "run", op: "dev-apply" });
    assert.ok(recreate.why && recreate.why.length > 0, "every rule must carry its why");
    assert.equal(recreate.flapThreshold, 20, "raised above the default 3 — see the Op's doc comment on why");

    const adopt = rules.find((r) => r.id === "adopt-report");
    assert.ok(adopt, "expected a rule named adopt-report");
    assert.deepEqual(adopt.when, { kind: "field-comparison", field: "adoptCount", op: "gt", value: 0 });
    assert.equal(adopt.then.kind, "report");
    assert.ok(adopt.why && adopt.why.length > 0, "every rule must carry its why");
  });

  it("dev-apply is a gate:always TerraformApplyOp over root estate", () => {
    const op = readOpJson(exampleDir, "dev-apply");
    assert.equal(op.labels.Apply, "true");
    assert.equal(op.labels.TerraformRoot, "estate");
    const gatePhase = op.phases.find((p: any) => p.name === "Gate");
    assert.ok(gatePhase, "expected a Gate phase");
    const gateStep = gatePhase.steps.find((s: any) => s.kind === "gate");
    assert.ok(gateStep, "expected a gate step in the Gate phase");
    assert.equal(gateStep.gate, "approve-dev-apply");
  });

  it("chant lint passes clean on the checked-in project", () => {
    const r = lint(exampleDir);
    assert.equal(r.status, 0, `chant lint failed:\n${r.output}`);
    assert.match(r.output, /No problems found/);
  });
});

describe("OPS014 red-then-green: a mutating dispatch needs dial apply", () => {
  let scratch = "";

  before(() => {
    scratch = mkdtempSync(join(tmpdir(), "converge-operator-ops014-"));
    for (const entry of ["chant.config.ts", "package.json", "package-lock.json", "src", "terraform"]) {
      cpSync(join(exampleDir, entry), join(scratch, entry), { recursive: true });
    }
    // Reuse this checkout's own node_modules rather than a second `npm ci` —
    // this test only needs `chant build`/`chant lint` to run, not a fresh
    // install, and the currency guard above already proved the checked-in
    // tree builds and lints clean with these exact dependencies.
    cpSync(join(exampleDir, "node_modules"), join(scratch, "node_modules"), { recursive: true });
  });

  after(() => {
    if (scratch) rmSync(scratch, { recursive: true, force: true });
  });

  it("RED: dial \"observe\" dispatching dev-apply (mutating) is refused at lint, named OPS014", () => {
    const opFile = join(scratch, "src", "dev-converge.op.ts");
    const original = readFileSync(opFile, "utf8");
    writeFileSync(opFile, original.replace('dial: "apply",', 'dial: "observe",'));

    // Measured, not assumed: OPS014 is a post-synth check, but `chant build`
    // runs it too (not only `chant lint`) and fails loudly on it — there is
    // no window where a build this refusal applies to succeeds quietly.
    // `chant build`'s own error line does not name the check by id (that is
    // `chant lint`'s table, asserted below) — it names the refusal itself.
    const b = build(scratch);
    assert.notEqual(b.status, 0, "chant build should refuse this tampered project too");
    assert.match(b.output, /dial "observe" only permits/);
    const l = lint(scratch);
    assert.notEqual(l.status, 0, "chant lint should refuse this tampered project");
    assert.match(l.output, /OPS014/);
    assert.match(l.output, /dial "observe" only permits/);
  });

  it("GREEN: restoring dial \"apply\" makes the same project lint clean again", () => {
    const opFile = join(scratch, "src", "dev-converge.op.ts");
    const tampered = readFileSync(opFile, "utf8");
    writeFileSync(opFile, tampered.replace('dial: "observe",', 'dial: "apply",'));

    const b = build(scratch);
    assert.equal(b.status, 0, `chant build failed after restoring:\n${b.output}`);
    const l = lint(scratch);
    assert.equal(l.status, 0, `chant lint failed after restoring:\n${l.output}`);
    assert.match(l.output, /No problems found/);
  });
});
