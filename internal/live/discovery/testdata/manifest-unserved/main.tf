terraform {
  live {
    estate = "smoke-crd"
  }
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_config_map_v1" "plain" {
  metadata {
    name      = "plain"
    namespace = "smoke-crd"
  }
}

resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "smoke-crd"
    }
  }
}

resource "kubernetes_manifest" "many" {
  for_each = toset(["a", "b"])
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = each.key
      namespace = "smoke-crd"
    }
  }
}

resource "kubernetes_manifest" "cm" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "via-manifest"
      namespace = "smoke-crd"
    }
  }
}
