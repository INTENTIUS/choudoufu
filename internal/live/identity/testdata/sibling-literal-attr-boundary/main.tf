# The Computed/Optional boundary #220's fix has to hold, independent of the
# three confirmed corpus sites: test_sibling.s has one attribute the schema
# says the configuration alone controls (literal_val, Optional, not
# Computed), one the provider can only ever fill in itself (computed_val,
# Computed, not Optional - never settable in real configuration, so it is
# never written into this block), and one the legacy-SDK shape where a
# caller MAY set it but the provider may still override it
# (optcomp_val, Optional+Computed - aws_s3_bucket's "bucket" is this shape
# in the real AWS provider). Only the first is safe to read as a plain
# literal; the other two must refuse exactly as they did before #220,
# whatever the configuration happened to write for them.

resource "test_sibling" "s" {
  key         = "sib-1"
  literal_val = "hello"
  optcomp_val = "set-but-not-trustworthy"
}

resource "aws_route53_record" "reads_literal" {
  zone_id = "Z1"
  name    = test_sibling.s.literal_val
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}

resource "aws_route53_record" "reads_computed" {
  zone_id = "Z1"
  name    = test_sibling.s.computed_val
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}

resource "aws_route53_record" "reads_optcomp" {
  zone_id = "Z1"
  name    = test_sibling.s.optcomp_val
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}
