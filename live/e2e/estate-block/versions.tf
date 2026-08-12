# P4.3 estate-block fixture — provider wiring, and the one thing that
# distinguishes this directory from live/e2e/estate: the "live"
# block. See README.md for why this fixture exists as a separate directory
# instead of adding the block to the main estate.
#
# Same environment-variable wiring as the main estate (AWS_ENDPOINT_URL,
# AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION); the provider block
# carries only the flags with no env-var form.

terraform {
  required_version = ">= 1.5.0"

  # This is the block under test: its presence is what turns plain "choudoufu
  # plan"/"choudoufu apply" stateless. "stateless-e2e-block" is
  # deliberately NOT "stateless-e2e" (the main estate's name, live/e2e/
  # estate/locals.tf) — a distinct estate name means this fixture's resources
  # are foreign to the main estate and vice versa, so the two can stand up in
  # the same account without either plan seeing the other's resources as its
  # own.
  live {
    estate = "stateless-e2e-block"
  }

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

  # skip_requesting_account_id deliberately absent — same rationale as the
  # main estate's versions.tf (live/e2e/estate/versions.tf): letting the
  # provider resolve the account keeps tag-filtered discovery honest about
  # what real AWS would do.

  s3_use_path_style = true
}
