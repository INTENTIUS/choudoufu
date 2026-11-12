// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// choudoufu fork port (#2370): remove when upstream #4058 lands. The whole
// file is the apply engine's side of package inrunestates: the deferred read
// of an in-run estate, and each in-run estate module's outputs carried into
// the final state.

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/lang/grapheval"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkModuleOutputs is the evaluator's side; *eval.ApplyOracle has it.
type forkModuleOutputs interface {
	ForkModuleOutputs(ctx context.Context, addr addrs.ModuleInstance) cty.Value
}

var _ forkModuleOutputs = (*eval.ApplyOracle)(nil)

func (ops *execOperations) forkOutputs(ctx context.Context) func(addrs.ModuleInstance) cty.Value {
	return func(addr addrs.ModuleInstance) cty.Value {
		o, ok := ops.configOracle.(forkModuleOutputs)
		if !ok {
			return cty.NilVal
		}
		return o.ForkModuleOutputs(ctx, addr)
	}
}

// forkInRunRead reports whether desired is a terraform_estate_outputs read
// the port answers from a module this run applies. Any other deferred read
// is the datasources port's (#2414), while it is on.
func (ops *execOperations) forkInRunRead(desired *eval.DesiredResourceInstance) bool {
	if !inrunestates.IsEstateOutputs(desired.Addr) {
		return false
	}
	configVal, _ := desired.ConfigVal.UnmarkDeep()
	_, inRun, _ := ops.inRunEstates.Target(desired.Addr, configVal)
	return inRun
}

// forkDataRead answers a terraform_estate_outputs read the plan deferred,
// from the outputs of the module it names, once that module's changes are
// applied, and saves the result to the state.
func (ops *execOperations) forkDataRead(ctx context.Context, desired *eval.DesiredResourceInstance) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	addr := desired.Addr
	configVal, _ := desired.ConfigVal.UnmarkDeep()
	val, moreDiags := ops.inRunEstates.ApplyValue(addr, configVal, ops.forkOutputs(ctx))
	diags = diags.Append(moreDiags)
	if diags.HasErrors() {
		return nil, diags
	}

	meta := exec.BuildResourceInstanceObjectMeta(addr.CurrentObject(), ops.configOracle.ResourceInstanceObjectMeta(ctx, addr.CurrentObject()), (*states.ResourceInstanceObjectFull)(nil))
	providerInst, _ := meta.ProviderInstance.Unmark()
	providerInstAddr, ok := providerInst.ValueOk()
	if !ok {
		return nil, diags.Append(fmt.Errorf("no provider instance known for %s during the apply; this is a bug in choudoufu's in-run estates port", addr))
	}
	schema, moreDiags := ops.plugins.ResourceTypeSchema(ctx, meta.Provider, addrs.DataResourceMode, meta.ResourceType)
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return nil, diags
	}
	state := &states.ResourceInstanceObjectFull{
		Value:                val,
		Status:               states.ObjectReady,
		ProviderInstanceAddr: providerInstAddr,
		ResourceType:         meta.ResourceType,
		SchemaVersion:        uint64(schema.Version),
	}
	src, err := states.EncodeResourceInstanceObjectFull(state, schema.Block.ImpliedType())
	if err != nil {
		return nil, diags.Append(fmt.Errorf("failed to encode the new state for %s: %w", addr, err))
	}
	diags = diags.Append(ops.workingState.SetResourceInstanceObjectFull(addr.CurrentObject(), src))
	return &exec.ResourceInstanceObject{Addr: addr.CurrentObject(), State: state}, diags
}

// forkInRunEstateOutputs reads each in-run estate module's outputs before
// the config oracle closes, and returns what puts them into the final
// state after Finish has cleared module outputs. The live write-back
// records a module's outputs as its estate's root outputs. An output not
// wholly known, or sensitive, is left out, as a root output is.
func (ops *execOperations) forkInRunEstateOutputs(ctx context.Context) func(*states.State) {
	mods := ops.inRunEstates.Modules()
	if len(mods) == 0 {
		return func(*states.State) {}
	}
	outputs := ops.forkOutputs(grapheval.ContextWithNewWorker(ctx))
	vals := make(map[string]cty.Value, len(mods))
	for _, m := range mods {
		vals[m.String()] = outputs(m)
	}
	return func(s *states.State) {
		for _, m := range mods {
			v := vals[m.String()]
			if v == cty.NilVal || !v.IsKnown() || v.IsNull() || !v.Type().IsObjectType() {
				continue
			}
			for name, ov := range v.AsValueMap() {
				unmarked, pvm := ov.UnmarkDeepWithPaths()
				if len(pvm) != 0 || !unmarked.IsWhollyKnown() {
					continue
				}
				s.EnsureModule(m).SetOutputValue(name, unmarked, false, "")
			}
		}
	}
}
