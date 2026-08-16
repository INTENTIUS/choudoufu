// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestCyclicIdentityDiagnosticsNeedsNoProvider is #224's projection half:
// the same cycle [TestBuildCyclicFormulas] catches by running the whole
// [BuildFrom] pipeline against a fake cloud is caught here from resolutions
// alone - the function signature does not even take a provider or a
// configuration - proving [orderWork]'s cyclic classification really does
// not need either.
func TestCyclicIdentityDiagnosticsNeedsNoProvider(t *testing.T) {
	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	diags := CyclicIdentityDiagnostics([]identity.Resolution{
		derivedOn(rtb, route, "id", "-x"),
		derivedOn(route, rtb, "id", "-y"),
	})

	if !diags.HasErrors() {
		t.Fatal("a cycle among parent-derived identities produced no error")
	}
	if !hasDiag(diags, "Cyclic parent-derived identities", "cycle") {
		t.Errorf("wrong diagnostics for a cycle:\n%s", renderDiags(diags))
	}
}

// TestCyclicIdentityDiagnosticsCleanOverNonCyclic pins the other direction:
// a resolution set with no cycle - the fixture's own real classification -
// produces nothing.
func TestCyclicIdentityDiagnosticsCleanOverNonCyclic(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg).All()

	diags := CyclicIdentityDiagnostics(resolutions)
	if diags.HasErrors() {
		t.Errorf("a non-cyclic resolution set produced errors:\n%s", renderDiags(diags))
	}
}

// oneSchema hands back one fixed schema regardless of what is asked for,
// which is all [TestEmptyImportIdentityDiagnostics...] needs: the point is
// exercising [importTarget]'s own decision, not schema lookup.
type oneSchema struct{ schema providers.Schema }

func (f oneSchema) ResourceTypeConfig(addrs.Provider, addrs.ResourceMode, string) (*providers.Schema, uint64) {
	return &f.schema, 0
}

// TestEmptyImportIdentityDiagnosticsFlagsAnUnbuildableComposite mirrors
// [TestImportTargetRefusesRatherThanApproximatingACompositeID], but through
// the exported, provider-free entry point rather than by calling
// importTarget/importAndRead directly: a concrete resolution with no import
// ID and only half of a composite identity's required attributes leaves
// [importTarget] with neither an identity object nor a string, which is
// exactly what [Build] would refuse with "Empty import identity" - computed
// here with no provider and no live call at all.
func TestEmptyImportIdentityDiagnosticsFlagsAnUnbuildableComposite(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	diags := EmptyImportIdentityDiagnostics(cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "", IdentityValues: map[string]string{"cluster": "prod"}},
	}, oneSchema{schema: compositeSchema()})

	if !diags.HasErrors() {
		t.Fatal("a concrete resolution with no import ID and half a composite identity produced no error")
	}
	if !hasDiag(diags, "Empty import identity", "") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestEmptyImportIdentityDiagnosticsCleanWhenImportIDPresent pins the other
// direction: an ordinary import-ID string is a usable target even under a
// schema whose identity object cannot be built from what the resolution
// supplied, exactly as [importTarget]'s string fallback intends.
func TestEmptyImportIdentityDiagnosticsCleanWhenImportIDPresent(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	diags := EmptyImportIdentityDiagnostics(cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, oneSchema{schema: compositeSchema()})

	if diags.HasErrors() {
		t.Errorf("a concrete resolution with an import ID string produced errors:\n%s", renderDiags(diags))
	}
}

// TestEmptyImportIdentityDiagnosticsSkipsParentDerived: a
// [identity.ClassParentDerived] resolution is never checked, because its
// identity depends on a parent's live value this entry point never has -
// see the function's own doc comment. Handing it one with an empty formula
// target must not panic or misreport it as empty-by-configuration.
func TestEmptyImportIdentityDiagnosticsSkipsParentDerived(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	diags := EmptyImportIdentityDiagnostics(cfg, []identity.Resolution{
		derivedOn(route, rtb, "id", "_0.0.0.0/0"),
	}, oneSchema{schema: compositeSchema()})

	if diags.HasErrors() {
		t.Errorf("a parent-derived resolution was evaluated for an empty import identity, which needs a live parent value this entry point does not have:\n%s", renderDiags(diags))
	}
}
