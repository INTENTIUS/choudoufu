# A child module declaring its own content-bearing `provider` block, which
# providerscope.Resolve serves directly (Module: cur.Path, not the root) the
# same way stock OpenTofu wires a resource straight to its own module's
# provider node. The root declares a DIFFERENT region, so a lookup that
# searched the root would render a marker naming the wrong region rather
# than fail visibly.

provider "aws" {
  region = "us-east-1"
}

module "child" {
  source = "./child"
}

resource "aws_arczonalshift_autoshift_observer_notification_status" "root" {}

resource "aws_sqs_queue" "root" {
  name = "jobs"
}
