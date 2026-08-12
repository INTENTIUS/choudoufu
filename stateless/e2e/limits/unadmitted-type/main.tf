# Limits fixture: RuleUnadmittedType.
#
# aws_instance is a real, non-logical type whose identity is server-assigned;
# it is in the AWS provider survey but not yet in the v0 admission table. See
# stateless/LIMITATIONS.md.

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}
