// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #746's review finding B4: writeBackRecordEnvelopes
// printed ONE once-per-type line for two different situations - an identity
// this fork structurally cannot derive, and the deliberate refusal to write a
// sensitive attribute into the record store - which is the exact distinction
// the branch's own comment says must never blur. The assertions are on the
// rendered lines, by value, not on which branch was taken.

// captureWriteBackLog redirects the standard logger for the duration of fn
// and returns what was written, the same shape internal/live/cloudcontrol's
// own request-log test uses.
func captureWriteBackLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})
	fn()
	return buf.String()
}

// unrecordableProbeProvider and the type name below are deliberately not any
// real provider type: the branch under test is reached for any type with no
// ratified row whose identity a record cannot hold, and naming a real one
// would tie the test to a table row that may change for unrelated reasons.
var unrecordableProbeProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

const unrecordableProbeType = "aws_choudoufu_writeback_probe"

// writeBackUnrecordableLog runs one write-back over a single instance of
// unrecordableProbeType carrying block, and returns the once-per-type line
// the "not recordable, not this instance's only carrier" branch printed.
func writeBackUnrecordableLog(t *testing.T, block *configschema.Block, val cty.Value) string {
	t.Helper()
	ctx := context.Background()

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"

	addr := mustAddr(t, unrecordableProbeType+".probe")
	schema := providers.Schema{Version: 0, Block: block}

	obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: val}
	src, err := obj.Encode(block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the final object: %s", err)
	}
	finalState := states.NewState()
	finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, unrecordableProbeProvider, addrs.NoKey)

	schemas := &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			unrecordableProbeProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{unrecordableProbeType: schema},
			},
		},
	}

	out := captureWriteBackLog(t, func() {
		WriteBack(ctx, WriteBackRequest{
			Store:      NewRecordEnvelopeStore(store, prefix),
			FinalState: finalState,
			Schemas:    schemas,
		})
	})

	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "identity record") && strings.Contains(l, unrecordableProbeType) {
			if line != "" {
				t.Fatalf("the once-per-type line was printed more than once:\n%s", out)
			}
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no once-per-type unrecordable line at all; the branch under test was not reached:\n%s", out)
	}
	return line
}

// TestWriteBack_unrecordableLogSeparatesDeliberateFromStructural is the
// finding itself. The two situations must produce two different lines, and
// the deliberate one must name the attribute whose sensitivity is the
// reason - which is the whole difference between "this fork cannot" and
// "this fork will not".
func TestWriteBack_unrecordableLogSeparatesDeliberateFromStructural(t *testing.T) {
	// Deliberate: the whole identity a record would hold is "id", and the
	// provider marks it sensitive.
	deliberate := writeBackUnrecordableLog(t, &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true, Sensitive: true},
	}}, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("probe-1")}))

	// Structural: no "id" at all, and no ratified row naming anything else,
	// so there is no identity to hold in the first place.
	structural := writeBackUnrecordableLog(t, &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"arn": {Type: cty.String, Computed: true},
	}}, cty.ObjectVal(map[string]cty.Value{"arn": cty.StringVal("arn:aws:probe")}))

	if deliberate == structural {
		t.Fatalf("the deliberate refusal and the structural one still print the identical line:\n%s", deliberate)
	}
	if !strings.Contains(deliberate, `"id"`) {
		t.Errorf("the deliberate line does not name the sensitive attribute it refused on:\n%s", deliberate)
	}
	if !strings.Contains(deliberate, "on purpose") {
		t.Errorf("the deliberate line does not say the refusal was deliberate:\n%s", deliberate)
	}
	if !strings.Contains(structural, "cannot") {
		t.Errorf("the structural line does not say the identity could not be derived:\n%s", structural)
	}
	if strings.Contains(structural, "on purpose") {
		t.Errorf("the structural line reads as a deliberate refusal, which would send an operator after a setting that would not help:\n%s", structural)
	}
}

// TestWriteBack_unrecordableLogGuardsCanFail records the mutations each
// assertion above was proven red under.
//
//   - Collapsing the two branches back to the single pre-#746 line makes
//     TestWriteBack_unrecordableLogSeparatesDeliberateFromStructural fail at
//     "still print the identical line".
//   - Swapping identity.SensitiveIdentityAttr for a constant "" makes it
//     fail at "still print the identical line" too: with no discriminator
//     both fixtures take the structural arm, which is the same collapse
//     seen from the other side.
//   - Swapping it for a whole-schema sensitivity sweep makes it fail at
//     "the structural line reads as a deliberate refusal", because the
//     structural fixture's own attribute would then be reported.
//
// The test body itself asserts nothing; the comment is the artifact.
func TestWriteBack_unrecordableLogGuardsCanFail(t *testing.T) {}
