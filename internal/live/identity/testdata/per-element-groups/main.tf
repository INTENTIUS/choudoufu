// The rust-lang/simpleinfra shape (.corpus/simpleinfra/terraform/team-members-access):
// a local mapping each user name to a LIST of sibling group references, a
// for_each over it, and `groups = each.value`.
locals {
  users = {
    "pietroalbini" = [aws_iam_group.infra_admins.name],
    "shepmaster"   = [aws_iam_group.infra_deploy_playground.name, aws_iam_group.infra_team.name],
    "Kobzol"       = [aws_iam_group.rustc_perf.name, aws_iam_group.infra_team.name],
  }
}

resource "aws_iam_group" "infra_admins" {
  name = "infra-admins"
}

resource "aws_iam_group" "infra_team" {
  name = "infra-team"
}

resource "aws_iam_group" "infra_deploy_playground" {
  name = "infra-deploy-playground"
}

resource "aws_iam_group" "rustc_perf" {
  name = "rustc-perf"
}

resource "aws_iam_user" "users" {
  for_each = local.users
  name     = each.key
}

resource "aws_iam_user_group_membership" "users" {
  for_each = local.users
  user     = each.key
  groups   = each.value
}

// The same identity written inline, so the list-construct path is covered
// beside the each.value hop. Written DESCENDING, so the canonical order is
// not simply the order the configuration used.
resource "aws_iam_user_group_membership" "inline" {
  user   = "carols10cents"
  groups = [aws_iam_group.rustc_perf.name, aws_iam_group.infra_admins.name]
}

// A collection reached through a variable rather than written as a
// construct: [resolver.staticElements].
variable "plain_groups" {
  type    = list(string)
  default = ["zeta", "alpha"]
}

resource "aws_iam_user_group_membership" "from_var" {
  user   = "jtgeibel"
  groups = var.plain_groups
}
