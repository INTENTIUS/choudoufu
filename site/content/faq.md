# Questions

The answers that do not belong on one of the path pages, because they cut
across all of them.

## Why is it called choudoufu?

Stinky tofu. It is off-putting on first encounter and people who like it like
it a lot.

Practically: the fork is OpenTofu, and the feature is one most people's first
reaction to is that removing the state file cannot possibly be a good idea.

## Is it a separate tool or a drop-in replacement?

All of OpenTofu plus one feature. The binary is `choudoufu`, and until a
configuration declares a `live` block it behaves exactly like the OpenTofu
commit it was forked from. The marker machinery only wakes up when a
configuration opts in.

Everything that is not live markers is stock OpenTofu, unmodified, documented
at [opentofu.org](https://opentofu.org/docs/).

## Is it production ready?

No. It is experimental, AWS only, and the command surface can still change.
`choudoufu live-plan` and `choudoufu live-mv` say EXPERIMENTAL in their own
`-help` text.

What is true is that the claims on this site are checked. The demo harness is
also the test suite, and its exit code is the verdict on whether each claim
still holds.

## Does it really keep no record at all?

It keeps records. None of them are authoritative.

A record is authoritative when believing it over the live system would make
OpenTofu do the wrong thing to your infrastructure. Prior state under markers
is a projection: rebuilt from the live system on every run by reading ownership
tags off your resources, and discarded when the run ends. A stale or missing
projection costs one re-read, never a wrong plan.

That is the difference worth holding on to. There is nothing to store, lock,
back up, or repair.

## What happens to my existing state file?

Deleting it is the migration. Adopting the live resources it described is the
part that needs care, and [Migrate an existing estate](migrate.html) is that
whole story.

## Can I get back to stock OpenTofu later?

Yes, and it costs nothing to keep the door open.

The markers are plain tags and the resources are ordinary resources. Remove the
`live` block, restore a `backend` if you want one, and import the resources
into a fresh state file with stock tooling. The marker tags can stay, since
stock OpenTofu ignores them, or you can delete them with your cloud CLI.

## What stops someone stripping a resource's markers?

Nothing in choudoufu, because the markers live in your account and your
account's access controls are what protect them. Stripping a marker makes the
resource invisible to the next plan, which then proposes creating a replacement
beside it.

`live/MARKERS.md` has the full treatment, including which protections were
tested rather than assumed, and what residual risk remains.

## With no lock, what stops two people applying at once?

Nothing prevents it, the same way a lock never actually prevented the conflicts
people think it did. What changes is the failure mode: every race resolves to a
clean re-plan or a loud collision naming both live resources, and none of them
silently orphans anything.

[Day-2 operations](day2.html#running-this-with-other-people) has the case-by-case
table.

## How does this relate to upstream OpenTofu?

choudoufu is a fork of [opentofu/opentofu](https://github.com/opentofu/opentofu),
tracking upstream and adding live resource markers. It is licensed MPL-2.0, the
same as OpenTofu.

It is not affiliated with or endorsed by OpenTofu or the Linux Foundation.
OpenTofu is a registered trademark of the Linux Foundation.
