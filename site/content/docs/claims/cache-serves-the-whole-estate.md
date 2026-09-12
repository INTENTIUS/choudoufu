---
title: "Claim 10: The cache serves the whole estate"
weight: 10
claim: cache-serves-the-whole-estate
---

# Claim 10: The cache serves the whole estate

`-refresh=false` is the path that serves from the state cache instead of
reading live. Until now it served the schema-admitted and record-backed
slice; a converged estate's server-assigned resources - VPCs, subnets,
security groups, every id the cloud hands out - were read live anyway.
Now they are served too, so one estate of a decomposed terralith plans
at the speed of reading a file rather than re-reading the cloud. A
default plan still refreshes, because the read is drift detection, and
the serving is vouched by the estate sweep, so a deleted resource is
caught rather than served from cache.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke cache-serves-the-whole-estate

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke cache-serves-the-whole-estate and report the "caught"
line: it deletes a resource out of band, and the plan must surface it
rather than serve the gone object from cache.
```

The steps as they print:

1. `stand the estate up` - four server-assigned resources with a warm
   cache.
2. `a default plan reads them all` - zero served; a default plan
   refreshes for drift by ruling.
3. `-refresh=false serves every instance from cache` - all four served,
   the estate planned without re-reading a resource.
4. `serving is existence-vouched` - the estate sweep confirms each is
   still live before serving, so a deletion is caught.

The `BREAK=1` run deletes a resource out of band. The sweep no longer
vouches it, so it is not served from cache and the plan surfaces it -
losing an object costs a read, never a wrong plan.
