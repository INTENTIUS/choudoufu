---
title: "Claim 3: Staleness costs reads, never results"
weight: 3
claim: staleness-costs-reads
---

# Claim 3: Staleness costs reads, never results

A stale state file is the classic failure: the file is the record, so
its lies become your plans. Here the file is a cache, never consulted for
ownership; live reads win every disagreement.
Losing or corrupting it costs a slower run and nothing else.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke staleness-costs-reads

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke staleness-costs-reads and report the "caught" line:
it moves the live world mid-comparison and the equality check must
notice.
```

In print order:

1. `manufacture a genuinely ancient cache` - apply, save the cache
   aside, destroy the whole estate, apply again. The saved cache now
   remembers only dead ids; the run proves the old and new VPC ids
   differ.
2. `three cache states, one answer` - the same plan runs against the
   fresh cache, then the ancient one, then no cache file at all. The
   outputs are byte-identical.
3. `the world moves and the fresh cache does not hide it` - a setting
   is changed behind the tool's back with the AWS CLI; the next plan
   shows the drift straight through a fresh cache, and the apply
   reconverges it.
4. `the one opt-in, and where the cost actually lives` -
   `-refresh=false` is the single path that serves reads from cache,
   and only for instances the sweep has already verified. The run
   measures its cache hits, then reruns with the cache gone to show
   none. The two outputs prove equal and both request counts print side
   by side. The price of staleness is paid in work, never in answers.
5. `the same answer where values live in the record store` - the same
   ancient-cache trick against the record store, plus a phantom: the
   cache remembers a resource that no longer exists anywhere. The plan
   neither destroys the phantom nor misses the survivor.
6. `teardown`.
