# The join point between slice 1's marker_repair toggle and slice 2's
# selection. Both resources ignore their whole tags argument; one is
# selected, one is not.
#
# The selected one holds its identity in the record store and carries no
# marker at all, so ignoring its tags costs nothing and is honoured exactly
# as stock honours it. The other still gets its marker written, so discarding
# the write is still the "created unfindable" failure RuleIgnoreChanges
# exists to prevent.
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
    ignore_changes = [tags]
  }
}

resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.42.1.0/24"

  lifecycle {
    ignore_changes = [tags]
  }
}
