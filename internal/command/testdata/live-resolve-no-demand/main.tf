# A configuration that refuses for a reason no managed read could settle: the
# bucket's name calls uuid(), which returns a different value every run.
# statelessResolve must never configure a provider for this one - a second
# pass would be handed the identical inputs and produce the identical answer,
# at the cost of starting a plugin and making a plan call per resource.
resource "aws_s3_bucket" "b" {
  bucket = "prefix-${uuid()}"
}
