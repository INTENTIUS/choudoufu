# corpus-alb-complete's Family A shape (GitHub issue #375's module-INPUT
# twin): a module call argument's object literal has one poisoned leaf
# (arn, a sibling resource's identity attribute) sitting beside several
# plain-literal siblings, and the child module's own for_each over that
# argument is FILTERED - `{ for k, v in var.policies : k => v if
# lookup(v, "create", true) }`, exactly terraform-aws-modules/alb's own
# `aws_lb_target_group_attachment.this` for_each - so the filter's own
# lookup() needs v's whole value just as much as any each.value.<attr>
# selection inside the resource block does.
#
# Three resources exercise the three routes this shape used to refuse
# through, all fixed by the same generic widening (localvalue.go's
# forCondIncludesTolerant/eachValueCondTolerant, resolve.go's
# resolveIndexedTraversal fallback - none of the three names a concrete
# aws_* type):
#
#   - aws_iam_role_policy_attachment.this: the filter itself
#     (lookup(v, "create", true)), and the poisoned leaf read directly
#     (each.value.arn).
#   - aws_iam_user.tag: a ternary whose CONDITION reads a plain-literal
#     SIBLING attribute of the same poisoned element
#     (try(each.value.kind, null) == "special" ? ... : ...) -
#     corpus-alb-complete's own aws_lb_target_group_attachment.this port
#     argument, reduced to a string identity component so it can be
#     asserted by value.
#   - aws_iam_role_policy_attachment.byindex: an INDEXED reference into a
#     DIFFERENT resource, where the index itself is a plain-literal sibling
#     attribute of the poisoned element (aws_iam_role.target[each.value.target_key].name) -
#     corpus-alb-complete's own aws_lb_target_group_attachment.additional's
#     target_group_arn argument.
resource "aws_iam_policy" "imagebuilder" {
  name   = "image-builder"
  policy = "{}"
}

resource "aws_iam_policy" "other" {
  name   = "other-policy"
  policy = "{}"
}

module "attach" {
  source = "./attach"

  role_name = "gh-image-builder"
  policies = {
    ImageBuilder = { arn = aws_iam_policy.imagebuilder.arn, kind = "special", target_key = "t1" }
    Other        = { arn = aws_iam_policy.other.arn, kind = "plain", target_key = "t2" }
  }
}
