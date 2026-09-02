# Provider wiring only, the same shape as every cohort's versions.tf: only
# the flags with no environment-variable form live here; endpoint,
# credentials and region come from the environment the test sets.

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
