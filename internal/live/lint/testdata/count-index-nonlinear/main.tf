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

# Multiplication by a value NEITHER check can pin down. var.multiplier has
# no default and this fixture supplies no value, so it renders unknown at
# every index and the value check learns nothing; the syntactic check
# cannot prove a variable nonzero either. Both answer "cannot prove", and
# "cannot prove" is refuse. Give var.multiplier a nonzero default and this
# resource is admitted, correctly - see the analogous
# "multiply_by_defaulted_variable" in testdata/count-index-pure-scalar.
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
  type = number
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

# A function-level collision no shape rule could see. substr truncates the
# index's decimal rendering to its first character, so indices 0..9 render
# "0".."9" and index 10 renders "1" again - a collision produced by what
# substr DOES, not by the shape of the call. No case in count_index.go
# names substr, or format, or any other function; rendering the twelve
# values this count actually produces and finding two the same is the whole
# of the argument.
resource "aws_route53_record" "truncating_function" {
  count = 12

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "record-${substr(tostring(count.index), 0, 1)}.example.com"
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

# An impure function reaching count.index. uuid() renders a different value
# every time it is called, so rendering this at three indices would produce
# three different strings and look perfectly injective - while naming a
# different cloud object on every single run, which is worse than a
# collision. It is refused because the evaluation goes through
# configs.StaticEvaluator.Pure (the same gate internal/live/identity uses),
# under which an impure function yields unknown, and an unknown value is
# one count_index_domain.go can prove nothing about. This is the fixture
# for that claim: without Pure() it would be admitted.
resource "aws_route53_record" "impure_function" {
  count = 3

  zone_id = "Z0123456789ABCDEFGHI"
  name    = format("record-%s-%d.example.com", uuid(), count.index)
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}
