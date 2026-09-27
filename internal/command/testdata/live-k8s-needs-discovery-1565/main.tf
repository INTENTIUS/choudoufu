terraform {
  required_providers {
    aws        = { source = "hashicorp/aws" }
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}

provider "aws" {
  region = "us-east-1"
}

# GitHub issue #1565. Every way this fixture's author could find to name a
# Kubernetes object from something other than a literal. None of them may
# resolve to NEEDS_DISCOVERY: internal/live/check's NodeStampUnmarkedApply
# only ever speaks about needs-discovery blocks, and its exemption in
# internal/live/markers/seams_test.go rests on no label or manifest type
# being one. aws_vpc is the control: it is server-assigned and must be.

resource "aws_vpc" "server_assigned" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_s3_bucket" "named" {
  bucket = "k8s-needs-discovery-1565"
}

resource "kubernetes_namespace" "ns" {
  metadata {
    name = "ns-1565"
  }
}

resource "kubernetes_config_map" "literal" {
  metadata {
    name      = "literal"
    namespace = "default"
  }
}

resource "kubernetes_config_map" "from_server_assigned_parent" {
  metadata {
    name      = aws_vpc.server_assigned.id
    namespace = "default"
  }
}

resource "kubernetes_config_map" "from_computed_attribute" {
  metadata {
    name      = aws_s3_bucket.named.arn
    namespace = "default"
  }
}

resource "kubernetes_config_map" "from_namespace" {
  metadata {
    name      = "in-ns"
    namespace = kubernetes_namespace.ns.metadata[0].name
  }
}

resource "kubernetes_config_map_v1" "unrowed" {
  metadata {
    name      = "unrowed"
    namespace = kubernetes_namespace.ns.metadata[0].name
  }
}

resource "kubernetes_manifest" "crd_object" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "crontab"
      namespace = "default"
    }
  }
}

resource "kubernetes_config_map_v1" "from_manifest" {
  metadata {
    name      = kubernetes_manifest.crd_object.object.metadata.name
    namespace = "default"
  }
}
