# GitHub issue #580: the unsafe case that must keep refusing. The module
# call is for_each-expanded and reads each.key exactly as case A's does, so
# #580's fix renders the values rather than giving up on them - and what it
# then sees is that indices 0 and 2 of every module instance pick the same
# entry out of var.slots. Two addresses, one name, in one module instance.
#
# testdata/count-index-module-foreach-distinct is this fixture with the
# duplicate entries removed and nothing else changed: it resolves. That pair
# is what shows this refusal fires for the reason it claims rather than for
# the module boundary.

locals {
  name_prefix = "tl"
}

module "m" {
  source   = "./m"
  for_each = toset(["pod-a", "pod-b"])

  prefix = "${local.name_prefix}-${each.key}"
}
