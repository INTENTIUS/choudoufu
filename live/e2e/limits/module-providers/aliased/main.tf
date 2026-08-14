# The configuration_aliases shape. The module declares an alias of its own
# and its resources name it; the call maps that alias to a parent provider
# config. Live mode resolves the resource's own address - aws.primary -
# against the ROOT module, which declares no such block, so the provider is
# configured from the environment and nothing the mapping named reaches it.

terraform {
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      configuration_aliases = [aws.primary]
    }
  }
}

resource "aws_s3_bucket" "data" {
  provider = aws.primary

  bucket = "estate-module-providers-aliased"
}
