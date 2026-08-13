// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// unexplainedNote is classifyRow's generic fallback note: a TF type that
// falls all the way through to via:none with no curated overlay entry
// behind it. The curated-68 pin below must see none of these - every
// non-mapped curated type has to carry an explicit fold or none entry in
// the overlay, not a silent auto-fallthrough.
const unexplainedNote = "no CFN counterpart found by name or curated overlay"

// TestCurated68Pin regenerates the join restricted to live/SURVEY.md's
// curated 68 and pins issue #43's own acceptance numbers: 59 mapped
// (28 by name, 31 by the overlay's aliases), 9 fold-or-none, and 0
// unexplained - every curated type the heuristic does not reach has an
// overlay entry that says why.
func TestCurated68Pin(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	curated, err := loadCuratedRoster(filepath.Join(root, curatedMDRel))
	if err != nil {
		t.Fatalf("loading the curated roster: %v", err)
	}
	if len(curated) != 68 {
		t.Fatalf("%s's per-type table has %d types, want 68", curatedMDRel, len(curated))
	}

	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}

	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	mapping, _, err := buildMapping(curated, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	foldOrNone := mapping.Counts.Fold + mapping.Counts.None
	var unexplained []string
	for _, row := range mapping.Rows {
		if row.Via == viaNone && row.Note != nil && *row.Note == unexplainedNote {
			unexplained = append(unexplained, row.TFType)
		}
	}

	if mapping.Counts.Mapped != 59 {
		t.Errorf("mapped = %d, want 59", mapping.Counts.Mapped)
	}
	if foldOrNone != 9 {
		t.Errorf("fold+none = %d, want 9", foldOrNone)
	}
	if len(unexplained) != 0 {
		t.Errorf("%d curated types fell through with no overlay explanation: %v", len(unexplained), unexplained)
	}
}

