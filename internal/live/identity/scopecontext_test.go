// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// projectScopeSchemas is the google provider's shape for GitHub issue #200,
// stated here rather than read from a provider so the test carries its own
// premise. Three properties are load-bearing and all three are real:
//
//   - google_project_service's identity schema requires "service" and marks
//     "project" optional for import, so [identityCandidates] makes "service"
//     the identity and "project" context, and the synthesized entry's only
//     component is "service". That is why three copies in three projects
//     resolve to one identity string.
//   - "project" is Optional+Computed in the resource block, which is what
//     [locallyDefaultable] needs to call it context at all.
//   - A second type marks "project" the same way, because [isContextAttr]
//     requires corroboration from a type other than the one under test
//     (#228) and would otherwise refuse google_project_service outright.
//
// google_project's own identity is project_id, and its "number" is Computed
// and is no identity attribute - the shape the unresolvable-scope case in
// testdata/scope-context leans on.
func projectScopeSchemas() map[string]providers.Schema {
	stringAttr := func(a *configschema.Attribute) *configschema.Attribute { return a }
	return map[string]providers.Schema{
		"google_project": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"project_id": stringAttr(&configschema.Attribute{Type: cty.String, Required: true}),
					"number":     stringAttr(&configschema.Attribute{Type: cty.String, Computed: true}),
				},
			},
			IdentitySchema: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"project_id": stringAttr(&configschema.Attribute{Type: cty.String, Required: true}),
				},
			},
		},
		"google_project_service": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"project": stringAttr(&configschema.Attribute{Type: cty.String, Optional: true, Computed: true}),
					"service": stringAttr(&configschema.Attribute{Type: cty.String, Required: true}),
				},
			},
			IdentitySchema: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"project": stringAttr(&configschema.Attribute{Type: cty.String, Optional: true}),
					"service": stringAttr(&configschema.Attribute{Type: cty.String, Required: true}),
				},
			},
		},
		// Corroboration only; no block of this type appears in the fixture.
		"google_iam_workload_identity_pool": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"project":                   stringAttr(&configschema.Attribute{Type: cty.String, Optional: true, Computed: true}),
					"workload_identity_pool_id": stringAttr(&configschema.Attribute{Type: cty.String, Required: true}),
				},
			},
			IdentitySchema: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"project":                   stringAttr(&configschema.Attribute{Type: cty.String, Optional: true}),
					"workload_identity_pool_id": stringAttr(&configschema.Attribute{Type: cty.String, Required: true}),
				},
			},
		},
	}
}

// TestScopeContextPremise pins what makes the rest of this file mean
// anything: google_project_service really does resolve to an identity that
// carries the service and nothing else. If a later change puts "project"
// into the identity itself, these tests would keep passing for the wrong
// reason, and this is the assertion that catches it.
func TestScopeContextPremise(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-context"), nil)
	result, _ := ResolveWith(context.Background(), cfg, Context{Schemas: projectScopeSchemas()})

	for addr, service := range map[string]string{
		"google_project_service.sibling_one":     "iam.googleapis.com",
		"google_project_service.sibling_two":     "iam.googleapis.com",
		"google_project_service.literal_one":     "run.googleapis.com",
		"google_project_service.literal_two":     "run.googleapis.com",
		"google_project_service.dup_a":           "storage.googleapis.com",
		"google_project_service.dup_b":           "storage.googleapis.com",
		"google_project_service.known_project":   "logging.googleapis.com",
		"google_project_service.unknown_project": "logging.googleapis.com",
	} {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", addr, res.Class)
			continue
		}
		if res.ImportID != service {
			t.Errorf("%s import ID is %q, want %q", addr, res.ImportID, service)
		}
		if got := res.IdentityValues["service"]; got != service {
			t.Errorf("%s service is %q, want %q", addr, got, service)
		}
		if got, carried := res.IdentityValues["project"]; carried {
			t.Errorf("%s identity carries project=%q; this test's whole premise is that it does not", addr, got)
		}
	}

	if got := len(result.All()); got != 10 {
		t.Errorf("resolved %d instances, want 10 (2 projects + 8 services)", got)
	}
}

