# Negative control for TestApprove_WritesNoSlotForAMarkerFallbackInstance:
# a count-expanded, client-named, DiscoverableFallbackType (aws_iam_role)
# whose "name" is present but impure (uuid()), so per-instance resolution
# is ClassNeedsDiscovery/DiscoveryMarkerFallback rather than
# DiscoveryNameOmitted or DiscoveryNamePrefix. Gate 4's Config-driven half
# must not unblock it: MarkerFallback is exactly the cause
# causeStableWithoutManagedResults excludes, because a real live-plan's
# two-pass resolution can turn it into something else once ManagedResults
# is in hand - measured directly on corpus-ecs-fargate's
# aws_ecs_service.this[0], which this fixture stands in for without needing
# a cross-module ARN transform to reproduce.

provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_role" "this" {
  count = 2
  name  = uuid()

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })
}
