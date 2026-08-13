# Issue #64's provider-alias e2e fixture: two aws provider configurations -
# the default and one aliased "west" - each pointed at a different region
# against floci's single endpoint. live_plan handles Alias structurally
# (identity.Resolve and projection key every resolution by its own
# addrs.AbsProviderConfig, alias included) but no fixture exercised it before
# this one; alias_live_test.go's TestAliasedProvidersAgainstFloci is what
# proves - or disproves - that a real plan over both actually works.
#
# Provider wiring copied verbatim from live/e2e/estate/versions.tf for the
# default provider (same pinned version, same floci-facing flags, so the
# plugin cache the P0.1 estate fixture already warms is reused); the "west"
# alias repeats the same flags with a different region, which is what makes
# this fixture a real test of internal/live/cloudcontrol/doc.go's
# region-scope header machinery: floci is one host for every region, and
# without that header a multi-region estate reads as one region silently.

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
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

provider "aws" {
  alias  = "west"
  region = "us-west-2"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
