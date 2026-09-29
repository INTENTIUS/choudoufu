/**
 * `bucket-plan`: what standing the record store up would do.
 *
 * With the bucket declared in a lexicon, the plan IS the built template - a
 * CloudFormation document you can read before anything reaches an account.
 * This Op builds it and stops.
 *
 * It is deliberately not a change set. chant has no CFN change-set activity
 * today, so a step claiming to diff against the live stack would be a step
 * doing something else under that name. What this proves is what will be
 * submitted, which is the half that a review can actually act on; whether the
 * live stack already matches is CloudFormation's own answer at apply time.
 *
 * Read-only, no credential that can write, safe on a pull request.
 */
import { Op, phase, build } from "@intentius/chant/op";

export default Op({
  name: "bucket-plan",
  overview: "Build the record-store bucket template for review, without touching an account",
  phases: [phase("Build", [build(".")])],
});
