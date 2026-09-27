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
([claim 24](../smoke/claims/k8s-custom-resource.md)).

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

Still refused: the handful of types whose block is not object metadata
(`kubernetes_labels`, `kubernetes_annotations`, `kubernetes_env`, the
`*_data` patch types), which act on an object rather than being one.

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

## How your configuration is written

### Expansion

No Kubernetes-specific case. `count` and `for_each` are expanded before
any provider-specific identity rule runs, so the same two phases -
data sources read first, and a sibling's own keys carrying across - apply
to a `kubernetes_*` resource exactly as they do to an `aws_*` one.

### `for_each` keys

Does not apply. An instance key is escaped into the `tofu-address` marker
on AWS, and a Kubernetes object's marker carries no address at all - only
the estate name (`live/MARKERS.md`, "Kubernetes: one label"). With no
address to build, there is no per-instance key to escape into one, so this
whole subsection has nothing to say for this substrate.

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

## Your modules

The rules for `count` and `for_each` on a module call, and for a module
call's own `providers` mapping, are OpenTofu-level and provider-agnostic;
`live/COMPATIBILITY.md`'s "Your modules" section applies to a module
holding `kubernetes_*` resources exactly as written.

### Crossing a module boundary

A Kubernetes object's marker carries no address, only the estate name
(`live/MARKERS.md`, "Kubernetes: one label"), so `live-mv`'s role here is
narrower than on AWS. `live-mv` runs on every object-metadata type: a
rename within an estate reports nothing to write and exits 0, and
`-from-estate` rewrites the `tofu-estate` label through the provider under
your own credential, so the admission policy judges it like any other
write ([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)); a
move of a `kubernetes_manifest` object is refused by name with the
equivalent `kubectl label`.

`choudoufu live-import` traverses every managed resource instance in the
whole state, root and child modules alike, for this substrate the same
way it does for AWS's.

## Your accounts and regions

Every Kubernetes type's identity resolves from the block's own
`metadata.namespace`/`metadata.name`, or a manifest's natural key, never
from a live list against the cluster - the shape AWS calls
`identity.ClassNeedsDiscovery` is exactly what
[#1016](https://github.com/INTENTIUS/choudoufu/issues/1016) refuses to
bring back for this substrate (`metadata.generate_name`, above, is that
refusal). So AWS's split between client-named types, which span
configurations freely, and server-assigned types, which must share one,
collapses here: every Kubernetes type is client-named, and none of them
trips the "one bound" restriction `live/COMPATIBILITY.md`'s "Your
accounts and regions" describes.

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

### Known differences

A replacement under `create_before_destroy` that also changes the
object's name is destroy-then-create here, where stock is
create-then-destroy. The label carries no address, so a renamed block
resolves to a new object at its new name and binds nothing; the sweep
finds the old object by its label and files it at a synthetic orphan
address, leaving two unrelated changes with no edge between them for
`create_before_destroy` to order. On AWS the marker names the block's
address, so the same edit is a replace and the lifecycle holds.

The case this bites is a content-hashed ConfigMap kept alive across a
rollout: `name = "cfg-${sha}"` with `create_before_destroy`, so a
Deployment can roll onto the new copy before the old one goes. Applying a
changed `sha` here destroys `cfg-a` before creating `cfg-b`; for the
length of that window neither object exists, and a Deployment whose pods
mount `cfg-a` sees it gone before `cfg-b` exists. Both sides converge on
the same object and the next plan is empty either way.

Tracked as [#1541](https://github.com/INTENTIUS/choudoufu/issues/1541).
The fix waits on [#1605](https://github.com/INTENTIUS/choudoufu/issues/1605)
(should the address ride an annotation, which would make a rename a
replace again); until then `day2_replace` stays `n/a` on the kind
substrate for this case (`tools/gauntlet/stages.go`, `live/GAUNTLET.md`).
[#1684](https://github.com/INTENTIUS/choudoufu/issues/1684) is open and
would settle the address-carrying question; this note stays until it
merges.

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
over-refusal guard AWS's `tags["Owner"]` case already has.

The second hazard, a module call's child-side `providers` mapping naming
an alias nothing resolves, is OpenTofu-level and provider-agnostic:
`live/COMPATIBILITY.md`'s description applies to a Kubernetes provider
alias exactly as written.

## Editors and linters

No Kubernetes-specific case. The `estate.chdf.hcl` sidecar and the
in-`terraform` `live` block are configuration-file concerns independent
of which provider a root declares; `live/COMPATIBILITY.md`'s "Editors and
linters" section applies to a Kubernetes root exactly as written.
