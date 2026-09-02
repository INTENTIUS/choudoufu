resource "test_instance" "foo" {
  ami = "bar"
  tags = {
    Name         = "foo"
    tofu-estate  = "team-estate"
    tofu-address = "test_instance.foo"
  }
}
