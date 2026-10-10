# reference-k8s-helm-template (#1963): prometheus-community's
# kube-prometheus-stack 92.3.0 (Apache-2.0), rendered client-side by the
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
# for_each expression, keyed kind/namespace/name, so names the chart shares
# across kinds (up to eight kinds for one name) stay distinct instances, and
# the manifest is the element itself: `manifest = each.value` (#1962).
#
# Two blocks, because a root cannot plan a CRD and an object of it in one
# pass: the chart's own CRDs are pre-applied on their own at cold deploy
# (#1173, declared in live/gauntlet/estates.json), and everything else the
# chart renders - 50 of them objects of those CRDs - is applied after them.

data "helm_template" "kps" {
  name         = "rel"
  namespace    = "monitoring"
  chart        = "${path.module}/charts/kube-prometheus-stack-92.3.0.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>=1.25.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "monitoring" {
  metadata {
    name = "monitoring"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kps.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# A false plain bool or a zero plain int (omitempty in the API's Go types,
# so the server never stores it) comes back null and the provider reports an inconsistent
# result after apply - stock fails on kube-state-metrics' hostNetwork: false
# and node-exporter's initialDelaySeconds: 0. The render's full list of such
# values in built-in kinds is these five fields; *bool fields survive.
# Dropping those lines before yamldecode is the delta; every other field is
# the chart's.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kps.manifest) : yamldecode(replace(d, "/(?m)^[ ]*((hostNetwork|hostIPC|hostPID|publishNotReadyAddresses): false|initialDelaySeconds: 0)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind != "CustomResourceDefinition" && !(o.kind == "Secret" && try(o.type, "") == "kubernetes.io/service-account-token")
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.monitoring, kubernetes_manifest.crds]
}

# The chart's ServiceAccount token Secret, after the ServiceAccount it names.
# Applied in the same pass, the token controller can see the Secret before
# its ServiceAccount exists and delete it, and the next plan proposes it
# again. The control-plane ServiceMonitors authenticate with it, so it stays.
resource "kubernetes_manifest" "tokens" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kps.manifest) : yamldecode(d)
      if length(regexall("(?m)^type: kubernetes.io/service-account-token", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}
