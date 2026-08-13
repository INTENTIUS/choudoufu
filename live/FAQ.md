# FAQ

Answers for an OpenTofu user seeing this repository for the first time.

## Why would I want this?

The one-line version is that choudoufu state is allowed to be stale and
OpenTofu state is not. OpenTofu's state file is authoritative. When it is
wrong, lost, or locked, the tool does wrong things or stops, which is why
it has to be stored, locked, backed up, and repaired with dedicated
commands. Choudoufu keeps no record it believes over the world. Every plan
re-reads the live system, so a stale or missing record costs one re-read,
never a wrong plan. In practice that removes the backend, the lock and its
infrastructure, state surgery (`state rm`, `moved` blocks, import
ceremony), and the file that quietly accumulates secrets. A crash
mid-apply leaves nothing to unlock or recover, because ownership markers
ride the create calls themselves. Two applies racing fail loud, with both
IDs named, instead of silently orphaning a resource.

## What is this?

Choudoufu is a fork of OpenTofu that adds live resource markers. Instead
of recording what it manages in a state file, it stamps plain tags on each
resource it creates. On every plan it reads those tags back off the live
system, rebuilds its picture of the world from them, and throws that
picture away when the run ends. There is no state file, no backend, and no
lock. It is experimental and AWS only at the moment.

## Is it a separate tool or a drop-in replacement?

It is all of OpenTofu plus one feature. The binary is called `choudoufu`,
and until a configuration declares a `live` block it behaves exactly like
the OpenTofu commit it was forked from. The marker machinery only wakes up
when a configuration opts in.

## Where do I download it?

