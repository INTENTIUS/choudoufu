# A root output whose value reaches a SENSITIVE schema attribute. The
# evaluator marks such a value (internal/tofu/evaluate.go), and a marked value
# stored as a prior output value panics the plan graph's own diff - see the
# unmark in ApplyRootOutputValues and TestApplyRootOutputValuesUnmarksBefore.
resource "stub_cert" "cert" {
  names = ["example.com"]
}

output "cert_password" {
  value     = stub_cert.cert.password
  sensitive = true
}
