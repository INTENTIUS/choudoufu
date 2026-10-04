resource "aws_s3_bucket" "root" {
  bucket = "tofu-live-static-module-root"
}

module "net" {
  source = "./net"
}
