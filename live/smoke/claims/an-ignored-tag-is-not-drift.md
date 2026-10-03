---
title: "Claim 45: Drift you ignore isn't drift"
claim: an-ignored-tag-is-not-drift
---

# Claim 45: Drift you ignore isn't drift

A key another system owns, added out of band, does not churn the plan
once the estate says it is not its business, and an edit to a key the
estate does declare still plans like any other change.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### an-ignored-tag-is-not-drift

    just smoke an-ignored-tag-is-not-drift
    BREAK=1 just smoke an-ignored-tag-is-not-drift

A tag another account's automation adds out of band plans nothing once
the provider's stock `ignore_tags` names it; a declared tag edited out of
band still plans. `BREAK=1` removes the `ignore_tags` line, and the same
write must plan an update naming the tag.

## On Kubernetes

### k8s-a-label-is-a-change (claim 27 until #1817)

    just smoke k8s-a-label-is-a-change
    BREAK=1 just smoke k8s-a-label-is-a-change

Keys the configuration never declared (the API server's
`kubernetes.io/metadata.name`, a controller's annotation) stay the
server's and churn nothing, because `kubernetes_manifest` reads the prior
manifest. A label or annotation edited, added or deleted in configuration
plans the one in-place update stock plans (#1177, #1211), and a second
working directory removes the same label when the records are shared, in
the cluster or in a bucket; the bucket step also needs the floci
emulator. `BREAK=1` writes an undeclared key, which must plan `No
changes.`, and runs the shared-store steps on the local store, where the
second directory must see nothing.
