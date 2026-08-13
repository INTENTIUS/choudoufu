# live-import e2e fixture (issue #61) — provider wiring only.
#
# This directory is deliberately a *plain* configuration: no live block, and
# no tofu-estate / tofu-address tags anywhere. It stands in for an estate
# that has been running under ordinary state-backed OpenTofu/Terraform for
# years and has never heard of live resource markers - exactly the estate
# "choudoufu live-import" exists to migrate.
#
# Every value the provider needs to reach floci comes from the environment
# (AWS_ENDPOINT_URL, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION -
# see live/e2e/README). The provider block below carries only the flags that
# have no environment-variable form, mirroring live/e2e/estate/versions.tf.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
