# The for_each-expanded call's target. Its own contents are inside the
# stateless subset, so the only thing the fixture proves is that the call
# itself - "keyed" in ../main.tf - is refused today, pending the keyed
# module traversal RuleChildModule names as planned for for_each.

resource "aws_vpc" "main" {
  cidr_block = "10.45.0.0/16"
}
