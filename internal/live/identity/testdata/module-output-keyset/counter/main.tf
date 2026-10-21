variable "items" {
  type = list(string)
}

output "n" {
  value = length(var.items)
}
