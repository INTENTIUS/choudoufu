# GitHub issue #580: the other unsafe case that must keep refusing. The
# module call is for_each-expanded and reads each.key, so this fixture is
# inside #580's widened admission - but the prefix is built from a managed
# resource's attribute, which is not knowable from configuration alone
# before an apply. countIndexStaticRoots keeps that reference out of the
# evaluator entirely, so the domain is unavailable and the syntactic rule
# refuses var.slots[count.index] as it always did.
#
# testdata/count-index-module-foreach-distinct is this fixture with the
# prefix taken from a local instead, and nothing else changed: it resolves.

resource "aws_iam_role" "seed" {
  name               = "seed-role"
  assume_role_policy = "{}"
}

module "m" {
  source   = "./m"
  for_each = toset(["pod-a", "pod-b"])

  prefix = "${aws_iam_role.seed.name}-${each.key}"
}
