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
	"reflect"
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

// testSources loads every committed input buildMapping needs - the two
// rosters, the overlay, issue #52's two generated/sourced tables, and issue
// #53's two taxonomy-classifier corroboration sources - the same way
// main.go's run() does, so every test below exercises the real production
// join rather than a hand-trimmed subset of it. former2Usable is returned
// separately since a couple of tests (the curated-68 pin) want an empty
// Sources.Former2 instead of the real one.
func testSources(t *testing.T) (tfTypes, cfnTypes []string, overlay Overlay, generatedAliases map[string][]string, former2Usable map[string]string, registryHandlerless, identitySchema map[string]bool) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	tfTypes, err = loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err = cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	cfnWithPrimaryID, err := cfnRoster.WithPrimaryIdentifier()
	if err != nil {
		t.Fatalf("loading primaryIdentifier presence: %v", err)
	}
	registryHandlerless, err = cfnRoster.HandlerlessTypes()
	if err != nil {
		t.Fatalf("loading handler presence: %v", err)
	}
	overlay, err = loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}
	identitySchema, err = loadIdentitySchemaSignals(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading identity_schema signals: %v", err)
	}

	tfSet := make(map[string]bool, len(tfTypes))
	for _, tf := range tfTypes {
		tfSet[tf] = true
	}
	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, c := range cfnTypes {
		cfnSet[c] = true
	}

	generatedAliases = PinnedNamesData.Aliases
	former2Usable, _ = filterFormer2Rows(PinnedFormer2.Rows, cfnWithPrimaryID, cfnSet, tfSet)
	return tfTypes, cfnTypes, overlay, generatedAliases, former2Usable, registryHandlerless, identitySchema
}

