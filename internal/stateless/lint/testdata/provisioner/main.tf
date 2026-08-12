# Fixture for RuleProvisioner. The resource type is admitted, so the only
# thing wrong with this configuration is the provisioner block.

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-lint-data"

  provisioner "local-exec" {
    command = "echo hello"
  }
}
