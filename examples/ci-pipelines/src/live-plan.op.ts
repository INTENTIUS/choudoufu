/**
 * `live-plan`: the pull-request half. Init, live-plan, report. Nothing here
 * ever changes the estate.
 *
 * `live: true` swaps the Plan step from `terraformPlan` to
 * `choudoufuLivePlan`, and that is the whole difference between a stock watch
 * and this one. A stock plan answers one question - did the root drift - and a
 * live plan answers three off a single read, because ownership is a pair of
 * tags on the resources rather than an entry in a state file:
 *
 *   Drift      - `live-plan -detailed-exitcode` came back 2
 *   Unowned    - live resources sitting at a declared identity with no marker
 *   Adoptable  - the subset an exact content match makes claimable
 *
 * All three ride out as run outcomes. The body that gets posted is the
 * `-no-color` plan text with the adoption ledger under it, and nothing else:
 * the `-json` document stays inside the Op, because it carries live identities
 * and attribute values for every resource the run touched.
 *
 * The finding mode is the one thing that differs per forge; see `./forge.ts`.
 */

import { TerraformWatchOp } from "@intentius/chant-lexicon-terraform";
import { planFindingMode } from "./forge";

const { op } = TerraformWatchOp({
  name: "live-plan",
  root: "estate",
  live: true,
  findingMode: planFindingMode,
  title: 'choudoufu live plan for estate "ci-pipelines-example"',
});

export default op;
