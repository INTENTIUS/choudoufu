# A terraform_remote_state source with a fully static backend and config:
# #179 stage 3's read pipeline reads it through the builtin terraform
# provider's own local backend, exactly the way stock OpenTofu would, before
# resolution needs its outputs.
data "terraform_remote_state" "network" {
  backend = "local"
  config = {
    path = "./testdata/remote-state-read/network.tfstate"
  }
}

resource "aws_cloudwatch_log_group" "per_network" {
  name = "/networks/${data.terraform_remote_state.network.outputs.vpc_id}"
}
