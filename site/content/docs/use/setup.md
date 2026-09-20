---
title: "What you set up by hand"
weight: 2
---

# What you set up by hand

Before any of this works, what has to exist that choudoufu will not create?
Credentials, access policy, and, for any estate more than one person runs, a
place for its records. Everything else is a line of configuration or
something the first run creates.

| Piece | What it is | Who creates it |
|---|---|---|
| Credentials | The provider's ordinary chain: the AWS SDK's, or a kubeconfig | You, before the first plan |
| Provider configuration | A `provider` block, or nothing | Optional |
| Estate declaration | `estate.chdf.hcl`, or `live { estate = "..." }` | A config edit |
| Record store, `local` | `.tofu-records` beside the module | The first run |
| Record store, `s3` | A bucket you already own, with three settings on it | You, before the first plan |
| Record store, `kubernetes` | A namespace you already own | You, before the first plan |
| Markers on resources choudoufu creates | Stamped on the create call | The apply |
| Markers on resources that already exist | The same markers | A write you run on purpose |
| Access policy | The provider's permissions plus the record store's | You |

## Credentials and region

Nothing about a `live` block changes where credentials come from. On AWS
that is the SDK chain: environment variables, then `~/.aws/config` and
`~/.aws/credentials`, then instance metadata.

A configuration with no `provider "aws"` block still runs. `choudoufu init`
resolves the provider from the resource type prefix, and the provider takes
its region and credentials from the ambient environment. A plan under the live backend
reads far more of an account than a stock plan does, so set `AWS_PROFILE`
deliberately before the first plan.

When something is missing, the failure arrives under a marker-discovery heading,
because discovery needs a configured provider before the plan graph is
walked.

| Missing | What prints |
|---|---|
| Region | `Error: Provider unavailable for marker discovery` ... `invalid AWS Region: .` |
| Credentials | `Error: Provider unavailable for marker discovery` ... `No valid credential sources found` |

Pin `required_providers` to the `provider_version` in `live/survey.json` if
you want the first plan quiet. Admission evidence is measured against that
version, and any other prints `Warning: Provider version does not match the
admission evidence version` and carries on.

## The estate declaration

One file or one block turns the live backend on.

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

Until this exists the binary behaves as stock OpenTofu, with no discovery
pass, no markers and no record directory. Two refusals guard the edges, both
at `init`.

| Mistake | What prints |
|---|---|
| A `backend` or `cloud` block alongside it | `Error: Both a backend and a live configuration are present`, at the offending block's line |
| Both the sidecar and a `live` block | `Error: Both a live sidecar file and a live block are present`, naming both |

[Start a new estate]({{< relref "/docs/use/start" >}}) covers the two forms.

### Deleting the state file is not enforced

A leftover `terraform.tfstate` is ignored. A plan run beside one proposes
creating every resource the file names, because prior state now comes from
markers and the markers are not on those resources yet.

`choudoufu live-import` reads that state file, and it is the command's only
input. Run it before deleting the file.
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) has the loop.

## The record store

Every estate has one, and declaring no `record_store` gets you the local one.
[Where things are stored]({{< relref "/docs/use/storage" >}}) covers what it
holds. This section is what must exist before the first plan.

| Backend | What must exist first |
|---|---|
| `local` | Nothing. Gitignore `.tofu-records/`, or the `path` you chose, because records hold secrets |
| `s3` | The bucket, with versioning, a lifecycle rule that expires noncurrent versions, and public-access block |
| `kubernetes` | The namespace, and a role bound to Secrets in it |

The store cannot be declared by the estate that uses it. The plan opens the
store before it can propose creating it, so the run fails with the same
`NoSuchBucket` text a typo gives. Create the bucket or the namespace outside
the estate.

The store holds secrets by default, readable by anyone who can read the
store. [Secrets]({{< relref "/docs/use/secrets" >}}) has who that is and the
ways out.

### A bucket

This is stock's S3 bootstrap without the lock table. Every write to the store
is one conditional request that holds nothing
([claim 4]({{< relref "/docs/claims/backend-sets-itself-up" >}})), so there
is no lock to create and none to strand an apply.

