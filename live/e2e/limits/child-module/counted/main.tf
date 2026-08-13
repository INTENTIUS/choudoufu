# The count-expanded call's target. Its own contents are inside the
# stateless subset, so the only thing the fixture proves is that the call
# itself - "counted" in ../main.tf - is refused, permanently, for the
# renumbering reason RuleChildModule names for count.

resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
