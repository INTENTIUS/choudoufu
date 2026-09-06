// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

// Issue #554's generation-time check for the general shape its own fix
// turned out to be: identity's aws_cognito_identity_pool_roles_attachment
// override set identity_pool_id to a shape-correct-but-fake literal, even
// though the generic pass alone - fillBlock's required pass, parentRef,
// siblingRef, seedFromExample - ALREADY resolves that exact argument to a
// real reference against the sibling aws_cognito_identity_pool this run also
// renders (confirmed empirically: deleting the override's own
// SetAttributeRaw call for that one argument and regenerating produced the
// byte-identical reference). The override's job is to fill what the generic
// pass structurally cannot reach - a nested block it never creates, a JSON
// string it cannot shape, an enum member it cannot guess - not to overwrite
// what the generic pass would have gotten right on its own.
//
// This test makes that comparison mechanical: for every type carrying a
// typeOverride, it regenerates every cohort that renders that type with the
// override's Apply function temporarily removed from typeOverrides (Reasons
// stays, so any GENERATED.md provenance line the override alone produced is
// absent, which is fine - this pass only inspects the resource body, not the
// provenance comment), and compares the two by TOP-LEVEL ARGUMENT NAME
// within that one type's own resource block (never by raw line position -
// removing an override can also un-suppress seedFromExample for the whole
// type, which can add or remove other arguments or whole nested blocks
// elsewhere in the same block for reasons unrelated to the one argument
// this check cares about; see topLevelArgs' own comment). An argument
// present on both sides where the committed body holds a quoted literal (or
// a list of them) and the override-removed regeneration holds an unquoted,
// dotted attribute reference - the identity_pool_id shape - is reported.
//
// # Why an allowlist, not a hard block on every finding
//
// Running this sweep against every override at #554's own commit (the PR
// this test landed in) found 78 more types with the identical shape,
// catalogued in referenceShadowKnown below. Each is a genuine candidate for
// the same class of bug identity_pool_id was, but NONE has been individually
// triaged the way #554 triaged identity's - some may have a real reason to
// prefer the literal (the sibling might not create cleanly against floci,
// the reference might not be the value the argument actually wants, the
// override might predate a later-added siblingRef/parentRef capability and
// simply never have been revisited). Fixing all 78 is its own unit of work,
// not this one - see the issue tracker for the follow-up.
//
// So this test enforces the one thing #554 can actually stand behind today:
// no NEW instance of the shape lands unnoticed. A type not already in
// referenceShadowKnown that starts shadowing a resolvable reference fails
// the build; shrinking the allowlist as each entry gets triaged and fixed
// (identity_pool_id's own removal from a prior version of this list is the
// worked example) is the intended way this list gets smaller over time, not
// bigger.
//
// Gated the same way as TestMeasureOverrideRetirements, which this test's
// setup mirrors: it needs the pinned provider's schema (terraform init,
// cached) but neither Docker nor the AWS CLI - the gate is this package's
// existing convention for "regenerates every cohort in the roster," not a
// statement that this specific test touches floci.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cohorts"
	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// referenceShadowKnown is the pre-existing catalogue #554's own sweep found.
