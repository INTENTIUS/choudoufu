# Marker spec

Spec version 1

This file is the format for live resource markers, the ownership records
this fork keeps on resources instead of in a state file. It is the only
integration surface external tools rely on: this document is a contract,
and nothing else about the mode's internals is.

A marker is an ownership record carried on the resource itself, as AWS
resource tags, or on Kubernetes as one label (see "Kubernetes: one label").
There is no side channel, no registry, and no shared library. If a tool
reads and writes these tags according to the grammar below, it can identify,
adopt, and safely modify resources that belong to a marker-managed estate,
with no dependency on OpenTofu itself.

## Tag keys

Three tag keys are defined, plus an optional fourth family. All are plain
resource tags, stamped on every taggable resource the mode manages.

| Key | Meaning | Present on |
|---|---|---|
| `tofu-estate` | The estate that owns the resource. | Every managed resource. |
| `tofu-address` | The resource's canonical config address, or its first chunk. | Every managed resource. |
| `tofu-address-2`, `tofu-address-3`, `tofu-address-4` | The rest of `tofu-address`, in order, when it does not fit in one tag. | Only a resource whose escaped address is longer than one tag value. |
| `tofu-slot` | A stable, opaque cardinality slot. | `count` instances of a set whose members the configuration does not itself tell apart. |

A resource carrying `tofu-estate` and `tofu-address` is fully identified by
an estate and a place within that estate's configuration. `tofu-slot` is
additional information layered on top for resources that come from a
`count` block, and only for some of those - see "Which count instances
carry one" below. It does not replace `tofu-address`, which still carries the
full indexed address (see below). `tofu-address-2` through `tofu-address-4`
are additional information of a different kind: they do not stand on their
own, and exist only to carry the rest of a `tofu-address` value one tag
could not hold. See "tofu-address continuation tags" below.

## AWS tag constraints

These limits come from AWS, and everything
below is designed to fit inside them.

- A tag value holds at most 256 Unicode characters.
- A tag key holds at most 128 Unicode characters. This is not a concern
  here, since all three keys are short, fixed strings.
- A value may contain letters and numbers representable in UTF-8, space,
  and the characters `+ - = . _ : / @`. No other punctuation, including
  `[`, `]`, and `"`, is permitted by AWS in a tag value.

A canonical OpenTofu resource address uses `[` `]` and `"` to express
instance keys (`aws_subnet.this["a"]`, `aws_eip.this[2]`), all three of
which are outside the AWS-allowed set. `tofu-address` values therefore go
through the escaping rule below before being written as a tag.

## Kubernetes: one label

