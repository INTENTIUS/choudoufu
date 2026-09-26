---
title: "Claim 7: Identity is a tag you can read and move"
claim: identity-is-a-tag
---

# Claim 7: Identity is a tag you can read and move

## On AWS

Because ownership lives on each resource as two tags, three things
follow that stock cannot offer: estates in one account are isolated by
construction, any AWS tool can answer ownership without this tool
present, and renaming a resource in code is a tag rewrite where stock
demands `state mv` surgery.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke identity-is-a-tag

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke identity-is-a-tag and report the "caught" line: it
renames the resource in code but skips the retag, and the plan must
propose the destroy-and-recreate stock would inflict.
```

The steps as they print:

1. `two estates stand up in one account` - two copies of the estate,
   different estate tags, one account. Nothing else separates them.
2. `any AWS tool answers ownership` - the plain CLI's tagging API lists
   each estate's resources and reads a resource's address tag. No
   choudoufu involved.
3. `neither estate can see the other` - both plans are clean, and
   neither plan output ever names the other estate's resources.
4. `a rename is a retag, not surgery` - `aws_vpc.main` becomes
   `aws_vpc.core` in code, `live-mv` rewrites the address tag on the
   live resource, and the next plan is clean. No state file was edited,
   because there is none to edit.
5. `teardown - both estates`, each by its own destroy.

The `BREAK=1` run skips `live-mv` after the code rename. The live vpc
still wears the old address, so the plan must treat the new name as
missing and the old one as orphaned - stock's destroy-and-recreate,
demonstrated as what the retag saves you from.

## On Kubernetes

On AWS the marker is two tags, because AWS hands back opaque ids and the
object has to carry the configuration address that owns it. Kubernetes
returns the natural key, group, kind, namespace and name, with the name
authored in the configuration, so the object carries one label,
`tofu-estate`, and nothing else. This was the first proof that ran on a
real API server rather than an emulator: a kind cluster in Docker, created
for the run and deleted after it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-greenfield

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-greenfield and report the "caught, twice" line:
the scenario strips the label with kubectl, live-ls must drop the object
from its listing, and the replan must refuse the object by name and
propose the create the block declares.
```

The steps, in the order they print:

1. `a Kubernetes estate, one plain apply` - a namespace, a ConfigMap, a
   ServiceAccount and a Service under a `live` block with no AWS provider
   anywhere; no `terraform.tfstate` appears.
2. `the marker, read back with kubectl - no choudoufu in the loop` - the
   ConfigMap and the namespace both carry `tofu-estate=smoke-k8s`, and
   neither carries a `tofu-address`.
3. `the inventory - live-ls lists the estate by its label, no state file` -
   `choudoufu live-ls -estate=smoke-k8s .` reads the provider block to
   learn the substrate, lists the cluster the way the sweep does (one
   label-selected list per kind), and prints all four objects by kind and
   natural key, each joined to the block that declares it on the kind and
   the natural key, since the object carries no address
   ([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)):

   ```text
   kubernetes_config_map        smoke-k8s/app-config
     kind:    ConfigMap (v1)
     address: kubernetes_config_map.app  (declared)
   kubernetes_namespace         smoke-k8s
     kind:    Namespace (v1)
     address: kubernetes_namespace.app  (declared)
   ```

   No AWS call is attempted: a configuration with a kubernetes provider
   and no aws provider lists the cluster alone.
4. `the replan - prior state rebuilt from the cluster` - empty.
5. `the state cache - present, disposable, and never trusted` - deleted,
   and the replan is still empty.
6. `an api_version change is not a move` - the ConfigMap block's type is
   rewritten from `kubernetes_config_map` to `kubernetes_config_map_v1`
   with the same metadata and no `moved` block, and the replan is empty:
   both spellings name the same object
   ([#1081](https://github.com/INTENTIUS/choudoufu/issues/1081)).
7. `destroy - exactly what was made` - four objects destroyed, the
   ConfigMap through its new spelling, `kube-system` untouched.
8. `the inventory after destroy - empty` - the same `live-ls` reports
   `Nothing found.`

The `BREAK=1` run removes the label with `kubectl label configmap
app-config -n smoke-k8s tofu-estate-` after step 3. `live-ls` must then
list three objects and not the ConfigMap - if it still listed it, the
inventory would not be reading the label - and the next plan must propose
exactly one in-place change, restoring `tofu-estate`; if it were still
empty, the label would not be what the plan reads and every empty-plan
assertion above would be scenery. That is also the `marker_repair` default
at work: a stripped marker is repaired by the next apply.

What this proof does not say: nothing fences a write on the label until
an admission policy is installed ([claim 13 on
Kubernetes](the-tag-is-the-boundary.md#on-kubernetes)), and an
object nobody declares is the sweep's business ([claim 1 on
Kubernetes](no-silent-orphans.md#on-kubernetes)), not this
listing's: `live-ls` reports such an object as undeclared and proposes
nothing.
