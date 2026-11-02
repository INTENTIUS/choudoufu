---
title: "Claim 33, retired into claim 2"
retired: 33
---

# Claim 33 is part of claim 2

Since #1817 claim 33 is one of the proofs of claim 2, "Nothing is held":
see [claim 2 on AWS](no-self-managed-locks.md#on-aws). Its scenario
was removed in #2288: the cell cites `TestS3StoreCASUnderEverySSEFlavour`,
run on real AWS with `SMOKE_REAL_AWS=1 just test-sse`.
