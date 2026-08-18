# Effects

A migration that ran. A cache that was invalidated. A notification that was
sent. None of them leaves anything in the cloud to read back, so no plan can
tell you whether it already happened.

A receipt is how you make it visible.

![A receipt makes an invisible effect visible in the plan](diagram-effects.svg)

## What a receipt is

An ordinary resource you declare, by convention an SSM parameter at
`/tofu-receipts/<estate>/<effect>` holding a hash of the effect's input.

A record store can be backed by Parameter Store too, so both can end up as
parameters in the same account. The difference is who owns them. A record is
written by choudoufu and its format is internal. A receipt is written by your
configuration, appears in your plan, and is yours to read.

It goes through the ordinary plan and apply cycle. Its diff appearing in a plan
is what tells a reviewer or a CI gate that this apply will trigger something
outside the resources being managed.

## choudoufu never runs the effect

This is the part that makes the diff mean something. `plan` and `apply` touch
the receipt resource and nothing else. The migration itself runs in the layer
above, a CI step or a runbook, which sees the proposed receipt change, runs the
real effect, and lets apply write the new value only once the effect succeeded.

If the tool ran the effect itself, the diff would stop being a preview of what
is about to happen and become the thing happening mid-plan. That is a
provisioner, and provisioners are refused.

The semantics are at-least-once. If the effect runs but the process dies before
the receipt is written, the next plan proposes the same change and the effect
runs again. Under-running never happens silently, and every unconfirmed effect
stays visible as a pending diff.

choudoufu does not write receipts. It lints them, enforcing that the value is a
hash or constant and never a `SecureString`, that nothing references a
receipt's attributes, and that inputs name secrets by pointer rather than by
value.

## Why a receipt is not a record

Enforced rather than advised. A `key_prefix` whose first segment is
`tofu-receipts` is a configuration error, so a record can never land in the
receipts namespace.

Visibility is why. A receipt is AWS-native so its value stays readable with a
plain `aws ssm get-parameter`, by someone with read-only IAM and no `choudoufu`
binary, at three in the morning. A record-store payload is tool-internal by
design. Moving a receipt onto it would trade a one-line CLI call for
choudoufu's own JSON envelope, which is strictly worse for the one artifact
whose job is being legible to someone not running the tool.

## The tempting mistake

Now `terraform_data` is record-backed, its `triggers_replace` looks like a
pseudo-receipt. Do not use it that way. It hides the fingerprint in the tool's
own store instead of a declared resource, and collapses a receipt into "did an
input change", with no existence flavour, no hash flavour, and no naming
convention the lint rules recognise.

`terraform_data` is for the graph, ordering an apply, feeding
`replace_triggered_by`, or standing in for a resource that does nothing.
Receipts are for external effects. Keep them apart.

[`live/RECEIPTS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECEIPTS.md)
has the pattern and the reasoning behind each guard.
