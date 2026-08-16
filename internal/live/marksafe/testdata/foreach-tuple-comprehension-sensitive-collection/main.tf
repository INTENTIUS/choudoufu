# Issue #240, site 9: resolver.forEachOverTupleComprehension iterated the
# collection clause without testing it for marks. Found by this package's
# mark-injection sweep over identity/testdata/foreach-tuple-comprehension.
variable "repos" {
  type      = list(string)
  default   = ["repo-a", "repo-b"]
  sensitive = true
}

resource "aws_ecr_repository" "github_repositories" {
  for_each = toset(["repo-a", "repo-b"])
  name     = "github/${each.key}"
}

resource "aws_ecr_lifecycle_policy" "p" {
  for_each   = toset([for repo in var.repos : aws_ecr_repository.github_repositories[repo].name])
  repository = each.key
  policy     = "{}"
}
