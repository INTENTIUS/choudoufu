# The mutation of shapeb-poison, and the boundary the selection must not
# cross: the only thing changed is WHICH attribute the identity reads. The
# element is the same element; `account` is the data-source read, and
# selecting it must still refuse rather than answer with a neighbour's value
# or with the key.
data "aws_caller_identity" "current" {}

module "u" {
  source = "./mod"

  users = {
    alice = { name = "alice-user", account = data.aws_caller_identity.current.account_id }
    bob   = { name = "bob-user", account = data.aws_caller_identity.current.account_id }
  }
}
