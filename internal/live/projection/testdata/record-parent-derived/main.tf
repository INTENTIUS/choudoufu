# GitHub issue #391's shape: a PARENT_DERIVED sibling whose formula names a
# GitHub issue #364 record-backed instance as a parent. null_resource.suffix
# stands in for corpus-eks-basic's random_string.suffix - a logical resource
# with no live object at all, whose value only the estate's record store
# holds - and aws_cloudwatch_log_group.app stands in for
# aws_eks_cluster.this[0], a live-read resource whose own import identity
# is a formula over that parent's value.
#
# The formula itself is built by hand in the test, not derived from this
# config's own expressions (ReadInstances takes a Resolution's Formula as
# already-settled input); this file exists only so mustAddr and the
# provider/schema machinery have a declared block to attach to.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
    null = {
      source = "hashicorp/null"
    }
  }
}

resource "null_resource" "suffix" {}

resource "aws_cloudwatch_log_group" "app" {
  name = "placeholder"
}
