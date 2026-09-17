# Limits fixture: RuleRetry (GitHub issues #1196, #1148).
#
# `mode = "aggressive"` is neither of the two retry modes the aws-sdk-go-v2
# clients this fork builds can be put into. The refusal is the point, and the
# reason is the same one the strict block's settings give: the two real modes
# differ in what they do when the cloud pushes back. "standard" spends its
# attempt budget at a fixed rate; "adaptive" slows its send rate against the
# service's own throttling signal and speeds back up. A spelling that is
# neither could plausibly be read as either.
#
# Resolving it to the default would run this estate under precisely the mode
# its author was trying to change - and this block exists because that
# mattered: a scale-50 certification against real AWS failed test_apply on
# nothing but "exceeded maximum number of attempts, 3 ... ThrottlingException"
# while its plan was empty. See live/LIMITATIONS.md, "retry".

terraform {
  live {
    estate = "my-estate"
    retry {
      mode = "aggressive"
    }
  }
}
