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

# The provider-configuration data read: this data source's value is what the
# aliased provider block below is configured from, which is the shape
# corpus-eks-basic's provider "kubernetes" has against data.aws_eks_cluster.
data "aws_s3_bucket" "config" {
  bucket = "tofu-import-unit-config"
}

provider "aws" {
  alias  = "derived"
  region = data.aws_s3_bucket.config.region
}

# The estate's one managed resource, declared through the derived provider
# configuration - which is what the state file names as its own, and what
# makes the aliased block have to be evaluated to ratify it.
resource "aws_s3_bucket" "data" {
  provider = aws.derived
  bucket   = "tofu-import-unit-data"
}
