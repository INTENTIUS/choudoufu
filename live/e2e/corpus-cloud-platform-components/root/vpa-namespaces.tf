# The three namespaces manager-vpas.tf's VerticalPodAutoscalers live in.
#
# Written here, not taken from the corpus. On cloud-platform's own clusters
# ingress-controllers, concourse and monitoring are created by module calls
# in the same root (ingress_controllers_v1, concourse, monitoring), and the
# crossing prunes every github.com/ministryofjustice/* module because none
# of them is in .corpus/_modules. Without the namespaces the VPA objects
# cannot be created at all, so they are the smallest stand-in for what the
# pruned modules provided: a namespace each, nothing inside it.
#
# They are part of the declared cold-deploy pre-apply
# (live/gauntlet/estates.json), with the two CRDs: kubernetes_manifest
# dry-runs a namespaced object against the API server at plan time, and a
# dry run into a namespace that does not exist is refused, so the
# namespaces have to exist before the VPAs can be planned.

resource "kubernetes_namespace_v1" "ingress_controllers" {
  metadata {
    name = "ingress-controllers"
  }
}

resource "kubernetes_namespace_v1" "concourse" {
  metadata {
    name = "concourse"
  }
}

resource "kubernetes_namespace_v1" "monitoring" {
  metadata {
    name = "monitoring"
  }
}
