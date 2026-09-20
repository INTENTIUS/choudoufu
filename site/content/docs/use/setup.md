---
title: "What you set up by hand"
weight: 2
---

# What you set up by hand

What has to exist that choudoufu will not create? Credentials, an access
policy, and, for an estate more than one person runs, a place for its
records.

| Piece | Who creates it |
|---|---|
| Credentials, and an access policy | You, before the first plan |
| The estate declaration, `estate.chdf.hcl` | You, one line |
| A local record store | The first run |
| A shared record store: a bucket, or a namespace | You, before the first plan |
| Markers on resources choudoufu creates | The apply |
| Markers on resources that already exist | You, with a command the plan prints |

## Credentials and region

Nothing about a `live` block changes where credentials come from. On AWS that
is the SDK chain: environment variables, then `~/.aws`, then instance
metadata.

A configuration with no `provider "aws"` block still runs, against whatever
your environment points at. A plan under the live backend reads far more of
an account than a stock plan does, so set `AWS_PROFILE` on purpose before the
first one. A missing region or credential fails with `Error: Provider
unavailable for marker discovery`.

Pin `required_providers` to the `provider_version` in `live/survey.json` for a
quiet first plan. Any other version prints a warning and carries on.

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

[Set up a record store bucket]({{< relref "/docs/use/bucket" >}}) has the
commands and the policy for a bucket. An estate that runs only on Kubernetes
needs neither bucket nor AWS account: create one namespace per estate,
`tofu-records-<estate>` by default, and bind the estate's role to Secrets in
it and nothing wider ([Kubernetes]({{< relref "/kubernetes" >}})).

## Markers

A resource choudoufu creates is marked on the create call. A resource that
already exists is adopted by a write you run on purpose:
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) has both
ways.

## Permissions

[Reference]({{< relref "/docs/use/reference#permissions-a-run-needs" >}})
catalogues the actions per stage and per record store.

A plan changes no resource, and a role that may only read can run one. That
includes a bucket or a local record store, once some earlier run under a
writing role has opened it. A cluster store also wants `create` on its
Secrets ([Kubernetes]({{< relref "/kubernetes/operate" >}})). A bucket no run has opened is refused by name, because an unopened
store looks exactly like an empty estate.
[A role that plans and never applies]({{< relref "/docs/use/bucket#a-role-that-plans-and-never-applies" >}})
renders the policy, and
[claim 38]({{< relref "/docs/claims/a-read-only-role-can-plan" >}}) runs it on
real AWS.

A plan reads widely. The sweep that finds resources whose block was deleted
covers every admitted resource type, however small your estate is. A role
scoped to the services you declare will not cover it, and the plan says so
with an `Incomplete sweep` warning per type it could not list.
[What a plan costs]({{< relref "/docs/model/plan-cost#the-two-terms" >}}) has
the numbers.

Adopting an existing resource needs that service's own tagging action,
`ec2:CreateTags` for the EC2 family and the
[per-service verb]({{< relref "/docs/use/reference#marker-stamping" >}}) for
the rest.
