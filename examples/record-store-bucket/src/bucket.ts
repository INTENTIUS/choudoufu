/**
 * The bucket a live estate's `record_store "s3"` writes into, declared through
 * the AWS lexicon rather than written as HCL.
 *
 * # Why the lexicon and not a terraform root
 *
 * This is a bootstrap step, and the first version of this example used
 * OpenTofu with a state file on disk - which meant you needed a state file to
 * create the bucket that exists so you would not need state files. Declaring
 * it here removes that: `chant build` emits the template, `chant run` applies
 * it, and nothing on the path needs tofu installed or a `terraform.tfstate`
 * to hold.
 *
 * It is also simply the house rule: what a lexicon can declare, a lexicon
 * declares.
 *
 * # Why S3Store does not do any of this for you
 *
 * `S3Store`'s own doc comment is explicit - "bucket creation, lifecycle
 * policy, and encryption configuration are the caller's concern". That is the
 * right seam for the store, because it keeps its surface free of anything
 * AWS-credential-shaped. It is the wrong place to leave an operator, because
 * records can carry secret material: the envelope has `sensitive_attributes`
 * and `private` members, and `strict { secrets = "store" }` is the default.
 *
 * So this file is that obligation discharged, and every claim in it is
 * something `just verify` checks against the live bucket. GitHub issue #1244.
 */

// A hardened bucket is one concern that CloudFormation happens to spell with
// ten property types, and splitting it by type would put the encryption
// somewhere other than the thing it encrypts.
// chant-disable COR009 -- the bucket and its hardening are a single concern
//
// COR013's heuristic reads any class name containing "Policy" as
// configuration. S3BucketPolicy is a resource, and it is the resource that
// makes the encryption enforced rather than default, so it belongs beside the
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
import { recordsKey } from "./key";
import { bucketName, estateRoleArns, keyPrefix } from "./params";

export const recordsBucket = new Bucket({
  BucketName: bucketName,

  // Versioning is on for a specific reason rather than out of habit.
  // S3Store's compare-and-swap is ETag-based (If-Match / If-None-Match), and
  // a record's delete is how a tombstone gets written. Versioning makes a
  // delete place a marker rather than destroy history, so a mistaken sweep is
  // recoverable.
  VersioningConfiguration: new Bucket_VersioningConfiguration({ Status: "Enabled" }),

  BucketEncryption: new Bucket_BucketEncryption({
    ServerSideEncryptionConfiguration: [
      new Bucket_ServerSideEncryptionRule({
        ServerSideEncryptionByDefault: new Bucket_ServerSideEncryptionByDefault({
          SSEAlgorithm: "aws:kms",
          KMSMasterKeyID: recordsKey.Arn,
        }),
        // Records are many and small and every plan reads the whole
        // namespace. Without a bucket key the per-object KMS calls cost real
        // money at ten thousand objects.
        BucketKeyEnabled: true,
      }),
    ],
  }),

  PublicAccessBlockConfiguration: new Bucket_PublicAccessBlockConfiguration({
    BlockPublicAcls: true,
    BlockPublicPolicy: true,
    IgnorePublicAcls: true,
    RestrictPublicBuckets: true,
  }),

  // Expire old VERSIONS, never current objects.
  //
  // A record is not a log. Expiring a current object would delete an estate's
  // identity for a resource that still exists, and the next plan would
  // propose creating something already there. Only superseded versions are
  // safe to expire, and they exist only because versioning is on above.
  LifecycleConfiguration: new Bucket_LifecycleConfiguration({
    Rules: [
      new Bucket_Rule({
        Id: "expire-superseded-record-versions",
        Status: "Enabled",
        NoncurrentVersionExpiration: new Bucket_NoncurrentVersionExpiration({ NoncurrentDays: 30 }),
        AbortIncompleteMultipartUpload: new Bucket_AbortIncompleteMultipartUpload({
          DaysAfterInitiation: 7,
        }),
      }),
    ],
  }),
});

/**
 * Least privilege for the runs, scoped to the record prefix.
 *
 * This is the bucket-side mirror of what `aws:ResourceTag/tofu-estate` does
 * for the resources: an estate reaches its own records and no other estate's.
 *
 * Hoisted out of the document below rather than spread inline because a
 * conditional spread has to be traceable to a const to be allowed at all
 * (EVL004), and the rule is right to insist - a statement list assembled from
 * an expression is a policy nobody can read off the page.
 */
