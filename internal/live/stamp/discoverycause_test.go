// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The refusal a marker-only resource gets when nothing could be stamped on it
// used to be one sentence for every reason it could not be found: "has an
// identity the provider assigns at create time". Across the 250-fixture
// corpus that sentence was true of just over half the sites it appeared on.
// The rest were an omitted name argument, a name_prefix, or a cloud property
// this run was never told - three situations with a next step, told they had
// none.
//
// Every assertion below is on the RENDERED diagnostic, never on a predicate:
// a boolean saying "this is a cloud cause" can be right while the sentence an
// operator reads is the other one, and that shape of defect has shipped green
// in this repository three times.

// untaggableSource is one resource of a type testSchemas() gives no tags map,
// which is the skip that carries 103 of the corpus's 104 sites into this
// refusal.
const untaggableSource = `
resource "aws_route_table_association" "app" {
  subnet_id      = "subnet-1"
  route_table_id = "rtb-1"
}
`

func stampWithCause(t *testing.T, disco identity.BlockDiscovery) (string, tfdiags.Severity) {
	t.Helper()

	diags := stampDiagsWithCause(t, disco)
	if len(diags) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %s", len(diags), diags.ErrWithWarnings())
	}
	return diags[0].Description().Detail, diags[0].Severity()
}

// stampDiagsWithCause is stampWithCause without the one-diagnostic
// expectation, for the cause whose whole point is that it produces none.
func stampDiagsWithCause(t *testing.T, disco identity.BlockDiscovery) tfdiags.Diagnostics {
	t.Helper()

	cfg := loadSource(t, untaggableSource)
	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		NeedsDiscovery: map[string]identity.BlockDiscovery{
			"aws_route_table_association.app": disco,
		},
	})
	return diags
}

// TestUnmarkedDiscoveryDetail_uniqueNameIsNotRefused is the one cause that
// must NOT produce a diagnostic at all, and the reason the test below skips
// it rather than expecting a sentence for it.
//
// Every other cause names a resource that can only ever be found by its
// ownership marker, so applying it unmarked creates something no later run
// can recognise, and stamping escalates. An instance carrying
// [identity.DiscoveryUniqueName] is found by a name AWS refuses to issue
// twice (internal/live/discovery/uniquename.go), marker or no marker.
// Refusing it would refuse every apply of an untaggable type of that shape -
// which is to say the whole population issue #272 admitted.
//
// The fixture is the same untaggable resource every other case in this file
// uses, so the ONLY difference between this test and the ones below is the
// cause. If mustStamp stopped reading the cause, this test goes red on its
// own.
func TestUnmarkedDiscoveryDetail_uniqueNameIsNotRefused(t *testing.T) {
	diags := stampDiagsWithCause(t, identity.BlockDiscovery{
		Cause: identity.DiscoveryUniqueName,
		Args:  []string{"name"},
	})
	if len(diags) != 0 {
		t.Errorf("a resource bound by its account-unique name raised %d diagnostic(s), want none: %s\n"+
			"Applying it unmarked is not the unrecoverable mistake this refusal exists to catch - a later run finds it by its name.",
			len(diags), diags.ErrWithWarnings())
	}

	// The contrast, in the same fixture: the same block with the ordinary
	// server-assigned cause IS refused. Without this line the assertion
	// above would pass just as well against a Stamp that had stopped
	// refusing anything.
	if got := stampDiagsWithCause(t, identity.BlockDiscovery{Cause: identity.DiscoveryServerAssigned}); len(got) != 1 {
		t.Errorf("the same fixture with a server-assigned cause raised %d diagnostic(s), want 1 - this test proves nothing if nothing is refused", len(got))
	}
}

