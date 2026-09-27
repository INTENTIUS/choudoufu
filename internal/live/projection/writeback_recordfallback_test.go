// Copyright (c) The OpenTofu Authors
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

// GitHub issue #1675: an instance this run's plan resolved through the
// record-fallback door (identity.Resolution.RecordFallback) has no marker to
// fall back on - that is the whole reason the resolver routed it here - so a
// write-back that cannot derive its identity from the applied object must
// not exit quietly. Before this fix, writeBackRecordEnvelopes's switch only
// asked "automatic" (identity.LocatedType) or "selected"
// (`markers = record`), which the record-fallback door is neither of, so a
// derivation failure there fell into the same best-effort, log-only branch
// an ordinary TAGGABLE instance gets - even though, unlike that ordinary
// instance, this one has no tag for ownership to fall back on either. The
// object would be created, the run would exit 0, and nothing would ever
// find it again.
//
// recordFallbackProbeType is deliberately not a real provider type, the same
// reason writeback_unrecordable_log_test.go's probe isn't: the branch under
// test is reached for any type whose applied object carries no usable
// identity, and naming a real one would tie the test to a table row that may
// change for unrelated reasons.
const recordFallbackProbeType = "aws_choudoufu_recordfallback_probe"

var recordFallbackProbeProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

// recordFallbackProbeBlock has no "id" attribute and no ratified row of its
// own, so identity.RecordableIdentitySchema (and therefore LocatedRecordFrom)
// answers false for it: decode succeeds, but nothing about the applied
// object can be recorded as an identity. This is the exact "recordable=false"
// shape the switch's default (quiet) branch exists for.
func recordFallbackProbeBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"arn": {Type: cty.String, Computed: true},
	}}
}

// runRecordFallbackProbe runs one write-back over a single instance of
// recordFallbackProbeType, with fallbackAddrs passed through as
// [WriteBackRequest.RecordFallbackAddrs], and returns the diagnostics.
func runRecordFallbackProbe(t *testing.T, fallbackAddrs []addrs.AbsResourceInstance) (addrs.AbsResourceInstance, tfdiags.Diagnostics) {
	t.Helper()
	ctx := context.Background()

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"

	addr := mustAddr(t, recordFallbackProbeType+".probe")
	block := recordFallbackProbeBlock()
	schema := providers.Schema{Version: 0, Block: block}

	obj := &states.ResourceInstanceObject{
		Status: states.ObjectReady,
		Value:  cty.ObjectVal(map[string]cty.Value{"arn": cty.StringVal("arn:aws:probe:fallback")}),
	}
	src, err := obj.Encode(block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the final object: %s", err)
	}
	finalState := states.NewState()
	finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, recordFallbackProbeProvider, addrs.NoKey)

	schemas := &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			recordFallbackProbeProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{recordFallbackProbeType: schema},
			},
		},
	}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:               NewRecordEnvelopeStore(store, prefix),
		FinalState:          finalState,
		Schemas:             schemas,
		RecordFallbackAddrs: fallbackAddrs,
	})
	return addr, diags
}

// TestWriteBack_OrdinaryUnrecordableStaysQuiet pins the baseline this issue
// must not disturb: an instance NOT named in RecordFallbackAddrs - the
// ordinary case, including every instance of a
// identity.RecordFallbackType-eligible type whose identity folds straight
// from configuration - still gets the quiet, best-effort log line, not an
// error, when its identity cannot be derived.
func TestWriteBack_OrdinaryUnrecordableStaysQuiet(t *testing.T) {
	_, diags := runRecordFallbackProbe(t, nil)
	if diags.HasErrors() {
		t.Fatalf("an instance not routed through the record-fallback door must not error on an unrecordable identity: %s", diags.Err())
	}
}

// TestWriteBack_RecordFallbackDerivationFailureIsLoud is GitHub issue #1675
// itself. An instance the plan resolved through the record-fallback door has
// no marker, so a write-back that cannot derive its identity must raise a
// loud, run-stopping error naming the instance - not the quiet log line an
// ordinary instance gets - because the record this pass failed to write was
// this instance's ONLY surviving identity carrier.
func TestWriteBack_RecordFallbackDerivationFailureIsLoud(t *testing.T) {
	fallbackAddr := mustAddr(t, recordFallbackProbeType+".probe")
	_, diags := runRecordFallbackProbe(t, []addrs.AbsResourceInstance{fallbackAddr})

	if !diags.HasErrors() {
		t.Fatal("a record-fallback instance whose identity could not be derived from the applied object exited with no error; nothing will ever find this object again")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Description().Summary, "Cannot record a located identity") &&
			strings.Contains(d.Description().Detail, fallbackAddr.String()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no diagnostic named the record-fallback instance %s; an operator reading this run's output would not know which object was orphaned:\n%v", fallbackAddr, diags.Err())
	}
}
