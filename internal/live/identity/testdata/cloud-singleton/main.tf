provider "aws" {
  region = "eu-west-1"
}

# No region argument of its own: the identity is the region its provider
# block declares.
resource "aws_vpc_block_public_access_options" "inherited" {
  internet_gateway_block_mode = "block-bidirectional"
}

# States its own region, which overrides the provider block's.
resource "aws_cloudwatch_otel_enrichment" "override" {
  region = "us-west-2"
}
