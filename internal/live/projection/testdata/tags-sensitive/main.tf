# A taggable resource one of whose TAG VALUES comes from a `sensitive = true`
# input variable. Ordinary, and common: an owner, a cost centre, a team
# contact held in a sensitive variable and written onto every resource.
#
# configuredTagsSeed decodes this `tags` argument through the static evaluator
# so that the provider's own ReadResource can tell an explicitly-declared tag
# from one that arrived through the provider's default_tags (GitHub issue #287
# item 8). The value it decodes carries the variable's mark, and the seed goes
# straight into ReadResourceRequest.PriorState - which nothing can serialize.
#
# Two resources, because the mark lands in two different places and cty
# treats them differently. stub_bucket.main marks the map's ELEMENT - an
# object constructor does not hoist its elements' marks - and stub_bucket.whole
# marks the CONTAINER. Neither was caught: IsNull is indifferent to a mark and
# cty's IsWhollyKnown unmarks before it recurses, so both cleared every guard
# configuredTagsSeed had and travelled on into the RPC.

variable "owner" {
  type      = string
  sensitive = true
  default   = "alice@example.com"
}

variable "owner_tags" {
  type      = map(string)
  sensitive = true
  default   = { Owner = "alice@example.com" }
}

resource "stub_bucket" "main" {
  name = "app-bucket"

  tags = {
    Owner = var.owner
  }
}

resource "stub_bucket" "whole" {
  name = "other-bucket"
  tags = var.owner_tags
}
