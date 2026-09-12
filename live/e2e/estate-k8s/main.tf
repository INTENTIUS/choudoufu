# Two of the four kubernetes_* types that carry ratified identity rows
# (#326): a namespace and a ConfigMap in it. Both are client-named -
# metadata.name and metadata.namespace are literals - so a plan resolves
# them with nothing stored anywhere.
#
# The namespace is a literal rather than
# kubernetes_namespace.app.metadata[0].name on purpose: identity resolution
# follows a single attribute of another resource, not a two-step traversal
# through a nested block, and the latter is admitted with a warning rather
# than resolved. Recorded on #1057.

resource "kubernetes_namespace" "app" {
  metadata {
    name = "smoke-k8s"
  }
}

resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "smoke-k8s"
  }

  data = {
    greeting = "hello"
  }

  depends_on = [kubernetes_namespace.app]
}

# Two types with no ratified row of their own (#1064): they resolve through
# the object-metadata rule, NAMESPACE/NAME read from the metadata block.
# The service account's namespace is read from the namespace resource's
# own metadata - the metadata[0].name traversal #1057 found admitted with a
# warning rather than resolved; since #1064 it resolves, because name is the
# parent's identity attribute.

resource "kubernetes_service_account" "app" {
  metadata {
    name      = "app"
    namespace = kubernetes_namespace.app.metadata[0].name
  }
}

resource "kubernetes_service" "app" {
  metadata {
    name      = "app"
    namespace = "smoke-k8s"
  }

  spec {
    selector = {
      app = "app"
    }
    port {
      port        = 80
      target_port = 8080
    }
  }

  depends_on = [kubernetes_namespace.app]
}
