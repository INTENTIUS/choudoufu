// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/liveimport"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
	residue "github.com/intentius/choudoufu/live"
)

// This file is the guard the first fix of issue #331 did not have. The veto
// itself was derived correctly - tools/survey-gen probes ImportResourceState
// and tools/row-gen emits the roster - and it was then consulted from ONE
// place, internal/live/lint's admitted(). An audit on 2026-08-20 found two
// further routes into admission that never asked, and a fourth turned up
// while fixing them:
//
//   - internal/live/identity's LocatedType, the record-located route (issue
//     #270), which reaches admission with no schema fallback involved at all;
//   - internal/live/liveimport's admittedByProviderSchema, which live-import
//     uses and whose own doc comment claimed equivalence with lint;
//   - internal/live/identity's resolver.lookupType, the resolution-layer
//     schema fallback.
//
// Each was one lookup away from correct, and that is the point: the veto is
// a rule, and a rule copied into every route is a rule that comes apart the
// next time a route is added. It now lives in identity.NotImportable, and
// the routes reach it either directly (LocatedType) or through the schema
// fallback they all already share (synthesizeTypeIdentity).
//
// What this test asserts is the property, not the plumbing: every route
// refuses a not-importable type, and - measured first, on the same subject,
// before the veto exists - every route would have admitted it. Without the
// control half a route could be refusing for some unrelated reason and this
// would pass while measuring nothing.

// admissionRoute is one way a resource type reaches admission, named the way
// an operator would recognise it and reduced to the single boolean every one
// of them ultimately produces.
type admissionRoute struct {
	// name says which command an operator would be running when this route
	// decides, because the failure this test prevents is one route saying
	// yes while another says no about the same type.
	name string
	// subject is the type this route is asked about. Not all routes can be
	// asked about the same one: the located route's population is
	// identity.MarkerlessTypes, and a markerless type is refused by
	// markerlessVetoed long before the schema fallback the other routes use.
	subject string
	admits  func(t *testing.T) bool
}

// TestEveryAdmissionRouteConsultsTheNotImportableVeto is issue #331's
// cross-route consistency check.
func TestEveryAdmissionRouteConsultsTheNotImportableVeto(t *testing.T) {
	// The schema-fallback subject: a type whose provider schema settles its
	// identity completely, so every route that consults the fallback admits
	// it on schema evidence alone. That is the exact shape the veto exists
	// to override - the schemas are RIGHT about the identity and silent
	// about whether the provider will import it.
	const synthSubject = "aws_thing"
	synthSchemas := thingSchema()

	cfg := loadConfigDir(t, "testdata/schema-admitted")
	signal, diags := identity.ScanConfig(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("scanning the fixture: %s", diags.Err())
	}

	// The located subject, chosen from the real roster rather than named:
	// the located route only ever considers identity.MarkerlessTypes, so a
	// synthetic type would have to be injected into that roster too, and a
	// type in it is refused by markerlessVetoed before the other routes
	// reach their fallback.
	locatedSubject := aLocatableType(t)
	locatedSchemas := locatableSchemas(locatedSubject)

	routes := []admissionRoute{{
		name:    "internal/live/lint admitted() - live-check, live-plan",
		subject: synthSubject,
		admits: func(t *testing.T) bool {
			return admitted(synthSubject, synthSchemas, signal)
		},
	}, {
		name:    "internal/live/identity SynthesizeTypeIdentity - resolver.lookupType, the resolution-layer fallback",
		subject: synthSubject,
		admits: func(t *testing.T) bool {
			_, ok := identity.SynthesizeTypeIdentity(synthSubject, synthSchemas, signal)
			return ok
		},
	}, {
		name:    "internal/live/liveimport Ratify - live-import's admittedByProviderSchema",
		subject: synthSubject,
		admits: func(t *testing.T) bool {
			return ratifyAdmits(t, synthSubject, synthSchemas)
		},
	}, {
		name:    "internal/live/identity LocatedType - the record-located route, issue #270",
		subject: locatedSubject,
		admits: func(t *testing.T) bool {
			return identity.LocatedType(locatedSubject, locatedSchemas)
		},
	}}

	for _, route := range routes {
		if _, already := identity.NotImportableTypes[route.subject]; already {
			t.Fatalf("%s is already on identity.NotImportableTypes, so the control below cannot be taken and this test would pass without measuring anything", route.subject)
		}
		if !route.admits(t) {
			t.Fatalf("control: %s already refuses %s for some other reason, so its refusal after the veto is injected would prove nothing",
				route.name, route.subject)
		}
	}

	for _, route := range routes {
		identity.NotImportableTypes[route.subject] = struct{}{}
	}
	t.Cleanup(func() {
		for _, route := range routes {
			delete(identity.NotImportableTypes, route.subject)
		}
	})

	for _, route := range routes {
		if route.admits(t) {
			t.Errorf("%s admits %s, a type identity.NotImportableTypes says the provider will not import. "+
				"One route admitting what the others refuse is the whole defect: the type plans, applies, "+
				"creates a real object, and every run after that fails on ImportResourceState with the object already live.",
				route.name, route.subject)
		}
	}
}

