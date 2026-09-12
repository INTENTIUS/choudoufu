# The consumer: a data source of the producer's own resource type, filtered on
# the marker pair every managed resource in a live root already carries, and
# one subnet declared inside the VPC it resolves to.
#
# This is live/OUTPUTS.md's decision, in four lines of filter: read the
# producer's live resource with a data source of its own type, with no new
# construct, no namespace and no lint rule. There is no `terraform_remote_state`
# here and no `output` block over in terraform/network - and therefore no
# stored copy of the VPC id anywhere. The value is resolved from the live
# system on every plan, through the provider's own read contract, so there is
# no snapshot to go stale and no moment where the two estates disagree about
# what the id is.
#
# What the alternative costs, if you are tempted back to remote state: once
# the producer adopts live markers it stops writing a state file, and anything
# still reading that file keeps reading a snapshot frozen at migration time -
# real-looking values, silently stale, with no "abandoned as of" marker on the
# file to give it away (live/LIMITATIONS.md).

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.59.0"
    }
  }
}

variable "aws_region" {
  description = "Region both estates live in. The pipelines pass this as TF_VAR_aws_region."
  type        = string
  default     = "us-east-1"
}

variable "network_estate" {
  description = <<-EOT
    The producer estate's name - the `tofu-estate` tag value choudoufu writes
    on every resource in terraform/network. This is the whole coupling between
    the two roots: one string, matching terraform/network/estate.chdf.hcl.
    tests/estates.test.ts asserts the two agree, because nothing in the
    language does.
  EOT
  type        = string
  default     = "cross-estate-network"
}

variable "network_vpc_address" {
  description = <<-EOT
    The producer's configuration address for the VPC - the `tofu-address` tag
    value. Together with `network_estate` it names exactly one live object:
    an address is unique within an estate by construction, which is why the
    pair needs no naming convention invented on top of it.
  EOT
  type        = string
  default     = "aws_vpc.main"
}

variable "subnet_cidr" {
  description = "The app subnet's CIDR. Must sit inside the producer VPC's cidr_block."
  type        = string
  default     = "10.90.1.0/24"
}

provider "aws" {
  region = var.aws_region
}

# The read. Two filters, both on tags choudoufu already wrote.
#
# `tag:tofu-estate` alone would match every resource in the producer estate;
# `tag:tofu-address` alone would match an `aws_vpc.main` in any estate. The
# pair is unique within the account by construction, and the provider refuses
# the read outright if it matches none or more than one - so a producer that
# has not been applied yet fails this root loudly at plan time rather than
# resolving to something plausible. That refusal is the ordering constraint
# making itself felt; src/estates-apply.op.ts is where the ordering is
# actually expressed.
data "aws_vpc" "network" {
  filter {
    name   = "tag:tofu-estate"
    values = [var.network_estate]
  }

  filter {
    name   = "tag:tofu-address"
    values = [var.network_vpc_address]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.network.id
  cidr_block = var.subnet_cidr

  tags = {
    Example = "cross-estate-dependency"
  }
}
