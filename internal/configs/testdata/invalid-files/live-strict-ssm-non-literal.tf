variable "key" {
  type = string
}

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = var.key
      }
    }
  }
}