// TestNotImportableRosterIsRefusedByEveryRoute is the same property asked of
// the committed roster rather than of an injected type, so that a real
// member cannot be admitted by a route the synthetic subjects above happen
// not to reach.
//
// The schemas are synthetic and generous on purpose: each type is handed the
// shape that would admit it if the veto were absent, which is what makes a
// refusal here attributable to the veto rather than to a schema that was
// never going to satisfy anything. What it cannot do is prove the real
// provider serves that shape - TestLocatedTypePopulation and this file's own
// real-schema run are for that (CHOUDOUFU_LIVE_SCHEMAS=1 in
// internal/live/identity).
func TestNotImportableRosterIsRefusedByEveryRoute(t *testing.T) {
	if len(identity.NotImportableTypes) == 0 {
		t.Fatal("identity.NotImportableTypes is empty; every assertion below would be vacuous")
	}

	// The fixture's own control, taken on types the veto does NOT claim: a
	// schema map that admits nothing admits nothing for every reason at
	// once, and the sweep below would then be a hundred assertions about a
	// broken fixture. aws_thing is outside every roster, so it exercises the
	// two schema-fallback routes; the located route needs a markerless
	// subject, and aLocatableType picks one the veto has not claimed.
	const shapeControl = "aws_thing"
	if !admitted(shapeControl, admittingSchemas(shapeControl), nil) {
		t.Fatalf("admittingSchemas does not admit %s through lint's own fallback, so refusals below say nothing about the veto", shapeControl)
	}
	if _, ok := identity.SynthesizeTypeIdentity(shapeControl, admittingSchemas(shapeControl), nil); !ok {
		t.Fatalf("admittingSchemas does not admit %s through the schema fallback, so refusals below say nothing about the veto", shapeControl)
	}
	locatedControl := aLocatableType(t)
	if !identity.LocatedType(locatedControl, admittingSchemas(locatedControl)) {
		t.Fatalf("admittingSchemas does not admit %s as record-located, so the located refusals below say nothing about the veto", locatedControl)
	}

	var located int
	for typeName := range identity.NotImportableTypes {
		if _, hasRow := identity.LookupType(typeName); hasRow {
			// Table wins over the veto, deliberately and by the same rule
			// markerlessVetoed applies - TestNotImportableVetoNeverOverrides
			// ARatifiedRow is where that direction is asserted.
			continue
		}

		schemas := admittingSchemas(typeName)
		if admitted(typeName, schemas, nil) {
			t.Errorf("lint admitted() admits %s", typeName)
		}
		if _, ok := identity.SynthesizeTypeIdentity(typeName, schemas, nil); ok {
			t.Errorf("identity.SynthesizeTypeIdentity admits %s, so identity.Resolve and live-import both admit it too", typeName)
		}
		if identity.LocatedType(typeName, schemas) {
			t.Errorf("identity.LocatedType admits %s as record-located", typeName)
		}
		if _, markerless := identity.MarkerlessTypes[typeName]; markerless {
			// Counted rather than asserted on: this is the population the
			// audit found - a type in both rosters is one the located route
			// would have admitted the moment an estate declared a
			// record_store, which is precisely what the refusal text for it
			// tells an operator to do.
			located++
		}
	}
	t.Logf("%d of %d not-importable types are also markerless, and so were reachable through the record-located route",
		located, len(identity.NotImportableTypes))
}

// admittingSchemas is the schema shape that satisfies BOTH families of
// route at once, so that one call can ask every route about the same type
// and get an answer that came from the veto rather than from a schema the
// route was never going to accept: a top-level string id for the located
// route, and thingSchema's identity shape - one required identity attribute
// that is also a required argument, with the AWS context pair as the only
// optional-for-import attributes - for the schema fallback.
//
// The sibling type is not decoration. #218 made "is this attribute context"
// a derived rule, and one clause of it is that a second, independently
// authored type in the same provider has to treat the name the same way; a
// single-type map cannot stand the shape up, and the fallback would refuse
// for that reason instead of the one under test.
func admittingSchemas(typeName string) map[string]providers.Schema {
	const sibling = "aws_other_thing"
	if typeName == sibling {
		// Nothing in the roster is called this, but a map cannot hold one
		// key twice and a silent collision would quietly halve the fixture.
		panic("the corroborating sibling collides with the subject")
	}
	return fakeProviderSchemas(map[string]fakeType{
		typeName: {
			args:     map[string]string{"id": "optcomp", "name": "req"},
			identity: map[string]string{"name": "req", "account_id": "opt", "region": "opt"},
		},
		sibling: {
			args:     map[string]string{"label": "req"},
			identity: map[string]string{"label": "req", "account_id": "opt", "region": "opt"},
		},
	})
}

