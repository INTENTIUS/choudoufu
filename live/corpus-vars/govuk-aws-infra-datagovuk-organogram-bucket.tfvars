# choudoufu corpus measurement input (#183, live/corpus-manifest.json).
#
# Not part of the alphagov/govuk-aws estate. infra-datagovuk-organogram-
# bucket declares "aws_environment" as a required root variable with no
# default, and its language-wall refusals (Non-static identity argument and
# the "Unresolvable identity" it cascades into, on the
# "datagovuk-${var.aws_environment}-ckan-organogram" bucket name) are
# flagged unset_var_only in live/corpus-refusals.json (#178's rule-2
# scoping). This file supplies it so the corpus can measure whether the
# language subset accepts the configuration once the value exists - it is
# not a claim about what govuk-aws actually runs with.
#
# Value policy: this directory ships integration.govuk.backend,
# staging.govuk.backend and production.govuk.backend - three backend
# configs, one per named environment. "integration" is one of the estate's
# own names, not an invented one.
aws_environment = "integration"

# "domain" and "remote_state_bucket" are also required with no default, but
# no refused site reads either (domain only reaches a CORS allowed_origins
# list; remote_state_bucket only reaches a data.terraform_remote_state
# argument, already refused outright as "remote-state"), so both are left
# unset here on purpose. What remains blocked after aws_environment alone -
# aws_iam_policy_attachment, an unadmitted resource type - is a type-
# coverage gap, not a language one, and outside this file's job.
