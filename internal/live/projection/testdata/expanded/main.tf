# An expanded client-named block plus a server-assigned one, which is the
# shape GitHub issue #187's narrow read has to get right: the aggregate a
# for_each block's value takes is an object over every one of its instances,
# so a read that answers for some of them and not others has no honest value
# to hand back at all.
#
# The KMS key is here to be unreadable: its identity is assigned by the
# provider, so nothing in this file names it and only marker discovery ever
# could.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_cloudwatch_log_group" "app" {
  for_each = toset(["a", "b"])
  name     = "/ours/${each.key}"
}

resource "aws_kms_key" "root" {
  description = "server-assigned, so no configuration names it"
}