// TestNotImportableRosterIsRefusedAgainstRealSchemas is the sweep above
// against the schemas the pinned provider actually serves, rather than
// against a fixture shaped to admit. It is the run that answers the question
// the synthetic version cannot: whether any real member of the roster has a
// real schema that a real route would take.
//
// Set CHOUDOUFU_LIVE_SCHEMAS=1 to run it; it installs hashicorp/aws.
func TestNotImportableRosterIsRefusedAgainstRealSchemas(t *testing.T) {
	if os.Getenv("CHOUDOUFU_LIVE_SCHEMAS") == "" {
		t.Skip("set CHOUDOUFU_LIVE_SCHEMAS=1 to install hashicorp/aws and measure every route against the schemas it really serves")
	}
	schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
		InitBin:  "terraform",
		WorkDir:  t.TempDir(),
		Source:   "hashicorp/aws",
		Version:  residue.EvidenceVersion(),
		Provider: addrs.NewDefaultProvider("aws"),
	})
	if err != nil {
		t.Fatalf("acquiring hashicorp/aws schemas: %s", err)
	}

	var served, refused []string
	for typeName := range identity.NotImportableTypes {
		if _, hasRow := identity.LookupType(typeName); hasRow {
			continue
		}
		if _, ok := schemas[typeName]; !ok {
			continue
		}
		served = append(served, typeName)
		var routes []string
		if admitted(typeName, schemas, nil) {
			routes = append(routes, "lint admitted()")
		}
		if _, ok := identity.SynthesizeTypeIdentity(typeName, schemas, nil); ok {
			routes = append(routes, "identity.SynthesizeTypeIdentity (resolution and live-import)")
		}
		if identity.LocatedType(typeName, schemas) {
			routes = append(routes, "identity.LocatedType (record-located)")
		}
		if ratifyAdmits(t, typeName, schemas) {
			routes = append(routes, "liveimport.Ratify (live-import)")
		}
		if len(routes) > 0 {
			t.Errorf("%s is admitted against the real provider schema by: %s", typeName, strings.Join(routes, ", "))
			continue
		}
		refused = append(refused, typeName)
	}
	sort.Strings(refused)
	if len(served) == 0 {
		t.Fatal("the provider serves a schema for none of the roster, so this run measured nothing")
	}
	t.Logf("%d of %d not-importable types are served by the pinned provider, and every route refuses all %d",
		len(served), len(identity.NotImportableTypes), len(refused))
}

// ratifyAdmits runs live-import's own ratification over a one-resource state
// carrying typeName, through a provider that serves schemas and nothing
// else, and reports whether the type cleared the admission gate.
//
// It drives the exported [liveimport.Ratify] rather than the unexported
// predicate underneath it, so the route measured is the one an operator
// running "choudoufu live-import" actually takes. StatusUnadmittedType is
// the only verdict that means "refused at the gate"; every other verdict
// (MISSING here, because the fake provider reports no live object) means the
// gate let the type through.
func ratifyAdmits(t *testing.T, typeName string, schemas map[string]providers.Schema) bool {
	t.Helper()

	state := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: "one"}.
				Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"name":"one"}`),
			},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("aws"), Module: addrs.RootModule},
			addrs.NoKey,
		)
	})

	rat, diags := liveimport.Ratify(context.Background(), liveimport.Request{
		Estate:    "acme",
		State:     state,
		Providers: ratifyProviders{provider: ratifyProvider(schemas)},
	})
	if diags.HasErrors() {
		t.Fatalf("liveimport.Ratify: %s", diags.Err())
	}
	if len(rat.Entries) != 1 {
		t.Fatalf("liveimport.Ratify returned %d entries for a one-resource state", len(rat.Entries))
	}
	return rat.Entries[0].Status != liveimport.StatusUnadmittedType
}

// ratifyProviders is [liveimport.Providers] over one provider.
type ratifyProviders struct{ provider providers.Interface }

func (p ratifyProviders) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return p.provider, nil
}

// ratifyProvider serves schemas and answers every read with "no such
// object", which is all [ratifyAdmits] needs: the question is whether the
// admission gate was passed, not what was found on the other side of it.
func ratifyProvider(schemas map[string]providers.Schema) providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: schemas,
		},
	}
	p.ConfigureProviderCalled = true
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
	}
	return p
}
