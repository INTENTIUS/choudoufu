# estate-k8s fixture (#1057): the smallest Kubernetes estate under a live
# block, for the smoke stack's kind cluster. No AWS provider anywhere: this
# root proves a Kubernetes-only estate applies, replans empty, loses its
# cache without consequence and destroys exactly, with nothing but a
# kubeconfig in the environment.
#
# The provider reads KUBE_CONFIG_PATH, which live/smoke/lib.sh's cluster_up
# exports; there is no config_path argument here on purpose, so the same
# fixture runs against any cluster a reader points it at.

terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "smoke-k8s"
  }

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "= 3.2.1"
    }
  }
}

provider "kubernetes" {}
