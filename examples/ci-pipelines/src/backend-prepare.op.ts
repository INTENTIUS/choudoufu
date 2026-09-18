/**
 * `backend-prepare`: stand up the bucket this estate's records live in,
 * before anything needs it.
 *
 * Every other Op in this project assumes the record store already exists.
 * `live-plan` reads it, `live-apply` writes it, `live-discover` narrows its
 * sweep by it. This is the one that creates it, and it is the reason a fresh
 * environment does not need somebody to run a justfile on a laptop first.
 *
 * # Why the backend is a pipeline step at all
 *
 * A record store on S3 has a bootstrap that Parameter Store did not: a bucket
 * has to exist, encrypted, before the first write. That was the one real
 * argument for SSM, and the answer is not to keep SSM - it is to make the
 * bucket a declared artifact that a pipeline can create, gated, per
 * environment, the same way this project treats everything else.
 *
 * The bucket is declared in `examples/record-store-bucket` through chant's
 * AWS lexicon, so this Op builds that project and applies its template. It
 * does not carry a second copy of the declaration: two spellings of one
 * bucket is how the two drift.
 *
 * # Why the trigger is a push to `bootstrap`
 *
 * Not `main`. Preparing the backend is rare and deliberate, and a gate on
 * every push to main would leave a pending approval on every commit, which is
 * how a gate becomes something people click through. A push to `bootstrap` is
 * somebody saying "stand this environment up".
 *
 * # Why the gate is unconditional
 *
 * `gate("prepare")` runs on create as well as update, which is deliberate and
 * is the same argument `bucket-apply` makes in the example itself. The usual
 * rule - gate only what destroys - is wrong here in both directions. A
 * bucket-policy change destroys nothing and can still lock every run out of
 * its own records. A KMS key change destroys nothing and can still make every
 * existing record unreadable. Neither shows up as a destroy in a plan.
 *
 * CloudFormation makes the create idempotent, so a second run with nothing to
 * change is a no-op rather than a second bucket - but it still stops at the
 * gate, because "nothing to change" is a claim this Op should not be the one
 * to make on its own.
 */

import { Op, phase, build, gate } from "@intentius/chant/op";
import { awsApply } from "@intentius/chant-lexicon-aws";

/** The bucket, and therefore the stack. Globally unique, so it is an input. */
const bucket = process.env.RECORD_BUCKET ?? "choudoufu-records-ci-pipelines-example";
const region = process.env.AWS_REGION ?? "us-east-2";

export default Op({
  name: "backend-prepare",
  overview: "Create or update the S3 bucket this estate's record store writes into, behind an approval gate",
  labels: { TerraformRoot: "estate", TerraformMode: "live", Backend: "s3" },
  phases: [
    // Builds the sibling project's CloudFormation from its lexicon
    // declaration. Nothing here needs credentials yet.
    phase("Build", [build("../record-store-bucket")]),

    phase("Approve", [
      gate("prepare", {
        description:
          "This creates or changes the bucket holding this estate's record store. A policy or key " +
          "change destroys nothing and can still lock runs out of their own records, or make every " +
          "existing record unreadable.",
      }),
    ]),

    phase("Apply", [
      awsApply("../record-store-bucket/templates/template.json", {
        stackName: bucket,
        region,
      }),
    ]),
  ],
});
