terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

# GitHub issue #123: aws.nope is declared nowhere. Under stock OpenTofu the
# graph's ProviderTransformer refuses this configuration ("Provider
# configuration not present"). Live mode resolves the address through
# statelessProviders.providerConfigValue long before that transformer runs,
# and its miss used to fall through to an empty body - the provider was
# configured from the environment alone, silently, and discovery read the
# live system through it.
resource "aws_s3_bucket" "east" {
  bucket = "tofu-undeclared-alias-east"
}

resource "aws_s3_bucket" "stray" {
  provider = aws.nope
  bucket   = "tofu-undeclared-alias-stray"
}