// Each key is a provider-local type whose override sets at least one
// argument to a literal that the generic pass alone would instead resolve
// to a reference against a sibling the same cohort renders. Recorded here
// as known debt to triage, not as a verdict that any entry is correct as
// written.
var referenceShadowKnown = map[string]bool{
	"aws_api_gateway_domain_name_access_association":            true, // apigateway: access_association_source, domain_name_arn
	"aws_apigatewayv2_domain_name":                              true, // apigateway: certificate_arn
	"aws_appconfig_deployment":                                  true, // remainder: deployment_strategy_id
	"aws_apprunner_vpc_ingress_connection":                      true, // compute-platforms: service_arn
	"aws_appsync_domain_name":                                   true, // streaming: certificate_arn
	"aws_cloudwatch_log_delivery_source":                        true, // observability: resource_arn
	"aws_cloudwatch_log_destination":                            true, // observability: role_arn, target_arn
	"aws_cloudwatch_log_subscription_filter":                    true, // observability: destination_arn
	"aws_codepipeline":                                          true, // devtools: location
	"aws_codepipeline_webhook":                                  true, // devtools: target_pipeline
	"aws_comprehend_document_classifier":                        true, // ai-location: s3_uri
	"aws_datasync_location_efs":                                 true, // data-movement: security_group_arns, subnet_arn
	"aws_datasync_location_fsx_lustre_file_system":              true, // data-movement: fsx_filesystem_arn, security_group_arns
	"aws_datasync_location_fsx_windows_file_system":             true, // data-movement: fsx_filesystem_arn, security_group_arns
	"aws_datasync_location_hdfs":                                true, // data-movement: agent_arns, hostname
	"aws_datasync_location_nfs":                                 true, // data-movement: agent_arns
	"aws_datasync_location_s3":                                  true, // data-movement: s3_bucket_arn
	"aws_datasync_location_smb":                                 true, // data-movement: agent_arns
	"aws_db_proxy":                                              true, // rds: vpc_subnet_ids
	"aws_ec2_fleet":                                             true, // ec2-core: launch_template_id, version
	"aws_ec2_transit_gateway_connect_peer":                      true, // ec2-networking: transit_gateway_attachment_id
	"aws_eks_fargate_profile":                                   true, // ecs-eks: subnet_ids
	"aws_fsx_lustre_file_system":                                true, // storage: import_path
	"aws_fsx_ontap_file_system":                                 true, // storage: preferred_subnet_id
	"aws_fsx_openzfs_snapshot":                                  true, // storage: volume_id
	"aws_guardduty_publishing_destination":                      true, // security: destination_arn, kms_key_arn
	"aws_ivschat_logging_configuration":                         true, // media: bucket_name
	"aws_lambda_event_source_mapping":                           true, // lambda: event_source_arn
	"aws_msk_scram_secret_association":                          true, // streaming: secret_arn_list
	"aws_mskconnect_custom_plugin":                              true, // streaming: bucket_arn
	"aws_networkfirewall_vpc_endpoint_association":              true, // networking-advanced: subnet_id, vpc_id
	"aws_networkmanager_connect_attachment":                     true, // networking-advanced: edge_location
	"aws_networkmanager_site_to_site_vpn_attachment":            true, // networking-advanced: vpn_connection_arn
	"aws_networkmanager_transit_gateway_peering":                true, // networking-advanced: transit_gateway_arn
	"aws_networkmanager_transit_gateway_route_table_attachment": true, // networking-advanced: transit_gateway_route_table_arn
	"aws_prometheus_query_logging_configuration":                true, // aps: log_group_arn
	"aws_rds_integration":                                       true, // rds: target_arn
	"aws_sagemaker_mlflow_app":                                  true, // sagemaker: artifact_store_uri
	"aws_sagemaker_mlflow_tracking_server":                      true, // sagemaker: artifact_store_uri
	"aws_sagemaker_notebook_instance_lifecycle_configuration":   true, // sagemaker: name
	"aws_ssm_resource_data_sync":                                true, // security: bucket_name, region
	"aws_timestreaminfluxdb_db_instance":                        true, // databases: vpc_security_group_ids
	"aws_vpclattice_resource_gateway":                           true, // networking-advanced: subnet_ids, vpc_id
	"aws_workspacesweb_network_settings":                        true, // connect-euc: security_group_ids, subnet_ids, vpc_id
	"aws_workspacesweb_user_access_logging_settings":            true, // connect-euc: kinesis_stream_arn

	// The 45 types above were found by an earlier, whole-file-line-count
	// version of this sweep (issue #554's own PR). Matching by argument
	// NAME instead of file line position - the fix that let this test
	// catch identity_pool_id's own regression, which the line-position
	// version missed - is strictly more precise and found 33 further
	// types the coarser version's line-count-must-match gate had been
	// hiding behind an unrelated structural change elsewhere in the same
	// resource block (an extra block or argument the doc-example seed
	// adds once an override no longer suppresses it for that type, the
	// same shape aws_cognito_identity_pool_roles_attachment's own
	// role_mapping block turned out to be).
	"aws_appintegrations_data_integration":              true, // data-movement: kms_key
	"aws_cloudwatch_composite_alarm":                    true, // messaging: alarm_rule
	"aws_cloudwatch_event_target":                       true, // observability: arn
	"aws_cloudwatch_metric_stream":                      true, // messaging: firehose_arn
	"aws_datasync_location_azure_blob":                  true, // data-movement: agent_arns
	"aws_datasync_location_fsx_ontap_file_system":       true, // data-movement: security_group_arns, storage_virtual_machine_arn
	"aws_datasync_location_fsx_openzfs_file_system":     true, // data-movement: fsx_filesystem_arn, security_group_arns
	"aws_db_event_subscription":                         true, // rds: sns_topic
	"aws_dms_event_subscription":                        true, // data-movement: sns_topic_arn
	"aws_docdb_event_subscription":                      true, // databases: sns_topic_arn
	"aws_ec2_client_vpn_endpoint":                       true, // ec2-networking: server_certificate_arn
	"aws_emr_studio":                                    true, // compute-platforms: default_s3_location, engine_security_group_id, service_role, subnet_ids, vpc_id, workspace_security_group_id
	"aws_flow_log":                                      true, // ec2-networking: vpc_id
	"aws_fsx_data_repository_association":               true, // storage: data_repository_path, file_system_id
	"aws_fsx_ontap_volume":                              true, // storage: storage_virtual_machine_id
	"aws_gamelift_fleet":                                true, // remainder: build_id
	"aws_imagebuilder_image_pipeline":                   true, // remainder: image_recipe_arn, infrastructure_configuration_arn
	"aws_iot_authorizer":                                true, // iot: authorizer_function_arn
	"aws_kms_replica_key":                               true, // security: primary_key_arn
	"aws_network_acl_rule":                              true, // ec2-networking: cidr_block
	"aws_networkfirewall_firewall":                      true, // networking-advanced: vpc_id
	"aws_networkmanager_core_network_policy_attachment": true, // stragglers: core_network_id
	"aws_networkmanager_vpc_attachment":                 true, // networking-advanced: subnet_arns, vpc_arn
	"aws_pipes_pipe":                                    true, // streaming: target
	"aws_route53_key_signing_key":                       true, // route53-cloudfront: key_management_service_arn
	"aws_route53_resolver_endpoint":                     true, // route53-cloudfront: security_group_ids
	"aws_route53_resolver_firewall_rule":                true, // route53-cloudfront: firewall_domain_list_id
	"aws_route53_resolver_query_log_config":             true, // route53-cloudfront: destination_arn
	"aws_sagemaker_endpoint_configuration":              true, // sagemaker: name
	"aws_timestreaminfluxdb_db_cluster":                 true, // databases: vpc_security_group_ids, vpc_subnet_ids
	"aws_transfer_agreement":                            true, // stragglers: access_role, local_profile_id, partner_profile_id, server_id
	"aws_vpclattice_service_network_vpc_association":    true, // networking-advanced: vpc_identifier
	"aws_wafv2_web_acl_logging_configuration":           true, // security: log_destination_configs
}

