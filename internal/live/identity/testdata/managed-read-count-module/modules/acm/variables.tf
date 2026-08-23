variable "domain_name" {
  type = string
}

variable "zone_id" {
  type = string
}

variable "create_certificate" {
  type    = bool
  default = true
}

# The real terraform-aws-modules/acm shape: an OPTIONAL fallback with its own
# default, sitting in try()'s second argument beside the certificate
# reference in main.tf's `local.validation_domains`. This is the site
# [resolver.namesAnUnprovenVariable] exists for - a blunt "any var anywhere"
# rule bails on the whole chased expression because this name is in it,
# despite it being provably not what made the value unknown here.
variable "acm_certificate_domain_validation_options" {
  type    = any
  default = {}
}
