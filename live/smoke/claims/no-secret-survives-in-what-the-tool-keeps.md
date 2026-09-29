---
title: "Claim 40: No secret survives in what the tool keeps"
claim: no-secret-survives-in-what-the-tool-keeps
---

# Claim 40: No secret survives in what the tool keeps

HANDOFF's first principle is that the tool stores no secrets, and it is a
toggle: `strict { secrets = "refuse" }` in the live block. Under it, a type
that generates a secret is refused by name, and a sensitive argument the
API never returns is left out of its record. The state cache is not
written. This claim applies an estate under that setting and then greps
every file the run kept for the plaintext, by value. It finds none.

The default, `secrets = "store"`, is compatible with stock, and stock keeps
the same password in its state file. So the default keeps it in the record
and in the cache, and the scenario shows that too. Claims 3 and 5 prove the
run works with the cache deleted.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke no-secret-survives-in-what-the-tool-keeps

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke no-secret-survives-in-what-the-tool-keeps and report
the "caught" line: it applies the same estate under secrets = "store",
and the same scan must then find the password.
```

The steps as they print:

1. `a secret-generating type is refused by name`. An estate with a
   `random_password` and an `aws_iam_access_key` under `refuse`. The plan
   exits non-zero. Each resource is refused in its own words: the
   password as a `SECRET_REFUSED` logical resource, the access key as a
   type whose secret AWS never returns again. Both name the setting. No
   record store, cache or IAM user is left behind.
2. `an estate with a settable secret applies under refuse`. One
   `aws_db_instance` whose `password` comes from `TF_VAR_db_password` and
   is written nowhere on disk. One plain apply.
3. `grep everything the run kept for the plaintext`. Every file under the
   working directory except the downloaded provider plugins: the record,
   the data dir, the lock file, the configuration. Zero hits. The record
   exists and holds the database's identifier and not the word
   `password`. No cache file exists.
4. `what you can still ask to be written`. A saved plan and a debug log
   are files an operator names with a flag, so they are read on their own.
   On a fresh estate under the same setting, `plan -out` and then
   `apply <planfile>` under `TF_LOG=debug`. The plan file is a zip, so
   the scan reads its members, and the `tfplan` member holds the value,
   because a saved plan carries what its apply will send. The debug log
   holds it in one entry: the aws provider's `HTTP Request Sent` for
   `CreateDBInstance`, with `MasterUserPassword` in the body. Every log
   entry that holds it must be the provider plugin's. Stock writes both
   the same way. The record store and data dir from that run still hold
   nothing.
5. `the replan under refuse proposes the argument again`. This is what
   the refusal costs. Nothing the run keeps holds the last-applied
   password, so the plan has no prior to compare against and proposes
   sending the value again: `Plan: 0 to add, 1 to change, 0 to destroy`,
   the one change being `password` on `aws_db_instance.app`. The step
   runs it twice, unchanged and then with `TF_VAR_db_password` rotated,
   because the rotation is the case that matters - a rotated password
   that planned `No changes.` would never be sent. Neither plan prints a
   password or leaves one on disk.
6. `the default, honestly`. The same estate with no strict block. The
   scan finds the password in the record and in
   `.terraform/choudoufu-cache.tfstate`. With the cache deleted, the
   plan is still `No changes.`
7. `teardown`.

Under `BREAK=1` the estate is applied with `secrets = "store"` and the
step 3 scan runs over the same set of files. It must find the password,
and it finds it in two files, the record and the cache. If it found
nothing, the zero under `refuse` would prove nothing, and the run fails.

## What this does not claim

The debug log and the saved plan hold the value. They are the operator's
files, but a reader who turns on `TF_LOG=debug` in CI should know the
provider logs request bodies.

The perpetual diff in step 5 is a cost, not a defect, and the claim does
not say otherwise: under `refuse` this argument is proposed on every
plan whether or not it changed, and each apply sends it again. That is
the same diff stock `terraform import` leaves for an argument no read
returns. The default trades it for keeping the value in the record.

Until #1503 was fixed the projection seeded the prior from the
configuration's own value, so the replan read `No changes.` and a
rotated password was silently never sent. Step 5 printed that replan
without asserting its shape; it now asserts both arms.
