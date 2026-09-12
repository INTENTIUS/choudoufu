---
title: "Claim 2: Contention settles at the platform API, never in a lock"
weight: 2
claim: no-self-managed-locks
---

# Claim 2: Contention settles at the platform API, never in a lock

Stock backends take a lock before touching state, because two writers
corrupting one file is fatal when the file is the record. A stuck lock
then needs `force-unlock`. With no authoritative file to defend there is no lock at all; two
racing applies are refereed by the platform's own uniqueness rules.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke no-self-managed-locks

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke no-self-managed-locks and report the "caught" line:
it strips the race winner's identity marker and convergence must fail.
```

Step by step:

1. `there is no lock to force open, and the tool says so` -
   `force-unlock` refuses with the true reason instead of pretending a
   lock exists.
2. `the race` - two applies of the same client-named IAM role start at
   the same moment. The cloud's name-uniqueness constraint referees;
   the phrase "Acquiring state lock" appears in neither output.
3. `the loser converges by reading reality` - the losing apply's next
   plan is `No changes.` Its whole recovery is one ordinary plan.
4. `the one race the API cannot referee is a named collision` -
   server-assigned resources can genuinely duplicate; the duplicate
   surfaces as a named pair rather than hiding.
5. `the human resolves it` - one delete, and the estate is clean again.
6. `teardown`.
