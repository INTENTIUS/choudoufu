terraform {
  live {
    estate = "app"

    record_store "local" {
      reads_outputs_of "network" {}
    }
  }
}
