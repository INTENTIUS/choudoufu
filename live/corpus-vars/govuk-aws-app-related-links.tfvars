# choudoufu corpus measurement input (#183, live/corpus-manifest.json).
#
# Not part of the alphagov/govuk-aws estate. app-related-links declares
# "aws_environment" and "stackname" as required root variables with no
# default; every one of this estate's language-wall refusals reads one of
# the two and is flagged unset_var_only in live/corpus-refusals.json (#178's
# rule-2 scoping). This file supplies both so the corpus can measure whether
# the language subset accepts the configuration once they exist - it is not
# a claim about what govuk-aws actually runs with.
#
# Value policy: neutral placeholders, and only what the estate's own files
# already imply, never an invented environment name.
#
# aws_environment: this directory ships integration.govuk.backend,
# staging.govuk.backend and production.govuk.backend - three backend
# configs, one per named environment. "integration" is one of the estate's
# own names, not a guess.
aws_environment = "integration"

# stackname: not identity-bearing on its own (it feeds
# "${var.stackname}-ec2-role" and the remote-state key prefixes), but
# required. ../../terraform/README.md documents the project's own naming
# convention literally as "<stackname>.backend", and this directory's own
# integration.govuk.backend is exactly that pattern with stackname
# substituted - so "integration.govuk" is read off the estate's own
# filename, not invented.
stackname = "integration.govuk"

# jenkins_ssh_public_key is also required with no default, but no refused
# site reads it (it only reaches the non-identity "public_key" argument of
# aws_key_pair.jenkins_public_key), so it is left unset here on purpose:
# supplying it would not change which refusals fire, only mask an unrelated
# gap in the artifact's unset-variable accounting.
