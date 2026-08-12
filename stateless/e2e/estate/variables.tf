variable "enabled" {
  description = "Drives the conditional-idiom resource (count = var.enabled ? 1 : 0). See README.md, 'Conditional idiom'."
  type        = bool
  default     = true
}
