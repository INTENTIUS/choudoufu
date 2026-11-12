// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// choudoufu fork port (#2414): remove when upstream #4058 and #4740 land. The
// whole file is the apply engine's side of package datasources: upstream
// pull request #4740's DataRead (commit 4b689c442b), for the reads a plan
// deferred to the apply.

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/datasources"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/resources"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkReadData reads a data resource instance whose read the plan deferred,
// and saves the result to the working state. A data block inside a check
// block is read again here whatever the plan read, and its errors are
// warnings, as in the default runtime.
func (ops *execOperations) forkReadData(ctx context.Context, desired *eval.DesiredResourceInstance) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	ret, diags := ops.forkReadDataInner(ctx, desired)
	if desired.ForkCheckNested {
		diags = tfdiags.OverrideAll(diags, tfdiags.Warning, nil)
	}
	return ret, diags
}

func (ops *execOperations) forkReadDataInner(ctx context.Context, desired *eval.DesiredResourceInstance) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	ret := &exec.ResourceInstanceObject{
		Addr: desired.Addr.CurrentObject(),
	}
	metadata := exec.BuildResourceInstanceObjectMeta(desired.Addr.CurrentObject(), ops.configOracle.ResourceInstanceObjectMeta(ctx, desired.Addr.CurrentObject()), (*states.ResourceInstanceObjectFull)(nil))

	providerInst, _ := metadata.ProviderInstance.Unmark()
	providerAddr, ok := providerInst.ValueOk()
	if !ok {
		return ret, diags
	}
	log.Printf("[TRACE] apply phase: DataRead %s using %s", desired.Addr, providerAddr)
	providerClient, moreDiags := ops.configOracle.ProviderInstance(ctx, providerAddr)
	if providerClient == nil {
		moreDiags = moreDiags.Append(tfdiags.AttributeValue(
			tfdiags.Error,
			"Provider instance not available",
			fmt.Sprintf("Cannot apply %s because its associated provider instance %s cannot initialize.", desired.Addr, providerAddr),
			nil,
		))
	}
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return ret, diags
	}

	resourceType := resources.NewDataResourceType(metadata.Provider, metadata.ResourceType, providerClient)
	schema, schemaDiags := resourceType.LoadSchema(ctx)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return ret, diags
	}

	// The default runtime validates a deferred read's configuration once it
	// is known, just before reading.
	diags = diags.Append(resourceType.ValidateConfig(ctx, desired.ConfigVal))
	if diags.HasErrors() {
		return ret, diags
	}

	tracer := contextTracer(ctx)
	if cb := tracer.StartDataResourceInstanceRead; cb != nil {
		ctx = cb(ctx, desired.Addr)
	}
	if cb := tracer.EndDataResourceInstanceRead; cb != nil {
		defer func() { // closure to delay evaluating diags until we return
			resultVal := cty.DynamicVal
			if ret.State != nil {
				resultVal = ret.State.Value
			}
			cb(ctx, desired.Addr, resultVal, diags)
		}()
	}

	resp, readDiags := resourceType.Read(ctx, &resources.DataResourceReadRequest{
		ResourceAddress: desired.Addr,
		ConfigValue:     desired.ConfigVal,
	}, desired.Addr.CurrentObject())
	diags = diags.Append(readDiags)
	if resp == nil {
		return ret, diags
	}

	result := resp.ResultUnmarked
	if result == cty.NilVal {
		result = cty.NullVal(schema.Block.ImpliedType())
	}
	if result.IsNull() {
		return ret, diags
	}

	status := states.ObjectTainted
	if !diags.HasErrors() {
		status = states.ObjectReady
	}
	state := &states.ResourceInstanceObjectFull{
		Status:               status,
		Value:                result,
		ProviderInstanceAddr: providerAddr,
		ResourceType:         metadata.ResourceType,
		SchemaVersion:        uint64(schema.Version),
		Dependencies:         slices.Collect(desired.RequiredResourceInstances.All()),
	}
	stateSrc, err := states.EncodeResourceInstanceObjectFull(state, schema.Block.ImpliedType())
	if err != nil {
		return ret, diags.Append(fmt.Errorf("failed to encode the new state for %s: %w", desired.Addr, err))
	}
	stateSrc.Value.SensitivePaths = resp.SensitivePaths
	diags = diags.Append(ops.workingState.SetResourceInstanceObjectFull(desired.Addr.CurrentObject(), stateSrc))
	ret.State = state
	return ret, diags
}

// forkDropDataAfterDestroy removes every data resource from the state a
// destroy leaves, as the default runtime does: the destroy plan kept them
// in its prior state only for the apply to evaluate references to them.
func forkDropDataAfterDestroy(s *states.State) {
	if !datasources.Enabled() {
		return
	}
	for _, mod := range s.Modules {
		for _, rs := range mod.Resources {
			if rs.Addr.Resource.Mode == addrs.DataResourceMode {
				mod.RemoveResource(rs.Addr.Resource)
			}
		}
	}
}
