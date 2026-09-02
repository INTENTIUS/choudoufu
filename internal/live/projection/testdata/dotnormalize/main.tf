# Two configurations of the same live object, spelled the way issue #281
# found them: one with Route 53's own trailing dot, one without. Both must
# bind the same live record and both must leave the projection holding the
# same canonical name - the one the fake provider's create-time plan (this
# test's stand-in for the real Route 53 normalisation) actually answers to.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_route53_record" "dotted" {
  zone_id = "Z1"
  name    = "foo.example.com."
  type    = "CNAME"
}

resource "aws_route53_record" "plain" {
  zone_id = "Z1"
  name    = "foo.example.com"
  type    = "CNAME"
}
