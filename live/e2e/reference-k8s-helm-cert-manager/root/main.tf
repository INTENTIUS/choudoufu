# reference-k8s-helm-cert-manager (#1981): jetstack's cert-manager chart
# v1.21.2 (Apache-2.0, from charts.jetstack.io), rendered client-side by the
# hashicorp/helm provider's helm_template data source and applied as
# kubernetes_manifest blocks, the shape live/kubernetes/COMPATIBILITY.md
# recommends for a Helm chart and the one reference-k8s-helm-template took
# first (#1963). No helm_release: Helm never talks to the cluster here.
#
# It is the same software as reference-k8s-cert-manager, at the same
# version, configured the other way. That estate is cert-manager's install
# bundle converted by tfk8s into 47 hand-shaped blocks, one per object;
# this one is the chart, one data source and four for_each blocks. The
# release is named cert-manager in namespace cert-manager, and with
# crds.enabled the render is the bundle's 46 non-Namespace objects with the
# same kinds, namespaces and names and, compared field by field, the same
# content except two labels the chart adds when its creator is "helm"
# (app.kubernetes.io/managed-by: Helm and helm.sh/chart:
# cert-manager-v1.21.2). The Namespace, which the bundle carries and helm
# template does not render, is a kubernetes_namespace_v1 here. The three
# custom resources come through the chart's extraObjects in values.yaml
# rather than a second .tf file.
#
# The chart archive is fetched and checked against its sha256 by run.sh,
# which records the URL, the version and the digest, and copies it to
# charts/ beside this file in every working root. values.yaml carries the
# estate's deltas from the chart defaults.
#
# Every document of the render is split out and yamldecoded INSIDE the
# for_each expression, keyed kind/namespace/name, so names the chart shares
# across kinds (cert-manager-webhook is a ServiceAccount, a Service, a
# Deployment, a MutatingWebhookConfiguration and a
# ValidatingWebhookConfiguration; selfsigned is a ClusterIssuer and an
# Issuer) stay distinct instances, and the manifest is the element itself:
# `manifest = each.value` (#1962).
#
# The render's deltas that live here rather than in values.yaml:
#
# - kube_version, pinned, for the reason given on the data source.
# - no omitempty drop. A false plain bool or zero plain int in a built-in
#   kind comes back null and the provider reports an inconsistent result
#   (reference-k8s-helm-template drops five such fields before yamldecode).
#   This render's false values in built-in kinds are all *bool fields that
#   the server keeps (allowPrivilegeEscalation in the three containers,
#   enableServiceLinks in the three pod specs); it carries no hostNetwork,
#   hostIPC, hostPID, publishNotReadyAddresses or zero probe delay, so the
#   documents are decoded as rendered.
# - no hook clause. The chart's only helm.sh/hook documents are the
#   startupapicheck Job and its RBAC, switched off in values.yaml.
#
# What cert-manager does at runtime, which nothing here declares: the
# webhook generates its own CA and serving certificate into the Secret
# cert-manager-webhook-ca, cainjector writes that CA into the caBundle of
# both webhook configurations (its own field manager, so no conflict with
# the server-side apply below), the controllers take Leases in kube-system,
# and the Certificate's controller issues the Secret example-com-tls.
#
# Four blocks, three of them the installation. A root cannot plan a CRD and
# an object of it in one pass, and both webhooks are failurePolicy: Fail,
# so the installation (the Namespace, crds, rest and late) is pre-applied
# at cold deploy (#1173, declared in live/gauntlet/estates.json), exactly
# as reference-k8s-cert-manager pre-applies its 47 bundle addresses; run.sh
# then waits for the webhook to admit a dry-run Issuer, and the main apply
# is the custom block.

data "helm_template" "cert_manager" {
  name         = "cert-manager"
  namespace    = "cert-manager"
  chart        = "${path.module}/charts/cert-manager-v1.21.2.tgz"
  include_crds = true
  values       = [file("${path.module}/values.yaml")]

  # helm_template never asks the cluster, so without this the render assumes
  # Helm's default capabilities (Kubernetes v1.20.0) and the chart's own
  # kubeVersion constraint (>= 1.22.0-0) refuses it. Pinned rather than read
  # from the cluster so the render is the same on every kind node image.
  kube_version = "1.36.0"
}

resource "kubernetes_namespace_v1" "cert_manager" {
  metadata {
    name = "cert-manager"
  }
}

# The chart's six CRDs (crds.enabled), on their own because the custom
# block's kinds are only served once these exist.
resource "kubernetes_manifest" "crds" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cert_manager.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "CustomResourceDefinition"
  }
  manifest = each.value
}

# Everything else the chart renders except the three Deployments and the
# custom resources: ServiceAccounts, ClusterRoles and their bindings, Roles
# and theirs (four of them in kube-system, a namespace the estate never
# declares), Services, and both webhook configurations. The webhook
# configurations only match cert-manager's own API groups, so admitting
# them before the webhook serves blocks nothing else in this block.
resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cert_manager.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if !contains(["CustomResourceDefinition", "Deployment", "ClusterIssuer", "Issuer", "Certificate"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_namespace_v1.cert_manager, kubernetes_manifest.crds]
}

# The controller, the webhook and cainjector, after the RBAC they need.
# The webhook's dynamic-serving Role must exist before it starts, or it
# cannot write its own CA Secret. The chart renders no ServiceAccount token
# Secret, so unlike reference-k8s-helm-template this block holds
# Deployments only.
resource "kubernetes_manifest" "late" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cert_manager.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if o.kind == "Deployment"
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.rest]
}

# The estate's own custom resources from values.yaml's extraObjects: the
# ClusterIssuer, the Issuer and the Certificate, plus day2_count's shard
# Issuers while that stage has them. Every write here, the plan-time
# server-side dry run included, goes through the validating webhook, which
# is why this block is outside the pre-apply and run.sh waits for the
# webhook to admit a probe before the main apply. No wait { rollout } on
# late: the pre-apply already put the Deployments up, and a rolled-out
# webhook Deployment still refuses until cainjector has injected its CA,
# which only the probe observes.
resource "kubernetes_manifest" "custom" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.cert_manager.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
    if contains(["ClusterIssuer", "Issuer", "Certificate"], o.kind)
  }
  manifest = each.value

  depends_on = [kubernetes_manifest.late]
}
