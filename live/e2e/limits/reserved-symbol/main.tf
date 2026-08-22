# Limits fixture: a configuration reading tofu.marker_module_prefix, the one
# evaluator symbol this fork adds to the language.
#
# The symbol carries the ownership marker prefix of the module instance being
# evaluated (internal/live/markers.ModulePrefixAttr, issue #378). It exists so
# internal/live/stamp can write a tofu-address for a resource declared inside
# a module call with more than one instance, where no literal in the one
# shared configuration body is right for every instance. It is written by this
# fork, in memory, and never read from a configuration: a marker built from it
# by hand is one this pass does not verify, its value is undefined during
# static evaluation, and a configuration depending on it would not run on
# stock OpenTofu at all.
#
# RuleReservedSymbol (internal/live/lint/reserved_symbol.go) is that refusal.
# See live/LIMITATIONS.md, "reserved-symbol".

resource "aws_s3_bucket" "reserved" {
  bucket = "tofu-stateless-limits-reserved-symbol"

  tags = {
    tofu-address = "${tofu.marker_module_prefix}.aws_s3_bucket.reserved"
  }
}
