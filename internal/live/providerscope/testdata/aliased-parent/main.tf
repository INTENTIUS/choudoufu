# The corpus's actual shape (live/corpus-refusals.json's "module-providers"
# entry, 110 of 110 sites): a module call maps its default provider to an
# ALIASED parent configuration. Modeled on
# live/e2e/limits/module-providers/main.tf, which carries this shape plus
# the two others Resolve is also tested against in this package.

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
  source = "./west"

  providers = {
    aws = aws
  }
}
