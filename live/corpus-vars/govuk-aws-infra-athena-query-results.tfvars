# choudoufu corpus measurement input (#183, live/corpus-manifest.json).
#
# Not part of the alphagov/govuk-aws estate. infra-athena-query-results
# declares "aws_environment" as a required root variable with no default,
# and its only language-wall refusal (Non-static identity argument, on the
# "govuk-${var.aws_environment}-athena-query-results" bucket name) is
# flagged unset_var_only in live/corpus-refusals.json (#178's rule-2
# scoping). This file supplies it so the corpus can measure whether the
# language subset accepts the configuration once the value exists - it is
# not a claim about what govuk-aws actually runs with.
#
# Value policy: this directory ships integration.govuk.backend,
# staging.govuk.backend, production.govuk.backend and test.govuk.backend -
# four backend configs, one per named environment. "integration" is one of
# the estate's own names, not an invented one.
aws_environment = "integration"

# "stackname" is also required with no default, but no refused site reads
# it (it only reaches remote_state.tf's data.terraform_remote_state
# arguments, already refused outright as "remote-state" regardless of its
# value), so it is left unset here on purpose.
