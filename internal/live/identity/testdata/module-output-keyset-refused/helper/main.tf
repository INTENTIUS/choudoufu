variable "names" {
  type = list(string)
}

output "keys" {
  value = toset(var.names)
}

output "key_count" {
  value = length(var.names)
}

output "sensitive_keys" {
  value     = toset(var.names)
  sensitive = true
}

output "sensitive_count" {
  value     = length(var.names)
  sensitive = true
}

# Literal outputs, so that a reference to an instance that does not exist
# would resolve if the instance-key guard were missing: nothing downstream of
# the module hop can mask it.
output "literal_keys" {
  value = toset(["x", "y"])
}

output "literal_count" {
  value = 2
}
