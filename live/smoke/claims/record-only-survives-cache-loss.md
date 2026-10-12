---
title: "Claim 17, retired into claim 3"
retired: 17
---

# Claim 17 is part of claim 3

Since #1817 claim 17 was one of the proofs of claim 3: see [claim 3
on AWS](staleness-costs-reads.md#on-aws). Its scenario was dropped. Claim 5's `a-killed-apply-hides-nothing` uses the same record-only `aws_iam_group_policy`, deletes the cache and `.terraform` in its step 7 and plans `No changes.`, and its `BREAK_EARLY_RECORD=1` arm shows the no-record outcome: `just smoke a-killed-apply-hides-nothing`.
