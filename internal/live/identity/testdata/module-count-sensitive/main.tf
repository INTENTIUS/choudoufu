# A sensitive variable as a module CALL's count (#1792). Stock unmarks a
# sensitive count, because the instance keys 0..n-1 say nothing about the
# value; the module path has to agree, so module.user[0] and module.user[1]
# resolve.

variable "size" {
  type      = number
  default   = 2
  sensitive = true
}

module "user" {
  source = "./user"
  count  = var.size

  name = "user-${count.index}"
}
