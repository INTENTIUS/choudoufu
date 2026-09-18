# Fixture for GitHub issue #1256: internal/live/lint honouring this run's
# -target / -exclude scope.
#
# Every rule under test is declared TWICE, once on a block whose label ends
# "_targeted" and once on a block whose label ends "_excluded", so a scope
# that keeps only the first can be told apart from a scope that has silenced
# the rule altogether. That distinction is the whole point: a rule that stops
# firing when a target set is present has been broken, not narrowed.
#
# It lives under internal/command/testdata rather than under
# internal/live/lint/testdata for the reason
# live-plan-target-scope-1203/main.tf gives: TestIdentityGolden sweeps every
# configuration directory under internal/live and live, and a repro fixture
# must not add pinned rows there.
#
# There is no live block on purpose. A live block implies a local record
# store (#364, configs.impliedRecordStore), which admits provisioners, and
# the provisioner rule is one of the twelve under test here.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  # Whole-configuration control, warning severity: RuleStateBackend names no
  # resource, so no scope narrows it. It has to keep firing even when the
  # scope keeps nothing at all.
  backend "local" {}
}

provider "aws" {
  region = "us-east-1"
}

variable "receipt_secret" {
  type      = string
  sensitive = true
  default   = "unused"
}

# ---- RuleProvisioner --------------------------------------------------

resource "aws_s3_bucket" "prov_targeted" {
  bucket = "prov-targeted"

  provisioner "local-exec" {
    command = "true"
  }
}

resource "aws_s3_bucket" "prov_excluded" {
  bucket = "prov-excluded"

  provisioner "local-exec" {
    command = "true"
  }
}

# ---- RuleLogicalResource ----------------------------------------------

resource "null_resource" "logical_targeted" {}

resource "null_resource" "logical_excluded" {}

# ---- RuleUnadmittedType -----------------------------------------------

resource "aws_devicefarm_test_grid_project" "unadmitted_targeted" {
  name = "unadmitted-targeted"
}

resource "aws_devicefarm_test_grid_project" "unadmitted_excluded" {
  name = "unadmitted-excluded"
}

# ---- RuleCountIndex ---------------------------------------------------

# var.rule_numbers repeats, so indices 0 and 2 render the same rule_number -
# a real collision in an identity-bearing argument, the same shape
# testdata/count-index's "list_index" block pins.

variable "rule_numbers" {
  type    = list(number)
  default = [100, 200, 100]
}

resource "aws_network_acl_rule" "countindex_targeted" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number    = var.rule_numbers[count.index]
  egress         = false
  protocol       = "tcp"
  rule_action    = "allow"
  cidr_block     = "10.0.0.0/16"
}

resource "aws_network_acl_rule" "countindex_excluded" {
  count = 3

  network_acl_id = "acl-0123456789abcdef1"
  rule_number    = var.rule_numbers[count.index]
  egress         = false
  protocol       = "tcp"
  rule_action    = "allow"
  cidr_block     = "10.0.0.0/16"
}

# ---- RuleIgnoreChanges ------------------------------------------------

resource "aws_s3_bucket" "ignore_targeted" {
  bucket = "ignore-targeted"

  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_s3_bucket" "ignore_excluded" {
  bucket = "ignore-excluded"

  lifecycle {
    ignore_changes = [tags]
  }
}

# ---- RuleForEachKey ---------------------------------------------------

resource "aws_subnet" "foreachkey_targeted" {
  for_each = toset(["bad%key"])

  cidr_block = "10.42.1.0/24"
}

resource "aws_subnet" "foreachkey_excluded" {
  for_each = toset(["bad%key"])

  cidr_block = "10.42.2.0/24"
}

# ---- RuleOverlongAddress ----------------------------------------------
#
# "aws_s3_bucket." is 14 characters, so a 1011-character label escapes to a
# 1025-character address - one past the 1024-character budget. The two
# labels end "_targeted" and "_excluded" like every other pair here, which
# is what the scope predicate reads.

resource "aws_s3_bucket" "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz_targeted" {
  bucket = "overlong-targeted"
}

resource "aws_s3_bucket" "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz_excluded" {
  bucket = "overlong-excluded"
}

# ---- RuleReceiptValue and RuleReceiptSecret ---------------------------
#
# SecureString trips Guard 2 (RuleReceiptValue) and the sensitive variable
# in the same block trips the secrets rule (RuleReceiptSecret), so these two
# blocks carry one issue of each rule apiece.

resource "aws_ssm_parameter" "receipt_targeted" {
  name  = "/tofu-receipts/scope-1256/targeted"
  type  = "SecureString"
  value = var.receipt_secret
}

resource "aws_ssm_parameter" "receipt_excluded" {
  name  = "/tofu-receipts/scope-1256/excluded"
  type  = "SecureString"
  value = var.receipt_secret
}

# ---- RuleReceiptLeaf --------------------------------------------------
#
# The rule is raised on the REFERRING block, so it is the referrer whose
# label carries the verdict.

resource "aws_s3_bucket" "leaf_targeted" {
  bucket = aws_ssm_parameter.receipt_targeted.value
}

resource "aws_s3_bucket" "leaf_excluded" {
  bucket = aws_ssm_parameter.receipt_targeted.value
}

# ---- Whole-configuration controls, error severity ---------------------
#
# RuleUndeclaredProviderAlias is the one per-resource rule #1256 ruled NOT
# scoped: the estate sweep's provider set is read off the configuration
# rather than off the target set, so a stray alias still configures a
# provider from the environment alone on a narrowed run. Both of these must
# keep refusing even when the scope keeps neither.

resource "aws_s3_bucket" "alias_targeted" {
  bucket   = "alias-targeted"
  provider = aws.nowhere
}

resource "aws_s3_bucket" "alias_excluded" {
  bucket   = "alias-excluded"
  provider = aws.nowhere
}

# RuleMovedBlock is the exception #1256 names by hand: internal/live/discovery
# builds its pending-move index from exactly the statements lint leaves out,
# so narrowing this rule would let a moved resource read as an orphan. The
# two endpoints name different resource types, which is unhonourable, and the
# "to" side deliberately names a block the scope EXCLUDES.

moved {
  from = aws_subnet.gone
  to   = aws_s3_bucket.prov_excluded
}
