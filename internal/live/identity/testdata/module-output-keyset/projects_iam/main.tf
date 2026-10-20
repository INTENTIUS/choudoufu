# terraform-google-modules/iam v8.3.0's modules/projects_iam/main.tf, with its
# two resources moved onto an AWS type so the fixture needs no google schema:
# google_project_iam_binding's project/role become aws_iam_role_policy's
# role/name. The for_each lines are the refused main.tf:33 and :52 (#2142),
# and every identity argument reads the helper's map back by each.key, as the
# original's project and role do.

variable "projects" {
  type = list(string)
}

variable "mode" {
  type    = string
  default = "additive"
}

variable "bindings" {
  type    = map(list(string))
  default = {}
}

variable "conditional_bindings" {
  type = list(object({
    role        = string
    title       = string
    description = string
    expression  = string
    members     = list(string)
  }))
  default = []
}

module "helper" {
  source               = "../helper"
  bindings             = var.bindings
  mode                 = var.mode
  entities             = var.projects
  conditional_bindings = var.conditional_bindings
}

resource "aws_iam_role_policy" "project_iam_authoritative" {
  for_each = module.helper.set_authoritative
  role     = module.helper.bindings_authoritative[each.key].name
  name     = module.helper.bindings_authoritative[each.key].role
}

resource "aws_iam_role_policy" "project_iam_additive" {
  for_each = module.helper.set_additive
  role     = module.helper.bindings_additive[each.key].name
  name     = module.helper.bindings_additive[each.key].member
}
