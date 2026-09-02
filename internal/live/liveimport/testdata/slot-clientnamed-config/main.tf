# Fixture for TestApprove_WritesSlotForANamePrefixedClientNamedInstance
# (GitHub issue #372's remainder): a count-expanded, client-named type
# (aws_iam_role) named through name_prefix rather than name, so its
# per-instance identity resolves ClassNeedsDiscovery (DiscoveryNameOmitted,
# since aws_iam_role's "name" component is ServerAssignedIfAbsent and that
# check runs ahead of the name_prefix one - see resolve.go's identityArgs)
# - server-completed, exactly like a server-assigned type, but only because
# THIS declaration says so, not because the type always is.

provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_role" "this" {
  count       = 2
  name_prefix = "task-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })
}
