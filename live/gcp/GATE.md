# Gate

On AWS the marker is an authorization primitive: an IAM condition on
`aws:ResourceTag` fences reads and writes per resource. GCP IAM cannot do
that with a label. This page says in the headline what the GCP fence
covers and what it does not.

## What IAM cannot do

An IAM condition on GCP can read a resource's type, name and service, the
request time, and its Resource Manager tags through `resource.matchTag`.
It cannot read a label. So no binding can say "may change resources
labelled `tofu-estate=prod`".

## What the project does

The fence is the project
([#1121](https://github.com/INTENTIUS/choudoufu/issues/1121)). One estate
per project, and granting the estate is an ordinary project IAM binding for
the principal that runs it:

```
live/gcp/estate-grant.sh
```

It enables the Cloud Asset API in the project, binds
`roles/cloudasset.viewer` for the sweep, and binds the roles the estate's
types need. `live/MARKERS.md`, "Granting a GCP estate", inlines it.
Handover is the bindings moving from one principal to another; nothing on
the resources changes.

The fence binds the credential, not the binary. `gcloud` under a
principal without the binding is refused by GCP, and so is choudoufu's own
apply under the same principal. It covers reads and writes, because a
project binding is what every GCP call is checked against.

## What it does not fence

- Two estates sharing one project. Each principal's binding reaches the
  other estate's resources, and nothing GCP evaluates can tell them apart.
  Lint warns when one estate's google resources resolve to more than one
  project, since that is how a shared project usually starts.
- Anything finer than the estate. On AWS a condition on
  `aws:ResourceTag/tofu-address` can grant one address; on GCP the
  address label is a hash and IAM cannot read it anyway.
- Bindings above the project. A folder or organization binding inherits
  into the project and passes the fence, the way the account root passes
  an IAM condition on AWS.

Every type class is fenced the same way, at the project: the labelled
types, the declaration-carried skeleton and the instance templates alike
(`live/MARKERS.md`, "What this grant cannot reach on GCP").

## Later

Resource Manager tag bindings are the GCP carrier an IAM condition can
match. A tag binding is a separate resource, so carrying the marker that
way has to be squared with the no-synthesized-resources rule first. It is
ruled later, on #1121.
