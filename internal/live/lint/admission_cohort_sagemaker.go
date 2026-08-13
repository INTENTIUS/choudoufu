// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesSagemaker is the sagemaker cohort's slice of [admittedTypesV0]:
// the types the sagemaker ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesSagemaker = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): SageMaker batch (domains,
	// ---- user profiles, models, endpoints and their configs, notebook
	// ---- instances, feature groups, model package groups, pipelines,
	// ---- spaces and apps, plus the surrounding algorithm/hub/image/
	// ---- workteam/monitoring family; issue #65's ratification campaign).
	// ---- Same tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's own website/docs/r/ source (fetched
	// ---- from GitHub at the pinned v6.58.0 tag) rather than accepted on
	// ---- row-gen's classification alone: most of this batch's rows
	// ---- correct a registry-laggard "evidence-only" or GUESSED-argument
	// ---- verdict once the real Argument/Attribute Reference and, for
	// ---- several types, a genuine Terraform 1.12+ Identity Schema are
	// ---- read directly. Two of row-gen's 29 SageMaker proposals are
	// ---- rejected (aws_sagemaker_device: an Optional argument nested in
	// ---- a block, not a clean top-level identity component;
	// ---- aws_sagemaker_image_version: its documented composite embeds a
	// ---- server-assigned version number with no corresponding
	// ---- configuration argument at all) — see
	// ---- internal/live/identity/table.go for the full per-type evidence
	// ---- and live/e2e/estates/sagemaker/README.md for the account.
	// ---- Cohort estate: live/e2e/estates/sagemaker.
	"aws_sagemaker_algorithm":                                 {},
	"aws_sagemaker_app":                                       {},
	"aws_sagemaker_app_image_config":                          {},
	"aws_sagemaker_code_repository":                           {},
	"aws_sagemaker_data_quality_job_definition":               {},
	"aws_sagemaker_device_fleet":                              {},
	"aws_sagemaker_domain":                                    {},
	"aws_sagemaker_endpoint":                                  {},
	"aws_sagemaker_endpoint_configuration":                    {},
	"aws_sagemaker_feature_group":                             {},
	"aws_sagemaker_hub":                                       {},
	"aws_sagemaker_image":                                     {},
	"aws_sagemaker_mlflow_app":                                {},
	"aws_sagemaker_mlflow_tracking_server":                    {},
	"aws_sagemaker_model":                                     {},
	"aws_sagemaker_model_card":                                {},
	"aws_sagemaker_model_package_group":                       {},
	"aws_sagemaker_model_package_group_policy":                {},
	"aws_sagemaker_monitoring_schedule":                       {},
	"aws_sagemaker_notebook_instance":                         {},
	"aws_sagemaker_notebook_instance_lifecycle_configuration": {},
	"aws_sagemaker_pipeline":                                  {},
	"aws_sagemaker_project":                                   {},
	"aws_sagemaker_space":                                     {},
	"aws_sagemaker_studio_lifecycle_config":                   {},
	"aws_sagemaker_user_profile":                              {},
	"aws_sagemaker_workteam":                                  {},
}

func init() { registerCohortAdmitted(admittedTypesSagemaker) }
