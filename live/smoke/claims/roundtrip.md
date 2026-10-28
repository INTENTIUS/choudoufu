---
title: "Claim 6: Stock in, stock out, whenever you need it"
claim: roundtrip
---

# Claim 6: Stock in, stock out, whenever you need it

`live-import` brings a stock estate in from the state file you already
have, and the local cache hands it back out as a stock-format state file.
With no live block, choudoufu plans exactly like stock.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### roundtrip

    just smoke roundtrip
    BREAK=1 just smoke roundtrip

Pinned stock OpenTofu applies a seven-resource estate with no tags.
`live-import` verifies every resource against the live system and marks
what verifies. The choudoufu plan is clean, the state file is deleted, and
an ordinary apply keeps the estate converged. The cache is then copied to
`terraform.tfstate` with the live block removed; stock's first plan
proposes only removing the two marker tags, and stock destroys all seven
resources from the handed-back file. `BREAK=1` skips `live-import`, and
the plan must propose a duplicate estate beside the real one.

### stock-when-you-need-it

    just smoke stock-when-you-need-it
    BREAK=1 just smoke stock-when-you-need-it

With the live block removed, choudoufu and the pinned stock oracle plan
the same state-backed estate with `TF_LOG=debug`. The filtered plan texts
are equal and the request counts are identical. `BREAK=1` plans the
choudoufu leg with the live block on, and the measurement must show the
difference.

### terragucci row: smoke, choudoufu/migrate

terragucci's smoke claim `choudoufu/migrate` (scenario `smoke`) brings a
root on s3 state into an estate from a pipeline: the migration is proved
and approved by its digest, the resources are stamped, the old state stays
the version it was, and the next plan of every root shows no change.

A terragucci row counts toward the claim when it ran choudoufu 0.31.0,
the newest release in `live/history`, and `live/CLAIMS.md` gives each
row's state. The rows stand beside the scenarios above, whose `BREAK=1`
arms rebuild choudoufu from source, which a pipeline running a release
cannot do.

## On Kubernetes

### k8s-roundtrip

    just smoke k8s-roundtrip
    BREAK=1 just smoke k8s-roundtrip

Needs `kind`, `kubectl` and `terraform`. Stock Terraform stands four
objects up with a plain state file; one `live-import` labels them and
leaves the state file byte for byte; the state file is deleted and the
estate plans from labels. On the way out the cache is copied to
`terraform.tfstate`, its provider address rewritten once with `terraform
state replace-provider`, and stock's first plan removes only the label
and the annotation. Stock then converges and destroys all four. `BREAK=1`
skips the `live-import`, and the live plan must refuse the unlabelled
objects by name.

### k8s-stock-when-you-need-it

    just smoke k8s-stock-when-you-need-it
    BREAK=1 just smoke k8s-stock-when-you-need-it

Needs `kind`, `kubectl` and stock OpenTofu at `live/oracle-versions.json`'s
`tofu_version` (`tofu` on PATH, or `ORACLE_TOFU=<binary>`). With no live
block, choudoufu and stock OpenTofu plan the same state-backed estate
through `live/smoke/k8sproxy.py`, which logs one line per request the
client sent. The plan texts match once each tool's name is taken out, and
the request counts are equal. `BREAK=1` turns the live block on for
choudoufu's leg, and the measurement must move.
