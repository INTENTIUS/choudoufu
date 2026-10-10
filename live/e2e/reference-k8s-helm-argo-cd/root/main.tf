# reference-k8s-helm-argo-cd (#1987): the Argo CD chart, argo-cd 10.10.2 (app
# v3.5.4, argoproj/argo-helm, Apache-2.0), rendered client-side by the
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
# across kinds (rel-argocd-server is a NetworkPolicy, ClusterRole,
# ClusterRoleBinding, Role, RoleBinding, Service and Deployment) stay
# distinct instances, and the manifest is the element itself:
# `manifest = each.value` (#1962).
#
# Deltas from the chart's render, and the ones that turned out not to be
# needed:
#
# - No false/zero omitempty field is dropped before yamldecode. The render's
#   built-in kinds carry exactly one plain `: false`, the containers'
#   allowPrivilegeEscalation (seven of them), which is a *bool and survives
#   the round trip; every other `false` is a quoted ConfigMap value, and no
#   plain field anywhere renders as 0 (every replicas is 1, every probe
#   delay is non-zero).
# - The redis-secret-init helm hook (a pre-install Job, its ServiceAccount,
#   Role and RoleBinding, all `helm.sh/hook` documents) is switched off in
#   values.yaml, and the Secret it would have generated with a random
#   password, argocd-redis, is declared there with a fixed one. No hook
#   document is left in the render, so the for_each filters exclude none.
# - argocd-notifications-secret is declared through extraObjects with data
#   rather than by the chart, whose template writes stringData (null with no
#   items, and never returned by the API server once folded into data).
# - The repo server's Role, which renders `rules:` with nothing under it, is
#   given one read rule.
# - Dex is off: no connector is configured, so it would serve nothing.
#
# argocd-secret is declared with no data at all, as the chart writes it;
# argocd-server fills it at start-up (server.secretkey, the admin password
# hash, a self-signed TLS pair) and creates argocd-initial-admin-secret and
# the `default` AppProject itself. None of that is declared here.
#
# Three blocks, because a root cannot plan a CRD and an object of it in one
# pass: the chart's own three CRDs are pre-applied on their own at cold
# deploy (#1173, declared in live/gauntlet/estates.json), then everything
# else, then the workloads.

data "helm_template" "argocd" {
  name         = "rel"
  namespace    = "argocd"
  chart        = "${path.module}/charts/argo-cd-10.10.2.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>=1.25.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "argocd" {
  metadata {
    name = "argocd"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.argocd.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# Everything that is not a CRD or a workload: the RBAC, the ConfigMaps and
# Secrets the workloads read, the Services and NetworkPolicies, and the
# estate's AppProject and Application, objects of the CRDs above.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.argocd.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment", "StatefulSet"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.argocd, kubernetes_manifest.crds]
}

# The five Deployments and the application controller's StatefulSet, after
# what they read. Redis takes its password from argocd-redis by a
# non-optional secretKeyRef, so started first it sits in
# CreateContainerConfigError; the server, the repo server and the
# application controller read the same key with optional: true, so started
# first they would come up with an empty password, fixed into their
# environment, and fail Redis auth for good. argocd-server also writes into
# argocd-secret at start-up, so that has to exist first too.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.argocd.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if contains(["Deployment", "StatefulSet"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}
