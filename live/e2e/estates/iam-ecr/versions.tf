# IAM/ECR cohort — provider wiring only, identical to live/e2e/estate/'s own
# versions.tf. See that file's comment for why the provider block carries
# only the flags with no environment-variable form.

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
