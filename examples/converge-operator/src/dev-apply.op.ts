/**
 * `dev-apply`: the only Op in this project allowed to change the estate, and
 * the dispatch target `dev-converge`'s rule table names. Init, `plan
 * -out=chant.tfplan`, Gate on the `show` of that file, `apply chant.tfplan` —
 * the same four-phase shape `examples/ci-pipelines/src/live-apply.op.ts`
 * documents at length; see that file for the mechanics of what the approval
 * covers on a live root and why the plan file crosses the gate rather than
 * being replayed.
 *
 * `gate: "always"` for the same reason as that example: this is the
 * authoritative position on the lifecycle dial for `dev`, so every apply
 * stops for a human rather than only the ones that would destroy something.
 * Its gate name defaults to `approve-dev-apply`.
 *
 * This Op classifies as `mutating`, never `destructive`
 * (`classifyOpVerbClass`, chant's `op-verb-class.ts`): a live root's Plan/
 * Apply steps run through `terraformPlan`/`terraformApply`, and
 * `terraform.roots.estate.delete: "gated"` only escalates a step to
 * `destructive` for the generic `nativeApply` activity other lexicons use —
 * terraform's own steps aren't in that set, so this stays `mutating`. That is
 * what makes it a legal `run()` target for `dev-converge`'s dial `"apply"`:
 * `OPS014` refuses a mutating dispatch under any other dial, and refuses a
 * destructive one under every dial including `"apply"` — see
 * `dev-converge.op.ts`'s doc comment for the rule that names it.
 */

import { TerraformApplyOp } from "@intentius/chant-lexicon-terraform";

const { op } = TerraformApplyOp({
  name: "dev-apply",
  root: "estate",
  gate: "always",
});

export default op;
