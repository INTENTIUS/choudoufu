/**
 * What this example lets you choose, and why each one is a choice rather than
 * a constant.
 *
 * A bucket name is globally unique, so there is no default that is correct for
 * two people. The prefix and the role list are choices because the topology
 * this example expects - one bucket, many estates, access scoped by prefix -
 * is a topology you configure rather than one it can guess.
 */

/**
 * The bucket name. Required, and deliberately without a fallback.
 *
 * `just up` passes it, deriving `choudoufu-records-<account>-<region>` when
 * you do not name one yourself. Any name you like works; nothing below or in
 * `bucket.ts` assumes the derived shape.
 */
const name = process.env.RECORD_BUCKET;
if (!name) {
  throw new Error(
    "RECORD_BUCKET is not set. A bucket name is globally unique, so this example refuses to " +
      "invent one: pass `just up <name>`, or export RECORD_BUCKET. `just up` with no argument " +
      "derives choudoufu-records-<account>-<region> for you.",
  );
}
export const bucketName = name;

/**
 * The namespace inside the bucket that records live under.
 *
 * One bucket serving several estates is the intended topology: the
 * `record_store` block takes a `key_prefix`, and the policy in `bucket.ts`
 * scopes access by it.
 */
export const keyPrefix = process.env.RECORD_PREFIX ?? "choudoufu";

/**
 * Principals allowed to read and write records, as a comma-separated list of
 * role ARNs.
 *
 * Empty means the bucket policy grants nobody, and that is the deliberate
 * default for a first apply: access is then whatever IAM already allows, and
 * you add the grant once you know which role your runs assume. A policy that
 * granted something plausible on day one would be a policy nobody ever
 * narrowed.
 */
export const estateRoleArns = (process.env.RECORD_ROLE_ARNS ?? "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);
