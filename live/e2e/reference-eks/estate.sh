# shellcheck shell=bash
# live/e2e/reference-eks/estate.sh: the reference-eks configuration (#1113),
# written once and sourced by both scripts that run it:
#
#   live/e2e/reference-eks/run.sh         the gauntlet crossing, on floci-eks
#   live/live-cert/reference-eks.sh       the real-AWS certification, whose
#                                         paid run is the maintainer's
#
# so the estate the emulator measures nightly and the estate the maintainer
# certifies are the same text. Hand-written, like reference-ec2-vpc: an
# EKS root reduced to what the substrate has to answer for, and nothing a
# module would add on top.
#
#   AWS leg      VPC, two public subnets in two zones, an internet gateway
#                and its route table, the cluster and node IAM roles, the
#                cluster, one managed node group, an access entry with a
#                view policy, and a pod identity association.
#   provider     provider "kubernetes" reads the cluster's endpoint and CA
#                straight off aws_eks_cluster.this - no data source between
#                them, the shape #1113 step 2 folds into the
#                provider-configuration fixpoint - and authenticates with
#                an exec plugin (`aws eks get-token`), the shape the
#                design's open point 4 asks to measure.
#   cluster leg  a namespace, a service account, a config map and a
#                deployment of the pause image (no rollout wait: a pod
#                scheduling is not what this estate measures).
#
# The access entry and the pod identity association are behind one
# variable, eks_access_api, true by default. floci serves only the LIST
# routes for both (EksController.java: GET /clusters/{name}/access-entries
# and GET /clusters/{name}/pod-identity-associations, no create), so a
# create there fails before anything this estate measures; the emulator
# runs pass `false` and say so in their verdicts, and the real-AWS run is
# the only one that creates them. That is a declared deviation, not a
# silent one: what the emulator cannot create is exactly what the live-cert
# cycle exists to certify (live/GAUNTLET.md, floci-eks on cold_deploy).
#
# Every function here prints HCL on stdout and needs ROOT set (for the
# provider pins in live/oracle-versions.json, read through
# live/e2e/lib/gauntlet.sh, which the caller has already sourced).

# reference_eks_terraform_block <estate-or-empty> [record-store-path]
#   The terraform block: both providers pinned from live/oracle-versions.json,
#   and, when an estate name is given, the live block.
reference_eks_terraform_block() {
  local estate="$1" store="${2:-.tofu-records}" aws_req k8s_req
  aws_req="$(gauntlet_aws_required_provider)" || return 1
  k8s_req="$(gauntlet_kubernetes_required_provider)" || return 1
  printf 'terraform {\n  required_providers {\n%s\n%s\n  }\n' "$aws_req" "$k8s_req"
  if [ -n "$estate" ]; then
    printf '  live {\n    estate = "%s"\n    record_store "local" {\n      path = "%s"\n    }\n  }\n' "$estate" "$store"
  fi
  printf '}\n'
}

# reference_eks_variables <prefix> <eks_access_api true|false>
reference_eks_variables() {
  cat <<HCL

variable "prefix" {
  type    = string
  default = "$1"
}

# See this file's header: false on floci, which cannot create either.
variable "eks_access_api" {
  type    = bool
  default = $2
}
HCL
}

# reference_eks_kubernetes_provider <region>
#   The provider block exactly as an EKS root writes it. No delta repoints
#   it on any target: floci's EKS host endpoint mode publishes k3s on
#   https://localhost:<port> with a localhost SAN, so the CA verifies as
#   written when the tools run on the host.
reference_eks_kubernetes_provider() {
  cat <<HCL

provider "kubernetes" {
  host                   = aws_eks_cluster.this.endpoint
  cluster_ca_certificate = base64decode(aws_eks_cluster.this.certificate_authority[0].data)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", aws_eks_cluster.this.name, "--region", "$1"]
  }
}
HCL
}

# reference_eks_aws_resources <region>
reference_eks_aws_resources() {
  local region="$1"
  cat <<HCL

resource "aws_vpc" "main" {
  cidr_block           = "10.40.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags = {
    Name = "\${var.prefix}-vpc"
  }
}

resource "aws_subnet" "a" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.40.1.0/24"
  availability_zone       = "${region}a"
  map_public_ip_on_launch = true
  tags = {
    Name = "\${var.prefix}-a"
  }
}

resource "aws_subnet" "b" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.40.2.0/24"
  availability_zone       = "${region}b"
  map_public_ip_on_launch = true
  tags = {
    Name = "\${var.prefix}-b"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags = {
    Name = "\${var.prefix}-igw"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }
  tags = {
    Name = "\${var.prefix}-public"
  }
}

