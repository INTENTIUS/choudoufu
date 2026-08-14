# Limits fixture: RuleModuleProviders (GitHub issue #104).
#
# Two calls to the same module. The first maps its aws provider to an
# aliased configuration, which live mode does not read - its resources
# would be planned against the root's default provider, in whatever account
# and region that names. The second maps to the default configuration,
# which is what live mode does anyway, and is admitted.

provider "aws" {
  region = "us-west-2"
}

provider "aws" {
  alias  = "useast1"
  region = "us-east-1"
}

module "east" {
  source = "./vpc"

  providers = {
    aws = aws.useast1
  }
}

module "west" {
  source = "./vpc"

  providers = {
    aws = aws
  }
}
