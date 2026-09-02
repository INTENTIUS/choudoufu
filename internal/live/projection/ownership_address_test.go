// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// These tests pin what [builder.checkOwnership] does about the tofu-address
// marker, which until GitHub issue #244 landed was nothing: it read
// tags[markers.TagEstate] and no other tag, so a live object carrying this
// estate's marker under some other resource's address was adopted into prior
// state without a diagnostic, an omission or an Unowned entry.
//
// The file used to record that defect. It now records its opposite, plus the
// two things a fix could easily have broken and did not: a `moved` block,
// whose whole mechanism is a live object legitimately carrying the OLD
// address until the tag rewrite lands (issue #198), and live-mv's own
// materialize path, which does not go through the ownership check at all.
//
// The other half of #244 is in internal/live/discovery, whose scan used to
// drop a live object whose marker names a ClassConcrete declared address
// (discovery.go's `if decl.declares(...) { continue }`) on the stated grounds
// that "the marker only confirms the estate still owns what it thinks it
// owns" - the confirmation this file's subject performs. That half landed too
// (internal/live/discovery/displaced.go); the two ask different questions of
// different inputs, and neither closes the other. This comment used to say
// half 2 was still open, which stopped being true one commit after it was
// written.

// TestOwnershipAddress_wrongAddressSameEstateIsRefused: this estate's marker,
// another resource's address. Stock OpenTofu, holding no state for this
// address, plans a create (and, for a unique-named type, collides); choudoufu
// used to adopt. It now refuses, loudly.
func TestOwnershipAddress_wrongAddressSameEstateIsRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate: ownershipEstate,
		// A different resource in this same estate owns this object.
		markers.TagAddress: "aws_cloudwatch_log_group.somebody_else",
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)

	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("another instance's live object was adopted (issue #244):\n%s", res)
	}
	if len(res.Unowned) != 1 {
		t.Fatalf("the address disagreement produced no single Unowned entry: %+v", res.Unowned)
	}
	if got := res.Unowned[0].Estate; got != ownershipEstate {
		t.Errorf("the Unowned entry records estate %q, want this run's own %q - the object IS this estate's, which is the point", got, ownershipEstate)
	}
	om := omissionFor(t, res, `aws_cloudwatch_log_group.app`)
	if !strings.Contains(om.Detail, "aws_cloudwatch_log_group.somebody_else") {
		t.Errorf("the omission does not name the address the live object actually carries:\n%s", om.Detail)
	}
	if got := diagSummaries(diags); len(got) != 1 || got[0] != SummaryWrongAddress {
		t.Errorf("diagnostics = %v, want exactly [%q]:\n%s", got, SummaryWrongAddress, renderDiags(diags))
	}
}

// TestOwnershipAddress_renumberedIndexIsRefused is the shape the widened
// count.index domain rule (internal/live/lint/count_index_domain.go) admits:
// an identity-bearing argument indexed by count.index, where deleting a
// middle element renumbers the survivors. app resolves to the identity the
// live object marked app[2] holds. Adopting it is what would have made this
// plan rewrite app[2]'s marker onto app, leaving the object app named behind
// carrying a marker a second object now also carries.
func TestOwnershipAddress_renumberedIndexIsRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/c", map[string]string{
		"id": "/ours/c", "name": "/ours/c",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: markers.EscapeAddress(`aws_cloudwatch_log_group.app[2]`),
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/c"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("a renumbered index adopted the neighbouring instance's live object:\n%s", res)
	}
	if len(res.Unowned) != 1 {
		t.Fatalf("Unowned = %+v, want one entry", res.Unowned)
	}
}

// TestOwnershipAddress_missingAddressMarkerIsStillAdmitted settles the policy
// question #244 left open, in the direction of KEEPING today's behavior.
//
// The estate marker with no tofu-address at all is the shape a resource
// stamped by a run older than the address marker has, and completing it is a
// shipped migration path: internal/command's TestLivePlan_stampsMissingMarkers
// asserts the resulting plan down to its `+ "tofu-address"` line. Unlike a
// wrong address, an absent one contradicts nothing - the instance was found by
// the identity the configuration itself computed, so admitting it is not the
// guess live/MARKERS.md forbids, and nothing can be leaked or double-claimed
// by writing a marker where there was none. See [builder.addressNames]'s doc
// comment for the full reconciliation with the spec and with discovery, which
// refuses the same shape because on its path the marker is the only evidence
// there is.
func TestOwnershipAddress_missingAddressMarkerIsStillAdmitted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate: ownershipEstate,
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("an object with the estate marker and no tofu-address was refused, which breaks the older-run migration path TestLivePlan_stampsMissingMarkers covers:\n%s", res)
	}
	if len(res.Unowned) != 0 || len(diags) != 0 {
		t.Errorf("an absent address marker produced a complaint: %+v\n%s", res.Unowned, renderDiags(diags))
	}
}

