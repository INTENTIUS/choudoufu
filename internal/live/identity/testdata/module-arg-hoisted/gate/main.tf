variable "base_configuration" {
  type = any
}

# count is the reason this fixture exists. A count needs a whole VALUE - it
# cannot be answered by the symbolic, one-argument-at-a-time chase
# [resolver.selectStatic] uses for an identity argument - so it is the shape
# where a module-call argument that refuses in one piece refuses everything
# the child derives from it.
resource "aws_iam_role" "gated" {
  count = var.base_configuration["enabled"] ? 1 : 0

  name               = "${var.base_configuration["label"]}-${count.index}"
  assume_role_policy = "{}"
}

# The same module, the same expression, reading the one member of the map
# that is not written down in the caller's own files for some callers and IS
# for others. It resolves exactly where the value is real and refuses
# everywhere else, which is what keeps the substitution from becoming a
# marker.
resource "aws_iam_role" "derived" {
  count = var.base_configuration["enabled"] ? 1 : 0

  name               = "derived-${var.base_configuration["subnet"]}"
  assume_role_policy = "{}"
}
