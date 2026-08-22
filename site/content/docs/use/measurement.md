---
title: "How the compatibility numbers are measured"
weight: 9
---

# How the compatibility numbers are measured

[`live/corpus-refusals.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/corpus-refusals.json)
measures which refusals fire and how often across the corpus.
[Compatibility reference]({{< relref "/docs/use/compatibility" >}}) copies no
count from it, because a copied count goes stale the moment the corpus
re-runs.

That measured ranking is why the static-evaluability rule (see
[Identity]({{< relref "/docs/model/identity" >}})) leads the reference page.
Several of the most frequent refusals are that one rule under different
diagnostics.

**Do not read the fixture or module-example populations as a compatibility
rate.** Module `examples/` directories demonstrate a module's full surface, so
they lean far harder on variables, conditionals and `dynamic` blocks than a
configuration describing one deployment, and refuse almost across the board.
Those populations are marked as a ranking, settled by
[#118](https://github.com/INTENTIUS/choudoufu/issues/118). One population can
honestly carry a rate since
[#147](https://github.com/INTENTIUS/choudoufu/issues/147), whole deployment
root modules published by their operators, pinned by commit, marked
`reads_as: rate`.

Run `choudoufu live-check` on your own configuration rather than inferring
anything from the corpus.
