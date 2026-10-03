---
title: "Claim 3: The cache never changes an answer"
claim: staleness-costs-reads
---

# Claim 3: The cache never changes an answer

A stale state file is the classic failure: the file is the record, so its
lies become your plans. Here the state file is a cache, never consulted
for ownership, and live reads win every disagreement. A fresh cache, an
ancient one and none at all give the same plan; what differs is how much
reading the run does. Where a record is the only copy of an identity,
losing the cache costs nothing and losing the record is named, never
guessed.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### staleness-costs-reads

    just smoke staleness-costs-reads
    BREAK=1 just smoke staleness-costs-reads

The run manufactures a genuinely ancient cache (apply, save it, destroy,
apply again, so it remembers only dead ids), then plans against the fresh
cache, the ancient one and no cache: the outputs are byte-identical. A
setting changed out of band shows through a fresh cache as drift.
`-refresh=false` is the one path that serves reads from cache, only for
instances the sweep has verified, and the run prints its request count
beside the uncached one with equal outputs. The same holds for values in
the record store, including a phantom the cache remembers and nothing else
does. `BREAK=1` moves the live world mid-comparison, and the equality
check must notice.

### record-only-survives-cache-loss (claim 17 until #1817)

    just smoke record-only-survives-cache-loss
    BREAK=1 just smoke record-only-survives-cache-loss

`aws_iam_group_policy` with its `name` left to the provider has no tag and
no listing, so the record this apply writes is the only copy of its
identity. Deleting the cache and the whole `.terraform` directory still
replans `No changes.`, with the identity read from the record. `BREAK=1`
deletes the record too, and the plan must name a duplicate create for that
instance rather than bind silently or report no changes. [Recover an
estate](https://intentius.io/choudoufu/docs/use/recover-an-estate/) says
what to do from there.

### the-estate-answers-in-the-present-tense (claim 41 until #1817)

    just smoke the-estate-answers-in-the-present-tense
    BREAK=1 just smoke the-estate-answers-in-the-present-tense

One question, "which of this estate's security groups are attached to
nothing", asked two ways after an instance is moved from one group to
another with the AWS CLI. The live answer (the tagging API and a describe)
is the new one; the cache answers as of the last apply. The next plan
reads the present and proposes the one update that moves it back. This is
the estate's own resources, not account-wide gap analysis. `BREAK=1`
skips the out-of-band move, and the two answers must then agree.
