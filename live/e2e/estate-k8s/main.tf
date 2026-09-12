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
