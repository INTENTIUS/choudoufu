# Negative control for TestApprove_WritesNoSlotForALiteralNamedInstance:
# the same client-named type, count-expanded, but named through a static
# literal "name" rather than name_prefix, so its identity resolves
# ClassConcrete - and gate 4 must still leave it blocked, proving the
# Config-driven half of the gate does not just admit every client-named
# type wholesale.

provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_role" "this" {
  count = 2
  name  = "task-${count.index}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })
}
