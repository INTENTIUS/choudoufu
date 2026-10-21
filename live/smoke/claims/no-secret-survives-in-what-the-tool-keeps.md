---
title: "Claim 40: No secret survives unless you let it"
claim: no-secret-survives-in-what-the-tool-keeps
---

# Claim 40: No secret survives unless you let it

Under `strict { secrets = "refuse" }` nothing the tool keeps holds a
secret. A type that generates one is refused by name, and a sensitive
argument is left out of its record.

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
7. `teardown`.

`BREAK=1` applies the estate with `secrets = "store"` and runs the step 3
scan over the same files. It must find the password, in the record and
the cache.

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
