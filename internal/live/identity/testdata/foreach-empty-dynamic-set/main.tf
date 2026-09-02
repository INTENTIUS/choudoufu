# toset([for x in local.names : x if false]): a filtered comprehension that
# never matches produces an empty set with cty.DynamicPseudoType as its
# element type, because cty has nothing to infer a concrete one from. Stock
# OpenTofu accepts this as an empty for_each; it must not be refused as
# "Invalid for_each set" the way a genuinely non-string set is.

locals {
  names = ["a", "b"]
}

resource "aws_subnet" "none" {
  for_each = toset([for n in local.names : n if false])

  cidr_block = "10.42.1.0/24"
}