Everything above and below this section describes the AWS shape. On
Kubernetes (GitHub issue #1061, under #1016's ruling of 2026-09-11) the
marker is a single label:

| Label | Meaning | Present on |
|---|---|---|
| `tofu-estate` | The estate that owns the object. | Every managed object whose type has a `metadata` block with a `labels` map (75 of hashicorp/kubernetes 3.2.1's 82 types; `kubernetes_manifest` is not one). Since #1064 the same block is what admits the type: 73 of the 82 carry the full object-metadata shape, 48 namespaced and 25 cluster-scoped, and resolve to NAMESPACE/NAME or NAME with no row each. |

There is no `tofu-address`, no continuation label and no `tofu-slot`. The
object's own group, kind, namespace and name are the join key back to the
configuration block that declares it, because those are authored in the
configuration this fork already parses; the address never goes on the
object. The provider's `id` on such an object is that same join key (the
name for a cluster-scoped kind, `NAMESPACE/NAME` for a namespaced one), so
a sibling reading `kubernetes_namespace_v1.x.id` reads the parent's whole
identity and resolves; grafana/quickpizza's root, the kubernetes lane's
first published estate, writes exactly that on every namespaced object
(#1067). #1016 measured the alternative: nearly half of real addresses are
illegal as a label value (the instance-key `:`), and a 63-character cap
binds at once on ordinary module-nested shapes.

A label value is at most 63 characters and matches
`(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?`. An estate name over 63
characters, or one ending in a hyphen, is a legal estate and an illegal
label value; a run refuses to stamp such an estate onto a Kubernetes
resource ("Ownership marker is not a legal label value") rather than
writing something the API server rejects.

The label is written into `metadata.labels` as part of the create, so a
created object carries it; a label stripped out of band shows in the next
plan as an in-place change restoring it, under the `marker_repair` default.
A configuration that sets `tofu-estate` to another estate's name is the
same "Ownership marker conflict" refusal the AWS shape raises. `strict {
markers "record" }` withholds the label the same way it withholds the tags,
and protects an existing one through `ignore_changes` the same way.

Migrating from a stock state file is the same bulk path as on AWS
(#1073): `choudoufu live-import -approve` reads the state once, verifies
each object by namespace and name, and writes the `tofu-estate` label into
`metadata.labels` through a labels-only plan and apply, judged the way a
tags-only write is judged - a plan that would also rename the object, move
it between namespaces or change anything outside the labels map is
refused, as is an object already labelled for another estate, and an
estate name that is not a legal label value. There is no address to split
and no `tofu-slot` to settle, so a Kubernetes count set is never
slot-classified. Before this the label surface was not a live-import
carrier and every `kubernetes_*` type migrated as UNTAGGABLE; the
kubernetes lane's first estate (reference-k8s, #1067) failed its migrate
stage on exactly that line, and passes it now.

A `kubernetes_manifest` block, the shape every custom resource is declared
through, is identified the same way (#1079's first unit): the natural key
is four keys inside its `manifest` argument's object constructor, read
through `Component.Path` in `internal/live/identity/manifest.go` and
rendered as the provider's documented import id,
`apiVersion=...,kind=...,[namespace=...,]name=...`. The projection seeds
the manifest from the configuration before the read, because the
provider's import never returns it. It carries the same one label
(#1079's second unit): the node stamp writes `tofu-estate` into
`manifest.metadata.labels` on create, in the object constructor's own
shape, merged with any labels the configuration declares and refused as
the same marker conflict when the configuration names another estate
(`internal/live/projection/nodestamp_manifest.go`). The seed the
projection hands the provider for a cache-less read is stamped the same
way, or the provider - whose `computed_fields` default names
`metadata.labels` - would plan the label as a change on every such run.
That default also means the provider takes a label stripped out of band
as the new truth of the field; claim 24's control measures what the plan
does about that (the projection mirrors the live object's marker into the
prior it builds, so a stripped label plans as the update that restores
it). The sweep lists every kind the cluster serves (#1079's third unit,
below), so an orphaned custom resource is proposed for removal like any
other. A block whose apiVersion and kind the cluster does not serve is
refused by name at the plan's first cluster contact (#1079's fourth unit,
`internal/live/discovery/kubernetes.go`), naming the block, the kind,
the apiVersion and the CRD to install; `live-check` is offline and cannot
ask.

`generateName` is refused rather than defaulted: the server mints the name,
so the join key is unknowable before the create, and that is the one shape
that would put an address back on the object.

A change of type between the two spellings of a kind
(`kubernetes_config_map` to `kubernetes_config_map_v1`) is not a move and
needs no `moved` block (#1081, item 2): the suffix is the API version the
block is written against, both spellings render the same natural key, the
sweep files both under the one kind, and the label carries no address to
rewrite, so the replan is empty. Claim 21's step 5 measures it.

`helm_release` is refused, by the ordinary unadmitted-type refusal, with
or without hashicorp/helm's schema (#1081, item 4): the provider serves no
resource identity schema for it and no object-metadata block, so neither
admission route reaches it. It is not a record-rung candidate: a release
is a release secret plus whatever the chart rendered, made by a path this
tool never sees, and the rendered objects carry the chart's labels and
Helm's `meta.helm.sh/release-name` annotation, never `tofu-estate`, so the
sweep never lists them and nothing needs excluding. A chart's objects are
owned here by rendering them into `kubernetes_manifest` blocks; a
`tofu-estate` written through a chart's values makes each object an
orphan the sweep proposes to remove.

The estate sweep (#1065) is one cluster-wide, label-selected list per kind
the cluster serves with list and delete verbs, found through API
discovery: a kind the provider has a resource type for is filed under that
type, and every other kind - every CRD, and the built-in kinds the
provider never gave a type - under `kubernetes_manifest` (#1079's third
unit), which manages any served kind and imports by `apiVersion=,kind=,
[namespace=,]name=`. An object it lists that no block declares is an
orphan and is proposed for removal, planned at the synthetic address
`<type>.orphan_<namespace>_<name>`, or
`kubernetes_manifest.orphan_<kind>_<namespace>_<name>` for a manifest
kind, since the label carries no address. A block and a listed object
meet on the kind and the natural key whichever type either is filed
under, so a ConfigMap declared through `kubernetes_manifest` is not an
orphan of `kubernetes_config_map_v1`.
Two exclusions run first, either sufficient: an object with a non-empty
`metadata.ownerReferences` (a ReplicaSet's from its Deployment, a Pod's
from its ReplicaSet, an EndpointSlice's from its Service) and an object
whose every `metadata.managedFields` manager is the control plane (the
legacy `Endpoints` the endpoints controller mirrors a Service's labels
onto). Both were made by a controller, not declared, and are never orphans
- which is what makes an estate label copied through a pod template safe.

### Granting a Kubernetes estate

RBAC cannot read the label: a `PolicyRule` has verbs, groups, resources
and names, and no predicate on a label. So the fence is admission (#1066):
one `ValidatingAdmissionPolicy`, installed once, cluster-wide, by a
cluster admin, whose CEL reads `tofu-estate` off the object a write is
about to change (`oldObject`, the `aws:ResourceTag` semantic) and off the
object the write would produce (`object`, the `aws:RequestTag` semantic),
and asks the API server's own authorizer whether the caller holds `use` on
a virtual resource named after each estate,
`estates.choudoufu.intentius.io/<estate>`. That verb exists nowhere but in
RBAC, which is the point: granting an estate is an ordinary ClusterRole,
handover is a binding moving from one principal to another, and the policy
is never edited for either. Claim 23
(`live/smoke/scenarios/k8s-the-label-is-the-boundary.sh`) runs it on a
kind cluster with two ServiceAccounts, and `BREAK=1` removes the policy to
show the refusals were its doing. `live-mv -from-estate` is the governed
relabel made through the provider under the caller's own credential, so
the policy judges it exactly as it judges a plain `kubectl label`; a
rename within one estate has nothing to write on this surface and
`live-mv` says so, exit 0 (#1081).

`live/kubernetes/estate-boundary.yaml`, applied once by a cluster admin:

```yaml
# The Kubernetes estate boundary (#1066, under #1016's ruling): one
# ValidatingAdmissionPolicy, installed once, cluster-wide, by a cluster
# admin. It reads the tofu-estate label off the object a write is about to
# change (oldObject, the aws:ResourceTag semantic) and off the object the
# write would produce (object, the aws:RequestTag semantic), and refuses the
# write unless the caller holds "use" on a virtual resource named after
# each estate: estates.choudoufu.intentius.io/<estate>. That verb exists
# nowhere but in RBAC, so granting an estate is an ordinary ClusterRole
# (live/kubernetes/estate-grant.yaml) and handover is an RBAC change, not
# an edit to this object. A cluster-admin's wildcard rule matches the
# virtual resource too, so cluster-admin holds every estate the way the
# account root does on AWS.
#
# What this fences and what it does not, stated here rather than in a
# caveat: admission sees create, update and delete, never get or list, so
# the fence is write-only where an IAM condition can fence a describe. It
# fences the object, not its subresources: a scale or a status write
# arrives as a Scale or a status object carrying no label, and RBAC on
# deployments/scale is the fence for those. The control plane is exempt
# (nodes, the kube-system controllers, the scheduler and the API server
# itself), and so is any object carrying an ownerReference: a controller
# made it from a template, and the estate sweep excludes it by the same
# rule, so the fence and the sweep agree on what an estate contains.
#
#   kubectl apply -f live/kubernetes/estate-boundary.yaml
#
# Needs admissionregistration.k8s.io/v1 (Kubernetes 1.30 or later).
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: choudoufu-estate-boundary
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["*"]
        apiVersions: ["*"]
        operations: ["CREATE", "UPDATE", "DELETE"]
        resources: ["*"]
    objectSelector:
      matchExpressions:
        - key: tofu-estate
          operator: Exists
  matchConditions:
    - name: not-the-control-plane
      expression: >-
        !('system:nodes' in request.userInfo.groups)
        && !request.userInfo.username.startsWith('system:serviceaccount:kube-system:')
        && !request.userInfo.username.startsWith('system:kube-')
        && request.userInfo.username != 'system:apiserver'
    - name: not-a-controllers-object
      expression: >-
        (oldObject == null ? object : oldObject).?metadata.?ownerReferences.orValue([]).size() == 0
  variables:
    - name: oldEstate
      expression: >-
        oldObject == null ? '' : oldObject.?metadata.?labels[?'tofu-estate'].orValue('')
    - name: newEstate
      expression: >-
        object == null ? '' : object.?metadata.?labels[?'tofu-estate'].orValue('')
    - name: boundToOld
      expression: >-
        variables.oldEstate == ''
        || authorizer.group('choudoufu.intentius.io').resource('estates').name(variables.oldEstate).check('use').allowed()
    - name: boundToNew
      expression: >-
        variables.newEstate == '' || variables.newEstate == variables.oldEstate
        || authorizer.group('choudoufu.intentius.io').resource('estates').name(variables.newEstate).check('use').allowed()
  validations:
    - expression: variables.boundToOld
      reason: Forbidden
      messageExpression: >-
        'tofu-estate=' + variables.oldEstate + ' fences this object and ' + request.userInfo.username
        + ' is not bound to that estate (no "use" on estates.choudoufu.intentius.io named ' + variables.oldEstate + ')'
    - expression: variables.boundToNew
      reason: Forbidden
      messageExpression: >-
        'tofu-estate=' + variables.newEstate + ' would move this object into estate ' + variables.newEstate + ' and '
        + request.userInfo.username + ' is not bound to it (no "use" on estates.choudoufu.intentius.io named ' + variables.newEstate + ')'
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: choudoufu-estate-boundary
spec:
  policyName: choudoufu-estate-boundary
  validationActions: ["Deny"]
```

`live/kubernetes/estate-grant.yaml`, once per estate and principal, with
`ESTATE`, `PRINCIPAL` and `PRINCIPAL_NAMESPACE` filled in:

```yaml
# One estate's grant (#1066; live/MARKERS.md, "Granting a Kubernetes
# estate"): the ClusterRole that names the estate, and the binding that
# hands it to one principal. The verb and the resource are virtual - no
# object called estates.choudoufu.intentius.io exists - and the only thing
# that reads them is live/kubernetes/estate-boundary.yaml, through the
# admission authorizer. Replace ESTATE with the estate name and the subject
# with your principal, then kubectl apply -f. Handover is this binding
# moving from one principal to another; nothing on the objects changes.
#
# This grant is the fence only. The principal still needs ordinary RBAC
# for the kinds its estate declares (create, update, patch, delete) and
# list on every kind the estate sweep asks for; the estate label is what
# the fence reads, and RBAC alone cannot read it.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: choudoufu-estate-ESTATE
rules:
  - apiGroups: ["choudoufu.intentius.io"]
    resources: ["estates"]
    resourceNames: ["ESTATE"]
    verbs: ["use"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: choudoufu-estate-ESTATE-PRINCIPAL
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: choudoufu-estate-ESTATE
subjects:
  - kind: ServiceAccount
    name: PRINCIPAL
    namespace: PRINCIPAL_NAMESPACE
```

**Three things are true of this fence that are not true of the IAM one,
and they are stated here rather than in a caveat.** Admission sees create,
update and delete and never get or list, so the fence is write-only where
an IAM condition can fence a describe; reads are RBAC's alone. It fences
the object and not its subresources: a `kubectl scale` or a status write
arrives as a Scale or a status object carrying no label (measured on
Kubernetes 1.36; a `*/*` rule does not change it), and RBAC on
`deployments/scale` is the fence for those. And the policy is one shared
cluster object with a wider blast radius than two IAM changes: a cluster
admin installs it and any cluster admin can remove it. The fence is also
per estate, never per address, because the label carries no address; a
team that wants two boundaries makes two estates.

**What is exempt.** The control plane (`system:nodes`, the `kube-system`
ServiceAccounts, `system:kube-*` and the API server itself), because
kubelets write status and controllers write the copies a template makes;
and any object carrying a non-empty `metadata.ownerReferences`, because a
controller made it from a template. That second exemption is the same
rule the estate sweep excludes by, so the fence and the sweep agree on
what an estate contains. A `cluster-admin`'s wildcard rule matches the
virtual resource, so `cluster-admin` holds every estate, the way the
account root does on AWS.

**The grant is the fence only.** A principal still needs ordinary RBAC for
the kinds its estate declares (create, update, patch, delete) and `list`
on every kind the estate sweep asks for. Claim 23 gives its two principals
reads on everything and writes on namespaces and ConfigMaps, beside the
estate grant.

**Splitting a Kubernetes estate is a label rewrite, then a grant.** With
no address on the object, the write is `kubectl label --overwrite
tofu-estate=<new>`, and the policy reads both sides of it: the caller must
hold the estate the object is leaving and the one it is entering. There is
no `live-mv` leg for Kubernetes; the rename rule has nothing to rewrite
there ("Operate" on the Kubernetes hub). Kyverno and Gatekeeper could
express the same policy and are unverified for it.

## `tofu-estate`

The name of the estate. An estate is the unit of ownership. Everything
carrying the same `tofu-estate` value is one management domain, matched by
one `live` config block.

The grammar is `[a-z][a-z0-9-]{0,127}`. A lowercase ASCII letter, then
lowercase letters, digits, or hyphens, 1 to 128 characters in total. This is
narrower than the AWS-allowed character set on purpose. An estate name is
meant to be typed, grepped, and read aloud, not to carry arbitrary content.
An example is `stateless-e2e`, the demo estate's name
(`live/e2e/estate/`).

## `tofu-address`

The resource's canonical address as OpenTofu's config address formatter
produces it, including module path and any `for_each` or `count` instance
key, escaped per the rule below. This is the field that answers "which
config block owns this resource". The entire binding mechanism for the
marker admission path (path 2) rests on this value matching an address that
exists in configuration.

**Grammar vs. current builds.** Every segment this grammar allows is a
segment current builds produce. Identity resolution, projection, discovery,
stamping and the rename all traverse `cfg.Children`, so a `tofu-address`
value carries the full module-qualified address at any nesting depth (issue
#59, phases 1-2 / "59b"/"59c"). A `for_each`-keyed module call with
statically-evaluable keys contributes a quoted key to that path; a module
call expanded with a statically-evaluable `count` contributes an integer one
(issue #195). `module.counted[0].aws_vpc.main` is a value written onto a
real VPC, not an illustration - it is the marker
`live/e2e/limits/child-module/counted` carries.

Both keyed forms are stamped automatically, since issue #378. The mechanism
changed under issue #644 and the format did not: #378 wrote a template over
`tofu.marker_module_prefix` into the one `*hclsyntax.Body` a module call's
several instances share, because that was the only way a configuration
rewrite could produce a different address per instance. The marker writer
is now `internal/live/projection`'s `NodeResolver.AdjustConfigValue`, which
is handed one concrete instance and its already-evaluated configuration
value, so it writes the escaped instance address as a plain string and the
evaluator symbol is gone. What AWS ends up holding is an ordinary escaped
address either way, which is the point. A resource that writes
`tofu-address` by hand inside a keyed module call keeps its own value,
verified against what this run resolved and never overwritten -
`live/LIMITATIONS.md` records that under "keyed module", and the concept
page's "Modules" section has the idiom.

What is refused is narrower than any form of this grammar, and it is about
the expression rather than the segment. A module call whose `count` or
`for_each` cannot be evaluated from `var`, `local`, `path`, `terraform` and
`tofu` alone is refused, because its instance keys become part of every
address inside the module and have to be known before anything is read from
the cloud. So is a call whose own arguments use `count.index` in any shape
this fork cannot prove gives each instance a distinct value - indexing a
list by it, or arithmetic like `count.index % 3`, which renders indices 0
and 3 identically. That is the same test `count.index` faces wherever it can
reach identity, and it is a test about the shape rather than about the
keyword: `"n-${count.index}"` passes it. (`RuleChildModule` in
`internal/live/lint/child_module.go`, `analyzeCountIndexSafety` in
`count_index.go`, and `live/LIMITATIONS.md`'s "child-module" entry.)

The unescaped grammar, in informal EBNF matching OpenTofu's address
syntax.

```
address    = segment { "." segment } ;
segment    = ( "module" "." ident [ index ] ) | ( ident "." ident [ index ] ) ;
index      = "[" ( digits | quoted-key ) "]" ;
quoted-key = '"' key-chars '"' ;
```

Some examples of unescaped addresses follow. Every one of them is an
address a tool reading these tags will meet, the last included: a `count`
key on a `module.` segment is written by this fork today, and a tool that
cannot parse one will fail on an estate that uses a counted module call.

- `aws_vpc.this`
- `aws_subnet.this["a"]`
- `aws_eip.this[2]`
- `module.subnets["a"].aws_subnet.this`
- `module.subnets[2].aws_subnet.this`

### Escaping rule

AWS tag values cannot contain `[`, `]`, or `"`. The escaping is a
substitution over the whole address string, applied before writing the tag
and never reversed by any code path that only needs to *compare*.
Comparison is always "escape the known config address, compare strings,"
never "decode the tag blind."

1. Escape the content of every instance key first (see "for_each key
   escaping", directly below) - this is a no-op for a `count` index, which
   is only ever digits.
2. Replace every `[` with `:`.
3. Delete every `]`.
4. Delete every `"`.

A `:` in an escaped value therefore always means an index starts there. The
index runs to the next `.` or the end of the string - which step 1 is what
guarantees: an instance key's own `.` and `:` are never raw by the time
steps 2-4 run.

| Unescaped | Escaped (`tofu-address` value) |
|---|---|
| `aws_vpc.this` | `aws_vpc.this` |
| `aws_subnet.this["a"]` | `aws_subnet.this:a` |
| `aws_eip.this[2]` | `aws_eip.this:2` |
| `module.subnets["a"].aws_subnet.this` | `module.subnets:a.aws_subnet.this` |
| `module.subnets[2].aws_subnet.this` | `module.subnets:2.aws_subnet.this` |
| `aws_subnet.this["alice.smith"]` | `aws_subnet.this:alice@dsmith` |
| `aws_subnet.this["at@sign"]` | `aws_subnet.this:at@@sign` |

A single tag value holds at most 256 characters (the AWS hard cap on tag
values). An escaped address that does not fit is carried across several
tags instead (see "tofu-address continuation tags", directly below), up to
a total of 1024 characters. Past that wider ceiling, the original rule
still holds without exception: an address that does not fit is a lint-time
error, not a truncation. Silently truncating an ownership key is worse than
refusing to admit the resource.

### for_each key escaping

Stock OpenTofu accepts any string as a `for_each` key. A `for_each` instance
key here may contain any character AWS allows a tag value to render as
printable text at all - which is almost everything - except six characters
that collide with a rule outside this fork's own control, not with the AWS
tag-value charset itself (`internal/live/markerkey`, `RuleForEachKey`):

- `"`, `\`, and every non-printable character (tab, CR, LF included) are
  escaped by OpenTofu's own address rendering (`addrs`' `toHCLQuotedString`)
  before this fork's own escaping ever sees the text, and it has no way to
  tell that escaping apart from the character actually being there.
- `$` and `%` are doubled by that same rendering when immediately followed
  by `{`, a transformation with no per-character inverse.
- `[` and `]` are the delimiters the address-level escaping rule (above)
  scans for; a raw one inside a key corrupts that scan before any key-level
  rule gets a chance to run.

Everything else - the full AWS-legal set `+ - = . _ : / @`, letters, digits,
and space as before, plus almost every other printable character, `(`, `)`,
`;`, `!`, and a great deal more (issue #210) - is escaped into a marker in
two layers, applied to a raw key in this order:

1. **Out-of-charset escaping** (issue #210). Every character outside the
   AWS-legal set (`+ - = . _ : / @`, letters, digits, space) is carried into
   it: `+` (the escape introducer) doubles, and everything else becomes `+`
   followed by its Unicode code point as six uppercase hex digits - `a(b)`
   becomes `a+000028b+000029`. A key already inside the AWS-legal set is
   unchanged by this layer, with one exception: a key containing a literal
   `+` is not, because `+` has to double to keep its own escape sequences
   unambiguous. `plus+one` becomes `plus++one`. No character among the
   eight in the AWS-legal punctuation set is free of this cost - it is the
   same trade issue #178 made for `@` below, on a different character.
   `+` was chosen as the least likely of the eight to already appear in a
   `for_each` key drawn from a resource name, an availability zone, a CIDR,
   or similar (see `internal/live/markerkey`'s `Introducer` for the full
   reasoning).
2. **The `.` / `:` / `@` doubling** issue #178 introduced (below), applied
   to step 1's output.

| Raw key | Escaped |
|---|---|
| `a(b)` | `a+000028b+000029` |
| `plus+one` | `plus++one` |
| `a;b` | `a+00003Bb` |

Step 1 has to run first: its own output is guaranteed to contain no raw
`@`, `.` or `:` as part of an escape sequence (its own introducer is `+`,
none of those three), so step 2 never mistakes anything step 1 produced for
its own escape sequences.

Three of the AWS-legal characters - `@`, `.` and `:` - would collide with
the address-level escaping above if embedded raw, so a key's own instance
of any of them is substituted, in this order:

1. Every `@` becomes `@@`.
2. Every `.` becomes `@d`.
3. Every `:` becomes `@c`.

The order is load-bearing: doubling `@` first guarantees that every `@`
these steps introduce is never itself mistaken for one that needs
doubling. Reading it back reverses both layers, in reverse order: the three
substitutions above in a single left-to-right scan, not as three
independent reverse replacements (two adjacent escaped characters have to
be read as two two-character units rather than reprocessed as if the
first's output could be the second's input), and then step 1's hex
unescaping on what that scan produces.

Issue #178 introduced the doubling: before it, `.` and `:` were excluded
from a `for_each` key entirely rather than escaped, because they collided
with the address-level rule and nothing decoded a key on its own to tell a
literal `.` apart from a segment separator. `@` was always admitted and was
never escaped - it is legal in a tag value and does not collide with
anything the address-level rule touches on its own - which is exactly what
makes it the one character both grammars admit but escape differently. See
"for_each key migration", below, for what that means for a marker a run
wrote before this issue landed.

Issue #210's own introducer, `+`, carries the identical burden, because `+`
was already legal and unescaped before #210 - `plus+one` stamped as
`plus+one` before this issue, and stamps as `plus++one` now. Unlike `@`,
this is not covered by a single fallback: a `for_each` key can combine `+`
with `.`, `:` or `@` in the same string, and issue #178's doubling of those
three was already active for any marker stamped after #178 landed and
before #210 did. `a.b+c` stamped as `a@db+c` in that window (`.` doubled,
`+` untouched) - a THIRD grammar neither the current escaping nor the
pre-#178 one (which applies no key escaping at all) reproduces on its own.
`AddressMatches` therefore tries three escapings of a declared address, not
two: current, then this pre-#210-but-post-#178 one
(`markers.pre210EscapeAddress`), then pre-#178
(`LegacyEscapeAddress`) - see "for_each key migration", below, for the same
accounting applied to `@`.

**The per-instance apply-time case.** A `for_each` block's marker value is
usually a template over `each.key`, evaluated once per instance by the
ordinary plan engine (see "count and for_each", below) - `replace()` calls
can express the `.`/`:`/`@` doubling this way, but not the hex escaping,
which is a function of each character's own code point that `replace()`
cannot compute. For a block where every key is unaffected by the hex
escaping (the overwhelming common case), the template is exactly as before.
For a block where at least one of its own keys needs it, the tool that
stamps the marker precomputes every instance's escaped address in advance
(the keys are already known before the plan runs) and writes a lookup
table keyed by the raw key instead of a template - functionally identical
to what `for_each` key escaping always produced, just built ahead of time
rather than replayed by three `replace()` calls at apply time.

### for_each key migration

Every candidate character this escaping could have used as its leader was
already legal, unescaped, inside a `for_each` key before issue #178 - the
whole admitted set before this issue was `+ - = _ / @`, and every one of
those five was already legal on the wire. `@` is the character issue #178
chose, which means a `for_each` key containing `@` is the one shape where a
marker a prior run stamped differs from what this run would stamp for the
same key: `aws_subnet.this["at@sign"]` escaped, before issue #178, to
`aws_subnet.this:at@sign` (see the table above; `@` passed through
untouched), and escapes now to `aws_subnet.this:at@@sign` (doubled). A key
containing only `.` or `:` cannot have this problem, because both were
refused by lint before this issue and so never reached a live marker.

This fork's compatibility answer is not a spec version bump (see
"Versioning", below, for why: every marker written under spec version 1
still parses and still names the instance it always named). It is that the
DECLARED side of every ownership comparison computes both the current
escaping and the pre-#178 one for the same address, and accepts either as a
match, while every write always uses the current escaping. A resource whose
live marker still reads `aws_subnet.this:at@sign` binds to
`aws_subnet.this["at@sign"]` exactly as it always did; the address a fresh
`live-mv`, adoption, or stamp writes for that same instance is
`aws_subnet.this:at@@sign`, and the next comparison recognizes that too.
Nothing in this fork rewrites an existing marker to the new escaping on its
own - a live resource carries whichever grammar last wrote it until
something explicitly restamps it.

**A residual ambiguity, not a guarantee both ways.** Decoding a marker back
into an address (removal planning's display label, `markerTypeOf`'s
best-effort type guess) is not the same operation as comparing it against a
known declared address, and it cannot always tell which grammar produced
the bytes it is reading: a pre-#178 key that happened to contain the
literal two-character sequence `@d`, `@c` or `@@` is indistinguishable, in
the tag text alone, from a post-#178 key whose escaping produced the same
bytes. Nothing that identifies or acts on a live resource depends on
resolving that ambiguity correctly - see `markers.UnescapeAddress`'s doc
comment - so the worst it can do is mislabel a resource in a message, never
bind, adopt, or destroy the wrong one.

**Issue #210 repeats this exact shape for `+`.** `+` was in that original
five-character set (`+ - = _ / @`), legal and unescaped since before #178,
so a `for_each` key containing it is the same kind of shape `@` is: a
marker a prior run stamped differs from what this run would stamp for the
same key, because `+` now doubles. `AddressMatches` covers it the same way,
extended to a third escaping rather than a second - see "for_each key
escaping", above, for why a key combining `+` with `.`, `:` or `@` needs
that third grammar and not just the pre-#178 one. `markers.UnescapeAddress`
carries the equivalent residual, best-effort ambiguity for `+`: a pre-#210
key that happened to contain the literal sequence `+` followed by six hex
digits is indistinguishable from a post-#210 key whose hex escaping
produced the same bytes, with the same guarantee that nothing which binds,
adopts, or destroys a resource depends on resolving it. Every other
character issue #210 admits (`(`, `)`, `;`, and everything else outside the
pre-#210 AWS-legal set) was refused by lint before this issue and so never
reached a live marker, exactly as `.` and `:` never did before #178.

### `tofu-address` continuation tags

Deep module trees and long `for_each` keys can produce an escaped address
longer than one 256-character tag value holds. Instead of refusing every
such address outright, `tofu-address` carries the first 256 characters and
up to three more tags (`tofu-address-2`, `tofu-address-3`,
`tofu-address-4`) carry the rest, in order, 256 characters at a time. A
reader concatenates `tofu-address`, then `tofu-address-2` if present, then
`tofu-address-3`, then `tofu-address-4`, and the result is the one escaped
address that would not fit in a single tag. This is the only sanctioned way
to read a split address. The continuation tags are never meaningful on
their own, individually or out of order.

This raises the effective limit to 1024 characters (four tags of 256), and
the limit remains bounded. An address that does not fit in four tags is still a
lint-time error (RuleOverlongAddress, `internal/live/lint/overlong_address.go`)
for the same reason the original 256-character refusal existed:
truncating an ownership key is worse than refusing to admit the resource,
and a fourth tier of continuation would just move the same question further
out without answering it. Four tags is deliberately generous headroom
against the 50-tag-per-resource AWS limit (minus whatever tags the
configuration's `tags` block already uses) while staying a small, fixed
number instead of a knob a configuration can turn.

A resource whose address fits in 256 characters (the overwhelming common
case, and every marker written before this addition existed) carries only
`tofu-address` and no continuation tags at all, exactly as before. Nothing
about a short address changes.

**Reading a corrupt chain.** A continuation tag can only exist because
something wrote the whole set together. The three tags below `tofu-address`
are never independently meaningful. A tag map where `tofu-address-3` is
present but `tofu-address-2` is not (the middle of the chain deleted by a
hand edit, a tag policy misfire, or two racing writes) cannot be
concatenated into anything, and per "Ownership semantics" below it is
malformed: reported loudly and by name, never silently read as the address
up to the gap and never treated as unowned.

**Writing one.** A tool that stamps markers and encounters an address over
256 characters splits it the same way: the first 256 characters into
`tofu-address`, the next 256 into `tofu-address-2`, and so on, stopping at
whichever tag holds the last of the address (a shorter final chunk is
normal and is not padded). A tool that only ever writes addresses under 256
characters never has to think about this section at all.

**Known limitation, left for the lint layer to enforce.** The escaping is
lossy in one way, by design, and ambiguous in one further way as a residue
of issue #178's migration.

- A bare integer `for_each`/`count` index and a quoted string index with
  the same digits collide. `this[2]` and `this["2"]` both escape to
  `this:2`. This is harmless in practice because a single resource block
  uses either `count` or `for_each`, never both, so the two never compete
  for the same address.
- A `for_each` key containing one of the six characters "for_each key
  escaping" names above (a quote, a backslash, a non-printable character,
  `$`, `%`, `[` or `]`) cannot be written as a marker at all, and is
  rejected by lint. Every other key - including one containing `.`, `:` or
  `@`, or one outside the AWS-allowed tag character set entirely, like
  `a(b)` - always escapes, per "for_each key escaping" above, since issue
  #210; there is no printable character left that the escaping rule
  refuses to handle except those six.
- A key long enough that its escaped form - after the out-of-charset
  expansion "for_each key escaping" describes - exceeds the continuation-tag
  budget below is refused there (RuleOverlongAddress), not truncated. The
  expansion is worst-case seven characters per out-of-charset rune, so a
  key that fits comfortably as written can still be refused once escaped;
  the refusal names the escaped length, not the raw one.
- Reading a marker's key back (rather than comparing it against a known
  declared address) carries the narrow, coincidental ambiguity "for_each
  key migration" describes: a pre-#178 key that happened to contain the
  literal bytes `@d`, `@c` or `@@` decodes the same way a post-#178 key
  whose escaping produced those bytes would. Nothing that binds, adopts, or
  destroys a resource depends on resolving it.

Unescaping. Removal planning turns a marker back into an address, which the
escaping rule supports for every value a lint-clean configuration produces
today (the `.`/`:` refusal this paragraph used to describe retired with
issue #178; see "for_each key escaping" above). A key of all digits is
read as a count index. A quoted string key of the same digits escapes to
the same value, and the reading cannot mislead, because the resource is
identified by its live import ID and the address is only the label the plan
prints. A live resource whose declared instance really was the string key
would have bound during discovery, since the comparison is between two
escaped values and those two are the same string.

## `tofu-slot`

Present only on resources that come from a `count` block, and not on all of
those. An opaque, stable identifier for one member of a fungible set,
assigned when the instance is created.

### Which count instances carry one

A slot answers exactly one question: which live resource is instance k of a
set whose members are interchangeable. Not every `count` block declares such
a set. Where the configuration itself names each instance - a log group whose
`name` is `"/svc/${count.index}"`, a bucket whose `bucket` argument is built
from the index, a `count = var.enabled ? 1 : 0` block with a fixed name - the
live resource that is instance k is the one the configuration names, there is
nothing left for a slot to decide, and no slot is written. Those instances
carry `tofu-estate` and `tofu-address` (and its continuation tags, if the
address needs them) and no `tofu-slot`, on the first apply and on every
apply after it. This is the common case rather than an edge case: over half
of the `count` instances this fork's own fixtures resolve are of that kind.

A slot is written when the configuration does NOT settle which live resource
is which: the instance's identity is not computable from the configuration
(a server-assigned id, a generated name), so the set is genuinely fungible
and the marker is the only record of which member is which.

The distinction is a property of the block, not of one instance, and it is
all-or-nothing: every member of a set carries a slot, or no member does. A
reader never has to know which kind of block it is looking at, and never has
to consult a configuration to find out - it reads the set. Slots present:
bind by slot, per the rules below. Slots absent: bind by `tofu-address`, the
index in each member's address being what says which instance it is, which
is the rule that applied before slots existed and is still correct for a set
whose members the configuration names. A set carrying slots on some members
and not others is neither, and is an error rather than a guess (LIMITATIONS,
"Partial slot markers on a count set"). A missing slot on one member of an
otherwise slotted set is therefore never to be read as "this kind of block
does not carry slots".

Reuse is bounded, not absolute (amended, spec v1). A marker-managed estate
has no registry and no side channel. The only record of a slot is the tag
on the live resource, so once that resource is gone the slot is
unrecoverable. The guarantee is therefore as follows. The assignment
high-water mark is the highest slot among the block's live resources. A
slot is never reused while any resource holds it, and never duplicated
within a set. A slot whose resource has been deleted may be assigned again
later.

The format is an unsigned base-10 integer, ASCII digits only, no leading
zeros (the value `0` is written as `0`, not `00`). Slots are assigned from
a monotonic counter per `count` resource block (not per instance address,
since an instance's address contains the index a slot is deliberately
independent of), starting at `0`. The first instance of `aws_eip.this`
gets slot `0`, the second gets slot `1`, and so on. New instances are
assigned slots above the live high-water mark. Ten digits (up to
4294967295) is the ceiling. No realistic `count` approaches it.

For a `count` instance carrying a slot, the slot is what binds it to a
declared instance, and the address follows. The k-th lowest live slot
binds to index k. `tofu-address` remains mandatory and remains the full
indexed address, but a value naming a different index than the slot bound
to is stale, never a rival claim. It is repaired by the next plan's ordinary
tag write, and it is never a collision. Scale-down deletes the highest
slots, compared numerically, so every survivor keeps the index it already
occupied. That is the no-churn rule.

Slot values are compared numerically, not lexicographically, because they
are carried as strings in a tag. `"9"` is a lower slot than `"10"` even
though it sorts after it as a string. Any tool implementing the scale-down
rule ("surplus deletes the highest slots") must parse before comparing.

`tofu-slot` is independent of `tofu-address`. A plain rename that does not
change cardinality leaves slot assignments untouched. Only a
change in the number of live instances assigns or retires slots.

## Ownership semantics

- A resource carrying a `tofu-estate` tag belongs to that estate. The
  value is the entire ownership claim, and there is no secondary
  check.
- A resource carrying neither `tofu-estate` nor `tofu-address` is foreign.
  It sits outside every estate's ownership and is reported, protected, and
  never auto-deleted.
- A resource carrying `tofu-estate` but missing or carrying an unparseable
  `tofu-address` is malformed, not foreign and not owned. This is a named
  error surfaced to the operator, the same way a binding ambiguity is (two
  live resources claiming one address). It is never guessed at, and never
  silently treated as either "belongs to no one" or "belongs to whichever
  address looks close enough." A `tofu-address` continuation chain with a
  gap in it (a `tofu-address-3` present while `tofu-address-2` is not) is
  the same malformed case: it cannot be concatenated into anything, so it
  is reported, never read as the address up to the gap.
- **How markers are read back has a timing property.** Two
  reads find them, and only one of the two is eventually consistent. Binding
  a declared resource to its live object goes through a per-type listing
  against the service's own API, so nothing about a marker written moments
  ago can be missed there. The estate-wide sweep for resources this estate
  owns but no longer declares may instead go through the Resource Groups
  Tagging API's `GetResources`, filtered on `tofu-estate`, and that index is
  eventually consistent: a resource whose tags have not yet propagated is
  simply not returned. The consequence is bounded and is in the safe
  direction - a sweep can be a run behind, so an orphan may be reported one
  run late. It cannot cause a marker to be lost, and it cannot make a
  declared resource look absent, because that question is never asked of the
  tag index.
- Two resources carrying the same `tofu-estate` and the same
  `tofu-address` at once is also a named error. The marker admission path
  assumes at most one live resource per address per estate, and a
  collision means something upstream (a manual tag edit, a botched
  `live-mv`) needs a human to resolve it. This holds regardless of region
  or provider configuration: an address is unique estate-wide, not
  estate-wide-per-region, so two live resources in two different regions
  both carrying one estate's marker for one address are the same named
  collision as two in one region, not two legitimate resources that happen
  to sit in different places. A multi-provider estate (issue #69) reports
  it the same way, naming every region involved.

## The rename rule

Renaming a resource in config, whether changing its address, moving it into
or out of a module, or changing a `for_each` key, is done by rewriting the
`tofu-address` tag on the live resource to the new escaped address. That
tag write is the move operation: there is no state to edit, no `moved`
block to author, and no two-step migration. The old
address is simply gone from the tag the instant the new one is written,
because a single tag value cannot hold both.

`choudoufu live-mv <old-address> <new-address>` performs exactly this. It escapes both addresses, finds the live resource whose
`tofu-address` equals the old escaped value within the target estate, and
overwrites it with the new escaped value in one tag-update call. After it
runs, a plan against the old address finds nothing (it was never a delete,
since the resource was never bound to "old" as far as anything after the
rewrite is concerned), and a plan against the new address finds the
resource already bound. The result is zero churn, with no special-casing
of renames in the plan engine.

Old markers never linger. There is exactly one `tofu-address` value on a
resource at any time, and after a rewrite that value is the new address,
not a history of addresses it once had. This holds for continuation tags
too: a rename onto a shorter address writes fewer `tofu-address-*` tags
than the old one carried, and a rename tool is expected to delete whichever
continuation tags the new address does not reach, so that no stale
tag remains for a later read to concatenate onto the new value.

## Versioning

The header at the top of this file ("Spec version 1") versions this
document, not the resources it describes. There is no
`tofu-marker-version` tag, and none is planned. Markers written under an
older revision of this spec remain on live resources indefinitely. Nothing
rewrites them proactively.

A change is additive (no version bump) if every marker already written
under the current spec still parses and means the same thing under the new
one. Documenting a previously implicit rule more precisely qualifies, as
does adding a new optional tag key that absence-tolerant readers can
ignore. The `tofu-address` continuation tags are exactly this: every marker
written before they existed has no `tofu-address-*` tag and reads exactly
as it always did, so no existing marker is invalidated. The asymmetry runs
the other way instead: a reader built only against spec version 1's single
`tofu-address` tag will silently read a new split marker's first 256
characters as the whole address instead of erroring. That is a real gap for
anything that has not been updated to read continuation tags, and it is
accepted without a version bump, because the
definition above is about old data under new code, not new data under old
code. The versioning number cannot help with the latter no matter which way
it is called.

A change is breaking (version bump required) if it invalidates that
guarantee. Renaming a tag key, changing the escaping rule, changing what
absence of a tag means, or narrowing a grammar so previously valid values
become invalid all qualify. A breaking change requires a coordinated
rewrite pass over every live estate before tools built against the new
version can trust what they read, and the version number here is what lets
a tool detect that an estate's markers predate what it understands, so it
can refuse to guess instead of misreading them.

**Issue #178 widened the escaping rule and did not bump the version.**
"Changing the escaping rule" above is the general case, where a version
bump is the only honest option because old markers would otherwise be
misread. Issue #178 is the narrower case the additive definition already
covers: it admits `.` and `:` into a `for_each` key rather than narrowing
anything, and every marker written under spec version 1 - both before and
after this issue - still parses under the current code and still names the
instance it always named, per "for_each key migration" above. The one
place the two escapings can disagree (a key containing `@`) is handled by
comparing both on the declared side rather than by asking a reader to know
which grammar wrote what it is looking at, which is what makes a version
bump unnecessary rather than merely inconvenient to add. A future change
that made a *reader* need to know which grammar wrote a marker - rather
than a *writer* needing to compute both to find it - would not get the same
pass.

## Interop

This file is the entire contract. Nothing about the mode's Go internals
(the projection builder, the lint rules, the identity resolution) is
required for another tool to participate. Any tool that reads and
writes `tofu-estate`, `tofu-address`, and `tofu-slot` per the grammar
above, on the resource types it manages, can discover, classify, and
safely mutate resources in a marker-managed estate.

This grammar is designed for external adoption. Any tool, in any language,
can read and write these three tags without linking against this fork or
knowing it exists. No known implementation of this spec exists outside
this fork today, which is expected at this stage: the spec is written to
be the stable integration surface a future tool builds against.

## Granting an estate

An estate is inherited by being granted access to it, and this is the grant.
The marker is an ordinary resource tag, so on a type that has tags IAM can
condition on it directly through `aws:ResourceTag`, with no second permission
model to keep in sync. That is <!-- survey-gen:begin marker-governable-count -->682 of the 1027<!-- survey-gen:end marker-governable-count -->
admitted AWS resource types; "What this grant cannot reach" below is the
rest, and it is a real gap rather than a caveat.
`the tier definitions (#417)` names that gap by tier - marker-carried,
declaration-carried, record-carried, excluded by design - and states, per
tier, what a grant like this one reaches and what losing the record store
costs.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ActOnWhatTheEstateAlreadyOwns",
      "Effect": "Allow",
      "Action": [
        "ec2:TerminateInstances",
        "ec2:ModifyInstanceAttribute",
        "ec2:DeleteTags"
      ],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:ResourceTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "CreateOnlyIntoThisEstate",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:CreateTags"
      ],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    }
  ]
}
```

**Two statements, because creation and mutation are conditioned by different
keys.** `aws:ResourceTag` reads a tag off a resource that already exists, so it
governs everything the estate acts on. It cannot govern a create: there is no
resource yet to carry the tag, and a `RunInstances` under a `ResourceTag`
condition never matches. What the creating principal supplies is
`aws:RequestTag`, and conditioning on it is what makes the second statement a
grant to create *into this estate* rather than a grant to create anything.

The actions above are illustrative, not the full scope: a real
grant names the actions the estate's own types need, which
[`site/content/docs/use/reference.md`](https://intentius.io/choudoufu/docs/use/reference/)
lists per stage, and the resource types the configuration declares.

**Handover is two IAM changes and no tag writes.** Attach that policy to the
receiving role, detach it from the sending one, and the estate has moved.
Nothing about the resources changes, no state is exported, and the two roles
never both hold it unless you want an overlap. The receiving team can list
what it inherited before running anything, with `aws resourcegroupstaggingapi
get-resources --tag-filters Key=tofu-estate,Values=prod-networking` and no
`choudoufu` binary.

**Splitting an estate is a tag rewrite, then two policies.** Rewrite
`tofu-estate` on the resources that are leaving, and the same statement with
the new estate name governs them. The split is a tag write and a policy
copy, and neither half moves. `choudoufu live-mv -from-estate` performs the
rewrite, one resource per call, with the rename's own refusals in front of
the write.

### Which services this actually reaches

The condition key is evaluated per action, per service, and support is not
uniform. This roster is generated from AWS's own Service Authorization
Reference (`live/iam-reference.json`, `just iamref`), so it is checkable
rather than asserted.

<!-- iamref-gen:begin resource-tag-services -->
| Service | IAM prefix | Actions naming `aws:ResourceTag` |
|---|---|---|
| EC2 | `ec2` | 495 of 793 |
| ResilienceHub, ResilienceHubV2 | `resiliencehub` | 57 of 128 |
| SES | `ses` | 48 of 228 |
| AutoScaling | `autoscaling` | 42 of 68 |
| ECS | `ecs` | 37 of 81 |
| Kinesis | `kinesis` | 31 of 40 |
| CertificateManager | `acm` | 20 of 41 |
| CleanRooms | `cleanrooms` | 12 of 107 |
| ElastiCache | `elasticache` | 11 of 77 |
| CloudWatch | `cloudwatch` | 7 of 67 |
| SageMaker | `sagemaker` | 7 of 444 |
| WorkSpaces | `workspaces` | 7 of 101 |
| KafkaConnect | `kafkaconnect` | 2 of 18 |
| AuditManager | `auditmanager` | 1 of 62 |
| Batch | `batch` | 1 of 45 |
| CUR | `cur` | 1 of 12 |
| SSMQuickSetup | `ssm-quicksetup` | 1 of 14 |

17 of the 157 IAM prefixes this estate's admitted types reach name the key on at least one action. The remaining 140 are **unmeasured, not disproven**: the reference does not set out to enumerate every global condition key per action, and `lambda:GetFunction` lists none at all while Lambda does support tag-based authorization. Read this as the set a marker-scoped grant is known to bite on, never as its complement.
<!-- iamref-gen:end resource-tag-services -->

The asymmetry in that last sentence is the whole of how to use this section,
and it is the same caution the SCP below states about `aws:TagKeys`: a
statement that looks correct and silently does nothing for one service is
worse than no policy. Build a grant on the services above and it constrains
what you expect. Assume the ones absent from it are unreachable and you will
be wrong about several, because the reference is silent rather than negative.

For a service outside the roster, the reachable guarantees are the ordinary
ones: the account boundary, the resource types named in the policy, and the
region. Those are coarser than a marker, and they are what an estate not yet
covered by tag-based authorization inherits.

### What this grant cannot reach

The roster above is about whether AWS evaluates the condition key. This
subsection is about the other half, which is whether there is a tag for the
key to read at all, and it applies to both grants a reader might write.

The across-estate grant is the one above, conditioned on
`aws:ResourceTag/tofu-estate`: a principal may act on everything this estate
owns. The within-estate grant is finer and is not published above, because it
is one substitution away: conditioning on `aws:ResourceTag/tofu-address`
instead gives a principal rights over one declared address, and
`aws:RequestTag/tofu-address` gives it the right to create that address and
nothing else. Both keys are ordinary resource tags, which is the whole reason
the substitution works.

<!-- survey-gen:begin marker-governable-gap -->
345 of the 1027 admitted AWS resource types carry no `tags` argument at all (`live/survey-full.json`'s taggability signal, joined to the admission table). A resource of one of those types carries `tofu-estate` no more than it carries `tofu-address`, so both conditions above are unmatched on it and both statements convey nothing about it. If a principal can act on such a resource, the grant is wider than its condition, and keeping the two in step is a second permission model. The top of this section says there is not one. There is, for these 345 types.

This is not the markerless veto. The 159 types in `internal/live/identity`'s `MarkerlessTypes` are untaggable *and* server-minted, and none of them is admitted, so no estate contains one. The 345 here are admitted: a configuration declares them and this fork manages them, identified from the declaration itself rather than from a tag, which is what the client-named, parent-derived and account-derived admission paths mean. Being identifiable without a tag is a different property from being governable by one, and only the second is what an IAM condition needs.

They span 96 CloudFormation services.

<details>
<summary>Untaggable admitted types per service</summary>

| Service | Untaggable | Admitted in this service |
|---|---|---|
| EC2 | 29 | 95 |
| S3 | 14 | 16 |
| ApiGateway | 11 | 19 |
| Cognito | 10 | 12 |
| IAM | 8 | 17 |
| SSO | 8 | 10 |
| WorkSpacesWeb | 8 | 18 |
| AutoScaling | 6 | 6 |
| ECR | 6 | 8 |
| Events | 6 | 8 |
| Glue | 6 | 18 |
| Lightsail | 6 | 14 |
| Logs | 6 | 12 |
| Route53 | 6 | 8 |
| SES | 6 | 14 |
| CloudFront | 5 | 15 |
| ElasticLoadBalancingV2 | 5 | 14 |
| NetworkManager | 5 | 17 |
| Notifications | 5 | 6 |
| DynamoDB | 4 | 5 |
| MSK | 4 | 8 |
| SSM | 4 | 8 |
| ServiceCatalog | 4 | 7 |
| APS | 3 | 7 |
| AppStream | 3 | 6 |
| AppSync | 3 | 6 |
| Config | 3 | 6 |
| EFS | 3 | 5 |
| Lambda | 3 | 7 |
| OpenSearchServerless | 3 | 5 |
| RDS | 3 | 17 |
| Redshift | 3 | 7 |
| SecretsManager | 3 | 4 |
| WAFv2 | 3 | 7 |
| ARCZonalShift | 2 | 2 |
| BedrockAgentCore | 2 | 15 |
| CloudWatch | 2 | 7 |
| CodeArtifact | 2 | 4 |
| EMR | 2 | 4 |
| LakeFormation | 2 | 2 |
| RAM | 2 | 4 |
| S3ObjectLambda | 2 | 2 |
| S3Outposts | 2 | 3 |
| SMSVOICE | 2 | 7 |
| SecurityHub | 2 | 7 |
| VpcLattice | 2 | 14 |
| ACMPCA | 1 | 2 |
| AccessAnalyzer | 1 | 2 |
| Amplify | 1 | 3 |
| AppFlow | 1 | 2 |
| ApplicationAutoScaling | 1 | 2 |
| Athena | 1 | 3 |
| Backup | 1 | 7 |
| CodeBuild | 1 | 4 |
| CodeDeploy | 1 | 3 |
| Connect | 1 | 12 |
| ControlTower | 1 | 3 |
| DataPipeline | 1 | 2 |
| DataZone | 1 | 2 |
| Detective | 1 | 2 |
| DevOpsGuru | 1 | 1 |
| ECS | 1 | 9 |
| EKS | 1 | 8 |
| ElastiCache | 1 | 8 |
| ElasticLoadBalancing | 1 | 2 |
| FIS | 1 | 2 |
| FSx | 1 | 10 |
| Grafana | 1 | 2 |
| GuardDuty | 1 | 7 |
| IoT | 1 | 10 |
| KMS | 1 | 4 |
| Kinesis | 1 | 3 |
| Lex | 1 | 2 |
| Location | 1 | 6 |
| NetworkFirewall | 1 | 6 |
| ObservabilityAdmin | 1 | 3 |
| Organizations | 1 | 5 |
| PaymentCryptography | 1 | 2 |
| QuickSight | 1 | 9 |
| ResourceGroups | 1 | 2 |
| Route53Resolver | 1 | 7 |
| S3Files | 1 | 3 |
| S3Tables | 1 | 2 |
| S3Vectors | 1 | 3 |
| SNS | 1 | 2 |
| SQS | 1 | 2 |
| SageMaker | 1 | 27 |
| Scheduler | 1 | 2 |
| ServiceCatalogAppRegistry | 1 | 3 |
| ServiceDiscovery | 1 | 5 |
| Shield | 1 | 3 |
| Signer | 1 | 1 |
| Synthetics | 1 | 3 |
| Transfer | 1 | 9 |
| WAFRegional | 1 | 4 |
| XRay | 1 | 3 |

</details>

66 further untaggable admitted types are absent from that table because `live/mapping.json` places them in no CloudFormation service at all: `aws_acmpca_policy`, `aws_amplify_backend_environment`, `aws_apprunner_custom_domain_association`, `aws_apprunner_default_auto_scaling_configuration_version`, `aws_auditmanager_account_registration`, `aws_bedrock_model_invocation_logging_configuration`, `aws_cloudfrontkeyvaluestore_key`, `aws_codecommit_approval_rule_template_association`, `aws_connect_lambda_function_association`, `aws_connect_phone_number_contact_flow_association`, `aws_datazone_asset_type`, `aws_devopsguru_event_sources_config`, `aws_devopsguru_service_integration`, `aws_directory_service_conditional_forwarder`, `aws_directory_service_trust`, `aws_ebs_fast_snapshot_restore`, `aws_ec2_allowed_images_settings`, `aws_glue_resource_policy`, `aws_guardduty_organization_admin_account`, `aws_guardduty_organization_configuration`, `aws_iam_account_alias`, `aws_iam_account_password_policy`, `aws_iam_role_policies_exclusive`, `aws_iam_role_policy_attachments_exclusive`, `aws_iam_user_group_membership`, `aws_inspector2_delegated_admin_account`, `aws_inspector2_member_association`, `aws_iot_event_configurations`, `aws_iot_thing_group_membership`, `aws_kinesis_account_settings`, `aws_lakeformation_lf_tag_expression`, `aws_lambda_function_scaling_config`, `aws_lambda_provisioned_concurrency_config`, `aws_licensemanager_association`, `aws_macie2_classification_export_configuration`, `aws_macie2_organization_admin_account`, `aws_networkmanager_attachment_routing_policy_label`, `aws_observabilityadmin_telemetry_evaluation`, `aws_observabilityadmin_telemetry_evaluation_for_organization`, `aws_opensearch_authorize_vpc_endpoint_access`, `aws_opensearch_package_association`, `aws_organizations_delegated_administrator`, `aws_redshiftserverless_custom_domain_association`, `aws_route53_vpc_association_authorization`, `aws_s3_account_public_access_block`, `aws_sagemaker_servicecatalog_portfolio_status`, `aws_security_group_rule`, `aws_securityhub_member`, `aws_securityhub_standards_control`, `aws_securityhub_standards_control_association`, `aws_servicecatalog_budget_resource_association`, `aws_servicequotas_auto_management`, `aws_servicequotas_service_quota`, `aws_ses_identity_policy`, `aws_sesv2_email_identity_policy`, `aws_spot_datafeed_subscription`, `aws_ssoadmin_region`, `aws_transfer_access`, `aws_vpc_security_group_rules_exclusive`, `aws_workmail_domain`, `aws_xray_encryption_config`, `aws_xray_trace_segment_destination`, `kubernetes_cluster_role_binding`, `kubernetes_config_map`, `kubernetes_namespace` and `kubernetes_storage_class`. They are named rather than dropped, because a service table that silently loses part of its subject reads as a complete one.

**What to use instead, for those types.** The reachable scope is the ordinary one: a `Resource` ARN in the statement, the service's own resource policy, the account, the region. That is coarser than a marker and it is maintained beside the estate instead of by it, so it has to be revisited when the estate changes. This fork does not narrow it and does not claim to.

**The count is a floor.** It is a fact about 1027 types, and a taggable type can still go unmarked in one particular configuration - a resource declared inside a `for_each`'d module body, a `tags` argument this pass can neither read nor merge into. Those are properties of a configuration rather than of a type, so nothing here counts them; `internal/live/stamp` reports each one as a skip when it happens.

**One further limit, on the within-estate half only.** An escaped `tofu-address` longer than one tag value is split across `tofu-address-2` through `tofu-address-4` (see "`tofu-address` continuation tags"), so `StringEquals` on `aws:ResourceTag/tofu-address` is compared against the first chunk alone. For such an address the condition is a prefix test over a value this grammar says is meaningless on its own, and it should not be written. The across-estate half is unaffected: `tofu-estate`'s own grammar caps it at 128 characters, so it never splits.
<!-- survey-gen:end marker-governable-gap -->

**This governs API calls, not tag survival.** A principal that cannot act on
an estate's resources can still, with tagging permissions elsewhere, remove
the markers that define it. That is the next section's subject.

## Protecting the markers

Markers are plain resource tags, and nothing about that grammar stops
anyone with tagging permissions from removing them: `aws ec2 delete-tags`,
a console "manage tags" cleanup, or an unrelated tag-hygiene automation
that untags whatever it does not recognize. What that costs depends on the
type.

For a client-named resource (`aws_s3_bucket`, `aws_iam_role`, the rest of
admission path 1) it is a nuisance and nothing worse: the next plan reports
the resource `[UNOWNED]` with the exact adoption command, because the
cloud's uniqueness constraint means a duplicate can never actually be
created under the same name. For a server-assigned resource (`aws_vpc`,
`aws_security_group`, and the rest of admission path 2's table above) the
marker is the only handle discovery has. Strip it off a live one and the
declared address it used to bind to looks exactly like a resource that was
never created: the next plan proposes CREATE, and unless something
intervenes, apply produces a second, functionally identical resource
sitting beside the orphaned first one. This section is about that case:
nothing about it looks like an error until the bill or the drift shows up.

### What actually stops it, checked rather than assumed

Two AWS Organizations mechanisms sound like they cover this. One does not,
and the other only partially.

**Tag policies enforce values, not survival.** Per AWS's documentation
([Tag policies](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_tag-policies.html),
[Enforce tagging consistency](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_tag-policies-enforcement.html)),
a tag policy "can specify that when the `CostCenter` tag is attached to a
resource, it must use the case treatment and tag values that the tag
policy defines," and enforcement mode "prevents noncompliant tagging
requests on specified resource types from completing." That is a check on
the value a tag is set to, run when a tag is written, on resource types the
feature explicitly supports. Nothing in that mechanism inspects a
`DeleteTags`-style call at all, and AWS says so directly: "Basic
compliance rules do not enforce tag compliance on resources that are
created without tags. This capability does not enforce missing tag keys."
A tag policy cannot be configured to block a tag from being removed,
because removing a tag is not the kind of event it evaluates. Do not rely
on one for this.

**SCPs can block the untagging call, but only inside the org, only in
member accounts, and only where the condition key is honored.** A service
control policy is a guardrail on what IAM principals in an organization's
*member* accounts can do
([Service control policies](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html)):
it never grants anything, it can `Deny` an action outright, and, per that
same page, it has no effect on the management account or on any
principal outside the organization. Denying the tag-removal actions for
the marker keys (`tofu-estate`, `tofu-slot`, and `tofu-address` together
with its `tofu-address-2` through `tofu-address-4` continuation tags),
with an exception for whichever principal runs `choudoufu`, is the closest
thing to a real backstop:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenyUntaggingMarkers",
      "Effect": "Deny",
      "Action": [
        "ec2:DeleteTags",
        "kms:UntagResource",
        "elasticloadbalancing:RemoveTags",
        "route53:ChangeTagsForResource",
        "acm:RemoveTagsFromCertificate",
        "states:UntagResource",
        "sns:UntagResource",
        "tag:UntagResources"
      ],
      "Condition": {
        "ForAnyValue:StringEquals": {
          "aws:TagKeys": ["tofu-estate", "tofu-address", "tofu-address-2", "tofu-address-3", "tofu-address-4", "tofu-slot"]
        },
        "ArnNotLike": {
          "aws:PrincipalArn": ["arn:aws:iam::*:role/choudoufu-automation"]
        }
      },
      "Resource": "*"
    }
  ]
}
```

The key list is every marker key this fork ever writes as of issue #71's
`tofu-address` continuation tags, not just the three literal keys a run
without a long address ever needs: `aws:TagKeys` in an SCP condition is a
set-membership check, with no prefix wildcard for "any `tofu-address-*`
key," so a policy that lists only the bare `tofu-address` leaves
`tofu-address-2` through `tofu-address-4` unprotected on any resource whose
escaped address needed them. Stripping just the continuation tags corrupts
the same ownership record as stripping `tofu-address` itself, since a
reader that cannot gather every chunk cannot reconstruct the address at
all. See "`tofu-address` continuation tags," above.

The eight actions in that statement are illustrative. The
exhaustive list is generated, because each service's tag-removal verb is
resolvable from botocore's service models the same way its tagging verb
already was, and whether AWS evaluates `aws:TagKeys` on it is resolvable
from the Service Authorization Reference.

<!-- iamref-gen:begin scp-untag-actions -->
130 tag-removal actions across this estate's services name `aws:TagKeys` in the Service Authorization Reference, so a `Deny` conditioned on it is evaluated for them. Each service's removal verb is resolved from botocore's own service models (`live/tag-verbs.json`), not written by hand.

<details>
<summary>The full action list, for pasting into the policy above</summary>

```json
"Action": [
  "acm-pca:UntagCertificateAuthority",
  "airflow:UntagResource",
  "amplify:UntagResource",
  "aoss:UntagResource",
  "app-integrations:UntagResource",
  "appconfig:UntagResource",
  "appflow:UntagResource",
  "application-autoscaling:UntagResource",
  "apprunner:UntagResource",
  "appstream:UntagResource",
  "appsync:UntagResource",
  "arc-region-switch:UntagResource",
  "athena:UntagResource",
  "auditmanager:UntagResource",
  "backup:UntagResource",
  "batch:UntagResource",
  "bcm-data-exports:UntagResource",
  "bedrock:UntagResource",
  "billing:UntagResource",
  "budgets:UntagResource",
  "ce:UntagResource",
  "chatbot:UntagResource",
  "cleanrooms:UntagResource",
  "cloud9:UntagResource",
  "cloudfront:UntagResource",
  "cloudwatch:UntagResource",
  "codeartifact:UntagResource",
  "codecommit:UntagResource",
  "codeconnections:UntagResource",
  "codeguru-reviewer:UntagResource",
  "codepipeline:UntagResource",
  "codestar-connections:UntagResource",
  "codestar-notifications:UntagResource",
  "comprehend:UntagResource",
  "config:UntagResource",
  "controltower:UntagResource",
  "cur:UntagResource",
  "datapipeline:RemoveTags",
  "datazone:UntagResource",
  "detective:UntagResource",
  "directconnect:UntagResource",
  "dlm:UntagResource",
  "dms:RemoveTagsFromResource",
  "docdb-elastic:UntagResource",
  "dsql:UntagResource",
  "dynamodb:UntagResource",
  "ec2:DeleteTags",
  "ecr:UntagResource",
  "ecs:UntagResource",
  "eks:UntagResource",
  "elasticache:RemoveTagsFromResource",
  "elasticloadbalancing:RemoveTags",
  "elasticmapreduce:RemoveTags",
  "emr-containers:UntagResource",
  "emr-serverless:UntagResource",
  "events:UntagResource",
  "fis:UntagResource",
  "fsx:UntagResource",
  "gamelift:UntagResource",
  "geo:UntagResource",
  "globalaccelerator:UntagResource",
  "grafana:UntagResource",
  "guardduty:UntagResource",
  "imagebuilder:UntagResource",
  "internetmonitor:UntagResource",
  "invoicing:UntagResource",
  "iot:UntagResource",
  "ivs:UntagResource",
  "ivschat:UntagResource",
  "kafkaconnect:UntagResource",
  "kendra:UntagResource",
  "kinesisanalytics:UntagResource",
  "kms:UntagResource",
  "lambda:UntagResource",
  "lightsail:UntagResource",
  "m2:UntagResource",
  "medialive:DeleteTags",
  "mediapackage:UntagResource",
  "mediapackagev2:UntagResource",
  "memorydb:UntagResource",
  "network-firewall:UntagResource",
  "networkmanager:UntagResource",
  "notifications-contacts:UntagResource",
  "notifications:UntagResource",
  "oam:UntagResource",
  "observabilityadmin:UntagResource",
  "odb:UntagResource",
  "organizations:UntagResource",
  "payment-cryptography:UntagResource",
  "pipes:UntagResource",
  "qbusiness:UntagResource",
  "ram:UntagResource",
  "rbin:UntagResource",
  "rds:RemoveTagsFromResource",
  "redshift-serverless:UntagResource",
  "redshift:DeleteTags",
  "rekognition:UntagResource",
  "resiliencehub:UntagResource",
  "resource-explorer-2:UntagResource",
  "rolesanywhere:UntagResource",
  "route53-recovery-readiness:UntagResource",
  "route53profiles:UntagResource",
  "route53resolver:UntagResource",
  "rum:UntagResource",
  "s3files:UntagResource",
  "s3tables:UntagResource",
  "s3vectors:UntagResource",
  "sagemaker:DeleteTags",
  "scheduler:UntagResource",
  "secretsmanager:UntagResource",
  "securitylake:UntagResource",
  "servicecatalog:UntagResource",
  "servicediscovery:UntagResource",
  "shield:UntagResource",
  "sns:UntagResource",
  "sqs:UntagQueue",
  "ssm-contacts:UntagResource",
  "ssm-incidents:UntagResource",
  "ssm-quicksetup:UntagResource",
  "ssm:RemoveTagsFromResource",
  "states:UntagResource",
  "storagegateway:RemoveTagsFromResource",
  "synthetics:UntagResource",
  "transfer:UntagResource",
  "verifiedpermissions:UntagResource",
  "vpc-lattice:UntagResource",
  "wafv2:UntagResource",
  "workspaces-web:UntagResource",
  "workspaces:DeleteTags",
  "xray:UntagResource"
]
```

</details>

**2 do not name it, and these are where the warning above actually bites:** `route53:ChangeTagsForResource` and `securityhub:UntagResource`. The reference is silent rather than negative here, so this is not proof the `Deny` fails - but it is the difference between a statement checked and a statement assumed, and these are the ones to verify against the service's own reference page before relying on them.

27 further services have no removal verb resolved in `live/tag-verbs.json` at all, either because the service's model offers more than one candidate or none. They are absent from the list above rather than silently covered by it.
<!-- iamref-gen:end scp-untag-actions -->

Before deploying anything like it:

- **`route53:ChangeTagsForResource`** folds adding and removing tags into
  one call keyed by a "keys to remove" parameter instead of a dedicated
  untag action. It is one of the two actions above the reference does not
  name `aws:TagKeys` on, so verify it against that action's own reference
  page before trusting it rather than assuming the condition keys off it.

- **The management account and any standalone (non-Organizations) account
  are outside SCP reach entirely.** A principal there needs ordinary
  least-privilege IAM to protect the marker keys, because no
  organization-level guardrail reaches it.
- **Nothing here stops the resource from being deleted outright**, only
  its markers being stripped while the resource survives. Outright
  deletion is a different, already-handled case: the next plan's estate
  sweep reports the address as gone and proposes nothing, since there is
  no live resource left to warn about.

### The residual risk, and the last line of defense

Even a correct SCP leaves gaps: the management account, a standalone
account, a compromised or misused exemption for the automation principal,
a service whose untag action does not honor `aws:TagKeys`, or simply a
policy nobody has written yet. Prevention cannot cover every case, which
is why this fork does not rely on it alone.

At plan time, when a declared resource of an admitted type would be
created and the estate sweep saw one or more live resources of the same
type that this estate does not own, the plan runs the same content-match
machinery that offers adoption elsewhere (`internal/live/foreign`'s match
table and its one-to-one rule) against the declared configuration. On a
match it does not change what the plan does (the create may be
intended), but the create's entry in the plan gains a `[POSSIBLE
DUPLICATE]` warning, naming the matched live resource's ID and the exact
command that adopts it instead. A type with no content-match rule (a route
table, an EIP: nothing in their configuration distinguishes one from
another) still gets a generic warning when exactly one same-type unowned
resource exists, naming it the same way. Either way the warning sits
immediately above the plan diff itself, not buried in a report an operator
could plan past without reading. This is the guard that assumes the tags
will get stripped sometime, by someone, despite whatever policy is in
place, and catches it anyway.

Taken together: a tag policy cannot do this job at all, an
SCP narrows who can strip a marker and where, and the plan-time guard
catches it when someone does it regardless.
