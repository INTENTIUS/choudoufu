import type { ChantConfig } from "@intentius/chant/config";
import "@intentius/chant-lexicon-terraform";

/**
 * One project, two live roots, two Ops.
 *
 * `binary: "choudoufu"` alone does not make a root live - it runs stock,
 * exactly as "terraform" and "tofu" do. What makes these two live is that
 * each directory also declares an estate, in the `estate.chdf.hcl` sidecar
 * beside its .tf files. Two sidecars, two different `estate` values, two
 * independent runs: see the README's "Why two estates".
 *
 * `terraformRootSchema` is `{dir, workspace, varFiles, backendConfig,
 * delete}`. There is no `dependsOn`, here or anywhere in the terraform
 * lexicon, so nothing in this file says that `service` needs `network`
 * first. That edge is Op phase order, in `src/estates-apply.op.ts`.
 *
 * Two fields are deliberately absent from both roots:
 *
 *  - `workspace`. choudoufu's TF025 refuses a non-default workspace on a
 *    live root: a workspace is a state-file partition, and a live root has
 *    no state file to partition. Two estates are how this project separates
 *    two domains, which is the answer workspaces were reached for.
 *  - `backendConfig`. TF024 refuses a `backend`/`cloud` block on a live root
 *    for the same underlying reason - ownership is the marker tags on the
 *    resources, not an entry in a state file.
 *
 * `delete: "owned-only"` on both is choudoufu's own default verb for the
 * `undeclared_tagged` quadrant, and it is what this project wants: an
 * orphaned resource still carrying this estate's markers is this estate's to
 * remove. `"gated"` would promise an approval gate, and neither Op here
 * builds one - the gated-apply shape is examples/ci-pipelines' `live-apply`.
 */
export default {
  lexicons: ["terraform"],
  terraform: {
    binary: "choudoufu",
    roots: {
      network: { dir: "./terraform/network", delete: "owned-only" },
      service: { dir: "./terraform/service", delete: "owned-only" },
    },
  },
} satisfies ChantConfig;
