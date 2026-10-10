# reference-k8s-yaml-rollouts (#1989): Argo Rollouts v1.10.0 install
# bundle (install.yaml, a GitHub release asset of argoproj/argo-rollouts,
# Apache-2.0), read with file() and yamldecode and applied as
# kubernetes_manifest blocks. No Helm chart and no hashicorp/helm provider:
# the bundle is the plain multi-document YAML a user would `kubectl apply
# -n argo-rollouts -f`, and this root is what that user writes instead.
#
# The bundle is fetched and checked against its sha256 by run.sh, which
# records the URL, the version and the digest, and copies it to
# manifests/install.yaml beside this file in every working root. It is
# never edited in the committed root; run.sh's day-2 stages edit their
# working copies of it by exact match, as a user of a plain manifest does.
#
# Every document is split out and yamldecoded INSIDE the for_each
# expression, keyed kind/namespace/name, so argo-rollouts - one name for a
# ServiceAccount, a ClusterRole, a ClusterRoleBinding and a Deployment -
# stays four distinct instances, and the manifest is the element itself:
# `manifest = each.value` (#1962). Unlike the lane's helm_template estates,
# the for_each reads no data source: file() of a path under path.module is
# all it needs, so the keys are known from the configuration alone.
#
# How the split handles the file's framing. The bundle opens with a comment
# line ("# This is an auto-generated file. DO NOT EDIT") and no `---`; the
# comment rides on the first document and yamldecode ignores it. A bundle
# that DID open with `---` would not split on "\n---\n", since the marker
# has no newline before it, so the text is read with a "\n" prepended: a
# leading marker then splits off an empty first piece. Separator lines
# carrying trailing blanks, a trailing comment or a CR (`--- `, `--- # x`,
# `---\r`) are normalised to a bare `---` by the replace() before the split.
# Pieces with no top-level `kind:` line - the empty piece, a comment-only
# preamble - are dropped by the `if`. `--- <content>` on one line and `...`
# end markers are not handled; the pinned bundle has neither.
#
# What the bundle needed, measured by splitting it the same way and
# decoding every document:
#
# - no namespace on any object. The bundle is written for `kubectl apply
#   -n`, and its ClusterRoleBinding's subject already names the
#   argo-rollouts namespace, so the five namespaced objects get
#   metadata.namespace = "argo-rollouts" merged in before they are keyed.
#   ClusterRole and ClusterRoleBinding are the render's cluster-scoped
#   kinds besides the CRDs, and they get nothing.
# - no false or zero plain omitempty field in any built-in kind: the one
#   false outside the CRD schemas is the controller's
#   allowPrivilegeEscalation, a *bool that survives the round trip. So,
#   unlike reference-k8s-helm-template, no line is dropped before
#   yamldecode.
# - no key that decodes to null. The ConfigMap and the Secret carry no data
#   key at all rather than an empty one, which the API stores as absent.
#   Four CRDs carry `subresources: {}`, a pointer the API keeps.
# - no webhook, no TLS Secret, no hook and nothing random. The one Secret
#   (argo-rollouts-notification-secret) is an empty Opaque Secret the
#   controller reads and nothing writes.
#
# Four blocks of the bundle's objects and the root's own, because a root
# cannot plan a CRD and an object of it in one pass: the bundle's five CRDs
# are pre-applied with the Namespace at cold deploy (#1173, declared in
# live/gauntlet/estates.json), and the rest of the bundle and the
# AnalysisTemplates below are the main apply.

resource "kubernetes_namespace_v1" "argo_rollouts" {
  metadata {
    name = "argo-rollouts"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", replace("\n${file("${path.module}/manifests/install.yaml")}", "/(?m)^---[ \t]*(#.*)?\r?$/", "---")) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for p in [
        for d in split("\n---\n", replace("\n${file("${path.module}/manifests/install.yaml")}", "/(?m)^---[ \t]*(#.*)?\r?$/", "---")) : yamldecode(d)
        if length(regexall("(?m)^kind:", d)) > 0
      ] : merge(p, { metadata = merge(p.metadata, contains(["ClusterRole", "ClusterRoleBinding"], p.kind) ? {} : { namespace = "argo-rollouts" }) })
      if !contains(["CustomResourceDefinition", "Deployment"], p.kind)
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.argo_rollouts, kubernetes_manifest.crds]
}

# The controller Deployment comes after everything above, in the same
# apply, so its pod starts with its ClusterRoleBinding, the
# argo-rollouts-config ConfigMap and the notification Secret it reads
# already in place. The bundle holds no ServiceAccount token Secret, so
# unlike reference-k8s-helm-template nothing else is late. Whether the
# controller would recover from starting first is not measured; the order
# is the one `kubectl apply -f` gives it, where the Deployment is the
# bundle's last document.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", replace("\n${file("${path.module}/manifests/install.yaml")}", "/(?m)^---[ \t]*(#.*)?\r?$/", "---")) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/argo-rollouts/${o.metadata.name}" => merge(o, { metadata = merge(o.metadata, { namespace = "argo-rollouts" }) })
    if o.kind == "Deployment"
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}

# The root's own custom resources, which the bundle does not carry:
# AnalysisTemplates, objects of the bundle's analysistemplates CRD. An
# AnalysisTemplate does nothing until a Rollout or an AnalysisRun names it,
# and nothing here does, so the controller never writes to them. The
# `base` template is what makes the CRD pre-apply load-bearing at cold
# deploy.
#
# shard_templates is day2_count's for_each: run.sh rewrites the map, from
# the `shard_templates =` line to its closing brace, and each entry is one
# template whose query returns the entry's value.
locals {
  shard_templates = {}
}

resource "kubernetes_manifest" "analysis" {
  for_each = merge({ base = "1" }, local.shard_templates)
  manifest = {
    "apiVersion" = "argoproj.io/v1alpha1"
    "kind"       = "AnalysisTemplate"
    "metadata" = {
      "name"      = each.key
      "namespace" = "argo-rollouts"
    }
    "spec" = {
      "metrics" = [{
        "name"             = "constant"
        "successCondition" = "result[0] >= 1"
        "provider" = {
          "prometheus" = {
            "address" = "http://prometheus.invalid:9090"
            "query"   = "vector(${each.value})"
          }
        }
      }]
    }
  }

  depends_on = [kubernetes_manifest.rest]
}
