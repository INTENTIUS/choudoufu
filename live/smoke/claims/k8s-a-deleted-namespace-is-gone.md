---
title: "Claim 46: Namespace deletion"
claim: k8s-a-deleted-namespace-is-gone
---

# Claim 46: Namespace deletion

## On Kubernetes

`kubectl delete namespace` deletes every object in the namespace, whoever
owns it, in one call nobody's plan made. Stock finds out from its refresh:
its state file still lists each object, the provider reads each one as
gone, and the plan proposes them again. This fork keeps no state to
refresh from. It lists the estate by the `tofu-estate` label, so what has
to be shown is that the listing and the plan agree the objects are gone,
and that the plan is then the one stock makes.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind, kubectl and terraform are installed. If Go is
not installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-a-deleted-namespace-is-gone

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-a-deleted-namespace-is-gone and report the
"caught" line: the control asserts an empty plan after the delete, and
that assertion must fail.
```

Two estates, each with a stock twin applying the identical shape into a
namespace of its own with a state file, so every plan is compared with
stock's plan from the same position on the same cluster:

- `smoke-nsd` declares its namespace (`kubernetes_namespace.app`), names it
  from a ConfigMap and a ServiceAccount through a reference, and keeps its
  records in the cluster (`record_store "kubernetes"`).
- `smoke-nsu` lives in a namespace the platform team made with kubectl,
  on the implied local store.

The steps, in the order they print:

1. `two estates and their stock twins apply`.
2. `before the fault, every plan is empty`.
3. `kubectl delete namespace, under all four` - and the step waits for
   each namespace to finish terminating.
4. `declared namespace` - `live-ls` lists no object; the plan proposes the
   three creates stock proposes, nothing read as present and no orphan;
   at `-parallelism=1` the namespace's create completes before either
   object inside it is dispatched, on both sides; the replan is empty.
5. `undeclared namespace` - the same two creates as stock. Neither tool
   can create into a namespace that is not there, so both applies fail
   with the API server's `namespaces "..." not found`, once per object.
   Once the namespace is back, one apply converges.
6. `a namespace still terminating` - a finalizer on the ConfigMap holds
   the namespace in `Terminating`. This is
   [claim 25](k8s-a-held-delete-is-not-gone.md)'s shape, so it is
   measured and reported rather than folded in: the plan reads the
   terminating namespace and the held ConfigMap as present, as stock's
   refresh does, and proposes the one create of the ServiceAccount the
   delete already took. An apply in that window is the API server's to
   refuse.
7. `the record store's namespace deleted` - the plan and the apply are
   both refused with `Cannot open the record store`, naming
   `namespace "tofu-records-smoke-nsd" does not exist` and the
   `kubectl create namespace tofu-records-smoke-nsd` line, as
   [OPERATE.md](../../kubernetes/OPERATE.md) says. The refused apply
   creates nothing: not the store's namespace, not the estate's, no
   object and no record. Once the namespace is back, one apply converges.
8. `teardown`.

The `BREAK=1` run makes the same delete and then asserts what a sweep that
never saw it would plan, `No changes.`, over the real plan. That assertion
must fail: the control passes only when the plan proposes the three
creates.

## What is not measured here

This is fault 5 of [#1110](https://github.com/INTENTIUS/choudoufu/issues/1110)
([#1765](https://github.com/INTENTIUS/choudoufu/issues/1765)). It is a claim
rather than a gauntlet stage; the stage form would re-measure every
kubernetes-lane estate, and `live/kubernetes/FAULTS.md` records why the
claim was taken instead.

A `kubernetes_namespace` destroy that waits on a namespace held by a
finalizer is the provider's own five-minute timer, recorded on claim 25 and
not repeated here.
