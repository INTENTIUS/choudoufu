// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// zeroRequiredSchemas are the shapes an identity schema that requires
// nothing for import comes in. They are transcribed from hashicorp/google
// 7.44.0, read through internal/live/pluginschema, not invented to agree
// with the rule:
//
//   - google_cert is google_compute_managed_ssl_certificate: identity
//     optional-for-import {name, project}, nothing required, and the block
//     carries name (Optional+Computed, the provider will invent one) and no
//     project argument at all. 32 of google's 1342 types have the
//     zero-required shape and 18 of them reduce to a candidate set like
//     this one.
//   - google_flow is google_workflows_workflow: identity optional-for-import
//     {name, project, region}, and region is Optional *without* Computed in
//     the block, so the block itself says the provider will not fill it in
//     and it stays part of what identifies an instance.
//   - google_topic is the control - google_pubsub_topic's shape, one
//     required-for-import attribute - so a test over this set is not
//     measuring the new rule against nothing but itself.
//   - google_settings is the singleton the old early return was right
//     about: nothing required for import, nothing but context optional.
//
// Three types carry project optional-for-import with no project argument,
// which is what [isContextAttr]'s corroboration rule needs to call it
// context without any name being written into the code.
func zeroRequiredSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"google_cert": {
			args:     map[string]string{"name": "optcomp", "domain": "req"},
			identity: map[string]string{"name": "opt", "project": "opt"},
		},
		"google_flow": {
			args:     map[string]string{"name": "optcomp", "region": "opt", "source": "req"},
			identity: map[string]string{"name": "opt", "project": "opt", "region": "opt"},
		},
		"google_topic": {
			args:     map[string]string{"topic_id": "req"},
			identity: map[string]string{"topic_id": "req", "project": "opt"},
		},
		"google_settings": {
			args:     map[string]string{"enabled": "opt"},
			identity: map[string]string{"project": "opt"},
		},
	})
}

// renderedIdentities is what every test in this file asserts on: the import
// string and identity values each instance actually resolved to, not the
// synthesizer's yes-or-no. A predicate that says "admitted" while the
// values behind it name the wrong live object is the failure mode this
// package has shipped twice, so the boolean is never the assertion.
func renderedIdentities(t *testing.T, result *Result) map[string]resolvedIdentity {
	t.Helper()
	out := map[string]resolvedIdentity{}
	for _, res := range result.All() {
		out[res.Addr.String()] = resolvedIdentity{
			class:    string(res.Class),
			importID: res.ImportID,
			values:   res.IdentityValues,
		}
	}
	return out
}

type resolvedIdentity struct {
	class    string
	importID string
	values   map[string]string
}

// TestZeroRequiredIdentityRendersTheConfiguredName is the point of the
// zero-required rule, asserted on what the resolution produced rather than
// on whether it produced anything.
//
// Before this, an identity schema with no required-for-import attribute was
// refused at [derivable] and [cohortAttrs] alike, on the reading that a
// provider requiring nothing for import has not said what names the
// resource. It has: it said the name is optional to *supply at import time*
// because the ambient scoping can be defaulted, and the configuration in
// front of us writes it on every block.
func TestZeroRequiredIdentityRendersTheConfiguredName(t *testing.T) {
	result, errText := resolveFallback(t, "zero-required-identity", zeroRequiredSchemas())
	if errText != "" {
		t.Fatalf("resolving a zero-required-for-import identity produced errors: %s", errText)
	}
	got := renderedIdentities(t, result)

	want := map[string]resolvedIdentity{
		// One candidate, so the import ID is that attribute's value.
		"google_cert.primary":   {class: "CONCRETE", importID: "front-cert", values: map[string]string{"name": "front-cert"}},
		"google_cert.secondary": {class: "CONCRETE", importID: "back-cert", values: map[string]string{"name": "back-cert"}},
		// Two candidates, so there is no separator any schema carries and
		// the entry refuses to invent one: the values travel, the string
		// does not. Same guarantee TestSynthesizedCompositeHasNoImportID
		// pins for a required-for-import composite.
		"google_flow.backups": {class: "CONCRETE", importID: "", values: map[string]string{"name": "nightly-backups", "region": "europe-west2"}},
		// The control, admitted by the unchanged required-for-import path.
		"google_topic.events": {class: "CONCRETE", importID: "events", values: map[string]string{"topic_id": "events"}},
	}
	for addr, w := range want {
		g, ok := got[addr]
		if !ok {
			t.Errorf("%s did not resolve at all", addr)
			continue
		}
		if g.class != w.class {
			t.Errorf("%s classified %q, want %q", addr, g.class, w.class)
		}
		if g.importID != w.importID {
			t.Errorf("%s rendered the import ID %q, want %q", addr, g.importID, w.importID)
		}
		if !reflect.DeepEqual(g.values, w.values) {
			t.Errorf("%s rendered the identity values %v, want %v", addr, g.values, w.values)
		}
	}

	// And the two certificates are told apart, which is the whole reason a
	// name may be an identity component at all.
	if a, b := got["google_cert.primary"], got["google_cert.secondary"]; a.importID == b.importID {
		t.Errorf("both certificates rendered the same identity %q, so one live object would have two owners", a.importID)
	}
}

