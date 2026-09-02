# The selection's unit is the resource BLOCK. One configuration body serves
# every instance a count expands to, and the tofu-address written into it is
# a template over count.index, so a marker cannot be withheld from one
# instance and written for its siblings.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
        addresses = ["aws_vpc.main[0]"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  count      = 2
  cidr_block = "10.42.0.0/16"
}
