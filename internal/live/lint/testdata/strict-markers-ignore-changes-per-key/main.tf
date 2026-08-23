# GitHub issue #380's own shape: internal/live/stamp now synthesizes
# ignore_changes on exactly the two marker keys for a selected resource,
# never the whole tags argument. This fixture is that shape written by
# hand, to prove checkIgnoreChanges's decline is not narrowly keyed to the
# "ignore_changes = [tags]" form TestStrictMarkersLiftsIgnoreChangesOnlyForTheSelected
# already covers - the same two conditions (selected, marker_repair =
# "never") have to lift the refusal regardless of which of the two
# refused shapes the traversal takes.
#
# One resource is selected, one is not; both ignore the same two per-key
# marker paths.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      marker_repair = "never"
      markers "record" {
        addresses = ["aws_vpc.main"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  lifecycle {
    ignore_changes = [tags["tofu-address"], tags["tofu-estate"]]
  }
}

resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.42.1.0/24"

  lifecycle {
    ignore_changes = [tags["tofu-address"], tags["tofu-estate"]]
  }
}
