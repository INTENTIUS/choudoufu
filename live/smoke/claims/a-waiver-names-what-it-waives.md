---
title: "Claim 30: A bucket waiver waives only the assertion it names, and says so on every run"
claim: a-waiver-names-what-it-waives
---

# Claim 30: A bucket waiver waives only the assertion it names, and says so on every run

The record store bucket's three assertions (claim 29) can be waived, for
a bucket an operator has reason to run differently or a role that cannot
read the bucket's configuration. The waiver is
`allow_insecure = ["versioning"]`: a list of names. Waiving one
assertion leaves the other two in force, and the configuration records
which risk was taken.

Every run under a waiver says so, with what the waiver costs, for as
long as it is configured. A waiver that goes quiet after the first apply
looks exactly like a bucket that passes.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI is installed, and Go is installed. From the
repo root run:

  just smoke a-waiver-names-what-it-waives

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke a-waiver-names-what-it-waives and report the "caught"
line: it rebuilds choudoufu so the warning appears on an estate's first
run only, and run two must be caught proceeding in silence.
```

As the run prints them:

1. `a bucket with no versioning, and a waiver that names versioning` -
   the apply proceeds. It warns that the versioning assertion is waived
   and that an overwritten or deleted record cannot be brought back, and,
   because an apply reads the bucket, that the waived assertion would
   have refused this apply.
2. `loud on every run` - a plan and a second apply of the same estate,
   with nothing changed. Each carries the warning. The plan's comes from
   the configuration alone and costs no request.
3. `the other two assertions are still in force` - the lifecycle and
   then the public-access block are broken in turn. Each apply is
   refused by that setting's name, and versioning is never among the
   refusals.
4. `a typo is refused, not ignored` - `["versionning"]` fails at
   configuration load, naming the word and listing the three valid
   names. Accepted silently it would waive nothing and still read as a
   waiver to whoever reviews the configuration.
5. `teardown`.

The same name waives a setting that is wrong and a setting the role
could not read. The refusal messages differ, "fails its versioning
assertion" against "versioning setting could not be read", because the
fix differs: one is a bucket change and the other is an IAM grant.

The `BREAK=1` binary warns on run one, so a check that ran the estate
once would pass it. Step 2 is the step that catches it.
