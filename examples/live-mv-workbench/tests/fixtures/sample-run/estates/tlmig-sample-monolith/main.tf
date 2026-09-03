terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
  live {
    estate = "tlmig-sample-monolith"
  }
}

provider "aws" {
  region = "us-east-1"
}


# ---- team-a ----
resource "aws_iam_role" "team_a" {
  name               = "tlmig-sample-team-a-role"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }] })
}

resource "aws_iam_role_policy" "team_a_inline" {
  name   = "tlmig-sample-team-a-inline"
  role   = aws_iam_role.team_a.id
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = ["logs:CreateLogStream"], Resource = "*" }] })
}

resource "aws_iam_policy" "team_a" {
  name   = "tlmig-sample-team-a-policy"
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = ["logs:PutLogEvents"], Resource = "*" }] })
}

resource "aws_iam_role_policy_attachment" "team_a" {
  role       = aws_iam_role.team_a.name
  policy_arn = aws_iam_policy.team_a.arn
}

resource "aws_cloudwatch_log_group" "team_a_0" {
  name              = "/tlmig-sample/team-a/svc-0"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "team_a_1" {
  name              = "/tlmig-sample/team-a/svc-1"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "team_a_2" {
  name              = "/tlmig-sample/team-a/svc-2"
  retention_in_days = 1
}

# ---- team-b ----
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

# ---- team-c ----
resource "aws_iam_role" "team_c" {
  name               = "tlmig-sample-team-c-role"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }] })
}

resource "aws_iam_role_policy" "team_c_inline" {
  name   = "tlmig-sample-team-c-inline"
  role   = aws_iam_role.team_c.id
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = ["logs:CreateLogStream"], Resource = "*" }] })
}

resource "aws_iam_policy" "team_c" {
  name   = "tlmig-sample-team-c-policy"
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = ["logs:PutLogEvents"], Resource = "*" }] })
}

resource "aws_iam_role_policy_attachment" "team_c" {
  role       = aws_iam_role.team_c.name
  policy_arn = aws_iam_policy.team_c.arn
}

resource "aws_cloudwatch_log_group" "team_c_0" {
  name              = "/tlmig-sample/team-c/svc-0"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "team_c_1" {
  name              = "/tlmig-sample/team-c/svc-1"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "team_c_2" {
  name              = "/tlmig-sample/team-c/svc-2"
  retention_in_days = 1
}
