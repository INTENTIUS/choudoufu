# Issue #240, site 10: the same function's "if" clause reached
# include.False() on a marked boolean.
variable "flag" {
  type      = bool
  default   = true
  sensitive = true
}

locals {
  repos = ["repo-a", "repo-b"]
}

resource "aws_ecr_repository" "github_repositories" {
  for_each = toset(local.repos)
  name     = "github/${each.key}"
}

resource "aws_ecr_lifecycle_policy" "p" {
  for_each   = toset([for repo in local.repos : aws_ecr_repository.github_repositories[repo].name if var.flag])
  repository = each.key
  policy     = "{}"
}
