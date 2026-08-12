# Limits fixture: an address that escapes past 256 chars.
#
# aws_s3_bucket is admitted. The resource label below is 250 characters long
# on purpose: "aws_s3_bucket." plus the label is 264 characters, past the
# AWS tag-value cap that stateless/MARKERS.md documents as a lint-time error
# ("An address that does not fit is a lint-time error under stateless mode,
# not a truncation"). RuleOverlongAddress
# (internal/stateless/lint/overlong_address.go) is that error: it escapes
# the instance address exactly as the stamped marker would be escaped and
# refuses anything past the cap. See stateless/LIMITATIONS.md.

resource "aws_s3_bucket" "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" {
  bucket = "tofu-stateless-limits-overlong-address"
}
