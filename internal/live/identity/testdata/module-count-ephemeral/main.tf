# An ephemeral variable as a module CALL's count. Stock refuses it ("Invalid
# count argument": the value could be exposed as an instance key), and so
# does the module path, even after #1792 stopped refusing a sensitive one.

variable "size" {
  type      = number
  default   = 2
  ephemeral = true
}

module "user" {
  source = "./user"
  count  = var.size

  name = "user-${count.index}"
}
