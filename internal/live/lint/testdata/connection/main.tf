# Fixture for RuleProvisioner via a standalone connection block: no
# provisioner block at all, so the rule has to catch the connection on its own.

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-lint-data"

  connection {
    type = "ssh"
    host = "example.com"
  }
}
