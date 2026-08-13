# Issue #51's e2e fixture: one resource of a type the ARN join table
# recognizes, stood up by stock terraform and recovered through the
# estate-wide tagging sweep (Request.TaggingSweep) rather than through a
# per-type list. See main.tf for the type choice.
#
# Provider wiring copied verbatim from live/e2e/estate/versions.tf, the same
# way testdata/cloudcontrol-e2e/versions.tf does: same pinned provider
# version so the plugin cache the P0.1 estate fixture already warms is
# reused, same floci-facing flags.

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
