// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// seedObjectRecord writes a kind=object record for addr and returns the
// version the store assigned, the read a plan would have made.
func seedObjectRecord(t *testing.T, ctx context.Context, store staterecord.Store, prefix string, addr addrs.AbsResourceInstance) string {
	t.Helper()
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(addr.String()),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	payload, err := encodeRecordPayload(val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding %s: %s", addr, err)
	}
	version, err := store.PutIfAbsent(ctx, RecordKey(prefix, addr), payload)
	if err != nil {
		t.Fatalf("seeding %s: %s", addr, err)
	}
	return version
}

func errorSummaries(diags tfdiags.Diagnostics) []string {
	var out []string
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			out = append(out, d.Description().Summary+": "+d.Description().Detail)
		}
	}
	return out
}

// TestWholeDestroyFailsWhileARecordedInstanceSurvives is GitHub issue
// #1355's shape at the write-back layer. The estate is the issue's: one
// record-backed resource over for_each = ["a.b", "plain"], both records in
// the store. The destroy's plan materialized only "a.b" (its read of
// "plain" said absent), so PriorVersions names "a.b" alone and the final
// state is empty. Before the guard, WriteBack deleted "a.b", left "plain"
// untouched and returned no error: the run printed "1 destroyed" and exited
// 0 with an instance still recorded. It must now be an error naming
// "plain", and "plain"'s record must still be there (the guard reports, it
// does not delete what the plan never saw).
//
// A neighbour estate whose prefix shares this one's as a string prefix
// holds a record too, and must not be named: the guard reads this estate's
// namespace only.
func TestWholeDestroyFailsWhileARecordedInstanceSurvives(t *testing.T) {
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/est"

	ab := mustAddr(t, `null_resource.effect["a.b"]`)
	plain := mustAddr(t, `null_resource.effect["plain"]`)
	abVersion := seedObjectRecord(t, ctx, store, prefix, ab)
	seedObjectRecord(t, ctx, store, prefix, plain)
	neighbour := mustAddr(t, `null_resource.other`)
	seedObjectRecord(t, ctx, store, "tofu-records/est-2", neighbour)

	diags := WriteBack(ctx, WriteBackRequest{
		Store:         NewRecordEnvelopeStore(store, prefix),
		PriorVersions: []RecordVersion{{Addr: ab, Version: abVersion}},
		FinalState:    states.NewState(),
		Schemas:       nullSchemas(),
		WholeDestroy:  true,
	})

	errs := errorSummaries(diags)
	if len(errs) != 1 {
		t.Fatalf("want exactly one error naming the surviving instance, got %d: %q", len(errs), errs)
	}
	if !strings.Contains(errs[0], "The destroy left record-backed instances behind") || !strings.Contains(errs[0], plain.String()) {
		t.Errorf("error does not name the surviving instance %s: %s", plain, errs[0])
	}
	if strings.Contains(errs[0], ab.String()) {
		t.Errorf("error names %s, whose record the destroy did delete: %s", ab, errs[0])
	}
	if strings.Contains(errs[0], neighbour.String()) {
		t.Errorf("error names the neighbour estate's %s: %s", neighbour, errs[0])
	}

	if _, _, exists, err := store.Get(ctx, RecordKey(prefix, ab)); err != nil || exists {
		t.Errorf("%s's record: exists=%v err=%v, want deleted", ab, exists, err)
	}
	if _, _, exists, err := store.Get(ctx, RecordKey(prefix, plain)); err != nil || !exists {
		t.Errorf("%s's record: exists=%v err=%v, want left in place", plain, exists, err)
	}
}