// TestOwnershipAddress_corruptContinuationChainIsRefused: a tofu-address-3
// with no tofu-address-2. live/MARKERS.md calls this the same malformed case
// as a missing address and forbids reading it as the address up to the gap.
func TestOwnershipAddress_corruptContinuationChainIsRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:          ownershipEstate,
		markers.TagAddress:         "aws_cloudwatch_log_group.app",
		markers.ContinuationTag(3): "trailing",
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("a gapped continuation chain was read as the address up to the gap and adopted:\n%s", res)
	}
}

// TestOwnershipAddress_ownAddressIsStillAdmitted is the control. A rule that
// refused everything would be safe and useless, and this is the case every
// working estate is in.
func TestOwnershipAddress_ownAddressIsStillAdmitted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: markers.EscapeAddress(`aws_cloudwatch_log_group.app`),
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("the estate's own correctly marked resource was refused:\n%s", res)
	}
	if len(res.Unowned) != 0 || len(diags) != 0 {
		t.Errorf("a correct marker produced a complaint: %+v\n%s", res.Unowned, renderDiags(diags))
	}
}

// TestOwnershipAddress_movedBlockAdmitsTheOldAddress is the constraint that
// makes a bare equality comparison wrong. A honoured `moved` block leaves the
// live object carrying its OLD tofu-address until the marker rewrite lands,
// and that rewrite is the ordinary tags diff the provider plans - it cannot
// happen before the object is in the prior state. Refusing here would break
// every moved block (issue #198) at the moment issue #244 was fixed.
func TestOwnershipAddress_movedBlockAdmitsTheOldAddress(t *testing.T) {
	cfg := loadConfig(t, "testdata/named-moved")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: markers.EscapeAddress(`aws_cloudwatch_log_group.legacy_app`),
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("a moved block's old address was refused, which breaks issue #198:\n%s", res)
	}
	if len(res.Unowned) != 0 || len(diags) != 0 {
		t.Errorf("a moved block's old address produced a complaint: %+v\n%s", res.Unowned, renderDiags(diags))
	}
}

// TestOwnershipAddress_movedBlockDoesNotAdmitAThirdAddress: the alias set is
// the moved block's origins, not "any address". A moved block from
// legacy_app must not make a marker naming somebody_else acceptable.
func TestOwnershipAddress_movedBlockDoesNotAdmitAThirdAddress(t *testing.T) {
	cfg := loadConfig(t, "testdata/named-moved")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: markers.EscapeAddress(`aws_cloudwatch_log_group.somebody_else`),
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("a moved block widened the acceptable markers to an address it never mentions:\n%s", res)
	}
}

// TestOwnershipAddress_mvMaterializePathIsUnaffected settles #244's stated
// blocker "whether mv's plan path goes through checkOwnership, and with which
// address in hand, has to be settled before a refusal lands, or the one
// supported way to change an address breaks."
//
// It does not go through it. internal/live/mv's materialize calls
// [BuildFrom], which passes a zero Options, so Ownership is nil and
// checkOwnership returns at its first case. This pins the property mv relies
// on, in the package that owns it, so a later change to BuildFrom's defaults
// fails here rather than in mv's own fixtures.
func TestOwnershipAddress_mvMaterializePathIsUnaffected(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate: ownershipEstate,
		// The old address, mid-rename: exactly what mv reads before it
		// rewrites the tag.
		markers.TagAddress: markers.EscapeAddress(`aws_cloudwatch_log_group.before_the_rename`),
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t))

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("BuildFrom now applies an ownership check, which breaks live-mv's materialize:\n%s", res)
	}
}

