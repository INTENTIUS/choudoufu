terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
  live {
    estate = "tlmig-sample-team-b"
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_role" "team_b" {
  name               = "tlmig-sample-team-b-role"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }] })
}

resource "aws_iam_role_policy" "team_b_inline" {
  name   = "tlmig-sample-team-b-inline"
  role   = aws_iam_role.team_b.id
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = ["logs:CreateLogStream"], Resource = "*" }] })
}

resource "aws_iam_policy" "team_b" {
  name   = "tlmig-sample-team-b-policy"
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = ["logs:PutLogEvents"], Resource = "*" }] })
}

resource "aws_iam_role_policy_attachment" "team_b" {
  role       = aws_iam_role.team_b.name
  policy_arn = aws_iam_policy.team_b.arn
}

resource "aws_cloudwatch_log_group" "team_b_0" {
  name              = "/tlmig-sample/team-b/svc-0"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "team_b_1" {
  name              = "/tlmig-sample/team-b/svc-1"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "team_b_2" {
  name              = "/tlmig-sample/team-b/svc-2"
  retention_in_days = 1
}

# ---- read across the estate boundary ----
# live/OUTPUTS.md's pattern, which replaces the banned terraform_remote_state:
# a data source filtered on the producer's two marker tags. This is what
# `choudoufu live-check -json` reports as references[], with read_by naming
# aws_subnet.app below - and what the planner prices, because moving
# aws_vpc.main out of tlmig-sample-team-a means rewriting this filter.
data "aws_vpc" "team_a_network" {
  filter {
    name   = "tag:tofu-estate"
    values = ["tlmig-sample-team-a"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.main"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.team_a_network.id
  cidr_block = "10.77.1.0/24"
}