// TestWholeDestroyOfEveryInstanceIsClean is the control: the same estate
// with both instances in the plan destroys both and raises nothing, and a
// destroy that is not a whole destroy (a -target) is never checked, because
// an instance it left alone is meant to survive.
func TestWholeDestroyOfEveryInstanceIsClean(t *testing.T) {
	ctx := context.Background()

	t.Run("both planned", func(t *testing.T) {
		store, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("building the local store: %s", err)
		}
		const prefix = "tofu-records/est"
		ab := mustAddr(t, `null_resource.effect["a.b"]`)
		plain := mustAddr(t, `null_resource.effect["plain"]`)
		abVersion := seedObjectRecord(t, ctx, store, prefix, ab)
		plainVersion := seedObjectRecord(t, ctx, store, prefix, plain)

		diags := WriteBack(ctx, WriteBackRequest{
			Store: NewRecordEnvelopeStore(store, prefix),
			PriorVersions: []RecordVersion{
				{Addr: ab, Version: abVersion},
				{Addr: plain, Version: plainVersion},
			},
			FinalState:   states.NewState(),
			Schemas:      nullSchemas(),
			WholeDestroy: true,
		})
		assertNoErrors(t, diags)
	})

	t.Run("targeted destroy", func(t *testing.T) {
		store, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("building the local store: %s", err)
		}
		const prefix = "tofu-records/est"
		ab := mustAddr(t, `null_resource.effect["a.b"]`)
		plain := mustAddr(t, `null_resource.effect["plain"]`)
		abVersion := seedObjectRecord(t, ctx, store, prefix, ab)
		seedObjectRecord(t, ctx, store, prefix, plain)

		diags := WriteBack(ctx, WriteBackRequest{
			Store:         NewRecordEnvelopeStore(store, prefix),
			PriorVersions: []RecordVersion{{Addr: ab, Version: abVersion}},
			FinalState:    states.NewState(),
			Schemas:       nullSchemas(),
			WholeDestroy:  false,
		})
		assertNoErrors(t, diags)
	})
}

// TestWholeDestroyGuardReadsPastTheRunCache pins that the guard does not
// ask the plan-phase snapshot again. The store is wrapped the way
// production wraps it ([staterecord.NewRunCache] under
// [NewRecordEnvelopeStore]) over a backend whose bulk read drops "plain",
// the issue-era short snapshot, so a listing served from that snapshot
// does not name "plain" either.
//
// The plan here saw neither instance, so WriteBack writes nothing and the
// cache is still serving when the guard runs (a write anywhere switches it
// off for the process, which is why [staterecord.ResetRunCacheForTest] is
// needed for this premise to hold). A guard that listed through the cache
// would name "a.b" alone; reading past it names both.
func TestWholeDestroyGuardReadsPastTheRunCache(t *testing.T) {
	staterecord.ResetRunCacheForTest(t)
	ctx := context.Background()
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/est"
	ab := mustAddr(t, `null_resource.effect["a.b"]`)
	plain := mustAddr(t, `null_resource.effect["plain"]`)
	seedObjectRecord(t, ctx, local, prefix, ab)
	seedObjectRecord(t, ctx, local, prefix, plain)

	backend := &jitterStore{LocalStore: local, drop: RecordKey(prefix, plain)}
	rs := NewRecordEnvelopeStore(staterecord.NewRunCache(backend, prefix), prefix)

	diags := WriteBack(ctx, WriteBackRequest{
		Store:        rs,
		FinalState:   states.NewState(),
		Schemas:      nullSchemas(),
		WholeDestroy: true,
	})
	errs := errorSummaries(diags)
	if len(errs) != 1 {
		t.Fatalf("want one error naming both survivors, got %d: %q", len(errs), errs)
	}
	for _, a := range []addrs.AbsResourceInstance{ab, plain} {
		if !strings.Contains(errs[0], a.String()) {
			t.Errorf("error does not name %s (a listing served from the plan-phase snapshot would miss it): %s", a, errs[0])
		}
	}
}

// nullSchemas is the schema set a null_resource final state decodes with.
func nullSchemas() *tofu.Schemas {
	return &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": nullResourceSchema()},
			},
		},
	}
}
