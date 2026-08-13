# The statically-keyed for_each call's target. Its own contents are inside
# the stateless subset, and since 59c the call itself - "keyed-static" in
# ../main.tf - is admitted too: its for_each is a literal set of strings, so
# RuleChildModule no longer reports it, and the five walkers traverse into
# aws_vpc.main below at "module.keyed-static[\"a\"].aws_vpc.main" and
# "module.keyed-static[\"b\"].aws_vpc.main".

resource "aws_vpc" "main" {
  cidr_block = "10.46.0.0/16"
}
