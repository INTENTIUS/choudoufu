# Static eligibility still requires the backend block's own arguments to be
# static: this source's config object reads a managed resource's attribute,
# so eligibility rule 1 refuses it under the phase's own registered wording
# - exactly the class-agnostic "not statically evaluable" sentence a
# same-stack data source's non-static argument gets, not a special backend
# rule. Backend credentials themselves are never checked here; this is
# refused before any backend would even be identified.
resource "aws_instance" "this" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"
}

data "terraform_remote_state" "network" {
  backend = "local"
  config = {
    path = aws_instance.this.id
  }
}

resource "aws_cloudwatch_log_group" "per_network" {
  name = "/networks/${data.terraform_remote_state.network.outputs.vpc_id}"
}
