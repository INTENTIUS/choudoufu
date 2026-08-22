terraform {
  live {
    estate = "my-estate"
    strict {
      marker_repair = "repair"
    }
    strict {
      marker_repair = "never"
    }
  }
}
