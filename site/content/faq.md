# Questions

The answers that do not belong on one of the path pages, because they cut
across all of them.

## Why is it called choudoufu?

Stinky tofu. It is off-putting on first encounter and people who like it like
it a lot.

The fork is OpenTofu, and most people's first reaction to the feature is that
letting the record of what you own go stale cannot possibly be a good idea.

## Is it a separate tool or a drop-in replacement?

All of OpenTofu plus one feature. The binary is `choudoufu`, and until a
configuration opts in with an `estate.chdf.hcl` sidecar file or a `live`
block, it behaves exactly like the OpenTofu commit it was forked from. The
marker machinery only wakes up when a configuration asks for it.

Everything that is not live markers is stock OpenTofu, unmodified, documented
at [opentofu.org](https://opentofu.org/docs/).

## Is it production ready?

No. It is experimental, AWS only, and the command surface can still change.
`choudoufu live-plan` and `choudoufu live-mv` say EXPERIMENTAL in their own
`-help` text.

The claims on this site are checked. The demo harness is also the test suite,
and its exit code is the verdict on whether each claim still holds.

## Where does the state live?

In three places, each one a feature the platform already has. Every operation
you run is either a plan, an apply, or a tag write. That is the invariant.

**Identity is two tags on each resource**, in your account, readable and
writable with your own cloud tools. Prior state is a projection rebuilt from
those tags on every run and discarded when the run ends. It is allowed to be
stale, because a stale or missing projection costs a re-read, never a wrong
plan.

**Values the platform has nowhere to put go in a `record_store`.** An effect
with no cloud twin leaves nothing to read back, so `null_resource`,
`terraform_data`, `time_*`, and `random_*` whose output carries no secret
persist as micro-state, one small record per resource. The backends are SSM
Parameter Store, S3, or a local directory.

**Access to both is your IAM.** Reading ownership is a tagging API call.
Reading a record is a `GetParameter`. Each is authorized per resource by the
policies you already run, so the granularity you designed for your account is
the granularity you get on your state.

Concurrent writes to a record are settled by conditional write, so two runs
racing produce a named failure on one of them rather than a wait or a silent
clobber.

Secret-generating resources are refused rather than recorded. `random_password`,
`random_bytes` and every `tls_*` produce material a record has no business
holding.

Records live in SSM Parameter Store or S3, services you already run and whose
durability is theirs to provide rather than yours to arrange. Losing one is
churn rather than a lost estate. The effect re-runs or its value regenerates,
and anything reading that value plans as a change. It cannot cost you a
resource, because a record-backed value is a resource attribute and identity
arguments have to be statically evaluable, so such a value can never name a
resource.

## What happens to my existing state file?

Adopting the resources it describes is the migration, and that is the part
that needs care. [Migrate an existing estate](migrate.html) is the whole
story. Once each resource carries its own markers, the file has nothing left
that is not on the resources themselves, and you can retire it.

## Can I get back to stock OpenTofu later?

Yes, and it costs nothing to keep the door open.

The markers are plain tags and the resources are ordinary resources. Remove
the live configuration (the `estate.chdf.hcl` sidecar or the `live` block),
restore a `backend` if you want one, and import the resources into a fresh
state file with stock tooling. The marker tags can stay, since stock OpenTofu
ignores them, or you can delete them with your cloud CLI.

## What stops someone stripping a resource's markers?

Nothing in choudoufu, because the markers live in your account and your
account's access controls are what protect them. Stripping a marker makes the
resource invisible to the next plan, which then proposes creating a
replacement beside it.

`live/MARKERS.md` has the full treatment, including which protections were
tested rather than assumed, and what residual risk remains.

## What stops two people applying at once?

Nothing prevents it. Every race resolves to a clean re-plan or a loud
collision naming both live resources, and none of them silently orphans
anything.

The micro-state records are the one place ordering matters, and they settle it
by conditional write. A record is written only if it still holds the version
the writer read. A losing writer gets a named failure rather than a blocking
wait or a silent overwrite.

[Day-2 operations](day2.html#running-this-with-other-people) has the
case-by-case table.

## How does this relate to upstream OpenTofu?

choudoufu is a fork of [opentofu/opentofu](https://github.com/opentofu/opentofu),
tracking upstream and adding live resource markers. It is licensed MPL-2.0, the
same as OpenTofu.

It is not affiliated with or endorsed by OpenTofu or the Linux Foundation.
OpenTofu is a registered trademark of the Linux Foundation.
