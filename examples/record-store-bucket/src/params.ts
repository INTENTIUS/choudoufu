/**
 * What this project lets you choose, and why each one is a choice rather than
 * a constant.
 */

/**
 * The bucket name. Required, and deliberately without a fallback.
 *
 * A bucket name is globally unique, so there is no default that is correct
 * for two people. `just up` passes it, deriving
 * `choudoufu-records-<account>-<region>` when you do not name one yourself.
 * Any name works; nothing here assumes the derived shape.
 */
const name = process.env.RECORD_BUCKET;
if (!name) {
  throw new Error(
    "RECORD_BUCKET is not set. A bucket name is globally unique, so this project refuses to " +
      "invent one: pass `just up <name>`, or export RECORD_BUCKET. `just up` with no argument " +
      "derives choudoufu-records-<account>-<region> for you.",
  );
}
export const bucketName = name;

/**
 * How many days a superseded or deleted record stays recoverable. Default 30.
 *
 * This is not a storage-cost knob with a recovery side effect. It is the
 * recovery window. The bucket is versioned, so a delete writes a delete marker
 * and an overwrite keeps the previous envelope as a noncurrent version, and
 * both survive for exactly this many days. A record-backed resource carries
 * no marker and cannot be imported under a live block, so for that slice this
 * number is the whole answer to "how long do we have to notice that a record
 * was destroyed by mistake". Set it on purpose.
 *
 * `just up` does not reset it behind your back: when you do not pass a value
 * and the bucket already exists, it keeps whatever the live bucket has.
 */
const days = Number(process.env.RECORD_NONCURRENT_DAYS ?? "30");
if (!Number.isInteger(days) || days < 1) {
  throw new Error(
    `RECORD_NONCURRENT_DAYS must be a whole number of days, at least 1; got ${JSON.stringify(process.env.RECORD_NONCURRENT_DAYS)}. ` +
      "Zero would expire a superseded record immediately, which is no recovery window at all.",
  );
}
export const noncurrentDays = days;

/**
 * The ARN of a customer managed KMS key YOU own, or unset.
 *
 * Unset, the bucket takes S3's own default encryption (SSE-S3), which every
 * bucket has had since January 2023. choudoufu works under every SSE flavour
 * and asserts none of them.
 *
 * Set, the bucket's default encryption becomes SSE-KMS under your key, and
 * the bucket policy refuses a put that asks for anything else. A customer
 * managed key is what the documentation recommends, because it is the one
 * flavour where you write the key policy and can cut access by revoking the
 * key.
 *
 * This project never creates the key. A key minted by `up` would be a key
 * `down` could delete, and deleting it makes every record unreadable: that
 * power stays in your hands. The key policy must let the estate's role use it
 * (kms:Decrypt, kms:GenerateDataKey).
 */
export const kmsKeyArn = process.env.RECORD_KMS_KEY_ARN?.trim() || undefined;
