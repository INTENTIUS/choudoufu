---
title: "Claim 24: A custom resource binds by its natural key"
weight: 24
claim: k8s-custom-resource
---

# Claim 24: A custom resource binds by its natural key

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

What this claim does not say, on purpose: the object carries no
`tofu-estate` label yet. The stamp into `manifest.metadata.labels` is the
ruling's next unit, and until it lands no sweep finds an object declared
this way and no admission policy fences it. It is bound by its declaration
alone, the way an untaggable AWS type is.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and kind and kubectl are installed. If Go is not
installed, export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke k8s-custom-resource

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke k8s-custom-resource and report the "caught" line:
the scenario deletes the custom resource with kubectl and the replan
must propose creating it.
```

The steps, in the order they print:

1. `a CRD the cluster serves, installed with kubectl` - the CronTab CRD
   from the Kubernetes documentation, established before the estate plans,
   because the provider reads a custom kind's schema from the cluster.
2. `the estate applies: a namespace and a custom resource, no state file` -
   two objects; kubectl reads the CronTab's spec back.
3. `the replan - prior state rebuilt from the cluster by the natural key` -
   empty.
4. `the cache is disposable` - the replan without it is still empty.
5. `destroy - exactly what was made` - the CronTab goes; the CRD, which
   nothing declared, stands.

The `BREAK=1` run deletes the CronTab with kubectl after step 2. The
replan must propose creating `kubernetes_manifest.crontab`: if it stayed
empty, nothing was reading the cluster by that key and every empty replan
above would be scenery.
