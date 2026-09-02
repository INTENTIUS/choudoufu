# The shape that matters: the resource whose computed attribute a caller wants
# is inside a shared module, not beside the block that reads it. Four estates
# call simpleinfra's shared acm-certificate module exactly this way.
module "acm" {
  source = "./child"
}
