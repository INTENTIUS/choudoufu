# Two-level nesting: root maps its aliased configuration into "mid", and
# "mid" passes its own (now-aliased-in-substance, plain-named-in-syntax)
# default straight down to "leaf" with an identity mapping. Nothing at the
# mid->leaf boundary names the alias explicitly - the walk has to carry it
# through a boundary whose own text looks like the admitted, unaliased
# case.

provider "aws" {
  region = "us-west-2"
}

provider "aws" {
  alias  = "east"
  region = "us-east-1"
}

module "mid" {
  source = "./mid"

  providers = {
    aws = aws.east
  }
}
