# reference-k8s-helm-cloudnative-pg (#1977): the CloudNativePG operator chart,
# cloudnative-pg 0.29.1 (app 1.30.1, Apache-2.0), rendered client-side by the
# hashicorp/helm provider's helm_template data source and applied as
# kubernetes_manifest blocks - the shape live/kubernetes/COMPATIBILITY.md
# recommends for a Helm chart. No helm_release: Helm never talks to the
# cluster here.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so the one name the chart
# gives four kinds (rel-cloudnative-pg: a ServiceAccount, a ClusterRole, a
# ClusterRoleBinding and the Deployment) stays four distinct instances, and
# the manifest is the element itself: `manifest = each.value` (#1962).
#
# The chart renders its eleven CRDs from templates/ (not crds/), gated by
# crds.create, so include_crds changes nothing here; it is set so the shape
# matches the other Helm estate. Unlike kube-prometheus-stack it renders no
# custom resource of them: the one database this estate runs is the
# Cluster below, declared in this file.
#
# Deltas from the chart's render, and the ones that turned out not to be
# needed:
#
# - No false/zero omitempty field is dropped before yamldecode. The render's
#   built-in kinds carry exactly one `: false`, the container's
#   allowPrivilegeEscalation, which is a *bool and survives the round trip;
#   hostNetwork is templated behind an `if`, the probes' delays are 3, and
#   the CRDs' `default: false` / `minimum: 0` sit inside JSONSchemaProps
#   values (*JSON, *float64), never as plain omitempty fields.
# - No webhook certificate is switched off: the chart has no hook, no
#   cert-gen Job and no caBundle. The operator writes its own CA and serving
#   certificate Secrets at start-up and patches caBundle into the two
#   webhook configurations itself.
# - config.data, the one key that rendered as an empty map, is given an
#   entry in values.yaml.
#
# Four blocks for the render, because a root cannot plan a CRD and an
# object of it in one pass: the chart's CRDs are pre-applied on their own at
# cold deploy (#1173, declared in live/gauntlet/estates.json), then the rest,
# then the operator's Deployment, then the database.

data "helm_template" "cnpg" {
  name         = "rel"
  namespace    = "cnpg-system"
  chart        = "${path.module}/charts/cloudnative-pg-0.29.1.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>=1.29.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "cnpg_system" {
  metadata {
    name = "cnpg-system"
  }
}

# The database's own namespace. Not the operator's: in its own namespace the
# operator clones its monitoring-queries ConfigMap into the Cluster's
# namespace under the chart's name, cnpg-default-monitoring, with no owner
# reference - one more object nothing declares, same-named with a declared
# one in cnpg-system. In cnpg-system it would skip the copy.
resource "kubernetes_namespace_v1" "pg" {
  metadata {
    name = "pg"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cnpg.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cnpg.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.cnpg_system, kubernetes_manifest.crds]
}

# The operator's Deployment, after its RBAC and its ConfigMaps, and waited
# for: both webhooks are failurePolicy: Fail, so the Cluster below is
# rejected until the operator answers on the webhook Service. The pod is
# Ready only once the webhook server is up, which is after the operator has
# written its certificates and patched caBundle in.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cnpg.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "Deployment"
  }
  manifest = each.value

  wait {
    rollout = true
  }

  depends_on = [kubernetes_manifest.rest]
}

# One PostgreSQL instance on kind's default StorageClass. The operator
# creates the instance Pod, its PersistentVolumeClaim, Secrets, Services and
# the rest of a database from this one object, owned by it and declared
# nowhere; once migrate stamps the Cluster they carry the estate's label
# too (INHERITED_LABELS in values.yaml).
resource "kubernetes_manifest" "cluster" {
  manifest = {
    "apiVersion" = "postgresql.cnpg.io/v1"
    "kind"       = "Cluster"
    "metadata" = {
      "name"      = "pg"
      "namespace" = "pg"
    }
    "spec" = {
      "instances" = 1
      "imageName" = "ghcr.io/cloudnative-pg/postgresql:18.6-minimal-trixie"
      "storage" = {
        "size" = "1Gi"
      }
    }
  }

  depends_on = [kubernetes_namespace_v1.pg, kubernetes_manifest.late]
}

# day2_count's for_each: ImageCatalogs, a namespaced custom kind of the
# chart's CRDs that no webhook admits and nothing reconciles into other
# objects. The chart has no values map that renders N objects, so the
# count lives here; run.sh edits catalog_shards by exact match.
locals {
  catalog_shards = 0
}

resource "kubernetes_manifest" "catalog" {
  for_each = toset([for i in range(local.catalog_shards) : "shard-${i}"])
  manifest = {
    "apiVersion" = "postgresql.cnpg.io/v1"
    "kind"       = "ImageCatalog"
    "metadata" = {
      "name"      = "catalog-${each.key}"
      "namespace" = "pg"
    }
    "spec" = {
      "images" = [{
        "major" = 18
        "image" = "ghcr.io/cloudnative-pg/postgresql:18.6-minimal-trixie"
      }]
    }
  }

  depends_on = [kubernetes_namespace_v1.pg]
}
