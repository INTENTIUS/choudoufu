# Issue #240, site 8: the same function's "if" clause, one branch further
# down, reached include.False() on a marked boolean.
variable "flag" {
  type      = bool
  default   = true
  sensitive = true
}

resource "aws_ecr_repository" "src" {
  name    = "src"
  records = ["a.example.com", "b.example.com"]
}

resource "aws_route53_record" "v" {
  for_each = { for d in aws_ecr_repository.src.records : d => d if var.flag }

  zone_id = "Z1"
  name    = each.key
  type    = "CNAME"
  ttl     = 60
  records = ["x"]
}
