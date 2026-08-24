// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestNodeResolver_RecordHit is step (a): an estate's own record, exactly
// what [builder.materializeFromRecord] reads for the pre-walk path, answers
// the resolver directly.
func TestNodeResolver_RecordHit(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("my-estate"))
	addr := locatedTestAddr(t, "aws_globalaccelerator_listener", "svc")

	const wantID = "arn:aws:globalaccelerator::123456789012:accelerator/abc/listener/def"
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: wantID}
	}); err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}

	resolver := &NodeResolver{RecordStore: store}
	target, found, diags := resolver.ResolveResourceIdentity(ctx, addr, cty.EmptyObjectVal, providers.Schema{})
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !found {
		t.Fatalf("expected the record to be found")
	}
	if target.ID != wantID {
		t.Errorf("target.ID = %q, want %q", target.ID, wantID)
	}
}

// TestNodeResolver_MarkerIndexHit is step (b): the discovery sweep's own
// resolutions, snapshotted into the index once and looked up by address.
func TestNodeResolver_MarkerIndexHit(t *testing.T) {
	addr := locatedTestAddr(t, "aws_instance", "web")
	resolver := &NodeResolver{
		MarkerIndex: map[string]providers.ImportTarget{
			addr.String(): {ID: "i-0123456789abcdef0"},
		},
	}

	target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, cty.EmptyObjectVal, providers.Schema{})
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !found || target.ID != "i-0123456789abcdef0" {
		t.Fatalf("target=%#v found=%v, want ID i-0123456789abcdef0/true", target, found)
	}
}

// TestNodeResolver_TableOverEvaluatedValueHit is step (c): no record, no
// marker, but the identity table resolves fully against the real evaluated
// configuration value.
func TestNodeResolver_TableOverEvaluatedValueHit(t *testing.T) {
	addr := locatedTestAddr(t, "aws_route", "r")
	resolver := &NodeResolver{}

	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.StringVal("10.0.0.0/16"),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, val, providers.Schema{})
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !found {
		t.Fatalf("expected the identity table to resolve this instance from its evaluated value")
	}
	if want := "rtb-0123456789abcdef0_10.0.0.0/16"; target.ID != want {
		t.Errorf("target.ID = %q, want %q", target.ID, want)
	}
}

// TestNodeResolver_PrecedenceOrder proves the three steps are consulted in
// the documented order (record, then marker, then table) rather than
// merely all being reachable: an address with an answer at every step must
// return the RECORD's answer, not whichever step happened to run last.
func TestNodeResolver_PrecedenceOrder(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("my-estate"))
	addr := locatedTestAddr(t, "aws_route", "r")

	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "from-record"}
	}); err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}

	resolver := &NodeResolver{
		RecordStore: store,
		MarkerIndex: map[string]providers.ImportTarget{
			addr.String(): {ID: "from-marker"},
		},
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.StringVal("10.0.0.0/16"),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	target, found, diags := resolver.ResolveResourceIdentity(ctx, addr, val, providers.Schema{})
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !found || target.ID != "from-record" {
		t.Fatalf("target=%#v found=%v, want ID from-record/true (the record must win over the marker index and the table)", target, found)
	}

	// Same address, marker only: the marker must win over the table.
	resolver2 := &NodeResolver{
		MarkerIndex: map[string]providers.ImportTarget{
			addr.String(): {ID: "from-marker"},
		},
	}
	target2, found2, diags2 := resolver2.ResolveResourceIdentity(ctx, addr, val, providers.Schema{})
	if diags2.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags2.Err())
	}
	if !found2 || target2.ID != "from-marker" {
		t.Fatalf("target=%#v found=%v, want ID from-marker/true", target2, found2)
	}
}

// TestNodeResolver_NoSourceDefaultRefuses is ruling 4 (#365): an instance
// with no record, no marker and no derivable identity refuses by default,
// with a diagnostic naming both remedies (live-import, or the toggle) -
// never a silent "found=false, plan a create."
func TestNodeResolver_NoSourceDefaultRefuses(t *testing.T) {
	addr := locatedTestAddr(t, "aws_route", "r")
	resolver := &NodeResolver{}

	// Every destination alternative absent: the table cannot derive an
	// identity, there is no record and no marker, so this is ruling 4's
	// no-source case.
	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.NullVal(cty.String),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, val, providers.Schema{})
	if found {
		t.Fatalf("expected found=false for a no-source instance, got target=%#v", target)
	}
	if !diags.HasErrors() {
		t.Fatalf("expected a refusal diagnostic for a no-source instance under the default toggle")
	}
	if !hasDiagSummary(diags, "No source for this instance's identity") {
		t.Errorf("expected the \"No source for this instance's identity\" summary, got:\n%s", diags.Err())
	}
}

