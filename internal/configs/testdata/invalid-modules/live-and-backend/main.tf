terraform {
  live {
    estate = "my-estate"
  }

  backend "local" {
    path = "somewhere.tfstate"
  }
}
