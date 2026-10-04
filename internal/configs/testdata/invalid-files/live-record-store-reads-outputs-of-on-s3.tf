terraform {
  live {
    estate = "app"

    record_store "s3" {
      bucket = "records"

      reads_outputs_of "network" {}
    }
  }
}
