# The static call's target - no count, no for_each. Its own contents are
# inside the stateless subset, so the only thing the fixture proves is that
# the call itself - "network" in ../main.tf - is refused today, pending the
# child-module traversal RuleChildModule names as in progress for the
# static case.

resource "aws_vpc" "main" {
  cidr_block = "10.43.0.0/16"
}
