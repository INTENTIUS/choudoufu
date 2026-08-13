// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableComputePlatforms is the compute-platforms cohort's slice of [DefaultTable]:
// the identity rows the compute-platforms ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableComputePlatforms = buildTable(
	// ---- Registry-ratified (#40, #44, #65): fifth batch, compute
	// ---- platforms -------------------------------------------------------
	//
	// Same tools/row-gen pipeline as the earlier batches: every row started
	// as a proposal from live/registry.json, cross-checked against the AWS
	// provider's documented import behaviour (its Argument Reference,
	// Attribute Reference and Import section, fetched from the provider's
	// own website/docs/r/ source at the pinned v6.58.0 tag) rather than
	// accepted on the registry's word alone, plus live/tag-verbs.json and
	// live/survey-full.json's per-type taggable/listable signals for
	// recoverability the same way the Route53/CloudFront batch above used
	// them. Six services: Batch, EMR (cluster/security-configuration/
	// studio plus the EMRContainers and EMRServerless siblings), App
	// Runner, Elastic Beanstalk, Amplify, Lightsail. Cohort estate:
	// live/e2e/estates/compute-platforms.
	//
	// Three rows needed reclassification beyond what row-gen itself
	// proposed, all caught by reading the provider docs rather than
	// trusting the registry or row-gen's own bucket:
	//
	//   - aws_batch_job_definition: row-gen proposed client-named on an
	//     "arn" argument (its own resolveArgName pulled "arn" out of
	//     live/survey-full.json's identity-schema signal, which names the
	//     provider's *identity* attribute, not a settable argument — the
	//     applyImportGrammarDemotions check that catches this shape for
	//     server-assigned rows never runs on a client-named one, so
	//     row-gen's own proposal is a bug here, not a source to paste
	//     as-is). The provider's Argument Reference confirms "arn" is
	//     Computed only; the Attribute Reference confirms the ARN embeds a
	//     revision number Batch mints on every new revision
	//     ("arn - ARN of the job definition, includes revision (:#)"),
	//     which no argument reconstructs. Ratified below as server-assigned
	//     instead.
	//   - aws_amplify_app: row-gen proposed server-assigned via the
	//     registry's "Arn" primaryIdentifier — right about server-assigned,
	//     wrong about which token: the provider's Import section documents
	//     import by the App ID alone (e.g. "d2ypk4k47z8u6"), and the
	//     Attribute Reference lists "id" ("Unique ID of the Amplify app")
	//     as a separate exported attribute from "arn", confirming they are
	//     not the same string. Ratified below with IdentityAttrs "id" only.
	//   - aws_elastic_beanstalk_environment: row-gen classified this
	//     evidence-only (its "environment_name" argument was GUESSED, not
	//     backed by an identity schema or the carve seed). The provider's
	//     own Import section shows otherwise: "import Elastic Beanstalk
	//     Environments using the id" (example: e-rpqsewtp2j), an opaque
	//     token Elastic Beanstalk mints at create time that no argument in
	//     the registry's create-only set (CNAMEPrefix, EnvironmentName,
	//     ApplicationName, SolutionStackName, Tier/Name, Tier/Type)
	//     reconstructs — the CFN registry entry does not even carry an
	//     EnvironmentId in its own read-only properties, a registry gap
	//     the provider's docs fill in directly. Ratified below as
	//     server-assigned, the same "docs over registry" move the earlier
	//     Route53/CloudFront batch made repeatedly.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_emr_instance_fleet, aws_emr_instance_group: row-gen proposed
	//     both server-assigned via the registry's opaque "Id" — the
	//     mapping only reaches them at all because live/mapping.json
	//     aliases them onto AWS::EMR::InstanceFleetConfig and
	//     AWS::EMR::InstanceGroupConfig (their TF and CFN names do not
	//     match directly). live/survey-full.json's mechanical signal
	//     rejects both anyway: untaggable, no native list resource, "no
	//     admission path recovers it" — they are child objects of a
	//     cluster with no individual tagging or listing surface of their
	//     own, the same shape live/tag-verbs.json's EMR AddTags entry
	//     shows (ClusterId plus ResourceId, not a single scalar this
	//     fork's tag-filtered marker discovery can key on). Confirmed
	//     against the provider's own Import section too: both import by
	//     CLUSTERID/FLEETID or CLUSTERID/GROUPID, a composite whose first
	//     half is recoverable (the cluster's marker) but whose second half
	//     names a specific live instance discovery has no way to find
	//     without the tag or list surface neither type has.
	//   - aws_emr_studio_session_mapping: row-gen classified this
	//     needs-hand-separator (registry primaryIdentifier ["StudioId",
	//     "IdentityType", "IdentityName"], composite, no separator in any
	//     schema). The provider's Import section does supply one
	//     (STUDIOID:IDENTITYTYPE:IDENTITYID, colon-separated) and all three
	//     are plain configuration arguments, the same shape the
	//     Route53/CloudFront batch hand-verified for several composites —
	//     but this batch's own scope for the EMR remainder is "only what
	//     row-gen proposes" (unlike Lightsail below, which was scoped to
	//     expand on row-gen's guesses), so it is left out here rather than
	//     hand-added, a deliberate scope boundary rather than a recoverability
	//     failure.
	//   - aws_emr_managed_scaling_policy: row-gen folded this as a
	//     property-child of AWS::EMR::Cluster (evidence-only, no pastable
	//     row); same "only what row-gen proposes" scope boundary as the
	//     session mapping above, even though its own Import section
	//     ("using the EMR Cluster identifier") would make it a clean
	//     named-singleton-child of aws_emr_cluster once in scope.
	//   - aws_elastic_beanstalk_application_version,
	//     aws_elastic_beanstalk_configuration_template: both
	//     needs-hand-separator per row-gen, and this batch's own scope for
	//     Elastic Beanstalk names only "applications, environments" —
	//     both are out of scope on that alone, and independent
	//     verification does not make either an easy hand-add anyway: the
	//     provider ships no Import section at all for
	//     aws_elastic_beanstalk_application_version (confirmed absent from
	//     live/import-grammar.json and from the provider's own docs page),
	//     and aws_elastic_beanstalk_configuration_template's
	//     live/survey-full.json signal is untaggable with no native list
	//     resource.
	//   - aws_amplify_domain_association: evidence-only per row-gen
	//     (import docs show the same composite shape as aws_amplify_branch
	//     below, APPID/DOMAINNAME), independently verifiable the same way
	//     — but this batch's own scope for Amplify names only
	//     "apps/branches", so it is left out rather than hand-added.
	//   - aws_amplify_webhook: never reaches row-gen at all —
	//     live/mapping.json records it "cfn-unmodeled" ("searched
	//     AWS::Amplify: only App, Branch and Domain exist; no Webhook
	//     type"), and notes by name that it is not
	//     AWS::CodePipeline::Webhook, a different service's unrelated
	//     same-named concept — the false-positive risk this batch's own
	//     recipe flagged in advance. Nothing to ratify or reject: it is
	//     outside the registry-backed pipeline entirely.
	//   - aws_lightsail_domain: row-gen classified this evidence-only (its
	//     "domain_name" argument guess is in fact correct — confirmed
	//     against the provider's Argument Reference, domain_name is the
	//     sole required argument), but the provider ships no Import
	//     section for this resource at all (confirmed absent from both
	//     live/import-grammar.json and the provider's own docs page), and
	//     live/survey-full.json's signal agrees separately: untaggable,
	//     no native list resource, "no admission path recovers it". Two
	//     independent rejections, and also simply not one of the
	//     categories ("instances, databases, buckets, LBs + certificates")
	//     this batch's own Lightsail scope named.
	//   - aws_lightsail_bucket_resource_access,
	//     aws_lightsail_container_service_deployment_version,
	//     aws_lightsail_disk_attachment, aws_lightsail_domain_entry,
	//     aws_lightsail_lb_attachment, aws_lightsail_lb_certificate_attachment,
	//     aws_lightsail_lb_https_redirection_policy,
	//     aws_lightsail_lb_stickiness_policy,
	//     aws_lightsail_bucket_access_key: property-child folds of the
	//     Lightsail types ratified below. Several now have every parent
	//     they need (aws_lightsail_lb_attachment's LBNAME,INSTANCENAME
	//     composite, for one), but adding parent-derived rows for a whole
	//     second tier of child types is a bigger step than this batch's
	//     named Lightsail scope ("instances, databases, buckets, LBs +
	//     certificates") asked for. Left for a future batch, the same
	//     deliberate boundary as the EMR and Elastic Beanstalk exclusions
	//     above.
	//
	// See internal/live/lint/admission.go's matching banner for the same
	// summary, and live/e2e/estates/compute-platforms/README.md for the
	// full per-type coverage table.

	serverAssigned("aws_batch_compute_environment",
		"AWS Batch mints the compute environment's own ARN at create time; the compute_environment_name argument is client-chosen but is not the import identity — the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	// aws_batch_job_definition: reclassified from row-gen's client-named
	// "arn" proposal to server-assigned; see the batch banner above.
	serverAssigned("aws_batch_job_definition",
		"AWS Batch mints a new ARN, embedding a revision number, on every job definition revision; the job_definition_name argument only a fresh revision's config sets does not reconstruct it. The provider's Identity Schema requires arn.",
		"ARN", "arn"),
	serverAssigned("aws_batch_job_queue",
		"AWS Batch mints the job queue's own ARN at create time; the job_queue_name argument is client-chosen but is not the import identity — the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_batch_scheduling_policy",
		"AWS Batch mints the scheduling policy's own ARN at create time; the name argument is client-chosen but is not the import identity.",
		"ARN", "arn", "id"),

	// aws_emr_cluster: the registry's own AWS::EMR::Cluster entry carries
	// handlers.create/read/update/delete/list all false (this fork's CFN
	// Cloud Control registry copy is a stub for this type), but this
	// fork's marker discovery does not depend on Cloud Control's own list
	// handler — live/survey-full.json's signal (taggable, "recoverable by
	// tag-filtered list") and the provider's own Import section
	// ("terraform import aws_emr_cluster.cluster j-123456ABCDEF") both
	// confirm the identity independently of the registry's handler gap.
	serverAssigned("aws_emr_cluster",
		"EMR mints the cluster's own id (j-…) at create time; none of the create-only arguments (Name, ReleaseLabel, ServiceRole, ...) reconstructs it.",
		"ID", "id"),
	// aws_emr_security_configuration: row-gen's own proposal, confirmed
	// against the provider's Import section (name argument, verbatim).
	TypeIdentity{
		Type:          "aws_emr_security_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	serverAssigned("aws_emr_studio",
		"EMR mints the studio's own id (es-…) at create time; none of the create-only arguments (AuthMode, ServiceRole, VpcId, ...) reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_emrcontainers_virtual_cluster",
		"EMRContainers mints the virtual cluster's own id at create time; the name and container_provider arguments configure it but do not identify it.",
		"ID", "id"),
	serverAssigned("aws_emrserverless_application",
		"EMRServerless mints the application's own id at create time; the name and type arguments configure it but do not identify it.",
		"ID", "id"),

	serverAssigned("aws_apprunner_auto_scaling_configuration_version",
		"App Runner mints the auto scaling configuration version's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_observability_configuration",
		"App Runner mints the observability configuration's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_service",
		"App Runner mints the service's own ARN at create time; the service_name argument is client-chosen but is not the import identity — the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_vpc_connector",
		"App Runner mints the VPC connector's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_vpc_ingress_connection",
		"App Runner mints the VPC ingress connection's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),

	// aws_elastic_beanstalk_application: row-gen's own proposal, confirmed
	// against the provider's Import section (name argument, verbatim).
	TypeIdentity{
		Type:          "aws_elastic_beanstalk_application",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_elastic_beanstalk_environment: reclassified from row-gen's
	// evidence-only guess ("environment_name", unconfident); see the
	// batch banner above.
	serverAssigned("aws_elastic_beanstalk_environment",
		"Elastic Beanstalk mints the environment's own id (e-…) at create time; the environment_name argument is client-chosen but is not the import identity — the registry's own schema does not even carry an EnvironmentId, but the provider's Import section documents import by the environment's own opaque id.",
		"ID", "id"),

	// aws_amplify_app: reclassified from row-gen's server-assigned "Arn"
	// proposal to the App ID the provider actually documents importing
	// by; see the batch banner above. IdentityAttrs names only "id", not
	// "arn" — the provider's own Attribute Reference lists them as two
	// distinct exported attributes.
	serverAssigned("aws_amplify_app",
		"Amplify mints the app's own id (App ID, e.g. d2ypk4k47z8u6) at create time; the name argument is client-chosen but is not the import identity — the provider's Import section documents import by the App ID, embedded as the trailing path segment of the app's ARN but a distinct exported attribute ('id') from 'arn' itself.",
		"ID", "id"),
	// aws_amplify_branch: row-gen classified this evidence-only (import
	// docs show an argument-composed id, which applyImportGrammarDemotions
	// catches for a server-assigned proposal). Independent verification
	// against the provider's Import section confirms the composite
	// directly: required app_id (the just-ratified aws_amplify_app's own
	// id) and branch_name, slash-separated (terraform import
	// aws_amplify_branch.master d2ypk4k47z8u6/master) — the same
	// hand-separator-from-docs move the Route53/CloudFront batch made
	// repeatedly.
	TypeIdentity{
		Type: "aws_amplify_branch",
		Components: []Component{
			attr("app_id"),
			sep("/"),
			attr("branch_name"),
		},
		ImportSyntax:  "APPID/BRANCHNAME",
		IdentityAttrs: nil,
	},

	// Lightsail: issue #65's recipe names this service as one "the sweep
	// mapped many" for — row-gen's own registry-derived argument guesses
	// (snake-cased CFN property names) are wrong for several of these
	// types, so every one below was independently checked against the
	// provider's own Argument Reference and Import section at v6.58.0
	// rather than pasted from row-gen's evidence-only guess.
	//
	// aws_lightsail_bucket, aws_lightsail_container_service,
	// aws_lightsail_distribution: row-gen's own proposals (import grammar
	// sourced "name" argument), confirmed against the provider's Import
	// sections verbatim.
	TypeIdentity{
		Type:          "aws_lightsail_bucket",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_lightsail_container_service",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_lightsail_distribution",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_instance: row-gen guessed "instance_name" (evidence-
	// only, unconfident). The provider's Argument Reference names the
	// required argument "name" ("Instance name, must be unique per
	// region"), matching the Import section's own "using their name".
	TypeIdentity{
		Type:          "aws_lightsail_instance",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_database: row-gen guessed "relational_database_name"
	// (evidence-only, unconfident) — the guess happens to be right this
	// time: the provider's Argument Reference confirms
	// relational_database_name as the required, ForceNew argument the
	// Import section's "using their name" refers to.
	TypeIdentity{
		Type:          "aws_lightsail_database",
		Components:    []Component{attr("relational_database_name")},
		ImportSyntax:  "RELATIONALDATABASENAME",
		IdentityAttrs: []string{"relational_database_name"},
	},
	// aws_lightsail_lb: row-gen guessed "load_balancer_name" (evidence-
	// only, unconfident). The provider's Argument Reference names the
	// required argument plain "name".
	TypeIdentity{
		Type:          "aws_lightsail_lb",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_certificate: row-gen guessed "certificate_name"
	// (evidence-only, unconfident). The provider's Argument Reference
	// names the required argument plain "name" ("Name of the
	// certificate"), matching the Import section's "using the certificate
	// name".
	TypeIdentity{
		Type:          "aws_lightsail_certificate",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_lb_certificate: row-gen classified this
	// needs-hand-separator (registry primaryIdentifier ["CertificateName",
	// "LoadBalancerName"], composite, no separator in any schema).
	// Independent verification against the provider's Argument Reference
	// (required: domain_name, lb_name, name — the certificate's own name
	// argument is plain "name", not "certificate_name") and Import section
	// supplies the separator: a comma, lb_name first (terraform import
	// aws_lightsail_lb_certificate.example
	// example-load-balancer,example-load-balancer-certificate) — both
	// components plain configuration arguments, no marker or tag
	// dependency either half.
	TypeIdentity{
		Type: "aws_lightsail_lb_certificate",
		Components: []Component{
			attr("lb_name"),
			sep(","),
			attr("name"),
		},
		ImportSyntax:  "LBNAME,CERTIFICATENAME",
		IdentityAttrs: nil,
	},
	// aws_lightsail_disk: row-gen guessed "disk_name" (evidence-only,
	// unconfident). The provider's Argument Reference names the required
	// argument plain "name", matching the Import section's "using the
	// name attribute".
	TypeIdentity{
		Type:          "aws_lightsail_disk",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_static_ip: row-gen guessed "static_ip_name" (evidence-
	// only, unconfident). The provider's Argument Reference names the
	// required argument plain "name", matching the Import section's
	// "using the name attribute".
	TypeIdentity{
		Type:          "aws_lightsail_static_ip",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
)

func init() { registerCohortTable(identityTableComputePlatforms) }
