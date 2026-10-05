// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898. The per-resource-instance spans of the classic graph
// walk go through [tracing.StartDetail], so they count against the walk's
// span budget and fold into "Aggregate: <span name>" summaries grouped by
// resource type when they are over it.

// traceNameRefreshResourceInstance is the span name for refreshing one
// resource instance object during the plan walk, a child of the
// "Plan resource instance changes" span.
const traceNameRefreshResourceInstance = "Refresh resource instance"

// traceAttrResourceInstanceAction is the planned action for the instance.
const traceAttrResourceInstanceAction = traceattrs.AttrResourceInstanceAction

// resourceInstanceDetail is the detail span spec for a per-instance span:
// grouped by resource type, labelled by address.
func resourceInstanceDetail(name string, addr addrs.AbsResourceInstance) tracing.DetailSpec {
	spec := tracing.DetailSpec{
		Kind:  tracing.DetailResourceInstance,
		Name:  name,
		Group: addr.Resource.Resource.Type,
		Label: addr.String(),
	}
	spec.GroupAttrs = append(spec.GroupAttrs, traceattrs.OpenTofuResourceType(addr.Resource.Resource.Type))
	return spec
}

// traceResourceInstanceAction sets the action of the change recorded for the
// instance (planned, or being applied) on its span. The action is a name
// such as "Create"; nothing of the change's values goes on the span.
func traceResourceInstanceAction(span tracing.Span, evalCtx EvalContext, addr addrs.AbsResourceInstance, deposedKey states.DeposedKey) {
	if span == nil || !span.IsRecording() || evalCtx == nil {
		return
	}
	changes := evalCtx.Changes()
	if changes == nil {
		return
	}
	var gen states.Generation = states.CurrentGen
	if deposedKey != states.NotDeposed {
		gen = deposedKey
	}
	if c := changes.GetResourceInstanceChange(addr, gen); c != nil {
		span.SetAttributes(traceattrs.String(traceAttrResourceInstanceAction, c.Action.String()))
	}
}
