import type { ChantConfig } from "@intentius/chant/config";
import "@intentius/chant-lexicon-aws";

/**
 * One lexicon, one declaration, two Ops.
 *
 * There is no terraform root here and that is the point. This is a bootstrap
 * step: it builds the bucket a live estate's record store writes into, so it
 * cannot itself keep records in that store. An earlier version of this example
 * answered that with a stock OpenTofu root and a state file on disk - which
 * meant you needed a state file to create the bucket that exists so you would
 * not need state files.
 *
 * Declaring the bucket in the AWS lexicon removes the circle rather than
 * living with it. `chant build` emits CloudFormation, CloudFormation holds the
 * stack's own identity, and nothing on the path needs tofu installed or a
 * `terraform.tfstate` to hold.
 */
export default {
  lexicons: ["aws"],
} satisfies ChantConfig;
