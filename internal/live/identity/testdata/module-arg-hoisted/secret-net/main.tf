output "configuration" {
  sensitive = true

  value = {
    enabled = true
    label   = "secret"
    subnet  = "subnet-secret"
  }
}
