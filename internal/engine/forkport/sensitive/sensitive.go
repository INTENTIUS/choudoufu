// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package sensitive is a choudoufu fork port (#2410, of #2256): sensitive
// values on OpenTofu's new runtime (internal/engine).
//
// The new runtime's evaluator already marks what is sensitive: a
// `sensitive = true` variable or output, the sensitive() function, and
// everything derived from them carry marks.Sensitive through evaluation.
// What it does not do is carry those marks across the places where a value
// leaves evaluation, which the old runtime (internal/tofu) does:
//
//   - planning hands the provider the configuration unmarked and records the
//     planned change without its marks, so a sensitive argument plans, shows
//     and reaches downstream references as plain text. [Planned] puts back
//     the configuration's sensitive paths and the schema's sensitive
//     attributes on the planned value, as node_resource_abstract_instance.go
//     plan does, and [Prior] marks the prior object with the state's
//     sensitive paths and the schema's, as its refresh does. [Action] turns
//     a NoOp whose sensitivity changed into an Update, as the old runtime
//     does, so the stored sensitive paths follow the configuration.
//   - applying stored the provider's new object with no sensitive paths.
//     That is fixed outside this port, unconditionally, by
//     internal/engine/applying's storedSensitiveValue: a schema-sensitive
//     attribute reached state and live-mode records unmarked even where
//     this row never refused anything, so the fix cannot be switchable.
//   - planning writes a root output's value into the plan's prior state
//     still marked, which fails to serialize; [OutputValue] unmarks it and
//     keeps its sensitivity as the state's flag.
//   - a root output whose value is sensitive must be declared sensitive.
//     [UndeclaredRootOutput] is that check, which the old runtime makes in
//     node_output.go.
//
// Only marks.Sensitive is handled. The engine's own marks (pending change,
// drift, resource instance) are never written by this port, and a state or
// record never receives one.
//
// Upstream tracks the new runtime's mark gaps as opentofu/opentofu #4360.
// Every call site is commented "choudoufu fork port (#2410): remove when
// upstream #4360 lands". Removing the port is deleting this package, those
// call sites, the "sensitive" row's fork_port and switch in
// internal/live/newruntime/gaps.json, and the port tag on that row's
// detector in internal/live/newruntime/checks.go.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), every function returns its input unchanged and the
// gap table's preflight refuses sensitive variables, outputs and the
// sensitive() function again, under its "sensitive" row.
package sensitive

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "sensitive"

// Enabled reports whether the port is on.
func Enabled() bool { return forkport.Enabled(Name) }

// Planned returns planned marked sensitive wherever config is, and wherever
// schema declares an attribute sensitive. config is the evaluated
// configuration value, with its marks; planned is the provider's planned
// object, which the engine planned from the unmarked configuration. Under
// an ignore_changes path the configuration was replaced by the prior
// object, so there prior's sensitive marks count instead of config's, as
// on the old runtime; prior is the marked prior object ([Prior]), null or
// cty.NilVal when there is none, and an empty path in ignore is
// ignore_changes = all.
func Planned(planned, config, prior cty.Value, ignore []cty.Path, schema *configschema.Block) cty.Value {
	if !Enabled() || planned == cty.NilVal {
		return planned
	}
	hasPrior := prior != cty.NilVal && !prior.IsNull()
	var pvms []cty.PathValueMarks
	if config != cty.NilVal {
		_, configMarks := config.UnmarkDeepWithPaths()
		for _, pvm := range sensitiveOnly(configMarks) {
			if !hasPrior || !under(pvm.Path, ignore) {
				pvms = append(pvms, pvm)
			}
		}
	}
	if hasPrior && len(ignore) != 0 {
		_, priorMarks := prior.UnmarkDeepWithPaths()
		for _, pvm := range sensitiveOnly(priorMarks) {
			if under(pvm.Path, ignore) {
				pvms = append(pvms, pvm)
			}
		}
	}
	return mark(planned, pvms, schema)
}

