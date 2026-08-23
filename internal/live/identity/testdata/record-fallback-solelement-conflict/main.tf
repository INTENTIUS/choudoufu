# GitHub issue #384: aws_security_group_rule's SoleElement alternation
# (cidr_blocks/ipv6_cidr_blocks/prefix_list_ids/source_security_group_id) is
# genuinely ambiguous when TWO of those are non-empty at once - the shape
# terraform-aws-modules/security-group's egress_rules = ["all-all"] produces,
# because the module defaults both egress_cidr_blocks and
# egress_ipv6_cidr_blocks and AWS creates two live rule objects for the one
# declared instance.
#
# TestSoleElementConflictNeverBindsAConcreteIdentity resolves this fixture
# with no schemas at all, proving the instance never binds a concrete
# identity. TestSecurityGroupRuleSourceSegmentStaysRefused resolves it again
# against aws_security_group_rule's REAL hashicorp/aws 6.59.0 schema (numeric
# from_port/to_port included) with a record_store declared, and proves the
# SAME refusal holds even though the type's identity CAN now be partly
# corroborated (issue #384's follow-up): the "cidr_block" segment of its
# documented import string has no safe resolution, so the record rung is not
# reached - see that test's doc comment for why, and for the deeper gap
# (a variadic trailing-source segment) left standing.
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
