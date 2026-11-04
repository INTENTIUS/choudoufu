resource "aws_iam_role" "r" {
  name               = "${var.sfx}-lambda"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "lambda.amazonaws.com" } }] })
}
resource "aws_lambda_function" "f" {
  function_name = "${var.sfx}-fn"
  role          = aws_iam_role.r.arn
  runtime       = "python3.12"
  handler       = "index.handler"
  filename      = "${path.module}/fn.zip"
}
