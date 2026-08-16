# Issue #212's second, unrelated-root-cause fix: collectBodyExpressions had
# no model for a "dynamic" block's iterator. Three shapes in one fixture:
#   - an ordinary dynamic block whose content reads its own iterator
#     (statement.value)
#   - a dynamic block NESTED inside another, whose content reads the OUTER
#     block's iterator, not its own (condition's content reads
#     statement.value) - the terraform-aws-modules/iam shape
#   - a dynamic block with `iterator = stmt`, renaming the bound identifier
#     away from the block's own label
locals {
  actions = ["a", "b"]
}

data "test_zone" "policy" {
  name = "example.com."

  dynamic "statement" {
    for_each = local.actions
    content {
      sid = "generic-${statement.value}"

      dynamic "condition" {
        for_each = local.actions
        content {
          test     = condition.value
          variable = statement.value
        }
      }
    }
  }

  dynamic "labeled" {
    for_each = local.actions
    iterator = stmt
    content {
      sid = stmt.value
    }
  }
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.test_zone.policy.zone_id}"
}
