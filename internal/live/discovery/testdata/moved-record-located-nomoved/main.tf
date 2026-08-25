# The mutation-check sibling of testdata/moved-record-located: the SAME
# declared block, deliberately with no `moved` statement at all, so a
# record sitting under some other address is a genuine orphan and this
# leg's destroy proposal must stand.

resource "aws_iam_role_policy" "inline" {
  name   = "deploy"
  role   = "app"
  policy = "{}"
}