// looksLikeReferenceShadow reports whether committed (this type's override
// active) and generic (the same argument with the override's own Apply
// removed) are the same argument line rewritten from a literal to a plain
// attribute reference: the committed side quotes a value, the generic side
// does not and contains a "." traversal - a resource address, never a bare
// literal genericExprText or a seeded literal would produce.
// looksLikeReferenceShadow takes the two VALUES one top-level argument
// holds - committed (the override active) and generic (the same argument
// with the override's own Apply removed), both as topLevelArgs returns them
// (everything after "name =", never the "name =" prefix itself) - and
// reports whether generic reads as a resource attribute reference
// (unquoted, dotted, e.g. aws_cognito_identity_pool.app.id) while committed
// reads as a quoted literal. That combination is the identity_pool_id
// shape: a hand override shadowing a value the generic pass alone would
// already have resolved to a real cross-resource reference.
func looksLikeReferenceShadow(committed, generic string) bool {
	return !strings.Contains(generic, "\"") && strings.Contains(generic, ".") &&
		strings.Contains(committed, "\"")
}

// extractResourceBlock returns the full text of the first
// `resource "typ" "label" { ... }` block in src, braces and all, by
// counting braces from the opening one - simple and sufficient here because
// estate-gen's own output is always terraform-fmt'd, never carries braces
// inside a string that would confuse a naive scan (its literals are plain
// ASCII, no embedded `{`/`}`), and this file only ever reads estate-gen's
// own generated text, never arbitrary user HCL.
func extractResourceBlock(src, typ string) (string, bool) {
	needle := `resource "` + typ + `" "`
	start := strings.Index(src, needle)
	if start < 0 {
		return "", false
	}
	open := strings.IndexByte(src[start:], '{')
	if open < 0 {
		return "", false
	}
	open += start
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1], true
			}
		}
	}
	return "", false
}

