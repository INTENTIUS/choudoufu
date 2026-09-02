# modulearg-nested-partial's mutation, and the half that has to hold for the
# other half to be worth having. Same two-call composition, same single
# unknowable leaf - moved from a VALUE to the place that decides an ADDRESS.
#
# ./preset builds its map by keying on the leaf's own value, and ./inner
# builds a second for_each over a SET whose ELEMENTS are that leaf. Neither
# key set is in the configuration, so neither may name an instance: naming
# the literal half alone silently drops one, and inventing the other writes
# a fabricated address into a cloud tag.
resource "aws_iam_role" "r" {
  name = "the-role"
}

module "preset" {
  source = "./preset"

  refs = {
    app = aws_iam_role.r.arn
  }
}