From the [releases page](https://github.com/INTENTIUS/choudoufu/releases).
Every tagged release carries prebuilt binaries for macOS, Linux and Windows
on amd64 and arm64, with a `SHA256SUMS` file. The README's Install section has a
copy-paste download snippet. Building from source is still one command,
`go build ./cmd/choudoufu`, which is what the demo harness does unless you
point `TOFU_BIN` at a downloaded binary.

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
(`website/docs/language/live-markers.mdx`, rendered on the docs site)
walks through a full example.

## Will my editor or linters choke on the live block?

Yes, if they parse the `terraform` block strictly against upstream's
schema, and no if they only tokenize HCL without validating it against a
schema. Tested directly: stock Terraform (not this fork) rejects a
configuration containing a `live` block outright, both `terraform init`
and `terraform validate`, with

```
Error: Unsupported block type

  on main.tf line 6, in terraform:
   6:   live {

Blocks of type "live" are not expected here.
```

exit code 1. This is expected, not a bug to work around: `live` is this
fork's own addition to the `terraform` block's schema
(`internal/configs/live.go`), and nothing about it is signaled to a tool
that never heard of it. `tofu validate` from stock, unmodified OpenTofu
would refuse the same configuration the same way, for the same reason -
this was not tested directly (no `tofu` binary was available to test
against), but the schema mismatch it would hit is identical.

Any tool that decodes the `terraform` block's schema this strictly will
have the same problem: `tflint` was not available to test against either,
but it decodes HCL through OpenTofu's own configuration-loading libraries,
which do exactly this schema check, so expect the same rejection until
`tflint` (or your fork of it) knows about this fork's schema. A tool that
only tokenizes or partially parses HCL - most syntax highlighters, `hclfmt`,
generic HCL linters that check formatting rather than block schemas - has
no problem with `live`, since nothing about its shape (an ordinary nested
block with attributes and one nested `policy` block) is unusual HCL.

There is no workaround that keeps a `live`-bearing configuration validating
against stock tooling today: the block either exists in the schema the tool
decodes against, or it does not. The practical options are running
`choudoufu validate` (this fork's own binary, which does know the schema)
in CI instead of stock `terraform validate`, or keeping the `live` block
isolated to a small root module that stock tooling never has reason to
touch.

## What happens to my existing state file?

For a brand new estate there is nothing to migrate, since you never had a
state file. For an existing project, deleting the state file is the
migration, but adopting the live resources is deliberate, not automatic.
Resources that already carry this estate's markers bind immediately.
Resources that do not are either offered for adoption in the plan's
Adoptable section (four types the classifier can match on content) or
refused with an `[UNOWNED]` note that names the exact tags to write. The
refusals also get their own rendered `Unowned` section in the plan, one
entry per live resource, marked `[ADOPTABLE]` with the tag values to copy
or `[IN_THE_WAY]` when the resource belongs to another estate. Read
"Migrating an Existing Estate" on the concept page before you apply
anything, because applying a plan without reading it can create
duplicates.

## Can I manage my whole infrastructure with this?

Closer than it used to be. Most mature Terraform estates are
module-structured, and that used to be the single biggest excluder -
today, module trees are admitted in the two shapes whose addresses stay
stable, and refused in the one shape whose addresses do not.

**Static module trees and statically-keyed `for_each` modules are
supported.** A resource inside a plain `module "app" {}` call binds by
its module-qualified address (`module.app.aws_x.y`) exactly as soundly as
a root resource does, at any nesting depth. A module call expanded with
`for_each` is admitted too, as long as its keys are statically evaluable -
a literal collection, or one built from variables, locals, `path` and
`terraform` values - because a key does not shift under insertion or
removal the way a `count` index does, so `module.app["prod"]` stays a
stable address for a marker to bind to no matter what happens to
`module.app["staging"]`. Auto-stamping cannot reach inside a keyed
module's own instances, though - its several instances share one
configuration body for the `tags` argument, so there is no single literal
address that is right for all of them. Build it by hand instead, threading
the module's own `each.key` through as a variable, the ordinary way a
value that must vary per instance reaches a child module:

```hcl
module "app" {
  for_each = toset(["prod", "staging"])
  key      = each.key                    # 1. pass each.key through
}
# inside the module:
variable "key" { type = string }         # 2. receive it as a variable
# tofu-address = "module.app[\"${var.key}\"].aws_x.y"  # 3. build the address by hand
```

**`count` on a module block is refused, permanently.** `count` renumbers
every address beneath it positionally on every insertion or removal above
the changed index, and a `tofu-address` marker records an address, not a
position - a renumbering that moves addresses out from under their own
markers is precisely the ambiguity markers exist to remove. Rewrite the
block as a keyed `for_each` over your own stable names, move its resources
into the root module, or give it an estate of its own.

The remaining hard limits, in the order they will actually stop you:

**It is experimental.** The admitted subset is real but partial, and the
command surface can still change. This is not yet something to bet
production infrastructure on without reading the rest of this list.

**A large, and still growing, list of resource types.** The concept
page's Contract section enumerates the current admitted list - it is
rendered from the admission table itself, so it cannot go stale. It now
covers EC2 instances, RDS, ECS/EKS clusters, and API Gateway alongside the
core VPC networking, S3 and its children, the IAM core, the ALB stack,
DynamoDB, KMS, Route53, ACM, CloudWatch basics, SQS/SNS, Lambda, and ECR
that were there first. It does not yet cover everything - Secrets
Manager, Cognito, WAF, and MSK are examples of families still missing.

**AWS only, and no logical resources.** Multi-cloud estates can bring
only their AWS portion, and configs leaning on `random_*`, `tls_*` or
similar are refused with a family-level explanation.

Beyond those, the construct limits: no provisioners, no workspaces, no
saved plan files. Each limit is documented with its reasoning and its
enforcing lint rule in `live/LIMITATIONS.md`, and the lint refuses a
config outside the subset up front - naming the specific reason per
resource - rather than failing halfway through an apply.

## What do I get instead of a saved plan, for a plan-approve-apply workflow?

No saved plan file, ever - `-out` is refused everywhere a live-markers run
appears, on `choudoufu live-plan` and on plain `choudoufu plan` and `apply`
once a configuration carries a `live` block, and applying a saved plan
file is refused the same way. There is no separate `live-apply` command
either: an ordinary `choudoufu apply` is what applies a live-markers
configuration, made stateless by the same hook `plan` uses. What that
means concretely, read from the code rather than assumed: every `apply`
re-reads the live system, re-runs lint and discovery, and rebuilds the
plan from scratch immediately before applying it - and that freshly built
plan is rendered and confirmed the same way any ordinary `apply`'s plan is
(the standard "do you want to perform these actions?" prompt), not applied
silently. `-auto-approve` skips the prompt exactly as it always has.

The honest gap this leaves, compared to a saved-plan workflow: the plan a
human reviews and the plan that gets applied are the same plan only when
nothing else touches the estate between the two, because there is no
artifact recording what was reviewed for a later step to check against.
Terraform/OpenTofu's usual plan-approve-apply split - produce a plan file,
have a person or a gate approve it, apply exactly that file later,
possibly from a different process or machine - is not available here even
in spirit: an `apply` always plans again right before it applies, and there
is no way to say "apply this specific, already-reviewed diff and refuse if
the live system has moved since." For a single interactive run this is no
different from approving any ordinary `apply`'s prompt; for a CI pipeline
built around a separate plan and apply stage with a gate in between, it is
a real, currently-unfilled gap, not a documented feature under another
name.

## Will my estate's resource types ever be covered?

Almost certainly yes, and this is now a measured claim rather than a
hope. Every one of the provider's 1,691 resource types has been
classified, with the classification committed as an artifact
(`live/mapping.json`) and enforced by tests:

- About three quarters have a CloudFormation-registry counterpart or fold
  into one as a property-child. For these, identity and enumeration are
  machine-derivable, and admission proposals are generated with evidence
  and adopted in reviewed batches - the list grows on a schedule, not by
  hand.
- The rest are classified with a recorded reason each: provider-side
  constructs that manage no cloud resource of their own (waiters,
  exclusive-set managers), resources CloudFormation does not model, dying
  services (Pinpoint, Greengrass V1, WAF classic), and a handful of
  structurally ambiguous types. `live/LIMITATIONS.md`'s exclusion-cohort
  section names every cohort with its count.

The types a typical estate is actually made of - compute, networking,
storage, queues, identity, DNS, certificates, observability - sit almost
entirely in the machine-reachable three quarters. What concentrates in
the unreachable quarter is what you arguably should not manage this way
anyway: data-plane objects (`aws_s3_object`, DynamoDB items), credentials
(access keys, secret versions - excluded by rule so they never transit a
marker), and services AWS is retiring. If your estate is ordinary
infrastructure on living services, the type dimension will not be what
blocks you; the module limit and the tool's experimental status will be,
and both are stated above rather than discovered mid-apply.

## Does it really keep no record at all?

No record it ever reads. An apply can leave an observational snapshot
behind when the configuration asks for one — a JSON record of what the run
saw, written to a `snapshot_path` file or as commits on a
`tofu-snapshots/<estate>` git branch. Nothing in the fork reads a snapshot
back (a test, `TestSnapshot_noReader`, pins that), so deleting one changes
nothing about any run. It exists for humans: drift over time becomes `git
log` and `git diff` on the branch. The concept page's `snapshots` reference
has the details.

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

That is the receipts pattern, specified in `live/RECEIPTS.md`. A
receipt is an ordinary SSM parameter in the estate that carries a
fingerprint of an effect's declared inputs, so the plan diff becomes the
reviewable signal that the effect needs to run again. OpenTofu never runs
the effect itself.

## How do two split estates share a value, with no remote state?

`terraform_remote_state` is refused for the same reason everything else in
`live/LIMITATIONS.md` is: it reads a state file, and there is none. The
answer, specified in `live/OUTPUTS.md`, is a plain data source: the
consuming estate reads the producing estate's live resource with a data
source of its own type, filtered on that resource's `tofu-estate`/
`tofu-address` marker tags (`live/MARKERS.md`). A first-class "estate
output" surface, outputs mirrored into SSM parameters under an estate
namespace, was considered and declined. `live/OUTPUTS.md`'s "Why not
outputs-as-receipts" has the reasoning, in short that a mirrored value is a
second copy that can go stale, which is exactly the failure mode removing
the state file was meant to retire.

## Is it production ready?

No. It is experimental, the command surface can still change, and the
admitted subset is small. What you can trust is that the claims it does
make are executable. `bash live/e2e/run.sh --expect 5` proves them
against a live emulator, and the exit code is the verdict.

## Can I get back to stock OpenTofu later?

Yes. The markers are plain tags and the resources are ordinary resources.
Remove the `live` block, restore a `backend` if you want one, and import
the resources into a fresh state file with stock tooling. The marker tags
can stay, since stock OpenTofu ignores them, or be deleted with your cloud
CLI.

## How does this relate to removed blocks / destroy = false?

Upstream needs three one-shot constructs because state is authoritative and
each of them edits that record surgically rather than replacing it: an
`import` block writes a new state entry, a `moved` block rewrites an entry's
address, and a `removed` block with `lifecycle { destroy = false }` deletes
an entry while leaving the underlying object alone. All three exist to
repair or extend a file of record without disturbing the object it describes.

Choudoufu has no file of record, so it does not need the constructs that
edit one. Bringing a resource under management is `choudoufu live-import`
for a bulk migration off an existing state file, or the plan's `Adoptable`
section plus its printed tag-write command for an ad hoc case - both
plan-visible, both a marker stamp rather than a state write ("Migrating an
Existing Estate" on the concept page has the walkthrough). Renaming is
`choudoufu live-mv`, which rewrites the `tofu-address` tag in place and is
the entire replacement for `moved` blocks; `moved` blocks themselves are
refused by lint (`live/LIMITATIONS.md`, "moved-block").

Forgetting without destroying is where the parallel stops being exact today.
There is no state entry to mark "keep but stop tracking," so deleting the
`resource` block is the whole gesture - but that gesture does not yet mean
"forget." The marker stays on the live resource after the block is gone, and
for a taggable, admitted type the estate-wide sweep on the next plan finds
that marker with no declaring block behind it and plans a destroy
(`internal/live/lifecycle/exactness_test.go` asserts exactly this: delete
the block, get one destroy on plan and on apply). That is upstream's own
default too - deleting a resource block destroys, unless a `removed` block
says otherwise. The difference is upstream gives you that override and
choudoufu, today, does not: the only way to delete the block without losing
the resource is to strip its `tofu-estate`/`tofu-address` tags by hand with
your cloud CLI first (the same manual path "Can I get back to stock OpenTofu
later?" above describes), which makes the resource foreign to the estate
before the sweep ever looks for it. There is no `choudoufu` command that
does the untagging for you.

That gap is design-in-progress, not a shipped feature: [issue
#67](https://github.com/INTENTIUS/choudoufu/issues/67) proposes a
configurable action per ownership quadrant (declared+marked,
declared+unmarked, undeclared+marked, undeclared+unmarked) in place of
today's fixed wiring (converge, refuse/offer adoption, sweep, ignore,
respectively). An `undeclared+marked` resource set to `keep` instead of
`sweep` is exactly "forget without destroy," made a policy instead of a
manual tag edit. Until that lands, the honest answer is: choudoufu deletes
the *category* of one-shot state-surgery constructs, but the specific
behavior of upstream's `destroy = false` is not yet one of the choices, only
a documented gap with a manual workaround.

## What stops someone from stripping a resource's markers and getting a duplicate?

Nothing stops the tags from being removed - they are ordinary AWS resource
tags, and `aws ec2 delete-tags` or a console cleanup can untag anything
with the right permissions. What it costs afterward depends on the type.
A client-named resource is a nuisance: the next plan reports it
`[UNOWNED]` with the adoption command, and the cloud's own uniqueness
constraint means a duplicate can never actually be created under the same
name. A server-assigned resource (`aws_vpc`, `aws_security_group`, and the
rest) has no other handle, so the next plan proposes creating a second
one beside the orphaned first.

`live/MARKERS.md`'s "Protecting the markers" section has the full answer:
what an AWS Organizations tag policy can and cannot do here (it enforces
tag *values*, not tag *survival*, and does not block untagging at all), an
SCP snippet that denies the untagging actions on the three marker keys to
everyone but the automation principal, with the caveats that make it
honest rather than a false sense of safety, and the plan-time backstop -
every create of an admitted type gets checked against the estate's unowned
live resources of the same type, and a match earns a `[POSSIBLE
DUPLICATE]` warning naming the live resource and the adopt command,
directly above the plan diff. Warn, never block: the create might be
genuine. But it is never silent.

## How does this relate to upstream OpenTofu?

The fork was cut from
[opentofu/opentofu](https://github.com/opentofu/opentofu) at commit
`03743ce6e8`, and everything outside the marker feature is unchanged from
that commit. The project is not affiliated with or endorsed by OpenTofu or
the Linux Foundation.

## Why is it called choudoufu?

Chou doufu is stinky tofu, the fermented street-food cousin of regular
tofu. This is OpenTofu with a stronger flavor.
