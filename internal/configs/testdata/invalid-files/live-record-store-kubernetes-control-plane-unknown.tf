terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      control_plane "doks" {
        name = "prod"
      }
    }
  }
}
