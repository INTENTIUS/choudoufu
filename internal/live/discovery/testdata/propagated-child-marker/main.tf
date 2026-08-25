# reference-ec2-vpc's greenfield shape, cut to the two objects that matter:
# one declared, tagged, server-assigned resource, and NO declaration of the
# type AWS creates a dependent object of. RunInstances makes a primary
# network interface for the instance, and every tag-propagation mechanism AWS
# offers (an autoscaling group's propagate_at_launch, an ECS service's
# propagate_tags = SERVICE, and the emulator's own DescribeNetworkInterfaces
# answer) copies the parent's tags - the ownership marker among them - onto
# an object of a type this configuration never mentions.
#
# See TestSweepCrossTypeMarkerOnUndeclaredTypeIsAWarning.
resource "aws_instance" "main" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"

  tags = {
    Name = "ec2-reference-instance"
  }
}