resource "aws_route_table_association" "a" {
  subnet_id      = aws_subnet.a.id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table_association" "b" {
  subnet_id      = aws_subnet.b.id
  route_table_id = aws_route_table.public.id
}

resource "aws_iam_role" "cluster" {
  name = "\${var.prefix}-cluster"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "eks.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })
}

resource "aws_iam_role_policy_attachment" "cluster" {
  role       = aws_iam_role.cluster.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_iam_role" "node" {
  name = "\${var.prefix}-node"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "node_worker" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"
}

resource "aws_iam_role_policy_attachment" "node_cni" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
}

resource "aws_iam_role_policy_attachment" "node_ecr" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_eks_cluster" "this" {
  name     = "\${var.prefix}-eks"
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    subnet_ids = [aws_subnet.a.id, aws_subnet.b.id]
  }

  access_config {
    authentication_mode                         = "API_AND_CONFIG_MAP"
    bootstrap_cluster_creator_admin_permissions = true
  }

  depends_on = [aws_iam_role_policy_attachment.cluster]
}

resource "aws_eks_node_group" "default" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "\${var.prefix}-default"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = [aws_subnet.a.id, aws_subnet.b.id]
  instance_types  = ["t3.small"]

  scaling_config {
    desired_size = 1
    min_size     = 1
    max_size     = 1
  }

  depends_on = [
    aws_iam_role_policy_attachment.node_worker,
    aws_iam_role_policy_attachment.node_cni,
    aws_iam_role_policy_attachment.node_ecr,
    aws_route_table_association.a,
    aws_route_table_association.b,
  ]
}

data "aws_caller_identity" "current" {}

resource "aws_iam_role" "ops" {
  count = var.eks_access_api ? 1 : 0
  name  = "\${var.prefix}-ops"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::\${data.aws_caller_identity.current.account_id}:root" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_eks_access_entry" "ops" {
  count         = var.eks_access_api ? 1 : 0
  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_iam_role.ops[0].arn
}

resource "aws_eks_access_policy_association" "ops_view" {
  count         = var.eks_access_api ? 1 : 0
  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_eks_access_entry.ops[0].principal_arn
  policy_arn    = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"

  access_scope {
    type = "cluster"
  }
}

resource "aws_iam_role" "app" {
  count = var.eks_access_api ? 1 : 0
  name  = "\${var.prefix}-app"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "pods.eks.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })
}

resource "aws_eks_pod_identity_association" "app" {
  count           = var.eks_access_api ? 1 : 0
  cluster_name    = aws_eks_cluster.this.name
  namespace       = kubernetes_namespace_v1.app.metadata[0].name
  service_account = kubernetes_service_account_v1.app.metadata[0].name
  role_arn        = aws_iam_role.app[0].arn
}
HCL
}

# reference_eks_cluster_resources
reference_eks_cluster_resources() {
  cat <<'HCL'

resource "kubernetes_namespace_v1" "app" {
  metadata {
    name = "app"
  }
}

resource "kubernetes_service_account_v1" "app" {
  metadata {
    name      = "app"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
}

resource "kubernetes_config_map_v1" "app" {
  metadata {
    name      = "app-config"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
  data = {
    greeting = "hello from reference-eks"
  }
}

resource "kubernetes_deployment_v1" "app" {
  metadata {
    name      = "app"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
  wait_for_rollout = false
  spec {
    replicas = 1
    selector {
      match_labels = { app = "app" }
    }
    template {
      metadata {
        labels = { app = "app" }
      }
      spec {
        service_account_name = kubernetes_service_account_v1.app.metadata[0].name
        container {
          name  = "pause"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
}
HCL
}

# reference_eks_main_tf <estate-or-empty> <aws-provider-block> <prefix> <eks_access_api> <region>
#   The whole root. The aws provider block differs by target (floci's
#   credential and endpoint flags, or real AWS's default_tags) and is the
#   caller's; everything else is this file's.
reference_eks_main_tf() {
  local estate="$1" aws_provider="$2" prefix="$3" access="$4" region="$5"
  reference_eks_terraform_block "$estate" || return 1
  reference_eks_variables "$prefix" "$access"
  printf '\n%s\n' "$aws_provider"
  reference_eks_kubernetes_provider "$region"
  reference_eks_aws_resources "$region"
  reference_eks_cluster_resources
}

# reference_eks_cluster_leg: the four cluster-leg addresses, for a
# cluster-leg-first teardown's -target list and for the label count.
reference_eks_cluster_leg() {
  printf '%s\n' \
    kubernetes_deployment_v1.app \
    kubernetes_config_map_v1.app \
    kubernetes_service_account_v1.app \
    kubernetes_namespace_v1.app
}
