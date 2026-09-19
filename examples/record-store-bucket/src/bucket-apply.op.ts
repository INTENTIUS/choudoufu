/**
 * `bucket-apply`: create or update the bucket, behind an approval gate.
 *
 * The gate stops every apply, including the additive ones, and that is
 * deliberate rather than cautious-by-default. The usual argument for gating
 * only destructive changes is wrong here: a change to the bucket policy
 * destroys nothing and can still lock every run out of its own records, and a
 * shorter lifecycle window destroys nothing today and shortens how long a
 * record deleted by mistake can be brought back.
 *
 * A gate is a fact on the gate ledger rather than a wait - the run records it
 * and ends `gated`, `chant approve bucket-apply apply` resolves it, and the
 * next run walks through carrying the approver. For standing a bucket up
 * yourself, `just up` deploys the same built template directly.
 */
import { Op, phase, build, gate } from "@intentius/chant/op";
import { awsApply } from "@intentius/chant-lexicon-aws";
import { bucketName } from "./params";

const stackName = process.env.RECORD_STACK ?? bucketName;
// WAW001 wants AWS::Region here, and it cannot apply: that pseudo-parameter
// resolves to the region a stack is ALREADY in, and this value is the answer
// to which region to submit it to. AWS_REGION overrides; the default matches
// the rest of this example.
// chant-disable-next-line WAW001
const region = process.env.AWS_REGION ?? "us-east-2";

export default Op({
  name: "bucket-apply",
  overview: "Create or update the record-store bucket, stopping for an approver first",
  phases: [
    phase("Build", [build(".")]),
    phase("Approve", [
      gate("apply", {
        description:
          "Applying changes the bucket that holds an estate's identity. A policy change destroys " +
          "nothing and can still lock runs out of their records, and a shorter lifecycle window " +
          "shortens how long a deleted record stays recoverable.",
      }),
    ]),
    phase("Apply", [awsApply("templates/template.json", { stackName, region })]),
  ],
});
