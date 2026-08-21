# The child module both calls in ../main.tf share. It declares the same
# for_each'd block the root does, under the same type and name, so the only
# difference between the three cases the test drives is the module path.

resource "aws_subnet" "this" {
  for_each = toset(["b"])

  vpc_id     = "vpc-00000000000000000"
  cidr_block = "10.0.0.0/24"
}
