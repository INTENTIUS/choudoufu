# Fixture for TestSSMShapedInsecureValueUpdatePlansTheConfiguredValue
# (GitHub issue #2261, mirrored from INTENTIUS/chant#4470). The parameter
# was applied with insecure_value = "v1"; this configuration changes it to
# "v2", an in-place update.

terraform {
  live {
    estate = "ssm-2261"
  }
}

resource "stub_parameter" "p" {
  name           = "/p"
  type           = "String"
  insecure_value = "v2"
}
