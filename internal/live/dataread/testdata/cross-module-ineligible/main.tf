# The negative twin of cross-module-eligible: the ancestor's own data
# source depends on a managed resource (rule 4), so it stays ineligible on
# its own. Widening eligibility to cross a module boundary must not also
# widen it past a genuinely unreadable ancestor - the refusal has to
# propagate across the module boundary exactly as it does within one
# module.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}

data "test_zone" "root" {
  name = aws_instance.web.private_dns
}

module "child" {
  source    = "./child"
  zone_name = data.test_zone.root.name
}
