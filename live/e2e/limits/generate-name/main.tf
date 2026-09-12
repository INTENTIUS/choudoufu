# generate-name (GitHub issue #1064): a Kubernetes object whose name the API
# server mints at create time. The name is the object's identity and the
# join key back to this block, so a name this configuration does not state
# is an object no later run could find again. Refused by RuleGenerateName;
# the remedy is metadata.name.
#
# kubernetes_config_map carries a ratified identity row (#326), so the
# unadmitted-type rule stays quiet and exactly one rule fires here.

terraform {
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

resource "kubernetes_config_map" "minted" {
  metadata {
    generate_name = "app-config-"
    namespace     = "default"
  }

  data = {
    key = "value"
  }
}
