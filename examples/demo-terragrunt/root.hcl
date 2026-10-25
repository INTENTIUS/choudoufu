# Every unit includes this file, as in any Terragrunt repo: the provider, the
# backend and the binary Terragrunt runs. The one line that is about choudoufu
# is terraform_binary; Terragrunt runs it where it would run tofu.
terraform_binary = "choudoufu"

# Endpoint, region and credentials come from the environment. The provider asks
# STS for the account id: choudoufu refuses an identity it cannot resolve.
generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF2
    provider "aws" {
      region = "us-east-1"
      # The emulator's keys, named so choudoufu signs its removal sweep: floci,
      # with IAM enforcement on, refuses an unsigned tagging call.
      access_key                  = "test"
      secret_key                  = "test"
      skip_credentials_validation = true
      skip_metadata_api_check     = true
      s3_use_path_style           = true
    }
  EOF2
}

# One state key per unit, as in Terragrunt's state backend docs. Terragrunt
# reads a dependency's outputs straight from this state; choudoufu keeps no
# state file and publishes each unit's outputs here after an apply.
remote_state {
  backend = "s3"
  generate = {
    path      = "backend.tf"
    if_exists = "overwrite"
  }
  config = {
    bucket       = "demo-terragrunt-choudoufu-state"
    key          = "${path_relative_to_include()}/terraform.tfstate"
    region       = "us-east-1"
    encrypt      = true
    use_lockfile = true
    # The emulator serves S3 path-style.
    use_path_style = true
  }
}
