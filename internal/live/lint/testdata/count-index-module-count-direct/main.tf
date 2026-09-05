# Probe fixture for GitHub issue #658: a module call's own COUNT expands
# statically, and one of its own arguments reads count.index directly in a
# template - "n-${count.index}" - the shape [analyzeCountIndexSafety] proves
# injective (a literal-prefixed rendering of the index can never collide
# between two distinct indices). RuleChildModule must admit this call: it is
# the counterpart to count-index-module-count-collision, which indexes a
# sibling collection instead and must still be refused.

module "counted" {
  source = "./counted"
  count  = 2
  name   = "n-${count.index}"
}
