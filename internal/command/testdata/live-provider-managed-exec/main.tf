# GitHub issue #1113, build-plan step 1: the module-output shape with the
# credential from an exec plugin rather than a data source - the form
# terraform-aws-modules/eks's own README recommends. No data source is
# involved at all, so before #1113 this provider-configuration phase had
# nothing to read and never ran.
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

module "eks" {
  source       = "../live-provider-managed-eks-module"
  cluster_name = "demo"
}

provider "kubernetes" {
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", module.eks.cluster_name]
  }
}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "app"
  }
}
