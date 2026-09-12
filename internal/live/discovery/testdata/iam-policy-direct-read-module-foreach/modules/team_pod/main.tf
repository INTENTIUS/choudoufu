# modules/team_pod - the terralith-gen shape (tools/terralith-gen/gen.go's
# own podModuleVariablesTF/podModuleMainTF), trimmed to the one resource
# issue #1063 is about.

variable "prefix" {
  type = string
}

variable "pod_size" {
  type = number
}

resource "aws_iam_policy" "pod_policy" {
  count  = var.pod_size
  name   = "${var.prefix}-team-${format("%04d", count.index)}-policy"
  policy = jsonencode({ Sid = "team" })
}
