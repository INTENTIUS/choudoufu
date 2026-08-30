# GitHub issue #580's adversarial case: the module call DOES read each.key,
# so #580's fix is what makes this configuration renderable at all - and
# then throws the key away, so both module instances are passed the same
# prefix and the eight names collapse onto four.
#
# This is the one shape the widened admission newly reaches that is not
# safe, so it is the shape worth pinning: count-index admits it (the four
# indices within one module instance are distinct), and
# internal/live/identity's checkCollisions refuses the estate. If that ever
# stops being true, this fixture is where a wrong marker gets written.

locals {
  name_prefix = "tl"
  pod_size    = 4
}

module "m" {
  source   = "./m"
  for_each = toset(["pod-a", "pod-b"])

  prefix   = "${local.name_prefix}-${substr(each.key, 0, 0)}"
  pod_size = local.pod_size
}
