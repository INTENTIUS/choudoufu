# A for_each key carrying ":" — AWS-legal in a tag value, which is what made
# it dangerous: it stamps, it applies, and only the next run finds a marker
# nothing can parse. Expansion must refuse it before it becomes an address.
resource "aws_subnet" "this" {
  for_each = toset(["2001:db8::/64"])

  cidr_block = "10.42.1.0/24"
}
