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

# The other side of the rule, live. "arn" is assigned by the provider and
# appears nowhere in aws_ssm_parameter.seed's block, so there is nothing in
# the configuration to project and this must refuse - in the words a managed
# reference refuses in, not with a plan that quietly reads the wrong thing.
data "aws_ssm_parameter" "seed" {
  name = aws_ssm_parameter.seed.arn
}

resource "aws_cloudwatch_log_group" "derived" {
  name = "/dataread-projection/${data.aws_ssm_parameter.seed.insecure_value}"

  tags = {
    tofu-estate  = "dataread-projection-e2e"
    tofu-address = "aws_cloudwatch_log_group.derived"
  }
}
