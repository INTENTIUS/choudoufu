// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// The plan is only as good as blockerAction, and a lookup table checked
// against nothing drifts the moment a refusal is renamed. Both directions are
// enforced here against live/corpus-refusals.json, which is produced by a
// different generator (tools/corpus-gen) from a different input than this
// package - so neither can quietly agree with the other.

// TestEveryFiredRefusalHasAnAction is the forwards leg: a refusal that
// actually blocked something in the last sweep and has no action is a blocker
// nobody has decided how to act on, and it would print as "?" in the plan.
func TestEveryFiredRefusalHasAnAction(t *testing.T) {
	fired := firedRefusals(t)
	if len(fired) < 10 {
		t.Fatalf("only %d refusals fired in the committed corpus artifact; the artifact is not being read properly and this check would pass vacuously", len(fired))
	}
	for _, id := range fired {
		if _, ok := blockerAction[id]; !ok {
			t.Errorf("refusal %q fired in live/corpus-refusals.json and blockerAction does not mention it.\n"+
				"Every blocker needs an action class, and the Reason is the deliverable: say WHERE the identity is, because that is what decides whether this is DERIVE, ADMIT, DEFER, RULE or PARITY.", id)
		}
	}
}

// TestEveryActionNamesARealRefusal is the backwards leg. An entry for a
// refusal the catalog no longer defines is dead weight that reads as
// coverage - the failure mode that has appeared three times in this
// repository under other names.
func TestEveryActionNamesARealRefusal(t *testing.T) {
	known := knownRefusalIDs(t)
	if len(known) < 50 {
		t.Fatalf("only %d refusal IDs in the committed corpus artifact; the read is broken and this check would pass vacuously", len(known))
	}
	for id := range blockerAction {
		if !known[id] {
			t.Errorf("blockerAction has an entry for %q, which is not a refusal ID the corpus artifact defines.\n"+
				"Either it was renamed - update the key - or it is gone, and the entry should go with it.", id)
		}
	}
}

// TestEveryActionCarriesAReason exists because an entry without one is a
// verdict with no argument, and the next reader cannot tell a considered
// classification from a guess.
func TestEveryActionCarriesAReason(t *testing.T) {
	for id, a := range blockerAction {
		switch a.Action {
		case ActionDerive, ActionAdmit, ActionDefer, ActionRule, ActionParity, ActionRead:
		default:
			t.Errorf("%q has action %q, which is not one of the five", id, a.Action)
		}
		if len(strings.Fields(a.Reason)) < 8 {
			t.Errorf("%q's reason is %d words: %q\nIt has to say where the identity is, not restate the refusal's title.", id, len(strings.Fields(a.Reason)), a.Reason)
		}
	}
}

// TestPlanIsOrderedAndStable pins the two properties an assignment rule needs:
// the first line is the estate with the fewest blockers, and two people
// planning from one sweep get the same first line.
func TestPlanIsOrderedAndStable(t *testing.T) {
	s := sweep{Entries: []sweepEntry{
		{Name: "z-two-blockers", Origin: ratePopulation, Blocked: true, Sites: 2,
			Refusals: map[string]int{"unadmitted-type": 1, "logical-resource": 1}},
		{Name: "a-one-blocker-many-sites", Origin: ratePopulation, Blocked: true, Sites: 90,
			Refusals: map[string]int{"unadmitted-type": 90}},
		{Name: "b-one-blocker-one-site", Origin: ratePopulation, Blocked: true, Sites: 1,
			Refusals: map[string]int{"unadmitted-type": 1}},
		{Name: "not-a-deployment", Origin: "in-repo fixture", Blocked: true, Sites: 1,
			Refusals: map[string]int{"unadmitted-type": 1}},
		{Name: "already-clean", Origin: ratePopulation, Blocked: false, Sites: 0},
	}}

	plan, unknown := buildPlan(s, nil)
	if len(unknown) != 0 {
		t.Fatalf("fixture uses refusals with no action: %v", unknown)
	}
	got := []string{plan[0].Name, plan[1].Name, plan[2].Name}
	want := []string{"b-one-blocker-one-site", "a-one-blocker-many-sites", "z-two-blockers"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("plan[%d] = %q, want %q (fewest blockers first, then fewest sites)", i, got[i], want[i])
		}
	}
	if len(plan) != 3 {
		t.Errorf("plan holds %d estates, want 3: the fixture and the clean deployment must both be excluded", len(plan))
	}

	// Stability: the same sweep planned twice gives the same order. Map
	// iteration over Refusals is the thing that would break this.
	for range 20 {
		again, _ := buildPlan(s, nil)
		for j := range again {
			if again[j].Name != plan[j].Name {
				t.Fatalf("plan is not stable across runs: position %d was %q, now %q", j, plan[j].Name, again[j].Name)
			}
		}
	}
}

