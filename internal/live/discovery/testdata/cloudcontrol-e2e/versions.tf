# Issue #47's e2e fixture: one resource of a marker-path type with no
# native provider list resource, stood up by stock terraform and then
# recovered through Cloud Control rather than through the AWS provider's
# own list protocol. See main.tf for why aws_glue_registry rather than the
# issue's own documented example, aws_efs_file_system.
#
# Provider wiring copied verbatim from live/e2e/estate/versions.tf: same
# pinned provider version (so the plugin cache the P0.1 estate fixture
# already warms is reused rather than downloading a second copy), same
# floci-facing flags.

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
