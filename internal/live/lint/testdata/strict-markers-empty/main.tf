# A markers "record" block naming neither a type nor an address narrows
# nothing, so reading it as a selection means guessing whether the author
# meant everything or nothing. Same rule, same reasoning, as an empty policy
# scope block.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
      }
    }
  }
}
