# Reduced from corpus-alb-complete's real shape. Three pieces, copied close
# to verbatim from their real sources, kept at the SAME module boundaries the
# real estate has them at (a module ARGUMENT, not a same-module local, is
# what [resolver.namedDef]'s "var" hop reads without ever calling
# [configs.StaticScopeData.GetLocalValue] on it eagerly - see this
# directory's own history in managedprovenance_module_test.go for why a
# same-module version of this fixture does not reach the mechanism this
# proves at all):
#
#   - `local.additional_certs` and `aws_lb_listener_certificate.this` are
#     terraform-aws-modules/terraform-aws-alb v9.9.0's own main.tf (lines
#     456-479), inside its own module boundary (here: modules/alb).
#   - `local.listeners`, combining an HTTPS listener (a CHILD MODULE output)
#     with an unrelated Cognito-authenticated listener
#     (aws_cognito_user_pool.this.arn, no module boundary), is
#     corpus-alb-complete's own examples/complete-alb/main.tf, passed into
#     the alb module as its `listeners` argument exactly as the real root
#     module does.
#   - `module.wildcard_cert`'s try() fallback is
#     terraform-aws-modules/terraform-aws-acm v4.5.0's own outputs.tf:
#     `try(aws_acm_certificate_validation.this[0].certificate_arn,
#     aws_acm_certificate.this[0].arn, "")`.
#
# See TestManagedFromModuleOutputBlindCrosstalk in
# managedprovenance_module_test.go for what this proves.

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
      certificate_arn = "arn:aws:acm:us-east-1:1:certificate/root-cert"
      authenticate_cognito = {
        user_pool_arn = aws_cognito_user_pool.this.arn
      }
    }
  }
}

module "alb" {
  source = "./modules/alb"

  listeners = local.listeners
}
