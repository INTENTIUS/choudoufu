---
title: "Claim 24: A custom resource binds by its natural key, carries the label and is swept by it"
weight: 24
claim: k8s-custom-resource
---

# Claim 24: A custom resource binds by its natural key, carries the label and is swept by it

Every custom resource is declared through `kubernetes_manifest`, whose
whole object is one dynamic `manifest` argument. The natural key
[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016) ruled on is
all there, authored in the configuration: `apiVersion`, `kind`,
`metadata.namespace` and `metadata.name` are four keys inside that
argument's object constructor. Identity resolution reads them out of the
constructor without evaluating the manifest (a local or variable the
argument is set to is walked the same way; a manifest computed by
`yamldecode(file(...))` is refused by name, because the key that names the
object is not known until the value exists) and renders the provider's own
import id, `apiVersion=...,kind=...,namespace=...,name=...`, so a replan
with nothing stored anywhere re-binds the object. This is the first unit
of [#1079](https://github.com/INTENTIUS/choudoufu/issues/1079).

The second unit is the label. The plan writes the one `tofu-estate`
label into `manifest.metadata.labels` on create, the same label every
built-in type carries in its metadata block, merged with whatever labels
the manifest declares (a manifest that names another estate is refused as
a marker conflict, word for word the refusal a tags map gets). kubectl
reads it back in step 2. The provider's `computed_fields` default names
`metadata.labels`, so a label the API server or a controller adds never
churns the plan; the same default means a label stripped out of band is
taken as the field's new truth, which is what the first `BREAK=1` control
measures.

The third unit is the sweep. It lists every kind the cluster serves with
list and delete verbs, CRDs included, selected on the estate label; a kind
the provider has no built-in type for is filed under `kubernetes_manifest`,
which manages any served kind. An object whose block is gone is proposed
for removal at `kubernetes_manifest.orphan_<kind>_<namespace>_<name>`,
and destroyed through the provider's own import of it. A block and a
listed object meet on the kind and the natural key, so a ConfigMap
declared through `kubernetes_manifest` is never an orphan of the built-in
type.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-custom-resource

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-custom-resource and report the three "caught"
lines: the scenario strips the tofu-estate label with kubectl and the
replan must propose restoring it; strips it again with the block removed
and the replan must not list the object; then deletes the custom
resource and the replan must propose creating it.
```

The steps, in the order they print:

1. `a CRD the cluster serves, installed with kubectl` - the CronTab CRD
   from the Kubernetes documentation, established before the estate plans,
   because the provider reads a custom kind's schema from the cluster.
2. `the estate applies: a namespace and a custom resource, no state file` -
   two objects; kubectl reads the CronTab's spec and its `tofu-estate`
   label back.
3. `the replan - prior state rebuilt from the cluster by the natural key` -
   empty.
4. `the cache is disposable` - the replan without it is still empty.
5. `the block is removed - the sweep finds the object by its label and
   the plan removes it` - `kubernetes_manifest.orphan_crontab_smoke-crd_my-crontab`
   is the one thing the plan proposes to destroy, and the apply destroys
   it; kubectl confirms.
6. `the block returns - the object is created again` - 1 added.
7. `destroy - exactly what was made` - the CronTab goes; the CRD, which
   nothing declared, stands.

The `BREAK=1` run has three controls after step 2. First it strips the
`tofu-estate` label with kubectl; the replan must propose updating
`kubernetes_manifest.crontab` in place and the apply must put the label
back. Then it strips the label again and removes the block; the replan
must not list the object at all, because an object with no label is
nobody's. Then it deletes the CronTab; the replan must propose creating
it. If any plan read the other way, the label or the natural key was
scenery.
