// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The command layer's half of the field-owner boundary (GitHub issue
// #1191, ruled 2026-10-03; the engine half is
// discovery.CheckKubernetesFieldOwners). It sits beside the server-side dry
// run for the same reason that does: the plan is in hand, each change's
// After is the planned write with the estate's field manager already
// stamped into it, and the cluster client is the sweep's.
//
// A change is field-granular by its schema (substrate.FieldGranularShape),
// never by its type name. Where the write lands in the object is read off
// the schema's own attributes too: a labels map writes metadata.labels, an
// annotations map metadata.annotations, template_annotations the pod
// template's, a data map the object's data, an env block one container's
// env, a taint block the node's taints.

// collectKubernetesFieldOwners judges every planned create or update of a
// field-granular instance, per cluster, and returns the refusals and
// warnings discovery.CheckKubernetesFieldOwners and
// discovery.SameObjectFieldWrites raise. estate "" (a run with no estate
// name has stamped nothing) checks nothing.
func collectKubernetesFieldOwners(ctx context.Context, sweepers map[string]kubesweep.Sweeper, config *configs.Config, plan *plans.Plan, schemas *tofu.Schemas, estate string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if estate == "" || plan == nil || plan.Changes == nil || schemas == nil {
		return diags
	}
	byProvider := map[string][]discovery.FieldGranularWrite{}
	var keys []string
	for _, rc := range plan.Changes.Resources {
		if rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if rc.Action != plans.Create && rc.Action != plans.Update && rc.Action != plans.CreateThenDelete && rc.Action != plans.DeleteThenCreate {
			continue
		}
		schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
		if schema == nil {
			continue
		}
		if _, ok := substrate.FieldGranularShape(schema.Block); !ok {
			continue
		}
		w, ok := plannedFieldGranularWrite(rc, schema)
		if !ok {
			continue
		}
		w.Create = rc.Action == plans.Create
		key := providerCacheKey(rc.ProviderAddr)
		if _, seen := byProvider[key]; !seen {
			keys = append(keys, key)
		}
		byProvider[key] = append(byProvider[key], w)
	}
	sort.Strings(keys)
	for _, key := range keys {
		diags = diags.Append(discovery.SameObjectFieldWrites(config, estate, byProvider[key]))
		reader, _ := sweepers[key].(kubesweep.ObjectReader)
		if reader == nil {
			// No cluster client for this provider configuration: the sweep
			// has already said so, once.
			continue
		}
		diags = diags.Append(discovery.CheckKubernetesFieldOwners(ctx, reader, config, estate, byProvider[key]))
	}
	return diags
}

// plannedFieldGranularWrite reads one planned change's After back as the
// object it patches and the fields it writes. ok is false when the planned
// value does not say yet - an unknown name or key - which leaves the
// instance to the apply, where the API server answers.
func plannedFieldGranularWrite(rc *plans.ResourceInstanceChangeSrc, schema *providers.Schema) (discovery.FieldGranularWrite, bool) {
	change, err := rc.Decode(schema)
	if err != nil {
		return discovery.FieldGranularWrite{Addr: rc.Addr}, false
	}
	w, ok := discovery.FieldGranularWriteOf(rc.Addr.Resource.Resource.Type, schema.Block, change.After)
	w.Addr = rc.Addr
	return w, ok
}

// fieldGranularFixedKind is [kubesweep.FieldGranularFixedKind].
func fieldGranularFixedKind(typeName string) (apiVersion, kind string) {
	return kubesweep.FieldGranularFixedKind(typeName)
}

// envWrite is [discovery.FieldGranularEnvWrite].
func envWrite(after cty.Value, kind string) (kubesweep.FieldWrite, bool) {
	return discovery.FieldGranularEnvWrite(after, kind)
}

// taintWrite is [discovery.FieldGranularTaintWrite].
func taintWrite(after cty.Value) (kubesweep.FieldWrite, bool) {
	return discovery.FieldGranularTaintWrite(after)
}

// ctyString is a known string attribute of obj, or "".
func ctyString(obj cty.Value, name string) string {
	if obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(name) {
		return ""
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}
