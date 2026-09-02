variable "container_definitions" {
  type = map(object({
    create = optional(bool, true)
    image  = optional(string)
  }))
}

module "task" {
  source = "./task"

  for_each = { for k, v in var.container_definitions : k => v if v.create }

  name = each.key
}
