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
#   cluster leg  a namespace, a service account, a config map, two counted
#                shard config maps (day2_count's block) and a deployment of
#                the pause image (no rollout wait: a pod scheduling is not
#                what this estate measures).
#
# The day-2 stages' configurations are written here too, as variants of the
# cluster leg (reference_eks_cluster_resources) and as the few extra blocks
# a stage adds and removes again (the crash pair, the create_before_destroy
# rename, the strict scratch root, the stock oracle's own root). The stage
# bodies that apply them are live/e2e/reference-eks/stages.sh, shared by the
# same two scripts.
#
# The access entry and the pod identity association are behind one
# variable, eks_access_api, true by default. floci main serves access-entry
# create since lex00/floci 6b389c33e (#3805), but pod identity associations
# only through their LIST route (EksController.java: GET
# /clusters/{name}/pod-identity-associations, no create), and whether the
# image live/floci-image pins carries the access-entry routes has not been
# checked. So the emulator runs pass `false` and say so in their verdicts,
# and the real-AWS run is the only one that creates them; turning the access
# entry on for the emulator is a split of this variable once the pin is
# known to carry it. That is a declared deviation, not a
# silent one: what the emulator cannot create is exactly what the live-cert
# cycle exists to certify (live/GAUNTLET.md, floci-eks on cold_deploy).
#
# Every function here prints HCL on stdout and needs ROOT set (for the
# provider pins in live/oracle-versions.json, read through
# live/e2e/lib/gauntlet.sh, which the caller has already sourced).

# reference_eks_terraform_block <estate-or-empty> [record-store]
#   The terraform block: both providers pinned from live/oracle-versions.json,
#   and, when an estate name is given, the live block. The second argument
#   is either a local record store's path (default .tofu-records) or a whole
#   `record_store "..." { ... }` block, verbatim - what
#   reference_eks_kubernetes_store prints for the live-cert cycle's records
#   check (#1524).
reference_eks_terraform_block() {
  local estate="$1" store="${2:-.tofu-records}" aws_req k8s_req
  aws_req="$(gauntlet_aws_required_provider)" || return 1
  k8s_req="$(gauntlet_kubernetes_required_provider)" || return 1
  printf 'terraform {\n  required_providers {\n%s\n%s\n  }\n' "$aws_req" "$k8s_req"
  if [ -n "$estate" ]; then
    case "$store" in
      record_store*)
        printf '  live {\n    estate = "%s"\n%s\n  }\n' "$estate" "$(printf '%s\n' "$store" | sed 's/^/    /')" ;;
      *)
        printf '  live {\n    estate = "%s"\n    record_store "local" {\n      path = "%s"\n    }\n  }\n' "$estate" "$store" ;;
    esac
  fi
  printf '}\n'
}

# reference_eks_kubernetes_store <kubeconfig> <namespace> <cluster> <region> [allow_insecure]
#   A `record_store "kubernetes"` block whose records live in the estate's
#   own cluster, with the `control_plane "eks"` block #1524 added, so
#   encryption_at_rest is read from DescribeCluster rather than reported NOT
#   CHECKED. allow_insecure, when given, is an HCL list literal naming the
#   contract assertions the caller measured as not OK - the live-cert cycle
#   computes it from `choudoufu live-cluster -json` first, the way
#   live/managed-k8s/harness.sh does, so the run measures the store and
#   prints its waiver on every run. Every value is a literal: the block is
#   decoded before any expression could be evaluated.
reference_eks_kubernetes_store() {
  local kubeconfig="$1" ns="$2" cluster="$3" region="$4" waive="${5:-}"
  printf 'record_store "kubernetes" {\n'
  printf '  namespace   = "%s"\n' "$ns"
  printf '  config_path = "%s"\n' "$kubeconfig"
  [ -n "$waive" ] && printf '  allow_insecure = %s\n' "$waive"
  printf '  control_plane "eks" {\n    name = "%s"\n' "$cluster"
  [ -n "$region" ] && printf '    region = "%s"\n' "$region"
  printf '  }\n}\n'
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

# reference_eks_aws_resources <region> [service-account block label]
#   The label defaults to app; day2_rename renames the block to team, and
#   the pod identity association follows the reference.
reference_eks_aws_resources() {
  local region="$1" sa="${2:-app}"
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
  service_account = kubernetes_service_account_v1.${sa}.metadata[0].name
  role_arn        = aws_iam_role.app[0].arn
}
HCL
}

