# GitHub issue #1113, build-plan step 1: a provider block that reads a
# MANAGED value directly, with no data source between them. The AWS half of
# an EKS root written against aws_eks_cluster itself rather than through
# data.aws_eks_cluster.
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_eks_cluster" "this" {
  name = "demo"
}

provider "kubernetes" {
  host                   = aws_eks_cluster.this.endpoint
  cluster_ca_certificate = base64decode(aws_eks_cluster.this.certificate_authority[0].data)
  token                  = "static-token"
}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "app"
  }
}
