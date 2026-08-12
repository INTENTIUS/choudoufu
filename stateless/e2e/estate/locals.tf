locals {
  # The marker's estate value (stateless/MARKERS.md, P0.3). Every taggable
  # resource in this fixture carries this plus its own tofu-address.
  estate_tag = "stateless-e2e"

  subnets = {
    a = { cidr = "10.42.1.0/24", az = "us-east-1a" }
    b = { cidr = "10.42.2.0/24", az = "us-east-1b" }
  }
}
