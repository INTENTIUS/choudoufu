// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"github.com/intentius/choudoufu/internal/addrs"
)

// ModulePrefixAttr is the name of the one evaluator symbol this fork adds to
// the language's "terraform"/"tofu" object, and it exists for exactly one
// reader: the tofu-address value internal/live/stamp writes into a resource
// declared inside a module call with more than one instance.
//
// The problem it answers is issue #378. A module call's several instances
// share exactly ONE *hclsyntax.Body for a resource's tags argument, so no
// literal string in that body can be the right tofu-address for all of them -
// module.container_definition:fluent-bit.aws_cloudwatch_log_group.this:0 and
// module.container_definition:al2023.aws_cloudwatch_log_group.this:0 differ
// in a segment the child module's own text cannot name. Every value that
// varies per instance of a for_each'd or count'd RESOURCE has an expression
// the language already provides (each.key, count.index) and stamp
// interpolates it; the module-instance segment had none, so stamp declined to
// write a marker at all and a plan's desired tag set for those instances
// carried no marker, which the ordinary tags diff renders as a DELETION of
// the marker live-import had genuinely written.
//
// So this is the missing expression: evaluated inside a module instance, it
// is that instance's own escaped module path, and stamp's template appends
// the resource's own escaped address to it.
//
//	tofu-address = "${tofu.marker_module_prefix}.aws_cloudwatch_log_group.this:${count.index}"
//
// It is deliberately NOT a general-purpose language addition. Two properties
// hold it to that:
//
//   - It is reserved. internal/live/lint's RuleReservedMarkerSymbol refuses a
//     configuration that names it anywhere outside the marker tags this fork
//     writes, so an operator can neither read it nor collide with it, and its
//     meaning stays this fork's to define.
//
//   - Where its value is not KNOWN it refuses rather than defaulting.
//     internal/configs' static evaluator has no notion of a module instance
//     at all unless a caller threaded one in ([configs.StaticEvaluator.
//     WithModuleInstance]), and a static evaluation that answered "" there
//     would produce ".aws_cloudwatch_log_group.this:0" - a silently WRONG
//     marker, which HANDOFF.md's safety rule forbids far more strongly than
//     it forbids a missing one. The same reasoning makes the root module a
//     refusal rather than the empty string.
const ModulePrefixAttr = "marker_module_prefix"

// ModulePrefixRef is [ModulePrefixAttr] as an operator would type it, for
// diagnostics. The "tofu" spelling rather than "terraform": both roots
// resolve to the same map (internal/lang/eval.go binds terraformAttrs to
// both), and this fork's own documents spell it tofu.
const ModulePrefixRef = "tofu." + ModulePrefixAttr

// ModulePrefix is [ModulePrefixAttr]'s value for one module instance: the
// instance's own module path, escaped by the same [EscapeAddress] every other
// address in the marker scheme goes through, so that concatenating it with an
// escaped resource address produces exactly the bytes EscapeAddress would
// have produced for the whole address at once.
//
// That concatenation property is what makes the symbol usable as a prefix at
// all, and it is not an accident of this function: [EscapeAddress] transforms
// only the text inside a "[...]" instance key and leaves every structural
// "." alone, so it distributes over the "." that joins a module path to the
// resource address beneath it. internal/live/stamp's
// TestModuleKeyedMarkerComposesLikeAWholeAddress holds it, checked against
// EscapeAddress over the whole address rather than against itself.
//
// ok is false for the root module, whose path is the empty string. A caller
// that gets false must refuse, not substitute: see [ModulePrefixAttr]'s doc
// for why an empty prefix is the one answer that must never be given.
func ModulePrefix(mod addrs.ModuleInstance) (string, bool) {
	if len(mod) == 0 {
		return "", false
	}
	return EscapeAddress(mod.String()), true
}
