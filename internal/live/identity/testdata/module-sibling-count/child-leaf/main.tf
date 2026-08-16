resource "aws_cloudwatch_log_group" "b" {
  name = "/b-leaf-count/should-not-exist"
}
