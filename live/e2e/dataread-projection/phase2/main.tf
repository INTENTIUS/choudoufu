# Issue #193's read side, phase 1: the seed alone.
#
# The projection this fixture exists to prove is only meaningful once the
# managed resource exists in the cloud, because the data source reads it.
# That is the run choudoufu is for - an estate that is already there - so
# phase 1 stands the seed up and phase 2 is the run under test.
#
# Every value the provider needs to reach floci comes from the environment
# (AWS_ENDPOINT_URL, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION),
# the same wiring live/e2e/estate/versions.tf uses.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "dataread-projection-e2e"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_ssm_parameter" "seed" {
  name  = "/dataread-projection/seed"
  type  = "String"
  value = "config-only-value"

  tags = {
    tofu-estate  = "dataread-projection-e2e"
    tofu-address = "aws_ssm_parameter.seed"
  }

  # The value is deliberately NOT reconciled. run.sh overwrites it out of
  # band between the phases so that the configuration and the cloud disagree,
  # which is what makes phase 2's assertion mean something; ignoring it here
  # keeps that disagreement from turning into an apply that writes the
  # configured value back and undoes the setup.
  lifecycle {
    ignore_changes = [value]
  }
}

# Phase 2, and the whole point.
#
# "name" is an argument aws_ssm_parameter.seed's own block SETS, so it is in
# the configuration and #193's projection answers it - offline, with no state
# and no listing. Without that, this reference is a managed resource
# attribute in a static context and the run refuses.
#
# The read then happens against the live parameter, and its VALUE - which the
# configuration above deliberately does not agree with, because run.sh
# overwrites it out of band between the phases - names the log group. So the
# log group's identity can only be right if the read really happened.
data "aws_ssm_parameter" "seed" {
  name = aws_ssm_parameter.seed.name
}

resource "aws_cloudwatch_log_group" "derived" {
  name = "/dataread-projection/${data.aws_ssm_parameter.seed.insecure_value}"

  tags = {
    tofu-estate  = "dataread-projection-e2e"
    tofu-address = "aws_cloudwatch_log_group.derived"
  }
}
