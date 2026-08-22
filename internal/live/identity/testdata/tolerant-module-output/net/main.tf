# Two calls away from the resource that spells them: "eu-west-1a" can only
# reach module.host by evaluating this output's expression.
output "configuration" {
  value = {
    availability_zone = "eu-west-1a"
  }
}