const estateStatements =
  estateRoleArns.length > 0
    ? [
        {
          Sid: "EstateRunsReadWriteTheirOwnRecords",
          Effect: "Allow",
          Principal: { AWS: estateRoleArns },
          Action: ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
          Resource: `arn:aws:s3:::${bucketName}/${keyPrefix}/*`,
        },
        {
          Sid: "EstateRunsListTheirOwnRecords",
          Effect: "Allow",
          Principal: { AWS: estateRoleArns },
          // ListObjectsV2 is a bucket-level action, so it cannot be scoped by
          // object key - the prefix condition is what scopes it.
          Action: "s3:ListBucket",
          Resource: `arn:aws:s3:::${bucketName}`,
          Condition: { StringLike: { "s3:prefix": `${keyPrefix}/*` } },
        },
      ]
    : [];

/**
 * The document that makes the encryption real.
 *
 * # The null-condition trap, which cost a 52-minute run to find
 *
 * The obvious way to write "deny unencrypted puts" is a Deny on
 * `StringNotEquals` for `s3:x-amz-server-side-encryption`, and it is wrong.
 * That condition key exists only when the REQUEST CARRIES THE HEADER. A
 * client that sends no encryption header at all - which is what `S3Store`
 * does, and what most SDK callers do - produces a null key, `StringNotEquals`
 * against null is true, and the Deny fires on precisely the well-behaved
 * request that bucket default encryption was about to encrypt correctly.
 *
 * The first version of this example shipped that policy. It passed every
 * configuration check, it passed a write test that only proved the deny
 * fired, and it made the bucket unusable as a record store: the live run
 * failed opening the store, on the sentinel write, with "explicit deny in a
 * resource-based policy".
 *
 * So each Deny below is also conditioned on the header being PRESENT
 * (`Null: { ...: "false" }`), which narrows them to what they were always
 * meant to cover: a client that asks for the wrong thing. The header-less
 * request is not left unprotected - it is covered by the bucket's default
 * encryption above, which is the same CMK. Between the two there is no path
 * that puts a wrongly-encrypted object in this bucket:
 *
 *   - no header            -> bucket default: SSE-KMS under the CMK
 *   - header, wrong algo   -> DenyUnencryptedPuts
 *   - header, wrong key    -> DenyWrongKey
 *
 * `just verify` tests that table rather than restating it, and the pair it
 * writes is chosen to fail if this regresses: an explicit `AES256` put must be
 * refused, AND a header-less put must succeed and come back `aws:kms` under
 * the CMK. The old single check could not tell a correct policy from one that
 * refused everything.
 */
export const recordsPolicyDocument = {
  Version: "2012-10-17",
  Statement: [
    {
      Sid: "DenyWrongEncryptionAlgorithm",
      Effect: "Deny",
      Principal: "*",
      Action: "s3:PutObject",
      Resource: `arn:aws:s3:::${bucketName}/*`,
      Condition: {
        // Both clauses are required. StringNotEquals alone denies the
        // header-less request; Null "false" narrows it to requests that
        // actually carry the header.
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
        StringNotEquals: {
          "s3:x-amz-server-side-encryption-aws-kms-key-id": recordsKey.Arn,
        },
        Null: { "s3:x-amz-server-side-encryption-aws-kms-key-id": "false" },
      },
    },
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
    ...estateStatements,
  ],
};

/**
 * The policy attached to the bucket.
 *
 * `Bucket` is a `Ref` rather than the literal name, and that is load-bearing
 * rather than stylistic. WAW042 - the lexicon's own "no TLS-only policy"
 * check - joins a policy to its bucket by walking the `Bucket` property for
 * Ref/GetAtt logical ids. A literal string is not a reference, so a policy
 * written that way is invisible to the check, and the bucket reads as having
 * no TLS deny at all while carrying one. The Ref is what makes the hardening
 * legible to the thing that verifies it.
 *
 * Nothing references the policy in turn - it is the leaf of the graph, which
 * is what COR004 reads as dead code.
 */
// chant-disable-next-line COR004
export const recordsBucketPolicy = new S3BucketPolicy({
  Bucket: Ref(recordsBucket),
  PolicyDocument: recordsPolicyDocument,
});
