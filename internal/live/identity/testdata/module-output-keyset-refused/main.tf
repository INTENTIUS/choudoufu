# #2142's adversarial half. Every key set here is a child module's output
# that is NOT statically knowable, or names something that is not one
# output of one instance, and each must stay refused: a key set read out of
# a module must never be guessed.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

module "live" {
  source = "./live"
}

module "helper" {
  source = "./helper"
  names  = ["a", "b"]
}

module "shard" {
  source = "./helper"
  count  = 3
  names  = ["s${count.index}"]
}

# An output reading a managed resource's attribute.
resource "aws_iam_role_policy" "from_managed" {
  for_each = module.live.from_managed
  role     = each.key
  name     = "p"
}

# A count reading a managed resource's attribute through an output.
resource "aws_iam_role_policy" "count_from_managed" {
  count = module.live.count_from_managed
  role  = "r${count.index}"
  name  = "p"
}

# An output reading a data source this run has not read.
resource "aws_iam_role_policy" "from_data" {
  for_each = module.live.from_data
  role     = each.key
  name     = "p"
}

# An output calling a provider function.
resource "aws_iam_role_policy" "from_provider_function" {
  for_each = module.live.from_provider_function
  role     = each.key
  name     = "p"
}

# An output calling an impure function.
resource "aws_iam_role_policy" "from_impure" {
  for_each = module.live.from_impure
  role     = each.key
  name     = "p"
}

# A sensitive output: its keys would become addresses, written in clear.
resource "aws_iam_role_policy" "sensitive" {
  for_each = module.helper.sensitive_keys
  role     = each.key
  name     = "p"
}

# The same sensitive output's length as a count.
resource "aws_iam_role_policy" "sensitive_count" {
  count = module.helper.sensitive_count
  role  = "r${count.index}"
  name  = "p"
}

# An instance the three-instance call does not expand to. The outputs are
# literals, so only the instance-key guard can refuse these.
resource "aws_iam_role_policy" "missing_instance" {
  for_each = module.shard[7].literal_keys
  role     = each.key
  name     = "p"
}

# The same, as a count.
resource "aws_iam_role_policy" "missing_instance_count" {
  count = module.shard[7].literal_count
  role  = "r${count.index}"
  name  = "p"
}

# An unindexed reference to a repeated call: a tuple of every instance's
# output, not one key set.
resource "aws_iam_role_policy" "unindexed_repeated" {
  for_each = module.shard.literal_keys
  role     = each.key
  name     = "p"
}

# A whole-module reference: an object of every output.
resource "aws_iam_role_policy" "whole_module" {
  for_each = module.helper
  role     = each.key
  name     = "p"
}

# A module call ranging over a managed-resource output.
module "consumer_managed" {
  source   = "./consumer"
  for_each = module.live.from_managed
  role     = each.key
}

# A module call ranging over a sensitive output.
module "consumer_sensitive" {
  source   = "./consumer"
  for_each = module.helper.sensitive_keys
  role     = each.key
}

# A module call ranging over an instance that does not exist.
module "consumer_missing_instance" {
  source   = "./consumer"
  for_each = module.shard[7].literal_keys
  role     = each.key
}

# A module call ranging over a whole module.
module "consumer_whole_module" {
  source   = "./consumer"
  for_each = module.helper
  role     = each.key
}
