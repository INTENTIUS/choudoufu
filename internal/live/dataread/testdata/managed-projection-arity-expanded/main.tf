# The other half of #193's fix class (d): length()/keys() over a block that
# WAS expanded must go on answering exactly what they answered before this
# fix - a count-expanded block projects as a tuple (length is the instance
# count; keys() rightly errors, a tuple has none), and a for_each-expanded
# one projects as an object keyed by the block's own each.key strings
# (length is the instance count; keys() is those very strings), never
# "//unprojected" the guard exists to catch on the unexpanded shape alone.
resource "aws_instance" "fleet" {
  count         = 3
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-${count.index}"
}

resource "aws_instance" "nodes" {
  for_each      = toset(["a", "b"])
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-${each.key}"
}

data "test_subnet" "count_arity" {
  id = "n-${length(aws_instance.fleet)}"
}

data "test_subnet" "foreach_arity" {
  id = join("-", keys(aws_instance.nodes))
}

resource "aws_cloudwatch_log_group" "count_arity" {
  name = "/subnets/${data.test_subnet.count_arity.cidr_block}"
}

resource "aws_cloudwatch_log_group" "foreach_arity" {
  name = "/subnets/${data.test_subnet.foreach_arity.cidr_block}"
}
