# An address naming no resource this configuration declares selects nothing,
# and the failure is invisible: the resource the entry meant to name keeps
# its marker, every run works, and the tag budget the operator was buying
# back is still spent.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
        addresses = ["aws_vpc.typo"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
