terraform {
  live {
    estate = "app"

    record_store "kubernetes" {
      reads_outputs_of "network" {}

      reads_outputs_of "dns" {
        namespace = "platform-records"
      }
    }
  }
}
