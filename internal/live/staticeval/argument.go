// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staticeval

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// argument.go is the select-by-path half: reach one named top-level
// argument inside a declared resource's own body and read it as
// configuration gives it.

// Argument reads one top-level argument of a declared resource as
// configuration gives it, through the module's static evaluator -
// constants, variables, locals and functions, the same subset identity
// resolution admits. The second return is empty on success and a reason on
// failure; a value this cannot read is never a wildcard, it disqualifies
// the instance from matching entirely.
//
// Deliberately NOT routed through [Evaluate]: neither copy this replaces
// (internal/live/discovery's staticArgumentValue, internal/live/foreign's
// staticString) had a recover, so adding one here would be a behaviour
// change smuggled in with a move. See the PR for issue #826 - the panic
// class evaluate.go's own comment describes can reach this call through a
// local, and closing that is its own change.
func Argument(ctx context.Context, mod *configs.Module, rc *configs.Resource, name string) (string, string) {
	content, _, hclDiags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if hclDiags.HasErrors() {
		return "", fmt.Sprintf("its %s argument could not be read from configuration", name)
	}
	attr, ok := content.Attributes[name]
	if !ok {
		return "", fmt.Sprintf("it sets no %s argument, and that is the argument a content match would have to be made on", name)
	}

	if root, bad := FirstDisallowed(attr.Expr); bad {
		return "", fmt.Sprintf(
			"its %s argument refers to %s, which is not known until the run is under way, so there is no configuration value to compare against",
			name, root)
	}

	if mod.StaticEvaluator == nil {
		return "", fmt.Sprintf("its %s argument could not be evaluated: the configuration carries no static evaluator", name)
	}
	val, evalDiags := mod.StaticEvaluator.Evaluate(ctx, attr.Expr, configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("%s.%s", rc.Addr(), name),
		DeclRange: attr.Range,
	})
	if evalDiags.HasErrors() {
		return "", fmt.Sprintf("its %s argument could not be evaluated from configuration alone", name)
	}
	if val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", fmt.Sprintf("its %s argument is not a plain known value", name)
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", fmt.Sprintf("its %s argument is not usable as a string", name)
	}
	if str.AsString() == "" {
		return "", fmt.Sprintf("its %s argument is empty, which matches nothing", name)
	}
	return str.AsString(), ""
}
