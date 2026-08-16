// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"testing"
)

// TestRuleAdoption_GroupsByBucketAndRule pins the grouping key: two
// proposals in the same bucket but reached by a different Rule string never
// pool into one ruleStats, and a non-pastable bucket (fold-child here) never
// enters the ledger at all - see pastableBucket's own doc comment for why
// that exclusion exists (a fold-child's Matched rate is always 0 by
// construction, not a real signal).
func TestRuleAdoption_GroupsByBucketAndRule(t *testing.T) {
	rows := []convergenceRow{
		{TFType: "aws_a", ProposedBucket: "server-assigned", ProposedRule: "rule-1", Matched: true},
		{TFType: "aws_b", ProposedBucket: "server-assigned", ProposedRule: "rule-1", Matched: true},
		{TFType: "aws_c", ProposedBucket: "server-assigned", ProposedRule: "rule-1", Matched: false},
		{TFType: "aws_d", ProposedBucket: "server-assigned", ProposedRule: "rule-precedence", Matched: true},
		{TFType: "aws_e", ProposedBucket: "client-named", ProposedRule: "rule-1", Matched: true},
		{TFType: "aws_f", ProposedBucket: "fold-child", ProposedRule: "via==fold: property-child of X", Matched: false},
		{TFType: "aws_g", ProposedBucket: "needs-hand-separator", ProposedRule: "composite", Matched: false},
		{TFType: "aws_h", ProposedBucket: "evidence-only", ProposedRule: "rule-1", Matched: false},
	}

	stats := ruleAdoption(rows)

	if len(stats) != 3 {
		t.Fatalf("ruleAdoption produced %d rule classes, want 3 (fold-child/needs-hand-separator/evidence-only rows must be excluded): %+v", len(stats), stats)
	}

	sa1 := stats[ruleKey{Bucket: bucketServerAssigned, Rule: "rule-1"}]
	if sa1.Compared != 3 || sa1.Matched != 2 {
		t.Errorf("server-assigned/rule-1 = %+v, want Compared=3 Matched=2", sa1)
	}
	saP := stats[ruleKey{Bucket: bucketServerAssigned, Rule: "rule-precedence"}]
	if saP.Compared != 1 || saP.Matched != 1 {
		t.Errorf("server-assigned/rule-precedence = %+v, want Compared=1 Matched=1", saP)
	}
	cn1 := stats[ruleKey{Bucket: bucketClientNamed, Rule: "rule-1"}]
	if cn1.Compared != 1 || cn1.Matched != 1 {
		t.Errorf("client-named/rule-1 = %+v, want Compared=1 Matched=1", cn1)
	}
}

// TestRuleStats_Qualifies pins the auto-propose bar: 100% match AND at
// least proposeMinSample instances. Either condition failing alone must
// disqualify - a rule class does not get a pass for being unanimous over
// too few instances, or for having plenty of instances but even one
// disagreement.
func TestRuleStats_Qualifies(t *testing.T) {
	tests := []struct {
		name string
		s    ruleStats
		want bool
	}{
		{"perfect and large enough", ruleStats{Compared: proposeMinSample, Matched: proposeMinSample}, true},
		{"perfect but one below the floor", ruleStats{Compared: proposeMinSample - 1, Matched: proposeMinSample - 1}, false},
		{"large enough but one mismatch", ruleStats{Compared: proposeMinSample + 10, Matched: proposeMinSample + 9}, false},
		{"zero compared", ruleStats{Compared: 0, Matched: 0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.qualifies(); got != tt.want {
				t.Errorf("qualifies() = %v, want %v (stats=%+v)", got, tt.want, tt.s)
			}
		})
	}
}

// TestQualifyingRules_FiltersToQualifyingOnly checks the ledger-to-qualifying
// filter keeps only classes ruleStats.qualifies accepts.
func TestQualifyingRules_FiltersToQualifyingOnly(t *testing.T) {
	stats := map[ruleKey]ruleStats{
		{Bucket: bucketServerAssigned, Rule: "clean"}:  {Compared: proposeMinSample, Matched: proposeMinSample},
		{Bucket: bucketServerAssigned, Rule: "dirty"}:  {Compared: proposeMinSample + 3, Matched: proposeMinSample + 2},
		{Bucket: bucketClientNamed, Rule: "too-small"}: {Compared: proposeMinSample - 1, Matched: proposeMinSample - 1},
	}
	got := qualifyingRules(stats)
	if len(got) != 1 {
		t.Fatalf("qualifyingRules returned %d classes, want 1: %+v", len(got), got)
	}
	if _, ok := got[ruleKey{Bucket: bucketServerAssigned, Rule: "clean"}]; !ok {
		t.Errorf("qualifyingRules dropped the one class that should have qualified: %+v", got)
	}
}

