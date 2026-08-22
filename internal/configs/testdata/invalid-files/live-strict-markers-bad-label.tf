terraform {
  live {
    estate = "my-estate"
    strict {
      markers "tag" {
        types = ["aws_ebs_volume"]
      }
    }
  }
}
