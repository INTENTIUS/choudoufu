# Two configurations of one provider, and one resource on each. The point is
# the region: an AWS provider pointed at one region derives a different ARN
# than the same provider pointed at another, so planning `away` through the
# default configuration mints a value from the wrong place. A wrong value
# ranks below a missing one everywhere in this package.

provider "stub" {
  region = "us-east-1"
}

provider "stub" {
  alias  = "west"
  region = "eu-west-2"
}

resource "stub_cert" "home" {
  names = ["example.com"]
}

resource "stub_cert" "away" {
  provider = stub.west
  names    = ["example.com"]
}
