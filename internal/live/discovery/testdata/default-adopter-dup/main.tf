# Regression fixture for the shape found crossing a real estate
# (choudoufu's corpus-xancloud-iac, XanCloud/xancloud-iac's landing-zone-basic
# blueprint) the day #325 landed: an estate that declares BOTH sides of a
# #325 default-adopter pair - an ordinary aws_security_group next to an
# aws_default_security_group adopting the VPC's own default one, exactly the
# shape terraform-aws-modules/vpc and this real module both use. AWS has one
# DescribeSecurityGroups list call, not two, so both this configuration's
# declared types get scanned against the very same live population, and the
# shared default object comes back from EACH scan - once under
# aws_security_group's own typeName (rebound to aws_default_security_group by
# #325's defaultAdopterSiblings), and once under aws_default_security_group's
# own typeName (already matching, no rebind needed). Before
# claimantAlreadyPresent, that read as two live resources claiming the one
# declared aws_default_security_group.default address - a false
# ProblemCollision on a single object discovered twice, not two objects.

resource "aws_security_group" "other" {
  name = "unrelated"
}

resource "aws_default_security_group" "default" {
}
