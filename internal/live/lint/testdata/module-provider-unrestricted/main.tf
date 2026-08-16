# RuleModuleProviderBlock's admitted twin (GitHub issue #201): the calling
# module block uses none of count, for_each, enabled or depends_on, so stock
# OpenTofu accepts a provider block declared inside the child module - this
# mirrors the real corpus site that drove the fix,
# simpleinfra/terraform/shared/modules/gha-iam-user/main.tf:10, a
# `provider "github" { owner = var.org }` block reached by a plain, static
# module call with no meta-arguments. Nothing here is refused.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
  region = "us-west-2"
}
