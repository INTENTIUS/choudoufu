terraform {
  live {
    estate = "my-estate"
    record_store "local" {}
    strict {
      marker_repair = "never"
      markers "record" {
        types     = ["aws_ebs_volume"]
        addresses = ["aws_instance.worker", "module.server.aws_instance.instance"]
      }
    }
  }
}
