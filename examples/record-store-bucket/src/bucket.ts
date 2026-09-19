/**
 * The bucket a live estate's `record_store "s3"` writes into, declared through
 * the AWS lexicon rather than written as HCL.
 *
 * # Why the lexicon and not a terraform root
 *
 * This is a bootstrap step, and a stock OpenTofu root would need a state file
 * to create the bucket that exists so you would not need state files.
 * `chant build` emits a CloudFormation template, CloudFormation holds the
 * stack's identity, and nothing on the path needs tofu installed.
 *
 * # What this file is held to
 *
 * choudoufu asserts three things about a record store bucket and refuses one
 * that fails any of them: versioning, a lifecycle rule that expires
 * noncurrent versions, and public-access block. `just verify` asks the
 * choudoufu binary whether THIS bucket satisfies them. Nothing here or in the
 * justfile re-implements those checks, so the project and the tool cannot
 * drift into two opinions about what a correct bucket is.
 *
 * Encryption at rest is not one of the three, deliberately: S3 encrypts every
 * object by default, so that check could not fail. See `params.ts` for the
 * optional customer managed key. GitHub issues #1332, #1339, #1341.
 */

// A correct bucket is one concern that CloudFormation happens to spell with
// several property types, and splitting it by type would put the lifecycle
// somewhere other than the versioning it depends on.
// chant-disable COR009 -- the bucket and its settings are a single concern
//
// COR013's heuristic reads any class name containing "Policy" as
// configuration. S3BucketPolicy is a resource, and it belongs beside the
// bucket it constrains.
// chant-disable COR013 -- S3BucketPolicy is a resource, not bucket configuration

import {
  Bucket,
  Bucket_AbortIncompleteMultipartUpload,
  Bucket_BucketEncryption,
  Bucket_LifecycleConfiguration,
  Bucket_NoncurrentVersionExpiration,
  Bucket_PublicAccessBlockConfiguration,
  Bucket_Rule,
  Bucket_ServerSideEncryptionByDefault,
  Bucket_ServerSideEncryptionRule,
  Bucket_VersioningConfiguration,
  Ref,
  S3BucketPolicy,
} from "@intentius/chant-lexicon-aws";
import { bucketName, kmsKeyArn, noncurrentDays } from "./params";

/**
 * Default encryption under the operator's own key, when one was given.
 *
 * Hoisted to a const because a conditional value has to be traceable to one
 * (EVL004). Bucket keys are on because records are many and small and every
 * plan reads the whole namespace: without one, the per-object KMS calls cost
 * real money at ten thousand objects.
 */
const encryption = kmsKeyArn
  ? new Bucket_BucketEncryption({
      ServerSideEncryptionConfiguration: [
        new Bucket_ServerSideEncryptionRule({
          ServerSideEncryptionByDefault: new Bucket_ServerSideEncryptionByDefault({
            SSEAlgorithm: "aws:kms",
            KMSMasterKeyID: kmsKeyArn,
          }),
          BucketKeyEnabled: true,
        }),
      ],
    })
  : undefined;

export const recordsBucket = new Bucket({
  BucketName: bucketName,

  // Asserted by choudoufu. A record can be the only copy of what it says, so
  // an overwrite or a delete in an unversioned bucket is final.
  VersioningConfiguration: new Bucket_VersioningConfiguration({ Status: "Enabled" }),

  // Asserted by choudoufu. Records hold secret material.
  PublicAccessBlockConfiguration: new Bucket_PublicAccessBlockConfiguration({
    BlockPublicAcls: true,
    BlockPublicPolicy: true,
    IgnorePublicAcls: true,
    RestrictPublicBuckets: true,
  }),

  // Asserted by choudoufu: noncurrent versions EXPIRE. Never current objects.
  //
  // A record is not a log. Expiring a current object would delete an estate's
  // identity for a resource that still exists, and the next plan would
  // propose creating something already there. Only superseded versions are
  // safe to expire.
  //
  // The rule has no prefix or tag filter on purpose. choudoufu counts a rule
  // only when it can show the rule reaches every key an estate writes, and an
  // unfiltered rule is the one shape that is true of for every estate that
  // will ever share this bucket.
  LifecycleConfiguration: new Bucket_LifecycleConfiguration({
    Rules: [
      new Bucket_Rule({
        Id: "expire-superseded-record-versions",
        Status: "Enabled",
        NoncurrentVersionExpiration: new Bucket_NoncurrentVersionExpiration({
          NoncurrentDays: noncurrentDays,
        }),

        // The last trace of a deleted record is a delete marker with nothing
        // left under it, once the noncurrent versions below it have expired.
        // Without this, those markers accumulate forever, every one of them
        // counts as a version, and `just down` refuses a bucket whose records
        // are all long gone (GitHub issue #1382).
        //
        // Two things make this safe to set here and not elsewhere. S3 rejects
        // a rule that combines ExpiredObjectDeleteMarker with an expiry by
        // days or date, or with tag filters, and this rule has none of those:
        // only the noncurrent expiry and the multipart abort. And choudoufu's
        // own bucket contract reads it correctly:
        // internal/live/staterecord/bucketcontract.go's
        // `expiresCurrentObjects` looks at Expiration.Days and
        // Expiration.Date, so a marker cleanup is not read as a rule that
        // deletes records, and `just verify` still passes.
        ExpiredObjectDeleteMarker: true,

        AbortIncompleteMultipartUpload: new Bucket_AbortIncompleteMultipartUpload({
          DaysAfterInitiation: 7,
        }),
      }),
    ],
  }),

  BucketEncryption: encryption,
},
// The bucket outlives its stack. A record is the only copy of an estate's
// identity for a record-backed resource, and without Retain a rollback, a
// stack rename, a `delete-stack` typed at the wrong terminal or a property
// change CloudFormation decides to do by replacement all take the records
// with them. With it, the worst a stack operation can do is orphan the
// bucket, which loses nothing.
//
// UpdateReplacePolicy as well as DeletionPolicy, and the second one is the
// one that would otherwise be missed: DeletionPolicy covers deleting the
// stack, UpdateReplacePolicy covers CloudFormation replacing the resource
// during an update, which is what a BucketName change is.
//
// `just down` prints what this means, because the recipe's final line used to
// say the bucket was gone and it no longer is.
{ DeletionPolicy: "Retain", UpdateReplacePolicy: "Retain" });

