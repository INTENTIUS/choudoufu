terraform {
  live {
    estate = "my-estate"

    record_store "local" {
      control_plane "eks" {
        name = "prod"
      }
    }
  }
}
