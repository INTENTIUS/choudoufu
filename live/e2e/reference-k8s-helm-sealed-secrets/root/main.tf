# reference-k8s-helm-sealed-secrets: the sealed-secrets project's own chart
# (github.com/bitnami/sealed-secrets, formerly bitnami-labs; its Helm repo
# https://bitnami.github.io/sealed-secrets, not bitnami/charts),
# sealed-secrets 2.20.0 (controller 0.40.0, Apache-2.0), rendered
# client-side by the hashicorp/helm provider's helm_template data source and
# applied as kubernetes_manifest blocks - the shape
# live/kubernetes/COMPATIBILITY.md recommends for a Helm chart, as in
# reference-k8s-helm-template. No helm_release: Helm never talks to the
# cluster here.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults: kubeseal's controller name, a
# fixture annotation and container limits so no rendered key is empty, and
# one SealedSecret through the chart's extraDeploy.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so the names the chart
# shares across kinds (sealed-secrets-controller is a ServiceAccount, a
# ClusterRoleBinding, a Service and a Deployment) stay distinct instances,
# and the manifest is the element itself: `manifest = each.value` (#1962).
#
# Three blocks. The chart's one CRD is pre-applied on its own at cold deploy
# (#1173, declared in live/gauntlet/estates.json), because a root cannot plan
# a CRD and an object of it in one pass. The other two are one apply: the
# RBAC, the Services and the SealedSecret, then the controller.
#
# What nothing here declares: the controller generates its own sealing key
# at start-up, a kubernetes.io/tls Secret named sealed-secrets-key<random>
# in this namespace, labelled sealedsecrets.bitnami.com/sealed-secrets-key
# and owned by nothing. The chart renders no Secret at all.

data "helm_template" "ss" {
  name         = "rel"
  namespace    = "sealed-secrets"
  chart        = "${path.module}/charts/sealed-secrets-2.20.0.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0). The chart's own
  # constraint (>=1.16.0-0) and its API-version switches (apps/v1 from 1.14)
  # would render the same there today, but the switches read this version,
  # so it is pinned rather than read from the cluster and the render is the
  # same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "sealed_secrets" {
  metadata {
    name = "sealed-secrets"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.ss.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# Two kinds of line the server never stores, dropped before yamldecode:
#
# - a zero plain int (omitempty in the API's Go types) comes back null and
#   the provider reports an inconsistent result after apply. The render's
#   full list of false or zero values in built-in kinds is the controller's
#   two probes' initialDelaySeconds: 0; its one false,
#   allowPrivilegeEscalation, is a *bool and survives, and hostNetwork is
#   written only when true.
# - both Services write `nodePort: null` into their one port whenever the
#   type is ClusterIP, and no value removes it; a null leaf replans for ever
#   ("known after apply"), the hazard values.yaml fills every empty key
#   against.
#
# Every other field is the chart's.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.ss.manifest) : yamldecode(replace(d, "/(?m)^[ ]*(initialDelaySeconds: 0|nodePort: null)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment", "DaemonSet", "StatefulSet"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.sealed_secrets, kubernetes_manifest.crds]
}

# The controller, after the RBAC it starts with: at start-up it lists the
# key Secrets in its namespace and, finding none, creates one, both through
# the ClusterRole and the key-admin Role above. Started before them it exits
# and restarts under back-off. The render carries no ServiceAccount token
# Secret, so this block holds the workload alone. The SealedSecrets stay in
# rest: the controller sets no finalizer on them, so the destroy order
# between them and the controller does not matter.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.ss.manifest) : yamldecode(replace(d, "/(?m)^[ ]*(initialDelaySeconds: 0|nodePort: null)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if contains(["Deployment", "DaemonSet", "StatefulSet"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}
