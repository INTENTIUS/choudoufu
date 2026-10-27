variable "logs_bucket" {
  type = string
}

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  tags = {
    logs = var.logs_bucket
  }
}

resource "aws_eip" "pool" {
  count    = 2
  instance = aws_instance.web.id
}

output "web_id" {
  value = aws_instance.web.id
}
