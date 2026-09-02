# The other corpus-registered shape (never seen outside the hand-written
# limits fixture per the scoping pass, but part of the same rule): the
# alias is on the CHILD side. Modeled on
# live/e2e/limits/module-providers/aliased/main.tf.
#
# Root declares the alias the mapping names, so this call resolves.

provider "aws" {
  region = "us-west-2"
}

provider "aws" {
  alias  = "primary"
  region = "us-east-1"
}

module "aliased" {
  source = "./aliased"

  providers = {
    aws.primary = aws.primary
  }
}
