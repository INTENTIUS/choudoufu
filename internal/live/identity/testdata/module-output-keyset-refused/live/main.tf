terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_iam_role" "r" {
  name               = "live"
  assume_role_policy = "{}"
}

data "aws_iam_role" "d" {
  name = "existing"
}

output "from_managed" {
  value = toset([aws_iam_role.r.unique_id])
}

output "count_from_managed" {
  value = aws_iam_role.r.max_session_duration
}

output "from_data" {
  value = toset([data.aws_iam_role.d.unique_id])
}

output "from_provider_function" {
  value = toset([provider::aws::trim_iam_role_path("/a/b")])
}

output "from_impure" {
  value = toset([uuid()])
}
