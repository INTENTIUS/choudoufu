# Coverage: client-named path (aws_cloudwatch_metric_alarm — identity is the
# alarm_name in config; the provider's documented import ID is the alarm name
# and its id attribute equals it). #19's second slice.
#
# The alarm watches a metric by name only; nothing here references another
# resource, so the block stays at defaults for everything the identity does
# not need — no actions, no dimensions — per the standing-diff limitation on
# config-only attributes (stateless/LIMITATIONS.md).

resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "tofu-stateless-e2e-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "CPUUtilization"
  namespace           = "AWS/EC2"
  period              = 60
  statistic           = "Average"
  threshold           = 80

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_cloudwatch_metric_alarm.cpu"
  }
}
