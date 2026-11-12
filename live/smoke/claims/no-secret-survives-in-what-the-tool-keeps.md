---
title: "Claim 40: No secret survives unless you let it, or keep it in SSM"
claim: no-secret-survives-in-what-the-tool-keeps
---

# Claim 40: No secret survives unless you let it, or keep it in SSM

Under `strict { secrets = "refuse" }` nothing the tool keeps holds a
secret. A type that generates one is refused by name, and a sensitive
argument is left out of its record.

Under `strict { secrets = "ssm" }` the records go to an S3 bucket and each
secret value to a `SecureString` parameter in SSM Parameter Store under a
customer managed KMS key. The record holds a reference, so the tool still
keeps no copy of the value, and the next plan reads it back (GitHub issue
#2393).

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### no-secret-survives-in-what-the-tool-keeps

    just smoke no-secret-survives-in-what-the-tool-keeps
    BREAK=1 just smoke no-secret-survives-in-what-the-tool-keeps

The steps as they print:

1. `a secret-generating type is refused by name`. A `random_password` and
   an `aws_iam_access_key` under `refuse` are each refused in their own
   words, naming the setting, and nothing is left behind.
2. `an estate with a settable secret applies under refuse`. One
   `aws_db_instance` whose `password` comes from `TF_VAR_db_password`
   applies.
3. `grep everything the run kept for the plaintext`. The record, the data
   dir, the lock file and the configuration hold zero copies of the
   password. The record holds the database's identifier, and no cache
   file exists.
4. `what you can still ask to be written`. A saved plan (`plan -out`) and
   a `TF_LOG=debug` log are the operator's own files, read on their own:
   the value is in the plan's `tfplan` member and in the provider plugin's
   `CreateDBInstance` request entry, as under stock. The record store and
   data dir from that run hold nothing.
5. `the replan under refuse proposes the argument again`. Unchanged and
   with `TF_VAR_db_password` rotated, the plan is `Plan: 0 to add, 1 to
   change, 0 to destroy` on `password`, so a rotated password is always
   sent. Neither plan prints a password or leaves one on disk.
6. `the default, honestly`. With no strict block, the scan finds the
   password in the record and in `.terraform/choudoufu-cache.tfstate`, as
   stock keeps it in its state file. With the cache deleted, the plan is
   `No changes.`
7. `secrets = "ssm": an AWS managed key is refused by name`. The run
   creates its own customer managed key, tagged with the run id, a
   parameter path `/choudoufu/smoke/secrets-<run id>` and a record bucket.
   The same database estate under `secrets = "ssm"` with
   `kms_key_id = "alias/aws/ssm"` is refused at plan time with
   `kms_key_id "alias/aws/ssm" is an AWS managed key by its alias`, and
   no database, record or parameter exists afterwards.
8. `the same estate applies under ssm with the run's customer managed key`.
9. `grep everything the run kept, the bucket's records included`. Every
   version of every object under the estate's prefix is downloaded and
   scanned with the data dir, the lock file and the configuration. Zero
   copies of the password. The record holds `secret_refs`, and no cache
   file exists.
10. `the parameters are SecureString under the run's key, and decrypt to
    the value`. One parameter per reference and nothing else under the
    path. The password's parameter decrypts to the value set.
11. `the replan under ssm reads the password back`. `No changes.`, where
    refuse proposed the password again.
12. `a rotation replaces the parameter and deletes the one it superseded`.
    With `TF_VAR_db_password` rotated the apply is `0 added, 1 changed`,
    the record references a new parameter that decrypts to the new value,
    the old parameter is gone, neither value is in what the run kept, and
    the next plan is `No changes.`
13. `teardown`. All four databases destroyed, no parameter left under the
    run's path, and the run's key scheduled for deletion.

The script numbers these 1, 2, 3, 4, 4b, 5, 6 to 11 and 12; the list above
counts each step once.

`BREAK=1` runs two controls and stops. The first applies the estate with
`secrets = "store"` and runs the step 3 scan over the same files. It must
find the password, in the record and the cache. The second applies it
under `secrets = "ssm"` with `CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1`, a
test-only switch that makes the store write each value into the record
beside its `secret_refs` (`internal/live/projection/ssmsecrets.go`). The
step 9 scan must find the password in the bucket's copy of that record.

The ssm steps run on the emulator like the rest, so they need nothing
beyond Docker and the AWS CLI. The same steps on a real account are
[secrets-in-ssm-on-real-aws](#secrets-in-ssm-on-real-aws), below. The estate's own role on a real account
needs what `examples/record-store-bucket/iam/render-policy.sh --ssm-path
<path> --ssm-kms <key-arn>` renders: `ssm:GetParameters`,
`ssm:PutParameter`, `ssm:DeleteParameter` and `ssm:GetParametersByPath`
under the path, `kms:Encrypt`, `kms:GenerateDataKey` and `kms:Decrypt` on
the key through SSM, and `kms:DescribeKey`. The scenario itself also calls
`kms:CreateKey`, `kms:TagResource`, `kms:ScheduleKeyDeletion`,
`ssm:DescribeParameters` and `ssm:GetParameter`.

### secrets-in-ssm-on-real-aws

    SMOKE_REAL_AWS=1 AWS_PROFILE=<profile> AWS_REGION=us-east-2 just smoke secrets-in-ssm-on-real-aws
    SMOKE_REAL_AWS=1 AWS_PROFILE=<profile> AWS_REGION=us-east-2 BREAK=1 just smoke secrets-in-ssm-on-real-aws

Real AWS, maintainer-run with `SMOKE_REAL_AWS=1`; without it the scenario
refuses before its first AWS call. `AWS_REGION` defaults to `us-east-2`.
It runs the ssm steps above against a real account, because the emulator
is an emulation of SSM, KMS and S3 and a claim about where a secret ends up
should also be shown where AWS keeps it (GitHub issue #2393).

The secret is an `aws_elasticache_user`'s password, not a database's. Its
`passwords` argument is sensitive and `DescribeUsers` returns only a
password count, so once the apply is done the value exists only in the
user, which cannot be asked for it, and in whatever the tool keeps: the
same property the master password has. A user costs nothing, needs no
cache cluster, and is created, modified and deleted in seconds; the
smallest database is billed by the hour and takes minutes each way.

The steps as they print:

1. `a key, a bucket and a path of this run's own`. A customer managed key
   and a record bucket (versioned, noncurrent versions expired, public
   access blocked), both tagged `choudoufu-smoke-run=<run id>`, and the
   parameter path `/choudoufu/smoke/<run id>`.
2. `an AWS managed key is refused by name, before anything is created`.
   `kms_key_id = "alias/aws/ssm"` is refused with
   `kms_key_id "alias/aws/ssm" is an AWS managed key by its alias`, and
   afterwards there is no user, no record and no parameter.
3. `the same estate applies with the run's customer managed key`. One
   user, active, with the password set, and the run never prints it.
4. `grep everything the run kept, every version in the bucket included`.
   Every version of every object under the estate's prefix is downloaded
   and grepped with the data dir, the lock file and the configuration:
   zero copies. The record holds `secret_refs`, and no cache file exists.
5. `the parameters are SecureString under the run's key, and decrypt to
   the value`. One parameter per reference, nothing else under the path.
6. `the replan reads the password back`. `No changes.`
7. `a rotation replaces the parameter and deletes the one it superseded`.
   `0 added, 1 changed`, a new parameter that decrypts to the new value,
   the old one gone, neither value in what the run kept, and the next plan
   `No changes.`
8. `teardown`. The estate destroyed and nothing left under the path.

`BREAK=1` stops after step 1 and applies the estate with
`CHOUDOUFU_TEST_SSM_LEAK_PLAINTEXT=1`. The step 4 scan over the real
bucket's record versions must find the password and print `caught`.

On every exit, pass, fail or interrupt, the EXIT trap destroys each
estate the run initialised, deletes any ElastiCache user the run named
that is still there, deletes every parameter under the run's path,
schedules the key for deletion (the 7-day minimum), and empties and
deletes the bucket. It ends on one line,
`teardown: cleaned ...; could not clean ...`, and each step that fails
prints a `COULD NOT` line with the command that finishes the job by hand.

What the credentials need, in the region the run uses:

- `sts:GetCallerIdentity`.
- KMS: `kms:CreateKey`, `kms:TagResource`, `kms:DescribeKey`,
  `kms:ScheduleKeyDeletion`, and `kms:Encrypt`, `kms:Decrypt` and
  `kms:GenerateDataKey` on the key the run creates (the key's default
  policy defers to IAM).
- SSM, on `arn:aws:ssm:<region>:<account>:parameter/choudoufu/smoke/*`:
  `ssm:PutParameter`, `ssm:GetParameter`, `ssm:GetParameters`,
  `ssm:GetParametersByPath`, `ssm:DeleteParameter` and
  `ssm:DeleteParameters`; and `ssm:DescribeParameters`, which takes no
  resource.
- S3, on `arn:aws:s3:::chdf-smoke-ssm-*` and its objects:
  `s3:CreateBucket`, `s3:PutBucketTagging`, `s3:PutBucketVersioning`,
  `s3:GetBucketVersioning`, `s3:PutLifecycleConfiguration`,
  `s3:GetLifecycleConfiguration`, `s3:PutBucketPublicAccessBlock`,
  `s3:GetBucketPublicAccessBlock`, `s3:ListBucket`,
  `s3:ListBucketVersions`, `s3:GetObject`, `s3:GetObjectVersion`,
  `s3:GetObjectTagging`, `s3:PutObject`, `s3:PutObjectTagging`,
  `s3:DeleteObject`, `s3:DeleteObjectVersion` and `s3:DeleteBucket`.
- ElastiCache, on `user:chdf-smoke-ssm-*`: `elasticache:CreateUser`,
  `elasticache:DescribeUsers`, `elasticache:ModifyUser`,
  `elasticache:DeleteUser`, `elasticache:AddTagsToResource`,
  `elasticache:ListTagsForResource` and
  `elasticache:RemoveTagsFromResource`.
- `tag:GetResources`, which choudoufu's discovery reads the estate's
  tagged resources through.

Cost: a few cents a run at most. An ElastiCache user and standard-tier
parameters are free and the S3 and API requests are a few dozen. The key
is $1 a month prorated by the hour, so its share is under one cent for the
run itself and about $0.23 if the 7-day deletion window is billed too. Time: about 5 minutes, most of it `init` downloading the
provider; the user's create, modify and delete each take seconds.

### What the scenario does not race

    go test ./internal/live/projection -run '^TestSSMSecretsRaceOneWinsNoDanglingReference$'
    go test ./internal/live/projection -run '^TestSSMSecretsCapacityRefusedBeforeAnyWrite$'

Two cases a live run cannot drive on purpose are held over a fake SSM.
`TestSSMSecretsRaceOneWinsNoDanglingReference` puts a second writer
between one write's parameter create and its commit: the bucket's
conditional write decides, the loser's parameter is an unreferenced
orphan, and no record references a parameter that does not exist.
`TestSSMSecretsCapacityRefusedBeforeAnyWrite` plans a parameter count over
the standard tier's ceiling, and the run's first write is refused before
any parameter or record is written.

## On Kubernetes

### k8s-no-secret-survives-in-what-the-tool-keeps

    just smoke k8s-no-secret-survives-in-what-the-tool-keeps
    BREAK=1 just smoke k8s-no-secret-survives-in-what-the-tool-keeps

Needs `kind` and `kubectl`. Under `strict { secrets = "refuse" }` a
`random_password` is refused by name and nothing is written or created.
A `kubernetes_secret_v1` whose `data` comes from `TF_VAR_app_password`
applies, no state cache is written, and a scan of every file the run kept
finds the value in none of them. The unchanged replan is `No changes.`,
and a rotated value is proposed. `BREAK=1` applies the same estate under
`secrets = "store"`, and the same scan must find the value in the state
cache.
