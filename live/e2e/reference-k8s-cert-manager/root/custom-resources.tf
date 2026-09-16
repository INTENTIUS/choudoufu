# The three custom resources, ours, written from cert-manager's own
# documentation: https://cert-manager.io/docs/configuration/selfsigned/ and
# https://cert-manager.io/docs/usage/certificate/. The self-signed issuer
# needs no external CA and no credentials, which is why it is the one the
# docs reach for first and the only one an offline kind cluster can serve.
#
# They are the point of the estate: a cluster-scoped custom kind
# (ClusterIssuer), a namespaced one (Issuer), and an object whose controller
# CREATES something else (the Certificate's Secret), all of which need their
# CRD registered at plan time and the validating webhook serving at apply
# time. Both of those are why live/gauntlet/estates.json declares a
# pre_apply.

resource "kubernetes_manifest" "clusterissuer_selfsigned" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "ClusterIssuer"
    "metadata" = {
      "name" = "selfsigned"
    }
    "spec" = {
      "selfSigned" = {}
    }
  }
}

resource "kubernetes_manifest" "issuer_selfsigned" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Issuer"
    "metadata" = {
      "name"      = "selfsigned"
      "namespace" = "cert-manager"
    }
    "spec" = {
      "selfSigned" = {}
    }
  }

  depends_on = [kubernetes_manifest.namespace_cert_manager]
}

resource "kubernetes_manifest" "certificate_example_com" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Certificate"
    "metadata" = {
      "name"      = "example-com"
      "namespace" = "cert-manager"
    }
    "spec" = {
      "secretName" = "example-com-tls"
      "commonName" = "example.com"
      "dnsNames" = [
        "example.com",
      ]
      "issuerRef" = {
        "name"  = "selfsigned"
        "kind"  = "Issuer"
        "group" = "cert-manager.io"
      }
    }
  }

  depends_on = [kubernetes_manifest.namespace_cert_manager]
}

# A counted custom resource, for day2_count. Two namespaced Issuers whose
# object name carries count.index, which is the ordinary scale shape and,
# on this substrate, also asks whether a count.index inside a
# `kubernetes_manifest` manifest object is statically evaluable.
# run.sh rewrites the literal below to scale it.
resource "kubernetes_manifest" "issuer_shard" {
  count = 2
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Issuer"
    "metadata" = {
      "name"      = "shard-${count.index}"
      "namespace" = "cert-manager"
    }
    "spec" = {
      "selfSigned" = {}
    }
  }

  depends_on = [kubernetes_manifest.namespace_cert_manager]
}
