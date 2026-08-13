# The static call's target - no count, no for_each. Its own contents are
# inside the stateless subset, and since 59b the call itself - "network" in
# ../main.tf - is admitted too: RuleChildModule no longer reports it, and
# the five walkers traverse into aws_vpc.main below at
# "module.network.aws_vpc.main".

resource "aws_vpc" "main" {
  cidr_block = "10.43.0.0/16"
}
