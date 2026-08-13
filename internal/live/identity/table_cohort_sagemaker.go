// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableSagemaker is the sagemaker cohort's slice of [DefaultTable]:
// the identity rows the sagemaker ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableSagemaker = buildTable(
	// ---- Registry-ratified (#40, #44, #65): SageMaker batch (domains,
	// ---- user profiles, models, endpoints and their configs, notebook
	// ---- instances, feature groups, model package groups, pipelines,
	// ---- spaces and apps, plus the surrounding algorithm/hub/image/
	// ---- workteam/monitoring family; issue #65's ratification campaign).
	// ---- go run ./tools/row-gen's SageMaker section proposed 29 types;
	// ---- 27 ratify here and two are rejected (see the two prose notes
	// ---- below, near aws_sagemaker_device and aws_sagemaker_image_version).
	// ----
	// ---- The dominant finding this batch makes is a service-wide
	// ---- registry-laggard shape, not a one-off correction: for roughly
	// ---- two thirds of these types, live/registry.json's primaryIdentifier
	// ---- names a field CFN models as read-only (an opaque "Id" or an ARN),
	// ---- and row-gen's classifier correctly declines to propose a row on
	// ---- that evidence alone (either "evidence-only" or a GUESSED argument
	// ---- name from the CFN property, never backed by a schema). But the
	// ---- AWS provider's own Argument Reference and Attribute Reference —
	// ---- fetched directly from https://github.com/hashicorp/terraform-provider-aws
	// ---- at the pinned v6.58.0 tag, not merely live/import-grammar.json's
	// ---- cache — show every one of these types is actually client-named:
	// ---- its Attribute Reference states plainly "id - The name of the
	// ---- <Type>", and its Import section documents import by that same
	// ---- name, not by any ARN. Several of these doc pages carry an
	// ---- unrelated copy-paste artifact in their worked example (the
	// ---- literal string "my-code-repo" reused verbatim across the Hub,
	// ---- Image and Model Package Group pages, and "workteam_name" copied
	// ---- into the MLflow Tracking Server page's prose) — the surrounding
	// ---- Argument Reference and the "using the `name`" sentence are what
	// ---- this batch trusts, not the reused example string, which is
	// ---- immaterial to the argument grammar it demonstrates. This is
	// ---- exactly the class of correction the security and streaming
	// ---- batches' own README/table entries already document one or two
	// ---- instances of (aws_guardduty_filter, aws_appsync_domain_name_api_association);
	// ---- this batch just finds it concentrated in one service. Cohort
	// ---- estate: live/e2e/estates/sagemaker.

	serverAssigned("aws_sagemaker_domain",
		"SageMaker AI assigns the Domain its own ID (d-…) at create time; domain_name is client-chosen but does not reconstruct it — the registry and the provider agree here, one of the few SageMaker markers where they do. Confirmed against the provider's documented import command (terraform import aws_sagemaker_domain.test_domain d-8jgsjtilstu8) and its Attribute Reference, which states id is \"The ID of the Domain\", distinct from arn (a different exported value this table does not claim as an identity source).",
		"DOMAINID", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[UserProfileName, DomainId],
		// row-gen flagged this "needs hand separator" (a genuine composite,
		// no separator in any schema). The provider's own documented import
		// command settles the shape directly: an ARN,
		// arn:aws:sagemaker:REGION:ACCOUNT:user-profile/DOMAIN_ID/PROFILE_NAME
		// (terraform import aws_sagemaker_user_profile.example
		// arn:aws:sagemaker:us-west-2:123456789012:user-profile/domain-id/profile-name),
		// built from the region and account of the cloud the run is
		// against plus the domain_id and user_profile_name arguments —
		// both Required in the resource's own schema, so concrete in any
		// realistic config. A real Terraform 1.12+ Identity Schema
		// corroborates the same two components directly as a structured
		// object (required: domain_id, user_profile_name), independent of
		// the ARN string. Same account/region-embedded-ARN shape as
		// aws_codeartifact_domain above.
		Type: "aws_sagemaker_user_profile",
		Components: []Component{
			inAttr("arn", sep("arn:aws:sagemaker:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":user-profile/")),
			inAttr("arn", attr("domain_id")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("user_profile_name")),
		},
		ImportSyntax:  "arn:aws:sagemaker:REGION:ACCOUNT:user-profile/DOMAINID/USERPROFILENAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DomainId, SpaceName], row-gen
		// flagged this "needs hand separator" too. Same correction as the
		// user profile above: the provider's documented import command is
		// an ARN, arn:aws:sagemaker:REGION:ACCOUNT:space/DOMAIN_ID/SPACE_NAME
		// (terraform import aws_sagemaker_space.test_space
		// arn:aws:sagemaker:us-west-2:123456789012:space/domain-id/space-name),
		// built from domain_id and space_name — both Required arguments —
		// plus the region and account of the cloud the run is against. Its
		// Attribute Reference states both arn and id are "The space's
		// Amazon Resource Name (ARN)."
		Type: "aws_sagemaker_space",
		Components: []Component{
			inAttr("arn", sep("arn:aws:sagemaker:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":space/")),
			inAttr("arn", attr("domain_id")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("space_name")),
		},
		ImportSyntax:  "arn:aws:sagemaker:REGION:ACCOUNT:space/DOMAINID/SPACENAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[AppName, AppType, DomainId,
		// UserProfileName], row-gen flagged this "needs hand separator".
		// The provider's documented import command is again an ARN,
		// arn:aws:sagemaker:REGION:ACCOUNT:app/DOMAIN_ID/USER_PROFILE_NAME/APP_TYPE/APP_NAME
		// (terraform import aws_sagemaker_app.example
		// arn:aws:sagemaker:us-west-2:012345678912:app/domain-id/user-profile-name/app-type/app-name),
		// built from domain_id, user_profile_name, app_type and app_name —
		// all four Required or effectively required in the resource's own
		// schema for the user-profile-owned shape this doc example
		// demonstrates — plus the region and account of the run.
		//
		// The resource also supports space-owned apps (user_profile_name
		// and space_name are each Optional; "At least one of
		// user_profile_name or space_name required"), and the provider's
		// docs demonstrate only the user-profile-owned ARN shape above —
		// no worked example or Argument/Attribute Reference text confirms
		// whether a space-owned app's ARN substitutes a "space/space-name"
		// segment or something else. This entry does not guess: it reads
		// user_profile_name specifically, not space_name as a fallback, so
		// a space-owned app's identity simply fails to resolve from
		// configuration here (ClassNeedsDiscovery, the honest outcome)
		// rather than construct an unverified ARN. Its Attribute Reference
		// states both arn and id are "The Amazon Resource Name (ARN) of
		// the app."
		Type: "aws_sagemaker_app",
		Components: []Component{
			inAttr("arn", sep("arn:aws:sagemaker:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":app/")),
			inAttr("arn", attr("domain_id")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("user_profile_name")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("app_type")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("app_name")),
		},
		ImportSyntax:  "arn:aws:sagemaker:REGION:ACCOUNT:app/DOMAINID/USERPROFILENAME/APPTYPE/APPNAME",
		IdentityAttrs: []string{"arn", "id"},
	},

	// aws_sagemaker_device: row-gen classified this "evidence-only" (its
	// registry primaryIdentifier is the composite "Device/DeviceName", and
	// the argument name was GUESSED). Independent verification confirms
	// row-gen's caution rather than correcting it: the provider's
	// documented import command is device-fleet-name/device-name
	// (terraform import aws_sagemaker_device.example my-fleet/my-device),
	// but the resource's own Argument Reference nests device_name inside a
	// Required device{} block as an Optional field (alongside an equally
	// Optional iot_thing_name) — neither is guaranteed present in any
	// given config, and this table's Component vocabulary reads top-level
	// resource arguments by name (see [Component.Attrs]'s doc comment),
	// not fields nested inside a block. Not ratified: no clean proposal.

	TypeIdentity{
		// registry.json: primaryIdentifier=[Id], evidence-only (an opaque
		// "Id" in readOnlyProperties — row-gen correctly declined to
		// propose server-assigned from this alone since it is not ⊆
		// createOnlyProperties either). The provider's Argument Reference
		// and Attribute Reference settle it: client-named via
		// code_repository_name (Required), and id is documented as "The
		// name of the Code Repository." Confirmed against the documented
		// import command (terraform import
		// aws_sagemaker_code_repository.test_code_repository my-code-repo).
		Type:          "aws_sagemaker_code_repository",
		Components:    []Component{attr("code_repository_name")},
		ImportSyntax:  "CODEREPOSITORYNAME",
		IdentityAttrs: []string{"id", "code_repository_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[HubArn], row-gen proposed
		// server-assigned from it — the registry-laggard shape this
		// batch's intro names: the provider's own Argument Reference shows
		// hub_name is Required, its Attribute Reference states id is "The
		// name of the Hub" (arn is a separate, different exported value),
		// and its documented import command uses the name, not the ARN
		// (terraform import aws_sagemaker_hub.test_hub my-code-repo —
		// the worked example string is a copy-paste artifact from the
		// Code Repository doc page above, immaterial to the "using the
		// `name`" grammar it demonstrates).
		Type:          "aws_sagemaker_hub",
		Components:    []Component{attr("hub_name")},
		ImportSyntax:  "HUBNAME",
		IdentityAttrs: []string{"id", "hub_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ImageArn], same correction as
		// the Hub above: the provider's Argument Reference shows image_name
		// is Required, its Attribute Reference states id is "The name of
		// the Image", and its documented import command uses the name
		// (terraform import aws_sagemaker_image.test_image my-code-repo —
		// again the reused worked-example string, not the argument
		// grammar, which is unambiguous).
		Type:          "aws_sagemaker_image",
		Components:    []Component{attr("image_name")},
		ImportSyntax:  "IMAGENAME",
		IdentityAttrs: []string{"id", "image_name"},
	},

	// aws_sagemaker_image_version: row-gen proposed server-assigned via the
	// registry's ImageVersionArn. Independent verification finds a real gap
	// row-gen's registry-only view could not see: the provider's documented
	// import ID is a comma-delimited image_name,version composite
	// (terraform import aws_sagemaker_image_version.example
	// example-name,1), where image_name is the Required argument above but
	// version is a plain output int ("version - The version of the image."
	// in the Attribute Reference only, no corresponding argument anywhere
	// in the Argument Reference) — SageMaker AI assigns each image version
	// its ordinal at create time, the same way aws_lambda_layer_version's
	// own version number is server-assigned. This table's Component
	// vocabulary composes configuration arguments, cloud properties and
	// fixed literals (see [Component]'s doc comment); it has nothing that
	// reads a sibling instance's own not-yet-known server-assigned output
	// mid-composite, the same gap the streaming batch's own
	// aws_appsync_function rejection above names. Not ratified: no clean
	// proposal from configuration alone.

	serverAssigned("aws_sagemaker_mlflow_app",
		"SageMaker AI mints the MLflow App's own ARN at create time; name is Required and client-chosen but names the app, not its identity — the provider's own real Terraform 1.12+ Identity Schema requires arn specifically (the one MLflow proposal in this batch's scope where the registry and the provider agree outright, the same shape aws_msk_cluster's entry above sets). Confirmed against the documented import command (terraform import aws_sagemaker_mlflow_app.example arn:aws:sagemaker:us-east-1:123456789012:mlflow-app/app-ABCD1234).",
		"ARN", "arn", "id"),
	TypeIdentity{
		// registry.json: primaryIdentifier=[TrackingServerName],
		// evidence-only (GUESSED argument, no schema backing). The
		// provider's Argument Reference confirms tracking_server_name is
		// Required ("This string is part of the tracking server ARN"),
		// and its Attribute Reference states id is "The name of the MLFlow
		// Tracking Server." The doc page's own Import prose has a
		// copy-paste bug ("using the `workteam_name`", reused verbatim
		// from the Workteam page below) but its worked example (terraform
		// import aws_sagemaker_mlflow_tracking_server.example example)
		// and the Argument/Attribute Reference agree on tracking_server_name.
		Type:          "aws_sagemaker_mlflow_tracking_server",
		Components:    []Component{attr("tracking_server_name")},
		ImportSyntax:  "TRACKINGSERVERNAME",
		IdentityAttrs: []string{"id", "tracking_server_name"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[ModelArn], row-gen's proposal
		// would have been server-assigned from it. The provider's Argument
		// Reference shows name is Optional (Terraform assigns a random
		// unique name if omitted — the same shape aws_lb's name argument
		// has, which does not change the identity grammar), and its
		// Attribute Reference states plainly "name - Name of the model."
		// Its documented import command uses the name (terraform import
		// aws_sagemaker_model.example model-foo), not the arn the registry
		// proposed.
		Type:          "aws_sagemaker_model",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[EndpointArn], same correction
		// as the Model above: name is Optional (auto-generated if
		// omitted) in the Argument Reference, and the documented import
		// command uses it directly (terraform import
		// aws_sagemaker_endpoint.test_endpoint my-endpoint). Its Attribute
		// Reference does not restate id or name explicitly (only arn and
		// tags_all), so no attribute beyond the argument itself is claimed
		// as an identity source.
		Type:          "aws_sagemaker_endpoint",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Id], evidence-only ("not
		// listable -> client-named only" per row-gen, but no schema-backed
		// argument). A real Terraform 1.12+ Identity Schema settles it
		// directly: required name (String) "Name of the endpoint
		// configuration." Its Argument Reference shows name is Optional,
		// conflicting with name_prefix (Terraform assigns a random unique
		// name, or completes name_prefix, if name itself is unset) — the
		// same optionality shape several S3 bucket rows above already
		// carry. Documented import command: terraform import
		// aws_sagemaker_endpoint_configuration.example example-endpoint-config.
		Type:          "aws_sagemaker_endpoint_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Id], evidence-only. The
		// provider's Argument Reference shows name is Required (not
		// Optional, unlike the Model/Endpoint pair above), and its
		// Attribute Reference states id is "The name of the notebook
		// instance." Documented import command: terraform import
		// aws_sagemaker_notebook_instance.test_notebook_instance
		// my-notebook-instance.
		Type:          "aws_sagemaker_notebook_instance",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Id], evidence-only. The
		// provider's Argument Reference shows name is Optional
		// (auto-generated if omitted). Its Import prose has a copy-paste
		// bug ("using the `name`" is right, but the surrounding sentence
		// says "import models" — reused verbatim from the Model page
		// above); the worked example (terraform import
		// aws_sagemaker_notebook_instance_lifecycle_configuration.lc foo)
		// and the Argument Reference agree on name regardless.
		Type:          "aws_sagemaker_notebook_instance_lifecycle_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[FeatureGroupName], evidence-only
		// (GUESSED argument). The provider's Argument Reference confirms
		// feature_group_name is Required, and its Attribute Reference
		// exports a redundant "name" attribute ("The name of the Feature
		// Group") alongside the arn — a different attribute name than the
		// argument, both carrying the same value. Documented import
		// command: terraform import
		// aws_sagemaker_feature_group.test_feature_group feature_group-foo.
		Type:          "aws_sagemaker_feature_group",
		Components:    []Component{attr("feature_group_name")},
		ImportSyntax:  "FEATUREGROUPNAME",
		IdentityAttrs: []string{"feature_group_name", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ModelPackageGroupArn], the
		// same registry-laggard correction as the Hub and Image above:
		// model_package_group_name is Required per the Argument Reference,
		// id is documented as "The name of the Model Package Group", and
		// the documented import command uses the name (terraform import
		// aws_sagemaker_model_package_group.test_model_package_group
		// my-code-repo — the reused worked-example string again, not the
		// argument grammar).
		Type:          "aws_sagemaker_model_package_group",
		Components:    []Component{attr("model_package_group_name")},
		ImportSyntax:  "MODELPACKAGEGROUPNAME",
		IdentityAttrs: []string{"id", "model_package_group_name"},
	},
	TypeIdentity{
		// row-gen marked this "(property-child of AWS::SageMaker::ModelPackageGroup)
		// [evidence-only]", proposing parent-derived admission "once [the
		// model package group] is ratified" — the ram-servicecatalog
		// family sweep (issue #53, tools/mapping-gen/overlay.d/sweep-ram-servicecatalog.json)
		// independently records the same fold. Ratified alongside the
		// group above: a named-singleton-child keyed on the group's own
		// model_package_group_name, the same shape as
		// aws_secretsmanager_secret_policy. Its only required argument is
		// model_package_group_name (already in configuration through the
		// group marker above), and its Attribute Reference states id is
		// "The name of the Model Package [Group]" (client-named, not a
		// separate identity of its own). Untaggable — no tags argument in
		// its Argument Reference.
		Type:          "aws_sagemaker_model_package_group_policy",
		Components:    []Component{attr("model_package_group_name")},
		ImportSyntax:  "MODELPACKAGEGROUPNAME",
		IdentityAttrs: nil,
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[PipelineName], client-named,
		// row-gen proposed it correctly (argument sourced from
		// live/import-grammar.json). Confirmed against the provider's
		// Argument Reference (pipeline_name Required) and its documented
		// import command (terraform import
		// aws_sagemaker_pipeline.test_pipeline pipeline).
		Type:          "aws_sagemaker_pipeline",
		Components:    []Component{attr("pipeline_name")},
		ImportSyntax:  "PIPELINE_NAME",
		IdentityAttrs: []string{"pipeline_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ModelCardName], client-named,
		// row-gen proposed it correctly. Confirmed against the provider's
		// Argument Reference (model_card_name Required) and its documented
		// import command (terraform import aws_sagemaker_model_card.example
		// my-model-card).
		Type:          "aws_sagemaker_model_card",
		Components:    []Component{attr("model_card_name")},
		ImportSyntax:  "MODEL_CARD_NAME",
		IdentityAttrs: []string{"model_card_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[StudioLifecycleConfigName],
		// client-named, row-gen proposed it correctly. Confirmed against
		// the provider's Argument Reference (studio_lifecycle_config_name
		// Required) and its Attribute Reference, which states id is "The
		// name of the Studio Lifecycle Config."
		Type:          "aws_sagemaker_studio_lifecycle_config",
		Components:    []Component{attr("studio_lifecycle_config_name")},
		ImportSyntax:  "STUDIO_LIFECYCLE_CONFIG_NAME",
		IdentityAttrs: []string{"id", "studio_lifecycle_config_name"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[AlgorithmArn], evidence-only
		// (primaryIdentifier ⊆ readOnlyProperties, the server-assigned
		// shape). A real Terraform 1.12+ Identity Schema settles it the
		// other way: required algorithm_name (String) "Name of the
		// algorithm", which is also Required in the plain Argument
		// Reference. Documented import command: terraform import
		// aws_sagemaker_algorithm.example example-algorithm.
		Type:          "aws_sagemaker_algorithm",
		Components:    []Component{attr("algorithm_name")},
		ImportSyntax:  "ALGORITHM_NAME",
		IdentityAttrs: []string{"algorithm_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=["Device/DeviceName"], different
		// shape from the rejected aws_sagemaker_device above: this is the
		// fleet container, not a device within it. Evidence-only per
		// row-gen (GUESSED argument). The provider's Argument Reference
		// confirms device_fleet_name is Required (top-level, not nested in
		// a block), and its Attribute Reference states id is "The name of
		// the Device Fleet." Documented import command: terraform import
		// aws_sagemaker_device_fleet.example my-fleet.
		Type:          "aws_sagemaker_device_fleet",
		Components:    []Component{attr("device_fleet_name")},
		ImportSyntax:  "DEVICE_FLEET_NAME",
		IdentityAttrs: []string{"id", "device_fleet_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[AppImageConfigName],
		// evidence-only (GUESSED argument — the guess turns out correct).
		// The provider's Argument Reference confirms app_image_config_name
		// is Required, and its Attribute Reference states id is "The name
		// of the App Image Config." Documented import command: terraform
		// import aws_sagemaker_app_image_config.example example.
		Type:          "aws_sagemaker_app_image_config",
		Components:    []Component{attr("app_image_config_name")},
		ImportSyntax:  "APP_IMAGE_CONFIG_NAME",
		IdentityAttrs: []string{"id", "app_image_config_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[MonitoringScheduleArn),
		// evidence-only. The provider's Argument Reference shows name is
		// Optional (auto-generated if omitted), and its Attribute
		// Reference states plainly "name - The name of the monitoring
		// schedule." Documented import command: terraform import
		// aws_sagemaker_monitoring_schedule.test_monitoring_schedule
		// monitoring-schedule-foo.
		Type:          "aws_sagemaker_monitoring_schedule",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[JobDefinitionArn],
		// evidence-only. The provider's Argument Reference shows name is
		// Optional (auto-generated if omitted), and its Attribute
		// Reference states plainly "name - The name of the data quality
		// job definition." Documented import command: terraform import
		// aws_sagemaker_data_quality_job_definition.test_data_quality_job_definition
		// data-quality-job-definition-foo.
		Type:          "aws_sagemaker_data_quality_job_definition",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ProjectArn], evidence-only.
		// The provider's Argument Reference confirms project_name is
		// Required, and its Attribute Reference states id is "The name of
		// the Project" (project_id, a different value, is a separate
		// exported attribute this table does not claim). Documented import
		// command: terraform import aws_sagemaker_project.example example.
		Type:          "aws_sagemaker_project",
		Components:    []Component{attr("project_name")},
		ImportSyntax:  "PROJECT_NAME",
		IdentityAttrs: []string{"id", "project_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Id], evidence-only ("not
		// listable -> client-named only" per row-gen). The provider's
		// Argument Reference confirms workteam_name is Required, and its
		// Attribute Reference states id is "The name of the Workteam."
		// Documented import command: terraform import
		// aws_sagemaker_workteam.example example.
		Type:          "aws_sagemaker_workteam",
		Components:    []Component{attr("workteam_name")},
		ImportSyntax:  "WORKTEAM_NAME",
		IdentityAttrs: []string{"id", "workteam_name"},
	},
)

func init() { registerCohortTable(identityTableSagemaker) }
