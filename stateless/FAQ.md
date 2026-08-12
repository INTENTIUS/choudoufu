# FAQ

Answers for an OpenTofu user seeing this repository for the first time.

## What is this?

Choudoufu is a fork of OpenTofu that adds live resource markers. Instead
of recording what it manages in a state file, it stamps plain tags on each
resource it creates. On every plan it reads those tags back off the live
system, rebuilds its picture of the world from them, and throws that
picture away when the run ends. There is no state file, no backend, and no
lock.

## Is it a separate tool or a drop-in replacement?

It is all of OpenTofu plus one feature. The binary is called `choudoufu`,
and until a configuration declares a `live` block it behaves exactly like
the OpenTofu commit it was forked from. The marker machinery only wakes up
when a configuration opts in.

## Where do I download it?

Nowhere yet. There are no published releases or packages. Clone the
repository and run `go build ./cmd/choudoufu` to produce the binary. The
demo harness builds it the same way.

## How do I turn markers on?

Add a `live` block to your `terraform` block and remove any `backend` or
`cloud` block.

```hcl
terraform {
  live {
    estate = "my-estate"
  }
}
```

The estate name is the unit of ownership, and every resource this
configuration manages gets tagged with it. The concept page
(`website/docs/language/stateless-mode.mdx`, rendered on the docs site)
walks through a full example.

## What happens to my existing state file?

For a brand new estate there is nothing to migrate, since you never had a
state file. For an existing project, deleting the state file is the
migration, but adopting the live resources is deliberate, not automatic.
Resources that already carry this estate's markers bind immediately.
Resources that do not are either offered for adoption in the plan's
Adoptable section (three types the classifier can match on content) or
refused with an `[UNOWNED]` note that names the exact tags to write. Read
"Migrating an Existing Estate" on the concept page before you apply
anything, because applying a plan without reading it can create
duplicates.

## Can I manage my whole infrastructure with this?

Almost certainly not yet. The admitted subset is AWS only, 14 resource
types, root module only. No child modules, no provisioners, no `random_*`
or other logical resources, no workspaces, no saved plan files. Each limit
is documented with its reasoning and its enforcing lint rule in
`stateless/LIMITATIONS.md`, and the lint refuses a config outside the
subset up front rather than failing halfway through an apply.

## With no lock, what stops two people from applying at once?

Nothing, the same as when a backend lock fails, except the failure modes
are better behaved. The cloud's own uniqueness constraints reject
duplicate client-named resources. A duplicated server-ID resource shows up
on the next plan as a marker collision naming both IDs, loudly, for a
human to resolve. The concept page's Concurrency section has the full
taxonomy, including a comparison table against backend locking. The
practical advice is unchanged from ordinary practice, which is to
serialize applies in CI.

## What about effects a plan cannot read back, like a database migration?

That is the receipts pattern, specified in `stateless/RECEIPTS.md`. A
receipt is an ordinary SSM parameter in the estate that carries a
fingerprint of an effect's declared inputs, so the plan diff becomes the
reviewable signal that the effect needs to run again. OpenTofu never runs
the effect itself.

## Is it production ready?

No. It is experimental, the command surface can still change, and the
admitted subset is small. What you can trust is that the claims it does
make are executable. `bash stateless/e2e/run.sh --expect 5` proves them
against a live emulator, and the exit code is the verdict.

## Can I get back to stock OpenTofu later?

Yes. The markers are plain tags and the resources are ordinary resources.
Remove the `live` block, restore a `backend` if you want one, and import
the resources into a fresh state file with stock tooling. The marker tags
can stay, since stock OpenTofu ignores them, or be deleted with your cloud
CLI.

## How does this relate to upstream OpenTofu?

The fork was cut from
[opentofu/opentofu](https://github.com/opentofu/opentofu) at commit
`03743ce6e8`, and everything outside the marker feature is unchanged from
that commit. The project is not affiliated with or endorsed by OpenTofu or
the Linux Foundation.

## Why is it called choudoufu?

Chou doufu is stinky tofu, the fermented street-food cousin of regular
tofu. This is OpenTofu with a stronger flavor. If the name reads as a
warning label, that is about right for an experimental fork.
