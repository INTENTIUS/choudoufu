# GitHub issue #790's fixture for references[]: live/OUTPUTS.md's
# cross-estate pattern, concretely - one data source filtered on a
# producer's marker tags, read by one resource in this estate. See
# README.md for why this is its own small directory rather than an
# addition to live/e2e/estate, and why it carries no run.sh or provider
# block: "choudoufu live-check" makes no cloud call and needs neither.

data "aws_vpc" "network" {
  filter {
    name   = "tag:tofu-estate"
    values = ["estate-references-network"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.main"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.network.id
  cidr_block = "10.77.1.0/24"

  tags = {
    tofu-estate  = "estate-references"
    tofu-address = "aws_subnet.app"
  }
}
