import type { ChantConfig } from "@intentius/chant/config";
import "@intentius/chant-lexicon-terraform";
import "@intentius/chant-lexicon-aws";

/**
 * One project, one root, six Ops.
 *
 * `binary: "choudoufu"` alone does not make a root live - it runs stock,
 * exactly as "terraform" and "tofu" do. What makes this one live is that
 * `./terraform` also declares an estate, in the `estate.chdf.hcl` sidecar
 * beside its .tf files. chant's terraform lexicon reads either form out of
 * the same HCL parse.
 *
 * The "aws" lexicon is here for one Op. `backend-prepare` applies the
 * CloudFormation that `examples/record-store-bucket` builds from its own
 * lexicon declaration, which is the bucket every other Op's record store
 * writes into. The estate root itself is untouched by it.
 *
 * `delete: "gated"` is chant's delete dial for this root: an orphaned marked
 * resource (choudoufu's `undeclared_tagged` quadrant) is deleted only through
 * an apply that crossed an approval gate, which is what `live-apply` builds.
 * `"never"` would additionally require the root's own `policy` block to say
 * so, and TF026 fails the build naming the setting if it does not.
 */
export default {
  lexicons: ["terraform", "aws"],
  terraform: {
    binary: "choudoufu",
    roots: {
      estate: { dir: "./terraform", delete: "gated" },
    },
  },
} satisfies ChantConfig;
