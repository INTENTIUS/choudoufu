# Questions

The answers that do not belong on one of the path pages, because they cut
across all of them.

## Why is it called choudoufu?

Stinky tofu. It is off-putting on first encounter and people who like it like
it a lot.

Practically: the fork is OpenTofu, and the feature is one most people's first
reaction to is that letting the record of what you own go stale cannot
possibly be a good idea.

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

No, and that claim is worth stating carefully, because a stronger version of it
gets repeated a lot.

**What goes away is state operations, not the idea of a record.** You never
configure a backend, take a lock, migrate a state file, or run state surgery.
That is the invariant.

**Most of the record moved onto the resources themselves.** Identity lives in
two tags on each resource, in your account, readable and writable with your own
cloud tools. Prior state is a projection rebuilt from those tags on every run
and discarded when the run ends. It is allowed to be stale, because a stale or
missing projection costs a re-read, never a wrong plan. That is the property
OpenTofu's state file does not have, and it is the whole argument.

**A small amount genuinely cannot be rebuilt.** An effect with no cloud twin
leaves nothing to read back: `null_resource`, `terraform_data`, `time_*`, and
`random_*` whose output carries no secret. Those persist as micro-state, one
small record per resource, through a `record_store` declared in the `live`
block. The backends are SSM Parameter Store, S3, or a local directory.

Secret-generating resources are refused rather than recorded. `random_password`,
`random_bytes` and every `tls_*` produce material only the state file ever
remembered, and a record that holds a secret is a state file with extra steps.

So: no state file to manage in the ordinary case, and a per-resource record for
the effects that need one. Nothing to store, lock, back up, or repair either
way.

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

The micro-state records are the one place ordering genuinely matters, and they
handle it without a lock. Writes are conditional: a record is written only if
it still holds the version the writer read. A losing writer gets a named
failure rather than a blocking wait or a silent overwrite.

[Day-2 operations](day2.html#running-this-with-other-people) has the case-by-case
table.

## How does this relate to upstream OpenTofu?

choudoufu is a fork of [opentofu/opentofu](https://github.com/opentofu/opentofu),
tracking upstream and adding live resource markers. It is licensed MPL-2.0, the
same as OpenTofu.

It is not affiliated with or endorsed by OpenTofu or the Linux Foundation.
OpenTofu is a registered trademark of the Linux Foundation.
