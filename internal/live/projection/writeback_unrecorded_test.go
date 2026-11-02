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
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// GitHub issue #2141: an instance of a type with no marker surface that the
// write-back cannot record, and no earlier record holds, is named in a
// warning instead of a TF_LOG line, on every family that has such types.
// The fixture types are deliberately not real provider types, for the reason
// writeback_unrecordable_log_test.go gives.

// unrecordedProbe is one write-back over instances of one probe type.
type unrecordedProbe struct {
	provider string
	typeName string
	// taggable gives the schema a settable top-level tags map, the marker
	// surface both AWS and Azure stamp.
	taggable bool
	// names are the instance names, one instance each.
	names []string
	// recorded seeds an identity record for the first instance before the
	// write-back, as an earlier apply or live-import would have left it.
	recorded bool
}

// run performs the write-back and returns its diagnostics and log output.
func (p unrecordedProbe) run(t *testing.T) (tfdiags.Diagnostics, string) {
	t.Helper()
	ctx := context.Background()

	backend, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	store := NewRecordEnvelopeStore(backend, "tofu-records/test-estate")

	// No "id" and no ratified row: an identity this mechanism cannot hold.
	attrs := map[string]*configschema.Attribute{
		"arn": {Type: cty.String, Computed: true},
	}
	vals := map[string]cty.Value{"arn": cty.StringVal("probe")}
	if p.taggable {
		attrs["tags"] = &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
		vals["tags"] = cty.NullVal(cty.Map(cty.String))
	}
	block := &configschema.Block{Attributes: attrs}
	schema := providers.Schema{Block: block}
	providerAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider(p.provider)}

	finalState := states.NewState()
	for i, name := range p.names {
		addr := mustAddr(t, p.typeName+"."+name)
		obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: cty.ObjectVal(vals)}
		src, err := obj.Encode(block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatalf("encoding the final object: %s", err)
		}
		finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, providerAddr, addrs.NoKey)
		if i == 0 && p.recorded {
			if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
				env.Identity = &identityPayload{ImportID: "probe-recorded-earlier"}
			}); err != nil {
				t.Fatalf("seeding the earlier record: %s", err)
			}
		}
	}

	schemas := &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			providerAddr.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{p.typeName: schema},
			},
		},
	}

	var diags tfdiags.Diagnostics
	out := captureWriteBackLog(t, func() {
		diags = WriteBack(ctx, WriteBackRequest{Store: store, FinalState: finalState, Schemas: schemas})
	})
	return diags, out
}

// unrecordedWarnings returns the [SummaryUntaggableUnrecorded] diagnostics
// in diags, failing on any other error.
func unrecordedWarnings(t *testing.T, diags tfdiags.Diagnostics) []tfdiags.Diagnostic {
	t.Helper()
	var out []tfdiags.Diagnostic
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == SummaryUntaggableUnrecorded {
			out = append(out, d)
			continue
		}
		if d.Severity() == tfdiags.Error {
			t.Fatalf("unexpected error: %s: %s", desc.Summary, desc.Detail)
		}
	}
	return out
}

func TestWriteBack_untaggableUnrecordedIsAWarning(t *testing.T) {
	for _, tc := range []struct {
		name, provider, typeName string
	}{
		{"aws", "aws", "aws_choudoufu_untaggable_probe"},
		{"azurerm", "azurerm", "azurerm_choudoufu_untaggable_probe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags, logOut := unrecordedProbe{provider: tc.provider, typeName: tc.typeName, names: []string{"b", "a"}}.run(t)
			warns := unrecordedWarnings(t, diags)
			if len(warns) != 1 {
				t.Fatalf("want one %q warning for the type, got %d: %v", SummaryUntaggableUnrecorded, len(warns), diags.ErrWithWarnings())
			}
			w := warns[0]
			if w.Severity() != tfdiags.Warning {
				t.Errorf("severity %v, want a warning", w.Severity())
			}
			detail := w.Description().Detail
			for _, want := range []string{tc.typeName, tc.typeName + ".a, " + tc.typeName + ".b", "no plan will find it", "cannot hold its identity in full"} {
				if !strings.Contains(detail, want) {
					t.Errorf("the warning does not say %q:\n%s", want, detail)
				}
			}
			if strings.Contains(logOut, "no identity record can be derived for "+tc.typeName) {
				t.Errorf("the INFO line is still printed beside the warning:\n%s", logOut)
			}
		})
	}
}

// TestWriteBack_taggableUnrecordedStaysALogLine keeps #746's behaviour for a
// type whose marker still decides ownership.
func TestWriteBack_taggableUnrecordedStaysALogLine(t *testing.T) {
	for _, provider := range []string{"aws", "azurerm"} {
		t.Run(provider, func(t *testing.T) {
			typeName := provider + "_choudoufu_taggable_probe"
			diags, logOut := unrecordedProbe{provider: provider, typeName: typeName, taggable: true, names: []string{"a"}}.run(t)
			if warns := unrecordedWarnings(t, diags); len(warns) != 0 {
				t.Fatalf("a taggable type drew %q: %s", SummaryUntaggableUnrecorded, warns[0].Description().Detail)
			}
			if !strings.Contains(logOut, "no identity record can be derived for "+typeName) {
				t.Errorf("the INFO line is gone for a taggable type:\n%s", logOut)
			}
		})
	}
}

// TestWriteBack_untaggableWithAnEarlierRecordIsNotWarned: the branch leaves
// an existing record alone, and that record still finds the object when the
// block is removed, so nothing is lost and nothing is said beyond the log.
func TestWriteBack_untaggableWithAnEarlierRecordIsNotWarned(t *testing.T) {
	diags, _ := unrecordedProbe{provider: "aws", typeName: "aws_choudoufu_untaggable_probe", names: []string{"a"}, recorded: true}.run(t)
	if warns := unrecordedWarnings(t, diags); len(warns) != 0 {
		t.Fatalf("an instance an earlier record holds drew %q: %s", SummaryUntaggableUnrecorded, warns[0].Description().Detail)
	}
}
