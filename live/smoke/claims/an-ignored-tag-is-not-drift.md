---
title: "Claim 45: Drift you ignore isn't drift"
claim: an-ignored-tag-is-not-drift
---

# Claim 45: Drift you ignore isn't drift

A key another system owns, added out of band, plans nothing once the
estate says it is that system's, and an edit to a key the estate declares
plans like any other change.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### an-ignored-tag-is-not-drift

    just smoke an-ignored-tag-is-not-drift
    BREAK=1 just smoke an-ignored-tag-is-not-drift

A tag another account's automation adds out of band plans nothing once
the provider's stock `ignore_tags` names it, and a declared tag edited out
of band plans. `BREAK=1` removes the `ignore_tags` line, and the same
write must plan an update naming the tag.

### terragucci rows: smoke drift/choudoufu and smoke-choudoufu drift/tofu

terragucci's drift claims hold the declared half of this claim in a
pipeline. `drift/choudoufu` (scenario `smoke`) changes a queue outside
choudoufu on a root under live resource markers: tf-drift plans the root
in full, its report and one drift issue name the root and the attribute,
and a run with no change closes the issue. `drift/tofu` under scenario
`smoke-choudoufu` is the core drift claim run on choudoufu.

A terragucci row counts toward the claim when it ran choudoufu 0.31.0,
the newest release in `live/history`, and `live/CLAIMS.md` gives each
row's state. The rows stand beside the scenarios above, whose `BREAK=1`
arms rebuild choudoufu from source, which a pipeline running a release
cannot do.

## On Kubernetes

### k8s-a-label-is-a-change

    just smoke k8s-a-label-is-a-change
    BREAK=1 just smoke k8s-a-label-is-a-change

Needs the floci emulator for its bucket step. Keys the configuration
never declared (the API server's `kubernetes.io/metadata.name`, a
controller's annotation) stay the server's and plan nothing. A label or
annotation edited, added or deleted in configuration plans the one
in-place update stock plans (#1177, #1211), and a second working
directory removes the same label when the records are shared, in the
cluster or in a bucket. `BREAK=1` writes an undeclared key, which must
plan `No changes.`, and runs the shared-store steps on the local store,
where the second directory must see nothing.
