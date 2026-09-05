# Probe fixture for GitHub issue #658: a module call's own COUNT expands
# statically, and one of its own arguments indexes a sibling collection at
# count.index - var.suffixes[count.index] - the shape
# [analyzeCountIndexSafety] cannot prove injective, because what sits at
# position count.index is controlled by the collection, not by the index.
# RuleChildModule must still refuse this call: it is the counterpart to
# count-index-module-count-direct, which reads count.index directly in a
# template and must be admitted.

variable "suffixes" {
  type    = list(string)
  default = ["a", "b"]
}

module "counted" {
  source = "./counted"
  count  = 2
  suffix = var.suffixes[count.index]
}
