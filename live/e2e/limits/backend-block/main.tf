# Limits fixture: RuleStateBackend, backend form.
#
# A backend configures where authoritative state is stored and locked. With
# no live block the run reads no state through it, so this is a warning.
# Beside a live block the block stays and is ignored for state (#2213), and
# an s3, gcs or azurerm backend is where records go (#2293). See
# live/LIMITATIONS.md, "backend-block".

terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}
