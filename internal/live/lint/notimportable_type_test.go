// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestNotImportableVetoRunsBeforeTheSchemaFallback is
// TestMarkerlessVetoRunsBeforeTheSchemaFallback's own shape for issue #331's
// veto: a type on [identity.NotImportableTypes] with a provider identity
// schema complete enough for [identity.SynthesizeTypeIdentity] to admit it on
// its own is exactly the case that makes the ordering load-bearing. Behind
// the fallback the veto would be a no-op for such a type - the schema
// fallback has no way to know ImportResourceState fails, and never asks.
//
// The control is measured BEFORE the roster is injected into, which is not a
// stylistic choice: the veto now lives inside the fallback itself
// ([identity.NotImportable], consulted by synthesizeTypeIdentity), so asking
// the fallback about an already-vetoed type would return the veto's own
// answer and the control would prove nothing.
func TestNotImportableVetoRunsBeforeTheSchemaFallback(t *testing.T) {
	const vetoed = "aws_thing"

	if _, already := identity.NotImportableTypes[vetoed]; already {
		t.Fatalf("%s is on the real roster; this test injects it and would not be proving anything", vetoed)
	}

	cfg := loadConfigDir(t, "testdata/schema-admitted")
	signal, diags := identity.ScanConfig(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("scanning the fixture: %s", diags.Err())
	}
	schemas := thingSchema()

	// The control, taken while the type is still off the roster: without the
	// veto this exact call admits the type - see
	// TestCheckWithSchemaAdmitsTypeOutsideTable. That is why a veto behind
	// the fallback would be invisible to this test.
	if _, ok := identity.SynthesizeTypeIdentity(vetoed, schemas, signal); !ok {
		t.Fatalf("the schema fallback no longer admits %s, so this test cannot tell the two orderings apart", vetoed)
	}

	identity.NotImportableTypes[vetoed] = struct{}{}
	t.Cleanup(func() { delete(identity.NotImportableTypes, vetoed) })

	if admitted(vetoed, schemas, signal) {
		t.Errorf("admitted(%s) consulted the schema fallback before the not-importable veto: a type with no classic "+
			"Importer would come back admitted on schema evidence the veto exists to override", vetoed)
	}
	// And the fallback itself, which is what the other admission routes ask
	// and lint does not: identity.Resolve's lookupType and
	// liveimport.admittedByProviderSchema both reach admission through this
	// one call with no veto of their own.
	if _, ok := identity.SynthesizeTypeIdentity(vetoed, schemas, signal); ok {
		t.Errorf("identity.SynthesizeTypeIdentity(%s) admits a vetoed type, so every admission route that is not lint "+
			"bypasses the veto - which is exactly the gap issue #331's first fix left open", vetoed)
	}
}

// TestNotImportableVetoNeverOverridesARatifiedRow mirrors
// TestMarkerlessVetoNeverOverridesARatifiedRow: the table always wins over
// either veto, so a type this rule vetoes but row-gen has not yet stopped
// emitting a row for keeps the support its row describes. Computed against
// the two committed tables, not a named type, so it holds as the overlap
// changes and holds vacuously if it is ever empty - which issue #331's own
// fix left it, since -emit's retraction removed both overlapping rows
// (aws_iot_ca_certificate, aws_lightsail_domain) from the table in the same
// run that generated the veto roster.
func TestNotImportableVetoNeverOverridesARatifiedRow(t *testing.T) {
	var overlap int
	for typeName := range identity.NotImportableTypes {
		if _, inTable := admittedTypesV0[typeName]; !inTable {
			continue
		}
		overlap++
		if notImportableVetoed(typeName) {
			t.Errorf("%s has a ratified row and the veto claimed it anyway; the row is what ships until row-gen stops emitting it", typeName)
		}
		if !admitted(typeName, nil, nil) {
			t.Errorf("%s has a ratified row and admitted() refused it", typeName)
		}
	}
	t.Logf("%d of %d not-importable types still carry a ratified row", overlap, len(identity.NotImportableTypes))
}

// TestNotImportableRealMeasurement pins issue #331's own finding against the
// committed tables rather than a fixture: the two types the wire-only-bucket
// audit found with no classic Importer at all refuse, the four control types
// with a documented Import section still admit, and
// aws_acm_certificate_validation - the one type notImportableExempt names by
// evidence, not the veto's rule - stays admitted on its own separate,
// already-ruled nameability path. A regression in any direction here is
// either the probe silently going stale or the exemption silently widening.
func TestNotImportableRealMeasurement(t *testing.T) {
	for _, tf := range []string{"aws_iam_policy_attachment", "aws_iot_ca_certificate", "aws_lightsail_domain"} {
		if _, ok := identity.NotImportableTypes[tf]; !ok {
			t.Errorf("%s: expected on identity.NotImportableTypes (issue #331's audit found no classic Importer for it), absent", tf)
		}
		if admitted(tf, nil, nil) {
			t.Errorf("%s: admitted() with no schemas offered still says true; issue #331's whole point is that this type cannot resolve", tf)
		}
	}

	for _, tf := range []string{"aws_iam_role", "aws_s3_bucket", "aws_iam_role_policy_attachment", "aws_security_group"} {
		if _, ok := identity.NotImportableTypes[tf]; ok {
			t.Errorf("%s: on identity.NotImportableTypes; this is one of tools/importer-probe's own positive controls and has a real Importer", tf)
		}
	}

	const exempt = "aws_acm_certificate_validation"
	if _, ok := identity.NotImportableTypes[exempt]; ok {
		t.Errorf("%s: on identity.NotImportableTypes; notImportableExempt (tools/row-gen/notimportable.go) is supposed to keep it off "+
			"the veto roster because its admission is a separate, already-ruled nameability decision, not this issue's importability axis", exempt)
	}
}
