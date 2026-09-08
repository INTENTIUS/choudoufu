/**
 * `live-adopt`: reconcile, which on a choudoufu estate is adoption rather
 * than regeneration.
 *
 * Stock Terraform has no typed path from a live resource back to HCL, so
 * chant's cloud-to-code position is open for the terraform lexicon. On a live
 * root the question is different rather than harder: `live-plan` already names
 * every live resource that exactly matches a declared block and carries no
 * marker, and it prints the two tag values that would claim it. So reconcile
 * here is "claim what the configuration already describes", and it is a tag
 * write. Ownership reconciles back to the estate; not one line of source
 * changes.
 *
 * Four phases, built by the composite: Check (`live-check`, the cheapest place
 * to find out the ledger below would not be authoritative), Ledger
 * (`live-plan` in adoption-only mode, which is what the gate approves), Gate,
 * and Adopt (the marker writes themselves). An address more than one live
 * resource sits at is reported and never written - a wrong marker adopts or
 * displaces a real object, and it is silent where a refusal is loud.
 *
 * `live-import` is not an Op and is not this. An estate that still has its
 * `terraform.tfstate` should run `choudoufu live-import` once by hand: it
 * reads every instance, index and key included, straight out of the state
 * file, which is the blind spot content matching cannot cover.
 */

import { TerraformAdoptOp } from "@intentius/chant-lexicon-terraform";

const { op } = TerraformAdoptOp({
  name: "live-adopt",
  root: "estate",
});

export default op;
