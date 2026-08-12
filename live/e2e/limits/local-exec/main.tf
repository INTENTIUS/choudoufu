# Limits fixture: RuleProvisioner, local-exec form.
#
# The resource type is admitted; the provisioner is the only thing that puts
# this configuration outside the stateless subset. A provisioner runs an
# effect, and whether it already ran is knowable only from a stored record —
# exactly the authority stateless mode gives up. See live/LIMITATIONS.md.

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-limits-local-exec"

  provisioner "local-exec" {
    command = "echo hello"
  }
}