// TestUnmarkedDiscoveryDetail_everyCauseIsToldApart is the whole point of
// [identity.DiscoveryCause] reaching this package: one sentence per cause,
// and every cause that has a next step says what it is.
//
// It iterates [identity.AllDiscoveryCauses] rather than a list written here,
// so a cause added to identity with no sentence of its own fails this test
// instead of silently inheriting the server-assigned wording.
func TestUnmarkedDiscoveryDetail_everyCauseIsToldApart(t *testing.T) {
	args := map[identity.DiscoveryCause][]string{
		identity.DiscoveryCloudUnknown: {string(identity.CloudAccountID)},
		identity.DiscoveryNameOmitted:  {"name"},
		identity.DiscoveryNamePrefix:   {"name", "name_prefix"},
		identity.DiscoverySiblingApply: {"aws_acm_certificate.cert", "name"},
	}
	// What the operator must be able to read out of each sentence. The
	// server-assigned pair share one, deliberately: there is no
	// configuration edit that resolves a server-minted identity, so its
	// sentence offers no step and the unspecified fallback is the same
	// sentence for the same reason.
	want := map[identity.DiscoveryCause][]string{
		identity.DiscoveryCauseUnspecified: {"identity the provider assigns at create time"},
		identity.DiscoveryServerAssigned:   {"identity the provider assigns at create time"},
		identity.DiscoveryCloudUnknown:     {"AWS account ID", "property of the cloud this run is pointed at"},
		identity.DiscoveryNameOmitted:      {"sets no name", "Setting name to a value this configuration chooses"},
		identity.DiscoveryNamePrefix:       {"named through name_prefix", "Naming it with name instead of name_prefix"},
		// The one cause whose next step is not an edit to the configuration.
		// The configuration is already right; what this run lacks is the
		// sibling, so the sentence has to name it and say applying it first
		// is the way out. See [identity.DiscoverySiblingApply].
		identity.DiscoverySiblingApply: {"takes name from aws_acm_certificate.cert", "Applying aws_acm_certificate.cert first"},
		// GitHub issue #289's cause: this instance's own expression did not
		// fold, but its TYPE is taggable and listable, so the marker is the
		// answer regardless of which expression failed - there is no single
		// argument to name, unlike every case above.
		identity.DiscoveryMarkerFallback: {"identity does not fold from its own configuration", "tagged and listable"},
	}

	details := make(map[identity.DiscoveryCause]string)
	for _, cause := range identity.AllDiscoveryCauses() {
		if cause.BindsByName() {
			// This cause produces no diagnostic at all - see
			// TestUnmarkedDiscoveryDetail_uniqueNameIsNotRefused, which is
			// where its behaviour is pinned. Reached through the predicate
			// rather than by naming the cause, so a second bindable cause
			// lands in the same place.
			continue
		}
		detail, severity := stampWithCause(t, identity.BlockDiscovery{Cause: cause, Args: args[cause]})
		details[cause] = detail

		// The severity is not the cause's to change. Every one of these is a
		// resource with nowhere to write a marker and no identity this run
		// can compute, and it is unfindable afterwards however it got that
		// way. A cause that softened this would be handing back a false
		// "you are fine".
		if severity != tfdiags.Error {
			t.Errorf("cause %s produced severity %v, want Error", cause, severity)
		}

		phrases, known := want[cause]
		if !known {
			t.Errorf("cause %s has no expected wording here; add its sentence to UnmarkedDiscoveryDetail and to this table", cause)
			continue
		}
		for _, phrase := range phrases {
			if !strings.Contains(detail, phrase) {
				t.Errorf("cause %s rendered:\n  %s\nwhich does not contain %q", cause, detail, phrase)
			}
		}
	}

	// And the three distinct sentences really are distinct: a switch that
	// fell through would satisfy every "contains" above for the
	// server-assigned pair and none of the others, but a table that drifted
	// into two causes sharing a phrase would not be caught by that alone.
	distinct := map[string][]identity.DiscoveryCause{}
	for cause, detail := range details {
		distinct[detail] = append(distinct[detail], cause)
	}
	if len(distinct) != 6 {
		t.Errorf("expected 6 distinct sentences across %d causes, got %d: %v", len(details), len(distinct), distinct)
	}
}

