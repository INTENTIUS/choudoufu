# The other danger case: the SELECTED branch of a resolvable conditional
# reads a sibling's Computed attribute. [resolver.resolveConditional] must
# still refuse there, at the exact same Computed-flag boundary
# [resolver.siblingLiteralExpr] already draws for a bare reference (#220) -
# no new boundary, reused unchanged because the branch is handed straight
# back to resolveExpr.

resource "test_sibling" "s" {
  key          = "sib-1"
  literal_val  = "hello"
  computed_val = "not-yet-known"
}

resource "aws_route53_record" "reads_literal" {
  zone_id = "Z1"
  name    = true ? test_sibling.s.literal_val : "unused"
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}

resource "aws_route53_record" "reads_computed" {
  zone_id = "Z1"
  name    = true ? test_sibling.s.computed_val : "unused"
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}