// TestCurated68Pin regenerates the join restricted to live/SURVEY.md's
// curated 68 and pins issue #43's own acceptance numbers: 59 mapped
// (28 by name, 31 by the overlay's aliases), 9 not mapped (fold, or one of
// issue #53's terminal taxonomy values - the one curated waiter,
// aws_acm_certificate_validation, is via:tf-only today, not via:none, since
// its overlay entry moved from nones to tf_only), and 0 unexplained - every
// curated type the heuristic does not reach has an overlay entry that says
// why. former2 and the generated service-alias table are both left out here
// on purpose: this pin is issue #43's own historical acceptance number, over
// the overlay's original hand aliases alone, and must stay stable regardless
// of what issue #52's sources or issue #53's taxonomy later grow to cover.
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

	mapping, _, _, err := buildMapping(curated, cfnTypes, Sources{Overlay: overlay})
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	// notMapped is deliberately Types-Mapped rather than a sum over the
	// individual not-mapped vias (Fold, TFOnly, CFNUnmodeled,
	// DeprecatedService, Unclassified): the historical "9" this test pins
	// is "everything that isn't Mapped," and stays true across a via being
	// renamed or a curated row moving from one terminal class to another
	// (as aws_acm_certificate_validation did, nones -> tf_only, when issue
	// #53 landed) without this test needing to track which taxonomy value
	// each of the 9 currently carries.
	notMapped := mapping.Counts.Types - mapping.Counts.Mapped
	var unexplained []string
	for _, row := range mapping.Rows {
		if row.Via == viaNone && row.Note != nil && *row.Note == unexplainedNote {
			unexplained = append(unexplained, row.TFType)
		}
	}

	if mapping.Counts.Mapped != 59 {
		t.Errorf("mapped = %d, want 59", mapping.Counts.Mapped)
	}
	if notMapped != 9 {
		t.Errorf("types-mapped = %d, want 9", notMapped)
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
// be deleted, not carried forever). heuristic_overrides (issue #58) gets
// the mirror-image version of that same "still needed" check - see the
// table's own loop below.
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
	// heuristic_overrides' two-way check (issue #58) is the mirror image of
	// the alias check above: an alias is stale when the heuristic DOES
	// derive it (redundant curation, delete the alias); an override is
	// stale when the heuristic does NOT derive a hit for it any more (the
	// CFN type it used to wrongly claim was renamed or removed out from
	// under it), because at that point there is no heuristic hit left to
	// override - delete the entry.
	for tf := range overlay.HeuristicOverrides {
		if !tfSet[tf] {
			t.Errorf("heuristic_overrides entry for %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, tf, tfRosterRel)
			continue
		}
		if _, ok := index[tf]; !ok {
			t.Errorf("heuristic_overrides entry for %s is stale: the name heuristic no longer derives any hit for %s to override; delete the overlay entry", tf, tf)
		}
	}
	for tf := range overlay.Nones {
		if !tfSet[tf] {
			t.Errorf("none entry for %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, tf, tfRosterRel)
		}
	}
	for tf := range overlay.TFOnly {
		if !tfSet[tf] {
			t.Errorf("tf_only entry for %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, tf, tfRosterRel)
		}
	}
	for tf := range overlay.CFNUnmodeled {
		if !tfSet[tf] {
			t.Errorf("cfn_unmodeled entry for %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, tf, tfRosterRel)
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

// TestServiceAliasSourceAgreement is issue #52's own staleness ratchet for
// the service_aliases table specifically: any overlay entry the generated
// names_data.hcl table now agrees with exactly is dead weight (the source
// already gives the identical answer - see mergedServiceAliases), and must
// be deleted from overlay.json rather than carried forever, the same
// "delete me" spirit as TestOverlayTwoWayStaleness's per-type alias check
// above, called out explicitly by name since issue #52 asked for it in
// those words.
func TestServiceAliasSourceAgreement(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}
	generated := PinnedNamesData.Aliases

	for prefix, hand := range overlay.ServiceAliases {
		gen, ok := generated[prefix]
		if !ok {
			continue
		}
		if reflect.DeepEqual(gen, hand) {
			t.Errorf("service_aliases[%q] = %v agrees exactly with the names_data.hcl-generated table; delete me from overlay.json (tools/mapping-gen/namesdata-generated.json already gives this answer)", prefix, hand)
		}
	}
}

// TestNoUnflaggedCollisions is the full-roster collision test: no two TF
// types resolve to the same CFN type unless the sharing is explicit -
// via:alias, a curated row someone deliberately wrote - rather than two
// independent via:name hits from the heuristic landing on the same CFN
// type, which the heuristic itself has no way to notice or resolve.
func TestNoUnflaggedCollisions(t *testing.T) {
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)

	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
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

// TestHeuristicOverrideSkipsNameHeuristic is classifyRow's own unit test for
// issue #58's heuristic_overrides table, exercised directly against a
// synthetic index/overlay rather than the real rosters: a TF type the name
// heuristic would otherwise hit falls through to via:none (unclaimed by any
// other source) once it is listed in HeuristicOverrides, and an
// unlisted sibling with the identical index entry still resolves via:name
// as normal - proving the override is scoped to the one type, not a global
// toggle.
func TestHeuristicOverrideSkipsNameHeuristic(t *testing.T) {
	index := map[string]string{
		"aws_lex_bot":         "AWS::Lex::Bot",
		"aws_other_untouched": "AWS::Other::Untouched",
	}
	cfnSet := map[string]bool{"AWS::Lex::Bot": true, "AWS::Other::Untouched": true}
	ov := Overlay{
		HeuristicOverrides: map[string]string{
			"aws_lex_bot": "the heuristic hit is wrong - see the issue #58 reasoning",
		},
	}

	row, ambiguous, err := classifyRow("aws_lex_bot", index, cfnSet, ov, nil, nil, serviceIndexCache{})
	if err != nil {
		t.Fatalf("classifyRow: %v", err)
	}
	if len(ambiguous) != 0 {
		t.Fatalf("classifyRow: unexpected ambiguous candidates %v", ambiguous)
	}
	if row.Via != viaNone {
		t.Fatalf("aws_lex_bot: via = %q, cfn_type = %v - want via:none (the override must suppress the name-heuristic hit entirely, not merely change it)", row.Via, row.CFNType)
	}
	if row.CFNType != nil {
		t.Fatalf("aws_lex_bot: cfn_type = %v, want nil once the heuristic hit is overridden", *row.CFNType)
	}

	// The untouched sibling, sharing the same index and overlay but no
	// override entry of its own, must still resolve normally.
	row2, _, err := classifyRow("aws_other_untouched", index, cfnSet, ov, nil, nil, serviceIndexCache{})
	if err != nil {
		t.Fatalf("classifyRow: %v", err)
	}
	if row2.Via != viaName || row2.CFNType == nil || *row2.CFNType != "AWS::Other::Untouched" {
		t.Fatalf("aws_other_untouched: via = %q, cfn_type = %v - want an ordinary via:name hit, unaffected by another type's override", row2.Via, row2.CFNType)
	}
}

// TestLexV1BotFamilyReclassified pins issue #58's fix at the row level,
// through the real, committed overlay (base + overlay.d/fix-58-lex.json)
// rather than a synthetic one: aws_lex_bot and its sibling
// aws_lex_bot_alias - the legacy Lex V1 bot and its alias resource - both
// name-heuristic-hit a CFN type whose own docs say it models Lex V2 only
// (AWS::Lex::Bot / AWS::Lex::BotAlias), so both must land on
// via:cfn-unmodeled, not via:name, once the overlay's heuristic_overrides
// and cfn_unmodeled entries are in play. aws_lexv2models_bot keeps the
// alias those V1 CFN types actually belong to, and aws_lex_intent/
// aws_lex_slot_type (already cfn_unmodeled before this issue, and never a
// heuristic hit in the first place - the registry has no AWS::Lex::Intent
// or AWS::Lex::SlotType type) stay exactly where they were.
func TestLexV1BotFamilyReclassified(t *testing.T) {
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	byType := make(map[string]Row, len(mapping.Rows))
	for _, r := range mapping.Rows {
		byType[r.TFType] = r
	}

	for _, tf := range []string{"aws_lex_bot", "aws_lex_bot_alias"} {
		row, ok := byType[tf]
		if !ok {
			t.Fatalf("%s: not in the regenerated mapping at all", tf)
		}
		if row.Via != viaCFNUnmodeled {
			t.Errorf("%s: via = %q, cfn_type = %v - want via:cfn-unmodeled with no cfn_type (issue #58: the name heuristic's hit for this V1 type is a V2-only CFN type)", tf, row.Via, row.CFNType)
			continue
		}
		if row.CFNType != nil {
			t.Errorf("%s: cfn_type = %v, want nil for a cfn-unmodeled row", tf, *row.CFNType)
		}
		if row.Note == nil || *row.Note == "" {
			t.Errorf("%s: cfn-unmodeled row carries no note", tf)
		}
	}

	if row, ok := byType["aws_lexv2models_bot"]; !ok || row.Via != viaAlias || row.CFNType == nil || *row.CFNType != "AWS::Lex::Bot" {
		t.Errorf("aws_lexv2models_bot: want via:alias -> AWS::Lex::Bot unchanged, got via=%q cfn_type=%v", row.Via, row.CFNType)
	}

	for _, tf := range []string{"aws_lex_intent", "aws_lex_slot_type"} {
		row, ok := byType[tf]
		if !ok || row.Via != viaCFNUnmodeled {
			t.Errorf("%s: want via:cfn-unmodeled (unchanged by issue #58), got %+v", tf, row)
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
// it is deliberately not one of these). The service pairing itself now
// comes from names_data.hcl (vpc, db, dx, route53_resolver, ssoadmin,
// sesv2) or agrees with it (cloudwatch_log, cloudwatch_event) rather than
// being hand-picked, but the resolution these anchors exercise is
// unchanged.
func TestServiceAliasAnchorsResolve(t *testing.T) {
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
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
// run against the real, full roster, both must still be via:none (or,
// conceivably, via:former2 - but never via:service-alias) - neither
// "amplify" nor "appsync" is a service_aliases prefix in either the overlay
// or the generated table, so nothing in servicealias.go can ever reach
// across into CodePipeline or Cassandra for them.
func TestServiceAliasFalsePositiveGuard(t *testing.T) {
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
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
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
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
		// aws_cloudwatch_event_bus_policy manages a whole EventBridge bus
		// policy document (PutBusPolicy); aws_cloudwatch_event_permission
		// manages one statement within that same document (PutPermission) -
		// two TF-side granularities over the one CFN resource,
		// AWS::Events::EventBusPolicy, the same shape as the alb/lb pairs
		// above (issue #53 family sweep A, verified against both resources'
		// own provider docs).
		{"aws_cloudwatch_event_bus_policy", "aws_cloudwatch_event_permission"}: true,
		// issue #53's codeartifact..ec2 family sweep: a Direct Connect
		// hosted virtual interface (allocated by the connection/LAG owner
		// on behalf of another account) and an owner-created one are the
		// same CFN resource type - AWS::DirectConnect::{Private,Public,Transit}VirtualInterface's
		// own CFN docs say so explicitly ("Hosted virtual interfaces are
		// supported by the CloudFormation resource for ... virtual
		// interfaces") and each type carries an
		// Allocate*VirtualInterfaceRoleArn property specifically for the
		// hosted case, not a separate type.
		{"aws_dx_hosted_private_virtual_interface", "aws_dx_private_virtual_interface"}: true,
		{"aws_dx_hosted_public_virtual_interface", "aws_dx_public_virtual_interface"}:   true,
		{"aws_dx_hosted_transit_virtual_interface", "aws_dx_transit_virtual_interface"}: true,
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

// TestFormer2ContradictionsAcknowledged is issue #52's own gate on former2
// disagreeing with an already-mapped row: "a conflict report in the
// artifact header and a test failure until resolved - do not silently
// prefer either." The report lives in live/mapping.json's own
// former2_contradictions (Mapping.Former2Contradictions); this test is the
// failure that stays red until each one is acknowledged here (with the
// reasoning for keeping the existing mapped answer over former2's) or fixed
// upstream in the overlay/former2-rows.json. The allowlist shape - and the
// "a stale entry fails too" rule - mirrors
// TestServiceAliasCrossHeuristicCollisions immediately above.
func TestFormer2ContradictionsAcknowledged(t *testing.T) {
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	// acknowledged names every TF type former2 is known to disagree with,
	// and why the row's existing answer (not former2's) is kept.
	acknowledged := map[string]string{
		"aws_ec2_transit_gateway_vpc_attachment": "the name heuristic's AWS::EC2::TransitGatewayVpcAttachment is the specific VPC-attachment type; former2's AWS::EC2::TransitGatewayAttachment is the base type every attachment kind (VPC, peering, Direct Connect gateway, ...) shares a parent with, not the one this TF resource itself creates",
		"aws_elasticsearch_domain":               "aws_elasticsearch_domain is TF's deprecated pre-rename resource and its own docs still document the classic AWS::Elasticsearch::Domain type; AWS::OpenSearchService::Domain (former2's answer) is what aws_opensearch_domain, a distinct TF resource, maps to",
		"aws_iam_policy":                         "AWS::IAM::Policy is CloudFormation's own inline/attached-policy resource for a customer managed policy created directly (matches aws_iam_policy's own shape - a standalone Policy resource with Users/Roles/Groups); AWS::IAM::ManagedPolicy (former2's answer) is CloudFormation's separate managed-policy-as-first-class-resource type, closer to a different TF shape",
	}

	found := map[string]bool{}
	for _, c := range mapping.Former2Contradictions {
		found[c.TFType] = true
		if _, ok := acknowledged[c.TFType]; !ok {
			t.Errorf("unacknowledged former2 contradiction: %s is mapped to %s (via %s), but former2 says %s; add %s to this test's acknowledged map with the reasoning for keeping the existing answer, or fix the disagreement at its source",
				c.TFType, c.MappedCFN, c.MappedVia, c.Former2CFN, c.TFType)
		}
	}
	for tf := range acknowledged {
		if !found[tf] {
			t.Errorf("acknowledged former2 contradiction %s no longer happens; remove it from this test's acknowledged map", tf)
		}
	}
}

// TestFormer2SampleRowsResolveCorrectly eyeballs a fixed sample of
// former2-sourced rows against both live rosters: issue #52's own warning
// that former2 is community data, so a wrong entry maps a wrong resource.
// Every TF type here must actually resolve via:former2 in the real
// artifact, and its CFN type must carry a primaryIdentifier - the same
// checks filterFormer2Rows already enforces mechanically, asserted again
// here by name so a reviewer scanning this test sees the sample the
// report's own "sample 20 former2-sourced conversions" asked for, not just
// a count.
func TestFormer2SampleRowsResolveCorrectly(t *testing.T) {
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	byType := make(map[string]Row, len(mapping.Rows))
	for _, r := range mapping.Rows {
		byType[r.TFType] = r
	}

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	withPrimaryID, err := cfnRoster.WithPrimaryIdentifier()
	if err != nil {
		t.Fatalf("loading primaryIdentifier presence: %v", err)
	}

	// A fixed 20-row random sample of former2-only conversions (types the
	// plain name/alias/service-alias heuristics could not reach on their
	// own - see mapping.Counts.ByVia["former2"]), eyeballed against the AWS
	// provider's own docs and live/registry.json at the time this test was
	// written: every pairing here is a real resource type on both sides,
	// not a coincidental name collision (aws_elasticache_cluster really is
	// AWS::ElastiCache::CacheCluster despite the name mismatch the plain
	// heuristic could not bridge; aws_launch_configuration really is
	// AWS::AutoScaling::LaunchConfiguration, TF's older name for the same
	// resource EC2's LaunchTemplate later superseded).
	sample := map[string]string{
		"aws_ssoadmin_account_assignment":            "AWS::SSO::Assignment",
		"aws_customer_gateway":                       "AWS::EC2::CustomerGateway",
		"aws_autoscaling_schedule":                   "AWS::AutoScaling::ScheduledAction",
		"aws_flow_log":                               "AWS::EC2::FlowLog",
		"aws_elasticache_cluster":                    "AWS::ElastiCache::CacheCluster",
		"aws_eip_association":                        "AWS::EC2::EIPAssociation",
		"aws_docdb_cluster":                          "AWS::DocDB::DBCluster",
		"aws_config_aggregate_authorization":         "AWS::Config::AggregationAuthorization",
		"aws_redshift_subnet_group":                  "AWS::Redshift::ClusterSubnetGroup",
		"aws_cognito_identity_pool_roles_attachment": "AWS::Cognito::IdentityPoolRoleAttachment",
		"aws_securityhub_account":                    "AWS::SecurityHub::Hub",
		"aws_network_interface":                      "AWS::EC2::NetworkInterface",
		"aws_cloudtrail":                             "AWS::CloudTrail::Trail",
		"aws_vpn_gateway":                            "AWS::EC2::VPNGateway",
		"aws_ses_event_destination":                  "AWS::SES::ConfigurationSetEventDestination",
		"aws_egress_only_internet_gateway":           "AWS::EC2::EgressOnlyInternetGateway",
		"aws_vpc_ipv4_cidr_block_association":        "AWS::EC2::VPCCidrBlock",
		"aws_elb":                                    "AWS::ElasticLoadBalancing::LoadBalancer",
		"aws_launch_configuration":                   "AWS::AutoScaling::LaunchConfiguration",
		"aws_autoscaling_policy":                     "AWS::AutoScaling::ScalingPolicy",
	}

	for tf, want := range sample {
		row, ok := byType[tf]
		if !ok {
			t.Errorf("%s: not in the regenerated mapping at all", tf)
			continue
		}
		if row.Via != viaFormer2 {
			t.Errorf("%s: via = %q, want %q (test's sample is stale - the type is now resolved by an earlier heuristic, which is good news but means it should move out of this sample)", tf, row.Via, viaFormer2)
			continue
		}
		if row.CFNType == nil || *row.CFNType != want {
			t.Errorf("%s: cfn_type = %v, want %q", tf, row.CFNType, want)
			continue
		}
		if !withPrimaryID[want] {
			t.Errorf("%s: cfn_type %s carries no primaryIdentifier in %s - filterFormer2Rows should have dropped this row", tf, want, cfnRosterRel)
		}
		if !contains(cfnTypes, want) {
			t.Errorf("%s: cfn_type %s is not in the live registry", tf, want)
		}
	}
}

// TestMappingJSONMatchesCommittedInputs regenerates live/mapping.json from
// the other committed inputs (live/survey-full.json, live/registry.json,
// the overlay, and issue #52's two generated/sourced tables) and diffs it
// against the committed artifact, the same pattern tools/survey-gen's
// TestSurveyJSONAgainstHandTable uses: it reads only committed files, so it
// needs no gate.
func TestMappingJSONMatchesCommittedInputs(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)

	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
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
	// MappingCounts carries a map field (ByVia), so it is no longer
	// comparable with == - DeepEqual instead, over the whole struct so a
	// future field addition is covered here for free.
	if !reflect.DeepEqual(gotM.Counts, wantM.Counts) {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantM.Counts, gotM.Counts)
	}
	if !reflect.DeepEqual(gotM.Former2Contradictions, wantM.Former2Contradictions) {
		t.Errorf("former2_contradictions drifted: committed %+v, regenerated %+v", wantM.Former2Contradictions, gotM.Former2Contradictions)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/mapping-gen` and review the diff", mappingJSONRel)
}

// enforceNoBareNone flipped true when issue #53's last family sweep
// landed. Every row is now mapped, folded, terminally classified
// (tf-only, cfn-unmodeled, deprecated-service), or carries a curated
// per-type reason in the overlay's nones table (the thirteen
// structurally-ambiguous types in overlay.d/sweep-residual-ambiguous.json,
// where one TF resource spans several CFN types and no single alias or
// fold_parent is honest). "Bare" therefore means via:"none" with only the
// generic unexplained note - a shrug - and a provider bump that adds one
// fails the build until a human classifies it or writes its reason down.
const enforceNoBareNone = true

// TestNoBareNoneOnceEnforced is issue #53's own closing gate, landed
// disabled behind enforceNoBareNone per the issue's own instruction
// ("A mapping-gen test fails on any bare/unclassified row"). Once every
// family sweep lands and the flag flips to true, this test forbids
// live/mapping.json from ever regrowing a bare via:"none" row - a provider
// bump that adds an unclassifiable type fails the build until a human gives
// it a classification, exactly the "the mapping can never silently regrow a
// shrug" promise issue #53 makes.
func TestNoBareNoneOnceEnforced(t *testing.T) {
	if !enforceNoBareNone {
		t.Skip("disabled until issue #53's last family sweep lands; flip enforceNoBareNone to enable")
	}
	tfTypes, cfnTypes, overlay, generated, former2Usable, registryHandlerless, identitySchema := testSources(t)
	mapping, _, _, err := buildMapping(tfTypes, cfnTypes, Sources{Overlay: overlay, GeneratedServiceAliases: generated, Former2: former2Usable, RegistryHandlerless: registryHandlerless, IdentitySchema: identitySchema})
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	var bare []string
	for _, row := range mapping.Rows {
		if row.Via == viaNone && (row.Note == nil || *row.Note == unexplainedNote) {
			bare = append(bare, row.TFType)
		}
	}
	if len(bare) > 0 {
		sort.Strings(bare)
		t.Errorf("%d TF type(s) are via:\"none\" with no curated reason (a shrug): %v - classify each, or record why it cannot be classified in the overlay's nones table", len(bare), bare)
	}
}

// unclassifiedRatchetMax is issue #53's own downward ratchet, the "pinned
// unclassified-count test that must only ever decrease" the issue asks for:
// the highest live/mapping.json's counts.unclassified may be. This pass
// measured 754 via:"none" rows before classification and left 713 after
// (34 tf-only, 7 deprecated-service, 0 cfn-unmodeled - see this package's
// taxonomy.go). Every family sweep after this one only ever removes rows
// from the unclassified bucket (by mapping, folding, or terminally
// classifying them), so this ceiling only ever moves down; lower it to
// match live/mapping.json's own committed count whenever a sweep lands.
// Raising it back up is exactly the "quietly regrow a shrug" issue #53
// exists to forbid, and TestUnclassifiedCountRatchet below fails the build
// the moment a regeneration would do that.
const unclassifiedRatchetMax = 13

// TestUnclassifiedCountRatchet reads the committed live/mapping.json's own
// unclassified count and fails if it is above unclassifiedRatchetMax -
// never below: a genuine drop is welcome and simply means the constant
// above is stale and should be lowered to match, not a test failure. Reads
// the committed artifact directly rather than regenerating (unlike
// TestMappingJSONMatchesCommittedInputs, which already guards the two
// staying in sync) so this ratchet keeps working as its own, independent
// check even if that drift test's own shape changes later.
func TestUnclassifiedCountRatchet(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, mappingJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v", mappingJSONRel, err)
	}
	var m Mapping
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decoding %s: %v", mappingJSONRel, err)
	}
	if m.Counts.Unclassified > unclassifiedRatchetMax {
		t.Errorf("%s's unclassified count is %d, above the ratchet ceiling of %d (unclassifiedRatchetMax); a regeneration must never grow the unclassified bucket - if a new TF or CFN type genuinely has no mapping and no terminal classification, it still needs to land through the taxonomy (tf-only, cfn-unmodeled, deprecated-service) or an explicit, reviewed bump of this constant, not a silent increase",
			mappingJSONRel, m.Counts.Unclassified, unclassifiedRatchetMax)
	}
}
