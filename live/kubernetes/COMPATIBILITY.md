# Compatibility

**Layout.** `live/COMPATIBILITY.md` is the substrate-neutral reference; this
page covers the same sections for Kubernetes only, and keeps only what is
Kubernetes-specific (ruled on
[#1602](https://github.com/INTENTIUS/choudoufu/issues/1602)). A section with
nothing substrate-specific to add says so in one line rather than dropping
it, which is what this page used to do.

```
choudoufu live-check
```

No cluster needed. On a root that declares Kubernetes resources it prints
one of two things per block.

## Your provider

The object-metadata rule and the manifest natural-key rule, which are what
this substrate's identity recovers from, are stated once in
`live/COMPATIBILITY.md`'s "Your provider" section rather than repeated
here: every type whose schema carries Kubernetes object metadata resolves
from `metadata.namespace`/`metadata.name`
([#1064](https://github.com/INTENTIUS/choudoufu/issues/1064)), and
`kubernetes_manifest` resolves from the natural key inside its manifest
([#1079](https://github.com/INTENTIUS/choudoufu/issues/1079)).

What that rule covers and does not is Your resource types, below.

## Your resource types

Every type whose schema carries Kubernetes object metadata: a `metadata`
block with a `name`, a `uid`, a `labels` map and, for a namespaced kind, a
`namespace`. That is the object-metadata rule
([#1064](https://github.com/INTENTIUS/choudoufu/issues/1064)): the
identity is `NAMESPACE/NAME`, or `NAME` for a cluster-scoped kind, read
from the block, and no type needs a row of its own to say so. The four
types that had ratified rows before the rule existed keep them as a check
on it. Counted against `live/MARKERS.md`'s figure for the provider at
3.2.1, that is nearly every resource type the provider ships.

`kubernetes_manifest`, and so every custom resource, plans
([#1079](https://github.com/INTENTIUS/choudoufu/issues/1079)). The object
carries the same one label as every built-in type. The plan writes
`tofu-estate` into `manifest.metadata.labels` on create, merged with any
labels the manifest declares, so the admission policy fences it like any
other object. The estate sweep lists every kind the cluster serves, CRDs
included, so an object whose block is removed is found by that label and
proposed for removal at `kubernetes_manifest.orphan_<kind>_<namespace>_<name>`
([claim 7](../smoke/claims/k8s-custom-resource.md)).

### Readiness tiers

Since [#1600](https://github.com/INTENTIUS/choudoufu/issues/1600) (merged),
`live/readiness.json` carries a `kubernetes` block alongside the AWS one.
It classifies the four ratified types -
`kubernetes_cluster_role_binding`, `kubernetes_config_map`,
`kubernetes_namespace` and `kubernetes_storage_class` - as
`marker-carried`, status `in-contract`: `metadata.labels` is this
substrate's marker, the role `tags` plays on AWS. Tier A's own test was a
top-level `tags` argument, a shape no `hashicorp/kubernetes` type has ever
had, and the ruling settled that a label-carried type maps to the same
tier a tag-carried one does.

The open-ended set the object-metadata rule reaches beyond those four has
no registry row, so `identity.AdmittedTypes()` does not enumerate it and
this table does not tally it. A type admitted only through the general
rule is neither counted nor excluded here; it is simply outside what this
generator can classify today.

### Refused

`metadata.generate_name` and a missing `namespace` on a namespaced kind
are refused rather than defaulted; the full rule is under Identity
arguments, below, alongside the other configuration-shape stops this
substrate adds to the ones AWS already has.

The six field-granular types plan
([#1191](https://github.com/INTENTIUS/choudoufu/issues/1191), ruled
2026-10-03): `kubernetes_labels`, `kubernetes_annotations`,
`kubernetes_env`, `kubernetes_config_map_v1_data`,
`kubernetes_secret_v1_data` and `kubernetes_node_taint`. They write some
fields of an object they do not own, by server-side apply, so they are
admitted by their own schema shape: a top-level `field_manager` and
`force`, and a `metadata` block that names the patched object rather than
being one. That shape is the predicate (`substrate.FieldGranularShape`),
not a list of names. The identity is the patched object:
`apiVersion=...,kind=...,[namespace=...,]name=...` for the three that
name their kind, `NAMESPACE/NAME` for the two data types and `NAME` for a
node's taints.

Their ownership marker is not a label. It is the field manager every
write is made under, `choudoufu:<estate>`: the plan sets `field_manager`
to it, and the API server's own `metadata.managedFields` then records,
per field, which estate wrote it. The patched object's own `tofu-estate`
label, if it has one, does not matter. So two estates can each own one
field of an object neither of them owns, which no whole-object type can
do. A live plan reads a field-granular instance under the estate's
manager and counts it as present only when that manager owns at least
one of its fields.

What the boundary does at plan time, after the plan exists and before
anything is applied:

| Plan | What happens |
|---|---|
| A write over a field another estate's manager owns | A warning naming that estate (`Field owned by another estate`). The API server refuses the apply with a 409 that names `choudoufu:<other>` |
| The same write with `force = true` | Refused by name, exit 1, nothing applied (`Force refused over another estate's field`). This is [#1106](https://github.com/INTENTIUS/choudoufu/issues/1106) section 3's control |
| `force = true` over a field kubectl, a controller or the provider's default `Terraform` manager owns | Planned and applied as stock does: force keeps its usual meaning against a manager that is not an estate's |
| Two field-granular blocks of one estate on one object | Refused (`Two field-granular blocks patch one object`). Both would write under the one manager, and server-side apply drops the fields a manager's next apply leaves out, so each would erase the other |
| A `field_manager` the configuration sets to anything but `choudoufu:<estate>` | Refused as an ownership marker conflict, the same refusal a hand-written `tofu-estate` naming another estate gets |

What is not done yet. A block removed from the configuration leaves its
fields on the object: nothing lists objects by field manager, so the
sweep does not find them and no destroy is proposed. A state migrated
from stock wrote under `Terraform`, so its first live plan proposes the
write again as a create, and the apply then shares each field with
`Terraform` rather than taking it over
([#1106](https://github.com/INTENTIUS/choudoufu/issues/1106) section 3's
migration item). The kind proof, `live/kubernetes/proof-ssa-conflict.sh`,
is written and has not been run.

`helm_release` is refused in a live root, and the refusal is the ordinary
unadmitted-type one
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 4;
ruled 2026-09-13 in
[#1105](https://github.com/INTENTIUS/choudoufu/issues/1105)).
hashicorp/helm 3.2.0 serves the type with no resource identity schema and
no object-metadata block, so neither admission route reaches it, and
`live-check` says so in those words. A release is more than one object:
it is a release secret in the release namespace plus whatever the chart
rendered. Those objects carry the chart's labels and Helm's own
`meta.helm.sh/release-name` annotation, and the estate's label is on none
of them, so the type stays refused.

Nothing about Helm is limited in stock OpenTofu, and this fork changes
nothing there: a root with no `live` block installs, upgrades and
uninstalls a `helm_release` exactly as stock does, with the release in the
state file and Helm's own release secret in the cluster. So a team on Helm
has two honest choices, and the trade between them is Helm's lifecycle
against ownership:

- **Keep the release root stock.** Put the Helm roots in a root of their
  own with no `live` block, beside the live estate. Helm keeps `helm
  rollback`, release history, hooks and chart-managed upgrades; the estate
  never claims the chart's objects, never sweeps them and never fences
  them, and `live-ls` does not show them. This is the default the ruling
  keeps.
- **Render the chart into manifests.** `helm template`, or the same
  provider's `helm_template` data source, renders the chart to YAML, and
  each object is declared as a `kubernetes_manifest` block. Every one of
  them is then admitted, labelled, swept, fenced and dry-run like any
  custom resource, and found again with no state file. What is given up is
  Helm's lifecycle: no rollback, no release history, no hooks, and an
  upgrade is a re-render and a plan.

A chart's own objects are never the estate's by accident. An object
carrying Helm's release annotation (`meta.helm.sh/release-name`) is
controller-held (ruled on
[#1604](https://github.com/INTENTIUS/choudoufu/issues/1604), built in
[#1607](https://github.com/INTENTIUS/choudoufu/issues/1607)): never
swept, never adopted, and reported with its release. So a `tofu-estate`
put in a chart's values no longer makes each rendered object an orphan.
The plan lists those objects under "Controller-held", each with the
release that holds it, and proposes destroying none of them. `live-ls`
lists them too, with a `held by: Helm release NAMESPACE/NAME` line
(`held_by` in `-json`). The label on them does nothing useful, so take it
out of the chart's values when you see that section. `helm template`
writes no release annotation, so a chart rendered into
`kubernetes_manifest` blocks is owned in the ordinary way. There is no
opt-in that brings a release inside the boundary: the ruling deferred it,
because a label written by a post-renderer is one an out-of-band
`helm upgrade` strips.

The annotation alone is not trusted forever: the sweep also checks that
the release it names still has a history record
(`sh.helm.release.v1.<name>.v*`, labelled `owner=helm,name=<name>`) in the
release's namespace, as a Secret or, under `HELM_DRIVER=configmap`, a
ConfigMap, and that the latest one is not `status=uninstalled`, which is
what `helm uninstall --keep-history` leaves
([#1625](https://github.com/INTENTIUS/choudoufu/issues/1625),
[#1738](https://github.com/INTENTIUS/choudoufu/issues/1738)). Held
means listed: the sweep reads the release's manifest from its latest
record and its last `deployed` one, and holds an object only if one of
them lists it, matched on kind, namespace and name (group and version are
not compared, and a document with no namespace is the release's). An
object annotated with a live release that its manifest does not list,
such as one a chart dropped under `helm.sh/resource-policy: keep` or a
copy of a Helm object's YAML, is not held and not destroyed either: the
plan warns `Annotated with a live Helm release, not in its manifest`,
naming it and the release, and `live-ls` lists it with `annotated with
live Helm release NAMESPACE/NAME, not in its manifest`
(`not_in_release_manifest` in `-json`). Remove its
`meta.helm.sh/release-name` annotation to let the sweep propose destroying
it. A record that cannot be read or decoded holds every object annotated
with its release. The sweep's credential therefore needs `list` on
Secrets and ConfigMaps in the namespaces your releases live in, and `get`
to read the manifest; the list is metadata-only, and only the one or two
records needed are fetched, once per release per run. A refused list is
reported as a denied gap for the kind being swept, and nothing in it is
proposed; a refused `get` holds, as the annotation alone did before.
Moving an object off Helm without re-creating it - adopting it into a
`kubernetes_manifest` block by import, then removing the release's
bookkeeping - leaves the annotation in place, because server-side apply
only touches fields its own writer claims. Once no release by that name
exists, the object is an ordinary `tofu-estate`-labelled object again:
swept, adoptable, and no longer reported under "Controller-held".

## How your configuration is written

### Expansion

No Kubernetes-specific case. `count` and `for_each` are expanded before
any provider-specific identity rule runs, so the same two phases -
data sources read first, and a sibling's own keys carrying across - apply
to a `kubernetes_*` resource exactly as they do to an `aws_*` one.

### `for_each` keys

Applies the same way as AWS, since
[#1639](https://github.com/INTENTIUS/choudoufu/issues/1639) (merged): the
block address goes on the object as an annotation,
`choudoufu.intentius.io/tofu-address`, "escaped exactly as the AWS
`tofu-address` tag value is" (`live/MARKERS.md`, "Kubernetes: one label"),
so an instance key becomes part of it through the same
`internal/live/markerkey` rule - the same runes need no escaping, the same
six are excluded. One difference: an annotation value has no length cap,
so there is no continuation-key splitting the way a long AWS address
needs `tofu-address-2` through `tofu-address-4`.

### Identity arguments

A namespace read from another resource's metadata, `namespace =
kubernetes_namespace.x.metadata[0].name`, resolves: the name is that
resource's identity attribute, and the reference is followed into the
block. So does `namespace = kubernetes_namespace_v1.x.id`, the shape
grafana/quickpizza's root writes on every namespaced object: the
provider sets an object's `id` to its own import id (the name for a
cluster-scoped kind, `NAMESPACE/NAME` for a namespaced one), so the rule
claims `id` as an identity attribute and the reference resolves to the
parent's whole identity ([#1067](https://github.com/INTENTIUS/choudoufu/issues/1067)).

A `kubernetes_secret_v1` whose `data` keys read sensitive variables plans
empty after adoption. The same root found the case where it did not: the
provider's schema marks the whole `data` map sensitive, the configuration
marks each key inside it, and a prior read from the cluster carries only
the schema's mark, so the planner's sensitivity comparison saw a
difference on every run and proposed an in-place update it rendered as
unchanged. The comparison now reduces both sides to their minimal cover
first: a mark under an already-marked ancestor is not a change.

Two more shapes stop here, both narrower than the general "argument is
not set" AWS already documents:

| Written like this | Why it stops |
|---|---|
| `metadata.generate_name` set | the API server mints the object's name at create time, so nothing in the configuration states the join key back to the block - the one shape that would need the configuration address on the object. Set `metadata.name` instead |
| A namespaced kind with no `namespace` | refused rather than defaulted to `default`, for the same reason: a resolver that guessed would fabricate an identity the configuration never stated |
| A `kubernetes_manifest` whose `manifest` is computed some other way, `yamldecode(file(...))` or a module output | the key is read without evaluating the manifest, so a value that does not exist as a literal object constructor yet has no key to read |

An identity argument the static evaluator cannot resolve at all - reading
a sibling's non-identity attribute, the shape AWS calls
`identity.ClassNeedsDiscovery` - used to be an outright node-level
refusal on this substrate, because the marker carried no address to bind
the object it might already own. Since the address annotation
([#1639](https://github.com/INTENTIUS/choudoufu/issues/1639)) and the
sweep that binds on it
([#1640](https://github.com/INTENTIUS/choudoufu/issues/1640)), ruled on
[#1539](https://github.com/INTENTIUS/choudoufu/issues/1539) (2026-09-26):
if the sweep found no object of that kind carrying this estate's label
and no such annotation, the create is planned; if it found one without
the annotation, one whose annotation names the block but did not bind
to it, or could not list every kind the type can declare, the static
refusal stands, naming the object -
`live/LIMITATIONS.md`'s "Identity not resolvable, and the marker carries
no address" has the message. Two annotated objects both claiming one
block - the crash window a `create_before_destroy` replacement leaves -
is the AWS collision refusal, naming both objects, and destroys neither
until an operator picks one. The annotation is compared with the block's
escaped address the way a `tofu-address` tag is, so a `for_each` key made
of digits (`x["0"]`, stamped `x:0`) binds, and so does an address a
`moved` block retired
([#1737](https://github.com/INTENTIUS/choudoufu/issues/1737)).

## Your modules

The rules for `count` and `for_each` on a module call, and for a module
call's own `providers` mapping, are OpenTofu-level and provider-agnostic;
`live/COMPATIBILITY.md`'s "Your modules" section applies to a module
holding `kubernetes_*` resources exactly as written.

### Crossing a module boundary

The block address does go on a Kubernetes object now, beside the estate
label, as the `choudoufu.intentius.io/tofu-address` annotation
([#1639](https://github.com/INTENTIUS/choudoufu/issues/1639), merged), so
`live-mv` writes more here than it used to. On a metadata-block type, a
rename within an estate rewrites the annotation in place through the
provider, the same way an AWS rename rewrites the `tofu-address` tag, and
`-from-estate` rewrites the `tofu-estate` label and the address annotation
together, so the admission policy judges it like any other write
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)). On the
manifest shape, a same-estate rename is one annotation merge patch under
the run's own credential (`internal/live/mv/manifest.go`), and a
cross-estate move is the same patch carrying the `tofu-estate` label too
([#1104](https://github.com/INTENTIUS/choudoufu/issues/1104)): sent first
with `dryRun=All` (under `-dry-run` as well, so the admission verdict
prints before anything is written), refused if the server's answer changes
anything beyond the two markers, and written under the block's own field
manager, which then owns the label in its Apply entry.

`choudoufu live-import` traverses every managed resource instance in the
whole state, root and child modules alike, for this substrate the same
way it does for AWS's.

## Your accounts and regions

Every Kubernetes type's identity resolves from the block's own
`metadata.namespace`/`metadata.name`, or a manifest's natural key, in the
ordinary case - no live list against the cluster needed. Where that
static resolution fails outright, `metadata.generate_name` is refused
unconditionally, with no fallback (Identity arguments, above); where only
one *argument* fails to resolve, the plan-node fallback described there
(the address annotation and the sweep that binds on it,
[#1605](https://github.com/INTENTIUS/choudoufu/issues/1605)) is a
same-estate, same-sweep affair, scoped to whichever one provider
configuration that block's own resources already use - it does not ask a
second cluster or context anything. So AWS's split between client-named
types, which span configurations freely, and server-assigned types, which
must share one, still collapses here in the sense that matters for this
section: nothing about a Kubernetes type demands sharing a provider
configuration with another one, the way an AWS server-assigned type's
cross-configuration discovery does.

One `provider "kubernetes" {}` block per cluster or context, resources
pinned with the `provider` meta-argument, spans freely for the same
reason. A module call's `providers` mapping
([#188](https://github.com/INTENTIUS/choudoufu/issues/188)) is honoured
for a Kubernetes provider alias exactly as for an AWS one, since
`internal/live/providerscope`'s walk is provider-agnostic. The estate
sweep runs once per provider configuration, listing that cluster's own
objects by the `tofu-estate` label, independently of any other cluster
an estate's other resources might use.

### Mixed estates

The common shape is an EKS module that also manages the `aws-auth`
ConfigMap. The AWS resources carry two tags and fall under your IAM; the
ConfigMap carries the one label and falls under the cluster's admission
policy, and each substrate's sweep lists its own. `live-check` reports
that root as not blocked.

EKS creates `aws-auth` itself, so the first plan of such a root reads an
object at `kube-system/aws-auth` carrying no `tofu-estate` label. Under
`declared_untagged`'s default the plan stops there with "Unlabelled live
object holds the declared name" and exit 1, because the create it would
otherwise propose is one the API server answers with 409 (#1546).
`policy { declared_untagged = "adopt" }`, or writing the label by hand,
adopts it; a plan where no such object exists still proposes the create.
A root made only of refused types is blocked as a whole, and the report
says which root and why.

**Objects EKS also writes.** EKS writes `aws-auth`, CoreDNS, kube-proxy
and the managed add-ons' Deployments with its own field managers. An
undeclared one carries no `tofu-estate` label, and the sweep selects on
that label, so no plan lists or proposes it. A declared one belongs to the
operator, as it does in stock (ruled on
[#1113](https://github.com/INTENTIUS/choudoufu/issues/1113)): choudoufu
writes what the block says, EKS writes what EKS writes, and where the two
meet in one field (`aws-auth`'s `mapRoles` is the usual case) they contend
exactly as they do under stock OpenTofu. No list of EKS field-manager
names is consulted. To keep one writer, grant cluster access with
`aws_eks_access_entry` (and `aws_eks_access_policy_association`) instead of
declaring `aws-auth`. An access entry is an AWS resource only the EKS API
writes and carries the estate's tags like any other, and the ConfigMap
stays EKS's alone.

**A provider block that reads the cluster.** Every published EKS root
configures `provider "kubernetes"` from the cluster it creates:
`host = aws_eks_cluster.this.endpoint`, `host =
module.eks.cluster_endpoint`, or through `data.aws_eks_cluster`, with a
token from `data.aws_eks_cluster_auth` or an `exec` block. Since #1113
each of these is answered from the live cluster, read before the plan, the
value stock's graph supplies once the cluster exists. A cluster that does
not exist yet reads as empty: a greenfield plan's cluster leg is all
creates, and the apply configures the provider once the cluster is there,
which is stock's order. A cluster that exists and cannot be read stops the
plan with `Cannot read a value a provider configuration needs`. An
unreachable cluster is never reported as an empty one.

**Two fences, one estate.** An estate spanning AWS and the cluster has one
name and two fences. IAM conditions on the `tofu-estate` and
`tofu-address` tags (`live/MARKERS.md`) govern the AWS leg. The
ValidatingAdmissionPolicy in `live/kubernetes/estate-boundary.yaml`
governs writes to the cluster leg, per principal. Each is judged under its
own credential, and nothing makes the two agree for you: a principal
fenced out of the AWS leg is not fenced out of the cluster unless the
admission policy says so as well. On EKS one AWS identity usually holds
both, because the cluster credential is minted from it through an access
entry or `aws-auth`. A carve across both legs is a sequence of
`live-mv -from-estate` calls, one per object, each judged by its own
fence. No move spans both legs atomically. A carve interrupted halfway
resumes by running the moves that have not happened yet. Re-running one
that already ran is refused as `new_address_claimed` (live-mv's own code
for a destination something already carries), so nothing is moved twice
or overwritten.
What `live-whoami` prints for a mixed estate is
[#1106](https://github.com/INTENTIUS/choudoufu/issues/1106)'s, and not
built yet.

## How you run it

The command-level refusals - a `backend` or `cloud` block, a non-default
workspace, every `tofu state` subcommand, `import`/`refresh`/`taint`/
`untaint`, the saved-plan re-check, the flags list - are tool-level and
provider-agnostic; `live/COMPATIBILITY.md`'s "How you run it" section
applies to a Kubernetes root exactly as written.

What's Kubernetes-only is what happens when the plan reaches the cluster.
A block whose apiVersion and kind the cluster does not serve is refused by
name at the plan's first contact with the cluster (`Kubernetes kind not
served by the cluster`), naming the CRD to install. `live-check` is
offline and does not raise this, and a cluster that cannot answer is a
warning.

Once the plan exists, every planned create or update of a
`kubernetes_manifest` instance is sent to the API server as the apply
would write it, label included, with `dryRun=All`
([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081), item 3).
The server validates it against the kind's schema, applies its defaults
and runs every admission policy, and persists nothing. The answer prints
above the plan, one line per object. A rejection is `Kubernetes API
server rejected the planned object`, quoting the server, and the run
stops with nothing rendered and nothing applied, on `plan` and on
`apply` alike.

This reaches the manifest shape only. A built-in type's block is not
submitted. An object whose namespace the same plan creates is reported
rather than submitted, and a server that cannot answer is a warning.
`live-check` does not ask.

### Known differences (fixed)

A replacement under `create_before_destroy` that also changed the
object's name used to be destroy-then-create here, where stock is
create-then-destroy: with no address on the object, a renamed block
resolved to a new object at its new name and bound nothing, so the sweep
found the old object by its label alone and filed it at a synthetic
orphan address, leaving two unrelated changes with no edge between them
for `create_before_destroy` to order.

[#1605](https://github.com/INTENTIUS/choudoufu/issues/1605)'s ruling of
2026-09-26 fixed it by putting the address on the object, as the
`choudoufu.intentius.io/tofu-address` annotation
([#1639](https://github.com/INTENTIUS/choudoufu/issues/1639)); the estate
sweep binds an object to its declared block through that annotation
([#1640](https://github.com/INTENTIUS/choudoufu/issues/1640)); and
`day2_replace` now runs on the kind substrate
(`tools/gauntlet/stages.go`, [#1684](https://github.com/INTENTIUS/choudoufu/issues/1684)/[#1641](https://github.com/INTENTIUS/choudoufu/issues/1641)),
proving the content-hashed-name case: `name = "cfg-${sha}"` with
`create_before_destroy` now creates `cfg-b` before destroying `cfg-a`,
the same order stock and AWS both use, so a Deployment mounting `cfg-a`
never sees the name gone. A replacement that keeps its name is
destroy-then-create on either tool and always was; that is not this
case. Tracked as [#1541](https://github.com/INTENTIUS/choudoufu/issues/1541),
closed by the above.

## Constructs this page used to refuse, and no longer does

Every `kubernetes_*` type, and `kubernetes_manifest` with it, used to be
refused outright as `unadmitted-type`: before
[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016)'s ruling that
Kubernetes is a substrate in its own right, this fork had no way to
recover a Kubernetes object's identity at all.
[#1064](https://github.com/INTENTIUS/choudoufu/issues/1064) admitted the
object-metadata rule and
[#1079](https://github.com/INTENTIUS/choudoufu/issues/1079) admitted
`kubernetes_manifest`. Nothing else on `live/COMPATIBILITY.md`'s list for
this section - provisioners, `terraform_remote_state`, `moved` blocks,
`random_password`/`tls_*`, `local_file`, `module { count = ... }` - is a
Kubernetes-specific history; each was an OpenTofu-level or a
non-provider-type construct, and the AWS-side history applies to a
Kubernetes root unchanged.

## Effects do work

No Kubernetes-specific case. `null_resource`, `terraform_data`, `time_*`
and the `random_*` family carry no substrate identity of their own - they
are `RECORD_ADMITTED` on either provider - so a Kubernetes root gets the
same lifecycle against the same record store that
`live/COMPATIBILITY.md`'s "Effects do work" describes for AWS.

## Two hazards that are now refusals

The tag hazard `live/COMPATIBILITY.md` describes has a Kubernetes
instantiation, ruled the same way and refused the same way: since
[#1645](https://github.com/INTENTIUS/choudoufu/issues/1645) (merged),
`lifecycle { ignore_changes = ... }` over this substrate's marker surface
is refused too. Before that fix, `checkIgnoreChanges`
([#103](https://github.com/INTENTIUS/choudoufu/issues/103)) refused the
AWS `tags` surface but returned early on any type that is not
`markers.Taggable` - every Kubernetes type - so `ignore_changes = all`,
`ignore_changes = [metadata]`, `ignore_changes = [metadata[0].labels]`
and `ignore_changes = [metadata[0].labels["tofu-estate"]]` on a
`kubernetes_config_map`, and the manifest-surface equivalents
(`ignore_changes = [manifest]`, `ignore_changes =
[manifest.metadata.labels]`) on a `kubernetes_manifest`, all passed lint
silently. All of those are refused now, naming the ignored construct and
the label this mode writes underneath it. Ignoring a label key of your
own, such as `metadata[0].labels["team"]`, stays admitted - the
over-refusal guard AWS's `tags["Owner"]` case already has. Since
[#1740](https://github.com/INTENTIUS/choudoufu/issues/1740) the same
refusal covers the address annotation (`metadata[0].annotations`,
`manifest.metadata.annotations`, or the
`choudoufu.intentius.io/tofu-address` key in either), and compares paths
step by step, so index spellings such as `metadata[0]["labels"]` or
`manifest["metadata"]` are refused as their dotted forms are.

The second hazard, a module call's child-side `providers` mapping naming
an alias nothing resolves, is OpenTofu-level and provider-agnostic:
`live/COMPATIBILITY.md`'s description applies to a Kubernetes provider
alias exactly as written.

A third, this substrate's own: the stateful un-migration guard
(`internal/command/live_unmigrate_guard.go`, issue #613) refuses a
state-backed plan that would drop this fork's ownership marker from a
live object. Until [#1649](https://github.com/INTENTIUS/choudoufu/issues/1649)
(merged) it read only the AWS tags map, so a state-backed plan dropping
`tofu-estate` from `metadata.labels` (or `manifest.metadata.labels`) un-migrated
the object with no refusal. It now reads every marker surface, tags,
`metadata.labels` and `manifest.metadata.labels` alike, and refuses the
same way on any of them, with the `UnmigrateEnvVar` escape unchanged.

## Editors and linters

No Kubernetes-specific case. The `estate.chdf.hcl` sidecar and the
in-`terraform` `live` block are configuration-file concerns independent
of which provider a root declares; `live/COMPATIBILITY.md`'s "Editors and
linters" section applies to a Kubernetes root exactly as written.
