# GitHub issue #580, case C: the same module, called for_each, but the
# prefix does NOT read each.key - so both module instances really do render
# "tl-team-0000-role" and the two configuration addresses claim one live
# role.
#
# It is here as the boundary of what this rule answers. Within either module
# instance the four indices are distinct, so count-index admits it; the
# collision is BETWEEN instances, which is a collision between two whole
# rendered identities rather than between two indices of one count, and
# internal/live/identity's checkCollisions is what refuses it. That was true
# before #580's fix (this configuration's frozen closure evaluates fine, no
# each.key being read) and is still true after, which is the point of
# keeping it.

locals {
  name_prefix = "tl"
  pod_size    = 4
}

module "m" {
  source   = "./m"
  for_each = toset(["pod-a", "pod-b"])

  prefix   = local.name_prefix
  pod_size = local.pod_size
}
