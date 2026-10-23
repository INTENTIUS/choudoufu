---
title: "Tutorial: Kubernetes"
description: Apply a small estate to a local kind cluster, plan it again from its labels, and watch the admission policy refuse another estate's write.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/kubernetes/ on this
  machine. Create the kind cluster `tutorial`, write the two files in a new
  empty directory, run every step and show me each check. Run `choudoufu` and
  `kubectl` only while `kubectl config current-context` prints `kind-tutorial`,
  and stop and ask if it prints anything else.
  Never apply or destroy against any other cluster or a cloud account, approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

This run builds a small estate on a kind cluster on your machine and plans it
again with its cache deleted. Then the cluster refuses a write from another
estate.

## Install

Start Docker and install `kubectl` and kind [`v0.33.0`](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0).
This downloads choudoufu `v0.30.0` the way [Install](../../use/start/#install) does.

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download v0.30.0 -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz
```

Put `choudoufu` on your `PATH`, and check that `choudoufu version` runs.

## Start the cluster

```
kind create cluster --name tutorial
```

kind `v0.33.0` starts Kubernetes `v1.37.0`, and `kubectl config current-context`
prints `kind-tutorial` once it is up.

## Install the estate boundary

An admin installs the [admission policy](../../../kubernetes/gate/) once per
cluster, then creates the namespace where the estate keeps its records:

```
kubectl apply -f https://raw.githubusercontent.com/INTENTIUS/choudoufu/v0.30.0/live/kubernetes/estate-boundary.yaml
kubectl create namespace tofu-records-tutorial
```

kubectl reports each object as created.

## Write the estate

In an empty directory, `estate.chdf.hcl`, the live backend:

```hcl
estate = "tutorial"

record_store "kubernetes" {
  config_path    = "~/.kube/config"
  allow_insecure = ["encryption_at_rest"]
}
```

`main.tf`:

```hcl
terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "3.2.1"
    }
  }
}

provider "kubernetes" {
  config_path = "~/.kube/config"
}

resource "kubernetes_namespace_v1" "app" {
  metadata {
    name = "tutorial"
  }
}

resource "kubernetes_config_map_v1" "app" {
  metadata {
    name      = "app-config"
    namespace = "tutorial"
  }
  data = {
    greeting = "hello"
  }
  depends_on = [kubernetes_namespace_v1.app]
}

resource "kubernetes_service_account_v1" "app" {
  metadata {
    name      = "app"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
}

resource "kubernetes_service_v1" "app" {
  metadata {
    name      = "app"
    namespace = "tutorial"
  }
  spec {
    selector = {
      app = "app"
    }
    port {
      port        = 80
      target_port = 8080
    }
  }
  depends_on = [kubernetes_namespace_v1.app]
}
```

## Init and apply

```
choudoufu init
choudoufu apply
```

Init prints `OpenTofu has been successfully initialized!`. Type `yes` at the
prompt, and the apply ends with:

```
Apply complete! Resources: 4 added, 0 changed, 0 destroyed.
```

## See the markers

Any kubectl reads them back. The label names the estate, and the annotation names the block.

```
kubectl get configmap app-config -n tutorial -o jsonpath='{.metadata.labels}'
kubectl get configmap app-config -n tutorial -o jsonpath='{.metadata.annotations.choudoufu\.intentius\.io/tofu-address}'
```

```
{"tofu-estate":"tutorial"}
kubernetes_config_map_v1.app
```

`live-ls` finds the estate by its label, with no state file:

```
choudoufu live-ls -estate=tutorial .
```

```
Estate "tutorial": 4 resource(s) carry its marker.
```

## Plan without the cache

```
rm .terraform/choudoufu-cache.tfstate
choudoufu plan
```

With nothing cached, the plan reads the cluster and reports `No changes.`

## Refuse another estate's write

A second team gets the ordinary `edit` role and a grant for its own estate, `team-b`:

```
kubectl create serviceaccount team-b -n default
kubectl create clusterrolebinding team-b-edit --clusterrole=edit --serviceaccount=default:team-b
curl -fsSL https://raw.githubusercontent.com/INTENTIUS/choudoufu/v0.30.0/live/kubernetes/estate-grant.yaml \
  | sed 's/PRINCIPAL_NAMESPACE/default/g; s/PRINCIPAL/team-b/g; s/ESTATE/team-b/g' | kubectl apply -f -
```

As `team-b`, try to delete your ConfigMap:

```
kubectl --as=system:serviceaccount:default:team-b delete configmap app-config -n tutorial
```

The policy reads `tofu-estate=tutorial` off the object and refuses:

```
configmaps "app-config" is forbidden: ValidatingAdmissionPolicy 'choudoufu-estate-boundary' with binding 'choudoufu-estate-boundary' denied request: tofu-estate=tutorial fences this object and system:serviceaccount:default:team-b is not bound to that estate (no "use" on estates.choudoufu.intentius.io named tutorial)
```

Your ConfigMap stands, so `choudoufu plan` still reports `No changes.`

## Next

- [Start a new estate](../../use/start/)
- [Migrate an estate](../../use/migrate/)
- [Kubernetes](../../../kubernetes/)