# reference_eks_cluster_resources [variant...]
#   The cluster leg. With no variant it is the shape cold_deploy applies.
#   The day-2 stages (live/e2e/reference-eks/stages.sh) move it with these,
#   so every configuration either script ever applies is written here:
#
#     reviewed     app-config's data gains reviewed = "yes" (plan_approval)
#     team         the ServiceAccount's block label is team, not app, and
#                  every reference follows it (day2_rename's new name)
#     moved        a moved block from kubernetes_service_account_v1.app to
#                  .team (day2_rename itself; dropped afterwards, as the
#                  kind estates drop theirs once both states hold the move)
#     sa_renamed   the ServiceAccount's own metadata.name is app-renamed, a
#                  genuine identity change (day2_rename's BREAK control)
#     drop_cm      kubernetes_config_map_v1.app's block is gone (day2_remove)
#     shards=N     kubernetes_config_map_v1.shard's count (default 2;
#                  day2_count scales it 2 -> 1 -> 2)
reference_eks_cluster_resources() {
  local v reviewed="" sa="app" moved="" sa_name="app" drop_cm="" shards=2
  for v in "$@"; do
    case "$v" in
      reviewed) reviewed=1 ;;
      team) sa="team" ;;
      moved) moved=1 ;;
      sa_renamed) sa_name="app-renamed" ;;
      drop_cm) drop_cm=1 ;;
      shards=*) shards="${v#shards=}" ;;
      *) printf 'reference_eks_cluster_resources: unknown variant %q\n' "$v" >&2; return 1 ;;
    esac
  done
  cat <<HCL

resource "kubernetes_namespace_v1" "app" {
  metadata {
    name = "app"
  }
}

