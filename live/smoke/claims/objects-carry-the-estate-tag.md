---
title: "Claim 36, retired into claim 28"
retired: 36
---

# Claim 36 is part of claim 28

Since #1817 claim 36 was one of the proofs of claim 28: see [claim 28
on AWS](a-name-prefix-shares-no-keys.md#on-aws). Its scenario is now part of `one-bucket-many-estates` (`SMOKE_REAL_AWS=1 just smoke one-bucket-many-estates`). Its control, once that scenario's `BREAK_UNTAGGED=1` arm, is now `TestS3StoreTagsEveryObjectItWrites` in pkg/staterecord, which runs in CI (#2288).
