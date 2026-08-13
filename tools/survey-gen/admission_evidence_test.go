// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// evidenceException is one admitted type whose client-named admission the
// strict schema rule cannot prove, with the reason a human ratified it
// anyway. Visible, not silent, the same contract as pathExceptions: the
// test fails on an admitted client-named type with neither derivability nor
// an entry here, and on an entry the schemas have started proving (remove
// it, the admission is mechanical now).
type evidenceException struct {
	// reason says what the hand-ratified evidence is and why the schema
	// cannot carry it.
	reason string
}

// admissionEvidenceExceptions is the full list of admitted types whose
// SURVEY.md path is client-named but whose evidence is not
// identity.Derivable. Two cohorts, both already documented row by row in
// pathExceptions (survey_gen_test.go):
//
//   - name-prefix idiom: the identifying argument is Optional+Computed
//     because a *_prefix alternative exists, and the strict rule
//     deliberately stops at required arguments
//     (internal/live/identity/doc.go). The ratified evidence is the
//     provider's documented import grammar.
//   - docs tier: v6.58.0 ships no identity schema at all, so the only
//     identity-side evidence is the documented import grammar.
//
// A provider release that promotes one of these (a required identity
// argument, or a first identity schema) makes the entry stale, and this
// test says so rather than leaving a hand-written exception standing in
// for evidence the schemas now carry.
var admissionEvidenceExceptions = map[string]evidenceException{
	// --- name-prefix idiom (Optional+Computed identifying argument) ---
	"aws_s3_bucket":            {"bucket is Optional+Computed (bucket_prefix idiom), the archetype in identity/doc.go; documented import grammar is the bucket name"},
	"aws_iam_role":             {"name is Optional+Computed (name_prefix idiom); documented import grammar is the role name"},
	"aws_cloudwatch_log_group": {"name is Optional+Computed (name_prefix idiom); documented import grammar is the log group name"},
	"aws_iam_role_policy":      {"name is Optional+Computed (name_prefix idiom); documented import grammar is ROLENAME:POLICYNAME, both halves client-chosen"},
	"aws_kms_alias":            {"name is Optional+Computed (name_prefix idiom); documented import grammar is the full alias/... name"},
	"aws_iam_instance_profile": {"name is Optional+Computed (name_prefix idiom); documented import grammar is the instance profile name (registry-ratified, #40/#44/#26)"},

	// --- docs tier: no identity schema in v6.58.0 ---
	"aws_ecs_cluster": {"no identity schema in v6.58.0; documented import grammar is the cluster name, and the provider sets id to the ARN"},
}

// admittedParents names the already-admitted parents each composite-wired
// admitted type derives its identity from, mirrored from the Components in
// internal/live/identity/table.go. Hand-written on purpose: which
// referenced type has to be a *managed, admitted* parent is a judgment
// (aws_iam_role_policy_attachment's policy_arn may name an AWS-managed
// policy nobody manages, and aws_lb_target_group_attachment's target_id an
// instance outside the estate), so the map records the claim and the test
// checks the claim still holds.
var admittedParents = map[string][]string{
	"aws_route": {"aws_route_table"},
	// The association's first component reads whichever of subnet_id and
	// gateway_id the configuration sets, so both referenced types must stay
	// admitted alongside the route table.
	"aws_route_table_association": {"aws_subnet", "aws_internet_gateway", "aws_route_table"},
	// policy_arn is a client-supplied ARN string that may name an
	// AWS-managed policy, so aws_iam_policy is deliberately not required
	// here; the role half is the managed parent.
	"aws_iam_role_policy_attachment": {"aws_iam_role"},
	// target_id may name an instance or a bare IP outside the estate; the
	// target group's live ARN is the managed parent.
	"aws_lb_target_group_attachment": {"aws_lb_target_group"},
	// SURVEY.md's path for the record is client-named (its name and type
	// are), but the fork wires it as a composite through the zone marker
	// because zone_id is the parent's server-assigned Z-ID (flag F5), so
	// its admission also rests on the zone staying admitted.
	"aws_route53_record": {"aws_route53_zone"},
}

