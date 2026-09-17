/**
 * `bucket-plan`: what standing the record store up would do. Read-only, no
 * credential that can write, safe to run on a pull request.
 */
import { TerraformWatchOp } from "@intentius/chant-lexicon-terraform";

const { op } = TerraformWatchOp({
  name: "bucket-plan",
  root: "bucket",
});

export default op;
