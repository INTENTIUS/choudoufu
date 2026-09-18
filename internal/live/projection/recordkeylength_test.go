// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1283.
//
// [RecordKey] base64url-encodes an address, which EXPANDS it 4/3, and the
// implied local store writes the result as a filename. A filesystem bounds
// one path component at NAME_MAX (255, measured on APFS), so a single
// encoded segment could hold at most a 191-byte address - while the only
// lint-time bound on an address, markers.MaxAddressLen, is 1024. Every
// address in that 5.4x window passes lint and cannot be written.
//
// The severity is not the failed write. For a record-rung instance the
// record IS the ownership, so a record that was never written is a live
// object nothing claims, and the very next plan proposes CREATING it - a
// second copy of a resource that already exists. That is what these tests
// assert: not that the store write succeeds, but that the plan built
// afterwards proposes nothing.

// longForEachKey is the 90-character for_each key GitHub issue #1283
// measured with: an ordinary factory-module key, not a stress fixture.
var longForEachKey = strings.Repeat("region-scoped-issuing-authority-", 2) + strings.Repeat("x", 90-64)

// writeLongAddressFixture writes the two-module-deep shape issue #1283
// measured: a shared platform module calling a per-region factory under
// for_each, whose resource is itself under a for_each with a long key. It
// returns the directory and the resulting instance address.
func writeLongAddressFixture(t *testing.T) (dir string, addr addrs.AbsResourceInstance) {
	t.Helper()
	dir = t.TempDir()

	write := func(rel, src string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("creating %s: %s", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatalf("writing %s: %s", full, err)
		}
	}

	write("main.tf", `
module "platform_networking_shared" {
  source = "./mod_shared"
}
`)
	write("mod_shared/main.tf", `
module "per_region_subnet_factory" {
  source   = "./mod_factory"
  for_each = toset(["eu-central-1"])
}
`)
	write("mod_factory/main.tf", fmt.Sprintf(`
resource "null_resource" "issuing_ca" {
  for_each = toset([%q])
  triggers = {
    input = "value"
  }
}
`, longForEachKey))

	addr = mustAddr(t, fmt.Sprintf(
		`module.platform_networking_shared.module.per_region_subnet_factory["eu-central-1"].null_resource.issuing_ca[%q]`,
		longForEachKey))
	return dir, addr
}

// TestRecordKeyLengthDoesNotProposeADuplicateCreate is the decisive arm.
// It runs a real plan over the projection a migration leaves behind, and
// the claim is about the plan's ACTION for the instance: NoOp, because the
// record was written and read back. Before chunking existed, the seed
// failed with "file name too long", the projection carried no prior state
// for the instance, and this plan proposed plans.Create over a live object
// that already exists.
func TestRecordKeyLengthDoesNotProposeADuplicateCreate(t *testing.T) {
	ctx := context.Background()
	staterecord.ResetRunCacheForTest(t)
	dir, addr := writeLongAddressFixture(t)

	// The premise: this address is nowhere near the only lint-time bound
	// there is, so nothing refuses the configuration that produces it.
	if got := len(addr.String()); got > markers.MaxAddressLen {
		t.Fatalf("the fixture address is %d characters, past markers.MaxAddressLen (%d) - lint would refuse it "+
			"and the case under test could not arise", got, markers.MaxAddressLen)
	}
	if got := len(recordKeyEncoding.EncodeToString([]byte(addr.String()))); got <= recordKeyLegacySegmentMax {
		t.Fatalf("the fixture's encoded address is %d bytes, within the %d-byte single segment the pre-chunking "+
			"key shape could hold - this fixture no longer reaches the case under test", got, recordKeyLegacySegmentMax)
	}

	cfg := loadConfigWithModules(t, dir)
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}

	// The migration half: exactly what liveimport's recordOne does for a
	// record-rung instance it found in the state file.
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("already-live-in-the-cloud"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	seeded, err := testSeedRecordForInstance(ctx, store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil {
		// Deliberately not fatal: the point of this test is the PLAN the
		// failure produces, so the failed write is reported and the run
		// continues to the arm that shows what it costs.
		t.Errorf("seeding the record for a %d-character address failed: %s", len(addr.String()), err)
	} else if seeded != SeedWritten {
		t.Errorf("seeding the record reported %v, want SeedWritten", seeded)
	}

	// The plan half, in a LATER process: the projection a live-plan builds
	// from that store, then a real plan against it.
	//
	// The store is wrapped the way [NewRecordStore] wraps every production
	// one - in a [staterecord.RunCache], which bulk-loads the namespace and
	// answers "no record" for any key the load did not return. That wrapper
	// is what makes the missing record SILENT rather than loud: read
	// directly, an over-long key gives the local store ENAMETOOLONG, which
	// every reader in this package turns into a hard "Cannot read a
	// persisted record" error. Read through the snapshot, the key that was
	// never written is simply absent, and absent is the ordinary shape of a
	// resource that does not exist yet.
	//
	// ResetRunCacheForTest above restores the cache's process-wide
	// kill-switch, which the seed's own write would otherwise have thrown
	// for the rest of this test binary. In production the migration and the
	// plan are separate processes, so the plan really does start with a
	// live cache.
	cached := staterecord.NewRunCache(store, seedPrefix)
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}},
		SingleProvider(nullProvider, nullResourceProvider()),
		Options{RecordStore: NewRecordEnvelopeStore(cached, seedPrefix)})
	assertNoErrors(t, diags)

	core, ctxDiags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("null"): func() (providers.Interface, error) { return nullResourceProvider(), nil },
		}, nil),
	})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}
	plan, planDiags := core.Plan(ctx, cfg, res.State, &tofu.PlanOpts{Mode: plans.NormalMode})
	if planDiags.HasErrors() {
		t.Fatalf("planning against the projected state: %s", planDiags.Err())
	}

	change := plan.Changes.ResourceInstance(addr)
	if change == nil {
		t.Fatalf("the plan holds no change at all for %s; changes: %v", addr, changedAddrs(plan))
	}
	if change.Action == plans.Create {
		t.Fatalf("the plan proposes CREATING %s, which already exists live: the record the migration wrote "+
			"could not be keyed, so the projection carried no prior state for it and the plan proposes a "+
			"duplicate of a live resource", addr)
	}
	if change.Action != plans.NoOp {
		t.Errorf("the plan proposes %s for %s, want NoOp", change.Action, addr)
	}
}

