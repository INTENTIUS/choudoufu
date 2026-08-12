# Fixture for RuleLogicalResource, one resource per banned prefix. None of
# these should also produce an unadmitted-type issue: the logical verdict is
# the whole answer for these types.

resource "random_pet" "name" {
  length = 2
}

resource "tls_private_key" "signing" {
  algorithm = "RSA"
}

resource "time_sleep" "wait" {
  create_duration = "30s"
}

resource "null_resource" "trigger" {
}

resource "local_file" "rendered" {
  filename = "out.txt"
  content  = "hello"
}
