# Fixture: the conservative boundary of receipt recognition. This
# aws_ssm_parameter's name is under the /tofu-receipts/ prefix but is built
# from an interpolation, not a static literal, so receiptResources() does
# not recognize it as a receipt — and a reference to its value is therefore
# not flagged. See stateless/RECEIPTS.md, "Lint enforcement": the rule never
# guesses at receipt-ness, only confirms it when it is evident on the page.

variable "estate" {
  type    = string
  default = "e2e"
}

resource "aws_ssm_parameter" "dynamic_name" {
  name  = "/tofu-receipts/${var.estate}/effect"
  type  = "String"
  value = "abc"
}

resource "aws_s3_bucket" "reads_dynamic" {
  bucket = aws_ssm_parameter.dynamic_name.value
}
