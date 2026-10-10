# reference-k8s-helm-metallb (#1975): the MetalLB project's metallb chart 0.16.1
# (Apache-2.0, the licence of github.com/metallb/metallb; the archive
# carries no LICENSE file of its own), rendered client-side by the
# hashicorp/helm provider's helm_template data source and applied as
# kubernetes_manifest blocks, the shape reference-k8s-helm-template took for
# kube-prometheus-stack (#1965). No helm_release: Helm never talks to the
# cluster here.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# two values lines run.sh's day-2 stages edit; both restate the chart's
# defaults, so the render is the chart's own.
#
# At its defaults the chart renders 42 objects over 12 kinds: MetalLB's
# controller Deployment and speaker DaemonSet, and the frr-k8s subchart's
# DaemonSet and status-cleaner Deployment (FRR-K8s is the default BGP
# backend since 0.16), their RBAC, two ValidatingWebhookConfigurations with
# failurePolicy: Fail, and 13 CRDs. Every document is split out and
# yamldecoded INSIDE the for_each expression, keyed kind/namespace/name, so
# the seven names the chart shares across kinds (rel-frr-k8s-controller is a
# ServiceAccount, Role, RoleBinding, ClusterRole and ClusterRoleBinding)
# stay distinct instances, and the manifest is the element itself:
# `manifest = each.value` (#1962).
#
# What the render needed, measured with `helm template --include-crds
# --kube-version 1.36.0` on the archive:
#
# - no false or zero plain omitempty field in any built-in kind. The two
#   DaemonSets and the status cleaner carry hostNetwork: true, which the
#   server stores; allowPrivilegeEscalation: false (*bool) and
#   terminationGracePeriodSeconds: 0 (*int64) survive a round trip, and the
#   CRDs' storage: false is a plain bool WITHOUT omitempty. So, unlike
#   reference-k8s-helm-template, no line is dropped before yamldecode.
# - no key that decodes to null (no empty volumes:, env:, args: or
#   annotations:), so values.yaml adds no placeholder entries.
# - no helm.sh/hook document, no cert-gen Job and no caBundle in the render.
#   The two webhook Secrets (metallb-webhook-cert, frr-k8s-webhook-server-cert)
#   are rendered with no data; the MetalLB controller's and the frr-k8s
#   status cleaner's own cert rotators fill them at runtime and inject the
#   CA into the webhook configurations and the bgppeers CRD's conversion
#   webhook, fields this root never declares. Nothing to switch off.
# - nothing random: two renders of the archive are byte-identical.
# - every CRD is a TEMPLATE of a nested crds subchart (metallb/charts/crds
#   and metallb/charts/frr-k8s/charts/crds), not a crds/ file, so
#   include_crds changes nothing in this render; it is kept for the shape.
#
# Three blocks of rendered objects plus the custom resources, because a
# root cannot plan a CRD and an object of it in one pass, and the objects
# of it go through the controller's failurePolicy: Fail webhook, which
# cannot answer until the controller is running: the chart's whole render
# is pre-applied at cold deploy (#1173, declared in
# live/gauntlet/estates.json), and the IPAddressPools and the
# L2Advertisement below are the main apply.

data "helm_template" "metallb" {
  name         = "rel"
  namespace    = "metallb-system"
  chart        = "${path.module}/charts/metallb-0.16.1.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0). The chart's own
  # kubeVersion constraint (>= 1.19.0-0) would accept that; it is pinned
  # anyway so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "metallb_system" {
  metadata {
    name = "metallb-system"
  }
}

resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.metallb.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.metallb.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment", "DaemonSet"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.metallb_system, kubernetes_manifest.crds]
}

# The workloads come after everything above, in the same apply: the pods
# start with their RBAC, the webhook Secrets they mount, the excludel2
# ConfigMap the speaker mounts, and the ValidatingWebhookConfigurations the
# cert rotators patch already in place. The render holds no ServiceAccount
# token Secret, so unlike reference-k8s-helm-template nothing else is late.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.metallb.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if contains(["Deployment", "DaemonSet"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}

# MetalLB's own configuration, written here because the chart renders none:
# IPAddressPools and an L2Advertisement, custom resources of the render's
# CRDs. Every pool is autoAssign = false and no Service of type LoadBalancer
# exists, so no address is ever handed out or announced. The addresses sit
# at the top of kind's default docker network (172.18.0.0/16), far above
# anything docker assigns a node, and MetalLB's webhook refuses overlapping
# pools, so every pool holds its own range.
#
# shard_pools is day2_count's for_each: run.sh rewrites the map, from the
# `shard_pools =` line to its closing brace, and each entry is one pool.
locals {
  shard_pools = {}
}

resource "kubernetes_manifest" "pool" {
  for_each = merge({ base = "172.18.255.200-172.18.255.203" }, local.shard_pools)
  manifest = {
    "apiVersion" = "metallb.io/v1beta1"
    "kind"       = "IPAddressPool"
    "metadata" = {
      "name"      = each.key
      "namespace" = "metallb-system"
    }
    "spec" = {
      "addresses"  = [each.value]
      "autoAssign" = false
    }
  }

  depends_on = [kubernetes_manifest.late]
}

resource "kubernetes_manifest" "l2" {
  manifest = {
    "apiVersion" = "metallb.io/v1beta1"
    "kind"       = "L2Advertisement"
    "metadata" = {
      "name"      = "base"
      "namespace" = "metallb-system"
    }
    "spec" = {
      "ipAddressPools" = ["base"]
    }
  }

  depends_on = [kubernetes_manifest.pool]
}
