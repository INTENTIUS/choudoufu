# GitHub issue #388's plan-node seam (rfc/20260823-foundation-order-ruling.md,
# ruling 3): aws_lb_target_group_attachment.reads_computed's port reads
# test_sibling.s's Computed, non-identity attribute, exactly the shape
# corpus-alb-complete's remaining test_plan wall is (a real value the static
# evaluator cannot fold, not a missing argument). The static evaluator must
# refuse this (see TestNodeSeamComponentsFromValueResolvesWhatStaticRefuses in
# valuecomponents_test.go), and the node-seam evaluator (identity.
# ComponentsFromValue) must resolve the identical shape once the node has
# already evaluated it into a concrete value - which is the whole point of
# resolving identity at the node instead of statically.

resource "test_sibling" "s" {
  key          = "sib-1"
  literal_val  = "hello"
  computed_val = "not-yet-known"
}

resource "aws_lb_target_group_attachment" "reads_computed" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/example/0123456789abcdef"
  target_id        = "i-0123456789abcdef0"
  port             = test_sibling.s.computed_val
}
