---
title: "How to write markers inside a for_each'd module"
weight: 7
---

# How to write markers inside a for_each'd module

Instances of a `for_each`'d module share one HCL body for `tags`, so no single
literal address is correct for all of them and auto-stamping cannot reach
inside. choudoufu leaves such a resource alone when it already declares
`tags`, and raises a must-stamp error when it declares none and its type needs
discovery.

Thread the module's own `each.key` through and build the address from it.

```hcl
# root module: the call passes its own each.key through
module "wrapped" {
  source   = "./wrapped"
  for_each = toset(["a", "b"])
  key      = each.key
}
```

```hcl
# wrapped module: receives it as a variable
variable "key" {
  type = string
}
```

```hcl
# wrapped module: builds its own address from the variable
resource "aws_eip" "app" {
  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "module.wrapped[\"${var.key}\"].aws_eip.app"
  }
}
```

`live/e2e/estate-module-keyed/` is the two-instance fixture this is drawn
from, proven against a live emulator.

See [Compatibility reference]({{< relref "/docs/use/compatibility" >}}) for
the module forms this pattern applies to.