// TestZeroRequiredIdentityRefusedWhenAnInstanceOmitsIt is the safety bar.
//
// An optional-for-import attribute that is also optional in the block is
// only an identity component when this configuration writes it on every
// instance, and "every" is [ConfigSignal.NamingOfType]'s unanimity rule,
// not a per-block judgement. One block declining is the whole type
// declining, because a type-level admission that half the instances
// contradict imports the wrong object for the other half.
func TestZeroRequiredIdentityRefusedWhenAnInstanceOmitsIt(t *testing.T) {
	result, errText := resolveFallback(t, "zero-required-identity-declined", zeroRequiredSchemas())
	if errText == "" {
		t.Fatalf("a type one of whose blocks never names itself was admitted anyway: %v", renderedIdentities(t, result))
	}
	if !strings.Contains(errText, `"name"`) {
		t.Errorf("the refusal does not name the attribute the configuration failed to supply: %s", errText)
	}
	if !strings.Contains(errText, "google_cert") {
		t.Errorf("the refusal does not name the type it refused: %s", errText)
	}
	// Including the block that DOES write a name: the verdict is the type's.
	if strings.Contains(errText, "composite") {
		t.Errorf("the refusal blamed a missing separator, which is not what happened: %s", errText)
	}
}

// TestZeroRequiredIdentityCollisionStillCaught: reliably present is not the
// same as injective, and the rule only supplies the first. Two blocks
// writing the same name resolve to one live certificate, and
// [resolver.checkCollisions] is what stops the run - asserted here against
// a type admitted by the new path, because a rule that admitted a type
// past that check would be worse than the refusal it replaced.
func TestZeroRequiredIdentityCollisionStillCaught(t *testing.T) {
	_, errText := resolveFallback(t, "zero-required-identity-collision", zeroRequiredSchemas())
	if errText == "" {
		t.Fatal("two blocks resolving to the same certificate name were both accepted")
	}
	if !strings.Contains(errText, "same identity") {
		t.Errorf("the refusal is not the duplicate-identity one: %s", errText)
	}
}

// TestZeroRequiredIdentitySingletonStillRefused: the case the early return
// was right about survives, and now says so in its own words instead of
// borrowing a message about a missing separator.
func TestZeroRequiredIdentitySingletonStillRefused(t *testing.T) {
	_, errText := resolveFallback(t, "zero-required-identity-singleton", zeroRequiredSchemas())
	if errText == "" {
		t.Fatal("an account-wide singleton with no distinguishing value was admitted")
	}
	if !strings.Contains(errText, "requires nothing for import and marks nothing but context") {
		t.Errorf("the refusal does not say why a singleton has no identity to synthesize: %s", errText)
	}
}

