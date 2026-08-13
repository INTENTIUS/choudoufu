// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableEcsEks is the ecs-eks cohort's slice of [DefaultTable]:
// the identity rows the ecs-eks ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableEcsEks = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, ECS and EKS
	// ---- (issue #65) ------------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own
	// website/docs/r/ source at the pinned v6.58.0 tag), not accepted on the
	// registry's classification alone. Six of row-gen's nine EKS proposals
	// were "needs hand separator" (a composite primaryIdentifier with no
	// separator in any schema, issue #44's own non-goal); this batch resolved
	// five of those six by hand from the provider's own documented import
	// grammar rather than the registry's (live/import-grammar.json, issue
	// #65's own note that this artifact "largely resolves" the needs-hand-
	// separator backlog). Cohort estate: live/e2e/estates/ecs-eks.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_ecs_capacity_provider: row-gen proposed client-named via the
	//     registry's createOnlyProperties "Name" — the same
	//     registry-says-client-named-but-the-provider-disagrees shape the
	//     earlier batches' rejections established. The provider's own
	//     identity schema requires the server-assigned arn
	//     (arn:aws:ecs:REGION:ACCOUNT:capacity-provider/NAME), not name, and
	//     its documented import command confirms it. Even granting
	//     server-assigned status, v6.58.0 ships this type with no native
	//     list resource (live/survey-full.json:
	//     aws_ecs_capacity_provider.signals.list_resource is false), the
	//     same gap that keeps aws_efs_file_system out of the marker cohort
	//     above: a tag-filtered list needs something to list.
	//   - aws_ecs_daemon_task_definition: the same family+server-assigned-
	//     revision shape as aws_ecs_task_definition below, one section
	//     later in the provider's own docs (ECS's new daemon-scheduling
	//     sibling of the ordinary task definition). Rejected for the same
	//     reason: see aws_ecs_task_definition's own entry.
	//   - aws_ecs_express_gateway_service: v6.58.0 ships this type with no
	//     identity schema at all (no "Identity Schema" heading in its own
	//     doc, unlike every other type in this section), its service_name
	//     argument is Optional and Terraform-generated when omitted, and
	//     row-gen's own enumeration story calls it flatly "not listable" —
	//     three independent reasons, any one of which alone would keep a
	//     type out of the four admission paths, and here all three hold at
	//     once.
	//   - aws_ecs_service: live/SURVEY.md's curated-68 row calls this type
	//     client-named ("cluster + name, the cluster itself client-named"),
	//     and its provider identity schema does require exactly those two
	//     names. But the resource's own Argument Reference documents
	//     `cluster` as "(Optional) ARN of an ECS cluster" — accepting an
	//     ARN — while the identity schema's `cluster` field is documented
	//     as "The name of the cluster": the same argument name, two
	//     different shapes. The type's own Example Usage sets
	//     `cluster = aws_ecs_cluster.foo.id`, and this table's own
	//     aws_ecs_cluster entry below records that id is the cluster's ARN,
	//     not its name — the idiomatic form of this exact argument would
	//     silently build a wrong identity (an ARN where the import grammar
	//     wants a bare name) rather than fail visibly. A hand composite that
	//     cannot tell which shape a given configuration used is a guess,
	//     not evidence; this needs a config-signal check (an argument that
	//     names the cluster by aws_ecs_cluster.foo.name specifically) this
	//     batch does not attempt, the same non-goal boundary the messaging
	//     batch's aws_cloudwatch_event_rule rejection drew.
	//   - aws_ecs_task_definition: SURVEY.md's own curated-68 row records
	//     this type's shape as "family + revision, the revision assigned
	//     server-side per registration" and groups it among the five rows
	//     its wrinkles section admits neither derivation nor a marker
	//     recovers. The ARN embeds family:revision, and revision is not a
	//     configuration argument at all — the Attribute Reference exports it
	//     read-only, incrementing by one on every new registration of the
	//     same family. That rules out client-naming (revision is never in
	//     configuration) and, less obviously, rules out the marker path
	//     too: every revision of one family is a distinct live object, but
	//     ECS does not vary a task definition's tags by revision, so a
	//     tag-filtered list would return every revision under one identical
	//     tag set with nothing left to tell them apart — the marker path's
	//     one-live-object-per-tag-set assumption, sound for every admitted
	//     marker type above, breaks here. A shape outside the four admission
	//     paths, honestly; rejected rather than forced into either one.
	//   - aws_ecs_task_set: row-gen's own needs-hand-separator note points at
	//     a three-part primaryIdentifier (Cluster, Service, Id), and the
	//     provider's own Import section confirms the shape:
	//     ecs-svc/DEPLOYMENTID,SERVICEARN,CLUSTERARN. The comma separator is
	//     no longer the obstacle live/import-grammar.json resolves for the
	//     five EKS composites below — the DEPLOYMENTID segment is, since it
	//     is server-assigned with no configuration argument or previously
	//     admitted parent's identity attribute that supplies it (unlike
	//     aws_route53_record's zone_id, which comes from an already-resolved
	//     parent). Compounding it, both `cluster` and `service` are
	//     documented as "Short name or ARN" — the same argument-accepts-
	//     either-shape ambiguity that rejected aws_ecs_service above, twice
	//     over in one type.
	//   - aws_eks_identity_provider_config: row-gen's needs-hand-separator
	//     note and live/import-grammar.json's own separator (":") both
	//     resolve cleanly — cluster_name and identity_provider_config_name,
	//     colon-joined, the same shape as aws_eks_addon below. But
	//     identity_provider_config_name is not a top-level argument of this
	//     resource: the provider's Argument Reference nests it inside the
	//     required `oidc` block (oidc.identity_provider_config_name), and
	//     every Component this table has ever built - every attr() call in
	//     this file - reads a top-level resource argument
	//     ([resolver.identityArgs] builds its schema from top-level
	//     hcl.AttributeSchema entries only). This table's vocabulary cannot
	//     honestly express an identity sourced from inside a nested block
	//     without inventing that capability, which is a mechanism change,
	//     not a ratification; rejected rather than forced.
	//   - aws_eks_pod_identity_association: the identity requires
	//     cluster_name (a required argument) plus association_id, which is
	//     not a configuration argument at all - the provider mints it and
	//     the Attribute Reference documents it as "The ID of the
	//     association", read-only. Server-assigned, so this needs the
	//     marker path; the type is taggable, but v6.58.0 ships it with no
	//     native list resource (live/survey-full.json:
	//     aws_eks_pod_identity_association.signals.list_resource is false),
	//     the same aws_efs_file_system gap that keeps
	//     aws_ecs_capacity_provider out above: nothing enumerates it.
	//
	// A note on floci, not on any of the rejections above: EKS cluster
	// creation is unsupported by the pinned floci image (lex00/floci#27,
	// still open), so nothing in this cohort that names a cluster_name
	// argument could be apply-verified against the emulator this batch ran
	// - see live/e2e/estates/ecs-eks/README.md, "Verifying by hand". Per
	// issue #65's own recipe ("apply against the pinned floci image where it
	// serves the types, gaps documented in the cohort README, not
	// blocking"), that gap is documented rather than treated as a reason to
	// leave aws_eks_cluster and its five EKS dependents unratified - the
	// same standard the messaging batch applied to aws_sqs_queue's own open
	// floci gap.

	serverAssigned("aws_ecs_daemon",
		"ECS mints the daemon's ARN at create time; the name argument is client-chosen but the documented import identity is the ARN (arn:aws:ecs:REGION:ACCOUNT:daemon/CLUSTER/NAME), which also embeds the cluster and region rather than reducing to a bare name.",
		"arn:aws:ecs:REGION:ACCOUNT:daemon/CLUSTER/NAME", "arn"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[Cluster], in
		// createOnlyProperties and not in readOnlyProperties — client-named,
		// and row-gen proposed it correctly. Confirmed against the
		// provider's documented import command (terraform import
		// aws_ecs_cluster_capacity_providers.example my-cluster) and its own
		// Attribute Reference ("id - Same as cluster_name"). No "Identity
		// Schema" heading in the provider's own doc at all — v6.58.0 ships
		// this type with no identity schema, the same docs-tier evidence
		// aws_ecs_cluster's own entry above rests on — and no tags argument
		// either: a named singleton child of the cluster, the same shape as
		// aws_s3_bucket_policy, concrete whenever the cluster is.
		Type:          "aws_ecs_cluster_capacity_providers",
		Components:    []Component{attr("cluster_name")},
		ImportSyntax:  "CLUSTER_NAME",
		IdentityAttrs: []string{"id", "cluster_name"},
	},

	TypeIdentity{
		// live/import-grammar.json: separator ":", arguments
		// [cluster_name, principal_arn], both required. Confirmed against
		// the provider's own Identity Schema (required: cluster_name,
		// principal_arn) and its documented import command
		// (example-cluster:arn:aws:iam::123456789012:role/example). Neither
		// argument is optional, so this is concrete in any realistic
		// config, the same iam_role_policy-style composite as
		// aws_iam_role_policy_attachment. The Attribute Reference exports
		// access_entry_arn, created_at and modified_at — no "id" at all —
		// so no attribute is claimed as an identity source.
		Type: "aws_eks_access_entry",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("principal_arn"),
		},
		ImportSyntax:  "CLUSTERNAME:PRINCIPALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// live/import-grammar.json flags this row's separator "unsure", but
		// the provider's own doc names it plainly: cluster_name,
		// principal_arn and policy_arn, octothorp-joined
		// (example-cluster#arn:...#arn:...), all three required arguments
		// per the Identity Schema. Untaggable — the Argument Reference
		// carries no tags block at all — so this joins
		// untaggableAdmittedTypes in internal/live/stamp alongside
		// aws_lb_target_group_attachment, the same shape: a composite of
		// three client-supplied values with no marker to fall back to.
		Type: "aws_eks_access_policy_association",
		Components: []Component{
			attr("cluster_name"),
			sep("#"),
			attr("principal_arn"),
			sep("#"),
			attr("policy_arn"),
		},
		ImportSyntax:  "CLUSTERNAME#PRINCIPALARN#POLICYARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// live/import-grammar.json: separator ":", arguments
		// [addon_name, cluster_name], both required per the Identity
		// Schema. The Attribute Reference documents id explicitly: "EKS
		// Cluster name and EKS Addon name separated by a colon (:)" —
		// cluster_name first, matching the documented import command
		// (example-cluster:example-addon) and this entry's own component
		// order.
		Type: "aws_eks_addon",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("addon_name"),
		},
		ImportSyntax:  "CLUSTERNAME:ADDONNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// live/import-grammar.json: separator ",", arguments
		// [capability_name, cluster_name], both required per the Identity
		// Schema (cluster_name, capability_name) and the documented import
		// command (example-cluster,example-capability). A newer EKS
		// resource (GitOps capabilities: ArgoCD, ACK, KRO) outside
		// live/SURVEY.md's curated 68, the same standing as
		// aws_lambda_layer_version and aws_cloudwatch_dashboard before it.
		// The Attribute Reference exports arn, configuration.*, tags_all
		// and version — no "id" — so no attribute is claimed as an
		// identity source.
		Type: "aws_eks_capability",
		Components: []Component{
			attr("cluster_name"),
			sep(","),
			attr("capability_name"),
		},
		ImportSyntax:  "CLUSTERNAME,CAPABILITYNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, proposed correctly.
		// Confirmed against the provider's own Identity Schema (required:
		// name) and its Attribute Reference ("id - Name of the cluster").
		// live/SURVEY.md's curated-68 row already reaches "client-named";
		// its "blocked-emulator" status is a floci gap (EKS cluster
		// creation, lex00/floci#27, still open), not an identity gap — see
		// the floci note above this section.
		Type:          "aws_eks_cluster",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// live/import-grammar.json: separator ":", arguments
		// [cluster_name, fargate_profile_name], both required per the
		// Identity Schema. The Attribute Reference documents id explicitly:
		// "EKS Cluster name and EKS Fargate Profile name separated by a
		// colon (:)", matching the documented import command
		// (example-cluster:example-profile).
		Type: "aws_eks_fargate_profile",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("fargate_profile_name"),
		},
		ImportSyntax:  "CLUSTERNAME:FARGATEPROFILENAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// live/survey-full.json classes this needs-config-signal, not
		// schema-provable: node_group_name is Optional in the resource
		// ("If omitted, Terraform will assign a random, unique name.
		// Conflicts with node_group_name_prefix.") the same
		// Optional+Computed name-generation idiom
		// admissionEvidenceExceptions already carries for aws_s3_bucket,
		// aws_iam_role and aws_iam_instance_profile, extended here to a
		// composite's second half rather than a lone argument, the same way
		// aws_iam_role_policy's own exception already does. Its
		// live/SURVEY.md curated-68 row classes it client-named by hand on
		// that same judgment ("cluster_name + node_group_name"), which this
		// entry follows; a config that sets only node_group_name_prefix (or
		// neither) resolves to ClassNeedsDiscovery honestly at the
		// per-instance level rather than failing this table's own
		// admission. live/import-grammar.json: separator ":", confirmed
		// against the provider's Attribute Reference ("id - EKS Cluster
		// name and EKS Node Group name separated by a colon (:)") and its
		// documented import command (example-cluster:example-group). Status
		// "blocked-emulator" in SURVEY.md is the same open EKS-cluster-
		// creation floci gap as aws_eks_cluster's own entry, not an
		// identity gap.
		Type: "aws_eks_node_group",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("node_group_name"),
		},
		ImportSyntax:  "CLUSTERNAME:NODEGROUPNAME",
		IdentityAttrs: []string{"id"},
	},
)

func init() { registerCohortTable(identityTableEcsEks) }
