# GitHub issue #1113, build-plan step 1: the shape of
# .corpus/k8s-io/infra/aws/terraform/prow-build-cluster/providers.tf. The
# endpoint and the CA come through module outputs that read the cluster's
# provider-assigned attributes; the token comes through
# data.aws_eks_cluster_auth, whose own argument is a module output too.
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

data "aws_eks_cluster_auth" "eks" {
  name = module.eks.cluster_name
}

provider "kubernetes" {
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
  token                  = data.aws_eks_cluster_auth.eks.token
}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "app"
  }
}