// TestInformationalFindingIsNotABlocker pins the correction an audit forced
// the same day this tool was written.
//
// dataread's SummaryEligibleRead declares itself "not a refusal", and
// ClassifyOnboarding lands such an estate on data-read-eligible rather than
// language-blocked - but Report.Blocked() is len(Findings) > 0, so the first
// version of this tool counted it and put a class no fix removes at the top of
// the board.
func TestInformationalFindingIsNotABlocker(t *testing.T) {
	const read = "Resolves at plan time via a data-source read"
	if blockerAction[read].Action != ActionRead {
		t.Fatalf("%q is classified %q; it is explicitly not a refusal", read, blockerAction[read].Action)
	}

	s := sweep{Entries: []sweepEntry{
		{Name: "reads-only", Origin: ratePopulation, Blocked: true, Sites: 9,
			Refusals: map[string]int{read: 9}},
		{Name: "one-real-blocker-plus-reads", Origin: ratePopulation, Blocked: true, Sites: 10,
			Refusals: map[string]int{read: 9, "unadmitted-type": 1}},
	}}

	plan, _ := buildPlan(s, nil)
	if len(plan) != 1 {
		t.Fatalf("plan holds %d estates, want 1: an estate whose only findings are informational is not blocked", len(plan))
	}
	e := plan[0]
	if e.Name != "one-real-blocker-plus-reads" {
		t.Fatalf("plan[0] = %q", e.Name)
	}
	if len(e.Blockers) != 1 || e.Blockers[0].ID != "unadmitted-type" {
		t.Errorf("blockers = %+v, want just unadmitted-type", e.Blockers)
	}
	// Still printed: the read has to succeed against a real cloud, and that
	// is step 6's job. Dropping it entirely would hide real remaining work.
	if len(e.Informs) != 1 || e.Informs[0].ID != read {
		t.Errorf("informational findings = %+v, want the read carried alongside", e.Informs)
	}
}

// TestUnmappedRefusalIsReportedNotSwallowed proves the "?" path works, since
// a new refusal appearing is exactly when the plan is most misleading.
func TestUnmappedRefusalIsReportedNotSwallowed(t *testing.T) {
	s := sweep{Entries: []sweepEntry{
		{Name: "e", Origin: ratePopulation, Blocked: true, Sites: 1,
			Refusals: map[string]int{"a-refusal-nobody-classified": 1}},
	}}
	plan, unknown := buildPlan(s, nil)
	if len(unknown) != 1 || unknown[0] != "a-refusal-nobody-classified" {
		t.Fatalf("unmapped refusal not reported, got %v", unknown)
	}
	if plan[0].Blockers[0].Action != "?" {
		t.Errorf("unmapped blocker rendered as %q, want \"?\"", plan[0].Blockers[0].Action)
	}
}

