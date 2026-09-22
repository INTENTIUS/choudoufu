# GitHub issue #1258's instrument: ten managed blocks across two providers,
# one of which (kubernetes) is configured from a data source whose own
# argument reads a managed resource's provider-assigned attribute - the
# #313 fixpoint's shape, corpus-eks-basic's reduced to what the counts need.
#
# The ACM/Route53 pair is what makes a first resolution pass refuse with a
# managed demand, which is the only condition under which
# projection.PlanInstances runs at all. Nine of the ten blocks are plannable
# (no for_each); the record is the tenth.
#
# -target=aws_route53_record.cert_validation keeps the record and the
# certificate it reads. The other eight blocks, the data source and the
# kubernetes provider all leave the plan graph.
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

resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}

resource "aws_eks_cluster" "this" {
  name = "demo"
}

data "aws_eks_cluster" "cluster" {
  name = aws_eks_cluster.this.id
}

# Directly readable, unlike the cluster above: nothing in its arguments
# waits on a managed resource. It is here so that the run's scope, and not
# the managed-read demand, is what decides whether it is read - the two
# halves of #1258's narrowing are otherwise indistinguishable on this
# fixture.
data "aws_region" "current" {}

provider "kubernetes" {
  host  = data.aws_eks_cluster.cluster.endpoint
  token = data.aws_region.current.name
}

resource "aws_s3_bucket" "logs" {
  bucket = "target-scope-1258-logs"
}

resource "aws_s3_bucket" "assets" {
  bucket = "target-scope-1258-assets"
}

resource "aws_sqs_queue" "jobs" {
  name = "target-scope-1258-jobs"
}

resource "aws_sns_topic" "events" {
  name = "target-scope-1258-events"
}

resource "aws_cloudwatch_log_group" "app" {
  name = "/target-scope-1258/app"
}

resource "kubernetes_namespace" "app" {
  metadata {
    name = "app"
  }
}

resource "kubernetes_config_map" "settings" {
  metadata {
    name      = "settings"
    namespace = "app"
  }
}
