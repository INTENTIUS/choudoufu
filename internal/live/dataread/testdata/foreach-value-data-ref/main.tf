# #209: govuk-infrastructure's actual corpus shape - a for_each map literal
# whose only value is a data source reference, with the identity-bearing
# argument reading each.value rather than the data source directly. The
# object's key ("content_data_api") is a plain string literal, so
# resolve.go's #178 key-set fix (staticForEachKeys) can expand the block
# without ever evaluating the value - which is exactly the path that used
# to leave this data source undiscovered: nothing ever asked
# resolve.go to evaluate the whole for_each value, so its diagnostic naming
# the data source never had a reason to exist, let alone reach
# [demandRoots]. [analyzer.scanForEachDataRefs] finds it by reading the
# for_each expression directly instead of waiting on a diagnostic.
provider "tfe" {
  token = "static-test-token"
}

data "tfe_outputs" "security" {
  organization = "acme"
  workspace    = "security"
}

resource "aws_security_group_rule" "postgres_from_eks_workers" {
  for_each          = { "content_data_api" = data.tfe_outputs.security.nonsensitive_values.access_sg_id }
  type              = "ingress"
  from_port         = 5432
  to_port           = 5432
  protocol          = "tcp"
  security_group_id = each.value
}
