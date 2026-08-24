# Isolated proof of #397's two independently-landed fixes:
#
#   1. staticCollElems's new `values(X)` case (localvalue.go) - X's own
#      values, decomposed one hop further when in merge()'s splat position.
#   2. forEachExpansion capturing eachValueDeferred on the evalStatic-
#      SUCCEEDED path too (resolve.go), not only the tolerant-retry one.
#
# Deliberately narrower than terraform-aws-modules/terraform-aws-alb's real
# local.additional_certs (main.tf:456-473, reproduced verbatim in
# testdata/managed-read-module-blind-crosstalk): the per-listener value here
# is a STATIC nested object, not a nested for-expression reading the OUTER
# comprehension's own loop variable. That is deliberate - it isolates the
# two fixes above from the THIRD, separate gap #397 also found and left
# open (no scope-threading for a loop variable an INNER for-expression
# reads from an OUTER one, plus forCondIncludesTolerant's inability to
# decide a length(lookup(v,key,default))>0 filter without v's value) - see
# managedprovenance_module_test.go's own doc comment on that remaining gap,
# which is what keeps the REAL corpus-alb-complete estate blocked.
#
# The shape that matters is still the real one: a map of maps flattened by
# merge(values(...)...) (per_listener -> flat), whose for_each source
# evaluates successfully AS A WHOLE even though one attribute inside it is
# UNKNOWN (module.wildcard_cert's own output chases to a real, unapplied
# resource; cty tolerates an embedded unknown, only a genuine error fails
# evalStatic) - and TWO listeners' own unknowns sit side by side in the
# SAME flattened map, one behind a module boundary and one a direct
# reference, exactly like the crosstalk fixture's own "https"/"cognito"
# pair. Before #397's fixes, aws_lb_listener_certificate.this["https/0"]
# had nothing to select each.value.certificate_arn out of but
# expansion.managedFrom's ONE combined answer for the WHOLE expansion -
# ambiguous here for the identical reason the crosstalk fixture is
# ambiguous (managedFoundAt's chase of local.flat's OWN definition walks
# every Variables() reference in it, reaching aws_cognito_user_pool.this
# from the SAME expression that reaches the ACM leg, regardless of which
# for_each KEY started the chase). The fixes let ["https/0"] resolve
# through its OWN element's own expression instead, reaching ONLY
# module.wildcard_cert's leg.

module "wildcard_cert" {
  source = "./modules/wildcard_cert"
}

resource "aws_cognito_user_pool" "this" {
  name = "pool"
}

locals {
  per_listener = {
    https = {
      "https/0" = {
        certificate_arn = module.wildcard_cert.acm_certificate_arn
      }
    }
    cognito = {
      "cognito/0" = {
        certificate_arn = aws_cognito_user_pool.this.arn
      }
    }
  }

  # The identical values()+merge() flatten idiom
  # terraform-aws-modules/terraform-aws-alb's own local.additional_certs
  # uses (main.tf:456-473) - OpenTofu's own function reference gives this
  # exact pattern (flatten a map of maps into one, via
  # merge(values(x)...)) as values()'s worked example.
  flat = merge(values(local.per_listener)...)
}

resource "aws_lb_listener_certificate" "this" {
  for_each = local.flat

  listener_arn    = "arn:aws:elasticloadbalancing:us-east-1:1:listener/app/x/1/2"
  certificate_arn = each.value.certificate_arn
}
