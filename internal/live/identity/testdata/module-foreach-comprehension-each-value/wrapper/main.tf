variable "container_definitions" {
  type = map(object({
    create = optional(bool, true)
    label  = optional(string, "default-team")
    # owner has NO explicit default - a bare optional(string). Neither
    # entry in main.tf sets it, so its value has to come from the
    # DECLARED TYPE's own optional-attribute shape (a properly-typed
    # null), never from typeexpr.Defaults.DefaultValues - that map only
    # ever holds an EXPLICIT `optional(T, default)` override and never
    # gets an entry at all for a bare `optional(T)`.
    owner = optional(string)
    image = optional(string)
  }))
}

module "task" {
  source = "./task"

  for_each = { for k, v in var.container_definitions : k => v if v.create }

  name  = each.key
  label = each.value.label
  owner = each.value.owner
}