// topLevelArgs returns block's own top-level `name = value` scalar
// arguments (depth 1 only, so a name/value pair inside a nested block -
// role_mapping's own identity_provider, say - is never confused with the
// resource's own top-level identity_provider, if it had one). block is the
// full text extractResourceBlock returns, opening/closing braces included.
// value is exactly what estate-gen's own terraform-fmt'd output puts after
// "=" - `"literal"`, `[...]`, `{...}`, or an unquoted traversal expression -
// which is all looksLikeReferenceShadow needs to tell a literal from a
// reference.
func topLevelArgs(block string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(block, "\n")
	depth := 0
	for i, line := range lines {
		if i == 0 {
			continue // the "resource ... {" line itself
		}
		trimmed := strings.TrimSpace(line)
		opens := strings.Count(line, "{")
		closes := strings.Count(line, "}")
		if depth == 0 && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if eq := strings.Index(trimmed, "="); eq > 0 && opens <= closes {
				name := strings.TrimSpace(trimmed[:eq])
				if !strings.ContainsAny(name, " \t") { // a bare identifier, not a stray "} else {"-shaped line
					out[name] = strings.TrimSpace(trimmed[eq+1:])
				}
			}
		}
		depth += opens - closes
	}
	return out
}

func TestOverrideDoesNotShadowAResolvableReference(t *testing.T) {
	if os.Getenv("ESTATE_RETIRE_MEASURE") != "1" {
		t.Skip("regenerates every cohort with each override temporarily removed; set ESTATE_RETIRE_MEASURE=1")
	}
	flocitest.Gate(t, "estate-gen reference-shadow sweep")
	flocitest.RequireBinary(t, defaultInitBin)

	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}

	// The baseline every removal is compared against: each cohort rendered
	// with typeOverrides intact. Until issue #699 this was the committed
	// tree; rendering it means the comparison is generator-against-generator
	// rather than generator-against-a-tree-that-might-already-disagree.
	baseline, typeCohorts := renderRoster(t, schemas, filepath.Join(t.TempDir(), "baseline"))

	rosters := map[string][]string{}
	for _, c := range cohorts.All() {
		rosters[c.Name] = c.Types
	}

	haveFmt := false
	if _, err := exec.LookPath(defaultFmtBin); err == nil {
		haveFmt = true
	}
	regen := func(cohort string) (string, error) {
		out := filepath.Join(t.TempDir(), cohort)
		g, err := planCohort(cohort, schemas, rosters[cohort])
		if err != nil {
			return "", err
		}
		if err := writeCohort(out, cohort, rosters[cohort], g, false, nil); err != nil {
			return "", err
		}
		if haveFmt {
			if err := formatWithBinary(defaultFmtBin, out, runCombined); err != nil {
				return "", err
			}
		}
		return out, nil
	}

	var overrideTypes []string
	for typ := range typeOverrides {
		overrideTypes = append(overrideTypes, typ)
	}
	sort.Strings(overrideTypes)

	newlyFound := map[string]bool{}
	for _, typ := range overrideTypes {
		var cohorts []string
		for c := range typeCohorts[typ] {
			cohorts = append(cohorts, c)
		}
		sort.Strings(cohorts)
		if len(cohorts) == 0 {
			continue
		}

		saved := typeOverrides[typ]
		delete(typeOverrides, typ)
		for _, cohort := range cohorts {
			out, err := regen(cohort)
			if err != nil {
				continue
			}
			baselineDir := baseline[cohort]
			walkErr := filepath.WalkDir(baselineDir, func(cpath string, d os.DirEntry, werr error) error {
				if werr != nil || d.IsDir() || !isConfigFile(d.Name()) {
					return werr
				}
				rel, rerr := filepath.Rel(baselineDir, cpath)
				if rerr != nil {
					return rerr
				}
				a, aerr := os.ReadFile(cpath)
				b, berr := os.ReadFile(filepath.Join(out, rel))
				if aerr != nil || berr != nil {
					return nil
				}
				// Scoped to the type's OWN resource block, not the whole
				// file: removing an override can cascade into unrelated
				// structural changes elsewhere - most commonly a new
				// supporting resource the seed-reference pass (gen.go) now
				// pulls in for THIS type's own doc-example arguments once
				// its override no longer suppresses seedFromExample - which
				// shifts every line count in the file without touching this
				// block at all. A whole-file line-for-line compare misses
				// exactly the case this test exists to catch (found
				// reverting identity_pool_id's own fix as a regression
				// check: the whole-file version passed even with the bug
				// reintroduced, because removing the override for
				// measurement purposes changed identity.tf's total line
				// count for an unrelated reason).
				aBlock, aOK := extractResourceBlock(string(a), typ)
				bBlock, bOK := extractResourceBlock(string(b), typ)
				if !aOK || !bOK {
					return nil
				}
				// Matched by ARGUMENT NAME, not by line position: removing
				// an override can also un-suppress seedFromExample for the
				// whole type (seed.go: an override suppresses the doc-
				// example seed for its entire type), which can add or
				// remove other top-level arguments or whole nested blocks
				// in the same resource - aws_cognito_identity_pool_roles_attachment's
				// own doc example adds an optional role_mapping block once
				// its override no longer suppresses it, twelve lines
				// unrelated to identity_pool_id's own one-line swap. A
				// position- or line-count-based compare misses exactly
				// that case (found as a false negative reverting
				// identity_pool_id's own fix as a regression check); only
				// the top-level argument NAME is a stable enough key to
				// match a committed value against its generic-pass-alone
				// counterpart regardless of what else changed around it.
				aArgs := topLevelArgs(aBlock)
				bArgs := topLevelArgs(bBlock)
				var names []string
				for name := range aArgs {
					if _, ok := bArgs[name]; ok {
						names = append(names, name)
					}
				}
				sort.Strings(names)
				for _, name := range names {
					ac, bc := aArgs[name], bArgs[name]
					if ac == bc {
						continue
					}
					if looksLikeReferenceShadow(ac, bc) {
						if !referenceShadowKnown[typ] {
							newlyFound[typ] = true
							t.Errorf("%s (%s, %s, argument %q): override sets a literal the generic pass alone would resolve to a reference - committed %q, generic-pass-alone %q. If this literal is deliberate, explain why in the override's own Reasons and add %q to referenceShadowKnown; if not, wire the reference the way #554 fixed identity_pool_id.",
								typ, cohort, rel, name, ac, bc, typ)
						}
					}
				}
				return nil
			})
			if walkErr != nil {
				t.Fatal(walkErr)
			}
		}
		typeOverrides[typ] = saved
	}
	if len(newlyFound) > 0 {
		var names []string
		for n := range newlyFound {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Logf("new reference-shadow types not yet in referenceShadowKnown: %s", strings.Join(names, ", "))
	}
}
