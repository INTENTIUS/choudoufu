terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      control_plane "gke" {
        name     = "prod"
        project  = "acme"
        location = "europe-west1"
      }
    }
  }
}
