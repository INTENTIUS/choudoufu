# Limits fixture: RuleLogicalResource, local_sensitive_file.
#
# hashicorp/local's own docs mark this resource's content-carrying arguments
# sensitive (content, content_base64), unlike local_file's - a record_store
# cannot hold that content, so this stays refused with or without one. See
# live/LIMITATIONS.md.

resource "local_sensitive_file" "rendered" {
  filename = "secret.txt"
  content  = "hello"
}
