# A resource type with no entry in the v0 identity table. Resolution must
# refuse it by name rather than assume anything about its identity.
# aws_cloudwatch_event_rule held this fixture's place until the Component
# vocabulary gained the omitted-argument fallback (Default) its identity
# needed; aws_appstream_directory_config is durable here - rejected on the
# standing credential ground (#175), the one class that never admits.
resource "aws_appstream_directory_config" "app" {
  directory_name = "corp.example.com"
}
