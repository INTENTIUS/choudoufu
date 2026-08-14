# RuleModuleProviderBlock's admitted twin: the provider block is declared at
# root - exactly where live mode reads provider configurations from - and the
# child module declares none of its own. Nothing here is refused.

provider "aws" {
  region = "us-west-2"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
}
