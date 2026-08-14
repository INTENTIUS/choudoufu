# Limits fixture: RulePolicyThreshold.
#
# threshold = 0 decodes fine - it is a non-negative literal - and means
# nothing as a first-run guard. The threshold exists to be raised
# deliberately once a delete quadrant's roster has been reviewed, which
# zero or a negative number cannot express. The scope block is present so
# that this fixture trips exactly one rule. See live/LIMITATIONS.md,
# "policy-threshold".

terraform {
  live {
    estate = "my-estate"
    policy {
      undeclared_untagged = "delete"
      threshold           = 0
      scope {
        services = ["ec2"]
      }
    }
  }
}
