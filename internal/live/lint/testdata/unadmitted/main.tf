# Fixture for RuleUnadmittedType. aws_instance is a real, non-logical type
# whose identity is server-assigned; it is simply not in the v0 table yet.

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}
