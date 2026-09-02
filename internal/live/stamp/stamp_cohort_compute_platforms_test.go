// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The compute-platforms cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableComputePlatforms = []string{
	// Registry-ratified compute-platforms batch (#40, #44, issue #65's
	// ratification campaign): Batch, EMR remainder, App Runner, Elastic
	// Beanstalk, Amplify and Lightsail. Three of this batch's types are
	// untaggable instead — see below. See
	// live/e2e/estates/compute-platforms/README.md.
	"aws_batch_compute_environment",
	"aws_batch_job_definition",
	"aws_batch_job_queue",
	"aws_batch_scheduling_policy",
	"aws_emr_cluster",
	"aws_emr_studio",
	"aws_emrcontainers_virtual_cluster",
	"aws_emrserverless_application",
	"aws_apprunner_auto_scaling_configuration_version",
	"aws_apprunner_observability_configuration",
	"aws_apprunner_service",
	"aws_apprunner_vpc_connector",
	"aws_apprunner_vpc_ingress_connection",
	"aws_elastic_beanstalk_application",
	"aws_elastic_beanstalk_environment",
	"aws_amplify_app",
	"aws_amplify_branch",
	"aws_lightsail_bucket",
	"aws_lightsail_certificate",
	"aws_lightsail_container_service",
	"aws_lightsail_database",
	"aws_lightsail_disk",
	"aws_lightsail_distribution",
	"aws_lightsail_instance",
	"aws_lightsail_lb",
}

var untaggableComputePlatforms = []string{
	// Registry-ratified compute-platforms batch (#40, #44, issue #65's
	// ratification campaign): three types with no tags argument at all
	// in the pinned provider's own wire schema, confirmed against
	// live/survey-full.json and, for the two Lightsail rows, the
	// provider's own Argument Reference directly.
	// aws_emr_security_configuration is client-named (its identity does
	// not depend on the marker path a tag enables); the other two are
	// this batch's parent-derived composites. See
	// live/e2e/estates/compute-platforms/README.md, "Untaggable types".
	"aws_emr_security_configuration",
	"aws_lightsail_lb_certificate",
	"aws_lightsail_static_ip",
}

func init() {
	registerCohortStamp(taggableComputePlatforms, untaggableComputePlatforms, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified compute-platforms batch (#40, #44, issue #65's
			// ratification campaign). Taggable/untaggable per the real
			// provider's documented Argument Reference for each type:
			// aws_emr_security_configuration and the two Lightsail
			// parent-derived composites (aws_lightsail_lb_certificate,
			// aws_lightsail_static_ip) carry no tags argument at all.
			"aws_batch_compute_environment":                    taggedSchema("id", "arn", "compute_environment_name"),
			"aws_batch_job_definition":                         taggedSchema("id", "arn", "name"),
			"aws_batch_job_queue":                              taggedSchema("id", "arn", "name"),
			"aws_batch_scheduling_policy":                      taggedSchema("id", "arn", "name"),
			"aws_emr_cluster":                                  taggedSchema("id", "arn", "name", "release_label", "service_role"),
			"aws_emr_security_configuration":                   untaggedSchema("id", "name", "configuration"),
			"aws_emr_studio":                                   taggedSchema("id", "arn", "name", "auth_mode", "service_role", "vpc_id"),
			"aws_emrcontainers_virtual_cluster":                taggedSchema("id", "arn", "name"),
			"aws_emrserverless_application":                    taggedSchema("id", "arn", "name", "type"),
			"aws_apprunner_auto_scaling_configuration_version": taggedSchema("id", "arn"),
			"aws_apprunner_observability_configuration":        taggedSchema("id", "arn"),
			"aws_apprunner_service":                            taggedSchema("id", "arn", "service_name"),
			"aws_apprunner_vpc_connector":                      taggedSchema("id", "arn"),
			"aws_apprunner_vpc_ingress_connection":             taggedSchema("id", "arn", "service_arn"),
			"aws_elastic_beanstalk_application":                taggedSchema("id", "name"),
			"aws_elastic_beanstalk_environment":                taggedSchema("id", "name", "application"),
			"aws_amplify_app":                                  taggedSchema("id", "arn", "name"),
			"aws_amplify_branch":                               taggedSchema("id", "arn", "app_id", "branch_name"),
			"aws_lightsail_bucket":                             taggedSchema("id", "arn", "name"),
			"aws_lightsail_certificate":                        taggedSchema("id", "arn", "name"),
			"aws_lightsail_container_service":                  taggedSchema("id", "arn", "name"),
			"aws_lightsail_database":                           taggedSchema("id", "arn", "relational_database_name"),
			"aws_lightsail_disk":                               taggedSchema("id", "arn", "name"),
			"aws_lightsail_distribution":                       taggedSchema("id", "arn", "name"),
			"aws_lightsail_instance":                           taggedSchema("id", "arn", "name"),
			"aws_lightsail_lb":                                 taggedSchema("id", "arn", "name"),
			"aws_lightsail_lb_certificate":                     untaggedSchema("id", "arn", "lb_name", "name"),
			"aws_lightsail_static_ip":                          untaggedSchema("id", "arn", "name"),
		})
	})
}
