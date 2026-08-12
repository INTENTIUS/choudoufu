# A resource type with no entry in the v0 identity table. Resolution must
# refuse it by name rather than assume anything about its identity.
resource "aws_instance" "app" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"
}
