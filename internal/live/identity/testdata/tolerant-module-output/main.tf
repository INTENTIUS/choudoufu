# The shape uyuni-project/sumaform's AWS backend is built out of, reduced to
# the four hops that matter, and the fixture behind
# [configs.StaticEvaluator.WithUnknownForRefusedReferences].
#
# The estate hands module.base a map whose skeleton is literal and one of
# whose leaves is a live subnet. module.base merges that with a LOCAL of its
# own that reads a live resource, and with a child module's whole OUTPUT, and
# publishes the result as its own output. module.host receives that output as
# one bare argument and decides two counts and three names from it.
#
# Everything module.host needs is written down somewhere in these files;
# only the subnet ID and the instance profile name are not. Stock OpenTofu
# plans this, computing both counts, because an apply-time leaf is an unknown
# to it rather than a refusal.
resource "aws_subnet" "live" {
  vpc_id     = "vpc-11111111"
  cidr_block = "10.0.0.0/24"
}

module "base" {
  source = "./base"

  provider_settings = {
    label            = "sumaform"
    public_subnet_id = aws_subnet.live.id
  }
}

module "host" {
  source = "./host"

  base_configuration = module.base.configuration
  settings           = { enabled = true }
}
