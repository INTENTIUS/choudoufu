// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package policy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestDefaultVerbIsAlwaysValid: [Build] fills every omitted quadrant from
// [DefaultVerb] with no validation step of its own, on the assumption that
// the preset is always legal for its quadrant. This pins that assumption:
// if a future edit to either map broke it, this is where it would be
// caught, rather than surfacing as a lint rule silently rejecting a
// configuration that never touched the quadrant that failed.
func TestDefaultVerbIsAlwaysValid(t *testing.T) {
	for _, q := range Quadrants {
		verb, ok := DefaultVerb[q]
		if !ok {
			t.Errorf("%s: no default verb", q)
			continue
		}
		if !Valid(q, verb) {
			t.Errorf("%s: default verb %q is not in its own quadrant's valid set (%s)", q, verb, ValidVerbNames(q))
		}
	}
}

// TestValidVerbsRulings pins the per-quadrant verb-validity matrix's
// documented rulings, one assertion per sentence in [ValidVerbs]'s doc
// comment.
func TestValidVerbsRulings(t *testing.T) {
	for _, tc := range []struct {
		quadrant Quadrant
		verb     Verb
		want     bool
	}{
		// declared+tagged
		{DeclaredTagged, Converge, true},
		{DeclaredTagged, Untag, true},
		{DeclaredTagged, Keep, true},
		{DeclaredTagged, Report, true},
		{DeclaredTagged, Adopt, false},
		{DeclaredTagged, Refuse, false},
		{DeclaredTagged, Delete, false},

		// declared+untagged
		{DeclaredUntagged, Converge, true},
		{DeclaredUntagged, Adopt, true},
		{DeclaredUntagged, Refuse, true},
		{DeclaredUntagged, Keep, true},
		{DeclaredUntagged, Report, true},
		{DeclaredUntagged, Untag, false},
		{DeclaredUntagged, Delete, false},

		// undeclared+tagged
		{UndeclaredTagged, Delete, true},
		{UndeclaredTagged, Keep, true},
		{UndeclaredTagged, Untag, true},
		{UndeclaredTagged, Report, true},
		{UndeclaredTagged, Converge, false},
		{UndeclaredTagged, Adopt, false},
		{UndeclaredTagged, Refuse, false},

		// undeclared+untagged
		{UndeclaredUntagged, Keep, true},
		{UndeclaredUntagged, Delete, true},
		{UndeclaredUntagged, Report, true},
		{UndeclaredUntagged, Adopt, false},
		{UndeclaredUntagged, Converge, false},
		{UndeclaredUntagged, Refuse, false},
		{UndeclaredUntagged, Untag, false},
	} {
		if got := Valid(tc.quadrant, tc.verb); got != tc.want {
			t.Errorf("Valid(%s, %s) = %v, want %v", tc.quadrant, tc.verb, got, tc.want)
		}
	}
}

// TestBuildNil: no policy block at all (raw == nil) resolves to the
// preset in full - "omitted policy block = today's exact behavior".
func TestBuildNil(t *testing.T) {
	got := Build(nil, "my-estate")
	want := &Policy{
		DeclaredTagged:     Converge,
		DeclaredUntagged:   Refuse,
		UndeclaredTagged:   Delete,
		UndeclaredUntagged: Keep,
		TagKey:             markers.TagEstate,
		TagValue:           "my-estate",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Build(nil, ...) = %#v, want %#v", got, want)
	}
}

// TestBuildPartial: a raw value that sets one quadrant leaves the other
// three defaulted, and an unset tag_key/tag_value default to the estate
// marker.
func TestBuildPartial(t *testing.T) {
	got := Build(&Raw{
		DeclaredTagged:    "keep",
		DeclaredTaggedSet: true,
	}, "my-estate")
	want := &Policy{
		DeclaredTagged:     Keep,
		DeclaredUntagged:   Refuse,
		UndeclaredTagged:   Delete,
		UndeclaredUntagged: Keep,
		TagKey:             markers.TagEstate,
		TagValue:           "my-estate",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Build(partial, ...) = %#v, want %#v", got, want)
	}
}

