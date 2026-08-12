# Limits fixture: RuleChildModule.
#
# Stateless mode v0 covers the root module only. Identity resolution,
# discovery, marker stamping and the projection all stop at the root, and
# module expansion changes every resource address inside the module - which is
# what a tofu-address marker records. See stateless/LIMITATIONS.md.
#
# The root module itself is inside the subset: the only issue this fixture
# raises is the module call below.
#
# Unlike every other fixture in this wing, this one needs "choudoufu get"
# before lint can be reached at all: a module block is refused with "Module
# not installed" while the configuration is still being loaded, which is
# earlier than any stateless code runs. The harness does that one step for
# this directory; nothing else about it is special.

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-limits-child-module"
}

module "network" {
  source = "./network"
}
