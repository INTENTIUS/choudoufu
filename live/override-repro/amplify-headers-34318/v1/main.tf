resource "aws_amplify_app" "a" {
  name           = "${var.sfx}-app"
  custom_headers = <<-YAML
    customHeaders:
      - pattern: '**/*'
        headers:
          - key: 'Strict-Transport-Security'
            value: 'max-age=31536000; includeSubDomains'
  YAML
}
