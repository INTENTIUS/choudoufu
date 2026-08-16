# Issue #240, site 6: resolver.forEachOverComprehension called
# include.False() on the "if" clause's value. cty.Value.False calls True,
# which asserts the value is unmarked and panics. Found by this package's
# mark-injection sweep over identity/testdata/foreach-comprehension.
variable "selected" {
  type      = list(string)
  default   = ["a", "c"]
  sensitive = true
}

resource "aws_subnet" "this" {
  for_each = toset(["a", "b", "c"])

  cidr_block = "10.42.1.0/24"
}

resource "aws_route_table_association" "selected" {
  for_each = { for k, v in aws_subnet.this : k => v if contains(var.selected, k) }

  subnet_id      = "subnet-${each.key}"
  route_table_id = "rtb-fixed"
}
