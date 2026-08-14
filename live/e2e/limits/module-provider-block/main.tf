# Limits fixture: RuleModuleProviderBlock (GitHub issue #70's ruling).
#
# The child module declares a provider block of its own. Live mode reads
# provider configurations from the root module only, so that block would
# never be consulted - the module's resources would be served by the root's
# own provider config, possibly a different account or region, with nothing
# said about it. Measured before ruling: 0 of 740 module-source files across
# the ten most-installed terraform-aws-modules repositories declare one, and
# upstream documents the shape as legacy, so it is refused.

provider "aws" {
  region = "us-west-2"
}

module "compute" {
  source = "./child"
}
