# Questions

Answers that cut across every path page.

## Why is it called choudoufu?

Stinky tofu. Off-putting at first, and people who like it like it a lot.

The fork is OpenTofu, and most people's first reaction is that letting the
record of what you own go stale cannot possibly be a good idea.

## Separate tool or drop-in replacement?

All of OpenTofu plus one feature. Until a configuration opts in with an
`estate.chdf.hcl` sidecar or a `live` block, the binary behaves exactly like the
OpenTofu commit it forked from. The marker machinery wakes only when asked.

Everything that is not live markers is stock OpenTofu, documented at
[opentofu.org](https://opentofu.org/docs/).

## Is it production ready?

No. Experimental, AWS only, and the command surface can still change.
`choudoufu live-plan` and `choudoufu live-mv` say EXPERIMENTAL in their own
`-help` text.

The claims on this site are checked. The demo harness is also the test suite,
and its exit code is the verdict.

## Where does the state live?

In three places, each already a platform feature. Every operation you run is a
plan, an apply, or a tag write. That is the invariant.

**Identity is two tags on each resource**, in your account, readable and
writable with your own cloud tools. Prior state is a projection rebuilt from
those tags every run and discarded at the end. It may go stale, because a stale
projection costs a re-read, never a wrong plan.

**Values the platform cannot hold go in a `record_store`.** An effect with no
cloud twin reads back as nothing, so `null_resource`, `terraform_data`,
`time_*`, and `random_*` without secrets persist as one small record each.
Backends are SSM Parameter Store, S3, or a local directory.

**Access to both is your IAM.** Reading ownership is a tagging API call.
Reading a record is a `GetParameter`. Each authorizes per resource through
policies you already run, so the granularity you designed for your account is
the granularity you get on your state.

Concurrent record writes settle by conditional write. Two racing runs produce a
named failure on one, never a wait or a silent clobber.

Secret-generating resources are refused rather than recorded. `random_password`,
`random_bytes` and every `tls_*` produce material a record has no business
holding.

Records live in SSM Parameter Store or S3, whose durability is theirs to
provide rather than yours to arrange. Losing one is churn rather than a lost
estate. The effect re-runs or its value regenerates, and anything reading that
value plans as a change. It cannot cost you a resource, because identity
arguments must be statically evaluable, so a record-backed value can never name
one.

## What happens to my existing state file?

Adopting the resources it describes is the migration, and that is the part
needing care. [Migrate an existing estate](migrate.html) is the whole story.
Once each resource carries its own markers, the file holds nothing that is not
on the resources themselves, and you can retire it.

## Can I get back to stock OpenTofu?

Yes, and keeping the door open costs nothing.

Markers are plain tags and the resources are ordinary resources. Remove the live
configuration, restore a `backend` if you want one, and import the resources
into a fresh state file with stock tooling. Marker tags can stay, since stock
OpenTofu ignores them, or delete them with your cloud CLI.

## What stops someone stripping a resource's markers?

Nothing in choudoufu. The markers live in your account and your account's
access controls protect them. Stripping a marker hides the resource from the
next plan, which then proposes a replacement beside it.

`live/MARKERS.md` has the full treatment, including which protections were
tested rather than assumed, and what risk remains.

## What stops two people applying at once?

Nothing prevents it. Every race resolves to a clean re-plan or a loud collision
naming both live resources. None silently orphans anything.

Records are the one place ordering matters, and they settle it by conditional
write. A record is written only if it still holds the version the writer read.
A losing writer gets a named failure rather than a blocking wait or a silent
overwrite.

[Day-2 operations](day2.html#running-this-with-other-people) has the
case-by-case table.

## How does this relate to upstream OpenTofu?

choudoufu forks [opentofu/opentofu](https://github.com/opentofu/opentofu),
tracking upstream and adding live resource markers. Licensed MPL-2.0, the same
as OpenTofu.

Not affiliated with or endorsed by OpenTofu or the Linux Foundation. OpenTofu is
a registered trademark of the Linux Foundation.
