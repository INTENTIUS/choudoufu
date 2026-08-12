# Coverage: fungible count (aws_eip.pool, count = 3). Instances are
# interchangeable — no identity-bearing property distinguishes slot 0 from
# slot 1 — which is exactly the shape phase 3's set matcher binds by slot
# marker, not by count.index.

resource "aws_eip" "pool" {
  count = 3

  domain = "vpc"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_eip.pool:${count.index}"
  }
}

# Coverage: marker path (aws_launch_template — EC2 mints the lt- ID; the
# name is client-chosen but the provider's identity schema requires the ID).
# Third slice of the survey's marker cohort (#20). The template exists to be
# a template, not to launch anything: floci cannot bring an instance to
# "running" (lex00/floci#32), which is why aws_instance is not here.
resource "aws_launch_template" "app" {
  name          = "tofu-stateless-e2e-app"
  image_id      = "ami-12345678"
  instance_type = "t3.micro"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_launch_template.app"
  }
}

# Coverage: marker path (aws_ebs_volume — EC2 mints the vol- ID; a size and
# an availability zone name nothing). Same slice (#20). The volume is
# unattached on purpose: attaching it would need an instance.
resource "aws_ebs_volume" "data" {
  availability_zone = "us-east-1a"
  size              = 1

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_ebs_volume.data"
  }
}
