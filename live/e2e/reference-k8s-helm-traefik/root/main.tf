# reference-k8s-helm-traefik (#1974): traefik's own chart, traefik 41.7.1
# (Apache-2.0, Traefik Proxy v3.7.14), rendered client-side by the
# hashicorp/helm provider's helm_template data source and applied as
# kubernetes_manifest blocks - the shape live/kubernetes/COMPATIBILITY.md
# recommends for a Helm chart, and the shape reference-k8s-helm-template
# (#1963) took for kube-prometheus-stack. No helm_release: Helm never talks
# to the cluster here.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so the five kinds the
# chart names rel-traefik (ServiceAccount, Service, Deployment,
# PodDisruptionBudget and the cluster-scoped IngressClass) stay distinct
# instances, and the manifest is the element itself: `manifest = each.value`
# (#1962).
#
# Three blocks, because a root cannot plan a CRD and an object of it in one
# pass: the chart's own 25 CRDs (ten traefik.io, fifteen hub.traefik.io the
# chart ships whether or not Traefik Hub is on) are pre-applied on their
# own at cold deploy (#1173, declared in live/gauntlet/estates.json), and
# everything else the chart renders - two IngressRoutes among them - is
# applied after them.

data "helm_template" "traefik" {
  name         = "rel"
  namespace    = "traefik"
  chart        = "${path.module}/charts/traefik-41.7.1.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>=1.25.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "traefik" {
  metadata {
    name = "traefik"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.traefik.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# A false plain bool or a zero plain int (omitempty in the API's Go types,
# so the server never stores it) comes back null and the provider reports an
# inconsistent result after apply. The render's full list of such values in
# built-in kinds is two fields of the Deployment: the pod's hostNetwork:
# false and the Deployment's minReadySeconds: 0. The other false/0 values
# the render carries are pointers and survive: the ServiceAccount's
# automountServiceAccountToken (*bool), the container's
# allowPrivilegeEscalation (*bool), the rolling update's maxUnavailable
# (*IntOrString), and the CRD schemas' `minimum: 0` (*float64). Dropping
# those two lines before yamldecode is the delta; every other field is the
# chart's.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.traefik.manifest) : yamldecode(replace(d, "/(?m)^[ ]*(hostNetwork: false|minReadySeconds: 0)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment", "DaemonSet", "StatefulSet"], o.kind) && !(o.kind == "Secret" && try(o.type, "") == "kubernetes.io/service-account-token")
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.traefik, kubernetes_manifest.crds]
}

# What has to come after everything above, in the same apply: the
# workloads, so that Traefik starts with its ClusterRoleBinding, its
# ServiceAccount and its IngressRoutes already there, and the first routers
# it loads are the render's. ServiceAccount token Secrets would go here too
# (applied before their ServiceAccount, the token controller deletes them);
# this chart renders none.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.traefik.manifest) : yamldecode(replace(d, "/(?m)^[ ]*(hostNetwork: false|minReadySeconds: 0)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if contains(["Deployment", "DaemonSet", "StatefulSet"], o.kind) || (o.kind == "Secret" && try(o.type, "") == "kubernetes.io/service-account-token")
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}
