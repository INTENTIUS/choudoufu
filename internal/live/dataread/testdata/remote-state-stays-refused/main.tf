# terraform_remote_state stays exactly as refused as it was before #179
# stage 2: this stage covers tfe_outputs only, and stage 3 is the one that
# gives this flavor its own read pipeline.
data "terraform_remote_state" "network" {
  backend = "local"
  config = {
    path = "../network/terraform.tfstate"
  }
}

resource "aws_cloudwatch_log_group" "per_network" {
  name = "/networks/${data.terraform_remote_state.network.outputs.vpc_id}"
}
