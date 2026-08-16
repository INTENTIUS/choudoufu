# A {Cloud: "region"} identity component answered from the provider block
# the resource actually resolves to, rather than from a run-level value
# nothing in this fork sets (issue #250).
#
# Every {Cloud: "region"} row in the table also carries Attrs: ["region"],
# so a resource stating its own `region` argument was already answered by
# resolver.cloudComponentAttr. What was not answered is the far commoner
# spelling below: no `region` on the resource at all, the provider block
# supplying it, which is exactly the defaulting the AWS provider itself
# applies.
#
# Two provider configurations in two regions, because a per-resource answer
# and a single run-level field differ precisely here: "home" and "away"
# name the same queue and must resolve to two different URLs.

provider "aws" {
  region = "eu-west-1"
}

provider "aws" {
  alias  = "away"
  region = "us-west-2"
}

# The region alone is the whole identity, so this one resolves concrete
# with no account and no cloud read at all - the single type in the table
# whose identity this settles by itself.
resource "aws_arczonalshift_autoshift_observer_notification_status" "here" {}

resource "aws_sqs_queue" "home" {
  name = "jobs"
}

resource "aws_sqs_queue" "away" {
  provider = aws.away
  name     = "jobs"
}
