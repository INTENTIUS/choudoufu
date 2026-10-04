/**
 * `backend-prepare`: stand up the bucket this estate's records live in,
 * before anything needs it. GitHub issue #1832, #1244 ruling 3.
 *
 * Every other Op in this project assumes the record store already exists.
 * `live-plan` reads it, `live-apply` writes it, `live-discover` narrows its
 * sweep by it. This is the one that creates it, so a fresh environment stands
 * its bucket up through the same gated path as everything else here instead
 * of somebody running `just up` on a laptop first.
 *
 * ## It runs the bucket project's own recipes, not a copy of them
 *
 * The bucket is declared in `examples/record-store-bucket` through chant's
 * AWS lexicon, and `just up` there does more than deploy the built template.
 * It reads the live bucket first: it keeps the noncurrent-version window it
 * finds (so a re-run never resets a recovery window somebody chose), and it
 * refuses a run that would silently drop the bucket's KMS key and the two
 * Deny statements that go with it (#1421). The bucket carries
 * `DeletionPolicy: Retain`, and this project never creates a key. An Op that
 * built the template and handed it to `awsApply` would skip all of that,
 * which is why #1244's first draft of this Op was not revived.
 *
 * So each step below is one call into `scripts/backend-prepare.sh`, which
 * calls those recipes and reads the bucket's name out of
 * `terraform/estate.chdf.hcl`, the line the estate's own runs read. There is
 * no second spelling of either the bucket or its refusals.
 *
 * ## Why the trigger is a push to `bootstrap`
 *
 * Not `main`. Preparing the backend is rare and deliberate, and a gate on
 * every push to main would leave a pending approval on every merge, which is
 * how a gate becomes something people click through. A push to `bootstrap`
 * is somebody saying "stand this environment up". See `generate.ts`.
 *
 * ## Why the gate is unconditional
 *
 * The usual rule, gate only what destroys, is wrong here in both directions.
 * A bucket-policy change destroys nothing and can still lock every run out of
 * its own records. A shorter lifecycle window destroys nothing today and
 * shortens how long a record deleted by mistake stays recoverable. Neither
 * shows up as a destroy anywhere. `bucket-apply` in the bucket project makes
 * the same argument for its own gate.
 *
 * CloudFormation makes a second run with nothing to change a no-op rather
 * than a second bucket, and it still stops at the gate, because "nothing to
 * change" is a claim this Op should not be the one to make on its own.
 *
 * ## What the approval covers
 *
 * The Plan step prints one line, the SHA256 of the CloudFormation template it
 * built (the template itself is in the step's log for the reviewer), and the
 * gate is bound to it (`plan`, chant #2300). An approval is for that
 * template: a later run whose template differs is refused by name instead of
 * standing up a bucket nobody read.
 *
 * ## Why every step is a single attempt
 *
 * `policyCheck` is chant's single-attempt profile, and it is used here for
 * that property rather than for its name. The default shell profile retries
 * three times, and a refusal (wrong account, an encryption downgrade) is the
 * same answer on every attempt; retrying `up` behind a CloudFormation update
 * still in progress turns one clear failure into a confusing second one.
 *
 * ## Verify
 *
 * The last step asks `choudoufu live-bucket` from the estate's root, which is
 * the same contract check (versioning, a lifecycle that expires noncurrent
 * versions, public-access block) every run of the estate makes before its
 * first write, under the sidecar's own `bucket_owner`. A bucket this Op stood
 * up that the estate would refuse fails here, not on the next `live-apply`.
 */

import { Op, phase, gate, shell, stepOutput } from "@intentius/chant/op";

const SCRIPT = "bash scripts/backend-prepare.sh";

const plan = shell(`${SCRIPT} plan`, { id: "template", profile: "policyCheck" });

export default Op({
  name: "backend-prepare",
  overview:
    "Create or update the S3 bucket this estate's record store writes into, behind an approval gate, then check it against the bucket contract",
  phases: [
    phase("Plan", [plan]),
    phase("Approve", [
      gate("prepare", {
        description:
          "This creates or changes the bucket holding this estate's records. A policy change destroys " +
          "nothing and can still lock runs out of their own records, and a shorter lifecycle window " +
          "shortens how long a deleted record stays recoverable. The template is in the Plan step's log.",
        plan: stepOutput(plan),
      }),
    ]),
    phase("Apply", [shell(`${SCRIPT} up`, { profile: "policyCheck" })]),
    phase("Verify", [shell(`${SCRIPT} verify`, { profile: "policyCheck" })]),
  ],
});
