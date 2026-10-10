# reference-k8s-helm-kyverno (#1978): kyverno's kyverno chart 3.9.1 (app v1.19.1,
# Apache-2.0), rendered client-side by the hashicorp/helm provider's
# helm_template data source and applied as kubernetes_manifest blocks, the
# shape live/kubernetes/COMPATIBILITY.md recommends for a Helm chart and the
# one reference-k8s-helm-template took first (#1963). No helm_release: Helm
# never talks to the cluster here.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so names the chart shares
# across kinds (rel-kyverno:admission-controller is a ClusterRole, a
# ClusterRoleBinding, a Role and a RoleBinding) stay distinct instances, and
# the manifest is the element itself: `manifest = each.value` (#1962).
#
# The render's deltas that live here rather than in values.yaml:
#
# - kube_version, pinned, for the reason given on the data source.
# - skip_tests: the chart's five helm test Pods carry helm.sh/hook: test and
#   have no values toggle of their own (they follow each controller's
#   enabled and metricsService.create). Every other hook the chart has is
#   switched off in values.yaml, so no helm.sh/hook document is in the
#   applied set and the for_each filters need no hook clause.
# - no omitempty drop. A false plain bool or zero plain int in a built-in
#   kind comes back null and the provider reports an inconsistent result
#   (reference-k8s-helm-template drops five such fields before yamldecode).
#   This render's false values in built-in kinds are all *bool fields that
#   the server keeps (automountServiceAccountToken on the four
#   ServiceAccounts, privileged and allowPrivilegeEscalation in every
#   container securityContext); it carries no hostNetwork, hostIPC,
#   hostPID, publishNotReadyAddresses or zero probe delay, so there is
#   nothing to drop and the documents are decoded as rendered.
#
# What kyverno does at runtime, which nothing here declares: it generates
# its own TLS Secrets in the namespace (admission and cleanup controllers,
# labelled cert.kyverno.io/managed-by=kyverno, no owner reference) and
# registers its own Validating- and MutatingWebhookConfigurations
# (labelled webhook.kyverno.io/managed-by=kyverno). Its policy webhook then
# validates every ClusterPolicy this root creates or updates, including
# the label choudoufu stamps on one.

data "helm_template" "kyverno" {
  name         = "rel"
  namespace    = "kyverno"
  chart        = "${path.module}/charts/kyverno-3.9.1.tgz"
  include_crds = true
  skip_tests   = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>=1.25.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

locals {
  # day2_count's for_each scale: the number of ClusterPolicies the root
  # declares below. run.sh rewrites this one line by exact match.
  policy_shards = 1
}

resource "kubernetes_namespace_v1" "kyverno" {
  metadata {
    name = "kyverno"
  }
}

# The chart's 22 CRDs, pre-applied on their own at cold deploy (#1173,
# declared in live/gauntlet/estates.json), because a root cannot plan a CRD
# and an object of it in one pass and the policies block below declares
# objects of clusterpolicies.kyverno.io.
resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kyverno.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# Everything else the chart renders except the four controller Deployments:
# ServiceAccounts, ConfigMaps, ClusterRoles and their bindings, Roles and
# theirs, Services.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kyverno.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.kyverno, kubernetes_manifest.crds]
}

# The four controllers, after the RBAC and the ConfigMaps they read at
# start-up, and waited on until rolled out: the policies below are created
# through the admission controller's policy webhook, which kyverno
# registers with failurePolicy: Fail, so a create that reaches the webhook
# before a ready pod backs its Service would be refused. Waiting on the
# rollout puts every policy create after the admission controller is
# serving.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kyverno.manifest) : yamldecode(d)
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

# The estate's own ClusterPolicies: the chart renders none, so these are
# the custom resources of the chart's CRDs, keyed by shard and scaled by
# local.policy_shards. Audit-only validation of ConfigMaps carrying a label
# nothing on the cluster carries, so kyverno admits and reports nothing,
# and failurePolicy Ignore, so the resource webhook kyverno registers for
# them never refuses a request while a controller is down. Every field the
# CRD would default is declared at its default, so what is stored is what
# is declared.
resource "kubernetes_manifest" "policies" {
  for_each = { for i in range(local.policy_shards) : "shard-${i}" => i }
  manifest = {
    "apiVersion" = "kyverno.io/v1"
    "kind"       = "ClusterPolicy"
    "metadata" = {
      "name" = "choudoufu-${each.key}"
    }
    "spec" = {
      "admission"               = true
      "background"              = true
      "emitWarning"             = false
      "failurePolicy"           = "Ignore"
      "validationFailureAction" = "Audit"
      "rules" = [{
        "name"                   = "require-team-label"
        "skipBackgroundRequests" = true
        "match" = {
          "any" = [{
            "resources" = {
              "kinds" = ["ConfigMap"]
              "selector" = {
                "matchLabels" = {
                  "choudoufu.intentius.io/policy-target" = each.key
                }
              }
            }
          }]
        }
        "validate" = {
          "allowExistingViolations" = true
          "message"                 = "a ConfigMap selected by ${each.key} carries a team label"
          "pattern" = {
            "metadata" = {
              "labels" = {
                "team" = "?*"
              }
            }
          }
        }
      }]
    }
  }

  depends_on = [kubernetes_manifest.late]
}
