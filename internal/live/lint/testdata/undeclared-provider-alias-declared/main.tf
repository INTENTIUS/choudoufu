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

provider "aws" {
  alias  = "west"
  region = "us-west-2"
}

# The alias resolves to a declared root provider block, so #123's rule has
# nothing to say; this is the multi-provider shape issue #69 admitted.
resource "aws_s3_bucket" "west" {
  provider = aws.west
  bucket   = "lint-declared-alias-west"
}
