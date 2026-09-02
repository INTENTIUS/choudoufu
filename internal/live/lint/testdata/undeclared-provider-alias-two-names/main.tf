terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
    aws2 = {
      source = "hashicorp/aws"
    }
  }
}

# One provider, two local names. The block is declared under one name and
# the resource references the other; stock OpenTofu resolves both to the
# same FQN-addressed configuration and accepts this, so #123's rule must
# too. Found by the phase-5 adversarial audit of the rule's first version,
# which compared literal names.
provider "aws2" {
  alias  = "x"
  region = "us-west-2"
}

resource "aws_s3_bucket" "cross" {
  provider = aws.x
  bucket   = "lint-two-names-cross"
}