// TestDistinctProjectsDoNotCollide is GitHub issue #200: two resources with
// the same identity string in two different GCP projects are two live
// objects, and refusing them refuses a configuration that works.
//
// Both spellings are here because only one of them was the regression. The
// literal pair is what a static evaluation can already read; the sibling
// pair is the shape the corpus actually hit, where the project is named
// only by a reference to another resource whose own identity resolved.
func TestDistinctProjectsDoNotCollide(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-context"), nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: projectScopeSchemas()})

	for _, addr := range []string{
		"google_project_service.sibling_two",
		"google_project_service.literal_two",
	} {
		if hasDiag(diags, "Two resources with the same identity", addr) {
			t.Errorf("%s was reported as a collision, but its project differs from its pair's:\n%s", addr, renderDiags(diags))
		}
	}

	// Reading a project through a sibling must not itself produce a
	// refusal. The probe that reads it is the only thing in the run that
	// looks at "project" at all, and a refusal it raises would be a refusal
	// over an argument the identity does not need.
	for _, d := range diags {
		if d.Severity() != 1 { // tfdiags.Error
			continue
		}
		if strings.Contains(d.Description().Detail, "google_project.one.number") ||
			d.Description().Summary == "Not an identity attribute" {
			t.Errorf("the scope probe raised a refusal of its own: [%s] %s", d.Description().Summary, d.Description().Detail)
		}
	}
}

// TestSameProjectStillCollides is the other direction, and the one this fix
// could quietly break: two blocks that really do name one live object in one
// project must still be refused.
func TestSameProjectStillCollides(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-context"), nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: projectScopeSchemas()})

	if !hasDiag(diags, "Two resources with the same identity", "google_project_service.dup_b") {
		t.Fatalf("dup_a and dup_b name the same service in project proj-five and were not refused:\n%s", renderDiags(diags))
	}
	if !hasDiag(diags, "Two resources with the same identity", `the identity "storage.googleapis.com"`) {
		t.Errorf("the collision did not render the identity it collided on:\n%s", renderDiags(diags))
	}
}

// TestUnresolvedProjectStaysAWildcard holds #217's safety direction through
// this change: a scope value the run could not read is not evidence of
// "somewhere else". known_project states proj-six; unknown_project's project
// is a Computed attribute of a sibling that no rule here can read. The pair
// must still be refused.
func TestUnresolvedProjectStaysAWildcard(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-context"), nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: projectScopeSchemas()})

	if !hasDiag(diags, "Two resources with the same identity", "google_project_service.unknown_project") {
		t.Fatalf("an unreadable project silently ruled out a collision, which is exactly what #217 refused to allow:\n%s", renderDiags(diags))
	}
}

// TestScopeContextRefusalCount is the arithmetic over the whole fixture, so
// that a change which clears a false collision by also clearing a true one
// fails here even if every named assertion above still passes.
func TestScopeContextRefusalCount(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-context"), nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: projectScopeSchemas()})

	var collisions int
	for _, d := range diags {
		if d.Description().Summary == "Two resources with the same identity" {
			collisions++
		}
	}
	if collisions != 2 {
		t.Fatalf("got %d duplicate-identity refusals, want exactly 2 (dup_a/dup_b, and known_project/unknown_project):\n%s", collisions, renderDiags(diags))
	}
}

// TestScopeContextNamesAreDerivedNotListed pins the derivation itself: the
// scope names come from the provider's identity schema minus whatever the
// resolved entry already says the identity is, and nothing in this package
// carries a list of provider-specific attribute names.
func TestScopeContextNamesAreDerivedNotListed(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-context"), nil)
	r := newResolver(context.Background(), cfg, Context{Schemas: projectScopeSchemas()})
	r.signal, _ = ScanConfig(context.Background(), cfg)

	if got := r.scopeContextNames("google_project_service"); len(got) != 1 || got[0] != "project" {
		t.Errorf("google_project_service scope names are %v, want [project]", got)
	}
	// google_project's identity IS project_id, so the entry already names
	// it and nothing is left over for the scope.
	if got := r.scopeContextNames("google_project"); len(got) != 0 {
		t.Errorf("google_project scope names are %v, want none: project_id is its identity", got)
	}
	// A type the run has no schema for contributes nothing, which is what
	// keeps a schema-less run comparing exactly as it did before.
	if got := r.scopeContextNames("aws_s3_bucket"); len(got) != 0 {
		t.Errorf("aws_s3_bucket scope names are %v, want none: no schema was supplied for it", got)
	}
}
