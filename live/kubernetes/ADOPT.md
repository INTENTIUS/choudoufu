# Adopt

## Today

Every type whose schema carries object metadata resolves through one rule
([#1064](https://github.com/INTENTIUS/choudoufu/issues/1064)): its
identity is `metadata.namespace` and `metadata.name`, both written in your
configuration, so a plan binds it with nothing stored anywhere. A missing
namespace is refused rather than defaulted, and `generate_name` is refused
by name. A custom resource, declared through `kubernetes_manifest`, binds
the same way by the `apiVersion`, `kind`, `metadata.namespace` and
`metadata.name` written inside its manifest
([#1079](https://github.com/INTENTIUS/choudoufu/issues/1079)); a block
whose kind the cluster does not serve is refused by name, naming the CRD
to install, at the plan's first contact with the cluster ([claim
24](../smoke/claims/k8s-custom-resource.md)).

Every one of them carries the marker: one label, `tofu-estate`, written on
the create. Strip it with kubectl and the next plan proposes restoring it.
[Claim 7 on Kubernetes](../smoke/claims/identity-is-a-tag.md#on-kubernetes) runs that on a
real cluster with a namespace, a ConfigMap, a ServiceAccount and a Service.

Delete one of those blocks from source and the next plan finds the live
object by its label, one cluster-wide list per kind, and proposes its
removal, with a controller's copies of the label excluded first ([claim 1
on Kubernetes](../smoke/claims/no-silent-orphans.md#on-kubernetes)). And once a
cluster admin has installed the one admission policy, every write to one
of them is fenced by the label it carries ([claim 13 on
Kubernetes](../smoke/claims/the-tag-is-the-boundary.md#on-kubernetes); [the
gate](https://intentius.io/choudoufu/kubernetes/gate/) says what that fence does not
reach).

## From a stock state file

```
choudoufu live-import -approve
```

The same bulk path as on AWS ([#1073](https://github.com/INTENTIUS/choudoufu/issues/1073)):
the stock state is read once, each object is verified by namespace and
name, and the `tofu-estate` label is written into `metadata.labels`
through a labels-only plan and apply. A plan that would also rename the
object, move it between namespaces or change anything outside the labels
map is refused, and so is an object already labelled for another estate.
Then plan with the state file out of the way: the plan is empty, because
every object is found again by its name and carries the label. The gauntlet's
`reference-k8s` estate measures exactly this at its `migrate` and
`test_plan` stages.

A custom resource in that state file comes with it
([#1109](https://github.com/INTENTIUS/choudoufu/issues/1109)). Its label
is written as one API merge patch under your own credential, because
`kubernetes_manifest` has no metadata block to write into and a
labels-only write through the provider would re-apply the whole manifest.
The patch is sent first with `dryRun=All`, and the object the server would
have stored is compared with the object it holds now. If anything outside
the labels map moved, as it would under a mutating webhook that rewrites
the spec, the write is refused by name and nothing is sent.

If the stock state lives in the `kubernetes` backend, there is no file to
delete. The state is a Secret named `tfstate-<workspace>-<secret_suffix>`,
`tfstate-default-<secret_suffix>` on the default workspace, with the Lease
`lock-tfstate-<workspace>-<secret_suffix>` beside it (the format is
`internal/backend/remote-state/kubernetes`'s own, `client.go`'s
`createSecretName`). `tofu state pull > stock.tfstate` gives `live-import`
its file. Keep the Secret until you trust the migration, since it is the way
back to stock: point a stock `backend "kubernetes"` at it and the estate runs
as it did. Delete it last, after the plan has been empty for as long as you
need it to be.

The migration also carries over what the state knew about a custom resource's
labels. `live-import -approve` seeds a `kubernetes_manifest` entry's declared
label and annotation keys from the stock state into the estate's record
([#1391](https://github.com/INTENTIUS/choudoufu/issues/1391)), so a label you
remove from the configuration after migrating is planned for removal, the way
stock would plan it
([claim 27](../smoke/claims/k8s-a-label-is-a-change.md)). Nothing the record
needs is left in the state after that, so the Secret is kept as the way back
and not because a plan still reads it.

## The marker

On AWS the marker carries the config address, because AWS hands back opaque
ids and the tag is the only way from a live object back to a line of
configuration. Kubernetes returns the natural key: group, kind, namespace and
name, with the name authored in the configuration this fork already parses.
So the address does not need to be on the object.

The marker is one label, `tofu-estate`, and re-binding goes through the
natural key. Measured against the identity golden set, nearly half of real config
addresses are illegal as a label value and a 63-character cap binds at once
on ordinary module-nested shapes; putting the address in a label would need
three or four continuation labels per object and would break the exact-match
condition a policy wants. An estate-only label fits by construction.

## Refused, not guessed

`generateName` lets the server mint the name, which makes the natural key
unknowable before the create. That is the one shape that would drag the
whole address-in-label machinery back in, so it is refused, the same way a
missing namespace is.

Controller-created objects, ReplicaSets and Pods from a Deployment, PVCs
from a volumeClaimTemplate, Jobs from a CronJob, are excluded by a non-empty
`metadata.ownerReferences` before anything reaches a delete. The author
chose neither the name nor the object.

## What Kubernetes does better

`metadata.managedFields` records which manager last wrote each field, so a
stripped label names who stripped it. Server-side apply refuses a contested
field with a 409 that names the competing manager. Server-side dry run
validates, defaults and runs admission without persisting, which is stronger
evidence than a locally computed plan and something AWS has no equivalent
for; the plan uses it, sending every planned `kubernetes_manifest` create or
update to the server with `dryRun=All` and printing the server's answer
above the plan, and a rejection refuses the plan by name in the server's
words before anything is applied ([claim
24](../smoke/claims/k8s-custom-resource.md)). Built-in types are
not submitted: the mapping from their block shape to the API object is the
provider's own.
