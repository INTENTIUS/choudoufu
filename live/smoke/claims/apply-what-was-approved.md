---
title: "Claim 15: Apply exactly what was approved"
claim: apply-what-was-approved
---

# Claim 15: Apply exactly what was approved

The artifact that crosses a CI approval gate is the plan file. The apply
never replays it: it reads the live system, plans again and compares,
applying only when the two match and refusing by name (exit 3) when they
do not. And the platform has the last word after the plan: a write it
refuses is reported in its own words with nothing changed, and the same
approved file applies once the refusal lifts.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### apply-what-was-approved

    just smoke apply-what-was-approved
    BREAK=1 just smoke apply-what-was-approved
    BREAK_UNMOVED=1 just smoke apply-what-was-approved

A plan saved with `-out` applies unchanged; an out-of-band change between
plan and apply makes the same file refuse by name, and so does the same
change writing a different value, naming `after.retention_in_days`.
Values compare canonically, unknown values are skipped, sensitive ones
compared by digest.

Steps 8-14 were two-saved-plans-one-attribute (claim 2's, #1504). Two
checkouts of one estate save plans changing one SQS queue's
`visibility_timeout_seconds` from 30, one to 60 and one to 90; the
scenario first proves the provider reads that attribute back from the
cloud. The first plan applies. The second re-reads the queue, finds 60
where its approval said 30, and is refused with exit 3 naming
`before.visibility_timeout_seconds` (#878); the queue keeps 60, and the
second checkout's recovery is a re-plan. The two applies run one after
the other: two that both re-read before either writes are not covered.

`BREAK=1` rebuilds choudoufu with the before-values comparison removed
(`go build -overlay`, so it needs Go), skips steps 1-7, and the second
saved plan must be caught reporting success with the queue on 90.
`BREAK_UNMOVED=1` is the inverse control: no out-of-band change, and the
file must apply.

### the-server-gets-the-last-word (claim 26 until #1817)

    just smoke the-server-gets-the-last-word
    BREAK=1 just smoke the-server-gets-the-last-word

Restated for AWS (the part the emulator can produce): a plan saved under
a permissive role, then an explicit Deny on `sqs:SetQueueAttributes`
before the apply. The apply fails quoting AWS's own message, the queue
unchanged and still marked, and the same file applies once the Deny is
lifted. A tag policy rewriting the marker is not measured
([lex00/floci#217](https://github.com/lex00/floci/issues/217)). `BREAK=1`
denies an action the apply never calls, which must not stop it.

## On Kubernetes

### k8s-the-server-gets-the-last-word (claim 26 until #1817)

    just smoke k8s-the-server-gets-the-last-word
    BREAK=1 just smoke k8s-the-server-gets-the-last-word

A fail-closed webhook refuses the approved plan in the API server's own
words, with nothing changed, and the same file lands once it is removed.
A mutating policy that rewrites a declared field reads as the same
perpetual drift stock reads, marker intact. A policy that strips
`tofu-estate` on the way in is named by the run that made it: the create
warns, the adopting update fails rather than reporting a change nothing
kept (#1192). `BREAK=1` points the stripping policy at a decoy label, and
the marker must land with no warning.

## On Azure

Open. The intended proof is the gauntlet's `plan_approval` stage on `reference-azure`, which is built and has not been run. floci-az evaluates no Policy, so the server's own refusal at apply time cannot be shown there.
