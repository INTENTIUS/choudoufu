# The required_providers alias: the local name "test" is bound to a provider
# source that serves no managed resource type at all, and a managed block
# under that local name would otherwise vote it into the live set on the
# strength of a type it does not serve. See dataread.LiveProviders' third
# derivation half.
terraform {
  required_providers {
    test = {
      source = "hashicorp/external"
    }
  }
}

resource "test_thing" "a" {
  name = "thing-a"
}
