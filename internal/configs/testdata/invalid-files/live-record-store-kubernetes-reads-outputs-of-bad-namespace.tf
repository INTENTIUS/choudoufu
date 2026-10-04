terraform {
  live {
    estate = "app"

    record_store "kubernetes" {
      reads_outputs_of "network" {
        namespace = "Platform_Records"
      }
    }
  }
}
