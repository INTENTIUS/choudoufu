# Gate

On AWS the marker is an authorization primitive out of the box: an IAM
condition on `aws:ResourceTag` fences reads and writes per resource, and
handover is two IAM changes. Kubernetes does not have that. What it has is
admission, and this page says in the headline what admission fences and
what it does not.

## What RBAC cannot do

A `PolicyRule` has exactly `verbs`, `apiGroups`, `resources`,
`resourceNames` and `nonResourceURLs`. There is no selector, no condition
and no attribute predicate. `resourceNames` is a static allowlist of names.
Nothing in RBAC can say "may update objects carrying this label".

## What admission does

```
kubectl apply -f live/kubernetes/estate-boundary.yaml
```

One `ValidatingAdmissionPolicy`, GA in `admissionregistration/v1`,
installed once by a cluster admin. Its CEL reads `tofu-estate` off
`oldObject`, the object a write is about to change, which is the
`aws:ResourceTag` semantic, and off `object`, the object the write would
produce, which is the `aws:RequestTag` semantic. For each it asks the API
server's own authorizer whether the caller holds `use` on a virtual
resource named after the estate, `estates.choudoufu.intentius.io/<estate>`.
No such resource exists; the verb lives only in RBAC, which is the point.

So the grant is an ordinary ClusterRole
(`live/kubernetes/estate-grant.yaml`): `use` on `estates` named
`<estate>`, bound to a principal. Handover is that binding moving from one
principal to another. Nothing on the objects changes and the policy is
never edited. A `cluster-admin`'s wildcard rule matches the virtual
resource too, so `cluster-admin` holds every estate, the way the account
root does on AWS. `live/MARKERS.md`, "Granting a Kubernetes estate", has
both templates in full.

The fence binds the credential, not the binary. A plain `kubectl label`
under a ServiceAccount that does not hold the estate is refused by the API
server with the policy's own message, and so is choudoufu's own apply
under the same ServiceAccount. What the fence permits is not hidden from
the tool either: the next plan reads the live object, not a log of who
wrote it.

Splitting an estate is a label rewrite, then a grant. With no address on
the object, the write is `tofu-estate=<new>` on the object - `live-mv
-from-estate` makes it through the provider, and `kubectl label
--overwrite` makes the same write tool-less - and the policy reads both
sides of it: the caller must hold the estate the object is leaving and the
one it is entering.

## What it does not fence

Three things are true of this fence that are not true of the AWS one:

- Admission sees create, update and delete, never get or list. The fence
  is write-only where an IAM condition can fence a describe; reads are
  RBAC's alone.
- It fences the object, not its subresources. A `kubectl scale` or a
  status write arrives as a Scale or a status object carrying no label
  (measured on Kubernetes 1.36; a `*/*` rule does not change it). RBAC on
  `deployments/scale` is the fence for those.
- The policy is one shared cluster object with a wider blast radius than
  two IAM changes. A cluster admin installs it and any cluster admin can
  remove it, which is exactly what the `BREAK=1` run of claim 13 on
  Kubernetes does.

The fence is also per estate, never per address: the label carries no
address by ruling, so a team that wants two boundaries makes two estates.

## What is exempt

Only the control plane is exempt, by name: nodes, the API server, the
scheduler and the controller manager's own controllers. If anything else in
kube-system is refused with "is not bound to it", grant it the estate:

```
sed -e 's/ESTATE/app/g' \
    -e 's/PRINCIPAL_NAMESPACE/kube-system/g' \
    -e 's/PRINCIPAL/NAME/g' \
    live/kubernetes/estate-grant.yaml | kubectl apply -f -
```

Do not add it to the installed policy's list: `estate_boundary` fails a
cluster whose policy is not the one this release ships.

Owned objects keep their estate ([#1449](https://github.com/INTENTIUS/choudoufu/issues/1449)).
An object that already carries an `ownerReference` may be updated with no
grant at all while its `tofu-estate` label stays exactly as it was, which
is what a third-party operator's status-like writes on a labelled child
need. Changing that label, stripping it, deleting the object or creating a
new labelled one needs `use` on every estate involved, owner or no owner.
Until #1449 the policy skipped any object with an `ownerReference`
outright, and `ownerReferences` is a field the caller writes, so one
estate's identity could add an owner to its own object and then relabel it
into an estate it was never granted. An operator that creates labelled
children of its own (cert-manager, an ingress controller) needs `use` on
that estate: one ClusterRoleBinding from
`live/kubernetes/estate-grant.yaml`. The estate sweep still excludes owned
objects ([Operate](https://intentius.io/choudoufu/kubernetes/operate/)), so
what the fence now judges is wider than what the sweep discovers.

Kyverno and Gatekeeper could express the same policy. Neither has been
verified for this.
