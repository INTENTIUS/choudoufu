// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/zclconf/go-cty/cty"

	terraformProvider "github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveEstateOutputs answers the builtin terraform provider's
// terraform_estate_outputs data source (GitHub issue #1371) for one command.
//
// The provider is constructed when the command builds its provider factories,
// well before a live run has opened its record store, so it is handed this
// holder and the run fills it in once the store is open: in PriorState for
// plain plan and apply under a live block, and after the store opens for
// live-plan. A command that never fills it (any stock run) answers every read
// with the provider's own "needs a live block" refusal.
type liveEstateOutputs struct {
	mu  sync.Mutex
	src *projection.EstateOutputsSource

	// planned are GitHub issue #2051's sources: an estate named by
	// -estate-outputs-from is answered from its plan, never the store.
	planned map[string]*projection.PlannedEstateOutputs

	// fromPlan is what was answered from a plan, by estate and output name,
	// as canonical JSON: the record a saved plan carries, so its apply can
	// refuse when the upstream's applied values differ.
	fromPlan map[string]map[string]string
}

var _ terraformProvider.EstateOutputReader = (*liveEstateOutputs)(nil)

// liveEstateOutputs returns this command's holder, creating it on first use.
// Every provider factory and every live run in one command must see the same
// one, which is why it hangs off Meta rather than being built per call.
func (m *Meta) liveEstateOutputs() *liveEstateOutputs {
	if m.estateOutputs == nil {
		m.estateOutputs = &liveEstateOutputs{}
	}
	return m.estateOutputs
}

// open records the store this run reads other estates' outputs from. store
// may be nil, for live-plan going on past an unreachable store; unavailable
// then says why, and every read refuses with it.
func (l *liveEstateOutputs) open(store staterecord.Store, rs *configs.LiveRecordStore, estate, unavailable string) {
	if l == nil {
		return
	}
	// A "kubernetes" store also carries the record_store block's
	// reads_outputs_of declarations and a way to open the namespaces they
	// name; see projection.NewEstateOutputsSource.
	src := projection.NewEstateOutputsSource(store, rs, estate, unavailable)
	l.mu.Lock()
	l.src = src
	l.mu.Unlock()
}

// ReadEstateOutputs implements [terraformProvider.EstateOutputReader].
func (l *liveEstateOutputs) ReadEstateOutputs(ctx context.Context, estate string, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	var src *projection.EstateOutputsSource
	var planned *projection.PlannedEstateOutputs
	if l != nil {
		l.mu.Lock()
		src = l.src
		planned = l.planned[estate]
		l.mu.Unlock()
	}
	if src == nil {
		var diags tfdiags.Diagnostics
		return nil, diags.Append(terraformProvider.EstateOutputsNeedLiveBlock())
	}
	// GitHub issue #2051. A self-read still goes to the store's path, which
	// refuses it in its own words.
	if planned != nil && estate != src.Estate {
		values, diags := projection.ReadPlannedEstateOutputs(planned, names)
		if !diags.HasErrors() {
			l.noteFromPlan(estate, values)
		}
		return values, diags
	}
	return projection.ReadEstateOutputs(ctx, *src, estate, names)
}

func (l *liveEstateOutputs) noteFromPlan(estate string, values map[string]cty.Value) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fromPlan == nil {
		l.fromPlan = map[string]map[string]string{}
	}
	if l.fromPlan[estate] == nil {
		l.fromPlan[estate] = map[string]string{}
	}
	for name, v := range values {
		// A value that will not render cannot be compared at apply, so it
		// is recorded as such and the apply refuses on it.
		text, err := projection.CanonicalOutputJSON(v)
		if err != nil {
			text = "(unrenderable: " + err.Error() + ")"
		}
		l.fromPlan[estate][name] = text
	}
}

// usePlanned installs -estate-outputs-from's documents (GitHub issue #2051).
// Each is read now, so a missing or malformed file fails the run before
// anything is planned.
func (l *liveEstateOutputs) usePlanned(from []arguments.EstateOutputsFrom) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	planned := map[string]*projection.PlannedEstateOutputs{}
	for _, f := range from {
		data, err := os.ReadFile(f.Path)
		if err == nil {
			planned[f.Estate], err = projection.ParsePlannedEstateOutputs(f.Estate, f.Path, data)
		}
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read another estate's plan", fmt.Sprintf(
				"-estate-outputs-from %s=%s: %s. The file is what \"choudoufu show -json PLANFILE\" prints for estate %q's saved plan.",
				f.Estate, f.Path, err, f.Estate)))
		}
	}
	if diags.HasErrors() {
		return diags
	}
	l.mu.Lock()
	l.planned = planned
	l.mu.Unlock()
	return diags
}

// estateOutputsFromPlanRecord is the #2051 entry of a saved plan file.
type estateOutputsFromPlanRecord struct {
	FormatVersion string `json:"format_version"`
	// Estates maps each upstream estate to the outputs read from its plan,
	// name to canonical JSON value.
	Estates map[string]map[string]string `json:"estates"`
}

// planFileRecord is what a saved plan embeds: nil when nothing was read from
// a plan, so a plan file made without -estate-outputs-from is unchanged.
func (l *liveEstateOutputs) planFileRecord() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fromPlan) == 0 {
		return nil
	}
	b, err := json.Marshal(estateOutputsFromPlanRecord{FormatVersion: "1", Estates: l.fromPlan})
	if err != nil {
		return nil
	}
	return b
}
