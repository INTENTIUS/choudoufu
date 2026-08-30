# The deterministic-identity fixture (issue #541's tier-1 gap): one
# aws_iam_policy, applied, destroyed, and recreated with the identical name
# and path.
#
# An IAM policy's identity is its ARN - arn:aws:iam::<account>:policy/<name>
# - assembled entirely from things known before the create call ever goes
# out: the account this run is against, and the name and path this
# configuration declares. Creating the SAME name and path a second time,
# in the SAME account, produces the IDENTICAL ARN every time. What is NOT
# deterministic is PolicyId: IAM mints a fresh one on every real create,
# same name and path or not - proven directly against the emulator in
# run.sh before any tofu is involved, so the fixture's own premise is
# checked rather than assumed.
#
# This is the type PR #500 got backwards: its worker copied the reference
# estate's "day2_replace produces a new identity" assertion onto an
# instance whose identity does not change on replace, because that
# assertion is only true for a SERVER-MINTED identity kind. PolicyId would
# have been the right thing to assert changed; the ARN was the wrong thing
# to assert changed. run.sh pins the distinction by name so it is not
# rediscovered a fourth time.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "deterministic-recreate-e2e"
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

resource "aws_iam_policy" "subject" {
  name = "det-recreate-e2e-subject"
  path = "/"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:GetObject"
      Resource = "*"
    }]
  })
}
