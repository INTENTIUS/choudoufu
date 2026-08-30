# The control for testdata/count-index-module-foreach-collides (the entries
# of var.slots made distinct) and for
# testdata/count-index-module-foreach-unprovable (the prefix taken from a
# local instead of from a managed resource's attribute). Each of those
# differs from this one in exactly the obstacle it is named for, and this
# one resolves - so neither refusal can be the module boundary itself.

locals {
  name_prefix = "tl"
}

module "m" {
  source   = "./m"
  for_each = toset(["pod-a", "pod-b"])

  prefix = "${local.name_prefix}-${each.key}"
}
