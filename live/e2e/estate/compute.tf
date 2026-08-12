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
