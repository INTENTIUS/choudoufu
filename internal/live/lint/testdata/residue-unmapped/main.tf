# Fixture for the residue roster's unmapped cohort (issue #49).
# aws_cloudformation_type is outside the v0 table and, per
# live/mapping.json, one of issue #53's thirteen reasoned via:"none" rows
# (one TF resource spans four CFN registry types by extension kind, so no
# single mapping is honest). It replaced aws_accessanalyzer_archive_rule
# here when the family sweeps folded that type into its analyzer.

resource "aws_cloudformation_type" "example" {
  schema_handler_package = "s3://example/handler.zip"
  type                   = "RESOURCE"
  type_name              = "Example::Thing::Type"
}
