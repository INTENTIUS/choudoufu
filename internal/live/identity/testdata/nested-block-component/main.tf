# Component.Block (issue #310), exercised through
# aws_autoscaling_traffic_source_attachment - the type this field was built
# for. The provider (aws 6.59.0) documents one composite import ID,
# autoscaling_group_name,traffic_source_type,traffic_source_identifier, and
# its own schema confirms all three are required, client-specified values -
# but the second and third live inside a required, max_items:1
# `traffic_source` nested block, not as top-level arguments.
#
# present exercises the ordinary case: the block is written, so both leaves
# resolve and the rendered ImportID matches the provider's own documented
# example verbatim (aws_autoscaling_traffic_source_attachment.html.markdown's
# Import section):
#
#	example,elbv2,arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/example/1234567890123456
resource "aws_autoscaling_traffic_source_attachment" "present" {
  autoscaling_group_name = "example"

  traffic_source {
    identifier = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/example/1234567890123456"
    type       = "elbv2"
  }
}

# absent is the mutation-tested adversarial case: no traffic_source block at
# all. A resolver that silently treated a missing block as an empty one, or
# that fell back to some default, would over-admit here - so this instance
# must refuse with the same "Identity argument not set" diagnostic an absent
# top-level required argument gets, not resolve to a partial or fabricated
# identity.
resource "aws_autoscaling_traffic_source_attachment" "absent" {
  autoscaling_group_name = "example-no-block"
}

# impure is the second mutation-tested adversarial case: the block IS
# present, but its identifier leaf is built from an impure call (the same
# uuid() shape testdata/impure-name pins for a top-level argument). A
# resolver that reads a Block-sourced leaf through some shortcut bypassing
# the ordinary resolveExpr machinery would fabricate an identity here; the
# leaf has to refuse exactly like an impure top-level argument does.
resource "aws_autoscaling_traffic_source_attachment" "impure" {
  autoscaling_group_name = "example-impure"

  traffic_source {
    identifier = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/example/${uuid()}"
    type       = "elbv2"
  }
}