// under reports whether path is one of prefixes or inside one of them.
func under(path cty.Path, prefixes []cty.Path) bool {
	for _, p := range prefixes {
		if len(path) >= len(p) && path[:len(p)].Equals(p) {
			return true
		}
	}
	return false
}

// Prior returns prior, a prior object decoded from state without marks,
// marked sensitive at the state's sensitive paths and wherever schema
// declares an attribute sensitive.
func Prior(prior cty.Value, statePaths []cty.Path, schema *configschema.Block) cty.Value {
	if !Enabled() || prior == cty.NilVal {
		return prior
	}
	pvms := make([]cty.PathValueMarks, 0, len(statePaths))
	for _, p := range statePaths {
		pvms = append(pvms, cty.PathValueMarks{Path: p, Marks: cty.NewValueMarks(marks.Sensitive)})
	}
	return mark(prior, pvms, schema)
}

// Action returns action, except that a NoOp whose planned value is
// sensitive at different paths than the prior object is an Update, so that
// applying it stores the new sensitive paths.
func Action(action plans.Action, before, after cty.Value) plans.Action {
	if !Enabled() || action != plans.NoOp || before == cty.NilVal || after == cty.NilVal {
		return action
	}
	_, b := before.UnmarkDeepWithPaths()
	_, a := after.UnmarkDeepWithPaths()
	if samePaths(sensitiveOnly(b), sensitiveOnly(a)) {
		return action
	}
	return plans.Update
}

// OutputValue returns a root output value ready to store in a state: with
// no marks, and sensitive when any part of it was. With the port off it
// returns v as it is and false.
func OutputValue(v cty.Value) (cty.Value, bool) {
	if !Enabled() {
		return v, false
	}
	sensitive := marks.Contains(v, marks.Sensitive)
	v, _ = v.UnmarkDeep()
	return v, sensitive
}

// UndeclaredRootOutput is the old runtime's error for a root module output
// whose value is sensitive but that is not declared sensitive. It returns
// nil when the output is in a child module, is declared sensitive, or its
// value is not sensitive, and with the port off.
func UndeclaredRootOutput(root, declaredSensitive bool, v cty.Value, subject *hcl.Range) *hcl.Diagnostic {
	if !Enabled() || !root || declaredSensitive || !marks.Contains(v, marks.Sensitive) {
		return nil
	}
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Output refers to sensitive values",
		Detail: `To reduce the risk of accidentally exporting sensitive data that was intended to be only internal, OpenTofu requires that any root module output containing sensitive data be explicitly marked as sensitive, to confirm your intent.

If you do intend to export this data, annotate the output value as sensitive by adding the following argument:
    sensitive = true`,
		Subject: subject,
	}
}

// mark adds the sensitive mark at each of pvms' paths and at every path
// schema declares sensitive, and no other mark.
func mark(v cty.Value, pvms []cty.PathValueMarks, schema *configschema.Block) cty.Value {
	if schema != nil && !v.IsNull() {
		// ValueMarks walks nested blocks, which it cannot do through the
		// engine's own marks (a pending change marks a whole object).
		unmarked, _ := v.UnmarkDeep()
		pvms = append(pvms, sensitiveOnly(schema.ValueMarks(unmarked, nil, nil))...)
	}
	if len(pvms) == 0 {
		return v
	}
	return v.MarkWithPaths(pvms)
}

// sensitiveOnly keeps, of each path's marks, only marks.Sensitive, and
// drops the paths left with none.
func sensitiveOnly(pvms []cty.PathValueMarks) []cty.PathValueMarks {
	var out []cty.PathValueMarks
	for _, pvm := range pvms {
		if _, ok := pvm.Marks[marks.Sensitive]; ok {
			out = append(out, cty.PathValueMarks{Path: pvm.Path, Marks: cty.NewValueMarks(marks.Sensitive)})
		}
	}
	return out
}

// samePaths reports whether a and b, each with only the sensitive mark,
// name the same set of paths.
func samePaths(a, b []cty.PathValueMarks) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x.Path.Equals(y.Path) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
