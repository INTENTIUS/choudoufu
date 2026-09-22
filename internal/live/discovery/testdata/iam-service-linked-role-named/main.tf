# Fixture for GitHub issue #1477: the iam-ecr cohort's shape. The ordinary
# aws_iam_role carries a configured name, so it resolves from configuration
# and nothing scans aws_iam_role on discovery's behalf - which is what took
# away #302's producer for the service-linked role: IAM's ListRoles surfaces
# service-linked roles beside ordinary ones, but only when something makes
# that call, and the provider's aws_iam_role list resource filters them out
# anyway. The service-linked role is left with its own type's enumeration,
# which has no provider list resource and no Cloud Control list handler.

resource "aws_iam_role" "named" {
  name               = "tofu-iam-ecr-cohort-iam-role"
  assume_role_policy = jsonencode({})
}

resource "aws_iam_service_linked_role" "app" {
  aws_service_name = "elasticbeanstalk.amazonaws.com"
}
