# The estate `dev-converge` converges and `dev-apply` writes to. Two taggable
# resources, `app` and `keepalive`: deleting `app` out of band is something
# `chant lifecycle plan --live` can actually see on a live root (see the
# README's "What a live root can and cannot see as drift" section for why a
# tag edit on the same resource would not have been), and its recreation is
# what "converged" looks like at the end of the demo.
#
# `keepalive` exists only so `app` can be deleted at all without also
# breaking `chant lifecycle snapshot` — measured, not assumed, while building
# this example: choudoufu reports a deleted resource as `ABSENT`, which
# chant's own describe-resources.ts doc calls "OBSERVED-ABSENT, spelled in
# neither map" — it lands in neither the `resources` map nor `unobserved`.
# With `app` the *only* declared resource, deleting it leaves `resources`
# empty (every other declared thing here is a `terraform`/`provider`/
# `variable` block, which always reads `unobserved` — see
# `dev-converge.op.ts`'s doc comment), and `chant lifecycle snapshot`
# refuses outright rather than record what looks like an empty environment:
# `terraform: nothing observed — 5 declared entity(ies) could not be read
# (see warnings); not snapshotting an unread environment as empty`. That
# refusal happens inside `ConvergeOp`'s own Observe phase, ahead of the
# Converge phase that would have classified the deletion as `createCount`,
# so a one-resource version of this root can never actually reach its own
# rule table once that resource is gone. See the README's "Two upstream
# findings this example works around" for the exact command and error.
# `keepalive` keeps `resources` non-empty regardless of what happens to
# `app`, which is the whole reason it is here — it is otherwise inert.
#
# No backend block, same reason as examples/ci-pipelines/terraform/main.tf:
# choudoufu refuses one on a live root, since ownership is the marker tags on
# the resource itself rather than an entry in a state file.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      # Pinned to the provider version choudoufu's own admission evidence was
      # surveyed against, the same pin examples/ci-pipelines/terraform/main.tf
      # carries and for the same reason: a marker's identity derives from the
      # provider's own identity schema.
      version = "~> 6.59.0"
    }
  }
}

variable "aws_region" {
  description = "Region the estate lives in. The demo script passes this as AWS_REGION."
  type        = string
  default     = "us-east-1"
}

variable "name_prefix" {
  description = "Prefix for the log group's name, so two copies of this example can coexist in one account."
  type        = string
  default     = "choudoufu-converge-operator-example"
}

provider "aws" {
  region = var.aws_region
}

# Taggable through the Resource Groups Tagging API, so its ownership marker is
# listable account-wide, and deletable with one `aws logs delete-log-group`
# call — the drift this example's demo script introduces from the CLI.
resource "aws_cloudwatch_log_group" "app" {
  name              = "/${var.name_prefix}/app"
  retention_in_days = 14

  tags = {
    Example = "converge-operator"
  }
}

# See the file-level comment above: this resource is never deleted, so a
# converge tick can always read at least one declared resource and
# `chant lifecycle snapshot` never refuses the whole environment as unread.
resource "aws_cloudwatch_log_group" "keepalive" {
  name              = "/${var.name_prefix}/keepalive"
  retention_in_days = 14

  tags = {
    Example = "converge-operator"
  }
}
