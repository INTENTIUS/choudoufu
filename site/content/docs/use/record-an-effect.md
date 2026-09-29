---
title: "Receipts: make an external effect show up in a plan"
weight: 8
aliases: ["/docs/model/effects/", "/how-it-works/effects/"]
---

# Receipts: make an external effect show up in a plan

A migration that ran, a cache that was invalidated, a notification that was
sent: none leaves anything in the platform to read back, so no plan can tell
you whether it already happened. A receipt makes it visible. It is optional,
and most estates have none.

![The plan shows a receipt diff for an effect that is otherwise invisible](diagram-effects.svg)

## What a receipt is

An ordinary resource you declare, holding a hash of the effect's input. It
goes through plan and apply like anything else, and its diff tells a reviewer
or a CI gate that this apply triggers something outside the resources being
managed. On AWS an SSM parameter at `/tofu-receipts/<estate>/<effect>` is a
supported choice. Nothing requires SSM: any resource whose value a reviewer
can read with the platform's own CLI does the job.

## choudoufu never runs the effect

`plan` and `apply` touch the receipt and nothing else. The effect runs in the
layer above, a CI step or a runbook, which sees the proposed receipt change,
runs the real effect, and lets apply write the new value once it succeeded.
If the tool ran the effect, the diff would stop being a preview and become the
thing happening mid-plan, which is a provisioner.

A provisioner runs when its resource is created and never again, and no plan
shows that it is about to run. A receipt's diff is the standing answer to
"have this effect's inputs changed since it last ran", asked on every plan.

The semantics are at-least-once. If the effect runs and the process dies
before the receipt is written, the next plan proposes the same change and the
effect runs again. An unconfirmed effect stays visible as a pending diff.

## The rules, which are linted

The value is a hash or a constant and never a `SecureString`. Nothing may
reference a receipt's attributes. Inputs name secrets by pointer and never by
value.

A receipt does not go in the record store. Its job is to be readable by
someone with read-only access and no `choudoufu` binary, and a record is
tool-internal JSON in a store few people may read. A `key_prefix` starting
with `tofu-receipts` is a configuration error.

`terraform_data`'s `triggers_replace` is not a substitute. It hides the
fingerprint in the tool's own store and tells a reviewer nothing.
`terraform_data` is for the dependency graph, and receipts are for external
effects.

[`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md)
has the pattern and the reasoning behind each guard.
