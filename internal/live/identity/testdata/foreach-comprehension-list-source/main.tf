# #239: a for-comprehension ranging over a LIST. A list's keys are its
# integer indices, so `for i, h in local.hosts` binds i to 0, 1, 2 and this
# block expands to
#   aws_iam_user.this["item-0"], ["item-1"], ["item-2"]
# with OpenTofu creating three users named item-0/item-1/item-2.
#
# This fixture began as the audit's wrong-key-set finding. #189 taught
# staticForEachKeys to union a tuple's elements' own object keys, and
# forExprKeys read its source collection through that, so the chase
# answered the union {"host", "port"} - the keys of the ELEMENTS - and this
# resolved to TWO instances, aws_iam_user.this["item-host"] and
# ["item-port"], with import IDs "item-host" and "item-port", and no
# diagnostic at all: check.Dir reported the directory as clean, three
# instances, not blocked. Wrong addresses, wrong identities, wrong count,
# silently.
#
# The audit fix bought correctness by refusing the shape outright. #239
# recovers it: the chase now carries the key's cty TYPE, so a list source
# binds the loop's key variable to a number exactly as
# hclsyntax.ForExpr.Value does, and the key set is the three indices rather
# than an invention.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  # A list of three uniform objects - the ordinary "index the list" idiom.
  hosts = [
    { host = "alpha", port = 1 },
    { host = "beta", port = 2 },
    { host = "gamma", port = 3 },
  ]

  # The value clause reads a managed resource's attribute, so evaluating
  # byidx as a whole fails and the key-set chase is what runs.
  byidx = {
    for i, h in local.hosts : "item-${i}" => merge(h, { role = aws_iam_role.team.name })
  }
}

resource "aws_iam_user" "this" {
  for_each = local.byidx
  name     = each.key
}