// changedAddrs renders a plan's changed addresses for a failure message.
func changedAddrs(plan *plans.Plan) []string {
	var out []string
	for _, c := range plan.Changes.Resources {
		out = append(out, c.Addr.String()+"="+c.Action.String())
	}
	return out
}

// TestRecordKeyChunkingIsReversible is the half orphan discovery depends
// on: a key is only useful if the address comes back out of it. Chunking
// puts "/" inside what used to be one segment, and a reader that split on
// the LAST "/" would decode a truncated base64 run - which fails, silently,
// exactly for the long addresses chunking exists to serve.
func TestRecordKeyChunkingIsReversible(t *testing.T) {
	for _, rawLen := range []int{len("null_resource.x"), 100, 191, 192, 255, recordKeyChunkLen, recordKeyChunkLen + 1, 400, 690, markers.MaxAddressLen} {
		t.Run(fmt.Sprintf("raw-%d", rawLen), func(t *testing.T) {
			name := strings.Repeat("n", rawLen-len("null_resource."))
			addr := mustAddr(t, "null_resource."+name)
			if len(addr.String()) != rawLen {
				t.Fatalf("built a %d-character address, want %d", len(addr.String()), rawLen)
			}
			key := RecordKey(seedPrefix, addr)
			for _, seg := range strings.Split(key, "/") {
				if seg == "" {
					t.Fatalf("key %q has an empty segment", key)
				}
				if len(seg) > recordKeyLegacySegmentMax {
					t.Errorf("key segment is %d bytes, past the %d-byte NAME_MAX ceiling a local store writes it under", len(seg), recordKeyLegacySegmentMax)
				}
			}
			got, ok := RecordAddr(seedPrefix, key)
			if !ok {
				t.Fatalf("RecordAddr could not read %q back", key)
			}
			if got.String() != addr.String() {
				t.Errorf("round-tripped to %s, want %s", got, addr)
			}
		})
	}
}

