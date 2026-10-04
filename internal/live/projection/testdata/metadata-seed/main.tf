# Fixture for TestMetadataSeedKeepsDeclaredInternalLabels: a Kubernetes
# object whose configuration declares labels and annotations under
# *.kubernetes.io prefixes, which hashicorp/kubernetes's read keeps only
# when the prior state already names them. See metadataseed.go.

locals {
  enforce = "restricted"
}

resource "stub_namespace" "this" {
  metadata {
    name = "apps"
    annotations = {
      "argocd.argoproj.io/sync-options" = "ServerSideApply=true"
      "scheduler.alpha.kubernetes.io/node-selector" = "pool=apps"
    }
    labels = {
      "app.kubernetes.io/managed-by"       = "Terraform"
      "pod-security.kubernetes.io/enforce" = local.enforce
      "kubernetes.io/cluster-service"      = "true"
    }
  }
}
