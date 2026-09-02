# Two DIFFERENT managed resources, both covered and both unknown, combined
# into one local a THIRD resource's identity argument reads only through
# count.index/element() (so it must go through the chase, never the direct
# match). Picking either one as "the" sibling would be a guess: the local
# names both, and nothing here says which one this specific record actually
# depends on.
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_cognito_user_pool_client" "app" {
  name         = "app"
  user_pool_id = "pool-1"
}

locals {
  combined = [
    {
      name = coalesce(aws_acm_certificate.cert.arn, aws_cognito_user_pool_client.app.id)
      type = "CNAME"
    }
  ]
}

resource "aws_route53_record" "ambiguous" {
  count = 1

  zone_id = "Z0423220"
  name    = element(local.combined, count.index)["name"]
  type    = element(local.combined, count.index)["type"]
  records = ["ignored"]
  ttl     = 60
}
