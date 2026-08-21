# kubernetes_config_map (issue #326): the type blocking corpus-eks-basic's
# test_plan stage before this table carried a row for it at all. The
# provider's own documented import ID (docs/resources/config_map.md,
# fetched live - the offline cache has no Kubernetes provider data) is
# NAMESPACE/NAME, both read out of the required metadata block via
# identity.Component.Block (#310) rather than as top-level arguments.
#
# present exercises the ordinary case: both namespace and name are given,
# so the rendered ImportID is the provider's own documented shape, verbatim.
resource "kubernetes_config_map" "present" {
  metadata {
    name      = "my-config"
    namespace = "default"
  }

  data = {
    key = "value"
  }
}

# no_namespace is the mutation-tested adversarial case: metadata.name is set
# but metadata.namespace is not. Namespace is Optional in the provider's own
# schema (it defaults server-side to "default"), but the ratified row reads
# it as a required identity component with no Default set - so a resolver
# that silently treated an absent namespace as the string "default" would
# fabricate an identity the configuration never stated. This must refuse,
# not guess.
resource "kubernetes_config_map" "no_namespace" {
  metadata {
    name = "my-config-2"
  }
}
