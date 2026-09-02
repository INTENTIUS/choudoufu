// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// mustCommandTestAddr parses an address for these tests, failing loudly on
// a typo rather than silently resolving to the zero value.
func mustCommandTestAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad test address %q: %s", s, diags.Err())
	}
	return addr
}

// seedIdentityRecord writes a minimal, well-formed kind=identity envelope
// directly through the raw staterecord.Store interface, at exactly the key
// [projection.RecordStore.GetIdentity] reads - [projection.RecordKey] is
// exported for precisely this, so a test never has to reach into the
// package's own unexported envelope type to prove something reads back
// what was written at the address it names.
func seedIdentityRecord(t *testing.T, raw staterecord.Store, prefix string, addr addrs.AbsResourceInstance, importID string) {
	t.Helper()
	key := projection.RecordKey(prefix, addr)
	payload := []byte(`{"format_version":2,"kind":"identity","identity":{"import_id":"` + importID + `"}}`)
	if _, err := raw.PutIfAbsent(context.Background(), key, payload); err != nil {
		t.Fatalf("seeding a record at %s: %s", key, err)
	}
}

// TestStatelessRecordBackedNeedsDiscoveryAddrs is edge 3 of the plan-node
// seam (the foundation-order ruling (#388), ruling 3; GitHub issue
// #388): it must find exactly the needs-discovery addresses whose record
// already holds an identity - read the same way
// [projection.NodeResolver.ResolveResourceIdentity]'s own step (a) reads it
// - and nothing else: an address with no record, an address that is not in
// needs at all, and a nil store or empty needs list (the guards that keep a
// flag-off caller from paying even the read cost) must all report nothing.
func TestStatelessRecordBackedNeedsDiscoveryAddrs(t *testing.T) {
	ctx := context.Background()
	vpc := mustCommandTestAddr(t, "aws_vpc.main")
	subnetA := mustCommandTestAddr(t, `aws_subnet.this["a"]`)
	subnetB := mustCommandTestAddr(t, `aws_subnet.this["b"]`)

	needs := []identity.Resolution{
		{Addr: vpc, Class: identity.ClassNeedsDiscovery, Reason: "server-assigned"},
		{Addr: subnetA, Class: identity.ClassNeedsDiscovery, Reason: "server-assigned"},
		// subnetB is deliberately absent from needs, even though it will
		// have a record below: it must never appear in the result just
		// because it happens to have one, since it was never asked about.
	}

	t.Run("nil store is a no-op", func(t *testing.T) {
		got, diags := statelessRecordBackedNeedsDiscoveryAddrs(ctx, nil, needs)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Err())
		}
		if len(got) != 0 {
			t.Errorf("got %v, want nothing (nil store)", got)
		}
	})

	t.Run("empty needs is a no-op even with a real store", func(t *testing.T) {
		raw, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %s", err)
		}
		store := projection.NewRecordEnvelopeStore(raw, "prefix/")
		seedIdentityRecord(t, raw, "prefix/", vpc, "vpc-1")

		got, diags := statelessRecordBackedNeedsDiscoveryAddrs(ctx, store, nil)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Err())
		}
		if len(got) != 0 {
			t.Errorf("got %v, want nothing (no needs-discovery instances at all)", got)
		}
	})

	t.Run("finds exactly the needs-discovery addresses with a record", func(t *testing.T) {
		raw, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %s", err)
		}
		const prefix = "prefix/"
		store := projection.NewRecordEnvelopeStore(raw, prefix)

		// vpc has a record and IS in needs: must be found.
		seedIdentityRecord(t, raw, prefix, vpc, "vpc-1")
		// subnetB has a record but is NOT in needs: must not appear, since
		// nothing asked about it.
		seedIdentityRecord(t, raw, prefix, subnetB, "subnet-b")
		// subnetA is in needs but has NO record: must not appear either.

		got, diags := statelessRecordBackedNeedsDiscoveryAddrs(ctx, store, needs)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Err())
		}
		if len(got) != 1 || !got[vpc.String()] {
			t.Errorf("got %v, want exactly {%q: true}", got, vpc.String())
		}
		if got[subnetB.String()] {
			t.Errorf("subnetB was included despite never being asked about (not in needs): %v", got)
		}
	})
}

// TestRecordBackedNeedsDiscoveryBlocks is [statelessStampGaps]'s and
// [stamp.Request.RecordBackedBlocks]'s shared input: the per-instance check
// above, reduced to BLOCK granularity, because [stamp.Skip.Addr] and
// [identity.BlockDiscovery] are both keyed by [addrs.ConfigResource] with no
// instance key - a for_each block's own escalation is all-or-nothing at
// that granularity, so a block counts as record-backed only when EVERY one
// of its needs-discovery instances has a record, never on a majority.
func TestRecordBackedNeedsDiscoveryBlocks(t *testing.T) {
	ctx := context.Background()
	// A single-instance block: the corpus-alb-complete shape
	// (aws_route53_record.validation[0]) this fix was found against.
	single := mustCommandTestAddr(t, "aws_route53_record.validation")
	// A for_each block with two instances, so the "all, not majority" rule
	// has something to prove.
	pairA := mustCommandTestAddr(t, `aws_lb_target_group_attachment.this["a"]`)
	pairB := mustCommandTestAddr(t, `aws_lb_target_group_attachment.this["b"]`)

	needs := []identity.Resolution{
		{Addr: single, Class: identity.ClassNeedsDiscovery, Reason: "untaggable"},
		{Addr: pairA, Class: identity.ClassNeedsDiscovery, Reason: "untaggable"},
		{Addr: pairB, Class: identity.ClassNeedsDiscovery, Reason: "untaggable"},
	}

	t.Run("nil store is a no-op", func(t *testing.T) {
		got, diags := recordBackedNeedsDiscoveryBlocks(ctx, nil, needs)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Err())
		}
		if len(got) != 0 {
			t.Errorf("got %v, want nothing (nil store)", got)
		}
	})

	t.Run("a fully-covered single-instance block is exempt; a half-covered for_each block is not", func(t *testing.T) {
		raw, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %s", err)
		}
		const prefix = "prefix/"
		store := projection.NewRecordEnvelopeStore(raw, prefix)

		seedIdentityRecord(t, raw, prefix, single, "single-record")
		seedIdentityRecord(t, raw, prefix, pairA, "pair-a-record")
		// pairB deliberately has no record: its block must not be exempt.

		got, diags := recordBackedNeedsDiscoveryBlocks(ctx, store, needs)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Err())
		}
		singleKey := single.ConfigResource().String()
		pairKey := pairA.ConfigResource().String()
		if !got[singleKey] {
			t.Errorf("got %v, want %q exempt - its one needs-discovery instance has a record", got, singleKey)
		}
		if got[pairKey] {
			t.Errorf("got %v, want %q NOT exempt - only one of its two needs-discovery instances "+
				"(pairA) has a record; pairB still has no other handle and must keep escalating", got, pairKey)
		}
	})
}
