# A small, deliberately marker-free estate: two server-assigned-identity
# resource types (aws_vpc, aws_security_group), the kind live-plan can only
# ever find again by their ownership marker. Before live-import runs, an
# ordinary "choudoufu live-plan -estate=..." over this same configuration
# finds nothing owned and proposes creating both from scratch - that is the
# gap live-import closes.

resource "aws_vpc" "main" {
  cidr_block = "10.77.0.0/16"

  tags = {
    Name = "live-import-e2e"
  }
}

resource "aws_security_group" "main" {
  name        = "live-import-e2e-main"
  description = "live-import e2e fixture security group"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "live-import-e2e"
  }
}