// TestModuleExamplesQueuedNotRated is the load-bearing guard for the split
// between the rate and the module-example work queue. It pins three things
// at once: rateTotal never counts a terraform-aws-modules entry, buildPlan
// (the rate queue) never returns one, and buildModuleQueue returns exactly
// the module entries and none of the rate ones. A population predicate that
// goes vacuous - matching every origin instead of just its own - fails this
// test, because the module entry would then leak into rateTotal and plan.
func TestModuleExamplesQueuedNotRated(t *testing.T) {
	entryType := func(name, origin string, blocked bool, sites int, refusals map[string]int) sweepEntry {
		return sweepEntry{Name: name, Origin: origin, Blocked: blocked, Sites: sites, Refusals: refusals}
	}

	s := sweep{Entries: []sweepEntry{
		entryType("real-estate", ratePopulation, true, 3, map[string]int{"unadmitted-type": 3}),
		entryType("clean-estate", ratePopulation, false, 0, nil),
		entryType(".corpus/iam/examples/iam-role", modulePopulation, true, 5, map[string]int{"unadmitted-type": 5}),
		entryType(".corpus/vpc/examples/clean", modulePopulation, false, 0, nil),
		entryType("some-fixture", "in-repo fixture", true, 1, map[string]int{"unadmitted-type": 1}),
	}}

	if got := rateTotal(s); got != 2 {
		t.Fatalf("rateTotal = %d, want 2: a module example (and a fixture) must not count toward the rate-capable total", got)
	}
	if got := moduleTotal(s); got != 2 {
		t.Fatalf("moduleTotal = %d, want 2", got)
	}

	plan, unknown := buildPlan(s, nil)
	if len(unknown) != 0 {
		t.Fatalf("fixture uses refusals with no action: %v", unknown)
	}
	if len(plan) != 1 || plan[0].Name != "real-estate" {
		t.Fatalf("buildPlan = %+v, want just real-estate: a module example must never appear in the rate plan", plan)
	}

	modules, unknown := buildModuleQueue(s, nil)
	if len(unknown) != 0 {
		t.Fatalf("fixture uses refusals with no action: %v", unknown)
	}
	if len(modules) != 1 || modules[0].Name != ".corpus/iam/examples/iam-role" {
		t.Fatalf("buildModuleQueue = %+v, want just the one blocked module example: a rate deployment or fixture must never appear in the module queue", modules)
	}
}

type corpusArtifact struct {
	Refusals []struct {
		ID      string `json:"id"`
		Configs int    `json:"configs"`
	} `json:"refusals"`
}

func readCorpusArtifact(t *testing.T) corpusArtifact {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "live", "corpus-refusals.json")
		if b, err := os.ReadFile(p); err == nil {
			var a corpusArtifact
			if err := json.Unmarshal(b, &a); err != nil {
				t.Fatalf("parsing %s: %s", p, err)
			}
			return a
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("live/corpus-refusals.json not found above the package directory; this test guards a table against it, so its absence is a failure rather than a skip")
		}
		dir = parent
	}
}

func firedRefusals(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, r := range readCorpusArtifact(t).Refusals {
		if r.Configs > 0 {
			out = append(out, r.ID)
		}
	}
	return out
}

func knownRefusalIDs(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, r := range readCorpusArtifact(t).Refusals {
		out[r.ID] = true
	}
	return out
}

