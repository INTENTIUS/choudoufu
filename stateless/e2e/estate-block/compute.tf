# Coverage: fungible count (aws_eip.pool, count = 2). Two rather than the
# main estate's three — enough to prove slot markers survive a plain apply
# without re-proving count-scale-down's exactness, which is step 9's job
# against the main estate.

resource "aws_eip" "pool" {
  count = 2

  domain = "vpc"
}
