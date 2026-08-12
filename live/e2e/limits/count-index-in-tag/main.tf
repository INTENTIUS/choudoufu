# Limits fixture: count.index leaking into a tag value.
#
# aws_vpc is admitted and neither meta-argument here is itself banned; the
# problem is count.index appearing inside an identity-bearing property (the
# tag), which the roadmap bans ("Banned, and why"). P3.4 landed the
# expression-analysis rule that catches this: RuleCountIndex
# (internal/live/lint/count_index.go, checkCountIndex). Check() reports
# exactly RuleCountIndex for this directory, asserted by TestLimitsEnforced
# (internal/live/lint/limits_test.go). See live/LIMITATIONS.md.

resource "aws_vpc" "this" {
  count = 2

  cidr_block = "10.${count.index}.0.0/16"

  tags = {
    Name = "vpc-${count.index}"
  }
}
