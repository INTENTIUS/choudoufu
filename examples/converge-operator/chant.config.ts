import type { ChantConfig } from "@intentius/chant/config";
import "@intentius/chant-lexicon-terraform";

/**
 * One project, one live root, two Ops: a `ConvergeOp` (the loop) and the
 * `TerraformApplyOp` it dispatches (the gated write). `examples/ci-pipelines`
 * is the same estate shape driven by five forge-triggered Ops; this project
 * drives the one root instead from `chant operator` ticking on an interval,
 * which is issue #1033's whole point — the replan-and-converge loop, shown
 * live rather than generated into someone else's CI.
 *
 * `binary: "choudoufu"` plus the `estate.chdf.hcl` sidecar under `./terraform`
 * is what makes this root live, exactly as in `examples/ci-pipelines`; see
 * that project's own `chant.config.ts` for the longer version of this note.
 *
 * `environments: ["dev"]` names the environment `dev-converge.op.ts` converges
 * and `dev-apply.op.ts` is dispatched into. Nothing here binds "dev" to the
 * `estate` root specifically — chant's terraform lexicon reads every declared
 * live root regardless of which environment name a caller passes (the
 * environment string only matters to lexicons whose reads are
 * credential/profile-scoped, aws's `describeResources` for a terraform root
 * is not one of them) — so with one root declared, "dev" resolves to it by
 * construction. A second root would need `terraform.roots` to grow a second
 * entry and this project's Ops to name it explicitly; nothing about
 * `environments` picks that out on its own.
 */
export default {
  lexicons: ["terraform"],
  environments: ["dev"],
  terraform: {
    binary: "choudoufu",
    roots: {
      estate: { dir: "./terraform", delete: "gated" },
    },
  },
} satisfies ChantConfig;
