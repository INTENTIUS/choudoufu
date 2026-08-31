---
title: "Rulings"
weight: 40
bookCollapseSection: true
---

# Rulings

A ruling records a decision the maintainer made: what was decided, the
evidence it rests on, and what it replaces. There is no review step and
nothing here is asking anyone's permission. Rulings live here rather than
in an issue comment when a later reader would otherwise have to re-derive
the reasoning, or when the decision changes something a document already
states.

Every figure in a ruling names a source the next reader can re-derive it
from - a commit, a committed artifact, a test - because several figures in
this repository have been wrong on first statement. A ruling that quotes a
number without one is quoting a rumour.

Each ends with what it did **not** verify. A decision record without that
section reads as more settled than it is.

- [What drives development]({{< relref "/docs/decisions/what-drives-development" >}}) -
  fast fixtures drive daily work, estates become a release cadence, and
  real AWS answers the three questions an emulator cannot.
