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
	"github.com/intentius/choudoufu/internal/instances"
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

// ArgumentScoped is [Argument] evaluated with one resource instance's own
// count.index/each.key/each.value bound through rep, for an instance of a
// count or for_each block whose whole collection is itself statically known
// - see [Count] and [ForEachElements], which is how a caller derives rep in
// the first place ([instances.RepetitionData]'s zero value, for a scalar
// resource, is a valid rep that simply leaves every one of those unbound).
//
// Every admission rule [Argument] enforces still applies unchanged; "count"
// and "each" are additionally allowed as traversal roots (see
// [FirstDisallowedScoped]) because rep, not the module's wider evaluator,
// answers them, and the evaluation itself runs through [Scoped] rather than
// [configs.StaticEvaluator.Evaluate] so that WithRepetitionData actually
// sees rep.
//
// This is a distinct function from [Argument] rather than that one widened
// with an optional rep, for the same reason [Scoped] is distinct from
// [Evaluate]: recover semantics are a caller-visible behaviour, and
// [Argument]'s own doc comment already explains why its callers are held to
// exactly the no-recover behaviour it has today.
func ArgumentScoped(ctx context.Context, mod *configs.Module, rc *configs.Resource, name string, rep instances.RepetitionData) (string, string) {
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

	if root, bad := FirstDisallowedScoped(attr.Expr); bad {
		return "", fmt.Sprintf(
			"its %s argument refers to %s, which is not known until the run is under way, so there is no configuration value to compare against",
			name, root)
	}

	if mod.StaticEvaluator == nil {
		return "", fmt.Sprintf("its %s argument could not be evaluated: the configuration carries no static evaluator", name)
	}
	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("%s.%s", rc.Addr(), name),
		DeclRange: attr.Range,
	}
	val, evalDiags, recovered := Scoped(ctx, mod.StaticEvaluator, attr.Expr, ident, rep, nil)
	if recovered != nil || evalDiags.HasErrors() {
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
