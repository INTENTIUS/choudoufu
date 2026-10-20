# Proof

Four scenarios prove the GCP column, each the GCP proof of a promise
first proven on AWS: the marker itself (claim 7), the sweep that finds a
deleted block's resource by it (claim 1), the project fence (claim 13,
restated as project-scoped), and the record store in a bucket (claim 29).
A claim is a promise and proof is per provider
([#1112](https://github.com/INTENTIUS/choudoufu/issues/1112)), so every
other GCP cell is stated in the claims data, `live/smoke/claims.json`,
rather than left implicit, and an open one is a missing proof.

## The harness

```
just smoke gcp-greenfield
```

The scenarios run against floci-gcp, the lex00/floci-gcp fork's image,
pinned by digest in `live/floci-gcp-image`. The shape is the AWS one: a
verdict line per step, exit 0 only when every claim held, and `BREAK=1`
to manufacture the fault. Only the maintainer runs them.

| Scenario | Claim | `BREAK=1` |
|---|---|---|
| `gcp-greenfield` | 7, identity is a tag | strips the label; the replan must restore it |
| `gcp-no-silent-orphans` | 1, no silent orphans | strips the orphan's label; the replan must leave it |
| `gcp-the-label-is-the-boundary` | 13, the tag is the boundary | binds the denied principal; its write must go through |
| `gcp-records-in-a-bucket` | 29, the store refuses what it can't trust | removes the bucket's fences |

## Emulator gaps

Each is filed on the fork and waited for, never routed around:
Resource Manager tags (lex00/floci-gcp#4), Compute verify (#5), the labels
round trip (#6), Cloud Asset search (#7) and `matchTag` (#8). Until #7
lands the sweep's equivalence with real GCP is unmeasured.

## The gauntlet lane

None yet. A hand-written reference estate comes first, then published
roots, as the kubernetes lane did.