/**
 * Statements that only make sense with a customer managed key.
 *
 * # The null-condition trap, which cost a 52-minute run to find
 *
 * The obvious way to write "deny puts under the wrong encryption" is a Deny
 * on `StringNotEquals` for `s3:x-amz-server-side-encryption`, and it is
 * wrong. That condition key exists only when the REQUEST CARRIES THE HEADER.
 * A client that sends no encryption header at all - which is what choudoufu's
 * S3 store does, and what most SDK callers do - produces a null key,
 * `StringNotEquals` against null is true, and the Deny fires on precisely the
 * well-behaved request that bucket default encryption was about to encrypt
 * correctly. The first version of this project shipped that policy. It passed
 * every configuration check and made the bucket unusable as a record store.
 *
 * So each Deny is also conditioned on the header being PRESENT
 * (`Null: { ...: "false" }`). The header-less request is covered by the
 * bucket's default encryption above, which is the same key:
 *
 *   - no header            -> bucket default: SSE-KMS under your key
 *   - header, wrong algo   -> DenyWrongEncryptionAlgorithm
 *   - header, wrong key    -> DenyWrongKey
 *
 * `just verify` tests that table when a key is configured.
 */
const keyStatements = kmsKeyArn
  ? [
      {
        Sid: "DenyWrongEncryptionAlgorithm",
        Effect: "Deny",
        Principal: "*",
        Action: "s3:PutObject",
        Resource: `arn:aws:s3:::${bucketName}/*`,
        Condition: {
          StringNotEquals: { "s3:x-amz-server-side-encryption": "aws:kms" },
          Null: { "s3:x-amz-server-side-encryption": "false" },
        },
      },
      {
        Sid: "DenyWrongKey",
        Effect: "Deny",
        Principal: "*",
        Action: "s3:PutObject",
        Resource: `arn:aws:s3:::${bucketName}/*`,
        Condition: {
          StringNotEquals: { "s3:x-amz-server-side-encryption-aws-kms-key-id": kmsKeyArn },
          Null: { "s3:x-amz-server-side-encryption-aws-kms-key-id": "false" },
        },
      },
    ]
  : [];

/**
 * The bucket policy. It grants nobody anything.
 *
 * Access for an estate's role is IAM, and the policy for that role is
 * published once, in the documentation (GitHub issue #1342): a prefix scope
 * plus BOTH object-tag condition keys. A second copy here would be a second
 * thing to keep correct, and the half-right version of that policy bricks a
 * new estate.
 */
export const recordsPolicyDocument = {
  Version: "2012-10-17",
  Statement: [
    {
      // No Null clause here, and deliberately: aws:SecureTransport is set by
      // S3 on every request rather than by the client, so it is never null
      // and there is no well-behaved request this can catch by accident.
      Sid: "DenyInsecureTransport",
      Effect: "Deny",
      Principal: "*",
      Action: "s3:*",
      Resource: [`arn:aws:s3:::${bucketName}`, `arn:aws:s3:::${bucketName}/*`],
      Condition: { Bool: { "aws:SecureTransport": "false" } },
    },
    ...keyStatements,
  ],
};

/**
 * The policy attached to the bucket.
 *
 * `Bucket` is a `Ref` rather than the literal name, and that is load-bearing.
 * WAW042 - the lexicon's own "no TLS-only policy" check - joins a policy to
 * its bucket by walking the `Bucket` property for Ref/GetAtt logical ids. A
 * literal string is not a reference, so a policy written that way is
 * invisible to the check.
 *
 * Nothing references the policy in turn, which is what COR004 reads as dead
 * code.
 */
// chant-disable-next-line COR004
export const recordsBucketPolicy = new S3BucketPolicy({
  Bucket: Ref(recordsBucket),
  PolicyDocument: recordsPolicyDocument,
});
