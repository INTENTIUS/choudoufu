# The #183 shape that collides with #187, and the reason this fixture is here
# rather than only in internal/live/identity's testdata.
#
# Under this package's loader an unset required root variable is substituted
# with cty.UnknownVal, so var.name reaches the identity argument as an unknown
# and refuses in resolver.stringValue's !IsWhollyKnown branch - the exact
# branch an argument left unknown by a live read would reach. The block DOES
# expand here, unlike the for_each fixture beside it, so the collision happens
# per instance rather than being headed off by the key set.
#
# Nothing in this configuration a live read could settle. It must contribute no
# managed demand.
variable "name" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.name
}
