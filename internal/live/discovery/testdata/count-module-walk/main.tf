# A count'd module call and a for_each'd one, side by side. Both expand to
# more than one instance, and the resource inside them is count'd, so
# indexCountBlocks has a count block to index under every module instance.
#
# The point of the fixture is the module-instance half of the block address:
# resolution names these blocks module.counted[0].aws_eip.pool and
# module.keyed["a"].aws_eip.pool, and the walk that indexes count blocks has
# to name them the same way, or a marker naming one finds no block.

variable "names" {
  type    = set(string)
  default = ["a", "b"]
}

module "counted" {
  source = "./child"
  count  = 2
}

module "keyed" {
  source   = "./child"
  for_each = var.names
}
