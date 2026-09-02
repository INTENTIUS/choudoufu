# A chain: aws_route53_record.a reads test_sibling.b's literal argument,
# which is itself set from test_sibling.c's Computed attribute. #220's fix
# must refuse at c - the genuinely apply-time link - and must not resolve a
# by treating b's argument as a static string just because b's own schema
# slot for it is a plain literal one.

resource "test_sibling" "c" {
  key = "c-1"
}

resource "test_sibling" "b" {
  key         = "b-1"
  literal_val = test_sibling.c.computed_val
}

resource "aws_route53_record" "a" {
  zone_id = "Z1"
  name    = test_sibling.b.literal_val
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}
