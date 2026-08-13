// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableDatabases is the databases cohort's slice of [DefaultTable]:
// the identity rows the databases ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableDatabases = buildTable(
	// ---- Registry-ratified (#40, #44, #65): sixth batch, databases beyond
	// ---- RDS/DynamoDB/ElastiCache (issue #65's own recipe: Redshift,
	// ---- OpenSearch/OpenSearchServerless, Neptune, DocDB, Timestream,
	// ---- QLDB, MemoryDB, Cassandra/Keyspaces). Same tools/row-gen pipeline
	// ---- as the batches above, cross-checked against
	// ---- live/import-grammar.json's scraped Import sections — the pinned
	// ---- v6.58.0 provider docs, fetched directly rather than accepted on
	// ---- the CFN registry's classification alone. Several rows below
	// ---- correct a row-gen "evidence-only" demotion the same way the RDS
	// ---- and messaging batches corrected their own registry-undersold
	// ---- proposals; the per-type comments say which. Cohort estate:
	// ---- live/e2e/estates/databases.
	//
	// Per-service scope is deliberately narrower than "every row-gen
	// proposal in the service section", matching issue #65's own sub-lists:
	//
	//   - Redshift: clusters, parameter/subnet groups, snapshot schedules
	//     only. row-gen's other eight Redshift proposals/evidence rows
	//     (event subscription, scheduled action, integration,
	//     endpoint access/authorization, logging and iam_roles property
	//     children, snapshot, hsm client cert/config, usage limit, partner,
	//     idc application, the two data-share types, resource policy,
	//     authentication profile) are left for a future batch, not
	//     rejected.
	//   - RedshiftServerless: namespaces and workgroups only (both row-gen
	//     proposed cleanly). Its snapshot, endpoint access, usage limit,
	//     resource policy and custom-domain-association types are left out
	//     of scope.
	//   - RedshiftData: issue #65 says "if proposed" — it is not. row-gen's
	//     full run carries no "service: RedshiftData" section at all (see
	//     tools/row-gen's own output), confirming live/mapping.json has no
	//     aws_redshiftdata_* row mapped to any CloudFormation RedshiftData
	//     type. Nothing to ratify or reject here.
	//   - OpenSearch: domains only (both TF resources that import by a
	//     domain_name argument — see aws_elasticsearch_domain's own
	//     comment below for the former2 mapping story issue #65 asked to be
	//     checked first). aws_opensearch_domain_policy,
	//     aws_opensearch_domain_saml_options and
	//     aws_elasticsearch_domain_policy (all property-children, all
	//     evidence-only per row-gen) are left for a future batch once their
	//     parent domain types have a batch history to follow, the same
	//     restraint the OpenSearchServerless security_config and
	//     vpc_endpoint types below get.
	//   - OpenSearchServerless: collections and policies only, per issue
	//     #65's own words. aws_opensearchserverless_security_config (a
	//     SAML/IAM-Identity-Center configuration type, not a policy despite
	//     living beside them) and aws_opensearchserverless_vpc_endpoint
	//     (neither a collection nor a policy) are left out of scope even
	//     though both have the same clean server-assigned/composite
	//     evidence shape as the ratified rows beside them.
	//   - Neptune: "registry coverage is partial — only clean proposals"
	//     per issue #65 — the three row-gen proposed client-named rows
	//     (both parameter group flavors, the subnet group). Its cluster,
	//     cluster instance, event subscription and global cluster rows are
	//     all evidence-only per row-gen (GUESSED argument names); this
	//     batch does not hand-correct them even though
	//     live/import-grammar.json documents real Import sections for the
	//     cluster and cluster instance ("using the cluster identifier" /
	//     "the instance identifier", without a backtick-quoted argument
	//     name to confirm cluster_identifier / instance_identifier against)
	//     — left for a future batch prepared to hand-verify those two
	//     against the provider's Argument Reference directly. NeptuneGraph
	//     is a distinct CFN service issue #65 does not name; left alone
	//     entirely, the same restraint the devtools batch showed CodeGuru.
	//   - DocDB: "mostly registry-laggard — only what row-gen proposes with
	//     real evidence" per issue #65 — the one row-gen proposed
	//     client-named row (event subscription) plus DocDBElastic's one
	//     proposed server-assigned row (the only other DocDB-family type
	//     row-gen's own classification made a pastable row for). DocDB's
	//     other five rows (cluster, cluster instance, cluster parameter
	//     group, global cluster, subnet group) are all evidence-only per
	//     row-gen; live/import-grammar.json has real Import-section
	//     evidence for four of them (cluster_identifier, identifier, name,
	//     name respectively) that would resolve them the same way this
	//     batch resolves aws_redshift_subnet_group and aws_qldb_ledger
	//     below, but issue #65's own restraint on this service keeps them
	//     out of this batch rather than hand-corrected.
	//   - Timestream: all five row-gen sections types, all independently
	//     confirmed against live/import-grammar.json's real Import/Identity
	//     Schema evidence below.
	//   - QLDB: the one row-gen evidence-only row this batch corrects
	//     (aws_qldb_ledger) plus an explicit rejection of the other QLDB
	//     type, aws_qldb_stream — see its own comment below.
	//   - MemoryDB: all six row-gen client-named/server-assigned proposals
	//     plus one correction (aws_memorydb_subnet_group, whose row-gen
	//     GUESSED argument name does not survive the provider's own docs).
	//   - Cassandra: both types row-gen's registry evidence carries for
	//     the service — issue #65's own parenthetical ("the sweeps aliased
	//     keyspace/table") is live/mapping.json's own note that AWS
	//     Keyspaces for Apache Cassandra maps to CloudFormation's
	//     AWS::Cassandra::* types, which is why "Cassandra" is the
	//     row-gen service section name for aws_keyspaces_keyspace and
	//     aws_keyspaces_table rather than a literal Cassandra product.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_qldb_stream: registry primaryIdentifier is
	//     ["LedgerName", "Id"], and unlike every composite this table
	//     resolves, one half is not a configuration argument at all — Id is
	//     absent from create_only_properties (QLDB mints the stream's own
	//     id at create time) and CFN registry read_only_properties confirms
	//     it. No component set built from configuration, literals and cloud
	//     values reconstructs it, the same unrecoverable shape as
	//     aws_elasticache_global_replication_group's rejection in the
	//     DynamoDB/ElastiCache batch above — not a row-gen misclassification
	//     to correct, a genuine gap in what configuration alone can recover.

	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_redshift_cluster.myprodcluster tf-redshift-cluster-12345),
		// which uses the required "cluster_identifier" argument verbatim.
		Type:          "aws_redshift_cluster",
		Components:    []Component{attr("cluster_identifier")},
		ImportSyntax:  "CLUSTER_IDENTIFIER",
		IdentityAttrs: []string{"cluster_identifier"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_redshift_parameter_group.paramgroup1 parameter-group-test-terraform),
		// which uses the required "name" argument verbatim.
		Type:          "aws_redshift_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen left this evidence-only: CFN Registry
		// AWS::Redshift::ClusterSubnetGroup ships an empty
		// createOnlyProperties, so row-gen's own createOnlyProperties-backed
		// rule never fires (neither the client-named nor the server-assigned
		// template matches an empty set). The provider's own documented
		// Import section settles it independently:
		// live/import-grammar.json's scraped evidence (terraform import
		// aws_redshift_subnet_group.testgroup1 test-cluster-subnet-group)
		// says plainly "import Redshift subnet groups using the `name`" —
		// client-named, not evidence-only.
		Type:          "aws_redshift_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen left this evidence-only via the registry's opaque "Arn"
		// (primaryIdentifier ⊆ readOnlyProperties, the server-assigned
		// shape, but row-gen still filed it evidence-only rather than
		// proposed since the flat serverAssigned() template had no argument
		// evidence to offer either). The provider's own documented Import
		// section resolves it as client-named instead:
		// live/import-grammar.json's scraped evidence (terraform import
		// aws_redshift_snapshot_schedule.default tf-redshift-snapshot-schedule)
		// says "import Redshift Snapshot Schedule using the `identifier`" —
		// the same Optional+Computed name-generation idiom
		// aws_s3_bucket/aws_iam_role/aws_db_subnet_group already carry in
		// this table (the resource also accepts identifier_prefix), not an
		// opaque server-minted value.
		Type:          "aws_redshift_snapshot_schedule",
		Components:    []Component{attr("identifier")},
		ImportSyntax:  "IDENTIFIER",
		IdentityAttrs: []string{"identifier"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_redshiftserverless_namespace.example example), which uses the
		// required "namespace_name" argument verbatim.
		Type:          "aws_redshiftserverless_namespace",
		Components:    []Component{attr("namespace_name")},
		ImportSyntax:  "NAMESPACE_NAME",
		IdentityAttrs: []string{"namespace_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_redshiftserverless_workgroup.example example), which uses the
		// required "workgroup_name" argument verbatim.
		Type:          "aws_redshiftserverless_workgroup",
		Components:    []Component{attr("workgroup_name")},
		ImportSyntax:  "WORKGROUP_NAME",
		IdentityAttrs: []string{"workgroup_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_opensearch_domain.example domain_name), which uses the
		// required "domain_name" argument verbatim.
		Type:          "aws_opensearch_domain",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{"domain_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// The mapping story issue #65 asked to be checked before touching
		// anything Elasticsearch-named: live/mapping.json maps
		// aws_elasticsearch_domain to AWS::Elasticsearch::Domain, its own
		// real CFN type, not folded into AWS::OpenSearchService::Domain —
		// and tools/mapping-gen's own former2 comparison disagrees, mapping
		// this TF type to AWS::OpenSearchService::Domain instead. That
		// disagreement is a live, acknowledged contradiction
		// (mapping_gen_test.go's TestFormer2ContradictionsAcknowledged),
		// kept deliberately: "aws_elasticsearch_domain is TF's deprecated
		// pre-rename resource and its own docs still document the classic
		// AWS::Elasticsearch::Domain type; AWS::OpenSearchService::Domain
		// (former2's answer) is what aws_opensearch_domain, a distinct TF
		// resource, maps to." So this is not a duplicate of
		// aws_opensearch_domain above and not a wrong mapping to fix — it
		// is its own resource type with its own CFN evidence, which row-gen
		// left evidence-only (AWS::Elasticsearch::Domain's primaryIdentifier
		// "Id" is opaque and its createOnlyProperties carries only
		// "DomainName", not an argument name row-gen's classifier could
		// read as an import identity). The provider's own documented Import
		// section resolves it independently: live/import-grammar.json's
		// scraped evidence (terraform import
		// aws_elasticsearch_domain.example domain_name) says "import
		// Elasticsearch domains using the `domain_name`" — the same
		// client-named shape as aws_opensearch_domain above, confirmed by
		// its own separate CFN registry entry rather than borrowed from
		// OpenSearchService's.
		Type:          "aws_elasticsearch_domain",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{"domain_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_opensearchserverless_collection",
		"OpenSearchServerless mints the collection's own id at create time; the required \"name\" argument is a create-time input but the registry's primaryIdentifier is the separate, server-generated Id, confirmed by the provider's own Identity Schema (v1.12.0+ identity-block import), which requires exactly one field: id.",
		"ID", "id"),
	serverAssigned("aws_opensearchserverless_collection_group",
		"Same shape as aws_opensearchserverless_collection above: OpenSearchServerless mints the collection group's own id at create time, confirmed by the provider's own Identity Schema, which requires exactly one field: id.",
		"ID", "id"),
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["Type", "Name"], composite, no separator in
		// any schema). live/import-grammar.json's own scraped Import
		// section supplies both the separator and the order: a slash,
		// name first (terraform import
		// aws_opensearchserverless_access_policy.example example/data),
		// confirmed against the provider's own newer Identity Schema
		// (v1.12.0+, Required: name, type — "Type of access policy").
		Type: "aws_opensearchserverless_access_policy",
		Components: []Component{
			attr("name"),
			sep("/"),
			attr("type"),
		},
		ImportSyntax:  "NAME/TYPE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same shape as aws_opensearchserverless_access_policy above:
		// row-gen filed this needs-hand-separator on the same
		// ["Type", "Name"] primaryIdentifier composite.
		// live/import-grammar.json's scraped Import section confirms the
		// same slash separator and name-first order (terraform import
		// aws_opensearchserverless_lifecycle_policy.example
		// example/retention), and the provider's Identity Schema requires
		// the same two fields: name, type ("Type of lifecycle policy").
		Type: "aws_opensearchserverless_lifecycle_policy",
		Components: []Component{
			attr("name"),
			sep("/"),
			attr("type"),
		},
		ImportSyntax:  "NAME/TYPE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same shape again: row-gen filed this needs-hand-separator on the
		// same ["Type", "Name"] composite. live/import-grammar.json's
		// scraped Import section confirms the same slash separator and
		// name-first order (terraform import
		// aws_opensearchserverless_security_policy.example
		// example/encryption), and the provider's Identity Schema requires
		// name and type ("Type of security policy").
		Type: "aws_opensearchserverless_security_policy",
		Components: []Component{
			attr("name"),
			sep("/"),
			attr("type"),
		},
		ImportSyntax:  "NAME/TYPE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_neptune_cluster_parameter_group.cluster_pg production-pg-1),
		// which uses the required "name" argument verbatim.
		Type:          "aws_neptune_cluster_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_neptune_parameter_group.some_pg some-pg), which uses the
		// required "name" argument verbatim.
		Type:          "aws_neptune_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_neptune_subnet_group.default production-subnet-group), which
		// uses the required "name" argument verbatim.
		Type:          "aws_neptune_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_docdb_event_subscription.example event-sub), which uses the
		// required "name" argument verbatim.
		Type:          "aws_docdb_event_subscription",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_docdbelastic_cluster",
		"DocDBElastic mints the cluster's own ARN at create time; the required \"cluster_name\", \"admin_user_name\" and \"auth_type\" arguments do not reconstruct it. The provider's own Identity Schema (v1.12.0+ identity-block import) requires exactly one field, arn — a real, non-templated correction of row-gen's own registry-opaque \"Id\" reason, which named the same value but not by its provider attribute name.",
		"arn:aws:docdb-elastic:REGION:ACCOUNT:cluster/CLUSTERID", "arn"),
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_timestreamwrite_database.example example), which uses the
		// required "database_name" argument verbatim.
		Type:          "aws_timestreamwrite_database",
		Components:    []Component{attr("database_name")},
		ImportSyntax:  "DATABASE_NAME",
		IdentityAttrs: []string{"database_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["DatabaseName", "TableName"], composite, no
		// separator in any schema). live/import-grammar.json's own scraped
		// Import section supplies both the separator and the order — a
		// colon, table first (terraform import
		// aws_timestreamwrite_table.example ExampleTable:ExampleDatabase),
		// confirmed against the provider's own documented text ("import
		// Timestream tables using the `table_name` and `database_name`
		// separate by a colon").
		Type: "aws_timestreamwrite_table",
		Components: []Component{
			attr("table_name"),
			sep(":"),
			attr("database_name"),
		},
		ImportSyntax:  "TABLE_NAME:DATABASE_NAME",
		IdentityAttrs: nil,
	},
	serverAssigned("aws_timestreaminfluxdb_db_cluster",
		"Timestream for InfluxDB mints the cluster's own id at create time (e.g. \"hzfuy146ke\"); the required \"name\", \"username\", \"password\", \"organization\" and \"bucket\" arguments do not reconstruct it. The provider's own Identity Schema (v1.12.0+ identity-block import) requires exactly one field, id — a real, non-templated correction of row-gen's registry-opaque \"Id\" reason.",
		"ID", "id"),
	serverAssigned("aws_timestreaminfluxdb_db_instance",
		"Same shape as aws_timestreaminfluxdb_db_cluster above: Timestream for InfluxDB mints the instance's own id at create time (e.g. \"0oo7rzble5\"), confirmed by the provider's own Identity Schema, which requires exactly one field: id.",
		"ID", "id"),
	serverAssigned("aws_timestreamquery_scheduled_query",
		"TimestreamQuery mints the scheduled query's own ARN at create time, embedding a random suffix the required \"name\", \"query_string\" and other arguments do not reconstruct (the provider's documented import example, arn:aws:timestream:...:scheduled-query/tf-acc-test-7774188528604787105-e13659544fe66c8d, is not a plain name-derived ARN the way aws_sns_topic's or aws_codepipeline_webhook's are). The provider's own documented Import section confirms import by \"the `arn`\" alone.",
		"arn:aws:timestream:REGION:ACCOUNT:scheduled-query/QUERYID", "arn"),
	TypeIdentity{
		// row-gen left this evidence-only (registry primaryIdentifier "Id"
		// ⊆ readOnlyProperties, opaque). The provider's own documented
		// Import section resolves it as client-named instead:
		// live/import-grammar.json's scraped evidence (terraform import
		// aws_qldb_ledger.sample-ledger sample-ledger) says plainly "import
		// QLDB Ledgers using the `name`" — the same correction shape as
		// aws_redshift_subnet_group and aws_elasticsearch_domain above.
		Type:          "aws_qldb_ledger",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_memorydb_acl.example my-acl), which uses the required "name"
		// argument verbatim.
		Type:          "aws_memorydb_acl",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_memorydb_cluster.example my-cluster), which uses the required
		// "name" argument verbatim.
		Type:          "aws_memorydb_cluster",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_memorydb_multi_region_cluster",
		"row-gen proposed server-assigned via the registry's opaque \"MultiRegionClusterName\" primaryIdentifier, and the provider's own documented Import section names the same attribute (\"import a cluster using the `multi_region_cluster_name`\") — but unlike the other MemoryDB rows in this batch, that value is not a create-time argument: CFN registry createOnlyProperties carries only \"MultiRegionClusterNameSuffix\", not the full name, and the provider's own example (multi_region_cluster_name \"virxk-example\" from a configured suffix \"example\") confirms MemoryDB mints a random prefix onto the client-chosen suffix. No configuration argument reconstructs the full name alone.",
		"MULTI_REGION_CLUSTER_NAME", "multi_region_cluster_name"),
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_memorydb_parameter_group.example my-parameter-group), which
		// uses the required "name" argument verbatim.
		Type:          "aws_memorydb_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_memorydb_user.example my-user), which uses the required
		// "user_name" argument verbatim.
		Type:          "aws_memorydb_user",
		Components:    []Component{attr("user_name")},
		ImportSyntax:  "USER_NAME",
		IdentityAttrs: []string{"user_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen left this evidence-only with a GUESSED argument name
		// ("subnet_group_name", the snake-cased CFN property
		// SubnetGroupName) that the provider's own documented Import
		// section does not confirm: live/import-grammar.json's scraped
		// evidence (terraform import aws_memorydb_subnet_group.example
		// my-subnet-group) says plainly "import a subnet group using its
		// `name`" — the real argument is "name", the same shape as every
		// other MemoryDB row in this batch, not the CFN-property-derived
		// guess.
		Type:          "aws_memorydb_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_keyspaces_keyspace.example my_keyspace), which uses the
		// required "name" argument verbatim.
		Type:          "aws_keyspaces_keyspace",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["KeyspaceName", "TableName"], composite, no
		// separator in any schema). live/import-grammar.json's own scraped
		// Import section supplies both the separator and the order — a
		// slash, keyspace first (terraform import
		// aws_keyspaces_table.example my_keyspace/my_table), confirmed
		// against the provider's own documented text ("import a table
		// using the `keyspace_name` and `table_name` separated by `/`").
		Type: "aws_keyspaces_table",
		Components: []Component{
			attr("keyspace_name"),
			sep("/"),
			attr("table_name"),
		},
		ImportSyntax:  "KEYSPACE_NAME/TABLE_NAME",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableDatabases) }
