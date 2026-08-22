# marker_repair = "never" on its own, which is slice 1's own refusal and has
# to stay one: the resource's marker is still written, and discarding the
# write leaves it findable by nothing.
#
# Two issues, both required. The setting is refused because no selection
# gives it a mechanism, and the ignore_changes is refused because the marker
# is still the resource's only identity.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      marker_repair = "never"
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  lifecycle {
    ignore_changes = [tags]
  }
}
