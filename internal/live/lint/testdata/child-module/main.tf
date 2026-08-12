# Fixture proving the check walks into child modules and reports the module
# path. The root module itself is clean.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
}
