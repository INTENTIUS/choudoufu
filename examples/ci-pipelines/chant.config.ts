import type { ChantConfig } from "@intentius/chant/config";
import "@intentius/chant-lexicon-terraform";

/**
 * One project, one root, five Ops.
 *
 * `binary: "choudoufu"` alone does not make a root live - it runs stock,
 * exactly as "terraform" and "tofu" do. What makes this one live is that
 * `./terraform` also declares an estate, in the `estate.chdf.hcl` sidecar
 * beside its .tf files. chant's terraform lexicon reads either form out of
 * the same HCL parse.
 *
 * `delete: "gated"` is chant's delete dial for this root: an orphaned marked
 * resource (choudoufu's `undeclared_tagged` quadrant) is deleted only through
 * an apply that crossed an approval gate, which is what `live-apply` builds.
 * `"never"` would additionally require the root's own `policy` block to say
 * so, and TF026 fails the build naming the setting if it does not.
 */
export default {
  lexicons: ["terraform"],
  terraform: {
    binary: "choudoufu",
    roots: {
      estate: { dir: "./terraform", delete: "gated" },
    },
  },
} satisfies ChantConfig;
