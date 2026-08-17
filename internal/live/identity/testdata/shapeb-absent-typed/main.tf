# The third wrinkle: absence is a property of the value the MODULE sees, not
# of the expression the caller wrote. Here the declared type supplies `name`
# through optional(), so the attribute the caller did not write is present
# inside the module and try() must NOT fall back. This package cannot read
# the converted element (the sibling is unknown), so it refuses - which is
# the safe answer, and is what the expression-carrying stops at a declared
# type for.
data "aws_caller_identity" "current" {}

module "u" {
  source = "./mod"

  users = {
    alice = { description = "just a description", account = data.aws_caller_identity.current.account_id }
  }
}