resource "kubernetes_service_account_v1" "${sa}" {
  metadata {
    name      = "${sa_name}"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
}
HCL
  if [ -z "$drop_cm" ]; then
    cat <<'HCL'

resource "kubernetes_config_map_v1" "app" {
  metadata {
    name      = "app-config"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
  data = {
    greeting = "hello from reference-eks"
HCL
    [ -n "$reviewed" ] && printf '    reviewed = "yes"\n'
    printf '  }\n}\n'
  fi
  cat <<HCL

resource "kubernetes_config_map_v1" "shard" {
  count = ${shards}
  metadata {
    name      = "shard-\${count.index}"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }
  data = {
    shard = tostring(count.index)
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
        service_account_name = kubernetes_service_account_v1.${sa}.metadata[0].name
        container {
          name  = "pause"
          image = "registry.k8s.io/pause:3.10"
        }
      }
    }
  }
}
HCL
  if [ -n "$moved" ]; then
    cat <<'HCL'

moved {
  from = kubernetes_service_account_v1.app
  to   = kubernetes_service_account_v1.team
}
HCL
  fi
}

# reference_eks_main_tf <estate-or-empty> <aws-provider-block> <prefix> <eks_access_api> <region> [variant...]
#   The whole root. The aws provider block differs by target (floci's
#   credential and endpoint flags, or real AWS's default_tags) and is the
#   caller's; everything else is this file's. The variants are
#   reference_eks_cluster_resources's. REFERENCE_EKS_STORE, when set, is the
#   record store reference_eks_terraform_block writes into the live block
#   (a path, or a whole block from reference_eks_kubernetes_store); unset,
#   the store is local at .tofu-records.
reference_eks_main_tf() {
  local estate="$1" aws_provider="$2" prefix="$3" access="$4" region="$5" sa="app" v
  shift 5
  for v in "$@"; do [ "$v" = "team" ] && sa="team"; done
  reference_eks_terraform_block "$estate" "${REFERENCE_EKS_STORE:-.tofu-records}" || return 1
  reference_eks_variables "$prefix" "$access"
  printf '\n%s\n' "$aws_provider"
  reference_eks_kubernetes_provider "$region"
  reference_eks_aws_resources "$region" "$sa"
  reference_eks_cluster_resources "$@" || return 1
}

# reference_eks_cluster_leg: the cluster-leg addresses cold_deploy applies,
# for a cluster-leg-first teardown's -target list. A -target on a counted
# block's bare address takes every instance of it.
reference_eks_cluster_leg() {
  printf '%s\n' \
    kubernetes_deployment_v1.app \
    kubernetes_config_map_v1.shard \
    kubernetes_config_map_v1.app \
    kubernetes_service_account_v1.app \
    kubernetes_namespace_v1.app
}

# reference_eks_crash_pair <namespace> [first|second|both]: day2_crash's
# two-object apply (the kind lane's shape, #1110 part 4, #1235). The second
# reads the first's name, so they are an edge in the graph and the
# -parallelism=1 walker cannot reach the second before the first's create
# commits. The first is a Secret because a Secret records residue
# (wait_for_service_account_token) and a ConfigMap records nothing.
reference_eks_crash_pair() {
  local ns="$1" which="${2:-both}"
  if [ "$which" != "second" ]; then
    cat <<HCL

resource "kubernetes_secret_v1" "crash_first" {
  metadata {
    name      = "crash-first"
    namespace = "${ns}"
  }
  data = {
    step = "one"
  }
}
HCL
  fi
  if [ "$which" != "first" ]; then
    cat <<HCL

resource "kubernetes_config_map_v1" "crash_second" {
  metadata {
    name      = "crash-second"
    namespace = "${ns}"
  }
  data = {
    after = kubernetes_secret_v1.crash_first.metadata[0].name
  }
}
HCL
  fi
}

# reference_eks_replace_block <namespace> <suffix>: day2_replace's
# content-hashed ConfigMap under create_before_destroy (#1541): a
# Kubernetes name is unique in its namespace, so the replacement
# create_before_destroy exists for is a rename, cfg-a -> cfg-b.
reference_eks_replace_block() {
  cat <<HCL

resource "kubernetes_config_map_v1" "hashed" {
  metadata {
    name      = "cfg-$2"
    namespace = "$1"
  }
  data = { v = "$2" }
  lifecycle {
    create_before_destroy = true
  }
}
HCL
}

# reference_eks_oracle_root <aws-provider-block> <cluster> <region> <namespace>
#   A stock root of its own, with its own state, reaching the estate's
#   cluster through a data source rather than the resource it does not own,
#   and declaring one namespace of its own for the objects a stage's oracle
#   applies for real (day2_replace's and day2_crash's). Nothing in it
#   carries a marker, so the estate's sweep never sees it.
reference_eks_oracle_root() {
  local aws_provider="$1" cluster="$2" region="$3" ns="$4"
  reference_eks_terraform_block "" || return 1
  printf '\n%s\n' "$aws_provider"
  cat <<HCL

data "aws_eks_cluster" "this" {
  name = "${cluster}"
}

provider "kubernetes" {
  host                   = data.aws_eks_cluster.this.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.this.certificate_authority[0].data)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", "${cluster}", "--region", "${region}"]
  }
}

resource "kubernetes_namespace_v1" "oracle" {
  metadata {
    name = "${ns}"
  }
}
HCL
}

# reference_eks_strict_root <estate> <secrets setting>: the strict stage's
# scratch estate (the reference lanes' shape, #363): every strict toggle on
# against one random_password, with a markers "record" selection naming a
# type this estate declares. It reaches no cloud and no cluster.
reference_eks_strict_root() {
  cat <<HCL
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
  live {
    estate = "$1"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets          = "$2"
      no_source_create = "refuse"
      marker_repair    = "never"
      markers "record" {
        types = ["kubernetes_config_map_v1"]
      }
    }
  }
}

resource "random_password" "db" {
  length = 16
}
HCL
}
