# #397's REAL shape, at the real module boundary, with TWO cert-carrying
# listeners so per-element attribution can be asserted in BOTH directions.
#
# testdata/managed-read-module-blind-crosstalk already reproduces
# terraform-aws-modules/terraform-aws-alb v9.9.0's own local.additional_certs
# verbatim, but its root module gives only ONE listener an
# `additional_certificate_arns` list, so the flattened map has exactly one
# key and "did each instance get its OWN answer" is unprovable there.
# testdata/values-splat-per-element has two keys but a deliberately
# STATIC per-listener value, which sidesteps the two things this fixture
# exists for:
#
#   1. the per-listener value clause is a for-expression NESTED inside the
#      outer one, reading the OUTER comprehension's own loop variable
#      (`listener_values`) - so the decomposition helpers must thread the
#      outer binding down through the recursive chase;
#   2. the outer filter is
#      `length(lookup(listener_values, "additional_certificate_arns", [])) > 0`
#      - a value-free predicate wrapped in length()/comparison, which
#      [resolver.forCondIncludesTolerant] recognised in neither shape.
#
# Three listeners, matching corpus-alb-complete's own examples/complete-alb:
# one whose additional cert is a CHILD MODULE's output, one whose additional
# cert is a DIRECT reference to an unrelated resource, and one with no
# additional certs at all (which the filter must drop, so no
# aws_lb_listener_certificate instance exists for it).

module "wildcard_cert" {
  source = "./modules/wildcard_cert"
}

resource "aws_cognito_user_pool" "this" {
  name = "pool"
}

locals {
  listeners = {
    https = {
      certificate_arn             = "arn:aws:acm:us-east-1:1:certificate/root-cert"
      additional_certificate_arns = [module.wildcard_cert.acm_certificate_arn]
    }
    cognito = {
      certificate_arn             = "arn:aws:acm:us-east-1:1:certificate/root-cert"
      additional_certificate_arns = [aws_cognito_user_pool.this.arn]
    }
    plain = {
      certificate_arn = "arn:aws:acm:us-east-1:1:certificate/root-cert"
    }
  }
}

module "alb" {
  source = "./modules/alb"

  listeners = local.listeners
}
