# GitHub issue #2232: declaration-carried records the cache may serve when
# their zone's one listing shows the cached values. zone_id is literal so
# every identity resolves from configuration alone.

resource "aws_route53_record" "www" {
  zone_id = "Z1"
  name    = "www.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}

resource "aws_route53_record" "weighted" {
  zone_id        = "Z1"
  name           = "api.example.com"
  type           = "A"
  ttl            = 60
  records        = ["10.0.0.2"]
  set_identifier = "blue"
}

resource "aws_route53_record" "other_zone" {
  zone_id = "Z2"
  name    = "mail.example.org"
  type    = "MX"
  ttl     = 300
  records = ["10 mx.example.org"]
}