// TestRefineAppliesTheScopeRulingOnlyWhenNoAWSTypeIsLeft covers the rule that
// takes an estate out of the queue, and the guard that keeps it in.
//
// The first version of this compared the count of distinct provider PREFIXES
// against the count of TYPES, so a two-type google estate read as mixed and
// stayed classified ADMIT. It sorted to the front of the board while being
// out of scope, which is the exact failure the rule exists to prevent.
func TestRefineAppliesTheScopeRulingOnlyWhenNoAWSTypeIsLeft(t *testing.T) {
	for _, tc := range []struct {
		name   string
		causes map[string]int
		want   Action
	}{
		{"one foreign type", map[string]int{"type:sentry_project": 1}, ActionRule},
		{"two foreign types, one provider", map[string]int{
			"type:google_storage_bucket": 1, "type:google_storage_bucket_acl": 1}, ActionRule},
		{"two foreign types, two providers", map[string]int{
			"type:tfe_variable": 1, "type:google_service_account": 1}, ActionRule},
		{"wholly aws stays admission work", map[string]int{
			"type:aws_iam_user_group_membership": 1}, ActionAdmit},
		{"one aws type among foreign ones keeps the blocker in scope", map[string]int{
			"type:google_storage_bucket": 1, "type:aws_s3_bucket_inventory": 1}, ActionAdmit},
		{"two aws types among foreign ones, still in scope", map[string]int{
			"type:kubernetes_namespace_v1": 1, "type:aws_ec2_tag": 1, "type:aws_default_vpc": 1}, ActionAdmit},
		{"no type-shaped cause at all", map[string]int{"reference:data_source": 1}, ActionAdmit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, note := refine("unadmitted-type", tc.causes, ActionAdmit, nil)
			if got != tc.want {
				t.Errorf("refine(unadmitted-type, %v) action = %s, want %s", tc.causes, got, tc.want)
			}
			// A note is owed whenever refine says something the ID alone does
			// not: either it reclassified the blocker, or it left the action
			// alone and flagged that this estate still cannot reach zero.
			// The only silent case is the one the ID already settles.
			mixed := strings.HasPrefix(note, "MIXED SCOPE:")
			switch {
			case got == ActionRule && note == "":
				t.Error("a reclassified blocker must carry the reason it was reclassified")
			case got == ActionAdmit && note != "" && !mixed:
				t.Errorf("an unchanged action may only carry a MIXED SCOPE note; got %q", note)
			}
			if mixed && !anyAWS(causeTypes(tc.causes)) {
				t.Error("MIXED SCOPE claims in-scope work exists, so at least one aws_ type must be present")
			}
		})
	}
}

// TestRefineRecordAdmittedEffectsAreAnnotatedNotReclassified pins that the
// pre-onboarding artifact is explained without being downgraded: the estate
// still carries the blocker, because the operator does have to declare a
// record_store before it clears.
func TestRefineRecordAdmittedEffectsAreAnnotatedNotReclassified(t *testing.T) {
	admitted := map[string]int{"type:random_pet": 1, "type:terraform_data": 1, "type:null_resource": 1}
	got, note := refine("logical-resource", admitted, ActionDefer, nil)
	if got != ActionRead {
		t.Errorf("action = %s, want %s - a blocker no work here clears, whose promise is enforced "+
			"downstream, is informational rather than blocking", got, ActionRead)
	}
	if note == "" {
		t.Error("an all-record-admitted logical blocker must say the record_store clears it")
	}

	// Secret-bearing logical types are genuinely refused, so one of them in
	// the set must withdraw the note entirely rather than weaken it.
	withSecret := map[string]int{"type:random_pet": 1, "type:random_password": 1}
	gotSecret, note := refine("logical-resource", withSecret, ActionDefer, nil)
	if note != "" {
		t.Errorf("random_password is not record-admitted, so the set is not clearable by a "+
			"record_store and must carry no note; got %q", note)
	}
	if gotSecret != ActionDefer {
		t.Errorf("action = %s, want %s - one secret-bearing type is enough to keep the whole "+
			"blocker blocking, because no record_store admits it", gotSecret, ActionDefer)
	}
}

