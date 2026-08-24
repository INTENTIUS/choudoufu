variable "listeners" {
  type    = any
  default = {}
}

locals {
  create = true

  # Verbatim shape from terraform-aws-modules/terraform-aws-alb's own
  # main.tf: take the list of `additional_certificate_arns` from every
  # listener and build a map of maps keyed by "listener_key/idx".
  additional_certs = merge(values({
    for listener_key, listener_values in var.listeners : listener_key =>
    {
      for idx, cert_arn in lookup(listener_values, "additional_certificate_arns", []) :
      "${listener_key}/${idx}" => {
        listener_key    = listener_key
        certificate_arn = cert_arn
      }
    } if length(lookup(listener_values, "additional_certificate_arns", [])) > 0
  })...)
}

resource "aws_lb_listener_certificate" "this" {
  for_each = { for k, v in local.additional_certs : k => v if local.create }

  listener_arn    = "arn:aws:elasticloadbalancing:us-east-1:1:listener/app/x/1/2"
  certificate_arn = each.value.certificate_arn
}
