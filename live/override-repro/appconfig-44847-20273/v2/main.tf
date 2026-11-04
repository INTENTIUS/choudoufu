resource "aws_appconfig_application" "a" { name = "${var.sfx}-app" }
resource "aws_appconfig_configuration_profile" "p" {
  application_id = aws_appconfig_application.a.id
  name           = "${var.sfx}-flags"
  location_uri   = "hosted"
  type           = "AWS.AppConfig.FeatureFlags"
}
resource "aws_appconfig_hosted_configuration_version" "v" {
  application_id           = aws_appconfig_application.a.id
  configuration_profile_id = aws_appconfig_configuration_profile.p.configuration_profile_id
  content_type             = "application/json"
  content = jsonencode({
    version = "1"
    flags   = { foo = { name = "foo" } }
    values  = { foo = { enabled = false } }
  })
}
