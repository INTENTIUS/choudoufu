# GitHub issue #384: aws_security_group_rule's SoleElement alternation
# (cidr_blocks/ipv6_cidr_blocks/prefix_list_ids/source_security_group_id) is
# genuinely ambiguous when TWO of those are non-empty at once - the shape
# terraform-aws-modules/security-group's egress_rules = ["all-all"] produces,
# because the module defaults both egress_cidr_blocks and
# egress_ipv6_cidr_blocks and AWS creates two live rule objects for the one
# declared instance. This fixture is only used with a SYNTHETIC schema
# (TestRecordFallbackClassifiesSoleElementConflict) that makes the type's
# whole documented import grammar resolvable as top-level strings, so the
# record rung's OWN wiring can be asserted independent of whether
# aws_security_group_rule's real hashicorp/aws schema (numeric from_port/
# to_port) currently qualifies - see that test's doc comment.
terraform {
  live {
    estate = "record-fallback-solelement-conflict"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "aws_security_group_rule" "egress_all_all" {
  security_group_id = "sg-0123456789abcdef0"
  type               = "egress"
  protocol           = "-1"
  from_port          = 0
  to_port            = 0
  cidr_blocks        = ["0.0.0.0/0"]
  ipv6_cidr_blocks   = ["::/0"]
}
