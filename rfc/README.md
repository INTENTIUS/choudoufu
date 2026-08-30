# Design records

This directory holds two kinds of document, and the difference matters when
you read one.

**Inherited OpenTofu RFCs.** Everything dated 2023 to 2025 came with the fork.
They are upstream OpenTofu's design documents, kept because they are useful
context for code this fork still runs. They describe OpenTofu's process and
OpenTofu's decisions, not this fork's, and they are historical records rather
than live instructions. Do not follow their process, and do not edit them to
match this fork.

**This fork's own design records**, dated 2026 onward. Each one settles a
question for choudoufu: a ruling, a measurement that a decision rests on, or
both.

## How this fork records a decision

choudoufu does not file RFCs with any other project. These documents are
internal records. There is no external review step and nothing here is a
proposal to anyone.

Write one when a decision is worth more than an issue comment: it changes a
charter, it overturns an earlier ruling, or a later reader would otherwise
have to re-derive the reasoning.

1. Create `rfc/${isodate}-${short-title}.md` on a branch.
2. Open with the question, then the ruling. Name the issue it answers, and
   name anything it supersedes, quoted, so a reader can see what changed.
3. Ground it in measurement. A figure gets a source the next reader can
   re-derive it from: a commit, an artifact, a test. Recompute rather than
   copying, since several figures in this repository have been wrong on first
   statement.
4. End with what was **not** verified. A design record without that section
   reads as more settled than it is.
5. Open a pull request against this repository, linked to the issue.

Not every design gets one. A decision that only affects the code it lives in
belongs in a doc comment beside that code, where it cannot drift out of sight
of the thing it governs.

## Amending one

An accepted record is not frozen while its work is in flight; open a pull
request against it.

Once the work is done, prefer leaving it as the record of what was decided. If
a later decision invalidates part of it, add a short `[!NOTE]` callout beside
the affected content naming the record that replaced it, so a future reader is
not misled by the old text. Rewriting the body of a settled record loses the
history that made it worth keeping.

The template is [yyyymmdd-template.md](./yyyymmdd-template.md). It came from
upstream and its section headings are a starting point rather than a
requirement.
