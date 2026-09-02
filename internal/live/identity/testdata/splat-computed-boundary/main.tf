# Danger case: the arity rule resolves, and then hands the one addressed
# instance to [resolver.parentPart] - which means every boundary parentPart
# already draws applies unchanged. computed_val is Computed in the
# provider's schema, so [resolver.siblingLiteralExpr] declines to read it as
# a plain literal and the reference refuses, exactly as a bare
# test_sibling.s.computed_val would (#220). Reaching it through a splat and
# a join is not a way around the schema.

resource "test_sibling" "s" {
  count = 1

  key          = "sib-1"
  literal_val  = "hello"
  computed_val = "not-yet-known"
}

resource "aws_route53_record" "reads_literal" {
  zone_id = "Z1"
  name    = join("", test_sibling.s.*.literal_val)
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}

resource "aws_route53_record" "reads_computed" {
  zone_id = "Z1"
  name    = join("", test_sibling.s.*.computed_val)
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}
