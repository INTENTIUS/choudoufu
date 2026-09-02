# Issue #240, site 7: resolver.comprehensionOverSiblingAttr iterated the
# sibling's literal argument without testing it for marks. The sibling sets
# that argument from a sensitive variable, so the collection arrives marked
# and ElementIterator panics. Needs a schema: siblingLiteralExpr only
# applies when the attribute is in the type's schema and not Computed.
variable "domains" {
  type      = list(string)
  default   = ["a.example.com", "b.example.com"]
  sensitive = true
}

resource "aws_ecr_repository" "src" {
  name    = "src"
  records = var.domains
}

resource "aws_route53_record" "v" {
  for_each = { for d in aws_ecr_repository.src.records : d => d }

  zone_id = "Z1"
  name    = each.key
  type    = "CNAME"
  ttl     = 60
  records = ["x"]
}
