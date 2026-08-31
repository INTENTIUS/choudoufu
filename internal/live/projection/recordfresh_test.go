// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestWriteBackReadsAreNeverServedFromTheSnapshot proves the safety bound on
// [staterecord.RunCache] at the layer that depends on it. Three classes of
// read decide what gets WRITTEN, so a remembered value in any of them is a
// lost update rather than a slow run:
//
//   - mergeEnvelope's own read-modify-write read,
//   - currentVersion, which exists to observe the store's version now so a
//     compare-and-swap still catches a writer outside this run,
//   - the seeders' read-before-write halves (recordseed, locatedseed,
//     tombstoneseed, residue's write-back), which read a version and then
//     assert it.
//
// Every assertion here runs BEFORE anything is written, on purpose: the cache
// switches itself off permanently at the first write, so a test that wrote
// first would pass against a cache that had simply stopped existing. The
// first assertion is what proves the cache was on at all.
func TestWriteBackReadsAreNeverServedFromTheSnapshot(t *testing.T) {
	ctx := context.Background()
	counting := staterecord.NewCountingStore(localHintStore(t), nil)
	prefix := RecordKeyPrefix("fresh-estate")
	store := NewRecordEnvelopeStore(staterecord.NewRunCache(counting, prefix), prefix)
	addr := locatedTestAddr(t, "aws_globalaccelerator_listener", "svc")

	// A plan-path read, twice. The second must cost nothing - without this
	// every assertion below would pass against a cache that was never on.
	if _, _, _, _, err := store.GetIdentity(ctx, addr); err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	primed := counting.Total()
	if _, _, _, _, err := store.GetIdentity(ctx, addr); err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if counting.Total() != primed {
		t.Fatalf("a repeated plan-path read cost %d extra trips; the cache is not serving, so nothing below is a real check",
			counting.Total()-primed)
	}

	costOf := func(t *testing.T, label string, fn func() error) {
		t.Helper()
		before := counting.Total()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got := counting.Total() - before; got != 1 {
			t.Errorf("%s made %d store trips, want 1 - it was served from the snapshot", label, got)
		}
	}

	costOf(t, "currentVersion", func() error {
		_, err := store.currentVersion(ctx, addr)
		return err
	})
	costOf(t, "getRawFresh (recordseed, tombstoneseed)", func() error {
		_, _, _, err := store.getRawFresh(ctx, addr)
		return err
	})
	costOf(t, "GetIdentityFresh (locatedseed)", func() error {
		_, _, _, _, err := store.GetIdentityFresh(ctx, addr)
		return err
	})
	costOf(t, "GetResidueFresh (residue write-back)", func() error {
		_, _, _, _, err := store.GetResidueFresh(ctx, addr)
		return err
	})

	// mergeEnvelope last, because it is the one that writes.
	before := counting.Total()
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "arn:aws:example"}
	}); err != nil {
		t.Fatalf("mergeEnvelope: %v", err)
	}
	gets := 0
	for _, tr := range counting.Trips()[before:] {
		if tr.Method == "Get" {
			gets++
		}
	}
	if gets != 1 {
		t.Errorf("mergeEnvelope made %d Get trips, want 1 - its read-modify-write read came from the snapshot", gets)
	}
}

// TestSeederCallSitesUseTheFreshAccessors is the other half. The test above
// proves the fresh accessors bypass the cache; this proves the four
// read-before-write call sites actually call them. A future edit that
// "simplified" one back to the plain accessor would pass every behavioural
// test in this package and reintroduce a lost update.
func TestSeederCallSitesUseTheFreshAccessors(t *testing.T) {
	plain := []string{".getRaw(", ".GetIdentity(", ".GetResidue("}
	for _, file := range []string{"recordseed.go", "locatedseed.go", "tombstoneseed.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for _, call := range plain {
			if strings.Contains(string(data), call) {
				t.Errorf("%s calls %s: a seeder's read-before-write must use the Fresh accessor, "+
					"or the version it asserts can come from a snapshot", file, strings.Trim(call, ".("))
			}
		}
	}

	// residue.go's write-back read, and mergeEnvelope's own, named
	// individually because both files legitimately contain plan-path reads
	// through the cached accessors too.
	for file, want := range map[string]string{
		"residue.go": "store.GetResidueFresh(ctx, addr)",
		"record.go":  "current, _, exists, err := s.getRawFresh(ctx, addr)",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s no longer contains %q: its write-back read may be served from a snapshot", file, want)
		}
	}
}
