terraform {
  live {
    estate = "app"

    record_store "kubernetes" {
      reads_outputs_of "Network!" {}
    }
  }
}
