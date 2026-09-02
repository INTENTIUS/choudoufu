variable "refs" {
  type = map(string)
}

variable "presets" {
  type = map(object({ port = number }))
  default = {
    http  = { port = 80 }
    https = { port = 443 }
  }
}

variable "extra_rules" {
  type    = any
  default = {}
}

# The setproduct/merge shape verbatim from that module's generated preset
# submodules: the KEYS come from two key sets this configuration states, and
# only the value under `ref` reads the caller's unknowable leaf.
locals {
  combined = {
    for pair in setproduct(keys(var.presets), keys(var.refs)) :
    "${pair[0]}/${pair[1]}" => merge(
      var.presets[pair[0]],
      { ref = var.refs[pair[1]] },
    )
  }
}

module "inner" {
  source = "../inner"

  rules = merge(
    local.combined,
    var.extra_rules,
  )
}
