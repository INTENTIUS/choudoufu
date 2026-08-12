# Limits fixture: RuleLogicalResource, local_file.
#
# The rendered file's content lives only in the state record; there is no
# live system to read it back from. See stateless/LIMITATIONS.md.

resource "local_file" "rendered" {
  filename = "out.txt"
  content  = "hello"
}
