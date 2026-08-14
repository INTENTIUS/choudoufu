# Limits fixture: RuleIgnoreChanges (GitHub issue #103).
#
# Three of the four resources here are refused, one is not, and the one that
# is not is the point: ignoring a tag key this tool does not write is an
# ordinary thing to want, and refusing it would be a refusal with no reason
# behind it.

# The common idiom, and the dangerous one. tofu-estate and tofu-address are
# written into this argument.
resource "aws_s3_bucket" "whole_tags" {
  bucket = "estate-whole-tags"

  tags = {
    Owner = "platform"
  }

  lifecycle {
    ignore_changes = [tags]
  }
}

# A marker key named directly.
resource "aws_s3_bucket" "marker_key" {
  bucket = "estate-marker-key"

  lifecycle {
    ignore_changes = [tags["tofu-address"]]
  }
}

# Everything, which includes tags.
resource "aws_s3_bucket" "everything" {
  bucket = "estate-everything"

  lifecycle {
    ignore_changes = all
  }
}

# Admitted: one non-marker key, ignored because something outside this
# configuration writes it. The markers are untouched.
resource "aws_s3_bucket" "one_foreign_key" {
  bucket = "estate-one-foreign-key"

  tags = {
    Owner = "platform"
  }

  lifecycle {
    ignore_changes = [tags["Owner"]]
  }
}
