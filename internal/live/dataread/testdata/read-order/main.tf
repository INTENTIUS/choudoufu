# The read-phase ordering fixture: test_record's argument needs
# test_zone's answer, so the phase must read test_zone first and hand its
# zone_id into test_record's config.
data "test_zone" "a" {
  name = "example.com."
}

data "test_record" "b" {
  zone = data.test_zone.a.zone_id
}

resource "aws_cloudwatch_log_group" "per_record" {
  name = "/records/${data.test_record.b.id}"
}
