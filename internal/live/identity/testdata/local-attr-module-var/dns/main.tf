variable "zone_id" {
  type = string
}

resource "aws_route53_record" "www" {
  zone_id = var.zone_id
  name    = "www"
  type    = "A"
}
