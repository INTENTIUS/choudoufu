---
title: "Claim 4: Declaring the backend is the whole setup"
weight: 4
claim: backend-sets-itself-up
---

# Claim 4: Declaring the backend is the whole setup

Stock remote state has a day one: create a bucket, enable versioning,
create a lock table, write IAM for both, run `init`, answer migration
prompts, keep it all in step forever. Here the backend's stores
provision themselves at first use, and each proves its own read path
with a sentinel before any plan trusts it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke backend-sets-itself-up

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke backend-sets-itself-up and report the "caught" line:
it makes the store unreachable and the run must refuse by name rather
than plan an empty-looking estate.
```

As the run prints them:

1. `no store declared - the local one appears unbidden` - a live block
   with nothing about storage gets a `.tofu-records` directory beside
   the module at first use, sentinel already written. Zero setup steps.
2. `it works: the effect survives between runs` - the recorded
   resource survives a replan, so the store is real, not scaffolding.
3. `a cloud store is one declaration, and it provisions itself` -
   `record_store "ssm" {}` is the entire cloud setup; the store writes
   its sentinel into Parameter Store and the AWS CLI reads it back.
4. `teardown` - nothing to deprovision, because nothing was ever
   provisioned by hand.

The `BREAK=1` run makes only the SSM store unreachable while the
provider stays healthy. A store that cannot answer must refuse loudly,
naming itself, because a store that answers with silence would read as
an empty estate and the next plan would propose rebuilding everything.
