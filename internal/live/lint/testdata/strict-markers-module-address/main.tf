# A module-qualified address, which is the shape the -target grammar takes
# and the one an estate with modules actually needs. Nothing is refused: the
# address resolves to a resource block the tree declares.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
        addresses = ["module.net.aws_vpc.main"]
      }
    }
  }
}

module "net" {
  source = "./mod"
}
