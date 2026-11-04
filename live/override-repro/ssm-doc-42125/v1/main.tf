resource "aws_ssm_document" "d" {
  name          = "${var.sfx}-doc"
  document_type = "Command"
  content = jsonencode({
    schemaVersion = "2.2"
    description   = "repro"
    mainSteps     = [{ action = "aws:runShellScript", name = "x", inputs = { runCommand = ["true"] } }]
  })
}
