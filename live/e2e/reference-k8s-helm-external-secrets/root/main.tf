# reference-k8s-helm-external-secrets (#1976): the External Secrets Operator's own
# chart, external-secrets 2.12.0 (app v2.12.0, Apache-2.0), rendered
# client-side by the hashicorp/helm provider's helm_template data source and
# applied as kubernetes_manifest blocks - the shape
# live/kubernetes/COMPATIBILITY.md recommends for a Helm chart, as in
# reference-k8s-helm-template. No helm_release: Helm never talks to the
# cluster here.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults: the validating webhook and its
# cert-controller off, and three custom resources (a SecretStore, a
# ClusterSecretStore and an ExternalSecret on the fake provider) added
# through the chart's own extraObjects.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so the one name the chart
# and extraObjects share across five kinds stays five distinct instances,
# and the manifest is the element itself: `manifest = each.value` (#1962).
#
# Four blocks. The chart's 25 CRDs are pre-applied on their own at cold
# deploy (#1173, declared in live/gauntlet/estates.json), because a root
# cannot plan a CRD and an object of it in one pass. The other three are one
# apply, ordered by depends_on: the RBAC and the ServiceAccount, then the
# controller's Deployment, then the custom resources.

data "helm_template" "eso" {
  name         = "rel"
  namespace    = "external-secrets"
  chart        = "${path.module}/charts/external-secrets-2.12.0.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0). The chart's own
  # constraint (>= 1.19.0-0) would pass, but its templates gate fields on
  # the version (hostUsers from 1.33), so it is pinned rather than read from
  # the cluster and the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "external_secrets" {
  metadata {
    name = "external-secrets"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.eso.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# A false plain bool (omitempty in the API's Go types, so the server never
# stores it) comes back null and the provider reports an inconsistent result
# after apply. The render's full list of such values in built-in kinds is one
# field, the controller Deployment's hostNetwork: false; its other false,
# allowPrivilegeEscalation, is a *bool and survives, and the render carries
# no zero int. Dropping that line before yamldecode is the delta; every other
# field is the chart's.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.eso.manifest) : yamldecode(replace(d, "/(?m)^[ ]*hostNetwork: false[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment"], o.kind) && !startswith(o.apiVersion, "external-secrets.io/")
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.external_secrets, kubernetes_manifest.crds]
}

# The controller, after the RBAC it reads with. The render carries no
# ServiceAccount token Secret, so unlike reference-k8s-helm-template this
# block holds the workload alone.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.eso.manifest) : yamldecode(replace(d, "/(?m)^[ ]*hostNetwork: false[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "Deployment"
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}

# The custom resources the render carries (every external-secrets.io
# object; the CRDs are apiextensions.k8s.io), after the controller. The
# order is for the destroy: the controller puts its own finalizer on every
# ExternalSecret and is the only thing that removes it, so the
# ExternalSecret has to be deleted while the controller still runs. A
# destroy walks depends_on backwards, so this block goes before the
# Deployment; in the same block as the Deployment the two deletes would
# race, and an ExternalSecret deleted after its controller stays
# Terminating, with the namespace behind it.
resource "kubernetes_manifest" "custom" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.eso.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if startswith(o.apiVersion, "external-secrets.io/")
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.late]
}
