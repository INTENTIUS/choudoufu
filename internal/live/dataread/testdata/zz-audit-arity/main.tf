# Issue #193's fix class (d): length()/keys() over an UNEXPANDED managed
# resource's own projection must refuse - not answer len(common)+1, and not
# hand back the "//unprojected" sentinel's own name as if it were one of
# the block's real arguments.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-0abc"
}

data "test_subnet" "arity" {
  id = "n-${length(aws_instance.web)}"
}

data "test_subnet" "keyed" {
  id = join("-", keys(aws_instance.web))
}

resource "aws_cloudwatch_log_group" "arity" {
  name = "/subnets/${data.test_subnet.arity.cidr_block}"
}

resource "aws_cloudwatch_log_group" "keyed" {
  name = "/subnets/${data.test_subnet.keyed.cidr_block}"
}
