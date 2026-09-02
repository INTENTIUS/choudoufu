// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The sagemaker cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableSagemaker = []string{
	// SageMaker batch (issue #65): 26 of its 27 ratified types carry a
	// tags argument, confirmed against each type's documented Argument
	// Reference at the pinned v6.58.0 tag; see
	// live/e2e/estates/sagemaker/README.md, "Untaggable types" for the
	// one exception (aws_sagemaker_model_package_group_policy, in
	// untaggableAdmittedTypes below).
	"aws_sagemaker_algorithm",
	"aws_sagemaker_app",
	"aws_sagemaker_app_image_config",
	"aws_sagemaker_code_repository",
	"aws_sagemaker_data_quality_job_definition",
	"aws_sagemaker_device_fleet",
	"aws_sagemaker_domain",
	"aws_sagemaker_endpoint",
	"aws_sagemaker_endpoint_configuration",
	"aws_sagemaker_feature_group",
	"aws_sagemaker_hub",
	"aws_sagemaker_image",
	"aws_sagemaker_mlflow_app",
	"aws_sagemaker_mlflow_tracking_server",
	"aws_sagemaker_model",
	"aws_sagemaker_model_card",
	"aws_sagemaker_model_package_group",
	"aws_sagemaker_monitoring_schedule",
	"aws_sagemaker_notebook_instance",
	"aws_sagemaker_notebook_instance_lifecycle_configuration",
	"aws_sagemaker_pipeline",
	"aws_sagemaker_project",
	"aws_sagemaker_space",
	"aws_sagemaker_studio_lifecycle_config",
	"aws_sagemaker_user_profile",
	"aws_sagemaker_workteam",
}

var untaggableSagemaker = []string{
	// SageMaker batch (issue #65): the one untaggable type this batch
	// ratifies — a named-singleton-child of aws_sagemaker_model_package_group
	// whose Argument Reference names only region and
	// model_package_group_name, no tags block at all. See
	// live/e2e/estates/sagemaker/README.md, "Untaggable types".
	"aws_sagemaker_model_package_group_policy",
}

func init() {
	registerCohortStamp(taggableSagemaker, untaggableSagemaker, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// SageMaker batch (issue #65). Taggable per the real provider's
			// documented Argument Reference for each type, except
			// aws_sagemaker_model_package_group_policy, whose Argument
			// Reference names only region and model_package_group_name.
			"aws_sagemaker_algorithm":                                 taggedSchema("id", "algorithm_name", "arn"),
			"aws_sagemaker_app":                                       taggedSchema("id", "arn", "app_name", "app_type", "domain_id"),
			"aws_sagemaker_app_image_config":                          taggedSchema("id", "app_image_config_name", "arn"),
			"aws_sagemaker_code_repository":                           taggedSchema("id", "code_repository_name", "arn"),
			"aws_sagemaker_data_quality_job_definition":               taggedSchema("id", "arn", "name", "role_arn"),
			"aws_sagemaker_device_fleet":                              taggedSchema("id", "device_fleet_name", "arn", "role_arn"),
			"aws_sagemaker_domain":                                    taggedSchema("id", "arn", "domain_name", "auth_mode"),
			"aws_sagemaker_endpoint":                                  taggedSchema("id", "arn", "name", "endpoint_config_name"),
			"aws_sagemaker_endpoint_configuration":                    taggedSchema("id", "arn", "name"),
			"aws_sagemaker_feature_group":                             taggedSchema("id", "arn", "feature_group_name", "role_arn"),
			"aws_sagemaker_hub":                                       taggedSchema("id", "arn", "hub_name"),
			"aws_sagemaker_image":                                     taggedSchema("id", "arn", "image_name", "role_arn"),
			"aws_sagemaker_mlflow_app":                                taggedSchema("arn", "name", "role_arn"),
			"aws_sagemaker_mlflow_tracking_server":                    taggedSchema("id", "arn", "tracking_server_name", "role_arn"),
			"aws_sagemaker_model":                                     taggedSchema("id", "arn", "name", "execution_role_arn"),
			"aws_sagemaker_model_card":                                taggedSchema("id", "arn", "model_card_name"),
			"aws_sagemaker_model_package_group":                       taggedSchema("id", "arn", "model_package_group_name"),
			"aws_sagemaker_model_package_group_policy":                untaggedSchema("id", "model_package_group_name", "resource_policy"),
			"aws_sagemaker_monitoring_schedule":                       taggedSchema("id", "arn", "name"),
			"aws_sagemaker_notebook_instance":                         taggedSchema("id", "arn", "name", "role_arn", "instance_type"),
			"aws_sagemaker_notebook_instance_lifecycle_configuration": taggedSchema("id", "arn", "name"),
			"aws_sagemaker_pipeline":                                  taggedSchema("id", "arn", "pipeline_name"),
			"aws_sagemaker_project":                                   taggedSchema("id", "arn", "project_name"),
			"aws_sagemaker_space":                                     taggedSchema("id", "arn", "domain_id", "space_name"),
			"aws_sagemaker_studio_lifecycle_config":                   taggedSchema("id", "arn", "studio_lifecycle_config_name"),
			"aws_sagemaker_user_profile":                              taggedSchema("arn", "domain_id", "user_profile_name"),
			"aws_sagemaker_workteam":                                  taggedSchema("id", "arn", "workteam_name"),
		})
	})
}
