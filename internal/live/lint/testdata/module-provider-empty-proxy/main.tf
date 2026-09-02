# RuleModuleProviderBlock's other admitted shape (GitHub issue #201): the
# call uses count, which forbids a CONTENT-BEARING local provider block in
# the module it reaches - but the child module's own block below is empty,
# `provider "aws" {}`, which is internal/configs/provider_validation.go's
# own "could be a proxy configuration" shape (emptyConfigs, never counted
# toward its count/for_each/enabled/depends_on error). Stock OpenTofu builds
# this configuration without complaint, and this fork must not refuse a
# shape upstream's own loader already accepted.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
  count  = 1
}
