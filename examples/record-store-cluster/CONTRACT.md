# The four things a record store cluster must have

choudoufu asserts four settings about the cluster behind
`record_store "kubernetes"`, on an estate's first contact with the store and
before any run that writes a record, and refuses a run whose cluster fails
one that could be read. Each is asked the way that can answer honestly, and
each exists because of something specific about what a record is. The
assertions and their words are `internal/live/staterecord/kubernetescontract.go`;
this page says what they mean for the two identities `just up` creates.

| Setting | What is asserted | How it is asked |
|---|---|---|
| `namespace_access` | The records namespace exists, and this identity may do to Secrets in it what the store will ask | One `SelfSubjectAccessReview` per verb, never an attempted write |
| `read_isolation` | This identity may not read Secrets outside the records namespace, and no other estate keeps records in it | `SelfSubjectAccessReview` for `get` and `list` on Secrets in every other namespace it can see |
| `encryption_at_rest` | The API server was started with an `EncryptionConfiguration` | The `kube-apiserver` Pod's flags in `kube-system`, where that Pod is visible |
| `estate_boundary` | `live/kubernetes/estate-boundary.yaml`'s policy and binding are installed and in force, and this identity holds `use` on its estate | The `ValidatingAdmissionPolicy` and its binding, compared with the shipped file, plus one review of the `use` verb |

A fifth line, `tls_verification`, appears only when the block sets
`insecure = true`, and it fails.

## namespace_access

Every record this estate keeps is a Secret in that namespace. A verb the
store needs and does not have stops a run part-way through writing records,
which leaves the estate half-recorded, and a records namespace that is not
there reads as an estate with no records at all. So the namespace is checked
to exist, and the identity is reviewed for each verb in turn, never by
attempting a write: a write that probes permission has to write something,
and the thing it would write is a record. Which verbs are required depends
on the run. An apply, a `live-mv` that is not a dry run and a `live-import
-approve` need `get`, `list`, `create`, `update` and `delete`. A plan needs
`get` and `list`, and is refused for lacking nothing else, because the one
write on its path is the provisioning sentinel, which an earlier writing
run already left behind. The `choudoufu-plan` ServiceAccount holds exactly
the two; `choudoufu-apply` holds exactly the five. Neither holds `patch` or
`watch`, because the store never patches or watches, and a verb nobody uses
is a verb nobody notices being abused.

## read_isolation

The namespace is the read boundary and there is no other one. RBAC cannot
condition on a label, and admission is never consulted for a `get` or a
`list`, so an identity that may read Secrets outside this namespace reads
every other estate's records in this cluster, and every record holds
whatever the resource it records holds. This is why both Roles here are
`Role`s and not `ClusterRole`s, bound in the records namespace and nowhere
else, and why the selftest asks the authorizer that neither identity may
`get` Secrets in `default`, `list` them in `kube-system`, or `list` them
`--all-namespaces`. The same check refuses a namespace that holds another
estate's record Secrets: two `record_store` blocks pointed at one namespace
cannot be fenced from each other by any Role, so each estate gets its own
namespace, which is the default and what `just up` creates. Where the
identity cannot enumerate namespaces the check reports what it could not
review rather than passing it.

## encryption_at_rest

A Secret is base64, not encryption. Without an `EncryptionConfiguration`
the API server writes each record's payload into etcd as it came, so
anything that reads etcd or an etcd backup reads every record in the
estate. The only thing that carries the API server's
`--encryption-provider-config` flag through the API is the API server's own
Pod in `kube-system`, so on kind and on a kubeadm cluster whose control
plane this identity can see, the flag's absence is read and refused. Its
presence is not read as a pass: the file the flag names is not an API
object, and a configuration whose first provider for secrets is `identity`
sets the flag and encrypts nothing, so a cluster with the flag reports
`NOT CHECKED` and names the file to read on the control-plane node. On a
managed control plane (EKS, GKE, AKS) there is no API server Pod to see at
any permission level, and the answer is `NOT CHECKED` there too. Neither
`just up` nor the two Roles can change any of this; it is the cluster's,
and the apply and plan identities do not need to list Pods in `kube-system`
for the store to work. A `NOT CHECKED` finding warns on every run that
writes a record and never refuses one, because refusing it would refuse
every correctly scoped identity, and a gate everyone waives on their first
day protects nothing. `choudoufu live-cluster` still calls the cluster not
correct until somebody who can read the answer has.

## estate_boundary

The record Secrets carry the estate's `tofu-estate` label, and
`live/kubernetes/estate-boundary.yaml`'s `ValidatingAdmissionPolicy` is what
stops an identity bound to another estate from writing them. Without it in
force, any identity with write access to this namespace can overwrite or
delete another estate's records, and a record can be the only copy of what
it says. The check reads the installed policy and its binding and compares
them with the shipped file, so a same-named policy whose CEL is `true`,
whose `failurePolicy` is `Ignore`, or whose binding is scoped away from the
records namespace does not pass. Installing the policy is a cluster admin's
act, done once per cluster, and is not part of `just up`. What `just up`
does do is grant the estate to the apply identity, with the shipped
`live/kubernetes/estate-grant.yaml`: one `ClusterRole` giving `use` on
`estates.choudoufu.intentius.io/<estate>` and one binding handing it to
`choudoufu-apply`. That verb exists nowhere but in RBAC; the policy's CEL
asks the authorizer for it on every write to a labelled object. The plan
identity is deliberately not granted, since the boundary fences writes and
a plan makes none. Handing an estate over is that binding moving to another
principal, and nothing on the objects changes.

## When it is checked, and the waivers

Not on every plan. The four are facts about the cluster that do not change
between two plans. They are asked on an estate's first contact with the
store, whatever the command, before any run that writes a record, and
whenever somebody runs `just verify <estate>`, which is `choudoufu
live-cluster` under the current kubeconfig's identity.

`allow_insecure` in the `record_store "kubernetes"` block names the
assertions an estate proceeds without, by setting: `"namespace_access"`,
`"read_isolation"`, `"encryption_at_rest"`, `"estate_boundary"`, and
`"tls_verification"`. Each waiver is announced on every run with what it
costs, in the store's own words. A plain kind cluster is refused twice on
first contact, for the missing encryption flag and the missing boundary
policy, and the refusal carries the one-line waiver to paste. `just verify`
never honours a waiver: it reports the cluster, not the configuration.
