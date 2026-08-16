# The injectivity boundary, and the shape #178 was originally about: a key
# clause that is a function of the index but not an injective one. Three
# elements, `i % 2`, two distinct keys - so two of the three would share an
# address, an address is the tofu-address marker, and a marker names one
# live object.
#
# HCL itself refuses this ("Duplicate object key: two different items
# produced the key ... in this 'for' expression"), so the configuration is
# already invalid. What matters here is that the key-set chase does not
# quietly fold three elements into two instances and report a clean
# directory, which is what deduplicating the key set did.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  hosts = [
    { host = "alpha" },
    { host = "beta" },
    { host = "gamma" },
  ]

  byidx = {
    for i, h in local.hosts : "item-${i % 2}" => merge(h, { role = aws_iam_role.team.name })
  }
}

resource "aws_iam_user" "this" {
  for_each = local.byidx
  name     = each.key
}