// aMarkerlessType returns one type from identity.MarkerlessTypes with no
// ratified row, chosen deterministically so a failure names the same type
// twice running.
func aMarkerlessType(t *testing.T) string {
	t.Helper()
	names := make([]string, 0, len(identity.MarkerlessTypes))
	for name := range identity.MarkerlessTypes {
		if _, ratified := identity.LookupType(name); ratified {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("identity.MarkerlessTypes is empty of unratified types, so every assertion below would be vacuous")
	}
	sort.Strings(names)
	return names[0]
}

// TestRefineMarkerlessTypeIsDemotedOnlyWhenLocatedAndSchemaBacked is
// acceptance criterion (b): markerless-type demotes to ActionRead only when
// every cause type is identity.LocatedType, and never without schemas in
// hand - the same fail-closed direction identity.LocatedType itself takes.
func TestRefineMarkerlessTypeIsDemotedOnlyWhenLocatedAndSchemaBacked(t *testing.T) {
	typeName := aMarkerlessType(t)
	locatableSchemas := map[string]providers.Schema{typeName: {Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		},
	}}}
	causes := map[string]int{"type:" + typeName: 1}

	// No schemas at all: fails closed, exactly as it read before #270.
	got, note := refine("markerless-type", causes, ActionDefer, nil)
	if got != ActionDefer {
		t.Errorf("action = %s, want %s - markerless-type must not demote with no schemas to check the "+
			"credential exclusion against", got, ActionDefer)
	}
	if note != "" {
		t.Errorf("no note expected with no schemas; got %q", note)
	}

	// Schemas present, type is located: demoted, and it must say so.
	got, note = refine("markerless-type", causes, ActionDefer, locatableSchemas)
	if got != ActionRead {
		t.Errorf("action = %s, want %s - every cause type is identity.LocatedType under this schema", got, ActionRead)
	}
	if note == "" {
		t.Error("a wholly-located markerless blocker must say the record_store clears it")
	}

	// One credential-bearing type in the set withdraws the note and keeps
	// the whole blocker blocking - the same shape recordAdmittedAll pins for
	// logical-resource, and the reason a roster-free predicate is safe here.
	credentialSchemas := map[string]providers.Schema{typeName: {Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"secret": {Type: cty.String, Computed: true, Sensitive: true},
		},
	}}}
	got, note = refine("markerless-type", causes, ActionDefer, credentialSchemas)
	if got != ActionDefer {
		t.Errorf("action = %s, want %s - credential material must keep the blocker blocking even under a schema", got, ActionDefer)
	}
	if note != "" {
		t.Errorf("credential material must withdraw the note entirely; got %q", note)
	}
}

// TestDriveableSortsRuledAndUnsetVariableEstatesToTheBack pins the ordering
// property the board is read for, and that they stay counted.
func TestDriveableSortsRuledAndUnsetVariableEstatesToTheBack(t *testing.T) {
	s := sweep{Entries: []sweepEntry{
		{Name: "ruled", Origin: ratePopulation, Blocked: true, Sites: 1,
			Refusals: map[string]int{"unadmitted-type": 1},
			Causes:   map[string]map[string]int{"unadmitted-type": {"type:sentry_project": 1}}},
		{Name: "unset-var", Origin: ratePopulation, Blocked: true, Sites: 1, UnsetVarSites: 1,
			Refusals: map[string]int{"Non-static count expression": 1}},
		{Name: "mixed-scope", Origin: ratePopulation, Blocked: true, Sites: 1,
			Refusals: map[string]int{"unadmitted-type": 1},
			Causes: map[string]map[string]int{"unadmitted-type": {
				"type:aws_secretsmanager_secret_version": 1, "type:google_service_account": 1}}},
		{Name: "driveable", Origin: ratePopulation, Blocked: true, Sites: 9,
			Refusals: map[string]int{"unadmitted-type": 1},
			Causes:   map[string]map[string]int{"unadmitted-type": {"type:aws_s3_bucket_inventory": 1}}},
	}}

	plan, unknown := buildPlan(s, nil)
	if len(unknown) != 0 {
		t.Fatalf("unexpected unmapped refusals: %v", unknown)
	}
	if len(plan) != 4 {
		t.Fatalf("every estate stays in the plan and in the blocked count, got %d", len(plan))
	}
	if plan[0].Name != "driveable" {
		t.Errorf("first line = %q, want %q - the driveable estate leads even though it carries "+
			"the most sites, because nothing here moves the other two", plan[0].Name, "driveable")
	}
	for _, e := range plan[1:] {
		if e.driveable() {
			t.Errorf("%s sorted to the back but reports driveable", e.Name)
		}
	}
	// The mixed-scope estate is the subtle one: its blocker is real ADMIT
	// work that buys OTHER estates, so the action must not be downgraded -
	// but this estate cannot reach zero while the rest of the blocker is out
	// of scope, so it must not head the queue either.
	for _, e := range plan {
		if e.Name != "mixed-scope" {
			continue
		}
		if e.Blockers[0].Action != ActionAdmit {
			t.Errorf("mixed-scope action = %s, want %s - the aws_ half is still admission debt",
				e.Blockers[0].Action, ActionAdmit)
		}
		if !e.Blockers[0].Unreachable {
			t.Error("mixed-scope blocker must be marked unreachable; without it the estate " +
				"sorts ahead of work that can actually finish")
		}
		if e.driveable() {
			t.Error("mixed-scope reports driveable, but clearing its admission work leaves it blocked")
		}
	}
}

