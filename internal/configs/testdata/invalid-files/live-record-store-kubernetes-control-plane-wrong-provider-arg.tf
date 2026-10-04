terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      control_plane "eks" {
        name           = "prod"
        resource_group = "rg"
      }
    }
  }
}
