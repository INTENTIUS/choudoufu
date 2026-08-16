# for_each = { for k, v in <resource> : keyExpr => valExpr if cond }: the
# corpus shape (govuk-infrastructure/deployments/github/main.tf, #178's
# for-comprehension bucket) with aws_subnet standing in for
# github_repository. The key clause is the loop key itself, unchanged, and
# the "if" filter reads only the key and a local - never the loop value -
# so the resulting key set is knowable from aws_subnet.this's own for_each
# without evaluating a single subnet attribute.
resource "aws_subnet" "this" {
  for_each = toset(["a", "b", "c"])

  cidr_block = "10.42.1.0/24"
}

locals {
  selected = ["a", "c"]
}

resource "aws_route_table_association" "selected" {
  for_each = { for k, v in aws_subnet.this : k => v if contains(local.selected, k) }

  subnet_id      = "subnet-${each.key}"
  route_table_id = "rtb-fixed"
}
