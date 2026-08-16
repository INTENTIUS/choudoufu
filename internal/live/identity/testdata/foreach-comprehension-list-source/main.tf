# Audit of #189's forExprKeys: the for-comprehension key-set chase read its
# SOURCE collection's key set through staticForEachKeys, which - once the
# same commit taught staticForEachKeys to union a tuple's elements' keys -
# answered a LIST with the union of its elements' own object keys.
#
# A list's keys are its integer indices. `for i, h in local.hosts` binds i
# to 0, 1, 2, so this block really expands to
#   aws_iam_user.this["item-0"], ["item-1"], ["item-2"]
# and OpenTofu creates three users named item-0/item-1/item-2.
#
# Before the fix, the chase returned the union {"host", "port"} - the keys
# of the ELEMENTS - and this resolved to TWO instances,
# aws_iam_user.this["item-host"] and ["item-port"], with import IDs
# "item-host" and "item-port", and no diagnostic at all: check.Dir reported
# the directory as clean, three instances, not blocked. Wrong addresses,
# wrong identities, wrong count, silently.
#
# forExprKeys now declines a list source, which puts the ordinary
# "Unable to compute static value" for_each refusal back in its place.

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
