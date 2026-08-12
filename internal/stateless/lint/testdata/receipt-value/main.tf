# Fixture: Guard 2's two rejected shapes plus everything the rule must
# leave alone. See stateless/RECEIPTS.md, "Guard 2. Hash-only values, and
# never SecureString". A receipt declared SecureString and a receipt whose
# value is a raw attribute read each trip RuleReceiptValue exactly once; a
# value routed through a local is flagged too, even though the local holds
# a hash, because the discipline must be evident on the page. The two
# documented flavors (hash call, constant literal) pass clean, and an
# ordinary SecureString parameter outside the /tofu-receipts/ naming
# convention proves the rule checks the convention rather than the type
# alone.

resource "aws_s3_bucket" "data" {
  bucket = "receipt-value-fixture"
}

resource "aws_ssm_parameter" "secure" {
  name  = "/tofu-receipts/stateless-e2e/secure"
  type  = "SecureString"
  value = sha256("x")
}

resource "aws_ssm_parameter" "raw_input" {
  name  = "/tofu-receipts/stateless-e2e/raw-input"
  type  = "String"
  value = aws_s3_bucket.data.bucket
}

locals {
  precomputed = sha256("x")
}

resource "aws_ssm_parameter" "hash_via_local" {
  name  = "/tofu-receipts/stateless-e2e/hash-via-local"
  type  = "String"
  value = local.precomputed
}

resource "aws_ssm_parameter" "hash_flavor" {
  name  = "/tofu-receipts/stateless-e2e/hash-flavor"
  type  = "String"
  value = sha256(jsonencode({ bucket = aws_s3_bucket.data.bucket }))
}

resource "aws_ssm_parameter" "existence_flavor" {
  name  = "/tofu-receipts/stateless-e2e/existence-flavor"
  type  = "String"
  value = "done"
}

resource "aws_ssm_parameter" "ordinary_secret" {
  name  = "/app/config/secret"
  type  = "SecureString"
  value = "not-a-receipt"
}
