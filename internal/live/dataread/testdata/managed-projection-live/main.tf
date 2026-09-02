# The live-fallback shape (Options.LiveManagedResults): a data source whose
# argument reads a managed resource attribute the block does NOT itself set
# (private_dns is provider-assigned, same as managed-projection's own
# counterpart fixture). Answerable only when a caller has already read
# aws_instance.web's live object and supplied it.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-0abc"
}

data "test_zone" "of_instance" {
  name = aws_instance.web.private_dns
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.test_zone.of_instance.zone_id}"
}