// TestSelectProposeCandidates covers every exclusion selectProposeCandidates
// applies, one proposal per reason so a broken exclusion fails on a specific
// case rather than a vague count mismatch.
func TestSelectProposeCandidates(t *testing.T) {
	qualifyingRule := ruleKey{Bucket: bucketServerAssigned, Rule: "clean"}
	qualifying := map[ruleKey]ruleStats{qualifyingRule: {Compared: 5, Matched: 5}}

	proposals := []proposal{
		{TFType: "aws_new_clean", Bucket: bucketServerAssigned, Rule: "clean"},           // should be proposed
		{TFType: "aws_already_admitted", Bucket: bucketServerAssigned, Rule: "clean"},    // excluded: already admitted
		{TFType: "aws_known_rejected", Bucket: bucketServerAssigned, Rule: "clean"},      // excluded: recorded rejection
		{TFType: "aws_wrong_rule", Bucket: bucketServerAssigned, Rule: "not-qualifying"}, // excluded: rule does not qualify
		{TFType: "aws_not_pastable", Bucket: bucketNeedsHandSeparator, Rule: "clean"},    // excluded: bucket never pastable
	}
	admitted := map[string]bool{"aws_already_admitted": true}
	rejected := map[string]bool{"aws_known_rejected": true}

	got := selectProposeCandidates(proposals, admitted, rejected, qualifying)

	if len(got) != 1 {
		t.Fatalf("selectProposeCandidates returned %d candidates, want 1: %+v", len(got), got)
	}
	if got[0].Proposal.TFType != "aws_new_clean" {
		t.Errorf("selectProposeCandidates returned %q, want aws_new_clean", got[0].Proposal.TFType)
	}
	if got[0].Rule != qualifyingRule {
		t.Errorf("candidate Rule = %+v, want %+v", got[0].Rule, qualifyingRule)
	}
}

// TestSelectProposeCandidates_Deterministic pins the sort order (TF type,
// ascending) so two runs over the same inputs always print candidates in
// the same order.
func TestSelectProposeCandidates_Deterministic(t *testing.T) {
	rule := ruleKey{Bucket: bucketClientNamed, Rule: "r"}
	qualifying := map[ruleKey]ruleStats{rule: {Compared: 5, Matched: 5}}
	proposals := []proposal{
		{TFType: "aws_zzz", Bucket: bucketClientNamed, Rule: "r"},
		{TFType: "aws_aaa", Bucket: bucketClientNamed, Rule: "r"},
		{TFType: "aws_mmm", Bucket: bucketClientNamed, Rule: "r"},
	}
	got := selectProposeCandidates(proposals, map[string]bool{}, map[string]bool{}, qualifying)
	want := []string{"aws_aaa", "aws_mmm", "aws_zzz"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Proposal.TFType != w {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i].Proposal.TFType, w)
		}
	}
}

