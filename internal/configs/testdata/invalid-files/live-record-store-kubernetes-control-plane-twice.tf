terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      control_plane "eks" {
        name = "prod"
      }
      control_plane "eks" {
        name = "other"
      }
    }
  }
}
