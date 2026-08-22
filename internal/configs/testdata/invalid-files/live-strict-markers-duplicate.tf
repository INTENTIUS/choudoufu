terraform {
  live {
    estate = "my-estate"
    strict {
      markers "record" {
        types = ["aws_ebs_volume"]
      }
      markers "record" {
        addresses = ["aws_instance.worker"]
      }
    }
  }
}