// TestLoadRejectedTypes_LedgerIsIntact is the regression tie to real history:
// aws_lambda_alias and aws_lambda_layer_version_permission were the identity
// table's own worked "Rejected, and deliberately absent from this table"
// example, recorded in prose in table_cohort_lambda.go until issue #96
// generated that table in full and moved every such ruling into
// rejected.json. If a future edit drops rows from the ledger, this fails
// loudly instead of silently losing the safety net.
//
// aws_lambda_layer_version_permission dropped out of the sentinel list
// itself (2026-08-16, the rejected3 batch): the original 2026 note's whole
// complaint was that row-gen's classifier proposed a wrong server-assigned
// row instead of the doc-documented "layer-arn,version-number" composite,
// with import-grammar precedence not yet able to derive that composite
// automatically. It can now (bucket: composite, layer_name,version_number
// joined by ",", both plain Required arguments on the resource, matching
// the provider's Import section exactly) - the extractor gap the original
// note named has since closed, so the type is admitted in
// table_generated.go rather than rejected.json. aws_lambda_alias alone
// still stands for the historical point: the provider's own Import section
// disagrees with the registry's read-only AliasArn, and nothing about that
// disagreement has changed.
func TestLoadRejectedTypes_LedgerIsIntact(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := loadRejectedTypes(root)
	if err != nil {
		t.Fatalf("loadRejectedTypes: %v", err)
	}
	for _, want := range []string{"aws_lambda_alias"} {
		if !rejected[want] {
			t.Errorf("loadRejectedTypes did not find %q, the identity table's own worked Rejected example", want)
		}
	}
	// Sentinels for the second recovery (#127): the remainder batch's
	// rejections lived only in prose banners a merge dropped before #96's
	// scrape ran, and were recovered separately from the remainder estate's
	// README. One from each of that README's two rejection sections.
	for _, want := range []string{"aws_fms_policy", "aws_waf_web_acl"} {
		if !rejected[want] {
			t.Errorf("loadRejectedTypes did not find %q, recovered from the remainder README in #127", want)
		}
	}
	// The ledger was recovered wholesale from deleted prose, and the first
	// recovery over-collected: it harvested every type name near the word
	// "Rejected" rather than the subject of each "- <type>:" bullet, so a
	// rejection's own explanatory prose ("...the already-admitted
	// aws_dynamodb_table") put admitted types into the veto set. Issue #131
	// re-ran the scrape on the bullet-subject rule and reconciled against
	// the admission table: 58 contradictions dropped, 5 genuine rejections
	// the first pass had missed recovered.
	//
	// So a floor still guards against silent loss, at the corrected count
	// rather than the original one. Lowering it again needs the same
	// treatment: a stated rule, applied to the source, with the delta
	// explained. TestRejectedLedgerIsDisjointFromAdmitted guards the other
	// direction.
	//
	// 153 (2026-08-16): the type-parity sweep's own reviewed drop. Six
	// entries were the aws_alb* family plus aws_lb_listener_certificate -
	// aws_alb, aws_alb_listener, aws_alb_listener_certificate,
	// aws_alb_listener_rule, aws_alb_target_group are the provider's own
	// documented aliases of aws_lb, aws_lb_listener, aws_lb_listener_
	// certificate, aws_lb_listener_rule, aws_lb_target_group ("is known as
	// ... The functionality is identical."), and aws_lb_listener_
	// certificate itself - the canonical name - had never been admitted
	// either. All six now admit with evidence (table_generated.go); a
	// rejected type that later admits with a stated identity is not a
	// stale ledger entry to leave sitting - it is the debt the ruling
	// names getting paid off. See internal/live/identity.DefaultTable's
	// aws_alb*/aws_lb_listener_certificate rows for the identity each one
	// carries.
	//
	// 132 (2026-08-16, same-day follow-up): the wall/rejected2 batch's own
	// reviewed drop, 21 entries. Re-derived against today's machinery
	// (registry-backed admission, import-grammar precedence, the doc
	// cache's v6.59.0 pin - a version bump ahead of the v6.58.0 evidence
	// several of these entries carried, which is why aws_resiliencehubv2_
	// service/system, aws_mailmanager_ingress_point, aws_osis_pipeline and
	// aws_prometheus_anomaly_detector's own doc pages simply did not exist
	// at the earlier pin) rather than by name: aws_comprehend_entity_
	// recognizer, aws_devopsguru_notification_channel, aws_dx_hosted_
	// private_virtual_interface, aws_dx_hosted_public_virtual_interface,
	// aws_dx_hosted_transit_virtual_interface, aws_dynamodb_global_
	// secondary_index, aws_dynamodb_kinesis_streaming_destination,
	// aws_elasticache_user_group_association, aws_inspector_resource_group,
	// aws_iot_ca_certificate, aws_kinesisanalyticsv2_application,
	// aws_lightsail_domain, aws_mailmanager_ingress_point,
	// aws_msk_single_scram_secret_association, aws_network_acl_association,
	// aws_osis_pipeline, aws_resiliencehubv2_service,
	// aws_resiliencehubv2_system, aws_s3outposts_endpoint,
	// aws_verifiedaccess_group, aws_vpc_endpoint_connection_notification.
	// All now admit with evidence (table_generated.go). Two candidates this
	// same batch tried and reverted, both for reasons the identity work
	// itself was right about but a downstream consumer was not ready for:
	// aws_fms_policy verifies cleanly on its own identity but ties with the
	// already-admitted aws_iam_policy under internal/live/identity/
	// parent.go's _policy suffix convention (live/e2e/estates/remainder/
	// README.md's "naming collision" section); aws_ec2_carrier_gateway
	// also verifies cleanly but internal/live/discovery/tagging_test.go
	// names it, by constant, as the canonical "mapped but not admitted"
	// real-artifact fixture. Both stay rejected, with the reason restated
	// in rejected.json itself rather than only in the deleted-prose
	// recovery this ledger already carries for most of its other rows.
	//
	// 104 (2026-08-16, rejected3 batch): 28 more admissions, all
	// independently doc/schema-verified rather than pasted from row-gen's
	// raw proposal - aws_amplify_domain_association, aws_appstream_fleet,
	// aws_appstream_image_builder, aws_backup_restore_testing_selection,
	// aws_bedrockagentcore_workload_identity, aws_codebuild_source_
	// credential, aws_cognito_user_pool_ui_customization, aws_elasticache_
	// global_replication_group, aws_emr_studio_session_mapping, aws_
	// gamelift_game_server_group, aws_glue_catalog, aws_glue_catalog_
	// table_optimizer, aws_glue_dev_endpoint, aws_glue_user_defined_
	// function, aws_lambda_layer_version_permission, aws_network_
	// interface_sg_attachment, aws_networkflowmonitor_monitor, aws_
	// pinpointsmsvoicev2_resource_policy, aws_rds_cluster_endpoint, aws_
	// route53_cidr_location, aws_scheduler_schedule, aws_service_
	// discovery_instance, aws_ses_receipt_filter, aws_ses_receipt_rule,
	// aws_ses_receipt_rule_set, aws_ses_template, aws_shield_protection_
	// group, aws_vpc_block_public_access_options. All now admit with
	// evidence (table_generated.go); see
	// TestLoadRejectedTypes_LedgerIsIntact's own note on why aws_lambda_
	// layer_version_permission left its historical sentinel role.
	//
	// Two more (aws_pinpoint_app, aws_ssm_document) verified cleanly on
	// identity alone but were tried and reverted for downstream reasons
	// the identity work itself was right about, the same shape as wall/
	// rejected2's aws_fms_policy/aws_ec2_carrier_gateway: aws_pinpoint_app
	// sits under live/residue.go's hand-curated DeprecatedServices roster
	// (the whole aws_pinpoint_ prefix, AWS's own Pinpoint end-of-support
	// announcement) and is TestRefusalNamesDeprecatedServiceCohort's own
	// fixture; aws_ssm_document sits in the same file's emulatorBlocked
	// roster ("floci answers ssm:CreateDocument with UnsupportedOperation")
	// and is TestRefusalNamesEmulatorBlockedCohort's own fixture. Both
	// restored to rejected.json with the reason stated directly.
	//
	// A wider sweep over the same non-WAF remainder caught nineteen more
	// candidates whose raw proposal was itself the defect - a config-
	// supplied parent-scoping argument (api_id, cluster_id, user_pool_id,
	// domain_identifier and the like) the classifier's flat server-
	// assigned or single-argument client-named proposal silently dropped,
	// the same wrong-object risk aws_backup_selection/aws_eks_pod_
	// identity_association/aws_prometheus_anomaly_detector already named
	// (aws_apigatewayv2_route and aws_datazone_environment_profile/
	// aws_datazone_project join that list this batch) - plus seven types
	// (aws_iam_group_membership, aws_iot_policy_attachment, aws_iot_thing_
	// principal_attachment, aws_lakeformation_data_lake_settings, aws_
	// lakeformation_permissions, aws_lakeformation_resource, aws_
	// lakeformation_resource_lf_tag) whose pinned v6.59.0 docs carry no
	// Import section at all (one states outright "You cannot import this
	// resource."), so the registry's own server-assigned claim has no doc
	// corroboration. All twenty-eight stay rejected, with the reason stated
	// directly in rejected.json rather than only via recovered_from.
	if len(rejected) < 104 {
		t.Errorf("rejected.json carries %d types, want at least the 104 standing after the rejected3 batch's 28-type admission", len(rejected))
	}
}

// TestLoadRejectedTypes_RefusesEmptyLedger pins the fail-closed rule: an
// absent or empty ledger is an error, never an empty veto set.
func TestLoadRejectedTypes_RefusesEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/tools/row-gen", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/tools/row-gen/rejected.json", []byte(`{"rejected":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRejectedTypes(dir); err == nil {
		t.Error("loadRejectedTypes accepted an empty ledger; it must fail closed")
	}
	if _, err := loadRejectedTypes(t.TempDir()); err == nil {
		t.Error("loadRejectedTypes accepted a missing ledger; it must fail closed")
	}
}
