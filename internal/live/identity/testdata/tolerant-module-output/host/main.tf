variable "base_configuration" {
  type = any
}

variable "settings" {
  type = any
}

locals {
  merged = merge({ enabled = false, tier = "default" }, var.settings)

  # The route53 shape: absent from the merged map, so the lookup's default
  # answers - but only if the map's KEY SET is known, which is the whole
  # thing an unknown in place of the whole argument destroys.
  domain = lookup(var.base_configuration, "domain", null)
}

resource "aws_iam_role" "gated" {
  count = local.merged["enabled"] ? 1 : 0

  name               = "${var.base_configuration["label"]}-${local.merged["tier"]}-${count.index}"
  assume_role_policy = "{}"
}

resource "aws_iam_role" "zoned" {
  count = local.merged["enabled"] ? 1 : 0

  name               = "${var.base_configuration["availability_zone"]}-${count.index}"
  assume_role_policy = "{}"
}

resource "aws_iam_role" "absent" {
  count = local.domain == null ? 0 : 1

  name               = "never-created"
  assume_role_policy = "{}"
}

resource "aws_iam_role" "derived" {
  count = local.merged["enabled"] ? 1 : 0

  name               = "derived-${var.base_configuration["public_subnet_id"]}"
  assume_role_policy = "{}"
}

resource "aws_iam_role" "profiled" {
  count = local.merged["enabled"] ? 1 : 0

  name               = "profiled-${var.base_configuration["inner_subnet_id"]}"
  assume_role_policy = "{}"
}
