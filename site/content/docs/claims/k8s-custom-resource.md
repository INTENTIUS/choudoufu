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
label into `manifest.metadata.labels` on create, merged with whatever
labels the manifest declares. A manifest that names another estate is
refused as a marker conflict. kubectl reads the label back in step 2.
The provider's `computed_fields` default names `metadata.labels`, so a
label stripped out of band would be taken as the field's new truth. The
projection therefore carries the live object's own answer for that one
key into the prior state it builds, and the plan refuses the unmarked
object by name. The label `BREAK=1` control measures that.

The third unit is the sweep. It lists every kind the cluster serves with
list and delete verbs, CRDs included, selected on the estate label; a kind
the provider has no built-in type for is filed under `kubernetes_manifest`,
which manages any served kind. An object whose block is gone is proposed
for removal at `kubernetes_manifest.orphan_<kind>_<namespace>_<name>`,
and destroyed through the provider's own import of it. A block and a
listed object meet on the kind and the natural key, so a ConfigMap
declared through `kubernetes_manifest` is never an orphan of the built-in
type.

The fourth unit is the refusal by name. A block whose apiVersion and
kind the cluster does not serve would fail at plan time with the
provider's own error. `choudoufu plan` asks the cluster first, through
the same API discovery the sweep uses, and refuses such a block by name:
the address, the kind, the apiVersion, and the CustomResourceDefinition
that would have to be installed. The plan exits non-zero with nothing
planned. `live-check` is offline and does not raise this, and a cluster
that cannot answer is a warning. Step 1 plans before the CRD exists and
requires exactly that refusal.

The fifth unit is the server's own verdict
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 3).
Every planned create or update of a `kubernetes_manifest` instance is
sent to the API server with `dryRun=All`, the `tofu-estate` label
already inside it. The server validates the object against the kind's
schema, applies its defaults, runs every admission policy, and persists
nothing. `kubectl --dry-run=server` is the same request. The answer
prints above the plan, one line per object, and a rejection refuses the
plan by name in the server's words, on `plan` and on `apply` alike.

Two kinds of object are not submitted. An object whose namespace this
same plan creates is reported only, because the server would answer 404;
step 3 shows that line and applies the namespace first. A built-in
type's block (`kubernetes_namespace`, `kubernetes_config_map` and the
rest) is never submitted, because the mapping from its block shape to
the API object is the provider's own. `live-check` is offline and does
not ask, and a server that cannot answer is a warning.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-custom-resource

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-custom-resource and report the five "caught"
lines: the scenario writes spec.replicas = 0 under a CRD that bounds it
at minimum 1 and the replan must be refused by name in the server's
words; strips the tofu-estate label with kubectl and the replan must
refuse the CronTab by name, then have the server's own dry run refuse the
create it falls back to because the unowned object still holds the name,
leaving the label off until an operator writes it back; strips it again
with the block removed and the replan must not list the object; deletes
the custom resource and the replan must propose creating it; and installs
a MutatingAdmissionPolicy that rewrites `spec.image` on every update, after
which the migration must refuse the label write by name rather than send
it.
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
11. `migrate: a custom resource stock made, adopted by live-import` -
    plain `terraform apply` creates a second namespace and a CronTab and
    records both in a real `terraform.tfstate`, with no label on either.
    The same source with a `live` block on it is then adopted:
    `live-import` reads the state once, reports the CronTab's live id as
    `apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd-stock,name=adopted-crontab`
    rather than as untaggable, and `-approve` reports both instances newly
    stamped, with nothing failed and nothing skipped. kubectl reads
    `tofu-estate=smoke-crd-stock` on the custom resource, and its spec is
    untouched.
12. `the migrated estate replans empty, and the sweep can now see the
    adopted object` - the plan with no state file is empty, and deleting
    the adopted block proposes destroying exactly
    `kubernetes_manifest.orphan_crontab_smoke-crd-stock_adopted-crontab`.

Steps 11 and 12 are what
[#1109](https://github.com/INTENTIUS/choudoufu/issues/1109) closed. Before
it, the summary line read `1 newly stamped ... 1 skipped` and the CronTab
carried no label. A migrated custom resource was bound by its natural
key, counted as migrated, and left outside the boundary: the sweep did
not list it and the admission policy did not fence it. The label goes on
as one API merge patch under the caller's own credential. `kubernetes_manifest`
has no metadata block, so a labels-only write through the provider would
re-apply the whole manifest from a state file that may be days stale.

The `BREAK=1` run has five controls, all after step 5, and it exits
there. Steps 6 to 12 are the main run only. First it writes
`spec.replicas = 0` into the manifest. The CRD bounds the field at
minimum 1, a rule only the server checks, so the replan must be refused
by name (`Kubernetes API server rejected the planned object`), quoting
the server's own `spec.replicas in body should be greater than or equal
to 1`, with no plan produced and nothing written.

Then it strips the `tofu-estate` label with kubectl, and the replan must
refuse the CronTab by name and leave the label off until an operator
writes it back. Then it strips the label again and removes the block,
and the replan must not list the object at all, because an object with
no label is nobody's. Then it deletes the CronTab, and the replan must
propose creating it.

The fifth control is the one the migration's own safety rests on. It
stands up step 11's fixture itself, a CronTab stock made with a state
file behind it, and installs a `MutatingAdmissionPolicy` that rewrites
`spec.image` on every update to a CronTab. The label patch is sent first
with `dryRun=All`, and the object the server says it would store is
compared with the object it holds. The migration must refuse by name
(`would also change spec.image`), count the resource as failed, and
leave the object with no label and its original image. With the policy
removed the same command goes through in the main run.
