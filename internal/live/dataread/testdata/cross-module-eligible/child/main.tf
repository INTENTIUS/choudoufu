variable "zone_name" {
  type = string
}

data "test_zone" "sub" {
  name = "${var.zone_name}sub."
}

data "test_record" "b" {
  zone = data.test_zone.sub.zone_id
}

resource "aws_cloudwatch_log_group" "per_record" {
  name = "/records/${data.test_record.b.id}"
}
