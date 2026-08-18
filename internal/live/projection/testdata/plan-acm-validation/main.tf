terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
}

# The #187 carrier shape as five simpleinfra estates write it: a for_each
# comprehension over a COMPUTED attribute of a sibling managed resource.
# Nothing in this configuration says what domain_validation_options holds,
# and no schema records the relationship between it and domain_name - only
# the provider knows, which is why the value has to be asked for rather
# than derived.
resource "aws_acm_certificate" "cert" {
  domain_name               = "example.com"
  subject_alternative_names = ["www.example.com"]
  validation_method         = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
