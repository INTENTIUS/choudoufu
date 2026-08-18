# Governance

choudoufu is a fork of OpenTofu maintained by INTENTIUS. It is experimental,
and its governance is deliberately small.

## What is decided here

Everything in the live-marker path. The admission table, the marker spec, the
refusals, the record store, receipts, the harness, and the docs site.

Decisions are made by this repository's maintainers, listed in
[`MAINTAINERS.md`](MAINTAINERS.md), in the open on the issue tracker. A change
that alters what the project claims needs the evidence for the claim in the
same change. [`HANDOFF.md`](HANDOFF.md) is the standing playbook and says what
makes a change acceptable.

An issue labelled `awaiting-ruling` carries a complete decision package and is
blocked on a maintainer, not on more work.

## What is not decided here

Everything outside live markers is stock OpenTofu, and this project does not
govern it. Language, CLI, providers, backends and the plugin protocol follow
upstream. Their governance is
[opentofu/org](https://github.com/opentofu/org/blob/main/GOVERNANCE.md), under
the charter at
[opentofu/org CHARTER.md](https://github.com/opentofu/org/blob/main/CHARTER.md).

A change belonging upstream should go upstream. This fork is not a route
around OpenTofu's process, and being merged here means nothing about being
accepted there.

## Trademark and affiliation

Not affiliated with or endorsed by OpenTofu or the Linux Foundation. OpenTofu
is a registered trademark of the Linux Foundation. Licensed MPL-2.0, the same
as OpenTofu.
