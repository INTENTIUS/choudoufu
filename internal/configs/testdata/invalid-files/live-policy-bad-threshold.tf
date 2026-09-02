terraform {
  live {
    estate = "my-estate"
    policy {
      undeclared_untagged = "delete"
      threshold            = -5
      scope {
        services = ["ec2"]
      }
    }
  }
}
