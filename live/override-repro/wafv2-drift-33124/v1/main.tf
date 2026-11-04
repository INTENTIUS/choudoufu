resource "aws_wafv2_web_acl" "w" {
  name  = "${var.sfx}-acl"
  scope = "REGIONAL"
  default_action {
    allow {}
  }
  rule {
    name     = "rate"
    priority = 0
    action {
      block {}
    }
    statement {
      rate_based_statement {
        aggregate_key_type = "FORWARDED_IP"
        limit              = 1000
        forwarded_ip_config {
          fallback_behavior = "MATCH"
          header_name       = "X-Forwarded-For"
        }
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = false
      metric_name                = "rate"
      sampled_requests_enabled   = false
    }
  }
  visibility_config {
    cloudwatch_metrics_enabled = false
    metric_name                = "acl"
    sampled_requests_enabled   = false
  }
}
