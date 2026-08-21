# GitHub issue #349's sub-problem 2 at the evaluation layer.
#
# static_arn reads a data source nothing else reads. Without a live value for
# it the reference answers cty.DynamicVal, the output is left unset, and the
# plan renders "+ static_arn" on every run - corpus-lambda-simple's
# lambda_function_arn_static, in miniature.
#
# boom is the blast-radius control: its expression genuinely fails to
# evaluate against a projection (indexing a block with no instances, with no
# try() to recover). That must cost boom its prior value and nothing else.

resource "stub_cert" "zero" {
  count = 0
  names = []
}

data "stub_lookup" "current" {
  name = "here"
}

output "static_arn" {
  value = "arn:${data.stub_lookup.current.id}"
}

output "boom" {
  value = stub_cert.zero[0].id
}
