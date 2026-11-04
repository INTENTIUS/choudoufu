resource "aws_elasticache_user" "u" {
  user_id       = "${var.sfx}-user"
  user_name     = "${var.sfx}-user"
  access_string = "on ~* +@all"
  engine        = "redis"
  authentication_mode {
    type = "no-password-required"
  }
}