// TestRecordKeyIsUnchangedBelowTheLegacyCeiling is the compatibility pin.
// Chunking that respelled a key already in a store would orphan its record
// - and an orphaned record is precisely the duplicate-create this issue is
// about, inflicted on an estate that was working. So every encoding that
// fit in the pre-chunking single segment must still be spelled as one.
func TestRecordKeyIsUnchangedBelowTheLegacyCeiling(t *testing.T) {
	// The longest address whose encoding still fits one 255-byte segment.
	const longestUnchunkedRaw = 191
	for _, rawLen := range []int{len("null_resource.x"), 100, longestUnchunkedRaw} {
		addr := mustAddr(t, "null_resource."+strings.Repeat("n", rawLen-len("null_resource.")))
		want := seedPrefix + "/null_resource/" + recordKeyEncoding.EncodeToString([]byte(addr.String()))
		if got := RecordKey(seedPrefix, addr); got != want {
			t.Errorf("a %d-character address is now keyed as\n %q\nwant the pre-chunking spelling\n %q", rawLen, got, want)
		}
	}
	// One past it must chunk, or the fix does nothing.
	addr := mustAddr(t, "null_resource."+strings.Repeat("n", longestUnchunkedRaw+1-len("null_resource.")))
	if got := RecordKey(seedPrefix, addr); !strings.Contains(strings.TrimPrefix(got, seedPrefix+"/null_resource/"), "/") {
		t.Errorf("a %d-character address is still keyed as one segment: %q", longestUnchunkedRaw+1, got)
	}
}

// TestRecordKeyChunkedKeysSurviveTheStore closes the seam between the key
// and the backend that actually has to hold it: a chunked key has to be
// writable, readable, listable and deletable through a real
// [staterecord.LocalStore], including the update path, which writes two
// sidecars named after the leaf and so fails at a shorter leaf than a
// create does.
func TestRecordKeyChunkedKeysSurviveTheStore(t *testing.T) {
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	// 400 characters: comfortably past the 191-byte single-segment ceiling
	// this chunking removes, and comfortably inside the one it leaves -
	// PATH_MAX over the whole path, which is a property of where the store
	// directory sits rather than of the address. See GitHub issue #1283 for
	// that residual ceiling; it is not what this test is about.
	addr := mustAddr(t, "null_resource."+strings.Repeat("n", 400-len("null_resource.")))
	key := RecordKey(seedPrefix, addr)

	version, err := store.PutIfAbsent(ctx, key, []byte(`{"v":1}`))
	if err != nil {
		t.Fatalf("PutIfAbsent on a chunked key: %s", err)
	}
	if _, err := store.PutIfVersion(ctx, key, []byte(`{"v":2}`), version); err != nil {
		t.Fatalf("PutIfVersion on a chunked key (the sidecar path): %s", err)
	}
	keys, err := store.List(ctx, seedPrefix)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("List returned %v, want exactly the chunked key back", keys)
	}
	back, ok := RecordAddr(seedPrefix, keys[0])
	if !ok || back.String() != addr.String() {
		t.Errorf("a listed chunked key did not read back as its address: ok=%v got=%s", ok, back)
	}
}

// TestRecordKeyStaysInsideTheRemoteBackendsShape is the other half of the
// bound check, for the two backends whose ceilings are API limits rather
// than filesystem ones. Neither is measured against a live service here -
// both are quoted from the SDK's own documented constraints:
//
//   - An S3 object key is at most 1024 bytes.
//   - An SSM parameter name is at most 1011 characters INCLUDING the ARN
//     that precedes it (45 characters for arn:aws:ssm:us-east-2:<account>:parameter/,
//     more in a longer region or partition), and a parameter hierarchy is
//     at most fifteen levels deep.
//
// The depth limit is the one chunking can newly violate, since chunking's
// whole mechanism is adding "/" to a key. This pins that it does not, and
// pins the byte cost so a future change to the chunk length cannot quietly
// push a remote key past a ceiling that has no local error to catch it.
func TestRecordKeyStaysInsideTheRemoteBackendsShape(t *testing.T) {
	const ssmMaxHierarchyDepth = 15

	// The longest address the only lint-time bound, markers.MaxAddressLen,
	// admits - so the worst case a configuration can reach at all.
	addr := mustAddr(t, "null_resource."+strings.Repeat("n", markers.MaxAddressLen-len("null_resource.")))
	key := RecordKey(RecordKeyPrefix("prod"), addr)

	// An SSM parameter name is "/" + key, so the key's own segment count is
	// its depth.
	if depth := len(strings.Split(key, "/")); depth > ssmMaxHierarchyDepth {
		t.Errorf("the key for the longest address lint admits is %d levels deep, past SSM's limit of %d", depth, ssmMaxHierarchyDepth)
	}

	// Chunking costs one byte per chunk boundary and nothing else, which is
	// what keeps it from materially moving the two byte-counted ceilings.
	unchunked := len(RecordKeyPrefix("prod")) + len("/null_resource/") + recordKeyEncoding.EncodedLen(len(addr.String()))
	if extra := len(key) - unchunked; extra != (recordKeyEncoding.EncodedLen(len(addr.String()))-1)/recordKeyChunkLen {
		t.Errorf("chunking added %d bytes to the key, want one per chunk boundary", extra)
	}
}
