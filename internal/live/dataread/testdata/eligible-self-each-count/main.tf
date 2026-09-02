# Two data sources whose own arguments read their own repetition value:
# for_each's each.value and count's count.index. Both are ordinary
# per-block scoping (the same scoping every resource and data block gets
# in stock OpenTofu), not a dynamic value - analysis marks both eligible
# and PerInstance, so Read binds the real value once an instance key is
# known instead of refusing (#193).
data "aws_route53_zone" "by_each" {
  for_each = toset(["a.example.com.", "b.example.com."])
  name     = each.value
}

data "aws_route53_zone" "by_count" {
  count = 2
  name  = "zone-${count.index}.example.com."
}

resource "aws_cloudwatch_log_group" "per_each" {
  for_each = data.aws_route53_zone.by_each
  name     = "/zones/${each.value.zone_id}"
}

resource "aws_cloudwatch_log_group" "per_count" {
  count = 2
  name  = "/zones/${data.aws_route53_zone.by_count[count.index].zone_id}"
}
