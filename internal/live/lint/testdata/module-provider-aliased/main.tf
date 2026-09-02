# RuleModuleProviderBlock's admitted case (GitHub issue #201), the aliased
# shape: same as testdata/module-provider-default, but the child module's
# local block carries an alias. Nothing here is refused either.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
}
