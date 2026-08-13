terraform {
  live {
    estate = "my-estate"
    policy {
      undeclared_untagged = "delete"
      scope {
        services = ["ec2"]
      }
      scope {
        services = ["s3"]
      }
    }
  }
}
