---
title: "Claim 25: A held delete is not a finished delete"
claim: k8s-a-held-delete-is-not-gone
---

# Claim 25: A held delete is not a finished delete

A finalizer turns DELETE into a request. The API server sets
`metadata.deletionTimestamp`, returns success, and the object stays in the
cluster until the controller that registered the finalizer takes it off.
Operators meet this every week: a backup operator, a policy agent, an
owner-reference collector, a CRD's own controller.

Stock reads what exists from a state file. The delete succeeded, so the
entry goes, and the object it names is never mentioned by any plan again -
a silent orphan made by a successful delete. Here the object is still the
estate's because it still carries `tofu-estate`, the sweep lists it like
any other live object, and the plan proposes the same one destroy on every
run until the object is really gone. When the finalizer clears, the object
goes and the plan is empty. No import, no state edit, no `-refresh-only`.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-a-held-delete-is-not-gone

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-a-held-delete-is-not-gone and report the "caught"
line: the control takes the finalizer off before the destroying apply,
and the object must then really go.
```

The steps, in the order they print:

0. `a namespace the estate does not own` - made with kubectl, so nothing
   in the run waits on a namespace delete.
1. `the estate applies` - two ConfigMaps, both carrying
   `tofu-estate=smoke-k8s`.
2. `an operator puts a finalizer on one of them` - out of band, with
   kubectl. Nothing in the configuration mentions it.
3. `the block is deleted from source` - the plan proposes exactly one
   destroy, found by the label.
4. `apply - the run says destroyed, the cluster says terminating` - the run
   prints `Destruction complete after 0s` and counts one destroyed, then
   warns `Delete accepted, object not gone`, naming `ConfigMap
   smoke-k8s/held-config` and the finalizer holding it; the object is still
   there, with a `deletionTimestamp` and its label.
5. `the next plan - the sweep finds it again, by the same label` - the same
   one destroy, twice over, because that is what every plan says until the
   object is gone.
6. `the finalizer clears - the object goes and the plan is empty`.
7. `apply -destroy over a held object` - the run reports the estate
   destroyed and exits 0 with one object still in the namespace, and the
   same warning names that one object and not the one that really went; the
   plan after it proposes exactly the one create that is genuinely missing,
   not two.
8. `clear the hold and put the namespace back`.

The `BREAK=1` run removes the finalizer just before the apply in step 3 and
requires the opposite outcome: the object gone in one apply, no warning,
and the replan empty. Without that control the whole scenario would read the same if
choudoufu simply never deleted a ConfigMap, and the finalizer would be
scenery.

## The line that is not true, and the warning after it

`Destruction complete after 0s` and `Resources: 0 added, 0 changed, 1
destroyed` are the provider's word for "the API accepted the delete", not
for "the object is gone". Stock prints the same two lines, so they are left
exactly as they are and the scenario asserts them verbatim, because they
are what a user sees. choudoufu is the one in a position to know better,
and since [#1184](https://github.com/INTENTIUS/choudoufu/issues/1184) it
says so in the same run: after the apply, for each kind the run deleted
anything of, it lists the estate's objects of that kind once by the
`tofu-estate` label, and every object it deleted that is still there with a
`deletionTimestamp` is named in one warning, with its finalizers:

```text
Warning: Delete accepted, object not gone

The API server accepted the delete of 1 object and it is still in the
cluster, terminating:

  - ConfigMap smoke-k8s/held-config (kubernetes_config_map.orphan_smoke-k8s_held-config), finalizers: smoke.choudoufu.io/hold

It stays until those finalizers are removed, and the next plan will propose
destroying it again. To see what holds it:
  kubectl get configmap held-config -n smoke-k8s -o jsonpath='{.metadata.finalizers}'
```

It is a warning and the exit code is the apply's. A run that deleted
nothing on the cluster makes no request for it. It is a Kubernetes check
only: no AWS call is added, because no AWS listing this fork already makes
reports an accepted, unfinished delete.

The promise this claim makes is still about the run after it. The summary
count is wrong for as long as the finalizer holds; the plan never is.

## What is not measured here

A `kubernetes_namespace` delete *does* wait for the namespace to be gone,
and a finalizer inside it blocks that wait for the provider's whole delete
timeout - five minutes, then `timeout while waiting for resource to be
gone (last state: 'Terminating', timeout: 5m0s)`, exit 1, the namespace
left `Terminating`. That is honest, and loud, but it is the provider's
timer rather than this claim's subject, and paying it twice a run buys
nothing. It is recorded on #1184.

That the destroy proposal in steps 3 and 5 is the *marker's* doing is
proved by [claim 1 on Kubernetes](no-silent-orphans.md#on-kubernetes),
whose control strips the label from an orphan and requires the replan to
leave it alone.
