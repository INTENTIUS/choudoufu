# #209's second corpus shape (cloud-platform-infrastructure's transit-gateway
# module): the for_each names a local, and the local - not the for_each
# expression itself - is the object constructor whose value carries the
# data reference. [analyzer.forEachDataRefs] chases exactly one level of
# local aliasing, the same shape [resolver.staticForEachKeys] chases in
# resolve.go, before giving up.
provider "tfe" {
  token = "static-test-token"
}

data "tfe_outputs" "security" {
  organization = "acme"
  workspace    = "security"
}

locals {
  attachments = {
    primary = {
      access_sg_id = data.tfe_outputs.security.nonsensitive_values.access_sg_id
    }
  }
}

resource "aws_security_group_rule" "via_local" {
  for_each          = local.attachments
  type              = "ingress"
  from_port         = 5432
  to_port           = 5432
  protocol          = "tcp"
  security_group_id = each.value.access_sg_id
}
