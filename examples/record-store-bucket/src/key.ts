/**
 * The key the records are encrypted under.
 *
 * A customer-managed key rather than SSE-S3. SSE-S3 is a legitimate, simpler
 * choice and `bucket.ts` takes it with one edit - set `SSEAlgorithm` to
 * "AES256", drop `KMSMasterKeyID`, and drop the `DenyWrongKey` statement with
 * it. A CMK is used here because the protection is then auditable and
 * revocable: its own key policy, its own grants, its own CloudTrail entries,
 * and revoking it makes every record unreadable in one action.
 */

import { KmsKey, KMSAlias } from "@intentius/chant-lexicon-aws";
import { bucketName } from "./params";

export const recordsKey = new KmsKey({
  Description: `choudoufu record store: ${bucketName}`,
  EnableKeyRotation: true,
  PendingWindowInDays: 30,
});

// The alias is a convenience for humans reading CloudTrail and the console;
// nothing in the estate points at it, so COR004 reads the leaf as dead code.
// chant-disable-next-line COR004
export const recordsKeyAlias = new KMSAlias({
  AliasName: `alias/${bucketName}-records`,
  TargetKeyId: recordsKey.KeyId,
});