// TestUnmarkedDiscoveryDetail_cloudCauseNamesTheArgument is #250's second
// half. A CLOUD_UNKNOWN carries the missing cloud property first and then the
// arguments the provider documents as defaulting to it, so the sentence can
// offer the same kind of next step DiscoveryNameOmitted already offers -
// "set catalog_id" - instead of describing a run-level setting the operator
// forgot and that does not exist.
//
// The assertion is on the rendered sentence, and the negative half matters as
// much as the positive one: a component with no such argument must keep the
// no-step wording rather than invent one.
func TestUnmarkedDiscoveryDetail_cloudCauseNamesTheArgument(t *testing.T) {
	withArg, severity := stampWithCause(t, identity.BlockDiscovery{
		Cause: identity.DiscoveryCloudUnknown,
		Args:  []string{string(identity.CloudAccountID), "catalog_id"},
	})
	if severity != tfdiags.Error {
		t.Errorf("naming the argument softened the severity to %v; the resource is still unfindable as written", severity)
	}
	for _, phrase := range []string{
		"AWS account ID",
		"Setting catalog_id in the resource block",
		"makes the identity computable and needs no marker at all",
	} {
		if !strings.Contains(withArg, phrase) {
			t.Errorf("the sentence does not contain %q:\n  %s", phrase, withArg)
		}
	}

	// The contrast: a bare account segment in the middle of an ARN has no
	// argument, and the sentence must not pretend otherwise.
	bare, _ := stampWithCause(t, identity.BlockDiscovery{
		Cause: identity.DiscoveryCloudUnknown,
		Args:  []string{string(identity.CloudAccountID)},
	})
	if strings.Contains(bare, "Setting") {
		t.Errorf("a cloud component with no argument was still offered one:\n  %s", bare)
	}
	if bare == withArg {
		t.Errorf("the argument made no difference to the sentence:\n  %s", bare)
	}

	// Two candidate arguments read as a choice, not as a list to set all of.
	both, _ := stampWithCause(t, identity.BlockDiscovery{
		Cause: identity.DiscoveryCloudUnknown,
		Args:  []string{string(identity.CloudRegion), "region", "location"},
	})
	if !strings.Contains(both, "Setting region or location in the resource block") {
		t.Errorf("two candidate arguments did not render as a choice:\n  %s", both)
	}
}

// TestUnmarkedDiscoveryDetail_zeroCauseStillRefuses is the trap this map's
// shape creates and the reason [stamper.discovery] uses the comma-ok form.
//
// [identity.BlockDiscovery]'s zero value carries the empty cause, which is
// also what a Go map returns for a key that is not in it. A reader that
// decided "marker-only" by comparing the looked-up value against the zero
// value would downgrade every entry a caller built without a cause from the
// hard error this mechanism exists to raise into a warning - audit finding
// C2, reintroduced by a refactor rather than by a decision.
func TestUnmarkedDiscoveryDetail_zeroCauseStillRefuses(t *testing.T) {
	detail, severity := stampWithCause(t, identity.BlockDiscovery{})
	if severity != tfdiags.Error {
		t.Fatalf("an entry left at the zero BlockDiscovery produced severity %v, want Error", severity)
	}
	if !strings.Contains(detail, "identity the provider assigns at create time") {
		t.Errorf("the zero cause did not fall back to the generic sentence; got:\n  %s", detail)
	}
}

// TestUnmarkedDiscoveryDetail_missingArgsFallBack: the subjects travel in a
// []string, so a caller can hand over a cause with nothing in it. Each of the
// three sentences that reads an argument out of that slice must degrade to
// something true rather than render an empty name or index out of range.
func TestUnmarkedDiscoveryDetail_missingArgsFallBack(t *testing.T) {
	for _, cause := range []identity.DiscoveryCause{
		identity.DiscoveryCloudUnknown,
		identity.DiscoveryNameOmitted,
		identity.DiscoveryNamePrefix,
	} {
		detail, severity := stampWithCause(t, identity.BlockDiscovery{Cause: cause})
		if severity != tfdiags.Error {
			t.Errorf("cause %s with no args produced severity %v, want Error", cause, severity)
		}
		if strings.Contains(detail, `""`) || strings.Contains(detail, "  ") {
			t.Errorf("cause %s with no args rendered a hole:\n  %s", cause, detail)
		}
		if !strings.Contains(detail, "aws_route_table_association.app") {
			t.Errorf("cause %s with no args did not name the resource:\n  %s", cause, detail)
		}
	}

	// A cloud cause whose one argument is present but is not a CloudValue
	// this package knows: CloudValue.Describe returns the raw string for an
	// unrecognised value, so the sentence still names something.
	detail, _ := stampWithCause(t, identity.BlockDiscovery{
		Cause: identity.DiscoveryCloudUnknown,
		Args:  []string{"partition"},
	})
	if !strings.Contains(detail, "partition") {
		t.Errorf("an unrecognised cloud property was dropped from the sentence:\n  %s", detail)
	}
}
