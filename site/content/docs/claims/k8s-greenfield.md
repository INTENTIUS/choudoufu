---
title: "Claim 21: The marker is a label: one tofu-estate label on a real cluster"
weight: 21
claim: k8s-greenfield
---

# Claim 21: The marker is a label: one tofu-estate label on a real cluster

On AWS the marker is two tags, because AWS hands back opaque ids and the
object has to carry the configuration address that owns it. Kubernetes
returns the natural key, group, kind, namespace and name, with the name
authored in the configuration, so the object carries one label,
`tofu-estate`, and nothing else. This is the first claim that runs on a
real API server rather than an emulator: a kind cluster in Docker, created
for the run and deleted after it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-greenfield

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-greenfield and report the "caught" line: the
scenario strips the label with kubectl and the replan must propose
restoring it.
```

The steps, in the order they print:

1. `a Kubernetes estate, one plain apply` - a namespace and a ConfigMap
   under a `live` block with no AWS provider anywhere; no
   `terraform.tfstate` appears.
2. `the marker, read back with kubectl - no choudoufu in the loop` - the
   ConfigMap and the namespace both carry `tofu-estate=smoke-k8s`, and
   neither carries a `tofu-address`.
3. `the replan - prior state rebuilt from the cluster` - empty.
4. `the state cache - present, disposable, and never trusted` - deleted,
   and the replan is still empty.
5. `destroy - exactly what was made` - two objects destroyed,
   `kube-system` untouched.

The `BREAK=1` run removes the label with `kubectl label configmap
app-config -n smoke-k8s tofu-estate-` after step 2. The next plan must
propose exactly one in-place change, restoring `tofu-estate`; if it were
still empty, the label would not be what the plan reads and every
empty-plan assertion above would be scenery. That is also the
`marker_repair` default at work: a stripped marker is repaired by the next
apply.

What this claim does not say: nothing lists an estate's objects across
kinds yet (the sweep is [#1016](https://github.com/INTENTIUS/choudoufu/issues/1016)'s
next unit), and nothing fences a write on the label until an admission
policy is installed. The label answers "who owns this" to any reader
today, and is what such a policy will read.
