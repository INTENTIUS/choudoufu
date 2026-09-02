# A terraform_remote_state source whose backend and config are fully
# static literals: #179 stage 3's eligibility rule 1 (the data source's own
# arguments must be statically evaluable) passes it, and the ANALYSIS side
# never opens the backend - Analyze is offline by contract. See
# testdata/remote-state-read for the paired read-time fixture that proves
# the same shape actually reads.
data "terraform_remote_state" "network" {
  backend = "local"
  config = {
    path = "../network/terraform.tfstate"
  }
}

resource "aws_cloudwatch_log_group" "per_network" {
  name = "/networks/${data.terraform_remote_state.network.outputs.vpc_id}"
}