// TestNodeResolver_ServerAssignedTypeNeverRefuses is the regression this
// unit's own first real gauntlet run caught: reference-ec2-vpc's greenfield
// apply (five brand-new resources, no record, no marker, no history at
// all) failed outright with "No source for this instance's identity"
// because an earlier version of this resolver applied ruling 4's toggle to
// EVERY unresolved instance, not only config-identified ones. A
// server-assigned type (aws_instance, aws_vpc: minted at create time, with
// no configuration argument to have derived an identity from in the first
// place) has no "source" to be missing, so a brand-new instance of one
// must always report found=false with NO diagnostic - the ordinary create
// path - regardless of the toggle.
func TestNodeResolver_ServerAssignedTypeNeverRefuses(t *testing.T) {
	for _, typeName := range []string{"aws_instance", "aws_vpc"} {
		for _, noSourceCreate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/NoSourceCreate=%v", typeName, noSourceCreate), func(t *testing.T) {
				addr := locatedTestAddr(t, typeName, "new")
				resolver := &NodeResolver{NoSourceCreate: noSourceCreate}

				target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, cty.EmptyObjectVal, providers.Schema{})
				if diags.HasErrors() {
					t.Fatalf("a brand-new %s must never be refused: %s", typeName, diags.Err())
				}
				if found {
					t.Fatalf("a brand-new %s has nothing for this resolver to have found: target=%#v", typeName, target)
				}
			})
		}
	}
}

// TestNodeResolver_UnknownTypeNeverRefuses is the same boundary for a type
// with no table row at all.
func TestNodeResolver_UnknownTypeNeverRefuses(t *testing.T) {
	addr := locatedTestAddr(t, "aws_this_type_does_not_exist", "new")
	resolver := &NodeResolver{}

	target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, cty.EmptyObjectVal, providers.Schema{})
	if diags.HasErrors() {
		t.Fatalf("a type absent from the table must never be refused: %s", diags.Err())
	}
	if found {
		t.Fatalf("nothing should have been found: %#v", target)
	}
}

// TestNodeResolver_NoSourceCreateToggle proves the toggle refuses exactly
// the no-source case and nothing else: set, the same no-source instance
// reports found=false with NO diagnostic, which lets managedResourceExecute
// fall through to stock's own create behavior.
func TestNodeResolver_NoSourceCreateToggle(t *testing.T) {
	addr := locatedTestAddr(t, "aws_route", "r")
	resolver := &NodeResolver{NoSourceCreate: true}

	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.NullVal(cty.String),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, val, providers.Schema{})
	if diags.HasErrors() {
		t.Fatalf("the toggle must silence the refusal, got: %s", diags.Err())
	}
	if found {
		t.Fatalf("a no-source instance must never report found=true even with the toggle set; got %#v", target)
	}

	// And the toggle changes nothing for an instance the table CAN
	// resolve - it is a fallback for absence, not a general override.
	resolved := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.StringVal("10.0.0.0/16"),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})
	target2, found2, diags2 := resolver.ResolveResourceIdentity(context.Background(), addr, resolved, providers.Schema{})
	if diags2.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags2.Err())
	}
	if !found2 || target2.ID != "rtb-0123456789abcdef0_10.0.0.0/16" {
		t.Fatalf("the toggle must not prevent a resolvable instance from resolving: target=%#v found=%v", target2, found2)
	}
}

// TestNewMarkerIndex_SkipsUnusableResolutions proves the index only keeps
// resolutions carrying a usable import identity, so a caller looking one
// up never gets an empty target back as if it were an answer.
func TestNewMarkerIndex_SkipsUnusableResolutions(t *testing.T) {
	usable := locatedTestAddr(t, "aws_instance", "usable")
	unusable := locatedTestAddr(t, "aws_instance", "unusable")

	idx := NewMarkerIndex([]identity.Resolution{
		{Addr: usable, Class: identity.ClassConcrete, ImportID: "i-123"},
		{Addr: unusable, Class: identity.ClassNeedsDiscovery, Reason: "server-assigned"},
	})

	if _, ok := idx[unusable.String()]; ok {
		t.Errorf("a resolution with no usable identity must not appear in the index")
	}
	target, ok := idx[usable.String()]
	if !ok || target.ID != "i-123" {
		t.Errorf("idx[%s] = %#v, %v; want {ID: i-123}, true", usable.String(), target, ok)
	}
}

// hasDiagSummary is a narrower hasDiag: this package's own test helpers
// (located_test.go and friends) do not already have one that only checks
// the summary.
func hasDiagSummary(diags tfdiags.Diagnostics, summary string) bool {
	for _, d := range diags {
		if strings.Contains(d.Description().Summary, summary) {
			return true
		}
	}
	return false
}