`examples/record-store-bucket` makes a correct bucket with CloudFormation, so
nothing on the path needs a state file.

```
cd examples/record-store-bucket
npm install
just up                    # or: just up <bucket-name>
just verify
```

`just up` prints the bucket name for the `record_store` block and how many
days a deleted record stays recoverable. `RECORD_NONCURRENT_DAYS` sets that
window, and `RECORD_KMS_KEY_ARN` puts the bucket under a key of yours
([Encryption at rest]({{< relref "/docs/use/bucket" >}})).

`just verify` asks the binary, which is also how you check a bucket made any
other way:

```
choudoufu live-bucket -bucket <name>
```

[The three settings]({{< relref "/docs/use/bucket" >}}) states what
makes a bucket correct, independent of the example. Then write the estate's
role its policy: [IAM for the record store bucket]({{< relref "/docs/use/bucket" >}}).

A missing bucket fails the plan, before anything is written:

```
Error: Cannot open the record store
... NoSuchBucket: The specified bucket does not exist
```

A bucket that fails one of the three settings refuses an estate's first run
against it by name, whatever the command, and leaves nothing behind. After
that the settings are checked before every apply.

### A namespace

Create one namespace per estate and bind the estate's role to `get`, `list`,
`create`, `update` and `delete` on Secrets in it, and to nothing wider. The
namespace is what keeps one estate's records from another, because RBAC
cannot condition on a label. No AWS account is involved.

```hcl
record_store "kubernetes" {
  namespace = "my-estate-records"
}
```

The first run checks that the namespace exists and that the role can do those
five things there, and refuses by name if not.

## Markers

A resource choudoufu creates is stamped on the create call itself, and no
separate tagging permission comes into it.

Adopting a resource that already exists is a write you run on purpose.
`choudoufu plan` prints an `Adoptable` section with the command already built:

```
aws_vpc.solo <- aws_vpc vpc-12909d4c
    matched on: cidr_block=10.70.0.0/16
    adopt with: aws ec2 create-tags --resources 'vpc-12909d4c' --tags ...
```

Content matching never offers a `count` or `for_each` instance. For an estate
that still has its state file, `choudoufu live-import` is the path, on AWS
and on Kubernetes alike.
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) has it.

## Permissions

[Reference]({{< relref "/docs/use/reference#permissions-a-run-needs" >}})
catalogues the actions per stage and per record store.

A plan changes no resource, and a role that may only read can run one. That
includes the record store: opening one sends a conditional write for the
store's sentinel, and a run that is refused that write carries on when the
sentinel is already there, so a role with `s3:GetObject` and `s3:ListBucket`
and no `s3:PutObject` can plan an estate some earlier run recorded. A bucket
that has never been opened is still refused by name, because a store with no
sentinel and no way to write one reads exactly like an estate with no
resources in it; run the estate once under a role that may write, and
read-only plans work from then on. Render the policy for such a role with
`render-policy.sh <estate> <bucket> --read-only`:
[IAM for the record store bucket]({{< relref "/docs/use/bucket#a-role-that-plans-and-never-applies" >}})
says what it leaves out and why, and
[claim 38]({{< relref "/docs/claims/a-read-only-role-can-plan" >}}) is the
run on real AWS where such a role plans an established estate, writes
nothing, and is refused by name against a store no run has provisioned.

A plan reads widely. The estate-wide sweep finds resources whose block was
deleted, and its width comes from the admission table and not from the size
of your estate. A read-only role scoped to the services you declare will not
cover it, and the sweep degrades to an `Incomplete sweep` warning per type it
could not list, with a `Not swept for removal` section naming them. An empty
removal list is a statement about the types that were swept and about nothing
else. [What a plan costs]({{< relref "/docs/model/plan-cost#the-two-terms" >}})
has the current count.

Adopting an existing resource needs that service's own tagging action,
`ec2:CreateTags` for the EC2 family and the
[per-service verb]({{< relref "/docs/use/reference#marker-stamping" >}}) for
the rest.

If your account already holds resources this configuration should manage,
read [Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) before
applying anything. Nothing binds a live resource to your configuration until
its marker is on it, so applying against unmarked resources creates a second
copy beside them.
