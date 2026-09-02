# #190: a resource named through the standard "<name>_prefix" convention
# rather than "<name>" itself is not a missing identity argument - the
# provider assigns a random suffix to the prefix at create time, so the
# resulting name is server-assigned for this instance the same way an
# entirely ServerAssigned type is. Both a name_prefix and an ordinary name
# in the same configuration must resolve differently: the first defers to
# discovery, the second stays concrete.

resource "aws_db_parameter_group" "prefixed" {
  name_prefix = "release-mysql-84-temp-"
  family      = "mysql8.4"
}

resource "aws_db_parameter_group" "named" {
  name   = "engine-params"
  family = "mysql8.4"
}

resource "aws_iam_role" "prefixed" {
  name_prefix        = "app-role-"
  assume_role_policy = "{}"
}
