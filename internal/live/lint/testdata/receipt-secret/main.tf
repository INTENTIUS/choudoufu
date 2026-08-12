# Fixture: the secrets discipline's directly visible case. See
# live/RECEIPTS.md, "Secrets discipline". A receipt whose value hashes
# a sensitive-declared variable trips RuleReceiptSecret even though the
# reference sits inside a hash call — hashing does not launder a
# low-entropy secret. A receipt hashing a non-sensitive variable (the
# pointer, e.g. a version id) passes clean: RuleReceiptSecret keys on the
# variable's sensitive declaration, not on referencing variables at all.
# The value shapes here are all visibly hash calls on purpose, so nothing
# trips RuleReceiptValue and the fixture isolates the one rule under test.

variable "db_password" {
  type      = string
  sensitive = true
}

variable "db_password_version_id" {
  type = string
}

resource "aws_ssm_parameter" "hashes_the_secret" {
  name  = "/tofu-receipts/stateless-e2e/hashes-the-secret"
  type  = "String"
  value = sha256(var.db_password)
}

resource "aws_ssm_parameter" "hashes_the_pointer" {
  name  = "/tofu-receipts/stateless-e2e/hashes-the-pointer"
  type  = "String"
  value = sha256(var.db_password_version_id)
}
