variable "zone_name" {
  type = string
}

# Same address as the root's own data.test_zone.shared, deliberately, and
# deliberately never referenced by anything in this module - it exists only
# to give a wrongly-scoped lookup something wrong to find.
data "test_zone" "shared" {
  name = "child-wrong.com."
}

data "test_record" "b" {
  zone = var.zone_name
}

resource "aws_cloudwatch_log_group" "per_record" {
  name = "/records/${data.test_record.b.id}"
}
