# Coverage: marker path over an ARN-identified type
# (aws_sfn_state_machine — Step Functions mints the state machine ARN at
# create time; the name is client-chosen but the provider's identity schema
# requires the ARN, which wraps the name in an account and a region the
# configuration does not carry). Third slice of the survey's marker cohort
# (#20).
#
# role_arn names the app role's ARN literally rather than as
# aws_iam_role.app.arn — the same treatment aws_iam_role_policy.app gives
# the bucket ARN, and for the same reason: the role is the fixture's
# standing residue (floci's iam:GetRole omits Tags, so live plans report it
# unowned and propose its create), and an interpolated ARN would drag this
# state machine into that residue as a second downstream change. The account
# segment is floci's fixed 000000000000, which the harness already relies on
# throughout.
resource "aws_sfn_state_machine" "pipeline" {
  name     = "tofu-stateless-e2e-pipeline"
  role_arn = "arn:aws:iam::000000000000:role/tofu-stateless-e2e-app"

  definition = jsonencode({
    StartAt = "Done"
    States = {
      Done = { Type = "Succeed" }
    }
  })

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sfn_state_machine.pipeline"
  }
}
