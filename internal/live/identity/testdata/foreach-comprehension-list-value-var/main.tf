# The commoner form of the same idiom: no index variable at all, and the
# key clause reads the ELEMENT. `{ for h in local.hosts : h.host => ... }`
# is what people write when the list already carries a natural name.
#
# It is provable for the same reason and under the same limit: local.hosts
# evaluates whole in the static scope - it is ordinary configuration data,
# with no resource, data source or module output anywhere inside it - so
# binding the value variable is copying what hclsyntax.ForExpr.Value binds,
# not standing in for something this package declined to read. The VALUE
# clause still reaches a managed resource, and is still never evaluated.
#
# Where the source is not evaluable the value variable stays unbound and a
# key clause needing it refuses; see foreach-comprehension-key-needs-value.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  hosts = [
    { host = "alpha", port = 1 },
    { host = "beta", port = 2 },
  ]

  byname = {
    for h in local.hosts : h.host => merge(h, { role = aws_iam_role.team.name })
  }
}

resource "aws_iam_user" "this" {
  for_each = local.byname
  name     = each.key
}
