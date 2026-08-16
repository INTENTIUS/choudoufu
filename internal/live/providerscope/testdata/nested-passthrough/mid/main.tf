module "leaf" {
  source = "./leaf"

  providers = {
    aws = aws
  }
}
