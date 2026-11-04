// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Replacing an aws_lb_target_group that a listener or rule forwards to fails
// with ResourceInUse after the provider's own retry of the delete, because
// the old group is deleted before the listener can move to the new one.
// Reproduced on real AWS with provider 6.71.0 on 2026-11-04 (#2309, the
// tg-listener-16889 repro): changing port retried the delete for over 1m20s
// and then failed.
//
// The override is approach D in its mildest form: a warning at plan time,
// nothing refused and nothing rewritten. The provider call shows neither the
// listener nor the lifecycle setting, so the warning is narrowed to the one
// case where the outcome is certain from the call itself: a replacement that
// keeps the same fixed name. AWS target group names are unique per region
// and account, so that replacement cannot create first, with or without
// create_before_destroy, and the old group has to be deleted while whatever
// forwards to it still does. A target group with nothing forwarding to it
// is rare, which is what makes the warning worth its false positives.
//
// A replacement under name_prefix, under a generated name, or one that
// changes the name too can create first, so it gets no warning.
var _ = register(Override{
	Name:     "aws-lb-target-group-replace-in-use",
	Provider: awsProvider,
	Type:     "aws_lb_target_group",
	Approach: Refuse,
	SDK:      SDKv2,
	Upstream: "hashicorp/terraform-provider-aws#16889",
	Summary:  "Replacing a target group that keeps its name deletes it first, which fails with ResourceInUse while a listener or rule forwards to it",
	ProvenOn: "6.71.0",
	Requires: map[string]cty.Type{
		"name": cty.String,
	},
	Plan: warnFixedNameReplace,
})

// warnFixedNameReplace adds a warning to the provider's own plan when that
// plan replaces the target group and the configured name is the one it
// already has. The response is otherwise the provider's, unchanged.
func warnFixedNameReplace(ctx context.Context, inner providers.Interface, req providers.PlanResourceChangeRequest) (providers.PlanResourceChangeResponse, bool) {
	resp := inner.PlanResourceChange(ctx, req)
	if req.PriorState.IsNull() || req.Config.IsNull() || resp.Diagnostics.HasErrors() || len(resp.RequiresReplace) == 0 {
		return resp, false
	}
	name, ok := tgKnownString(req.Config, "name")
	if !ok {
		return resp, false
	}
	if prior, ok := tgKnownString(req.PriorState, "name"); !ok || prior != name {
		return resp, false
	}
	resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(tfdiags.Warning,
		"Target group must be deleted before it is replaced",
		fmt.Sprintf("This plan replaces the target group %q and keeps its name. Target group names are unique, so the new group cannot be created until the old one is deleted, and AWS refuses that delete with ResourceInUse while a listener or listener rule forwards to it. The apply then fails after the provider's retry, with the old group still in place. To replace it safely, use name_prefix instead of name and set lifecycle { create_before_destroy = true }, so the new group exists and the listener moves to it before the old one is deleted.", name)))
	return resp, true
}

// tgKnownString returns the string attribute attr of obj when it is known and
// not null.
func tgKnownString(obj cty.Value, attr string) (string, bool) {
	if !obj.IsKnown() || obj.IsNull() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(attr) {
		return "", false
	}
	v := obj.GetAttr(attr)
	if !v.IsKnown() || v.IsNull() || v.IsMarked() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}
