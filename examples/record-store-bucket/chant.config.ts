import type { ChantConfig } from "@intentius/chant/config";
import "@intentius/chant-lexicon-terraform";

/**
 * One project, one root, two Ops.
 *
 * This root is deliberately STOCK. It runs `tofu`, not `choudoufu`, and it
 * declares no estate — because it builds the bucket a live estate's record
 * store writes into, and a store cannot hold the records of the thing that
 * creates it. Bootstrapping infrastructure is the one place a state file is
 * the right answer, and this is that place.
 *
 * `delete: "gated"` because destroying this bucket destroys an estate's
 * identity for every resource it records. The Op's approval gate is the point
 * of the setting, not ceremony.
 */
export default {
  lexicons: ["terraform"],
  terraform: {
    binary: "tofu",
    roots: {
      bucket: { dir: "./terraform", delete: "gated" },
    },
  },
} satisfies ChantConfig;
