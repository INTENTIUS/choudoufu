// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesComputePlatforms is the compute-platforms cohort's slice of [admittedTypesV0]:
// the types the compute-platforms ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesComputePlatforms = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): fifth batch, compute
	// ---- platforms (Batch, EMR remainder, App Runner, Elastic
	// ---- Beanstalk, Amplify, Lightsail). Same tools/row-gen pipeline as
	// ---- the earlier batches, cross-checked against the AWS provider's
	// ---- documented import behaviour (its own Argument/Attribute/Import
	// ---- sections, fetched from the pinned v6.58.0 tag) and, where
	// ---- row-gen's registry-only evidence was silent on recoverability
	// ---- or wrong about the argument, against live/tag-verbs.json,
	// ---- live/survey-full.json's mechanical per-type signals, and
	// ---- live/import-grammar.json's docs-derived evidence — not
	// ---- accepted on row-gen's classification alone. Two reclassified
	// ---- rows (aws_batch_job_definition, aws_amplify_app) and one
	// ---- corrected wrong guess (aws_elastic_beanstalk_environment) are
	// ---- the notable catches; see internal/live/identity/table.go for
	// ---- the per-type evidence and for the rejected and deferred
	// ---- proposals. Cohort estate: live/e2e/estates/compute-platforms.
	"aws_batch_compute_environment":                    {},
	"aws_batch_job_definition":                         {},
	"aws_batch_job_queue":                              {},
	"aws_batch_scheduling_policy":                      {},
	"aws_emr_cluster":                                  {},
	"aws_emr_security_configuration":                   {},
	"aws_emr_studio":                                   {},
	"aws_emrcontainers_virtual_cluster":                {},
	"aws_emrserverless_application":                    {},
	"aws_apprunner_auto_scaling_configuration_version": {},
	"aws_apprunner_observability_configuration":        {},
	"aws_apprunner_service":                            {},
	"aws_apprunner_vpc_connector":                      {},
	"aws_apprunner_vpc_ingress_connection":             {},
	"aws_elastic_beanstalk_application":                {},
	"aws_elastic_beanstalk_environment":                {},
	"aws_amplify_app":                                  {},
	"aws_amplify_branch":                               {},
	"aws_lightsail_bucket":                             {},
	"aws_lightsail_certificate":                        {},
	"aws_lightsail_container_service":                  {},
	"aws_lightsail_database":                           {},
	"aws_lightsail_disk":                               {},
	"aws_lightsail_distribution":                       {},
	"aws_lightsail_instance":                           {},
	"aws_lightsail_lb":                                 {},
	"aws_lightsail_lb_certificate":                     {},
	"aws_lightsail_static_ip":                          {},
}

func init() { registerCohortAdmitted(admittedTypesComputePlatforms) }