// TestOverlayTwoWayStaleness checks tools/mapping-gen/overlay.json against
// the current TF and CFN rosters in both directions, the way
// tools/survey-gen/survey_gen_test.go's exception table does: every entry
// must still name types that exist (drift: a type renamed or removed out
// from under a hand-written row), and every alias must still be needed (a
// no-cliff check, chant's aws-resources.test.ts:7-14 - an alias the name
// heuristic has since grown to derive on its own is a stale row that should
// be deleted, not carried forever).
func TestOverlayTwoWayStaleness(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	tfSet := make(map[string]bool, len(tfTypes))
	for _, t := range tfTypes {
		tfSet[t] = true
	}

	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, c := range cfnTypes {
		cfnSet[c] = true
	}

	index, err := buildNameIndex(cfnTypes)
	if err != nil {
		t.Fatalf("buildNameIndex: %v", err)
	}

	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	for tf, cfn := range overlay.Aliases {
		if !tfSet[tf] {
			t.Errorf("alias %s -> %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, cfn, tf, tfRosterRel)
		}
		if !cfnSet[cfn] {
			t.Errorf("alias %s -> %s: %s is no longer in the CFN roster (%s); remove or update this overlay entry", tf, cfn, cfn, cfnRosterRel)
		}
		if got, ok := index[tf]; ok && got == cfn {
			t.Errorf("alias %s -> %s is stale: the name heuristic now derives this pair on its own; delete the overlay entry", tf, cfn)
		}
	}
	for tf, parent := range overlay.Folds {
		if !tfSet[tf] {
			t.Errorf("fold %s -> %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, parent, tf, tfRosterRel)
		}
		if !cfnSet[parent] {
			t.Errorf("fold %s -> %s: %s is no longer in the CFN roster (%s); remove or update this overlay entry", tf, parent, parent, cfnRosterRel)
		}
	}
	for tf := range overlay.Nones {
		if !tfSet[tf] {
			t.Errorf("none entry for %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, tf, tfRosterRel)
		}
	}

	// service_aliases' two-way check: the TF-side half is "does any TF type
	// in the current roster still start with this prefix" (a prefix no
	// type uses any more is dead weight, the service_aliases analog of an
	// alias entry naming a TF type that no longer exists), and the CFN-side
	// half is "does the named service still appear in the current CFN
	// roster at all" (a service the registry has since renamed or dropped).
	cfnServices := map[string]bool{}
	for c := range cfnSet {
		if svc, _, ok := splitCFNType(c); ok {
			cfnServices[svc] = true
		}
	}
	for prefix, services := range overlay.ServiceAliases {
		ptoks := strings.Split(prefix, "_")
		used := false
		for tf := range tfSet {
			if !strings.HasPrefix(tf, "aws_") {
				continue
			}
			tokens := strings.Split(strings.TrimPrefix(tf, "aws_"), "_")
			if tokenPrefixEqual(tokens, ptoks) {
				used = true
				break
			}
		}
		if !used {
			t.Errorf("service_aliases prefix %q: no TF type in the current roster (%s) starts with aws_%s_; remove or update this overlay entry", prefix, tfRosterRel, prefix)
		}
		for _, svc := range services {
			if !cfnServices[svc] {
				t.Errorf("service_aliases %q -> %q: %q is no longer a CFN service in the current registry (%s); remove or update this overlay entry", prefix, svc, svc, cfnRosterRel)
			}
		}
	}
}

// TestNoUnflaggedCollisions is the full-roster collision test: no two TF
// types resolve to the same CFN type unless the sharing is explicit -
// via:alias, a curated row someone deliberately wrote - rather than two
// independent via:name hits from the heuristic landing on the same CFN
// type, which the heuristic itself has no way to notice or resolve.
func TestNoUnflaggedCollisions(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	mapping, _, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	byCFN := map[string][]Row{}
	for _, row := range mapping.Rows {
		if row.Via != viaName && row.Via != viaAlias {
			continue
		}
		byCFN[*row.CFNType] = append(byCFN[*row.CFNType], row)
	}

	for cfn, rows := range byCFN {
		if len(rows) < 2 {
			continue
		}
		var nameHits []string
		for _, r := range rows {
			if r.Via == viaName {
				nameHits = append(nameHits, r.TFType)
			}
		}
		if len(nameHits) > 1 {
			sort.Strings(nameHits)
			var all []string
			for _, r := range rows {
				all = append(all, r.TFType+" ("+r.Via+")")
			}
			sort.Strings(all)
			t.Errorf("%s is claimed by more than one unflagged name-heuristic hit (%v); every TF type past the first must be an explicit overlay alias. Full group: %v",
				cfn, nameHits, all)
		}
	}
}

// TestServiceAliasAnchorsResolve pins the heuristic v2 anchors from issue
// #43's follow-up: TF types the plain name heuristic could never reach (its
// one candidate is service+resource concatenated; these all need either a
// prefix with no relation to a real AWS service, or a CFN resource segment
// that redundantly repeats the service word) that the service-alias
// heuristic must now resolve, each verified against the live registry
// before being asserted here (aws_db_snapshot's own natural anchor,
// AWS::RDS::DBSnapshot, does not actually exist in live/registry.json, so
// it is deliberately not one of these).
func TestServiceAliasAnchorsResolve(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}
	mapping, _, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	byType := make(map[string]Row, len(mapping.Rows))
	for _, r := range mapping.Rows {
		byType[r.TFType] = r
	}

	anchors := map[string]string{
		// vpc -> EC2: the CFN resource segment repeats "VPC" (a word the
		// TF prefix already carries), which the plain heuristic's single
		// "ec2" + resource candidate can never reach.
		"aws_vpc_peering_connection": "AWS::EC2::VPCPeeringConnection",
		// cloudwatch_log -> Logs: cloudwatch_log_* is really the Logs
		// service; the match needs the cut that strips only "cloudwatch_",
		// keeping "log_stream" - Logs' own resource names all keep "Log"
		// as a literal word.
		"aws_cloudwatch_log_stream": "AWS::Logs::LogStream",
		// cloudwatch_event -> Events, generalizing the single
		// aws_cloudwatch_event_rule overlay alias to the rest of the
		// family.
		"aws_cloudwatch_event_bus": "AWS::Events::EventBus",
		// db -> RDS: an abbreviated TF prefix with no service-casing
		// relationship to "RDS" at all.
		"aws_db_proxy": "AWS::RDS::DBProxy",
		// dx -> DirectConnect.
		"aws_dx_connection": "AWS::DirectConnect::Connection",
		// route53_resolver -> Route53Resolver, a distinct CFN service from
		// plain route53's Route53 - the longer prefix must be tried, not
		// just the one tf's own first token spells.
		"aws_route53_resolver_endpoint": "AWS::Route53Resolver::ResolverEndpoint",
		// ssoadmin -> SSO.
		"aws_ssoadmin_permission_set": "AWS::SSO::PermissionSet",
		// sesv2 -> SES (live/registry.json carries no separate SESv2
		// service).
		"aws_sesv2_email_identity": "AWS::SES::EmailIdentity",
	}
	for _, ct := range anchors {
		if !contains(cfnTypes, ct) {
			t.Fatalf("test bug: anchor CFN type %s is not in the live registry; pick a different anchor", ct)
		}
	}
	if contains(cfnTypes, "AWS::RDS::DBSnapshot") {
		t.Error("AWS::RDS::DBSnapshot now exists in live/registry.json; consider adding aws_db_snapshot as an anchor")
	}

	for tf, want := range anchors {
		row, ok := byType[tf]
		if !ok {
			t.Errorf("%s: not in the regenerated mapping at all", tf)
			continue
		}
		if row.Via != viaServiceAlias {
			t.Errorf("%s: via = %q, want %q", tf, row.Via, viaServiceAlias)
			continue
		}
		if row.CFNType == nil || *row.CFNType != want {
			t.Errorf("%s: cfn_type = %v, want %q", tf, row.CFNType, want)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestServiceAliasFalsePositiveGuard is the regression test for the two
// hits that motivated a service-scoped heuristic in the first place (a
// naive cross-service name join once matched aws_amplify_webhook to
// AWS::CodePipeline::Webhook and aws_appsync_type to AWS::Cassandra::Type):
// run against the real, full roster, both must still be via:none - neither
// "amplify" nor "appsync" is a service_aliases prefix, so nothing in this
// package can ever reach across into CodePipeline or Cassandra for them.
func TestServiceAliasFalsePositiveGuard(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}
	mapping, _, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	byType := make(map[string]Row, len(mapping.Rows))
	for _, r := range mapping.Rows {
		byType[r.TFType] = r
	}

	for _, tf := range []string{"aws_amplify_webhook", "aws_appsync_type"} {
		row, ok := byType[tf]
		if !ok {
			t.Fatalf("%s: not in the regenerated mapping at all", tf)
		}
		if row.Via == viaServiceAlias {
			t.Errorf("%s: via = service-alias (cfn_type %v) - the service-scoped heuristic must never cross into a service its own TF prefix was not aliased to", tf, row.CFNType)
		}
	}
}

// TestServiceAliasCrossHeuristicCollisions documents, rather than merely
// asserts, every case where a service-alias hit shares a CFN type with
// another mapped row: unlike TestNoUnflaggedCollisions' name-heuristic
// rule, this is not automatically an error - aws_alb_* and aws_lb_* (TF's
// own older/newer names for the same ElasticLoadBalancingV2 resources) and
// aws_ses_*/aws_sesv2_* (the v1 and v2 SES APIs managing the same
// account-level SES objects) are genuine synonyms, verified by hand against
// the AWS provider's own docs, not a coincidence like the S3/S3Outposts
// bucket case the overlay's own service_aliases comment for s3control
// describes catching. A collision this allowlist does not name failing here
// is exactly the signal that caught that S3Outposts bug: a human needs to
// look, not silently trust either side.
func TestServiceAliasCrossHeuristicCollisions(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}
	mapping, _, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	// Every via:service-alias row's CFN type, paired with every other TF
	// type (any via) that also reaches the same CFN type - the allowlist
	// below must name every pair found, and every pair it names must still
	// be found (so a stale entry - a collision that stopped happening -
	// fails loudly too).
	byCFN := map[string][]Row{}
	for _, r := range mapping.Rows {
		if r.Via == viaName || r.Via == viaAlias || r.Via == viaServiceAlias {
			byCFN[*r.CFNType] = append(byCFN[*r.CFNType], r)
		}
	}

	allowed := map[[2]string]bool{
		{"aws_alb_listener", "aws_lb_listener"}:                         true,
		{"aws_alb_listener_certificate", "aws_lb_listener_certificate"}: true,
		{"aws_alb_listener_rule", "aws_lb_listener_rule"}:               true,
		{"aws_alb_target_group", "aws_lb_target_group"}:                 true,
		{"aws_ses_configuration_set", "aws_sesv2_configuration_set"}:    true,
		{"aws_ses_email_identity", "aws_sesv2_email_identity"}:          true,
	}
	seen := map[[2]string]bool{}

	for _, rows := range byCFN {
		hasServiceAlias := false
		for _, r := range rows {
			if r.Via == viaServiceAlias {
				hasServiceAlias = true
			}
		}
		if len(rows) < 2 || !hasServiceAlias {
			continue
		}
		names := make([]string, len(rows))
		for i, r := range rows {
			names[i] = r.TFType
		}
		sort.Strings(names)
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				pair := [2]string{names[i], names[j]}
				seen[pair] = true
				if !allowed[pair] {
					t.Errorf("undocumented collision: %s and %s both reach the same CFN type; either this is a genuine synonym pair (add it to this test's allowlist with a comment saying why) or a service_aliases entry needs narrowing (see the s3control/S3Outposts fix in overlay.json)", pair[0], pair[1])
				}
			}
		}
	}
	for pair := range allowed {
		if !seen[pair] {
			t.Errorf("allowlisted collision %s/%s no longer happens; remove it from this test's allowlist", pair[0], pair[1])
		}
	}
}

// TestMappingJSONMatchesCommittedInputs regenerates live/mapping.json from
// the other committed inputs (live/survey-full.json, live/registry.json,
// the overlay) and diffs it against the committed artifact, the same
// pattern tools/survey-gen's TestSurveyJSONAgainstHandTable uses: it reads
// only committed files, so it needs no gate.
func TestMappingJSONMatchesCommittedInputs(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	mapping, _, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	got, err := mapping.marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated mapping: %v", err)
	}

	want, err := os.ReadFile(filepath.Join(root, mappingJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", mappingJSONRel, err)
	}
	if bytes.Equal(got, want) {
		return
	}

	var gotM, wantM Mapping
	if err := json.Unmarshal(got, &gotM); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantM); err != nil {
		t.Fatalf("decoding the committed %s: %v", mappingJSONRel, err)
	}
	wantRows := map[string]Row{}
	for _, r := range wantM.Rows {
		wantRows[r.TFType] = r
	}
	for _, g := range gotM.Rows {
		w, ok := wantRows[g.TFType]
		if !ok {
			t.Errorf("%s: regenerated but absent from the committed file", g.TFType)
			continue
		}
		delete(wantRows, g.TFType)
		gj, _ := json.Marshal(g)
		wj, _ := json.Marshal(w)
		if string(gj) != string(wj) {
			t.Errorf("%s drifted:\n  committed:   %s\n  regenerated: %s", g.TFType, wj, gj)
		}
	}
	for tfType := range wantRows {
		t.Errorf("%s: committed but no longer regenerated", tfType)
	}
	if gotM.Counts != wantM.Counts {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantM.Counts, gotM.Counts)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/mapping-gen` and review the diff", mappingJSONRel)
}
