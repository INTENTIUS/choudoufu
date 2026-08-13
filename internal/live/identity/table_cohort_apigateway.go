// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableApigateway is the apigateway cohort's slice of [DefaultTable]:
// the identity rows the apigateway ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableApigateway = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, API Gateway v1 and v2
	// ---- (issue #65) -----------------------------------------------------
	//
	// Same pipeline as the earlier three batches: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour. Two extensions beyond
	// what row-gen itself proposes were needed for this service, because
	// row-gen's own rule refuses any primaryIdentifier with more than one
	// part ("needs hand separator", issue #44 non-goal 3):
	//
	//   - live/import-grammar.json (tools/importdocs-gen, scraped from the
	//     pinned v6.58.0 provider docs) supplies the documented separator and
	//     the argument names the provider's own Import section names, for
	//     every composite ratified below.
	//   - Knowing the separator is not enough by itself: several of API
	//     Gateway's composite import IDs name a segment that is the child
	//     resource's own server-minted id (an AuthorizerId, a DeploymentId, a
	//     DocumentationPartId, a RequestValidatorId, the ResourceId the
	//     provider's own identity schema calls "id") rather than a
	//     configuration argument. Nothing in configuration can supply that
	//     segment, so a Components row for it would be a guess dressed up as
	//     a separator, which is exactly what issue #44 non-goal 3 forbids.
	//     Each such type was checked against the provider's Argument
	//     Reference — and, where available, live/survey-full.json's identity
	//     schema or the resource's own source for where SetId points — before
	//     landing either in the rejections below or, where every segment
	//     really is a configuration argument, in the ratified rows.
	//
	// 25 ApiGateway and 13 ApiGatewayV2 types were in row-gen's scope; 16 and
	// 5 respectively ratify here, the rest rejected (a composite needs a
	// server-minted segment) or deferred (a method/response property-child
	// per live/mapping.json's fold — see below). Cohort estate:
	// live/e2e/estates/apigateway; see that cohort's README for the full
	// floci verification this comment's floci notes summarize.
	//
	// Rejected, and deliberately absent from this table — every one because
	// the documented composite import ID names a segment that is the child's
	// own server-minted id, confirmed against the provider's Argument
	// Reference (v6.58.0) rather than against row-gen's registry evidence
	// alone:
	//
	//   - aws_api_gateway_authorizer: REST-API-ID/AUTHORIZER-ID. The
	//     resource's only arguments are name and rest_api_id; AuthorizerId is
	//     minted by the provider and appears nowhere in configuration.
	//   - aws_api_gateway_deployment: REST-API-ID/DEPLOYMENT-ID. The
	//     resource's only required argument is rest_api_id; DeploymentId is
	//     minted at create time.
	//   - aws_api_gateway_documentation_part: REST-API-ID/DOC-PART-ID. The
	//     resource's arguments are location, properties and rest_api_id;
	//     DocumentationPartId is minted at create time.
	//   - aws_api_gateway_request_validator: REST-API-ID/REQUEST-VALIDATOR-ID.
	//     The resource's arguments are name and rest_api_id; the validator's
	//     id is a value the provider assigns independently of name.
	//   - aws_api_gateway_resource: REST-API-ID/RESOURCE-ID.
	//     live/survey-full.json's identity schema names the requirement
	//     directly: required_for_import=[id, rest_api_id] — the resource's
	//     own id, not an argument.
	//   - aws_apigatewayv2_api_mapping: API-MAPPING-ID/DOMAIN-NAME. The
	//     resource's arguments are api_id, domain_name, stage and the
	//     optional api_mapping_key; ApiMappingId is minted at create time and
	//     is not api_mapping_key's value.
	//   - aws_apigatewayv2_authorizer: API-ID/AUTHORIZER-ID. The resource's
	//     arguments are api_id, authorizer_type and name; AuthorizerId is
	//     minted at create time.
	//   - aws_apigatewayv2_deployment: API-ID/DEPLOYMENT-ID. The resource's
	//     only required argument is api_id; DeploymentId is minted at create
	//     time.
	//   - aws_apigatewayv2_integration: API-ID/INTEGRATION-ID. The resource's
	//     required arguments are api_id and integration_type; IntegrationId
	//     is minted at create time.
	//   - aws_apigatewayv2_integration_response: API-ID/INTEGRATION-ID/
	//     INTEGRATION-RESPONSE-ID. Confirmed against the provider's own
	//     source (internal/service/apigatewayv2/integration_response.go):
	//     resourceIntegrationResponseCreate calls
	//     d.SetId(aws.ToString(output.IntegrationResponseId)), a value the
	//     API mints independently of the integration_response_key argument.
	//   - aws_apigatewayv2_model: API-ID/MODEL-ID. Confirmed against the
	//     provider's source (internal/service/apigatewayv2/model.go):
	//     resourceModelCreate calls d.SetId(aws.ToString(output.ModelId)), a
	//     value the API mints independently of the name argument.
	//   - aws_apigatewayv2_route: API-ID/ROUTE-ID. live/survey-full.json's
	//     identity schema names the requirement directly:
	//     required_for_import=[api_id, id] — the route's own id, not an
	//     argument route_key resolves to.
	//   - aws_apigatewayv2_route_response: API-ID/ROUTE-ID/
	//     ROUTE-RESPONSE-ID. Confirmed against the provider's source
	//     (internal/service/apigatewayv2/route_response.go):
	//     resourceRouteResponseCreate calls
	//     d.SetId(aws.ToString(output.RouteResponseId)), a value the API
	//     mints independently of the route_response_key argument.
	//
	// Deferred as method/response property-children, per live/mapping.json's
	// fold (row-gen's own output marks each
	// "(property-child of AWS::ApiGateway::Method)" or "...Stage", "no
	// pastable row" — no independent cfn_type of its own), not for any
	// identity weakness: each one's identity is in fact fully composable from
	// real configuration arguments alone. aws_api_gateway_method,
	// _integration, _integration_response and _method_response all require
	// exactly rest_api_id, resource_id and http_method (the latter two also
	// status_code), confirmed against live/survey-full.json's identity
	// schema — none of the four is the type's own server-minted id, unlike
	// the rejections above. aws_api_gateway_method_settings's identity
	// (rest_api_id, stage_name, method_path) is confirmed the same way
	// against live/import-grammar.json's scraped Import section instead,
	// that type predating the provider's identity-schema mechanism.
	// aws_api_gateway_method itself is not a fold (it is its own CFN
	// resource, AWS::ApiGateway::Method, merely with a composite
	// primaryIdentifier) and ratifies below; its three literal
	// property-children do not, because admitting a property-child needs a
	// parent-derived admission mechanism this table does not have yet — the
	// same gap aws_prometheus_alert_manager_definition and its APS siblings
	// are waiting on upstream. A future batch that builds that mechanism can
	// pick these three straight up; the identity work is already done here:
	//
	//   - aws_api_gateway_integration, aws_api_gateway_integration_response,
	//     aws_api_gateway_method_response (fold into AWS::ApiGateway::Method)
	//   - aws_api_gateway_method_settings (fold into AWS::ApiGateway::Stage)

	serverAssigned("aws_api_gateway_account",
		"the account settings resource is a singleton per AWS account: its identity is the caller's own AWS account ID, which pre-exists the resource and is never supplied by a configuration argument — the resource's only argument, cloudwatch_role_arn, does not identify it. Confirmed against the provider's own source (internal/service/apigateway/account.go): the Create method sets the id to r.Meta().AccountID(ctx), not to anything the configuration names.",
		"ACCOUNT_ID", "id"),
	serverAssigned("aws_api_gateway_api_key",
		"API Gateway mints the key's own id at create time; name is client-chosen but is not unique and is not what the provider imports by. Ratified despite a floci gap: reading an existing key back crashes the provider (a nil-pointer panic in resourceAPIKeyRead) rather than erroring gracefully — see live/e2e/estates/apigateway/README.md.",
		"APIKEYID", "id"),
	serverAssigned("aws_api_gateway_client_certificate",
		"API Gateway mints the client certificate's id at create time; every argument (description, region, tags) is optional and none of them identifies it. floci returns 406 for GenerateClientCertificate — not implemented, not evidence against the identity.",
		"CLIENTCERTIFICATEID", "id"),
	serverAssigned("aws_api_gateway_domain_name_access_association",
		"API Gateway mints the association's own ARN at create time; confirmed against the provider's own Identity Schema (required: arn) — the row-gen proposal here already matched the provider's documented behaviour with no correction needed.",
		"ARN", "arn", "id"),
	serverAssigned("aws_api_gateway_rest_api",
		"API Gateway mints the REST API's id at create time; name is client-chosen but is not unique and is not what the provider imports by. Re-verified against the pinned floci image (issue #65): the old blocked-emulator note undersold it — CreateRestApi and GetRestApi both work, and a terraform import against a floci-created REST API round-trips cleanly (confirmed by hand), but the provider's post-create availability waiter still spins forever because floci's DescribeRestApi never reports the AVAILABLE status the waiter polls for. That is a create-path gap, not a read/import gap, and this table only needs the latter — see live/e2e/estates/apigateway/README.md.",
		"RESTAPIID", "id"),
	serverAssigned("aws_api_gateway_usage_plan",
		"API Gateway mints the usage plan's id at create time; name is client-chosen but is not what the provider imports by. floci's own GetUsagePlan is broken (routes to a stray S3 NoSuchBucket error instead of the plan), so a terraform-managed usage plan cannot be read back against this emulator at all — a floci gap, not evidence against the identity; see live/e2e/estates/apigateway/README.md.",
		"ID", "id"),
	serverAssigned("aws_api_gateway_vpc_link",
		"API Gateway mints the VPC link's id at create time; name is client-chosen but is not what the provider imports by, the same shape as aws_lb above. floci returns 406 for CreateVpcLink — not implemented, not evidence against the identity.",
		"VPCLINKID", "id"),
	serverAssigned("aws_apigatewayv2_api",
		"API Gateway mints the v2 API's id at create time; confirmed against the provider's own Identity Schema (required: id) — the row-gen proposal here already matched the provider's documented behaviour. Confirmed against floci by hand: create, get and a terraform import all round-trip cleanly.",
		"APIID", "id"),
	serverAssigned("aws_apigatewayv2_routing_rule",
		"API Gateway mints the routing rule's own ARN at create time; the provider's docs list routing_rule_arn only in the Attribute Reference, not the Argument Reference, confirming it is computed rather than configurable. Untested against floci: creating one needs a working aws_apigatewayv2_domain_name first, and floci's CreateDomainName itself misroutes (see that type's note below) — the identity is sound regardless, being a property of the provider, not the emulator.",
		"ROUTINGRULEARN", "arn", "id"),
	serverAssigned("aws_apigatewayv2_vpc_link",
		"API Gateway mints the v2 VPC link's id at create time; name is client-chosen but is not what the provider imports by. Confirmed working against floci by hand (CreateVpcLink succeeds, unlike its v1 counterpart above).",
		"VPCLINKID", "id"),

	TypeIdentity{
		// row-gen marked this evidence-only because the registry's own
		// primaryIdentifier=[DomainName] argument name was GUESSED (not
		// backed by a provider identity schema or the carve seed) — not
		// because the identity itself is in doubt. Confirmed directly
		// against the provider's Argument Reference: domain_name is the
		// resource's sole required argument, and the documented import
		// command (terraform import aws_api_gateway_domain_name.example
		// dev.example.com) uses that same value verbatim. floci misroutes
		// CreateDomainName (a stray S3-shaped 400), so this type is untested
		// end-to-end against the pinned image; see
		// live/e2e/estates/apigateway/README.md.
		Type:          "aws_api_gateway_domain_name",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{"domain_name"},
	},
	TypeIdentity{
		// Same correction as aws_api_gateway_domain_name just above, and the
		// same floci gap (CreateDomainName misroutes) for the same reason —
		// domain_name_configuration's own certificate_arn dependency is not
		// what blocks it here, the emulator's routing is.
		Type:          "aws_apigatewayv2_domain_name",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{"domain_name"},
	},
	TypeIdentity{
		// A named-singleton-child of the REST API, the same shape as
		// aws_s3_bucket_policy and aws_sns_topic_policy above: at most one
		// per REST API, imported by the API's own id, which this resource's
		// sole reference argument, rest_api_id, already carries. row-gen
		// marked this "(property-child of AWS::ApiGateway::RestApi)
		// [evidence-only]" the same as the four deferred property-children
		// above, but unlike those this one needs no separator decision at
		// all — a single component, not a composite — so it ratifies here on
		// the same standard the earlier batches' singleton-child policies
		// used. Confirmed against the provider's Argument Reference (policy,
		// rest_api_id) and the documented import command (terraform import
		// aws_api_gateway_rest_api_policy.example 12345abcde).
		Type:          "aws_api_gateway_rest_api_policy",
		Components:    []Component{attr("rest_api_id")},
		ImportSyntax:  "REST_API_ID",
		IdentityAttrs: []string{"id", "rest_api_id"},
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: rest_api_id
		// and name are both required arguments, and the documented import
		// command (terraform import aws_api_gateway_model.example
		// 12345abcde/example) joins them REST-API-ID/NAME. Confirmed against
		// floci by hand: create, get and a terraform import all round-trip
		// cleanly with zero plan diff.
		Type: "aws_api_gateway_model",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("name"),
		},
		ImportSyntax:  "REST-API-ID/NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: rest_api_id
		// and stage_name are both required arguments (deployment_id is a
		// third, unrelated to identity), and the documented import command
		// (terraform import aws_api_gateway_stage.example 12345abcde/example)
		// joins them REST-API-ID/STAGE-NAME. Confirmed against floci by
		// hand: create, get and a terraform import all round-trip cleanly
		// with zero plan diff — note the stage's own id attribute is an
		// unrelated internal "ags-..." value, which is why it is not listed
		// as an identity source here, the same standard of care aws_route's
		// synthesized id gets.
		Type: "aws_api_gateway_stage",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("stage_name"),
		},
		ImportSyntax:  "REST-API-ID/STAGE-NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: rest_api_id
		// and version are both required arguments, and the documented import
		// command (terraform import aws_api_gateway_documentation_version.example
		// 5i4e1ko720/example-version) joins them REST-API-ID/VERSION. floci
		// returns 406 for CreateDocumentationVersion — not implemented, so
		// this type is untested end-to-end against the pinned image; see
		// live/e2e/estates/apigateway/README.md.
		Type: "aws_api_gateway_documentation_version",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("version"),
		},
		ImportSyntax:  "REST-API-ID/VERSION",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen marked this evidence-only because the registry's own
		// primaryIdentifier=[Id] is opaque and read-only — the same
		// registry-says-server-assigned-but-the-provider-disagrees shape as
		// aws_sns_topic_policy above. The provider's real, documented import
		// command (terraform import aws_api_gateway_gateway_response.example
		// 12345abcde/UNAUTHORIZED) is REST-API-ID/RESPONSE-TYPE, both of
		// which are the resource's own required arguments (rest_api_id,
		// response_type). floci's PutGatewayResponse misroutes (a stray
		// S3-shaped 404), so this type is untested end-to-end against the
		// pinned image; see live/e2e/estates/apigateway/README.md.
		Type: "aws_api_gateway_gateway_response",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("response_type"),
		},
		ImportSyntax:  "REST-API-ID/RESPONSE-TYPE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: domain_name
		// and the optional base_path are both configuration arguments (base_path
		// defaults to "" for the root path), and the documented import
		// examples (terraform import aws_api_gateway_base_path_mapping.example
		// example.com/ and .../example.com/base-path) join them
		// DOMAIN-NAME/BASE-PATH — note rest_api_id, though required to
		// create the mapping, is not part of the identity at all. This type
		// sits behind the same floci domain_name gap as
		// aws_api_gateway_domain_name above, so it is untested end-to-end
		// against the pinned image.
		Type: "aws_api_gateway_base_path_mapping",
		Components: []Component{
			attr("domain_name"),
			sep("/"),
			attr("base_path"),
		},
		ImportSyntax:  "DOMAIN-NAME/BASE-PATH",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: usage_plan_id
		// and key_id are both required arguments, and confirmed against the
		// provider's own source (internal/service/apigateway/usage_plan_key.go):
		// the import function splits the documented
		// USAGE-PLAN-ID/USAGE-PLAN-KEY-ID string and calls
		// d.Set(names.AttrKeyID, usagePlanKeyId) — the second segment is
		// literally the key_id argument's value, not a separate id the
		// resource mints. Confirmed against floci by hand: create, get and a
		// terraform import all round-trip cleanly with zero plan diff, even
		// though the parent aws_api_gateway_usage_plan cannot itself be read
		// back through this same emulator (see that type's note above) —
		// GetUsagePlanKey and GetUsagePlan are independent floci code paths.
		Type: "aws_api_gateway_usage_plan_key",
		Components: []Component{
			attr("usage_plan_id"),
			sep("/"),
			attr("key_id"),
		},
		ImportSyntax:  "USAGE-PLAN-ID/USAGE-PLAN-KEY-ID",
		IdentityAttrs: []string{"id", "key_id"},
	},
	TypeIdentity{
		// The one composite in this batch whose primaryIdentifier really is
		// three configuration arguments end to end, confirmed directly
		// against live/survey-full.json's identity schema
		// (required_for_import=[http_method, resource_id, rest_api_id] —
		// none of the three is the method's own id, because a method has no
		// id the provider mints; it is identified entirely by the three
		// arguments that address it). The documented import command
		// (terraform import aws_api_gateway_method.example
		// 12345abcde/67890fghij/GET) joins them
		// REST-API-ID/RESOURCE-ID/HTTP-METHOD. Confirmed working against
		// floci by hand (PutMethod via the raw API), though not
		// import-tested end to end because it needs the same rest_api chain
		// as aws_api_gateway_resource, which this table rejects above.
		Type: "aws_api_gateway_method",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("resource_id"),
			sep("/"),
			attr("http_method"),
		},
		ImportSyntax:  "REST-API-ID/RESOURCE-ID/HTTP-METHOD",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: api_id and
		// name (the v2 stage's client-chosen name — this type's argument is
		// literally called "name", not "stage_name" as in the v1 type above)
		// are both required arguments, and the documented import command
		// (terraform import aws_apigatewayv2_stage.example
		// aabbccddee/example-stage) joins them API-ID/STAGE-NAME. Confirmed
		// against floci by hand: creating a v2 stage through terraform
		// succeeds cleanly (unlike the v1 rest_api chain above, v2's own API
		// and stage create paths have no waiter gap).
		Type: "aws_apigatewayv2_stage",
		Components: []Component{
			attr("api_id"),
			sep("/"),
			attr("name"),
		},
		ImportSyntax:  "API-ID/STAGE-NAME",
		IdentityAttrs: nil,
	},

	// ---- Fold-children (issue #68): declared property-children of an
	// ---- admitted parent whose whole identity is the parent's own plus,
	// ---- for three of them, one further argument of the child's own ------
	//
	// live/mapping.json classifies each of these seven as "fold": a TF type
	// with no cfn_type of its own, decomposed out of a CFN parent resource
	// that models it as a nested property rather than a resource of its own
	// (tools/mapping-gen/overlay.go's own doc comment names the shape:
	// "Terraform decomposes some resources finer than CloudFormation
	// does"). Two sub-shapes ratify here:
	//
	//   - The API Gateway four duplicate an already-admitted parent's own
	//     composite Components verbatim: aws_api_gateway_integration reads
	//     exactly the rest_api_id/resource_id/http_method triple
	//     aws_api_gateway_method's own row above already builds, and
	//     aws_api_gateway_method_settings reads the rest_api_id/stage_name
	//     pair aws_api_gateway_stage's own row already builds. This is
	//     admission path 3 (parent-derived, live/doc.go) worked exactly the
	//     way aws_api_gateway_method itself already is above - nothing new
	//     for declared-instance resolution - and it ratifies on the same
	//     standard of evidence table.go's own "fourth batch, API Gateway"
	//     section comment already cited when it deferred these four: every
	//     component confirmed against live/survey-full.json's identity
	//     schema, except aws_api_gateway_method_settings (confirmed instead
	//     against live/import-grammar.json's scraped Import section, that
	//     type predating the provider's identity-schema mechanism).
	//   - The APS/Prometheus three key on a single parent argument
	//     (workspace_id, scraper_id) and nothing else of their own - the
	//     same named-singleton-child shape aws_s3_bucket_policy and
	//     aws_sns_topic_policy already ratify above, against a new parent
	//     family. aws_prometheus_workspace and aws_prometheus_scraper admit
	//     alongside them below, neither having had a row before this batch.
	//
	// Removal (issue #60's parent-read sweep,
	// internal/live/discovery/parent_read.go): all seven are untaggable,
	// confirmed against each type's own Argument Reference (none has a tags
	// argument), so none can carry an ownership marker and every one of them
	// depends on reading through a parent to be swept at all.
	//
	//   - The APS three fit identity.SingleParentComponent unchanged - one
	//     attribute-supplying component, entirely the parent's - so the
	//     existing #60 leg covers them with no new discovery code once
	//     aws_prometheus_workspace and aws_prometheus_scraper are themselves
	//     taggable admitted parents. Report-only, the same "unverified,
	//     stays report-only" standard parent_read.go's parentReadRemovable
	//     comment already holds aws_sns_topic_policy and
	//     aws_sqs_queue_policy to - a Describe* "no configuration" response
	//     for either has not been confirmed unambiguous here either.
	//   - aws_api_gateway_integration's identity has three components, so
	//     identity.SingleParentComponent's own "exactly one" test excludes
	//     it, but it needs no argument beyond what its parent
	//     (aws_api_gateway_method) already supplies: rendering the method's
	//     own identity - itself parent-derived through
	//     aws_api_gateway_rest_api, not directly taggable, so #60's original
	//     leg cannot anchor on it either - settles the integration's
	//     identity completely too. identity.FoldParentTypes and
	//     discovery's foldChildReadSweep are the small, explicitly-curated
	//     extension this batch adds for exactly this shape (see both doc
	//     comments) - report-only, the same standard as the APS three.
	//   - aws_api_gateway_integration_response, aws_api_gateway_method_response
	//     and aws_api_gateway_method_settings each need one further argument
	//     (status_code, method_path) that a parent read cannot supply once
	//     the child's own block is gone, so removal-sweep coverage for
	//     these three stays the same accepted gap live/LIMITATIONS.md's
	//     "Untaggable types cannot be removed by the sweep" entry already
	//     carries for aws_api_gateway_method itself and for aws_route.
	//     Declared-instance resolution (plan, apply, read-back) is
	//     unaffected either way, since that has never depended on the
	//     sweep.

	TypeIdentity{
		Type: "aws_api_gateway_integration",
		Components: []Component{
			attr("rest_api_id"), sep("/"), attr("resource_id"), sep("/"), attr("http_method"),
		},
		ImportSyntax: "REST-API-ID/RESOURCE-ID/HTTP-METHOD",
		// "This resource exports no additional attributes" (provider docs);
		// nothing may derive another resource's identity from it.
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_api_gateway_integration_response",
		Components: []Component{
			attr("rest_api_id"), sep("/"), attr("resource_id"), sep("/"), attr("http_method"), sep("/"), attr("status_code"),
		},
		ImportSyntax: "REST-API-ID/RESOURCE-ID/HTTP-METHOD/STATUS-CODE",
		// The child's own status_code beyond the method's own triple: not
		// recoverable from a read of the parent alone, see the section
		// comment above.
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_api_gateway_method_response",
		Components: []Component{
			attr("rest_api_id"), sep("/"), attr("resource_id"), sep("/"), attr("http_method"), sep("/"), attr("status_code"),
		},
		ImportSyntax:  "REST-API-ID/RESOURCE-ID/HTTP-METHOD/STATUS-CODE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Fold parent is aws_api_gateway_stage (rest_api_id/stage_name,
		// confirmed against live/survey-full.json's identity schema for
		// that type above), plus method_path, this type's own argument -
		// confirmed against live/import-grammar.json's scraped Import
		// section (composed_of_arguments=true,
		// arguments=["method_path","rest_api_id","stage_name"]), this type
		// predating the provider's identity-schema mechanism.
		Type: "aws_api_gateway_method_settings",
		Components: []Component{
			attr("rest_api_id"), sep("/"), attr("stage_name"), sep("/"), attr("method_path"),
		},
		ImportSyntax:  "REST-API-ID/STAGE-NAME/METHOD-PATH",
		IdentityAttrs: nil,
	},

	serverAssigned("aws_prometheus_workspace",
		"AMP mints the workspace's own id (ws-...) at create time; row-gen's registry-derived guess (the read-only Arn field) is wrong the same way several earlier batches' rejections were (aws_lambda_alias, aws_iam_policy) - confirmed against the provider's documented import command (terraform import aws_prometheus_workspace.demo ws-C6DCB907-F2D7-4D96-957B-66691F865D8B) and its own source (internal/service/amp/workspace.go's Create path uses schema.ImportStatePassthroughContext on d.Id(), never on the separately-exported arn attribute). No configuration argument names it: alias is an optional, non-unique display name the provider does not import by.",
		"WORKSPACEID", "id"),
	serverAssigned("aws_prometheus_scraper",
		"AMP mints the scraper's own id (s-...) at create time; the same registry-Arn mismatch as the workspace above - confirmed against the provider's documented import command (terraform import aws_prometheus_scraper.example s-b6f487db-4761-4930-9215-e9d588a7efe2) and its generated plugin-framework identity schema, which names the scraper's own id rather than the separately-exported arn.",
		"SCRAPERID", "id"),

	TypeIdentity{
		// Named-singleton child of the workspace, the same shape as
		// aws_s3_bucket_policy and aws_sns_topic_policy above: AMP allows at
		// most one alert manager definition per workspace, and the
		// documented import id is the workspace's own id verbatim
		// (terraform import aws_prometheus_alert_manager_definition.demo
		// ws-C6DCB907-F2D7-4D96-957B-66691F865D8B). The provider exports no
		// further attributes for this type.
		Type:          "aws_prometheus_alert_manager_definition",
		Components:    []Component{attr("workspace_id")},
		ImportSyntax:  "WORKSPACEID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same shape as the alert manager definition just above, confirmed
		// against the provider's own DescribeQueryLoggingConfiguration
		// operation (not the older, unrelated DescribeLoggingConfiguration)
		// and its documented import (the workspace id, verbatim).
		Type:          "aws_prometheus_query_logging_configuration",
		Components:    []Component{attr("workspace_id")},
		ImportSyntax:  "WORKSPACEID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Named-singleton child of the scraper rather than the workspace:
		// AMP allows at most one logging configuration per scraper, imported
		// by the scraper's own id verbatim (terraform import
		// aws_prometheus_scraper_logging_configuration.example
		// s-b6f487db-4761-4930-9215-e9d588a7efe2).
		Type:          "aws_prometheus_scraper_logging_configuration",
		Components:    []Component{attr("scraper_id")},
		ImportSyntax:  "SCRAPERID",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableApigateway) }
