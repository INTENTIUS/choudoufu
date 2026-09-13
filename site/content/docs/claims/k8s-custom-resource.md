---
title: "Claim 24: A custom resource binds by its natural key, carries the label, is swept by it, is refused by name while its CRD is missing, and carries the server's own dry-run verdict"
weight: 24
claim: k8s-custom-resource
---

# Claim 24: A custom resource binds by its natural key, carries the label, is swept by it, is refused by name while its CRD is missing, and carries the server's own dry-run verdict

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

The fourth unit is the refusal by name. The provider reads a custom kind's
schema from the cluster when it plans, so a block whose apiVersion and
kind the cluster does not serve fails at plan time with the provider's
own error. `choudoufu plan` asks the cluster first, at its first contact
with it (the same API discovery the sweep uses), and refuses such a block
by name: the address, the kind, the apiVersion, and the
CustomResourceDefinition whose group, kind and served version would have
to be installed. The plan exits non-zero with nothing planned, the same
outcome the provider's error gives, with the cause stated instead of
found. `live-check` is offline and cannot ask a cluster, so it does not
raise this; a cluster that cannot answer the question is a warning, never
a refusal. Step 1 plans before the CRD exists and requires exactly that
refusal.

The fifth unit is the server's own verdict
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 3).
Once the plan exists, every planned create or update of a
`kubernetes_manifest` instance is the API object itself, the `tofu-estate`
label already inside it, and the plan sends exactly that object to the
API server with `dryRun=All`: the server validates it against the kind's
schema, applies its defaults and runs every admission policy, the estate
boundary's included, and persists nothing. `kubectl --dry-run=server` is
the same request. AWS has no equivalent. The answer prints above the
plan, one line per object, and a rejection refuses the plan by name in
the server's words: the plan exits non-zero, nothing is rendered and
nothing is applied, on `plan` and on `apply` alike. Two bounds are
stated rather than hidden. An object whose namespace this same plan
creates is reported, not submitted, because the server would answer 404
for the apply's order rather than for the object; step 3 shows that line
and applies the namespace first. And a built-in type's block
(`kubernetes_namespace`, `kubernetes_config_map` and the rest) is never
submitted: the mapping from its block shape to the API object is the
provider's own, and this tool does not reproduce it. `live-check` is
offline and does not ask; a server that cannot answer is a warning.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-custom-resource

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-custom-resource and report the four "caught"
lines: the scenario writes spec.replicas = 0 under a CRD that bounds it
at minimum 1 and the replan must be refused by name in the server's
words; strips the tofu-estate label with kubectl and the replan must
propose restoring it; strips it again with the block removed and the
replan must not list the object; then deletes the custom resource and
the replan must propose creating it.
```

The steps, in the order they print:

1. `before the CRD exists, the block is refused by name` - the plan runs
   on a cluster that does not serve `stable.example.com/v1` `CronTab`,
   exits non-zero, and names `kubernetes_manifest.crontab`, the kind, the
   apiVersion and the CRD to install; no plan is produced.
2. `a CRD the cluster serves, installed with kubectl` - the CronTab CRD
   from the Kubernetes documentation, established before the estate plans,
   because the provider reads a custom kind's schema from the cluster.
3. `the namespace first: the server cannot judge an object in a namespace
   this same plan creates` - the plan of both blocks reports the CronTab
   as `[NOT SUBMITTED]`, naming the namespace it waits on; the namespace
   is applied on its own, 1 added.
4. `the plan asks the server first: the planned CronTab, dry run, nothing
   written` - `kubernetes_manifest.crontab [ACCEPTED] CronTab
   smoke-crd/my-crontab: create accepted by the server's admission, dry
   run, nothing written`, above `Plan: 1 to add`; kubectl confirms no
   CronTab exists.
5. `the estate applies: the custom resource, no state file` - 1 added;
   kubectl reads the CronTab's spec and its `tofu-estate` label back.
6. `the replan - prior state rebuilt from the cluster by the natural key` -
   empty.
7. `the cache is disposable` - the replan without it is still empty.
8. `the block is removed - the sweep finds the object by its label and
   the plan removes it` - `kubernetes_manifest.orphan_crontab_smoke-crd_my-crontab`
   is the one thing the plan proposes to destroy, and the apply destroys
   it; kubectl confirms.
9. `the block returns - the object is created again` - 1 added.
10. `destroy - exactly what was made` - the CronTab goes; the CRD, which
    nothing declared, stands.

The `BREAK=1` run has four controls after step 5. First it writes
`spec.replicas = 0` into the manifest; the CRD bounds the field at
minimum 1, a rule the provider does not check and the server does, so
the replan must be refused by name (`Kubernetes API server rejected the
planned object`), quoting the server's own `spec.replicas in body should
be greater than or equal to 1`, with no plan produced and nothing
written; the edit is then reverted. Then it strips the `tofu-estate`
label with kubectl; the replan must propose updating
`kubernetes_manifest.crontab` in place and the apply must put the label
back. Then it strips the label again and removes the block; the replan
must not list the object at all, because an object with no label is
nobody's. Then it deletes the CronTab; the replan must propose creating
it. If any plan read the other way, the label, the natural key or the
dry run was scenery.
