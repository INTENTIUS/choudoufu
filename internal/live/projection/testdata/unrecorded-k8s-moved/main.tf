# A markerless Kubernetes resource renamed by a chain of two moved blocks.
# Its record was written under an earlier address, and nothing re-keys a
# record for a bare HCL moved block, so the record this instance has is the
# one at a moved-from address. kubernetes_unlabelled_stub stands in for any
# markerless type that is not a field-granular patch: the Kubernetes family
# answers UnrecordedCreates for every type, so the name carries no meaning.

terraform {
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

resource "kubernetes_unlabelled_stub" "app" {
  name = "app"
}

moved {
  from = kubernetes_unlabelled_stub.app_v1
  to   = kubernetes_unlabelled_stub.app_v2
}

moved {
  from = kubernetes_unlabelled_stub.app_v2
  to   = kubernetes_unlabelled_stub.app
}
