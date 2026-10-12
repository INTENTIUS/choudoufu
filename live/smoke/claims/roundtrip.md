---
title: "Claim 6: Stock in, stock out, whenever you need it"
claim: roundtrip
---

# Claim 6: Stock in, stock out, whenever you need it

Migrating to a new state tool is usually a trapdoor: once your estate
is in, the only way back is another migration project. Here the door in
is `live-import`, which reads the state file you already have and
stamps ownership markers on what verifies, and the door out is the
local cache, which is a stock-format state file ready to hand back.

Stock behavior is not a mode you leave behind either. It was claim 8,
"Stock when you need it", until it merged here: with no live block,
choudoufu plans like stock, with the same plan text and the same request
count, so stock is one deleted live block away.

## On AWS

### roundtrip

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke roundtrip

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke roundtrip and report the "caught" line: it skips
live-import, and the plan must propose a duplicate estate - the
documented quiet failure of every migration.
```

The steps as they print:

1. `stock stands the estate up, tagless` - pinned stock OpenTofu, in
   its own container, applies a seven-resource estate with no tags
   anywhere. Identity exists only in `terraform.tfstate`.
2. `the door in - one command` - `live-import` reads that file once
   and verifies every resource against the live system; what verifies
   gets the two markers. Nothing else is touched.
3. `bound - and the old record is now optional` - the choudoufu plan is
   clean, the state file is deleted, and an ordinary apply keeps the
   estate converged while refreshing the cache.
4. `the door out - one file` - the cache is copied to
   `terraform.tfstate` and the live block removed. Stock's first plan
   back proposes only the removal of the two marker tags; one apply
   later, nothing of the fork remains.
5. `teardown - by stock, from the handed-back file` - stock destroys
   all seven resources using the file choudoufu handed back.

The `BREAK=1` run skips the one command. The plan must then propose
building a duplicate estate beside the real one, because turning on the
live block never binds resources by itself - the markers do.

### stock-when-you-need-it (claim 8 until it merged here)

Stock behavior is not a mode you leave behind - it is the fallback,
whole and exact, one deleted live block away. The scenario measures
that rather than promising it: choudoufu and the pinned stock oracle
plan the same state-backed estate side by side with debug logging on,
and the plan texts match and so do the request counts, exactly. And
with the live backend on, what you pay scales with your estate rather
than the account around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke stock-when-you-need-it

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke stock-when-you-need-it and report the
"caught" line: it runs the choudoufu leg with the live block ON, and
the measurement must show the difference.
```

The steps as they print:

1. `a stock estate, stood up by choudoufu with no live block` - the
   fixture's live block is removed and choudoufu applies the ordinary
   way: a real `terraform.tfstate`, no markers, no hooks.
2. `same plan, same requests` - choudoufu and the pinned oracle each
   plan the estate with `TF_LOG=debug`; the scenario asserts the
   filtered plan texts are equal and the request counts identical.
   This is the #588 parity measurement as a two-minute demo.
3. `the live backend on - and you pay for your estate, not your
   account` - the
   live estate goes up and its plan's request count is measured. Twenty
   foreign resources then appear in the account and the count is
   measured again; it must not move.
4. `teardown` - estate and clutter both removed.

The `BREAK=1` run plans the choudoufu leg with the live block on. The
asked-for machinery must show up in the measurement - a live plan that
measured identical to stock would mean the parity comparison compares
nothing.

## On Kubernetes

### k8s-roundtrip

    just smoke k8s-roundtrip
    BREAK=1 just smoke k8s-roundtrip

Needs `kind`, `kubectl` and `terraform`. Stock Terraform stands four
objects up with a plain state file; one `live-import` labels them and
leaves the state file byte for byte; the state file is deleted and the
estate plans from labels. On the way out the cache is copied to
`terraform.tfstate`, its provider address rewritten once with `terraform
state replace-provider` (Terraform resolves `hashicorp/kubernetes`
against its own registry), and stock's first plan may only remove the
label and the annotation. Stock then converges and destroys all four.
`BREAK=1` skips the `live-import`, and the live plan must refuse the
unlabelled objects by name.

### k8s-stock-when-you-need-it (claim 8 until it merged here)

    just smoke k8s-stock-when-you-need-it
    BREAK=1 just smoke k8s-stock-when-you-need-it

Needs `kind`, `kubectl` and stock OpenTofu at `live/oracle-versions.json`'s
`tofu_version` (`tofu` on PATH, or `ORACLE_TOFU=<binary>`; any other
version is refused). With no live block, choudoufu and stock OpenTofu
plan the same state-backed estate through `live/smoke/k8sproxy.py`, which
logs one line per request the client sent. The plan texts match once each
tool's name is taken out, and the request counts are equal (8 each on the
first run). `BREAK=1` turns the live block on for choudoufu's leg, and the
measurement must move. Foreign objects and a plan's cost are claim 14's.
