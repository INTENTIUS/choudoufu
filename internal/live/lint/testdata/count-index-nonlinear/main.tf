# Fixture for GitHub issue #217: count.index wrapped in a nonlinear or
# otherwise unproven-injective operation, landing in an identity-bearing
# argument. Before #217's fix, checkCountIndex (count_index.go) enumerated
# unsafe shapes - an IndexExpr's Key, four named accessor functions, a
# conditional's Condition - and treated everything else, BinaryOpExpr
# included, as safe by default. That let 100 + (count.index % 3) through:
# at count = 5, indices 0 and 3 both render rule_number = 100, so
# aws_network_acl_rule.modulo[0] and [3] resolve to the identical live
# identity, a wrong marker written onto a real cloud resource.
#
# analyzeCountIndexSafety inverts the default: every shape below is unsafe
# because none of them is one of the specific injective forms the function
# proves safe (a bare index, a template, +/- a constant, * a nonzero
# constant) - see its own doc comment for the per-shape argument and
# testdata/count-index-pure-scalar for the mirror-image safe fixture.

# The exact shape #217's audit found accepted: modulo, nested inside an
# otherwise-safe addition. Safety is recursive over the whole expression,
# not decided at the outermost node - the addition's own shape does not
# rescue a non-injective operand.
resource "aws_network_acl_rule" "modulo" {
  count = 5

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + (count.index % 3)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# Integer division collapses distinct indices onto the same quotient once
# count exceeds the divisor, the same collision shape as modulo.
resource "aws_network_acl_rule" "integer_division" {
  count = 4

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + floor(count.index / 2)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# min()/max() collapse every index past the bound onto the bound itself.
resource "aws_network_acl_rule" "bounded" {
  count = 5

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + min(count.index, 2)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# Multiplication by a value this rule has no evaluation context to prove
# nonzero: count.index * var.multiplier collapses every index to 0 whenever
# var.multiplier happens to be 0, and this is a syntax-only check with no
# variable values in scope to rule that out.
resource "aws_network_acl_rule" "multiply_by_variable" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + (count.index * var.multiplier)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

variable "multiplier" {
  type    = number
  default = 1
}

# Multiplication by a literal zero: the constant IS statically known, and
# known to be zero, so this is refused on its own terms rather than for
# lack of proof.
resource "aws_network_acl_rule" "multiply_by_zero" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + (count.index * 0)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# Both operands of a subtraction reference the index: nothing here proves
# the combination stays injective (count.index - count.index is the
# degenerate case, always 0), so this falls to the conservative default
# rather than being assumed safe because each operand is individually a
# bare index.
resource "aws_network_acl_rule" "both_operands_reference_index" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + (count.index - count.index)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# format() renders count.index through a general-purpose function this rule
# has not proven injective, unlike a template's own decimal interpolation.
resource "aws_route53_record" "format_function" {
  count = 3

  zone_id = "Z0123456789ABCDEFGHI"
  name    = format("record-%d.example.com", count.index)
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}

# A comparison operator's result is one of a small fixed set of values (here,
# just two), which collides as soon as count exceeds that set's size. This
# is egress (a bool Components attribute), a comparison used as a value
# directly, not as a conditional's condition - contrast the "selecting
# conditional" shape in testdata/count-index, which is a different node
# entirely (ConditionalExpr, not a bare BinaryOpExpr comparison).
resource "aws_network_acl_rule" "comparison" {
  count = 5

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100
  egress      = count.index > 2
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}
