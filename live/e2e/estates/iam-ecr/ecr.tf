# Coverage: the registry-ratified ECR half of the second registry-backed
# batch (#40's admission strategy, #44's row-gen tool, issue #26). See
# internal/live/identity/table.go's "Registry-ratified ... second batch"
# comment for the per-type evidence.

# Coverage: client-named path (aws_ecr_repository — identity is the name
# argument, already in config; confirmed against the provider's own
# identity schema, live/survey-full.json, and against the documented import
# command, which sets id to the repository name verbatim). Issue #26's
# first named type: floci's ecr:CreateRepository no longer needs a Docker
# daemon on the pinned image — see this cohort's README for the
# verification result.
resource "aws_ecr_repository" "app" {
  name = "tofu-iam-ecr-cohort-repo"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_ecr_repository.app"
  }
}

# Coverage: marker path, untaggable (aws_ecr_registry_policy — a singleton
# per AWS account; its identity is the account's own ECR registry ID, which
# pre-exists the resource and is never supplied by a configuration
# argument. Carries no tags argument in the provider — see this cohort's
# README, "Untaggable types").
resource "aws_ecr_registry_policy" "app" {
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "TofuIamEcrCohortAllowPull"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = ["ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
    }]
  })
}

# Coverage: marker path, untaggable (aws_ecr_registry_scanning_configuration
# — same singleton-per-account shape as the registry policy above; carries
# no tags argument).
resource "aws_ecr_registry_scanning_configuration" "app" {
  scan_type = "BASIC"
}

# Coverage: marker path, untaggable (aws_ecr_replication_configuration —
# same singleton-per-account shape; carries no tags argument). The
# destination is a literal placeholder region and registry ID rather than a
# second real account, the same "keep the block out of the emulator's
# boundary" choice live/e2e/estates/lambda/lambda.tf's placeholder ARNs
# make.
resource "aws_ecr_replication_configuration" "app" {
  replication_configuration {
    rule {
      destination {
        region      = "us-west-2"
        registry_id = "000000000000"
      }
    }
  }
}
