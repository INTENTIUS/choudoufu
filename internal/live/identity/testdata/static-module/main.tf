resource "aws_s3_bucket" "root" {
  bucket = "tofu-stateless-static-module-root"
}

module "net" {
  source = "./net"
}