// TestBuildFull exercises every field Raw carries: all four quadrants, a
// tag_key/tag_value distinct from the estate marker, a scope, and a
// threshold.
func TestBuildFull(t *testing.T) {
	got := Build(&Raw{
		DeclaredTagged:        "untag",
		DeclaredTaggedSet:     true,
		DeclaredUntagged:      "converge",
		DeclaredUntaggedSet:   true,
		UndeclaredTagged:      "keep",
		UndeclaredTaggedSet:   true,
		UndeclaredUntagged:    "delete",
		UndeclaredUntaggedSet: true,
		TagKey:                "preserve",
		TagKeySet:             true,
		TagValue:              "yes",
		TagValueSet:           true,
		Scope: &RawScope{
			Services: []string{"ec2", "s3"},
			Types:    []string{"aws_instance"},
			Regions:  []string{"us-east-1"},
		},
		Threshold:    25,
		ThresholdSet: true,
	}, "my-estate")

	want := &Policy{
		DeclaredTagged:     Untag,
		DeclaredUntagged:   Converge,
		UndeclaredTagged:   Keep,
		UndeclaredUntagged: Delete,
		TagKey:             "preserve",
		TagValue:           "yes",
		Scope: &Scope{
			Services: []string{"ec2", "s3"},
			Types:    []string{"aws_instance"},
			Regions:  []string{"us-east-1"},
		},
		Threshold:    25,
		ThresholdSet: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Build(full, ...) = %#v, want %#v", got, want)
	}
}

// TestPolicyString is a smoke test on the format [Policy.String] renders:
// nothing consumes it for real output yet (see the package doc comment),
// but it has to actually render every field so the shape is worth
// something to the plan-rendering work that does, eventually, consume it.
func TestPolicyString(t *testing.T) {
	p := Build(&Raw{
		UndeclaredUntagged:    "delete",
		UndeclaredUntaggedSet: true,
		Scope:                 &RawScope{Services: []string{"ec2"}},
		Threshold:             25,
		ThresholdSet:          true,
	}, "my-estate")

	s := p.String()
	for _, want := range []string{
		"tofu-estate", "my-estate",
		"declared+tagged: converge",
		"declared+untagged: refuse",
		"undeclared+tagged: delete",
		"undeclared+untagged: delete",
		"scope:", "ec2",
		"threshold: 25",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, want it to contain %q", s, want)
		}
	}

	var nilPolicy *Policy
	if got := nilPolicy.String(); got == "" {
		t.Error("String() on a nil *Policy returned an empty string")
	}
}

// TestBuildMaintainerExample is issue #67's pin: the maintainer's exact
// example policy block, loaded through the real configuration decoder and
// bridged into a [Raw] the same way internal/command's statelessPolicy
// does, has to Build into the expected [Policy] - untag/converge/keep/delete
// across the four quadrants, the estate marker as the default tag, and no
// scope or threshold, since the example sets neither.
func TestBuildMaintainerExample(t *testing.T) {
	parser := configs.NewParser(nil)
	mod, diags := parser.LoadConfigDir("../../configs/testdata/valid-modules/live-policy", configs.RootModuleCallForTesting())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if mod.Live == nil || mod.Live.Policy == nil {
		t.Fatal("no policy block was decoded")
	}
	lp := mod.Live.Policy

	raw := &Raw{
		DeclaredTagged:        lp.DeclaredTagged,
		DeclaredTaggedSet:     lp.DeclaredTaggedSet,
		DeclaredUntagged:      lp.DeclaredUntagged,
		DeclaredUntaggedSet:   lp.DeclaredUntaggedSet,
		UndeclaredTagged:      lp.UndeclaredTagged,
		UndeclaredTaggedSet:   lp.UndeclaredTaggedSet,
		UndeclaredUntagged:    lp.UndeclaredUntagged,
		UndeclaredUntaggedSet: lp.UndeclaredUntaggedSet,
		TagKey:                lp.TagKey,
		TagKeySet:             lp.TagKeySet,
		TagValue:              lp.TagValue,
		TagValueSet:           lp.TagValueSet,
		Threshold:             lp.Threshold,
		ThresholdSet:          lp.ThresholdSet,
	}
	if lp.Scope != nil {
		raw.Scope = &RawScope{
			Services: lp.Scope.Services,
			Types:    lp.Scope.Types,
			Regions:  lp.Scope.Regions,
		}
	}

	got := Build(raw, mod.Live.Estate)
	want := &Policy{
		DeclaredTagged:     Untag,
		DeclaredUntagged:   Converge,
		UndeclaredTagged:   Keep,
		UndeclaredUntagged: Delete,
		TagKey:             markers.TagEstate,
		TagValue:           "my-estate",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Build(maintainer's example) = %#v, want %#v", got, want)
	}
}
