# Limits fixture: RuleProvisioner, remote-exec form with a connection block.
#
# Both the provisioner and the connection block that feeds it fire the same
# rule; the connection has no purpose once provisioners are gone. See
# stateless/LIMITATIONS.md.

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-limits-remote-exec"

  provisioner "remote-exec" {
    inline = ["echo hello"]
  }

  connection {
    type = "ssh"
    host = "example.com"
  }
}
