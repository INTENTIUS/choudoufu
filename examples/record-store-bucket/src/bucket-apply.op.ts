/**
 * `bucket-apply`: create or update the bucket, behind an approval gate.
 *
 * `gate: "always"` rather than the default `"on-destroy"`. The usual argument
 * for the default — only stop when something would be destroyed — is wrong
 * here in both directions. A change to the bucket policy destroys nothing and
 * can still lock every run out of its own records, and a change to the KMS key
 * destroys nothing and can still make every existing record unreadable.
 *
 * So every apply stops for a human, including the additive ones.
 */
import { TerraformApplyOp } from "@intentius/chant-lexicon-terraform";

const { op } = TerraformApplyOp({
  name: "bucket-apply",
  root: "bucket",
  gate: "always",
});

export default op;