// TestAdmissionEvidenceAgainstProviderSchemas re-checks, for every admitted
// type that is also a row in live/SURVEY.md's curated-68 table (the two
// rosters this test actually has hand evidence for), that the evidence its
// admission was ratified on still holds against the pinned provider's live
// schemas and the committed survey artifact:
//
//   - a client-named type is still identity.Derivable, or carries a
//     documented exception above;
//   - a marker type is still taggable, so the tag-filtered list can still
//     recover it;
//   - a parent-derived type's parents are still admitted (admittedParents);
//   - an account-derived type's identity-table entry still composes every
//     attribute the provider requires for import, and the type is still
//     taggable for the context-less fallback;
//   - the eip-shaped list+content type is still taggable (the tofu-slot
//     marker) and still has a native list resource;
//   - and no admitted type is classified moves-to-Ops or excluded by rule
//     in the committed survey.json.
//
// This test's purpose is curated-survey evidence, not global admission
// coverage: it walks live/SURVEY.md's hand roster, so it has nothing to
// check for a registry-ratified type outside the curated 68 (issue #40's
// tiering; issue #54 generalized the split). Those types skip this test's
// per-type checks entirely rather than failing it for lacking a hand
// roster row they were never meant to have - the registry-ratified
// universe's own admission evidence is exercised by tools/row-gen's own
// tests (row_gen_test.go, classify_test.go), which read live/registry.json
// and live/survey-full.json instead of this file's provider-schema survey.
//
// The test never edits anything. Every failure names the type and says
// "review this": either the provider moved under the pin and the admission
// table needs a human decision, or the tables drifted and the drift is the
// review material.
//
// Gated with the floci tier like TestSurveyJSONMatchesProviderSchemas: it
// needs a terraform binary and the provider download, but no emulator and
// no cloud.
//
//	TF_FLOCI_TEST=1 go test ./tools/survey-gen/ -run TestAdmissionEvidenceAgainstProviderSchemas -v
func TestAdmissionEvidenceAgainstProviderSchemas(t *testing.T) {
	flocitest.Gate(t, "admission evidence")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	// The hand table's per-type paths class each admitted type.
	handRows, err := readRoster(filepath.Join(root, surveyMDRel))
	if err != nil {
		t.Fatalf("parsing %s's per-type table: %v", surveyMDRel, err)
	}
	handPath := map[string]string{}
	for _, r := range handRows {
		handPath[r.Type] = r.Path
	}

	// The committed artifact, for the ops/excluded cross-check.
	data, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading %s (regenerate with `go run ./tools/survey-gen`): %v", surveyJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		t.Fatalf("decoding %s: %v", surveyJSONRel, err)
	}
	surveyRow := map[string]Row{}
	for _, r := range survey.Types {
		surveyRow[r.Type] = r
	}

	// The live schemas, and the strict client-named judgment over them.
	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring the provider schemas: %v", err)
	}
	derivable := map[string]bool{}
	for _, d := range identity.Derivable(schemas.ResourceTypes) {
		derivable[d.Type] = true
	}

	admitted := identity.AdmittedTypes()
	admittedSet := map[string]bool{}
	for _, typeName := range admitted {
		admittedSet[typeName] = true
	}

	usedExceptions := map[string]bool{}
	for _, typeName := range admitted {
		// Scope to the curated survey's own rows (issue #54): this test's
		// purpose is re-checking curated-68 evidence, not walking the whole
		// admission table. A registry-ratified type outside live/SURVEY.md's
		// roster (issue #40's tiering) has no hand-classified path here to
		// re-check, so it is not a gap this test should name - the
		// registry-ratified universe is covered by tools/row-gen's own
		// tests instead (see the doc comment above).
		row, inSurvey := surveyRow[typeName]
		if !inSurvey {
			continue
		}

		// Admitted-but-excluded is the loudest drift there is: the survey
		// says the type left the resource model and the admission table
		// still carries it.
		if reason, ok := opsExcluded[typeName]; ok {
			t.Errorf("%s is admitted but excluded by rule (%s); review this: the admission table and the exclusion cannot both be right", typeName, reason)
		}
		if row.Path == pathOps {
			t.Errorf("%s is admitted but %s classifies it %q (%s); review this: either the admission or the classification has to move", typeName, surveyJSONRel, row.Path, row.Evidence)
		}

		path, inTable := handPath[typeName]
		if !inTable {
			t.Errorf("%s is admitted but not in %s's per-type table; review this: every admitted type must carry a surveyed path", typeName, surveyMDRel)
			continue
		}

		rs, hasSchema := schemas.ResourceTypes[typeName]
		if !hasSchema || rs.Block == nil {
			t.Errorf("%s is admitted but the provider serves no schema for it; review this: the type may have been removed in v%s", typeName, providerVersion)
			continue
		}

		switch path {
		case pathClientNamed:
			exc, hasExc := admissionEvidenceExceptions[typeName]
			switch {
			case derivable[typeName]:
				if hasExc {
					usedExceptions[typeName] = true // reported here, not again as unused
					t.Errorf("%s: stale evidence exception (the schemas now prove client-naming); review this and remove the entry", typeName)
				}
			case hasExc:
				usedExceptions[typeName] = true
				t.Logf("documented evidence exception %s: %s", typeName, exc.reason)
			default:
				t.Errorf("%s is admitted client-named but has no identity.Derivable evidence and no documented exception (survey evidence: %s); review this: ratify the hand evidence in admissionEvidenceExceptions or reclassify the type",
					typeName, row.Evidence)
			}

		case pathMarker:
			if !taggable(rs.Block) {
				t.Errorf("%s is admitted on the marker path but is no longer taggable in v%s; review this: the tag-filtered list cannot recover it", typeName, providerVersion)
			}

		case pathParentDerived:
			if _, ok := admittedParents[typeName]; !ok {
				t.Errorf("%s is admitted parent-derived but admittedParents has no entry for it; review this: name its parents so their admission is checked", typeName)
			}

		case pathAccountDerived:
			entry, ok := identity.LookupType(typeName)
			if !ok {
				t.Errorf("%s is admitted account-derived but has no identity-table entry; review this", typeName)
				continue
			}
			if len(cloudValuesOf(typeName)) == 0 {
				t.Errorf("%s is admitted account-derived but its identity-table entry names no cloud value; review this: the token and the entry disagree", typeName)
			}
			if rs.IdentitySchema == nil {
				t.Errorf("%s is admitted account-derived but v%s ships no identity schema for it; review this: the template has nothing to compose against", typeName, providerVersion)
			} else if required, _ := identityAttrNames(rs.IdentitySchema); !entry.ComposesIdentity(required) {
				t.Errorf("%s: the identity-table entry no longer composes every required-for-import attribute (%s); review this: the provider's identity schema moved under the pin",
					typeName, strings.Join(required, ", "))
			}
			if !taggable(rs.Block) {
				t.Errorf("%s is admitted account-derived but is no longer taggable in v%s; review this: a run without a CloudContext falls back to the marker path, which needs tags", typeName, providerVersion)
			}

		case pathListContent:
			// The eip shape: a fungible set bound by the tofu-slot marker,
			// enumerated by the native list resource.
			if !taggable(rs.Block) {
				t.Errorf("%s is admitted on the list+content path but is no longer taggable in v%s; review this: the tofu-slot marker needs tags", typeName, providerVersion)
			}
			if _, hasList := schemas.ListResourceTypes[typeName]; !hasList {
				t.Errorf("%s is admitted on the list+content path but v%s ships no native list resource for it; review this: nothing enumerates the fungible set", typeName, providerVersion)
			}

		case pathOps:
			t.Errorf("%s is admitted but %s's table says %q; review this: an excluded type cannot stay admitted", typeName, surveyMDRel, path)

		default:
			t.Errorf("%s carries path %q, which this test does not know how to evidence; review this test", typeName, path)
		}
	}

	// Stale-entry sweeps, the pathExceptions contract: an exception or a
	// parent claim nothing uses anymore is drift hiding in plain sight.
	for typeName := range admissionEvidenceExceptions {
		switch {
		case !admittedSet[typeName]:
			t.Errorf("evidence exception for %s, which is not admitted; review this and remove the entry", typeName)
		case handPath[typeName] != pathClientNamed:
			t.Errorf("evidence exception for %s, whose %s path is %q rather than client-named; review this and remove the entry", typeName, surveyMDRel, handPath[typeName])
		case !usedExceptions[typeName]:
			t.Errorf("stale evidence exception for %s: no admitted client-named type uses it; review this and remove the entry", typeName)
		}
	}
	for typeName, parents := range admittedParents {
		if !admittedSet[typeName] {
			t.Errorf("admittedParents names %s, which is not admitted; review this and remove the entry", typeName)
			continue
		}
		for _, parent := range parents {
			if !admittedSet[parent] {
				t.Errorf("%s derives its identity from %s, which is no longer admitted; review this: the composite has an unadmitted parent", typeName, parent)
			}
			if _, ok := schemas.ResourceTypes[parent]; !ok {
				t.Errorf("%s's recorded parent %s is not a resource type the provider serves; review this entry", typeName, parent)
			}
		}
	}
}
