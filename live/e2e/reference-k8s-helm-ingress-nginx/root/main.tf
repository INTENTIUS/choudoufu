# reference-k8s-helm-ingress-nginx (#1984): the Kubernetes project's own
# ingress-nginx chart 4.15.1 (Apache-2.0, controller v1.15.1), rendered
# client-side by the hashicorp/helm provider's helm_template data source and
# applied as kubernetes_manifest blocks - the shape
# live/kubernetes/COMPATIBILITY.md recommends for a Helm chart, and the shape
# reference-k8s-helm-template (#1963) took for kube-prometheus-stack. No
# helm_release: Helm never talks to the cluster here.
#
# The upstream repository (kubernetes/ingress-nginx) was retired in March
# 2026 and is archived on GitHub; 4.15.1 (2026-03-19) is its last chart, and
# the release asset and the registry.k8s.io image it pins by digest are
# still published, linux/arm64 included. The estate pins both, so the
# retirement changes nothing a run reads.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so the names the chart
# shares across kinds (rel-ingress-nginx is a ServiceAccount, a ClusterRole,
# a ClusterRoleBinding, a Role and a RoleBinding; rel-ingress-nginx-controller
# a ConfigMap, a Service and a Deployment) stay distinct instances, and the
# manifest is the element itself: `manifest = each.value` (#1962).
#
# The chart ships no CRDs, so there is no crds block and no declared
# pre-apply: the whole root plans and applies in one pass against an empty
# cluster.

data "helm_template" "ingress_nginx" {
  name         = "rel"
  namespace    = "ingress-nginx"
  chart        = "${path.module}/charts/ingress-nginx-4.15.1.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>=1.21.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "ingress_nginx" {
  metadata {
    name = "ingress-nginx"
  }
}

# A false plain bool or a zero plain int (omitempty in the API's Go types,
# so the server never stores it) comes back null and the provider reports an
# inconsistent result after apply. The render's full list of such values in
# built-in kinds is one field, the Deployment's minReadySeconds: 0. The two
# other false values it carries are the container's allowPrivilegeEscalation
# and readOnlyRootFilesystem, both *bool, which survive. Dropping that line
# before yamldecode is the delta; every other field is the chart's.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.ingress_nginx.manifest) : yamldecode(replace(d, "/(?m)^[ ]*(minReadySeconds: 0)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment", "DaemonSet", "StatefulSet"], o.kind) && !(o.kind == "Secret" && try(o.type, "") == "kubernetes.io/service-account-token")
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.ingress_nginx]
}

# What has to come after everything above, in the same apply: the
# controller's Deployment, so that it starts with its RBAC, its ConfigMap,
# the Service it publishes and the IngressClass it serves already there.
# ServiceAccount token Secrets would go here too (applied before their
# ServiceAccount, the token controller deletes them); this chart renders
# none.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.ingress_nginx.manifest) : yamldecode(replace(d, "/(?m)^[ ]*(minReadySeconds: 0)[ ]*$/", ""))
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if contains(["Deployment", "DaemonSet", "StatefulSet"], o.kind) || (o.kind == "Secret" && try(o.type, "") == "kubernetes.io/service-account-token")
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}

# Ours, not the chart's: one Ingress per IngressClass the render holds,
# keyed by the class's name. The chart renders no Ingress, and an ingress
# controller with nothing to route measures nothing about the objects it
# writes to. The keys come from the same render the data-read phase reads,
# so values.yaml's ingressClassResource.aliases scales this block and the
# rest block together (day2_count), and ingressClassResource.enabled takes
# instances out of both (day2_remove).
#
# Each Ingress routes its own host to the controller's own Service; nothing
# sends a request through it. What the estate reads is the controller's
# side: with --publish-service it writes the Service's address into
# status.loadBalancer of every Ingress it admits - the status subresource of
# an object this root owns, which no replan may propose to touch.
resource "kubernetes_manifest" "ingresses" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.ingress_nginx.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : o.metadata.name => o
    if o.kind == "IngressClass"
  }
  manifest = {
    "apiVersion" = "networking.k8s.io/v1"
    "kind"       = "Ingress"
    "metadata" = {
      "name"      = "via-${each.key}"
      "namespace" = "ingress-nginx"
    }
    "spec" = {
      "ingressClassName" = each.key
      "rules" = [{
        "host" = "${each.key}.choudoufu.test"
        "http" = {
          "paths" = [{
            "path"     = "/"
            "pathType" = "Prefix"
            "backend" = {
              "service" = {
                "name" = "rel-ingress-nginx-controller"
                "port" = { "name" = "http" }
              }
            }
          }]
        }
      }]
    }
  }

  depends_on = [kubernetes_manifest.rest]
}
