terraform {
  live {
    estate = "stamp-schema-gate"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "random_id" "suffix" {
  byte_length = 4
}

resource "aws_instance" "web" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"
}
