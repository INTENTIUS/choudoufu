# configuredAttrsSeed's data-source fixture: aws_launch_configuration's real
# shape is `user_data_base64 = base64encode(data.template_file.userdata...)`
# - an attribute that reads a DATA SOURCE, which the bare module-level
# StaticEvaluator cannot resolve on its own (see Options.DataResults' own
# doc comment). This fixture is what proves materialize() threads
# Options.DataResults into the seed evaluator rather than only the caller-
# supplied values var/local/path/terraform already covered.

data "stub_data" "userdata" {}

resource "stub_lc" "withdata" {
  name              = "data-workers"
  user_data_base64  = data.stub_data.userdata.rendered
}
