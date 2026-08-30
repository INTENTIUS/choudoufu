# A configuration that declares its own tags and knows nothing about
# ownership markers - which is every configuration, because "choudoufu
# live-import" writes the markers onto the live resources and never edits
# the .tf files. GitHub issue #613's fixture.
resource "test_instance" "foo" {
  ami = "bar"

  tags = {
    Name = "foo"
  }
}
