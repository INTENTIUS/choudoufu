# Issue #375. Five calls into ONE child module, differing only in how the
# caller spells the argument, to pin which spellings the tolerant
# substitution reaches and which it does not.
#
# The two that used to refuse and now resolve are "hoisted" and "output":
# both name a value every member of which is written in the caller's own
# files except one, and stock OpenTofu plans all five.
#
#   hoisted  a constructor the caller put in a local instead of writing out
#            at the call. Identical in content to what "inline" would be, and
#            [rebuildConstructor] used to see a bare traversal rather than an
#            ObjectConsExpr and decline.
#   output   a child module's whole output, named on its own. The identical
#            reference already resolved as a LEAF of a constructor
#            ([elementOrUnknown]'s moduleOutput arm); on its own there was no
#            constructor to be a leaf of.
#
# "merged" - merge() of two literal objects, one member of which reads the
# live subnet - joined them when the tolerant static scope landed
# ([configs.StaticEvaluator.WithUnknownForRefusedReferences]). The call is
# still never REBUILT; it is RUN, on a value whose refused leaf the scope
# substituted an unknown for, and merge's own answer is the one taken. Its
# "derived" resource, which reads that very member, keeps refusing, which is
# the half that says the substitution did not become a marker.
#
# The two that must keep refusing:
#
#   secret   a sensitive module output. A marker is written into a cloud tag
#            in clear, so the refusal is on the DECLARATION, not on whether
#            the mark survives.
#   dynamic  nothing in the map is knowable, so nothing is.
resource "aws_subnet" "s" {
  vpc_id     = "vpc-11111111"
  cidr_block = "10.0.0.0/24"
}

locals {
  hoisted = {
    enabled = true
    label   = "hoisted"
    subnet  = aws_subnet.s.id
  }

  merged = merge({ enabled = true, label = "merged" }, { subnet = aws_subnet.s.id })

  dynamic = {
    enabled = aws_subnet.s.id != ""
    label   = aws_subnet.s.id
    subnet  = aws_subnet.s.id
  }
}

module "net" {
  source = "./net"
}

module "secret_net" {
  source = "./secret-net"
}

module "hoisted" {
  source = "./gate"

  base_configuration = local.hoisted
}

module "output" {
  source = "./gate"

  base_configuration = module.net.configuration
}

module "merged" {
  source = "./gate"

  base_configuration = local.merged
}

module "secret" {
  source = "./gate"

  base_configuration = module.secret_net.configuration
}

module "dynamic" {
  source = "./gate"

  base_configuration = local.dynamic
}

# The control the whole fix is measured against: the identical object, written
# out at the call instead of hoisted into a local. It resolved before this
# change and must resolve to the SAME thing "hoisted" now does - same class,
# same rendered value, on both resources. The claim is an equivalence between
# two spellings, not a new answer.
module "inline" {
  source = "./gate"

  base_configuration = {
    enabled = true
    label   = "hoisted"
    subnet  = aws_subnet.s.id
  }
}
