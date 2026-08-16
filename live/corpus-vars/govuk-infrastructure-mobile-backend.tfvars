# choudoufu corpus measurement input (#183, live/corpus-manifest.json).
#
# Not part of the alphagov/govuk-infrastructure estate. mobile-backend
# declares "govuk_environment" as a required root variable with no default.
# Its language-wall refusals sit across a module boundary - the site is
# "shared-modules/s3/main.tf", reached via
# "module.mobile_backend_remote_config { govuk_environment =
# var.govuk_environment }" in config-bucket.tf - and are flagged
# unset_var_only in live/corpus-refusals.json once the accounting walk
# (#183 part 1) follows the module-call argument back to this root
# variable (#178's rule-2 scoping is this estate's own worked example).
# This file supplies the variable so the corpus can measure whether the
# language subset accepts the configuration once the value exists - it is
# not a claim about what govuk-infrastructure actually runs with.
#
# Value policy: variables-common.tf's own validation block constrains
# govuk_environment to "production", "staging", "integration", "test" or an
# "eph-" prefix - "integration" is one of the estate's own named values,
# not an invented one.
govuk_environment = "integration"

# The rest of variables-common.tf's declarations (vpc_cidr, the three
# subnet maps, publishing_service_domain, force_destroy, cluster_name,
# cluster_log_retention_in_days) are not referenced anywhere in this
# deployment's own .tf files, so no reference from it is ever unresolved
# and nothing is left unset here on their account.
