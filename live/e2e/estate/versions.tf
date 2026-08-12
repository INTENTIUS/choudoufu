# P0.1 estate fixture — provider wiring only. See README.md for what each
# resource file is coverage for.
#
# Every value the provider needs to reach floci comes from the environment
# (AWS_ENDPOINT_URL, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION —
# see live/e2e/README or the roadmap's "Testing setup"). The provider
# block below carries only the flags that have no environment-variable form:
# LocalStack-style emulators need path-style S3 addressing and need the
# credential/account/metadata probes that would otherwise try to reach real
# AWS turned off.

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

  # skip_requesting_account_id is deliberately absent (P2.3). The provider
  # appends an owner-id filter, built from the account it resolved at
  # configure time, to every filtered EC2 list — and with the account
  # unresolved that filter goes out with an empty value, which floci ignores
  # and real EC2 matches nothing against. Marker discovery lists by
  # tag:tofu-estate, so a silently empty owner-id would make it find nothing
  # outside the emulator. Floci serves STS GetCallerIdentity (account
  # 000000000000), so letting the provider ask costs one request at startup
  # and keeps the fixture honest about what real AWS would do.

  s3_use_path_style = true
}
