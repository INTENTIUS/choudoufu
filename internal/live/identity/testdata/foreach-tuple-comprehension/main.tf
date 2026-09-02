# GitHub issue #220, confirmed site: govuk-infrastructure's
# terraform/deployments/ecr/main.tf. aws_ecr_lifecycle_policy's for_each is a
# keyless (tuple-producing) for-expression wrapped in toset(): the collection
# is an ordinary local, and the per-element value indexes into a sibling
# resource by the loop variable and reads its own "name" argument - which is
# already that sibling's whole identity (aws_ecr_repository imports by
# NAME), so this is a for_each-expansion gap, not an identity-boundary one.

locals {
  lifecycle_policy_repositories = ["repo-a", "repo-b"]
}

resource "aws_ecr_repository" "github_repositories" {
  for_each = toset(local.lifecycle_policy_repositories)
  name     = "github/alphagov/govuk/${each.key}"
}

resource "aws_ecr_lifecycle_policy" "ecr_lifecycle_policy" {
  for_each   = toset([for repo in local.lifecycle_policy_repositories : aws_ecr_repository.github_repositories[repo].name])
  repository = each.key
  policy     = "{}"
}