// TestSchemaRefusalNamesTheGateThatFired is the second defect.
//
// aws_route is refused because the schema marks the three destination_*
// arguments optional for import and this type's own block makes each a
// settable argument, so route_table_id names a route table rather than a
// route. [SchemaRefusal] used to report that as "the identity schema for
// aws_route is a composite of \"route_table_id\"" - a composite of one
// thing, and a cause that was never the one that fired. Measured against
// hashicorp/aws 6.59.0 and hashicorp/google 7.44.0 through
// internal/live/pluginschema, that clause was reached 6 and 214 times
// respectively, and [onlyContext] was the gate every single time.
func TestSchemaRefusalNamesTheGateThatFired(t *testing.T) {
	signal := scanFixture(t, "schema-fallback-route")
	got := SchemaRefusal("aws_route", routeSchema(), signal)

	if got == "" {
		t.Fatal("aws_route was admitted, so there is no refusal to check")
	}
	if strings.Contains(got, "composite of") {
		t.Errorf("the refusal blames a missing separator for a gate that never looked at one: %s", got)
	}
	for _, want := range []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %s, which is what makes route_table_id insufficient: %s", want, got)
		}
	}
	if !strings.Contains(got, "route_table_id") {
		t.Errorf("the refusal does not name what the schema DOES settle: %s", got)
	}
}

// TestSchemaRefusalNeverDisagreesWithSynthesis is the structural half of
// the same fix, and the one that keeps it fixed.
//
// The defect was not a badly worded sentence. It was two functions deriving
// the same verdict separately, one of them carrying a list of causes that
// had gone stale when [TypeIdentity.IdentityObjectOnly] gave composite
// identities somewhere to go. Either function may grow a new gate; this
// fails if only one of them does.
func TestSchemaRefusalNeverDisagreesWithSynthesis(t *testing.T) {
	sets := map[string]struct {
		schemas map[string]providers.Schema
		fixture string
	}{
		"fallback":     {fallbackSchemas(), "schema-fallback"},
		"declined":     {fallbackSchemas(), "schema-fallback-declined"},
		"route":        {routeSchema(), "schema-fallback-route"},
		"zeroRequired": {zeroRequiredSchemas(), "zero-required-identity"},
		"singleton":    {zeroRequiredSchemas(), "zero-required-identity-singleton"},
	}
	var checked, refused int
	for name, set := range sets {
		signal := scanFixture(t, set.fixture)
		for typeName := range set.schemas {
			checked++
			_, admitted := SynthesizeTypeIdentity(typeName, set.schemas, signal)
			refusal := SchemaRefusal(typeName, set.schemas, signal)
			if admitted && refusal != "" {
				t.Errorf("%s/%s: admitted, and yet explained why it was refused: %s", name, typeName, refusal)
			}
			if !admitted {
				refused++
				if refusal == "" {
					t.Errorf("%s/%s: refused with nothing to say about why", name, typeName)
				}
			}
		}
	}
	if checked == 0 || refused == 0 {
		t.Fatalf("this asserted nothing: %d types checked, %d refused", checked, refused)
	}
}

// TestSchemaRefusalCompositeClauseIsGone pins the specific dead branch.
// A composite identity is admitted by identity object (#105), so no type
// can be refused for being one, and the sentence saying so must not come
// back.
func TestSchemaRefusalCompositeClauseIsGone(t *testing.T) {
	sets := []map[string]providers.Schema{fallbackSchemas(), routeSchema(), zeroRequiredSchemas()}
	fixtures := []string{"schema-fallback", "schema-fallback-composite", "schema-fallback-route", "zero-required-identity"}
	for _, schemas := range sets {
		for _, fixture := range fixtures {
			signal := scanFixture(t, fixture)
			for typeName := range schemas {
				if got := SchemaRefusal(typeName, schemas, signal); strings.Contains(got, "composite of") {
					t.Errorf("%s (%s): %s", typeName, fixture, got)
				}
			}
		}
	}
}