// TestOwnershipAddress_legacyGrammarMarkerIsStillAdmitted is the audit
// question "what does this newly refuse that used to work?", asked of the one
// input that can make a correct marker fail a naive comparison: a live object
// stamped by a run older than issue #178.
//
// A for_each key containing "@" is the whole of that window. Both the pre- and
// post-#178 grammars admit the character and escape it differently, so the
// value a 2025 run wrote onto this object is not the value EscapeAddress
// computes for the same address today. [markers.AddressMatches] tries the
// older grammars for exactly this reason, and [moved.Accepts] - which is what
// [builder.addressNames] calls - is defined in terms of it.
//
// Both legs were unit-tested when #244 landed (markers.TestAddressMatches...,
// moved.TestAcceptsHonoursOlderEscapingGrammars) and neither was tested HERE,
// where the refusal that could fire on them lives. Stripping the two fallback
// grammars out of AddressMatches left this whole package green. So the
// property "an estate stamped before #178 does not start reading as marked for
// another address" rested on nobody rewriting the call site to a bare
// equality - the exact narrowing #244's own body warns against, because it is
// what produced 107 cross-grammar false positives earlier in this campaign.
//
// The refusal this guards against is not a warning an operator can ignore: it
// takes the instance out of the prior state, so the plan proposes CREATING a
// log group that already exists, and for a unique-named type the apply then
// fails. On every instance of the estate at once.
func TestOwnershipAddress_legacyGrammarMarkerIsStillAdmitted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	const addr = `aws_cloudwatch_log_group.app["at@sign"]`

	// What a pre-#178 run stamped, and what today's grammar computes for the
	// same address. If these ever coincide the test has stopped testing
	// anything, so it says so rather than passing vacuously.
	legacy := markers.LegacyEscapeAddress(addr)
	current := markers.EscapeAddress(addr)
	if legacy == current {
		t.Fatalf("the two grammars agree on %s (%q), so this fixture no longer exercises the fallback", addr, legacy)
	}

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: legacy,
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, addr)) {
		t.Fatalf("a marker written under the pre-#178 grammar (%q, where today's is %q) was read as another address's and refused:\n%s", legacy, current, res)
	}
	if len(res.Unowned) != 0 || len(diags) != 0 {
		t.Errorf("a pre-#178 marker produced a complaint: %+v\n%s", res.Unowned, renderDiags(diags))
	}

	// The materialized value, not the predicate: Has() being true says the
	// address is in the projection, not that it is bound to the live object
	// the marker is on. A fallback that admitted the address while
	// materializing something else would satisfy every assertion above.
	is := res.State.ResourceInstance(mustAddr(t, addr))
	if is == nil || is.Current == nil {
		t.Fatalf("Has() is true but the state carries no current object for %s", addr)
	}
	if got, want := string(is.Current.AttrsJSON), `"/ours/logs"`; !strings.Contains(got, want) {
		t.Errorf("admitted, but the attributes are not the live object the marker sits on:\n%s", got)
	}
	if got := is.Current.Status; got != 'R' {
		t.Errorf("object status is %q, want ObjectReady", got)
	}
}

// TestOwnershipAddress_legacyGrammarDoesNotWidenToAnotherKey bounds the test
// above. The fallback grammars exist to recognize ONE address written an older
// way, not to make near misses acceptable: a pre-#178 marker for the "at@sign"
// key must still be refused when the instance being checked is a different
// key of the same block. Without this, "admit on any grammar" and "admit the
// right thing" are indistinguishable.
func TestOwnershipAddress_legacyGrammarDoesNotWidenToAnotherKey(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: markers.LegacyEscapeAddress(`aws_cloudwatch_log_group.app["at@sign"]`),
	})

	other := `aws_cloudwatch_log_group.app["other@sign"]`
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, other), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, other)) {
		t.Fatalf("a pre-#178 marker for one for_each key admitted a different key of the same block:\n%s", res)
	}
	if got := diagSummaries(diags); len(got) != 1 || got[0] != SummaryWrongAddress {
		t.Errorf("diagnostics = %v, want exactly [%q]:\n%s", got, SummaryWrongAddress, renderDiags(diags))
	}
}

// diagSummaries is the Summary of every diagnostic, in order. The refusal
// registry is keyed by Summary, so asserting on it is asserting that the
// refusal a user gets is the one they can look up.
func diagSummaries(diags tfdiags.Diagnostics) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Description().Summary)
	}
	return out
}
