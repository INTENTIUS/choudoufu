# Fixture for RuleModuleProviderBlock (GitHub issue #70's ruling): the root
# module is clean and the child module declares a default (unaliased)
# provider block, which live mode never consults.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
}
