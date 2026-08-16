resource "aws_iam_role" "this" {
  name = "worker"
}

output "name" {
  value = aws_iam_role.this.name
}

output "names" {
  value = {
    primary = aws_iam_role.this.name
  }
}
