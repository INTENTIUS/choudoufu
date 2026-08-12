# Fixture that must produce no issues: admitted types only, exercising the
# meta-arguments that survive the subset (count as cardinality, for_each with
# stable keys and each.key/each.value, the count = var.enabled ? 1 : 0 idiom)
# plus a data source of an admitted-adjacent type, which the type rules
# deliberately do not police. aws_eip.pool and aws_cloudwatch_log_group.meta
# also cover the count.index rule's clean side (P3.4): a resource that uses
# count but never reads count.index, and count.index appearing only in the
# count expression itself, a meta-argument position out of that rule's scope
# regardless of what it contains — see the "Scope of the count.index rule"
# section of doc.go.

variable "enabled" {
  type    = bool
  default = true
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_subnet" "this" {
  for_each = {
    a = "10.42.1.0/24"
    b = "10.42.2.0/24"
  }

  vpc_id     = aws_vpc.main.id
  cidr_block = each.value

  tags = {
    Name = each.key
  }
}

resource "aws_eip" "pool" {
  count = 3

  domain = "vpc"
}

resource "aws_cloudwatch_log_group" "optional" {
  count = var.enabled ? 1 : 0

  name = "/stateless-lint/optional"
}

resource "aws_cloudwatch_log_group" "meta" {
  # count.index here is nonsensical config (count is not defined while count
  # itself is being computed) and would fail at evaluation time, but lint
  # does no evaluation, only structural walking of resource.Config — which
  # never includes the count expression at all. This exists purely to prove
  # that boundary rather than assume it.
  count = tobool(count.index) ? 1 : 0

  name = "/stateless-lint/meta"
}

data "aws_region" "current" {
}