// TestUnsetVarArtifactCountsCascades pins the predicate against the estates
// it was measured on, and against the ones it must NOT claim.
//
// Every row here is an estate that was actually probed with values supplied
// for its required root variables. The clears went to zero blockers; the
// keepers did not. A predicate that cannot tell them apart takes real
// language-wall work off the board, which is worse than leaving an artifact
// on it - so the keeper rows are the ones that matter.
func TestUnsetVarArtifactCountsCascades(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		unset, blocking, cascade int
		want                     bool
	}{
		// Marked sites cover every blocker. The equality already found these.
		{"infra-vpc: 1 of 1", 1, 1, 0, true},
		{"infra-public-wafs: 3 of 3", 3, 3, 0, true},

		// The cascade shape the equality missed. The unmarked remainder is
		// entirely Unresolvable identity / Identity not resolvable, each the
		// shadow of a marked site.
		{"infra-athena-query-results: 1 marked, 1 cascade", 1, 2, 1, true},
		{"app-related-links: 2 marked, 3 cascade", 2, 5, 3, true},
		{"infra-database-backups-bucket: 4 marked, 10 cascade", 4, 14, 10, true},
		{"infra-assets: 12 marked, 2 cascade", 12, 14, 2, true},

		// Keepers. An unmarked blocker that is not a cascade is real work,
		// and one of them is enough to disqualify the estate.
		{"unmarked remainder exceeds the cascade sites", 4, 14, 9, false},
		{"marked, but every other blocker is independent", 1, 5, 0, false},
		{"no marked site at all - not a candidate", 0, 3, 3, false},

		// Degenerate inputs. A probe that marked more sites than block is
		// incoherent and must not be read as a clean sweep.
		{"more marked than blocking", 5, 2, 0, false},
		{"nothing blocking", 0, 0, 0, false},
	} {
		if got := unsetVarArtifact(tc.unset, tc.blocking, tc.cascade); got != tc.want {
			t.Errorf("%s: unsetVarArtifact(unset=%d, blocking=%d, cascade=%d) = %v, want %v",
				tc.name, tc.unset, tc.blocking, tc.cascade, got, tc.want)
		}
	}
}

// TestCascadeSitesCountsOnlyTheShadowClasses stops the remainder being
// covered by a refusal that carries its own independent claim.
func TestCascadeSitesCountsOnlyTheShadowClasses(t *testing.T) {
	got := cascadeSites([]blocker{
		{ID: "Unresolvable identity", Sites: 3},
		{ID: "Identity not resolvable from configuration", Sites: 2},
		{ID: "unadmitted-type", Sites: 7},
		{ID: "markerless-type", Sites: 4},
		{ID: "Non-static count expression", Sites: 5},
	})
	if got != 5 {
		t.Errorf("cascadeSites = %d, want 5 - only the two shadow refusals count; "+
			"an unadmitted type or a markerless one is a finding in its own right and "+
			"supplying a variable does not touch it", got)
	}
}
