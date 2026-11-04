resource "aws_backup_region_settings" "s" {
  resource_type_opt_in_preference = {
    "DynamoDB" = true
  }
}
