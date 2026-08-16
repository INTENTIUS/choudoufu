# The one real corpus site #213's fix touched (govuk-infrastructure's
# govuk-publishing-infrastructure deployment, security.tf's
# aws_security_group_rule.postgres_from_eks_workers), generalized past its
# specific resource names: for_each ranges over an object constructor whose
# one value is not statically known (#178's key-set fix, staticForEachKeys,
# expands it from the KEY alone), and the resource's OWN identity argument
# reads each.value directly - a bare top-level reference, not through a
# local.
#
# The object constructor is wrapped in local.members here, which the real
# site's for_each did not need: the real value was a data source reference
# (data.tfe_outputs...), and [resolver.isSymbolic] already treats "data" as
# not-symbolic, so evalStatic gets to try and fail on its own, falling
# through to staticForEachKeys directly. A bare managed-resource reference
# in the same position - aws_iam_group.admins.name below, with no local
# wrapper - makes isSymbolic treat the WHOLE for_each expression as "ranges
# over a resource" (forEachOverResource) instead, a different code path
# this fixture is not testing. Routing the same shape through a local, the
# way local-attr-foreach already does for the key half, reaches
# staticForEachKeys the same way the real site's data-source value did.
#
# each.value is genuinely unknown here (aws_iam_group.admins.name is a
# managed-resource reference, never statically evaluable, by design - see
# staticScopeData.StaticValidateReferences' default case in
# internal/configs/static_scope.go). Before #213's change to evalPure (it
# used to filter "each"/"count" out of the traversal set entirely, then
# splice scope.vars into the EvalContext post hoc, unconditionally,
# regardless of whether the "each" object it built actually had a "value"
# key), this refused too, but via HCL's own bare "Unsupported attribute"
# diagnostic (accessing .value on an object that was only ever given a
# "key" attribute) rather than this package's structured, categorized
# "Dynamic value in static context". Same outcome - the run is still
# blocked - better diagnostic.
resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "alice" = aws_iam_group.admins.name
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
