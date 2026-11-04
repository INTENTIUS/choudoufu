resource "aws_service_discovery_http_namespace" "n" { name = "${var.sfx}-ns" }
resource "aws_service_discovery_service" "s" {
  name         = "${var.sfx}-svc"
  namespace_id = aws_service_discovery_http_namespace.n.id
  health_check_custom_config {
    failure_threshold = 5
  }
}
