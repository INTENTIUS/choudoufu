variable "zone_name" {
  type = string
}

data "test_zone" "sub" {
  name = "${var.zone_name}sub."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.test_zone.sub.zone_id}"
}
