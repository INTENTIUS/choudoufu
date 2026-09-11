---
title: "Claim 5: Recovery is a re-run, never surgery"
weight: 5
claim: recovery-is-a-rerun
---

# Claim 5: Recovery is a re-run, never surgery

Two disasters end an estate's day under stock. An apply that crashes
after a create call leaves a resource no state file knows about;
re-applying creates a duplicate and the original leaks. A lost state
file is worse, because the file was the record of everything you own.
Both end the same way here: run it again.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke recovery-is-a-rerun

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke recovery-is-a-rerun and report the "caught" line: it
withholds the crashed resource's markers and the bind check must fail
rather than claim an unmarked resource.
```

The steps as they print:

1. `the crash - an apply dies after its first create` - the VPC is made
   with the AWS CLI and stamped with the estate's markers, exactly what
   the dead apply would have written before crashing. The configuration
   still declares it.
2. `re-run the apply - it binds, completes, duplicates nothing` - the
   whole recovery is the same apply again: it finds `aws_vpc.main`
   already owned and builds the rest around it; the vpc keeps its id
   and the follow-up plan is clean.
3. `now lose every local file` - the cache and the whole `.terraform`
   directory are deleted; after an init, the next plan is `No changes.`
   The narration also says what the deleted cache held, and why that
   disposable file is the one place allowed to hold it.
4. `teardown` - the crashed vpc is destroyed with the rest of the
   estate. It was a full citizen from the moment it was bound.

The `BREAK=1` run withholds the markers. The estate must refuse to bind
an unmarked resource, so the re-run builds a second vpc - stock's crash
behavior, demonstrated as the exact boundary of the claim.
